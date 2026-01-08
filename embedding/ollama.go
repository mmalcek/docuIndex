package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultOllamaEndpoint = "http://localhost:11434"

// ollamaProvider implements embedding using Ollama
type ollamaProvider struct {
	baseProvider
	client   *http.Client
	endpoint string
	model    string
}

// Ollama embedding request
type ollamaEmbeddingRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // Can be string or []string
}

// Ollama embedding response
type ollamaEmbeddingResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error,omitempty"`
}

// Ollama legacy single embedding response (older API)
type ollamaLegacyResponse struct {
	Embedding []float32 `json:"embedding"`
	Error     string    `json:"error,omitempty"`
}

func newOllamaProvider(cfg Config) (*ollamaProvider, error) {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultOllamaEndpoint
	}
	endpoint = strings.TrimSuffix(endpoint, "/")

	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text" // Default embedding model
	}

	p := &ollamaProvider{
		baseProvider: baseProvider{
			config:    cfg,
			dimension: cfg.Dimension,
		},
		client:   &http.Client{Timeout: cfg.Timeout},
		endpoint: endpoint,
		model:    model,
	}

	return p, nil
}

// Name returns the provider identifier
func (p *ollamaProvider) Name() string {
	return fmt.Sprintf("ollama/%s", p.model)
}

// Embed generates embeddings for the given texts
func (p *ollamaProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
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
func (p *ollamaProvider) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	return p.baseProvider.embedSingle(ctx, text, p.Embed)
}

func (p *ollamaProvider) doRequest(ctx context.Context, texts []string) ([][]float32, error) {
	// Build request body
	var input any
	if len(texts) == 1 {
		input = texts[0]
	} else {
		input = texts
	}

	reqBody := ollamaEmbeddingRequest{
		Model: p.model,
		Input: input,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/embed", p.endpoint)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: status %d: %s", ErrAPIError, resp.StatusCode, string(respBody))
	}

	// Try new API format first
	var embResp ollamaEmbeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if embResp.Error != "" {
		return nil, fmt.Errorf("%w: %s", ErrAPIError, embResp.Error)
	}

	// Handle the response
	if len(embResp.Embeddings) > 0 {
		// New API format returns array of embeddings
		return embResp.Embeddings, nil
	}

	// Try legacy single embedding response
	var legacyResp ollamaLegacyResponse
	if err := json.Unmarshal(respBody, &legacyResp); err == nil && len(legacyResp.Embedding) > 0 {
		// Legacy format - wrap single embedding
		return [][]float32{legacyResp.Embedding}, nil
	}

	return nil, fmt.Errorf("%w: no embeddings in response", ErrInvalidResponse)
}

// EmbedWithPrefix embeds texts with a prefix (useful for models that support task prefixes)
func (p *ollamaProvider) EmbedWithPrefix(ctx context.Context, texts []string, prefix string) ([][]float32, error) {
	if prefix == "" {
		return p.Embed(ctx, texts)
	}

	prefixedTexts := make([]string, len(texts))
	for i, t := range texts {
		prefixedTexts[i] = prefix + t
	}
	return p.Embed(ctx, prefixedTexts)
}
