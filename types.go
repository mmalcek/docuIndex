package docuindex

import (
	"time"
)

// IndexProgress reports progress during document indexing
type IndexProgress struct {
	DocumentID      string        `json:"document_id"`
	DocumentName    string        `json:"document_name"`
	Status          string        `json:"status"` // "parsing", "extracting", "indexing", "embedding", "complete", "error"
	TotalPages      int           `json:"total_pages"`
	ProcessedPages  int           `json:"processed_pages"`
	TotalBlocks     int           `json:"total_blocks"`
	ProcessedBlocks int           `json:"processed_blocks"`
	Error           error         `json:"error,omitempty"`
	StartTime       time.Time     `json:"start_time"`
	ElapsedTime     time.Duration `json:"elapsed_time"`
}

// ProgressCallback is called during document indexing to report progress
type ProgressCallback func(IndexProgress)

// ChunkOptions configures how content is chunked for LLM context windows
type ChunkOptions struct {
	MaxTokens     int    `json:"max_tokens"`     // Maximum tokens per chunk (e.g., 512, 1024)
	OverlapTokens int    `json:"overlap_tokens"` // Token overlap between chunks
	ChunkBy       string `json:"chunk_by"`       // "paragraph", "sentence", "tokens"
}

// DefaultChunkOptions returns sensible defaults for chunking
func DefaultChunkOptions() ChunkOptions {
	return ChunkOptions{
		MaxTokens:     512,
		OverlapTokens: 50,
		ChunkBy:       "paragraph",
	}
}

// Chunk represents a portion of content with token information
type Chunk struct {
	Content    string `json:"content"`
	StartIdx   int    `json:"start_idx"`
	EndIdx     int    `json:"end_idx"`
	TokenCount int    `json:"token_count"`
}

// QueryType represents the detected intent of a search query
type QueryType string

const (
	// QueryTypeFactual for questions like "What is X?", "Who is Y?"
	QueryTypeFactual QueryType = "factual"
	// QueryTypeNavigation for "Show me section...", "Find..."
	QueryTypeNavigation QueryType = "navigation"
	// QueryTypeSummary for "Summarize...", "Overview of..."
	QueryTypeSummary QueryType = "summary"
	// QueryTypeComparison for "Compare X and Y", "Difference between..."
	QueryTypeComparison QueryType = "comparison"
	// QueryTypeDefinition for "Define X", "What is the definition of..."
	QueryTypeDefinition QueryType = "definition"
	// QueryTypeList for "List all X", "Enumerate...", "What are all..."
	QueryTypeList QueryType = "list"
	// QueryTypeUnknown when intent cannot be determined
	QueryTypeUnknown QueryType = "unknown"
)

// AgentSearchResponse provides AI agent-friendly search results
type AgentSearchResponse struct {
	Query           string              `json:"query"`
	QueryType       QueryType           `json:"query_type"`
	Results         []AgentSearchResult `json:"results"`
	TotalHits       int                 `json:"total_hits"`
	SearchTime      time.Duration       `json:"search_time"`
	EstimatedTokens int                 `json:"estimated_tokens"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
}

// AgentSearchResult is a single result optimized for AI agent consumption
type AgentSearchResult struct {
	DocumentID   string         `json:"document_id"`
	DocumentName string         `json:"document_name"`
	BlockID      string         `json:"block_id"`
	Content      string         `json:"content"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	Page         int            `json:"page"`
	Section      string         `json:"section"`
	CitationRef  string         `json:"citation_ref"` // e.g., "[1]", "[2]"
	TokenCount   int            `json:"token_count"`
	Context      []ContentBlock `json:"context,omitempty"`
	Images       []string       `json:"images,omitempty"`
}

// DedupResult contains information about duplicate detection
type DedupResult struct {
	IsDuplicate  bool    `json:"is_duplicate"`
	ExistingID   string  `json:"existing_id,omitempty"`
	ExistingName string  `json:"existing_name,omitempty"`
	Similarity   float64 `json:"similarity"`
	Method       string  `json:"method"` // "checksum", "content_hash", "embedding"
}

// BackgroundEmbeddingStatus represents the status of background HNSW building
type BackgroundEmbeddingStatus struct {
	Running        bool          `json:"running"`         // Is background build in progress
	StartedAt      time.Time     `json:"started_at"`      // When build started
	DocumentsTotal int           `json:"documents_total"` // Total documents to process
	DocumentsDone  int           `json:"documents_done"`  // Documents processed so far
	CurrentDocID   string        `json:"current_doc_id"`  // Currently processing document
	CurrentDocName string        `json:"current_doc_name"`
	ElapsedTime    time.Duration `json:"elapsed_time"`
	Error          error         `json:"error,omitempty"` // Error if failed
}

// Progress returns the completion percentage (0-100)
func (s BackgroundEmbeddingStatus) Progress() float64 {
	if s.DocumentsTotal == 0 {
		return 0
	}
	return float64(s.DocumentsDone) / float64(s.DocumentsTotal) * 100
}

// DateRange represents a time range for filtering
type DateRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// BlockType represents the type of content block
type BlockType string

const (
	BlockTypeText    BlockType = "text"
	BlockTypeHeading BlockType = "heading"
	BlockTypeImage   BlockType = "image"
	BlockTypeList    BlockType = "list"
	BlockTypeTable   BlockType = "table"
	BlockTypeCustom  BlockType = "custom" // Custom data entry
)

// DocumentFormat represents the source document format
type DocumentFormat string

const (
	FormatPDF        DocumentFormat = "pdf"
	FormatDOCX       DocumentFormat = "docx"
	FormatCustomData DocumentFormat = "customdata" // Custom data source
)

// BoundingBox represents the position and size of content on a page
type BoundingBox struct {
	X          float64 `json:"x"`           // Left edge in points
	Y          float64 `json:"y"`           // Bottom edge in points (PDF coordinate system)
	Width      float64 `json:"width"`       // Width in points
	Height     float64 `json:"height"`      // Height in points
	PageWidth  float64 `json:"page_width"`  // Page width for relative calculations
	PageHeight float64 `json:"page_height"` // Page height for relative calculations
}

// RelativePosition returns position as percentages of page dimensions
func (b BoundingBox) RelativePosition() (xPct, yPct, wPct, hPct float64) {
	if b.PageWidth > 0 {
		xPct = b.X / b.PageWidth * 100
		wPct = b.Width / b.PageWidth * 100
	}
	if b.PageHeight > 0 {
		yPct = b.Y / b.PageHeight * 100
		hPct = b.Height / b.PageHeight * 100
	}
	return
}

// FontInfo contains font metadata for text content
type FontInfo struct {
	Name   string  `json:"name"`             // Font name (e.g., "Helvetica-Bold")
	Size   float64 `json:"size"`             // Font size in points
	Bold   bool    `json:"bold,omitempty"`   // Is bold
	Italic bool    `json:"italic,omitempty"` // Is italic
}

// SemanticInfo contains AI-friendly metadata about content
type SemanticInfo struct {
	IsHeading    bool     `json:"is_heading,omitempty"`
	HeadingLevel int      `json:"heading_level,omitempty"` // 1-6 like HTML
	Section      string   `json:"section,omitempty"`       // Parent section title
	Keywords     []string `json:"keywords,omitempty"`      // Extracted keywords
	Context      string   `json:"context,omitempty"`       // Surrounding context summary
}

// ContentBlock represents a unit of content with position and metadata
type ContentBlock struct {
	ID       string       `json:"id"`                 // Unique block ID (e.g., "blk_001")
	Type     BlockType    `json:"type"`               // text, heading, image, etc.
	Content  string       `json:"content"`            // Text content or image path
	Page     int          `json:"page"`               // 1-indexed page number
	BBox     BoundingBox  `json:"bbox"`               // Position on page
	Font     *FontInfo    `json:"font,omitempty"`     // Font info for text
	Semantic SemanticInfo `json:"semantic,omitempty"` // AI-friendly metadata
	Children []string     `json:"children,omitempty"` // Child block IDs for hierarchy
}

// DocumentInfo contains metadata about an indexed document
type DocumentInfo struct {
	ID           string         `json:"id"`                        // UUID
	Name         string         `json:"name"`                      // Original filename
	OriginalPath string         `json:"original_path"`             // Path when indexed
	SizeBytes    int64          `json:"size_bytes"`                // File size
	PageCount    int            `json:"page_count"`                // Number of pages
	Format       DocumentFormat `json:"format"`                    // pdf, docx, customdata
	Checksum     string         `json:"checksum"`                  // SHA-256 hash
	CreatedAt    time.Time      `json:"created_at"`                // When indexed
	UpdatedAt    time.Time      `json:"updated_at"`                // Last update
	Source       string         `json:"source,omitempty"`          // CustomData source identifier
	Description  string         `json:"description,omitempty"`     // CustomData description
	ImportedAt   time.Time      `json:"imported_at,omitempty"`     // CustomData import timestamp
	ExternalID   string         `json:"external_id,omitempty"`     // External identifier for upsert
}

// DocumentContent holds the structured content of a document
type DocumentContent struct {
	Version string         `json:"version"` // Schema version
	Blocks  []ContentBlock `json:"blocks"`  // All content blocks
}

// Document represents a fully indexed document
type Document struct {
	Info    DocumentInfo    `json:"info"`
	Content DocumentContent `json:"content"`
}

// GetTextBlocks returns only text-type blocks
func (d *Document) GetTextBlocks() []ContentBlock {
	var result []ContentBlock
	for _, b := range d.Content.Blocks {
		if b.Type == BlockTypeText || b.Type == BlockTypeHeading {
			result = append(result, b)
		}
	}
	return result
}

// GetImageBlocks returns only image-type blocks
func (d *Document) GetImageBlocks() []ContentBlock {
	var result []ContentBlock
	for _, b := range d.Content.Blocks {
		if b.Type == BlockTypeImage {
			result = append(result, b)
		}
	}
	return result
}

// GetBlocksByPage returns blocks for a specific page
func (d *Document) GetBlocksByPage(page int) []ContentBlock {
	var result []ContentBlock
	for _, b := range d.Content.Blocks {
		if b.Page == page {
			result = append(result, b)
		}
	}
	return result
}

// GetBlockByID finds a block by its ID
func (d *Document) GetBlockByID(id string) *ContentBlock {
	for i := range d.Content.Blocks {
		if d.Content.Blocks[i].ID == id {
			return &d.Content.Blocks[i]
		}
	}
	return nil
}

// ImageInfo contains metadata about an extracted image
type ImageInfo struct {
	ID           string `json:"id"`                      // Image UUID
	DocumentID   string `json:"document_id,omitempty"`   // Parent document ID
	BlockID      string `json:"block_id,omitempty"`      // Associated content block ID
	Format       string `json:"format"`                  // png, jpeg, etc.
	Width        int    `json:"width"`                   // Image width in pixels
	Height       int    `json:"height"`                  // Image height in pixels
	Page         int    `json:"page"`                    // Page number
	OriginalName string `json:"original_name,omitempty"` // Original image name from PDF/DOCX
}

// SearchResult represents a single search hit
type SearchResult struct {
	DocumentID   string            `json:"document_id"`
	DocumentName string            `json:"document_name"`
	BlockID      string            `json:"block_id"`
	Content      string            `json:"content"`                 // Matched content
	Snippet      string            `json:"snippet"`                 // Highlighted snippet
	Score        float64           `json:"score"`                   // Relevance score
	Page         int               `json:"page"`                    // Page number
	Section      string            `json:"section"`                 // Section name
	Context      []ContentBlock    `json:"context"`                 // Surrounding blocks for RAG
	Positions    []int             `json:"positions"`               // Match positions in content
	Images       []string          `json:"images,omitempty"`        // Image paths in same section
	Tags         map[string]string `json:"tags,omitempty"`          // Document tags
	Source       string            `json:"source,omitempty"`        // Source identifier (for CustomData)
	ExternalID   string            `json:"external_id,omitempty"`   // External system ID
}

// SearchResults contains search results with metadata
type SearchResults struct {
	Query       string             `json:"query"`
	TotalHits   int                `json:"total_hits"`
	Results     []SearchResult     `json:"results"`
	SearchTime  time.Duration      `json:"search_time"`
	Diagnostics *SearchDiagnostics `json:"diagnostics,omitempty"`
}

// SearchDiagnostics provides detailed information about how search was executed.
// Useful for debugging and optimizing search performance.
type SearchDiagnostics struct {
	KeywordResults  int           `json:"keyword_results"`  // Results from BM25 keyword search
	VectorResults   int           `json:"vector_results"`   // Results from vector/semantic search
	KeywordTime     time.Duration `json:"keyword_time"`     // Time spent on keyword search
	VectorTime      time.Duration `json:"vector_time"`      // Time spent on vector search
	FusionTime      time.Duration `json:"fusion_time"`      // Time spent fusing results
	FilteredByScore int           `json:"filtered_by_score"` // Results filtered by MinScore
	DiversifiedFrom int           `json:"diversified_from"`  // Results before diversification (0 if not applied)
}

// ContextResult contains content blocks around a specific block
type ContextResult struct {
	DocumentID string         `json:"document_id"`
	CenterID   string         `json:"center_id"`   // The block we're getting context for
	Before     []ContentBlock `json:"before"`      // Blocks before
	Center     ContentBlock   `json:"center"`      // The center block
	After      []ContentBlock `json:"after"`       // Blocks after
}

// Page represents a single page with its content
type Page struct {
	Number int            `json:"number"`
	Width  float64        `json:"width"`
	Height float64        `json:"height"`
	Blocks []ContentBlock `json:"blocks"`
}

// Posting represents a term occurrence in the index
type Posting struct {
	DocumentID string  `json:"doc_id"`
	BlockID    string  `json:"block_id"`
	Positions  []int   `json:"positions"` // Positions within the block text
	TF         float64 `json:"tf"`        // Term frequency for this posting
}

// TermEntry contains all postings for a term
type TermEntry struct {
	Term     string    `json:"term"`
	DF       int       `json:"df"`       // Document frequency
	Postings []Posting `json:"postings"` // All occurrences
}

// DataEntry represents a single entry in custom data
type DataEntry struct {
	ID       string            `json:"id,omitempty"`       // Optional, auto-generated if empty
	Content  string            `json:"content"`            // Text content to index/embed
	Type     string            `json:"type,omitempty"`     // "text" (default), "json", "code"
	Metadata map[string]string `json:"metadata,omitempty"` // Entry-specific metadata
	Images   []CustomImage     `json:"images,omitempty"`   // Images associated with this entry
}

// CustomData represents structured data to be indexed
type CustomData struct {
	Source      string            `json:"source"`                // Source identifier (e.g., "crm", "faq")
	Name        string            `json:"name"`                  // Display name
	Description string            `json:"description,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`        // Filter-only tags (not searched)
	Entries     []DataEntry       `json:"entries"`               // Data entries to index
	ImportedAt  time.Time         `json:"imported_at,omitempty"` // When data was imported (for incremental updates)
	ExternalID  string            `json:"external_id,omitempty"` // Unique ID from source system (for upsert)
	Images      []CustomImage     `json:"images,omitempty"`      // Document-level images (not tied to specific entry)
}

// CustomImage represents an image to be indexed with custom data
type CustomImage struct {
	Data         []byte `json:"-"`                       // Image bytes (required, excluded from JSON)
	Format       string `json:"format"`                  // "png", "jpeg", "gif", "bmp" (required)
	Width        int    `json:"width,omitempty"`         // Optional, auto-detected if not provided
	Height       int    `json:"height,omitempty"`        // Optional, auto-detected if not provided
	OriginalName string `json:"original_name,omitempty"` // Optional display name
	Description  string `json:"description,omitempty"`   // AI-friendly alt text/description
}

// EmbeddingStatus contains information about a document's embedding state
type EmbeddingStatus struct {
	HasEmbeddings   bool      `json:"has_embeddings"`              // True if any embeddings exist
	IsComplete      bool      `json:"is_complete"`                 // True if all embeddable blocks have vectors
	EmbeddedCount   int       `json:"embedded_count"`              // Number of blocks with embeddings
	TotalEmbeddable int       `json:"total_embeddable"`            // Number of blocks that can be embedded
	Model           string    `json:"model,omitempty"`             // Embedding model used
	Dimension       int       `json:"dimension,omitempty"`         // Vector dimension
	LastUpdated     time.Time `json:"last_updated,omitempty"`      // When embeddings were last updated
}

// Progress returns embedding completion as a percentage (0-100)
func (e *EmbeddingStatus) Progress() float64 {
	if e.TotalEmbeddable == 0 {
		return 100.0 // Nothing to embed = complete
	}
	return float64(e.EmbeddedCount) / float64(e.TotalEmbeddable) * 100.0
}

// DatabaseInfo contains information about the database schema and version
type DatabaseInfo struct {
	SchemaVersion  int       `json:"schema_version"`  // Current schema version
	LibraryVersion string    `json:"library_version"` // Library version that created/migrated DB
	CreatedAt      time.Time `json:"created_at"`      // When database was created
	LastMigration  time.Time `json:"last_migration"`  // When last migration was applied
}

// StoreHealth contains consistency check results for diagnosing store issues
type StoreHealth struct {
	IsHealthy             bool     `json:"is_healthy"`              // True if all checks pass
	HNSWSize              int      `json:"hnsw_size"`               // Number of vectors in HNSW index
	SQLiteVectorCount     int      `json:"sqlite_vector_count"`     // Number of vectors in SQLite
	HNSWSynced            bool     `json:"hnsw_synced"`             // True if HNSW matches SQLite
	IncompleteEmbeddings  []string `json:"incomplete_embeddings"`   // Document IDs with partial embeddings
	PendingEmbeddings     []string `json:"pending_embeddings"`      // Document IDs without any embeddings
	DocumentCount         int      `json:"document_count"`          // Total number of documents
	BlockCount            int      `json:"block_count"`             // Total number of content blocks
}
