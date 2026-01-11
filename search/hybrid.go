package search

import (
	"context"
	"sync"
	"time"
)

// SearchMode defines the type of search
type SearchMode string

const (
	SearchModeKeyword  SearchMode = "keyword"
	SearchModeSemantic SearchMode = "semantic"
	SearchModeHybrid   SearchMode = "hybrid"
)

// HybridSearcher combines keyword and vector search
type HybridSearcher struct {
	// KeywordSearch performs BM25 search
	KeywordSearch func(query string, limit int) ([]SearchResult, error)

	// VectorSearch performs vector similarity search
	// The ef parameter overrides HNSW efSearch (0 = use default)
	VectorSearch func(ctx context.Context, query string, limit int, ef int) ([]VectorSearchResult, error)

	// GetBlockContent retrieves content for a block
	GetBlockContent func(docID, blockID string) (content, snippet string, page int, section string, err error)

	// GetDocumentName retrieves document name
	GetDocumentName func(docID string) string

	// Fusion configuration
	FusionConfig *FusionConfig
}

// HybridSearchOptions configures hybrid search
type HybridSearchOptions struct {
	Mode          SearchMode
	MaxResults    int
	MinScore      float64
	VectorWeight  float64
	KeywordWeight float64
	Timeout       time.Duration
	EfSearch      int // Override HNSW efSearch (0 = use default)
}

// DefaultHybridSearchOptions returns default options
func DefaultHybridSearchOptions() *HybridSearchOptions {
	return &HybridSearchOptions{
		Mode:          SearchModeHybrid,
		MaxResults:    20,
		MinScore:      0.0,
		VectorWeight:  0.5,
		KeywordWeight: 0.5,
		Timeout:       30 * time.Second,
	}
}

// HybridSearchResults contains results with metadata
type HybridSearchResults struct {
	Query           string
	Mode            SearchMode
	TotalHits       int
	Results         []FusedResult
	SearchTime      time.Duration
	KeywordTime     time.Duration
	VectorTime      time.Duration
	KeywordResults  int
	VectorResults   int
	FilteredByScore int
}

// Search performs hybrid search
func (h *HybridSearcher) Search(ctx context.Context, query string, opts *HybridSearchOptions) (*HybridSearchResults, error) {
	if opts == nil {
		opts = DefaultHybridSearchOptions()
	}

	start := time.Now()
	results := &HybridSearchResults{
		Query: query,
		Mode:  opts.Mode,
	}

	// Handle context timeout
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	switch opts.Mode {
	case SearchModeKeyword:
		return h.keywordOnlySearch(ctx, query, opts, results, start)
	case SearchModeSemantic:
		return h.vectorOnlySearch(ctx, query, opts, results, start)
	case SearchModeHybrid:
		return h.hybridSearch(ctx, query, opts, results, start)
	default:
		return h.hybridSearch(ctx, query, opts, results, start)
	}
}

// keywordOnlySearch performs BM25 search only
func (h *HybridSearcher) keywordOnlySearch(ctx context.Context, query string, opts *HybridSearchOptions, results *HybridSearchResults, start time.Time) (*HybridSearchResults, error) {
	keywordStart := time.Now()
	keywordResults, err := h.KeywordSearch(query, opts.MaxResults)
	results.KeywordTime = time.Since(keywordStart)

	if err != nil {
		return nil, err
	}

	results.KeywordResults = len(keywordResults)
	fused := KeywordToFusedResults(keywordResults)

	// Set fused score = keyword score
	for i := range fused {
		fused[i].FusedScore = fused[i].KeywordScore
	}

	if opts.MinScore > 0 {
		beforeFilter := len(fused)
		fused = FilterByFusedScore(fused, opts.MinScore)
		results.FilteredByScore = beforeFilter - len(fused)
	}

	results.Results = fused
	results.TotalHits = len(fused)
	results.SearchTime = time.Since(start)

	return results, nil
}

// vectorOnlySearch performs vector search only
func (h *HybridSearcher) vectorOnlySearch(ctx context.Context, query string, opts *HybridSearchOptions, results *HybridSearchResults, start time.Time) (*HybridSearchResults, error) {
	if h.VectorSearch == nil {
		// Fall back to keyword search if vector search not available
		return h.keywordOnlySearch(ctx, query, opts, results, start)
	}

	vectorStart := time.Now()
	vectorResults, err := h.VectorSearch(ctx, query, opts.MaxResults, opts.EfSearch)
	results.VectorTime = time.Since(vectorStart)

	if err != nil {
		return nil, err
	}

	results.VectorResults = len(vectorResults)
	fused := VectorToFusedResults(vectorResults)

	// Enrich with content
	for i := range fused {
		if h.GetBlockContent != nil {
			content, snippet, page, section, err := h.GetBlockContent(fused[i].DocumentID, fused[i].BlockID)
			if err == nil {
				fused[i].Content = content
				fused[i].Snippet = snippet
				fused[i].Page = page
				fused[i].Section = section
			}
		}
		if h.GetDocumentName != nil {
			fused[i].DocumentName = h.GetDocumentName(fused[i].DocumentID)
		}
		fused[i].FusedScore = fused[i].VectorScore
	}

	if opts.MinScore > 0 {
		beforeFilter := len(fused)
		fused = FilterByFusedScore(fused, opts.MinScore)
		results.FilteredByScore = beforeFilter - len(fused)
	}

	results.Results = fused
	results.TotalHits = len(fused)
	results.SearchTime = time.Since(start)

	return results, nil
}

// hybridSearch performs combined BM25 + vector search
func (h *HybridSearcher) hybridSearch(ctx context.Context, query string, opts *HybridSearchOptions, results *HybridSearchResults, start time.Time) (*HybridSearchResults, error) {
	// If vector search not available, fall back to keyword only
	if h.VectorSearch == nil {
		return h.keywordOnlySearch(ctx, query, opts, results, start)
	}

	var wg sync.WaitGroup
	var keywordResults []SearchResult
	var vectorResults []VectorSearchResult
	var keywordErr, vectorErr error

	// Run keyword and vector search in parallel
	wg.Add(2)

	// Keyword search
	go func() {
		defer wg.Done()
		keywordStart := time.Now()
		keywordResults, keywordErr = h.KeywordSearch(query, opts.MaxResults*2) // Get more for fusion
		results.KeywordTime = time.Since(keywordStart)
	}()

	// Vector search
	go func() {
		defer wg.Done()
		vectorStart := time.Now()
		vectorResults, vectorErr = h.VectorSearch(ctx, query, opts.MaxResults*2, opts.EfSearch) // Get more for fusion
		results.VectorTime = time.Since(vectorStart)
	}()

	wg.Wait()

	// Check for errors
	if keywordErr != nil && vectorErr != nil {
		return nil, keywordErr // Return first error
	}

	// If one failed, fall back to the other
	if keywordErr != nil {
		return h.vectorOnlySearch(ctx, query, opts, results, start)
	}
	if vectorErr != nil {
		return h.keywordOnlySearch(ctx, query, opts, results, start)
	}

	results.KeywordResults = len(keywordResults)
	results.VectorResults = len(vectorResults)

	// Convert to fused results
	keywordFused := KeywordToFusedResults(keywordResults)
	vectorFused := VectorToFusedResults(vectorResults)

	// Enrich vector results with content
	for i := range vectorFused {
		if h.GetBlockContent != nil {
			content, snippet, page, section, err := h.GetBlockContent(vectorFused[i].DocumentID, vectorFused[i].BlockID)
			if err == nil {
				vectorFused[i].Content = content
				vectorFused[i].Snippet = snippet
				vectorFused[i].Page = page
				vectorFused[i].Section = section
			}
		}
		if h.GetDocumentName != nil {
			vectorFused[i].DocumentName = h.GetDocumentName(vectorFused[i].DocumentID)
		}
	}

	// Configure fusion
	fusionConfig := h.FusionConfig
	if fusionConfig == nil {
		fusionConfig = DefaultFusionConfig()
	}
	fusionConfig.KeywordWeight = opts.KeywordWeight
	fusionConfig.VectorWeight = opts.VectorWeight

	// Fuse results
	fused := RRFFusion(keywordFused, vectorFused, fusionConfig)

	// Normalize scores to 0-1 range for intuitive MinScore filtering
	NormalizeFusedScores(fused)

	// Apply filters (now works with normalized 0-1 scores)
	if opts.MinScore > 0 {
		beforeFilter := len(fused)
		fused = FilterByFusedScore(fused, opts.MinScore)
		results.FilteredByScore = beforeFilter - len(fused)
	}

	// Limit results
	fused = LimitFusedResults(fused, opts.MaxResults)

	results.Results = fused
	results.TotalHits = len(fused)
	results.SearchTime = time.Since(start)

	return results, nil
}

// NewHybridSearcher creates a new hybrid searcher
func NewHybridSearcher() *HybridSearcher {
	return &HybridSearcher{
		FusionConfig: DefaultFusionConfig(),
	}
}
