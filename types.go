package docuindex

import (
	"time"
)

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
	ID         string      `json:"id"`          // Image ID (e.g., "img_001")
	Path       string      `json:"path"`        // Relative path to image file
	Width      int         `json:"width"`       // Image width in pixels
	Height     int         `json:"height"`      // Image height in pixels
	Format     string      `json:"format"`      // png, jpeg, etc.
	ColorSpace string      `json:"color_space"` // RGB, CMYK, Grayscale
	BBox       BoundingBox `json:"bbox"`        // Position in document
	Page       int         `json:"page"`        // Page number
	Context    string      `json:"context"`     // Surrounding text context
}

// SearchResult represents a single search hit
type SearchResult struct {
	DocumentID   string         `json:"document_id"`
	DocumentName string         `json:"document_name"`
	BlockID      string         `json:"block_id"`
	Content      string         `json:"content"`      // Matched content
	Snippet      string         `json:"snippet"`      // Highlighted snippet
	Score        float64        `json:"score"`        // Relevance score
	Page         int            `json:"page"`         // Page number
	Section      string         `json:"section"`      // Section name
	Context      []ContentBlock `json:"context"`      // Surrounding blocks for RAG
	Positions    []int          `json:"positions"`    // Match positions in content
}

// SearchResults contains search results with metadata
type SearchResults struct {
	Query      string         `json:"query"`
	TotalHits  int            `json:"total_hits"`
	Results    []SearchResult `json:"results"`
	SearchTime time.Duration  `json:"search_time"`
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
}
