package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// azureProvider implements embedding using Azure OpenAI or Azure AI
type azureProvider struct {
	baseProvider
	client          *http.Client
	endpoint        string
	apiKey          string
	tokenCredential TokenCredential
	model           string
	apiVersion      string

	// Token cache for token-based auth
	tokenMu     sync.RWMutex
	cachedToken string
	tokenExpiry time.Time
}

// Azure OpenAI embedding request
type azureEmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model,omitempty"`
}

// Azure OpenAI embedding response
type azureEmbeddingResponse struct {
	Data  []azureEmbeddingData `json:"data"`
	Usage azureUsage           `json:"usage,omitempty"`
	Error *azureError          `json:"error,omitempty"`
}

type azureEmbeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type azureUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type azureError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func newAzureProvider(cfg Config) (*azureProvider, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("azure endpoint is required")
	}
	if cfg.APIKey == "" && cfg.TokenCredential == nil {
		return nil, fmt.Errorf("azure API key or token credential is required")
	}

	apiVersion := cfg.APIVersion
	if apiVersion == "" {
		apiVersion = "2024-10-21" // Default to latest stable GA API
	}

	p := &azureProvider{
		baseProvider: baseProvider{
			config:    cfg,
			dimension: cfg.Dimension,
		},
		client:          &http.Client{Timeout: cfg.Timeout},
		endpoint:        strings.TrimSuffix(cfg.Endpoint, "/"),
		apiKey:          cfg.APIKey,
		tokenCredential: cfg.TokenCredential,
		model:           cfg.Model,
		apiVersion:      apiVersion,
	}

	return p, nil
}

// Name returns the provider identifier
func (p *azureProvider) Name() string {
	return fmt.Sprintf("azure/%s", p.model)
}

// Embed generates embeddings for the given texts
func (p *azureProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
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
func (p *azureProvider) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	return p.baseProvider.embedSingle(ctx, text, p.Embed)
}

// getToken returns a valid access token, refreshing if necessary
func (p *azureProvider) getToken(ctx context.Context) (string, error) {
	if p.tokenCredential == nil {
		return "", fmt.Errorf("token credential not configured")
	}

	// Check if we have a valid cached token (with 5 min buffer)
	p.tokenMu.RLock()
	if p.cachedToken != "" && time.Now().Add(5*time.Minute).Before(p.tokenExpiry) {
		token := p.cachedToken
		p.tokenMu.RUnlock()
		return token, nil
	}
	p.tokenMu.RUnlock()

	// Need to refresh token
	p.tokenMu.Lock()
	defer p.tokenMu.Unlock()

	// Double-check after acquiring write lock
	if p.cachedToken != "" && time.Now().Add(5*time.Minute).Before(p.tokenExpiry) {
		return p.cachedToken, nil
	}

	// Get new token - Azure Cognitive Services scope
	accessToken, err := p.tokenCredential.GetToken(ctx, TokenRequestOptions{
		Scopes: []string{"https://cognitiveservices.azure.com/.default"},
	})
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}

	p.cachedToken = accessToken.Token
	p.tokenExpiry = accessToken.ExpiresOn
	return p.cachedToken, nil
}

func (p *azureProvider) doRequest(ctx context.Context, texts []string) ([][]float32, error) {
	// Build request body
	reqBody := azureEmbeddingRequest{
		Input: texts,
	}

	if p.model != "" {
		reqBody.Model = p.model
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Build URL - Azure OpenAI format
	// Supports both v1 API and legacy deployment-based format
	url := p.endpoint
	if !strings.Contains(url, "/embeddings") {
		if p.apiVersion == "v1" {
			// New v1 API format (GA, recommended)
			url = fmt.Sprintf("%s/openai/v1/embeddings", url)
		} else if p.model != "" && !strings.Contains(url, "/deployments/") {
			// Legacy deployment-based format
			url = fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s", url, p.model, p.apiVersion)
		} else {
			url = fmt.Sprintf("%s/embeddings", url)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Use token credential if available, otherwise API key
	if p.tokenCredential != nil {
		token, err := p.getToken(ctx)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("api-key", p.apiKey)
	}

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

	var embResp azureEmbeddingResponse
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
