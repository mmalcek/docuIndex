package docuindex

// StoreOption configures Store behavior
type StoreOption func(*storeConfig)

// HNSWConfig configures the HNSW vector index parameters
type HNSWConfig struct {
	M        int // Max connections per layer (default: 16, range: 4-64)
	EfConst  int // Construction ef parameter (default: 200, range: 10-500)
	EfSearch int // Search ef parameter (default: 50, range: 10-500)
}

type storeConfig struct {
	// Storage settings
	BasePath       string
	MaxConcurrency int  // Max concurrent operations
	EnableCache    bool // Enable object caching
	CacheSize      int  // Max cached objects

	// Indexing settings
	ExtractImages    bool // Extract images from documents
	ExtractSemantics bool // Perform semantic analysis
	ComputeChecksum  bool // Compute document checksums
	EnableDedupCheck bool // Check for duplicates before indexing

	// Search settings
	EnableStemming  bool // Enable Porter stemming
	EnableStopWords bool // Filter common words
	IndexNGrams     bool // Enable n-gram indexing
	NGramSize       int  // N-gram size (default 3)

	// HNSW settings
	HNSWConfig *HNSWConfig // HNSW index configuration
}

func defaultStoreConfig() *storeConfig {
	return &storeConfig{
		MaxConcurrency:   4,
		EnableCache:      true,
		CacheSize:        1000,
		ExtractImages:    true,
		ExtractSemantics: true,
		ComputeChecksum:  true,
		EnableStemming:   true,
		EnableStopWords:  true,
		IndexNGrams:      false,
		NGramSize:        3,
	}
}

// WithMaxConcurrency sets the maximum concurrent operations
func WithMaxConcurrency(n int) StoreOption {
	return func(c *storeConfig) {
		if n > 0 {
			c.MaxConcurrency = n
		}
	}
}

// WithCache enables/disables object caching
func WithCache(enabled bool, size int) StoreOption {
	return func(c *storeConfig) {
		c.EnableCache = enabled
		if size > 0 {
			c.CacheSize = size
		}
	}
}

// WithImageExtraction enables/disables image extraction
func WithImageExtraction(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.ExtractImages = enabled
	}
}

// WithSemanticAnalysis enables/disables semantic analysis
func WithSemanticAnalysis(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.ExtractSemantics = enabled
	}
}

// WithChecksum enables/disables document checksum computation
func WithChecksum(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.ComputeChecksum = enabled
	}
}

// WithStemming enables/disables Porter stemming in search
func WithStemming(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.EnableStemming = enabled
	}
}

// WithStopWords enables/disables stop word filtering
func WithStopWords(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.EnableStopWords = enabled
	}
}

// WithNGrams enables n-gram indexing for fuzzy search
func WithNGrams(enabled bool, size int) StoreOption {
	return func(c *storeConfig) {
		c.IndexNGrams = enabled
		if size >= 2 {
			c.NGramSize = size
		}
	}
}

// WithDedupCheck enables duplicate detection before indexing
func WithDedupCheck(enabled bool) StoreOption {
	return func(c *storeConfig) {
		c.EnableDedupCheck = enabled
	}
}

// WithHNSWConfig configures the HNSW vector index parameters.
// Use this to tune performance vs quality trade-offs for bulk imports.
// See OPTIMISATIONS.md for recommended settings by dataset size.
func WithHNSWConfig(cfg HNSWConfig) StoreOption {
	return func(c *storeConfig) {
		c.HNSWConfig = &cfg
	}
}

// SearchOption configures search behavior
type SearchOption func(*searchConfig)

type searchConfig struct {
	MaxResults    int      // Maximum results to return
	MinScore      float64  // Minimum relevance score
	Sections      []string // Limit to specific sections
	ContextWindow int      // Number of context blocks before/after
	HighlightPre  string   // Prefix for highlighting matches
	HighlightPost string   // Suffix for highlighting matches
	PageRange     *struct{ Start, End int }
	DocumentIDs   []string          // Limit to specific documents
	IncludeImages bool              // Include image blocks in results
	SearchMode    SearchMode        // Search mode: keyword, semantic, or hybrid
	VectorWeight  float64           // Weight for vector search in hybrid mode
	KeywordWeight float64           // Weight for keyword search in hybrid mode
	Sources       []string          // Filter by source or format (e.g., "pdf", "crm")
	Tags          map[string]string // Filter by tags (AND logic)

	// Agent-friendly output options
	AgentOutput      bool          // Return AgentSearchResponse format
	EstimateTokens   bool          // Include token count estimates
	IncludeCitations bool          // Add citation references [1], [2], etc.
	ChunkOptions     *ChunkOptions // Chunking options for results
	Filter           *Filter       // Advanced filter DSL

	// HNSW tuning
	EfSearch int // Override efSearch for this query (0 = use default)
}

func defaultSearchConfig() *searchConfig {
	return &searchConfig{
		MaxResults:    100,
		MinScore:      0.0,
		ContextWindow: 2,
		HighlightPre:  "**",
		HighlightPost: "**",
		IncludeImages: false,
		SearchMode:    SearchModeHybrid, // Default to hybrid (falls back to keyword if no embeddings)
		VectorWeight:  0.5,
		KeywordWeight: 0.5,
	}
}

// WithMaxResults sets the maximum number of results
func WithMaxResults(n int) SearchOption {
	return func(c *searchConfig) {
		if n > 0 {
			c.MaxResults = n
		}
	}
}

// WithMinScore sets the minimum relevance score threshold
func WithMinScore(score float64) SearchOption {
	return func(c *searchConfig) {
		c.MinScore = score
	}
}

// WithSections limits search to specific sections
func WithSections(sections ...string) SearchOption {
	return func(c *searchConfig) {
		c.Sections = sections
	}
}

// WithContextWindow sets the number of surrounding blocks to include
func WithContextWindow(blocks int) SearchOption {
	return func(c *searchConfig) {
		if blocks >= 0 {
			c.ContextWindow = blocks
		}
	}
}

// WithHighlight sets the highlight markers for matched terms
func WithHighlight(pre, post string) SearchOption {
	return func(c *searchConfig) {
		c.HighlightPre = pre
		c.HighlightPost = post
	}
}

// WithPageRange limits search to a specific page range
func WithPageRange(start, end int) SearchOption {
	return func(c *searchConfig) {
		c.PageRange = &struct{ Start, End int }{start, end}
	}
}

// WithDocuments limits search to specific documents
func WithDocuments(docIDs ...string) SearchOption {
	return func(c *searchConfig) {
		c.DocumentIDs = docIDs
	}
}

// WithImages includes image blocks in search results
func WithImages(include bool) SearchOption {
	return func(c *searchConfig) {
		c.IncludeImages = include
	}
}

// IndexOption configures indexing behavior
type IndexOption func(*indexConfig)

type indexConfig struct {
	ForceReindex     bool             // Re-index even if document exists
	SourcePath       string           // Original file path for metadata
	Name             string           // Override document name
	ProgressCallback ProgressCallback // Callback for progress updates
	DeferEmbedding   bool             // Skip embedding during indexing (use EmbedPendingDocuments later)
}

func defaultIndexConfig() *indexConfig {
	return &indexConfig{
		ForceReindex: false,
	}
}

// WithForceReindex forces re-indexing even if document exists
func WithForceReindex(force bool) IndexOption {
	return func(c *indexConfig) {
		c.ForceReindex = force
	}
}

// WithSourcePath sets the original source path for metadata
func WithSourcePath(path string) IndexOption {
	return func(c *indexConfig) {
		c.SourcePath = path
	}
}

// WithName overrides the document name
func WithName(name string) IndexOption {
	return func(c *indexConfig) {
		c.Name = name
	}
}

// WithProgressCallback sets a callback for progress updates during indexing
func WithProgressCallback(fn ProgressCallback) IndexOption {
	return func(c *indexConfig) {
		c.ProgressCallback = fn
	}
}

// WithDeferEmbedding skips embedding generation during indexing.
// Use store.EmbedPendingDocuments() or store.EmbedDocuments() to generate
// embeddings later in a batch operation. This is recommended for bulk imports.
// See OPTIMISATIONS.md for recommended patterns.
func WithDeferEmbedding(defer_ bool) IndexOption {
	return func(c *indexConfig) {
		c.DeferEmbedding = defer_
	}
}

// SearchMode defines the type of search
type SearchMode string

const (
	// SearchModeKeyword uses BM25 keyword search only
	SearchModeKeyword SearchMode = "keyword"
	// SearchModeSemantic uses vector similarity search only
	SearchModeSemantic SearchMode = "semantic"
	// SearchModeHybrid combines BM25 and vector search with RRF fusion
	SearchModeHybrid SearchMode = "hybrid"
)

// WithSearchMode sets the search mode (keyword, semantic, or hybrid)
func WithSearchMode(mode SearchMode) SearchOption {
	return func(c *searchConfig) {
		c.SearchMode = mode
	}
}

// WithVectorWeight sets the weight for vector search in hybrid mode (0-1)
func WithVectorWeight(weight float64) SearchOption {
	return func(c *searchConfig) {
		if weight >= 0 && weight <= 1 {
			c.VectorWeight = weight
		}
	}
}

// WithKeywordWeight sets the weight for keyword search in hybrid mode (0-1)
func WithKeywordWeight(weight float64) SearchOption {
	return func(c *searchConfig) {
		if weight >= 0 && weight <= 1 {
			c.KeywordWeight = weight
		}
	}
}

// WithSources filters search results by source or format (e.g., "pdf", "docx", "crm")
func WithSources(sources ...string) SearchOption {
	return func(c *searchConfig) {
		c.Sources = sources
	}
}

// WithTags filters search results by tags (AND logic - all must match)
func WithTags(tags map[string]string) SearchOption {
	return func(c *searchConfig) {
		c.Tags = tags
	}
}

// WithAgentOutput enables agent-friendly output format
func WithAgentOutput(enabled bool) SearchOption {
	return func(c *searchConfig) {
		c.AgentOutput = enabled
	}
}

// WithEstimateTokens includes token count estimates in results
func WithEstimateTokens(enabled bool) SearchOption {
	return func(c *searchConfig) {
		c.EstimateTokens = enabled
	}
}

// WithCitations adds citation references [1], [2], etc. to results
func WithCitations(enabled bool) SearchOption {
	return func(c *searchConfig) {
		c.IncludeCitations = enabled
	}
}

// WithChunking configures result chunking for LLM context windows
func WithChunking(opts ChunkOptions) SearchOption {
	return func(c *searchConfig) {
		c.ChunkOptions = &opts
	}
}

// WithFilter applies an advanced filter DSL
func WithFilter(f *Filter) SearchOption {
	return func(c *searchConfig) {
		c.Filter = f
	}
}

// WithEfSearch overrides the HNSW efSearch parameter for this query.
// Higher values improve recall at the cost of latency.
// If ef <= 0 (default), the store's configured EfSearch value is used.
// Recommended: 50 for speed, 100 for balanced, 200+ for high recall.
func WithEfSearch(ef int) SearchOption {
	return func(c *searchConfig) {
		c.EfSearch = ef
	}
}
