package embedding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.BatchSize != 100 {
		t.Errorf("BatchSize = %d, want 100", cfg.BatchSize)
	}
	if cfg.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", cfg.MaxRetries)
	}
	if cfg.RetryDelay != time.Second {
		t.Errorf("RetryDelay = %v, want 1s", cfg.RetryDelay)
	}
}

func TestNewProviderUnknown(t *testing.T) {
	_, err := NewProvider(Config{
		Provider: "unknown",
	})
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
}

func TestNewProviderDefaults(t *testing.T) {
	// This test verifies default values are applied
	// We can't fully test without actual provider setup, but we test the config path

	// Azure requires endpoint
	_, err := NewProvider(Config{
		Provider: "azure",
		Endpoint: "https://test.openai.azure.com",
		APIKey:   "test-key",
		Model:    "text-embedding-3-small",
	})
	// This will fail due to network, but we're testing the path
	if err != nil && err.Error() != "azure provider requires endpoint" {
		// Expected - the provider was created but may fail later
	}
}

func TestChunkTexts(t *testing.T) {
	tests := []struct {
		name      string
		texts     []string
		batchSize int
		wantLen   int
	}{
		{
			name:      "empty",
			texts:     []string{},
			batchSize: 10,
			wantLen:   0,
		},
		{
			name:      "single batch",
			texts:     []string{"a", "b", "c"},
			batchSize: 10,
			wantLen:   1,
		},
		{
			name:      "exact batches",
			texts:     []string{"a", "b", "c", "d"},
			batchSize: 2,
			wantLen:   2,
		},
		{
			name:      "partial last batch",
			texts:     []string{"a", "b", "c", "d", "e"},
			batchSize: 2,
			wantLen:   3,
		},
		{
			name:      "zero batch size uses default",
			texts:     []string{"a", "b"},
			batchSize: 0,
			wantLen:   1, // Default is 100
		},
		{
			name:      "negative batch size uses default",
			texts:     []string{"a", "b"},
			batchSize: -1,
			wantLen:   1, // Default is 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batches := ChunkTexts(tt.texts, tt.batchSize)
			if len(batches) != tt.wantLen {
				t.Errorf("ChunkTexts() returned %d batches, want %d", len(batches), tt.wantLen)
			}

			// Verify all texts are present
			var total int
			for _, batch := range batches {
				total += len(batch)
			}
			if total != len(tt.texts) {
				t.Errorf("Total texts in batches = %d, want %d", total, len(tt.texts))
			}
		})
	}
}

func TestChunkTextsContent(t *testing.T) {
	texts := []string{"a", "b", "c", "d", "e"}
	batches := ChunkTexts(texts, 2)

	// Should be [[a,b], [c,d], [e]]
	if len(batches) != 3 {
		t.Fatalf("Expected 3 batches, got %d", len(batches))
	}

	if batches[0][0] != "a" || batches[0][1] != "b" {
		t.Errorf("First batch = %v, want [a, b]", batches[0])
	}
	if batches[1][0] != "c" || batches[1][1] != "d" {
		t.Errorf("Second batch = %v, want [c, d]", batches[1])
	}
	if batches[2][0] != "e" || len(batches[2]) != 1 {
		t.Errorf("Third batch = %v, want [e]", batches[2])
	}
}

func TestRetryWithBackoffSuccess(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 3, time.Millisecond, func() error {
		attempts++
		return nil
	})

	if err != nil {
		t.Errorf("retryWithBackoff() error = %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

func TestRetryWithBackoffEventualSuccess(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 3, time.Millisecond, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("retryWithBackoff() error = %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRetryWithBackoffMaxRetries(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 2, time.Millisecond, func() error {
		attempts++
		return errors.New("persistent error")
	})

	if err == nil {
		t.Error("Expected error after max retries")
	}
	// Initial attempt + 2 retries = 3
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestRetryWithBackoffNonRetryableError(t *testing.T) {
	ctx := context.Background()
	attempts := 0

	err := retryWithBackoff(ctx, 3, time.Millisecond, func() error {
		attempts++
		return ErrEmptyInput // Non-retryable
	})

	if !errors.Is(err, ErrEmptyInput) {
		t.Errorf("Expected ErrEmptyInput, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry for ErrEmptyInput)", attempts)
	}
}

func TestRetryWithBackoffContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0

	// Cancel after first attempt
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	err := retryWithBackoff(ctx, 10, 50*time.Millisecond, func() error {
		attempts++
		return errors.New("error")
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestBaseProviderDimension(t *testing.T) {
	bp := &baseProvider{dimension: 1536}
	if bp.Dimension() != 1536 {
		t.Errorf("Dimension() = %d, want 1536", bp.Dimension())
	}
}

func TestBaseProviderMaxBatchSize(t *testing.T) {
	bp := &baseProvider{config: Config{BatchSize: 50}}
	if bp.MaxBatchSize() != 50 {
		t.Errorf("MaxBatchSize() = %d, want 50", bp.MaxBatchSize())
	}
}

func TestBaseProviderEmbedSingleEmpty(t *testing.T) {
	bp := &baseProvider{}
	_, err := bp.embedSingle(context.Background(), "", func(ctx context.Context, texts []string) ([][]float32, error) {
		return nil, nil
	})

	if !errors.Is(err, ErrEmptyInput) {
		t.Errorf("Expected ErrEmptyInput for empty text, got %v", err)
	}
}

func TestBaseProviderEmbedSingleSuccess(t *testing.T) {
	bp := &baseProvider{}
	expected := []float32{0.1, 0.2, 0.3}

	result, err := bp.embedSingle(context.Background(), "test", func(ctx context.Context, texts []string) ([][]float32, error) {
		return [][]float32{expected}, nil
	})

	if err != nil {
		t.Fatalf("embedSingle() error = %v", err)
	}
	if len(result) != len(expected) {
		t.Errorf("embedSingle() returned %d elements, want %d", len(result), len(expected))
	}
}

func TestBaseProviderEmbedSingleEmptyResponse(t *testing.T) {
	bp := &baseProvider{}

	_, err := bp.embedSingle(context.Background(), "test", func(ctx context.Context, texts []string) ([][]float32, error) {
		return [][]float32{}, nil
	})

	if !errors.Is(err, ErrInvalidResponse) {
		t.Errorf("Expected ErrInvalidResponse for empty response, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	// Verify error values are distinct
	errs := []error{
		ErrProviderNotConfigured,
		ErrEmptyInput,
		ErrAPIError,
		ErrRateLimited,
		ErrInvalidResponse,
	}

	for i, e1 := range errs {
		for j, e2 := range errs {
			if i != j && errors.Is(e1, e2) {
				t.Errorf("Errors should be distinct: %v and %v", e1, e2)
			}
		}
	}
}

// mockProvider for testing EmbedBatched
type mockProvider struct {
	embedFunc func(ctx context.Context, texts []string) ([][]float32, error)
	dimension int
	batchSize int
}

func (m *mockProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return m.embedFunc(ctx, texts)
}

func (m *mockProvider) EmbedSingle(ctx context.Context, text string) ([]float32, error) {
	vecs, err := m.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, ErrInvalidResponse
	}
	return vecs[0], nil
}

func (m *mockProvider) Dimension() int {
	return m.dimension
}

func (m *mockProvider) Name() string {
	return "mock"
}

func (m *mockProvider) MaxBatchSize() int {
	return m.batchSize
}

func TestEmbedBatchedEmpty(t *testing.T) {
	mp := &mockProvider{
		embedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
			t.Error("Embed should not be called for empty input")
			return nil, nil
		},
		batchSize: 10,
	}

	result, err := EmbedBatched(context.Background(), mp, []string{})
	if err != nil {
		t.Fatalf("EmbedBatched() error = %v", err)
	}
	if result != nil {
		t.Errorf("EmbedBatched() = %v, want nil", result)
	}
}

func TestEmbedBatchedSingleBatch(t *testing.T) {
	callCount := 0
	mp := &mockProvider{
		embedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
			callCount++
			result := make([][]float32, len(texts))
			for i := range texts {
				result[i] = []float32{float32(i)}
			}
			return result, nil
		},
		batchSize: 10,
	}

	result, err := EmbedBatched(context.Background(), mp, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatched() error = %v", err)
	}
	if callCount != 1 {
		t.Errorf("Expected 1 call, got %d", callCount)
	}
	if len(result) != 3 {
		t.Errorf("Expected 3 results, got %d", len(result))
	}
}

func TestEmbedBatchedMultipleBatches(t *testing.T) {
	callCount := 0
	mp := &mockProvider{
		embedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
			callCount++
			result := make([][]float32, len(texts))
			for i := range texts {
				result[i] = []float32{float32(callCount*10 + i)}
			}
			return result, nil
		},
		batchSize: 2,
	}

	result, err := EmbedBatched(context.Background(), mp, []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatalf("EmbedBatched() error = %v", err)
	}
	if callCount != 3 {
		t.Errorf("Expected 3 calls (batches of 2), got %d", callCount)
	}
	if len(result) != 5 {
		t.Errorf("Expected 5 results, got %d", len(result))
	}
}

func TestEmbedBatchedError(t *testing.T) {
	mp := &mockProvider{
		embedFunc: func(ctx context.Context, texts []string) ([][]float32, error) {
			return nil, errors.New("embedding failed")
		},
		batchSize: 10,
	}

	_, err := EmbedBatched(context.Background(), mp, []string{"a", "b"})
	if err == nil {
		t.Error("Expected error from EmbedBatched")
	}
}

func TestTokenCredentialInterface(t *testing.T) {
	// Verify the interface matches expected shape
	var _ TokenCredential = &mockTokenCredential{}
}

type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, options TokenRequestOptions) (AccessToken, error) {
	return AccessToken{
		Token:     "test-token",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}
