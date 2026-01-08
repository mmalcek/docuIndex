# DocuIndex

A pure Go package for parsing PDF files and extracting structured content optimized for AI search and RAG (Retrieval-Augmented Generation) applications.

## Features

- **Pure Go** - No CGO or external dependencies
- **PDF Parsing** - Complete PDF parser with PostScript content stream interpreter
- **Text Extraction** - Extract text with positioning, font info, and semantic structure
- **Image Extraction** - Extract embedded images (JPEG, PNG)
- **Semantic Analysis** - Automatic heading detection, section tracking, keyword extraction
- **Full-Text Search** - BM25-based search with boolean queries, phrase matching, and context windows for RAG
- **Thread-Safe** - Safe for concurrent use
- **Persistent Storage** - JSON-based storage with automatic indexing

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

    // Index a PDF file
    doc, err := store.IndexDocument("./document.pdf")
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

#### Index Documents

```go
// Index from file path
doc, err := store.IndexDocument("./document.pdf")

// Index from io.Reader
file, _ := os.Open("document.pdf")
doc, err := store.IndexReader(file, "document.pdf")

// With custom name
doc, err := store.IndexDocument("./document.pdf",
    docuindex.WithName("My Custom Name"),
)
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

#### Basic Search

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

#### Get Context for RAG

```go
// Get surrounding content blocks for a specific block
ctx, err := store.GetContext("doc-id", "blk_042", 5)

// ctx.Before - blocks before the target
// ctx.Center - the target block
// ctx.After - blocks after the target
```

### Document Structure

Each indexed document is stored with the following structure:

```
{uuid}/
├── document.json  # Document metadata + content blocks
├── index.json     # Search index
└── images/        # Extracted images (if enabled)
    ├── img_001.png
    └── img_001.json
```

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
```

## Storage Format

### document.json

```json
{
  "info": {
    "id": "abc123...",
    "name": "document.pdf",
    "original_path": "/path/to/document.pdf",
    "size_bytes": 1234567,
    "page_count": 42,
    "format": "pdf",
    "checksum": "sha256:...",
    "created_at": "2024-01-08T10:30:00Z",
    "updated_at": "2024-01-08T10:30:00Z"
  },
  "content": {
    "version": "1.0",
    "blocks": [
      {
        "id": "blk_001",
        "type": "heading",
        "content": "Chapter 1: Introduction",
        "page": 1,
        "bbox": {
          "x": 72,
          "y": 720,
          "width": 400,
          "height": 24,
          "page_width": 612,
          "page_height": 792
        },
        "font": {
          "name": "Helvetica-Bold",
          "size": 18,
          "bold": true
        },
        "semantic": {
          "is_heading": true,
          "heading_level": 1,
          "keywords": ["introduction", "chapter"]
        }
      }
    ]
  }
}
```

## Supported PDF Features

- PDF 1.0 - 1.7
- Traditional and cross-reference stream xref tables
- Stream filters: FlateDecode, ASCIIHexDecode, ASCII85Decode, LZWDecode, RunLengthDecode
- Font types: Type1, TrueType, Type0 (CID), Type3
- Encoding support: WinAnsi, MacRoman, Standard, PDFDocEncoding
- ToUnicode CMap for proper character mapping
- Content stream operators for text positioning and graphics state
- Embedded images (DCTDecode/JPEG, PNG)

## Limitations

- Encrypted PDFs are not supported
- DOCX support is planned but not yet implemented
- JBIG2Decode and CCITTFaxDecode filters have limited support

## License

MIT License
