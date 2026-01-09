package embedding

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Common errors
var (
	ErrProviderNotConfigured = errors.New("embedding provider not configured")
	ErrEmptyInput            = errors.New("empty input text")
	ErrAPIError              = errors.New("embedding API error")
	ErrRateLimited           = errors.New("rate limited by embedding provider")
	ErrInvalidResponse       = errors.New("invalid response from embedding provider")
)

// Provider defines the interface for embedding providers
type Provider interface {
	// Embed generates embeddings for the given texts
	// Returns a slice of vectors, one for each input text
	Embed(ctx context.Context, texts []string) ([][]float32, error)

	// EmbedSingle generates an embedding for a single text
	EmbedSingle(ctx context.Context, text string) ([]float32, error)

	// Dimension returns the dimensionality of the embedding vectors
	Dimension() int

	// Name returns the provider and model identifier
	Name() string

	// MaxBatchSize returns the maximum number of texts per request
	MaxBatchSize() int
}

// TokenCredential interface for Azure token-based authentication
// Compatible with github.com/Azure/azure-sdk-for-go/sdk/azcore.TokenCredential
type TokenCredential interface {
	GetToken(ctx context.Context, options TokenRequestOptions) (AccessToken, error)
}

// TokenRequestOptions for token requests
type TokenRequestOptions struct {
	Scopes []string
}

// AccessToken represents an Azure access token
type AccessToken struct {
	Token     string
	ExpiresOn time.Time
}

// Config holds configuration for an embedding provider
type Config struct {
	// Provider type: "azure", "openai", "ollama"
	Provider string

	// Model or deployment name
	Model string

	// API endpoint (required for Azure and Ollama)
	Endpoint string

	// API key (not required for Ollama, optional for Azure if TokenCredential is provided)
	APIKey string

	// TokenCredential for Azure token-based authentication (alternative to APIKey)
	// If provided, uses Bearer token authentication instead of api-key header
	TokenCredential TokenCredential

	// Vector dimension (auto-detected if 0)
	Dimension int

	// Maximum texts per batch request
	BatchSize int

	// APIVersion for Azure OpenAI (e.g., "v1", "2024-10-21"). Default: "2024-10-21"
	APIVersion string

	// Request timeout
	Timeout time.Duration

	// Maximum retries on failure
	MaxRetries int

	// Retry delay (exponential backoff base)
	RetryDelay time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		BatchSize:  100,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		RetryDelay: time.Second,
	}
}

// NewProvider creates a new embedding provider based on config
func NewProvider(cfg Config) (Provider, error) {
	// Apply defaults
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = time.Second
	}

	switch cfg.Provider {
	case "azure":
		return newAzureProvider(cfg)
	case "openai":
		return newOpenAIProvider(cfg)
	case "ollama":
		return newOllamaProvider(cfg)
	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.Provider)
	}
}

// baseProvider provides common functionality for all providers
type baseProvider struct {
	config    Config
	dimension int
}

func (p *baseProvider) Dimension() int {
	return p.dimension
}

func (p *baseProvider) MaxBatchSize() int {
	return p.config.BatchSize
}

// EmbedSingle implements single text embedding using batch method
func (p *baseProvider) embedSingle(ctx context.Context, text string, batchEmbed func(context.Context, []string) ([][]float32, error)) ([]float32, error) {
	if text == "" {
		return nil, ErrEmptyInput
	}

	vectors, err := batchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(vectors) == 0 {
		return nil, ErrInvalidResponse
	}

	return vectors[0], nil
}

// retryWithBackoff executes a function with exponential backoff retry
func retryWithBackoff(ctx context.Context, maxRetries int, baseDelay time.Duration, fn func() error) error {
	var lastErr error
	delay := baseDelay

	for i := 0; i <= maxRetries; i++ {
		if err := fn(); err != nil {
			lastErr = err

			// Check if context is cancelled
			if ctx.Err() != nil {
				return ctx.Err()
			}

			// Don't retry on certain errors
			if errors.Is(err, ErrEmptyInput) || errors.Is(err, ErrInvalidResponse) {
				return err
			}

			// Wait before retry (with exponential backoff)
			if i < maxRetries {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(delay):
					delay *= 2 // Exponential backoff
				}
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// ChunkTexts splits texts into batches respecting max batch size
func ChunkTexts(texts []string, batchSize int) [][]string {
	if batchSize <= 0 {
		batchSize = 100
	}

	var batches [][]string
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batches = append(batches, texts[i:end])
	}
	return batches
}

// EmbedBatched embeds texts in batches, handling chunking automatically
func EmbedBatched(ctx context.Context, provider Provider, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	batches := ChunkTexts(texts, provider.MaxBatchSize())
	var allVectors [][]float32

	for _, batch := range batches {
		vectors, err := provider.Embed(ctx, batch)
		if err != nil {
			return nil, err
		}
		allVectors = append(allVectors, vectors...)
	}

	return allVectors, nil
}
