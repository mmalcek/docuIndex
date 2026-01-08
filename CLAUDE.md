# DocuIndex

Pure Go package for parsing PDF files into AI-searchable format.

## Project Structure

```
docuindex/
├── CLAUDE.md           # This file - project docs for AI
├── CHECKLIST.md        # Implementation progress tracking
├── go.mod              # Go module (github.com/mariomalcek/docuindex)
├── docuindex.go        # Main API entry point
├── types.go            # Core types (ContentBlock, Document, etc.)
├── errors.go           # Custom error types
├── options.go          # Configuration options
│
├── pdf/                # Pure Go PDF parser
│   ├── lexer.go       # PDF tokenizer
│   ├── objects.go     # PDF object types
│   ├── xref.go        # Cross-reference table
│   ├── document.go    # PDF document structure
│   ├── stream.go      # Stream decompression
│   ├── page.go        # Page tree navigation
│   ├── content.go     # Content stream interpreter
│   ├── text.go        # Text extraction
│   ├── image.go       # Image extraction
│   ├── font.go        # Font handling
│   ├── semantic.go    # Heading/paragraph detection
│   └── encoding/      # Text encodings
│       ├── winansi.go    # Windows-1252 encoding
│       ├── macroman.go   # Mac Roman encoding
│       ├── standard.go   # Adobe Standard + PDFDoc encoding
│       └── glyphnames.go # Adobe Glyph List mappings
│
├── storage/            # Document storage
│   ├── store.go       # Store interface
│   └── filesystem.go  # File system implementation
│
├── search/             # Search functionality
│   ├── index.go       # Inverted index
│   ├── tokenizer.go   # Text tokenization with Porter stemming
│   ├── query.go       # Query parsing (boolean, phrase)
│   └── ranking.go     # BM25 ranking with boosting
│
├── cmd/                # CLI tools
│   └── test_pdf/
│       └── main.go    # Simple PDF test utility
│
└── testApp/            # Test application
    └── main.go        # Full-featured testing CLI
```

## Key Design Decisions

1. **Pure Go**: No CGO, no external PDF libraries - implements PDF parsing from scratch
2. **PostScript interpreter**: Proper content stream parsing with stack-based interpreter
3. **AI-optimized output**: Semantic blocks with context, positions, and metadata
4. **Thread-safe**: Concurrent document processing with `sync.RWMutex`
5. **Pluggable storage**: Interface-based storage layer for flexibility
6. **Security limits**: Memory and recursion limits to prevent DoS attacks

## Core Types

| Type | Description |
|------|-------------|
| `ContentBlock` | Unit of content (text/image) with position and semantics |
| `Document` | Parsed document with metadata and content blocks |
| `DocumentInfo` | Metadata (ID, Name, Size, PageCount, Format, Checksum, Timestamps) |
| `Store` | Document storage and search interface |
| `SearchResult` | Search hit with score, snippet, and context |
| `SearchResults` | Query results with timing and total hits |
| `BoundingBox` | Position information with `RelativePosition()` method |
| `SemanticInfo` | AI-friendly metadata (headings, sections, keywords) |
| `FontInfo` | Font name, size, bold, italic flags |
| `ContextResult` | Before/Center/After blocks for RAG |

## Storage Format

Each indexed document is stored in a UUID folder:

```
{uuid}/
├── document.json  # Document metadata + content blocks
├── index.json     # Document-level search index
└── images/        # Extracted images with metadata
    ├── img_001.png
    └── img_001.json
```

## Public API

```go
// Create a new store
store, err := docuindex.NewStore("/path/to/data")

// Index a document from file
doc, err := store.IndexDocument("/path/to/file.pdf")

// Index from io.Reader
doc, err := store.IndexReader(reader, "filename.pdf")

// Search across all documents
results, err := store.Search("query terms", docuindex.WithMaxResults(10))

// Search within specific document
results, err := store.SearchInDocument(docID, "query")

// Get context for RAG
context, err := store.GetContext(docID, blockID, windowSize)

// List all documents
docs, err := store.ListDocuments()

// Get document by ID
doc, err := store.GetDocument(docID)

// Delete document
err := store.DeleteDocument(docID)

// Get store statistics
stats := store.Stats()
```

## Configuration Options

### Store Options
```go
docuindex.WithMaxConcurrency(n)        // Parallel operation limit
docuindex.WithCache(enabled, size)     // Object caching
docuindex.WithImageExtraction(bool)    // Enable image extraction
docuindex.WithSemanticAnalysis(bool)   // Enable heading/section detection
docuindex.WithChecksum(bool)           // Compute document checksums
docuindex.WithStemming(bool)           // Porter stemming (default: true)
docuindex.WithStopWords(bool)          // Filter stop words (default: true)
```

### Search Options
```go
docuindex.WithMaxResults(n)            // Max results to return
docuindex.WithMinScore(float64)        // Minimum relevance score
docuindex.WithContextWindow(blocks)    // RAG context size
docuindex.WithHighlight(pre, post)     // Match highlighting markers
docuindex.WithPageRange(start, end)    // Filter by page range
docuindex.WithDocuments(...ids)        // Filter by document IDs
docuindex.WithSections(...strings)     // Filter by section
```

## Build & Test

```bash
go build ./...
go test ./...
go test -race ./...  # Verify thread safety
```

### Test Application

```bash
cd testApp
go run main.go index /path/to/file.pdf
go run main.go search "query terms"
go run main.go list
go run main.go info <doc-id>
go run main.go full-test /path/to/file.pdf  # Run all tests
```

## PDF Operators Implemented

### Text Operators
- `BT`, `ET` - Begin/end text object
- `Tf` - Set font and size
- `Td`, `TD`, `Tm`, `T*` - Text positioning
- `Tj`, `TJ`, `'`, `"` - Show text
- `Tc`, `Tw`, `Tz`, `TL`, `Tr`, `Ts` - Text state

### Graphics State
- `q`, `Q` - Save/restore graphics state
- `cm` - Modify transformation matrix
- `w`, `J`, `j`, `M`, `d` - Line properties
- `gs` - Set graphics state from dict
- `RG`, `rg`, `K`, `k` - Color operators

### XObjects
- `Do` - Paint XObject (images)

## Stream Filters Supported

- `FlateDecode` - Deflate/zlib compression
- `ASCIIHexDecode` - Hex encoding
- `ASCII85Decode` - Base85 encoding
- `LZWDecode` - LZW compression
- `RunLengthDecode` - Run-length encoding
- `DCTDecode` - JPEG (pass-through)
- `JPXDecode` - JPEG2000 (pass-through)
- PNG predictors - Sub, Up, Average, Paeth

## Font Types Supported

- Type1 (standard PostScript fonts)
- TrueType
- Type0 (CID composite fonts)
- Type3 (bitmap fonts)
- ToUnicode CMap parsing
- Encoding Differences arrays

## Search Features

- **Full-text search**: Inverted index with term positions
- **BM25 ranking**: Industry-standard relevance scoring
- **Boolean queries**: AND, OR, NOT operators (+, -, keywords)
- **Phrase matching**: Quoted exact phrases
- **Boosting**: Headings boosted 1.5x, exact matches boosted
- **Tokenization**: Porter stemming, stop word filtering
- **Context**: RAG-friendly context window retrieval

## Common Tasks

| Task | Location |
|------|----------|
| Add new PDF operator | `pdf/content.go` operators map |
| Add new encoding | `pdf/encoding/` directory |
| Add new stream filter | `pdf/stream.go` decodeFilter() |
| Modify search ranking | `search/ranking.go` |
| Add new font type | `pdf/font.go` loadFont() |
| Modify tokenization | `search/tokenizer.go` |

## Dependencies

Standard library only:
- `compress/zlib` - FlateDecode decompression
- `compress/lzw` - LZW decompression
- `encoding/binary` - Binary data handling
- `encoding/json` - Document serialization
- `image/*` - Image encoding/decoding
- `crypto/sha256` - Document checksums

## Error Handling

### Sentinel Errors (`errors.go`)
- `ErrInvalidPDF`, `ErrCorruptedPDF` - Malformed PDF structure
- `ErrEncryptedPDF` - Encrypted PDFs not supported
- `ErrUnsupportedFeature`, `ErrUnsupportedEncoding` - Unimplemented features
- `ErrDocumentNotFound`, `ErrDocumentExists` - Storage errors
- `ErrSearchFailed`, `ErrInvalidQuery` - Search errors

### Structured Error Types
- `ParseError` - Offset + operation context
- `ObjectError` - Object number/generation
- `PageError` - Page-specific errors
- `StreamError` - Filter decoding errors
- `FontError` - Font processing errors
- `StorageError`, `SearchError` - Operation failures

### Error Checking
```go
if docuindex.IsParseError(err) { ... }
if docuindex.IsStorageError(err) { ... }
if docuindex.IsSearchError(err) { ... }
```

## Security Limits

- `MaxStringLength`: 100MB (PDF string limit)
- `MaxDecompressedSize`: 500MB (stream limit)
- `MaxRecursionDepth`: 100 (prevents stack overflow)
- `MaxPredictorColumns`: 100,000
- Cycle detection in object references and page trees

## Concurrency Model

- Store operations are thread-safe via `sync.RWMutex`
- Multiple documents can be indexed concurrently
- Search operations use read locks for parallel queries
- Index updates use write locks

## Performance Considerations

- Lazy loading: Page content loaded on demand
- Streaming: Large files processed without full memory load
- Caching: Frequently accessed objects cached
- Parallel: Multi-page extraction can run concurrently

## Current Limitations

- DOCX support not yet implemented
- Encrypted PDFs not supported
- JBIG2Decode filter is a stub
- CCITTFaxDecode has limited support
