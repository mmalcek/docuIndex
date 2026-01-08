# DocuIndex

A pure Go package for parsing PDF and DOCX files and extracting structured content optimized for AI search and RAG (Retrieval-Augmented Generation) applications.

** This package is under very active development. Expect frequent updates and improvements. Not yet stable. **

## Features

- **Pure Go** - No CGO or external dependencies
- **PDF Parsing** - Complete PDF parser with PostScript content stream interpreter
- **DOCX Parsing** - Full DOCX support via ZIP/XML parsing with style resolution
- **Custom Data Sources** - Index arbitrary structured data with tag-based filtering
- **Text Extraction** - Extract text with positioning, font info, and semantic structure
- **Image Extraction** - Extract embedded images (JPEG, PNG, GIF, BMP, TIFF)
- **Semantic Analysis** - Automatic heading detection, section tracking, keyword extraction
- **SQLite Storage** - Unified SQLite database for all metadata and search indices
- **Hybrid Search** - BM25 keyword search + vector semantic search with RRF fusion
- **Embedding Providers** - Azure OpenAI, OpenAI, and Ollama support
- **Thread-Safe** - Safe for concurrent use

## Installation

```bash
go get github.com/mariomalcek/docuindex
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    "github.com/mariomalcek/docuindex"
)

func main() {
    // Create a store (documents will be saved to ./data directory)
    store, err := docuindex.NewStore("./data")
    if err != nil {
        log.Fatal(err)
    }
    defer store.Close()

    // Index a PDF or DOCX file
    doc, err := store.IndexDocument("./document.pdf")   // PDF
    // doc, err := store.IndexDocument("./document.docx") // DOCX
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Indexed document: %s (%d pages)\n", doc.Info.Name, doc.Info.PageCount)

    // Search across all documents
    results, err := store.Search("search query")
    if err != nil {
        log.Fatal(err)
    }

    for _, r := range results.Results {
        fmt.Printf("Found in %s (page %d): %s\n", r.DocumentName, r.Page, r.Snippet)
    }
}
```

## API Reference

### Store Operations

#### Create a Store

```go
// Basic store
store, err := docuindex.NewStore("./data")

// With options
store, err := docuindex.NewStore("./data",
    docuindex.WithImageExtraction(true),    // Extract images from PDFs
    docuindex.WithChecksum(true),           // Compute SHA-256 checksums
    docuindex.WithSemanticAnalysis(true),   // Enable heading/section detection
    docuindex.WithStemming(true),           // Enable Porter stemming for search
    docuindex.WithStopWords(true),          // Filter common stop words
)
```

#### Configure Embedding Provider (Optional)

```go
import "github.com/mariomalcek/docuindex/embedding"

// Azure OpenAI
provider, err := embedding.NewProvider(embedding.Config{
    Provider: "azure",
    Endpoint: os.Getenv("AZURE_ENDPOINT"),
    APIKey:   os.Getenv("AZURE_API_KEY"),
    Model:    "text-embedding-3-small",
})

// OpenAI
provider, err := embedding.NewProvider(embedding.Config{
    Provider: "openai",
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    Model:    "text-embedding-3-small",
})

// Ollama (local)
provider, err := embedding.NewProvider(embedding.Config{
    Provider: "ollama",
    Endpoint: "http://localhost:11434",
    Model:    "nomic-embed-text",
})

// Add to store for semantic search
store.SetEmbeddingProvider(provider)
```

#### Index Documents

```go
// Index from file path (PDF or DOCX)
doc, err := store.IndexDocument("./document.pdf")
doc, err := store.IndexDocument("./document.docx")

// Index from io.Reader
file, _ := os.Open("document.pdf")
doc, err := store.IndexReader(file, "document.pdf")

file, _ := os.Open("document.docx")
doc, err := store.IndexReader(file, "document.docx")

// With custom name
doc, err := store.IndexDocument("./document.pdf",
    docuindex.WithName("My Custom Name"),
)
```

#### Index Custom Data

```go
// Index structured data from any source (creates new document each time)
doc, err := store.IndexCustomData(&docuindex.CustomData{
    Source:      "crm-api",
    Name:        "Customer Notes Q4 2024",
    Description: "Extracted customer interaction notes",
    Tags: map[string]string{
        "quarter": "Q4-2024",
        "type":    "customer-notes",
    },
    ImportedAt: time.Now(), // Optional: track import time for incremental updates
    Entries: []docuindex.DataEntry{
        {Content: "Meeting with Acme Corp about renewal..."},
        {Content: "Support ticket #1234: User reported issue..."},
        {Content: "Sales call summary: Interested in enterprise plan..."},
    },
})

// Upsert custom data (update existing document if source + external_id match)
doc, err := store.UpsertCustomData(&docuindex.CustomData{
    Source:      "salesforce-api",
    Name:        "Salesforce Opportunities",
    ExternalID:  "opportunities-q4",  // Optional - enables update-or-create behavior
    ImportedAt:  time.Now(),
    Entries: []docuindex.DataEntry{
        {Content: "Acme Corp - $50k deal in progress..."},
        {Content: "Widget Inc - Renewal pending..."},
    },
})

// Get last import time for incremental updates
lastImport, err := store.GetLastImportTime("crm-api")
if !lastImport.IsZero() {
    // Fetch only new data from source since lastImport
}
```

#### Retrieve Documents

```go
// Get by ID
doc, err := store.GetDocument("document-id")

// List all documents
docs, err := store.ListDocuments()
for _, info := range docs {
    fmt.Printf("%s: %s (%d pages)\n", info.ID, info.Name, info.PageCount)
}

// Delete a document
err := store.DeleteDocument("document-id")
```

### Search Operations

#### Basic Search (BM25 Keyword Search)

```go
results, err := store.Search("machine learning")

for _, r := range results.Results {
    fmt.Printf("Document: %s\n", r.DocumentName)
    fmt.Printf("Page: %d\n", r.Page)
    fmt.Printf("Section: %s\n", r.Section)
    fmt.Printf("Score: %.2f\n", r.Score)
    fmt.Printf("Snippet: %s\n", r.Snippet)
}
```

#### Search Modes

```go
// Keyword search (BM25) - default
results, err := store.Search("neural networks",
    docuindex.WithSearchMode(docuindex.SearchModeKeyword),
)

// Semantic search (vector embeddings) - requires embedding provider
results, err := store.Search("how does machine learning work",
    docuindex.WithSearchMode(docuindex.SearchModeSemantic),
)

// Hybrid search (BM25 + vectors with RRF fusion)
results, err := store.Search("climate change impacts",
    docuindex.WithSearchMode(docuindex.SearchModeHybrid),
    docuindex.WithVectorWeight(0.6),   // Weight for semantic results
    docuindex.WithKeywordWeight(0.4),  // Weight for keyword results
)
```

#### Search with Options

```go
results, err := store.Search("neural networks",
    docuindex.WithMaxResults(10),           // Limit results
    docuindex.WithMinScore(0.5),            // Minimum relevance score
    docuindex.WithContextWindow(3),         // Include 3 blocks before/after
    docuindex.WithHighlight("<b>", "</b>"), // Highlight matches in snippet
)
```

#### Boolean and Phrase Queries

```go
// Boolean operators: AND, OR, NOT (or +, -)
results, err := store.Search("machine learning AND neural")
results, err := store.Search("+required -excluded optional")

// Phrase matching with quotes
results, err := store.Search(`"exact phrase match"`)
```

#### Search in Specific Document

```go
results, err := store.SearchInDocument("doc-id", "query")
```

#### Search with Source/Tag Filtering

```go
// Search only custom data
results, err := store.Search("renewal",
    docuindex.WithSources("customdata"))

// Search by specific source
results, err := store.Search("renewal",
    docuindex.WithSources("crm-api"))

// Search with tag filter
results, err := store.Search("renewal",
    docuindex.WithTags(map[string]string{"quarter": "Q4-2024"}))

// Combined filters
results, err := store.Search("enterprise",
    docuindex.WithSources("crm-api", "faq"),
    docuindex.WithTags(map[string]string{"type": "customer-notes"}))
```

#### Get Context for RAG

```go
// Get surrounding content blocks for a specific block
ctx, err := store.GetContext("doc-id", "blk_042", 5)

// ctx.Before - blocks before the target
// ctx.Center - the target block
// ctx.After - blocks after the target
```

### Document Structure

#### Content Block

```go
type ContentBlock struct {
    ID       string       // Unique block ID (e.g., "blk_001")
    Type     BlockType    // text, heading, image, list, table
    Content  string       // Text content or image path
    Page     int          // 1-indexed page number
    BBox     BoundingBox  // Position on page
    Font     *FontInfo    // Font metadata
    Semantic SemanticInfo // Heading level, section, keywords
}
```

#### Working with Blocks

```go
doc, _ := store.GetDocument("doc-id")

// Get all text blocks
textBlocks := doc.GetTextBlocks()

// Get all image blocks
imageBlocks := doc.GetImageBlocks()

// Get blocks from a specific page
page3Blocks := doc.GetBlocksByPage(3)

// Find a specific block
block := doc.GetBlockByID("blk_042")
```

### Store Statistics

```go
stats := store.Stats()
fmt.Printf("Documents: %d\n", stats.DocumentCount)
fmt.Printf("Total blocks: %d\n", stats.TotalBlocks)
fmt.Printf("Total images: %d\n", stats.TotalImages)
fmt.Printf("Index terms: %d\n", stats.IndexTerms)
fmt.Printf("Vectors: %d\n", stats.VectorCount)
```

### Embedding Status

Check whether embeddings have been generated for a document:

```go
// Quick check if any embeddings exist
hasEmb, err := store.HasEmbeddings(docID)
if hasEmb {
    fmt.Println("Document has embeddings")
}

// Get detailed embedding status
status, err := store.GetEmbeddingStatus(docID)
fmt.Printf("Progress: %.1f%% (%d/%d blocks)\n",
    status.Progress(), status.EmbeddedCount, status.TotalEmbeddable)

if status.IsComplete {
    fmt.Println("Fully embedded")
} else if status.HasEmbeddings {
    fmt.Println("Partially embedded")
} else {
    fmt.Println("No embeddings")
}

// EmbeddingStatus fields:
// - HasEmbeddings   bool      - true if any embeddings exist
// - IsComplete      bool      - true if all embeddable blocks have vectors
// - EmbeddedCount   int       - number of blocks with embeddings
// - TotalEmbeddable int       - number of blocks that can be embedded
// - Model           string    - embedding model used
// - Dimension       int       - vector dimension
// - LastUpdated     time.Time - when embeddings were last updated
```

## Storage Architecture

DocuIndex uses a unified SQLite database for all metadata and search indices:

```
data/
├── docuindex.db           # SQLite database (all metadata)
├── hnsw.idx               # HNSW vector index (binary)
└── images/                # Extracted images with UUID names
    ├── a1b2c3d4-e5f6-7890-abcd-ef1234567890.png
    └── ...
```

### Database Schema

The SQLite database contains:
- **documents** - Document metadata (name, path, format, page count, timestamps)
- **content_blocks** - Parsed content with position, font, and semantic info
- **search_terms** - BM25 inverted index with term positions
- **document_stats** - Statistics for BM25 ranking
- **vectors** - Block embeddings as BLOBs
- **images** - Image metadata (actual files in images/ folder)

## Search Capabilities

### BM25 Keyword Search
- Industry-standard relevance ranking
- Boolean queries (AND, OR, NOT)
- Phrase matching with position data
- Porter stemming and stop word filtering
- Heading boost (1.5x)

### Semantic Vector Search
- HNSW approximate nearest neighbor
- Supports Azure OpenAI, OpenAI, Ollama
- Block-level embeddings for granular retrieval
- Cosine similarity distance

### Hybrid Search
- Combines BM25 + vector results
- Reciprocal Rank Fusion (RRF) scoring
- Configurable weights

## Supported PDF Features

- PDF 1.0 - 1.7
- Traditional and cross-reference stream xref tables
- Stream filters: FlateDecode, ASCIIHexDecode, ASCII85Decode, LZWDecode, RunLengthDecode
- Font types: Type1, TrueType, Type0 (CID), Type3
- Encoding support: WinAnsi, MacRoman, Standard, PDFDocEncoding
- ToUnicode CMap for proper character mapping
- Content stream operators for text positioning and graphics state
- Embedded images (DCTDecode/JPEG, PNG)

## Supported DOCX Features

- Full ZIP archive parsing via standard library
- XML content parsing with namespace handling
- Style-based and font-based heading detection
- Style inheritance chain resolution
- Bullet and numbered list extraction
- Table content with row/column structure
- Inline and anchored image extraction (JPEG, PNG, GIF, BMP, TIFF)
- Dublin Core metadata (title, author, keywords)
- Application properties (page count, word count)
- Field instructions (TOC, page numbers, hyperlinks)
- Position estimation for search result context

## Dependencies

- `modernc.org/sqlite` - Pure Go SQLite (no CGO)
- `github.com/google/uuid` - UUID generation
- Standard library for everything else

## Limitations

- Encrypted PDFs are not supported
- JBIG2Decode and CCITTFaxDecode PDF filters have limited support
- DOCX position estimation is approximate (DOCX lacks exact positioning unlike PDF)
- DOCX vector images (EMF, WMF) are detected but skipped

## License

MIT License
