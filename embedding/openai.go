package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const openAIEndpoint = "https://api.openai.com/v1/embeddings"

// openAIProvider implements embedding using OpenAI API
type openAIProvider struct {
	baseProvider
	client *http.Client
	apiKey string
	model  string
}

// OpenAI embedding request
type openAIEmbeddingRequest struct {
	Input          interface{} `json:"input"`
	Model          string      `json:"model"`
	EncodingFormat string      `json:"encoding_format,omitempty"`
}

// OpenAI embedding response
type openAIEmbeddingResponse struct {
	Object string                  `json:"object"`
	Data   []openAIEmbeddingData   `json:"data"`
	Model  string                  `json:"model"`
	Usage  openAIUsage             `json:"usage"`
	Error  *openAIEmbeddingError   `json:"error,omitempty"`
}

type openAIEmbeddingData struct {
	Object    string    `json:"object"`
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type openAIUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type openAIEmbeddingError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

func newOpenAIProvider(cfg Config) (*openAIProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai API key is required")
	}

	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small" // Default model
	}

	p := &openAIProvider{
		baseProvider: baseProvider{
			config:    cfg,
			dimension: cfg.Dimension,
		},
		client: &http.Client{Timeout: cfg.Timeout},
		apiKey: cfg.APIKey,
		model:  model,
	}

	// Set default dimensions based on model
	if p.dimension == 0 {
		switch model {
		case "text-embedding-3-small":
			p.dimension = 1536
		case "text-embedding-3-large":
			p.dimension = 3072
		case "text-embedding-ada-002":
			p.dimension = 1536
		}
	}

	return p, nil
}

// Name returns the provider identifier
func (p *openAIProvider) Name() string {
	return fmt.Sprintf("openai/%s", p.model)
}

// Embed generates embeddings for the given texts
func (p *openAIProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	var result [][]float32

	err := retryWithBackoff(ctx, p.config.MaxRetries, p.config.RetryDelay, func() error {
		vectors, err := p.doRequest(ctx, texts)
		if err != nil {
			return err
		}
		result = vectors
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Auto-detect dimension from first response
	if p.dimension == 0 && len(result) > 0 && len(result[0]) > 0 {
		p.dimension = len(result[0])
	}

	return result, nil
}

// EmbedSingle generates an embedding for a single text
func (p *openAIProvider) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	return p.baseProvider.embedSingle(ctx, text, p.Embed)
}

func (p *openAIProvider) doRequest(ctx context.Context, texts []string) ([][]float32, error) {
	// Build request body
	var input interface{}
	if len(texts) == 1 {
		input = texts[0]
	} else {
		input = texts
	}

	reqBody := openAIEmbeddingRequest{
		Input: input,
		Model: p.model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openAIEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 429 {
		return nil, ErrRateLimited
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrAPIError, resp.StatusCode, string(respBody))
	}

	var embResp openAIEmbeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if embResp.Error != nil {
		return nil, fmt.Errorf("%w: %s", ErrAPIError, embResp.Error.Message)
	}

	// Extract vectors in correct order
	vectors := make([][]float32, len(texts))
	for _, data := range embResp.Data {
		if data.Index < len(vectors) {
			vectors[data.Index] = data.Embedding
		}
	}

	// Verify all vectors were received
	for i, v := range vectors {
		if v == nil {
			return nil, fmt.Errorf("%w: missing embedding for index %d", ErrInvalidResponse, i)
		}
	}

	return vectors, nil
}
