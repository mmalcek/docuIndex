# DocuIndex

Pure Go package for parsing PDF and DOCX files into AI-searchable format with unified SQLite storage and optional semantic search via embeddings.

## Project Structure

```
docuindex/
├── CLAUDE.md           # This file - project docs for AI
├── CHECKLIST.md        # Implementation progress tracking
├── go.mod              # Go module (github.com/mmalcek/docuIndex)
├── docuindex.go        # Main API entry point
├── types.go            # Core types (ContentBlock, Document, etc.)
├── errors.go           # Custom error types
├── options.go          # Configuration options
├── filter.go           # Filter Query DSL for advanced filtering
├── query.go            # Query intent detection
├── tokens.go           # Token estimation for LLM context
├── chunking.go         # LLM-friendly content chunking
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
├── docx/               # Pure Go DOCX parser
│   ├── document.go    # ZIP/document loader, file access
│   ├── types.go       # XML structs + content block types
│   ├── styles.go      # Style parsing and resolution
│   ├── numbering.go   # List/numbering definitions
│   ├── relationships.go # .rels file parsing (images, links)
│   ├── text.go        # Text extraction from paragraphs/runs
│   ├── image.go       # Image extraction from word/media/
│   ├── table.go       # Table content extraction
│   ├── metadata.go    # docProps/core.xml + app.xml
│   └── semantic.go    # SemanticExtractor orchestrator
│
├── sqlite/             # Unified SQLite storage
│   ├── store.go       # SQLiteStore implementation
│   ├── schema.go      # Database schema and migrations
│   ├── documents.go   # Document CRUD operations
│   ├── blocks.go      # Content block operations
│   ├── search.go      # BM25 search on SQLite
│   ├── vectors.go     # Vector storage as BLOBs
│   ├── images.go      # Image metadata + file management
│   ├── tags.go        # Document tag operations
│   └── dedup.go       # Document deduplication
│
├── embedding/          # Embedding providers
│   ├── provider.go    # Provider interface + factory
│   ├── azure.go       # Azure OpenAI provider
│   ├── openai.go      # OpenAI provider
│   └── ollama.go      # Ollama local provider
│
├── vectorindex/        # HNSW approximate nearest neighbor
│   └── hnsw.go        # Pure Go HNSW implementation
│
├── search/             # Search functionality
│   ├── types.go       # Minimal SearchResult for internal fusion
│   ├── query.go       # Snippet extraction utility
│   ├── hybrid.go      # Hybrid BM25 + vector search
│   └── fusion.go      # RRF score fusion
│
├── internal/           # Internal shared packages
│   └── nlp/           # Natural language processing
│       ├── stopwords.go  # Shared stop word detection
│       └── stemmer.go    # Shared Porter stemmer
│
├── cmd/                # CLI tools
│   └── test_pdf/
│       └── main.go    # Simple PDF test utility
│
└── testApp/            # Test application
    └── main.go        # Full-featured testing CLI
```

## Key Design Decisions

1. **Pure Go**: No CGO, no external libraries - implements PDF, DOCX parsing, SQLite (modernc.org/sqlite), and HNSW from scratch
2. **Unified SQLite Storage**: Single `docuindex.db` file for all metadata (documents, blocks, search index, vectors)
3. **PostScript interpreter**: Proper PDF content stream parsing with stack-based interpreter
4. **DOCX via ZIP+XML**: DOCX files parsed as ZIP archives with XML content using `archive/zip` and `encoding/xml`
5. **Custom Data Sources**: Index arbitrary structured data alongside documents with tag-based filtering
6. **AI-optimized output**: Semantic blocks with context, positions, and metadata
7. **Hybrid Search**: BM25 keyword search + optional vector semantic search with RRF fusion
8. **Optional Embeddings**: Embedding providers (Azure, OpenAI, Ollama) are optional - works without them
9. **Thread-safe**: Concurrent document processing with `sync.RWMutex`
10. **Security limits**: Memory and recursion limits to prevent DoS attacks

## Architecture

### Storage Layout

```
data/
├── docuindex.db           # Single SQLite database (all metadata)
├── hnsw.idx               # HNSW index file (binary, rebuilt on startup)
└── images/                # All images with UUID names
    ├── a1b2c3d4-e5f6-7890-abcd-ef1234567890.png
    └── ...
```

### Database Schema

```sql
-- Documents table
CREATE TABLE documents (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    original_path TEXT,
    format TEXT NOT NULL,
    size_bytes INTEGER,
    page_count INTEGER,
    checksum TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    source TEXT DEFAULT '',      -- CustomData source identifier
    description TEXT DEFAULT '', -- CustomData description
    imported_at TEXT DEFAULT '', -- CustomData import timestamp
    external_id TEXT DEFAULT ''  -- External identifier for upsert
);

-- Unique index for upsert by source + external_id
CREATE UNIQUE INDEX idx_documents_source_external ON documents(source, external_id) WHERE external_id != '';

-- Content blocks table
CREATE TABLE content_blocks (
    id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    type TEXT NOT NULL,
    content TEXT,
    page INTEGER,
    sequence INTEGER,
    -- Bounding box, font info, semantic info...
    entry_metadata TEXT DEFAULT '',  -- CustomData entry metadata (JSON)
    PRIMARY KEY (document_id, id),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);

-- BM25 inverted index
CREATE TABLE search_terms (
    term TEXT NOT NULL,
    document_id TEXT NOT NULL,
    block_id TEXT NOT NULL,
    positions TEXT,
    term_frequency REAL,
    PRIMARY KEY (term, document_id, block_id)
);

-- Vector embeddings
CREATE TABLE vectors (
    block_id TEXT NOT NULL,
    document_id TEXT NOT NULL,
    vector BLOB NOT NULL,
    text_hash TEXT,
    model TEXT,
    dimension INTEGER,
    created_at TEXT NOT NULL,
    PRIMARY KEY (document_id, block_id)
);

-- Image references
CREATE TABLE images (
    id TEXT PRIMARY KEY,
    document_id TEXT NOT NULL,
    block_id TEXT,
    format TEXT NOT NULL,
    width INTEGER,
    height INTEGER,
    page INTEGER,
    original_name TEXT
);

-- Document tags for filtering
CREATE TABLE document_tags (
    document_id TEXT NOT NULL,
    tag_key TEXT NOT NULL,
    tag_value TEXT NOT NULL,
    PRIMARY KEY (document_id, tag_key),
    FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE
);
```

## Core Types

| Type | Description |
|------|-------------|
| `ContentBlock` | Unit of content (text/image/custom) with position and semantics |
| `Document` | Parsed document with metadata and content blocks |
| `DocumentInfo` | Metadata (ID, Name, Size, PageCount, Format, Source, Description, ImportedAt, ExternalID, Timestamps) |
| `Store` | Document storage and search interface |
| `SearchResult` | Search hit with score, snippet, context, and images |
| `SearchResults` | Query results with timing and total hits |
| `BoundingBox` | Position information with `RelativePosition()` method |
| `SemanticInfo` | AI-friendly metadata (headings, sections, keywords) |
| `FontInfo` | Font name, size, bold, italic flags |
| `ContextResult` | Before/Center/After blocks for RAG |
| `SearchMode` | Search type: keyword, semantic, or hybrid |
| `CustomData` | Structured data source with entries, tags, ImportedAt, and ExternalID for upsert |
| `DataEntry` | Single entry in custom data with content and metadata |
| `EmbeddingStatus` | Document embedding state (HasEmbeddings, IsComplete, EmbeddedCount, TotalEmbeddable, Model, Dimension, LastUpdated) |
| `IndexProgress` | Progress tracking for async document indexing (status, pages, blocks, elapsed time) |
| `ProgressCallback` | Function type for receiving indexing progress updates |
| `QueryType` | Detected query intent (factual, navigation, summary, comparison, definition, list) |
| `AgentSearchResponse` | Structured search response for AI agents (with token estimates, citations) |
| `AgentSearchResult` | Single result with CitationRef, TokenCount, and Context |
| `DedupResult` | Document deduplication check result (IsDuplicate, ExistingID, Method) |
| `ChunkOptions` | LLM chunking configuration (MaxTokens, OverlapTokens, ChunkBy) |
| `Chunk` | A chunked content piece with token count and position |
| `Filter` | Fluent filter DSL for advanced metadata filtering |
| `TokenBudget` | Helper for tracking token usage across operations |
| `TokenCredential` | Interface for Azure token-based auth (compatible with azcore.TokenCredential) |
| `AccessToken` | Azure access token with expiry time |

## Public API

```go
// Create a new store (SQLite-backed)
store, err := docuindex.NewStore("/path/to/data")

// Index a document from file (PDF or DOCX)
doc, err := store.IndexDocument("/path/to/file.pdf")
doc, err := store.IndexDocument("/path/to/file.docx")

// Index from io.Reader
doc, err := store.IndexReader(reader, "filename.pdf")

// Index custom data (creates new document each time)
doc, err := store.IndexCustomData(&docuindex.CustomData{
    Source:      "crm",
    Name:        "Customer Notes",
    Description: "CRM exported notes",
    Tags:        map[string]string{"team": "sales", "quarter": "Q4"},
    ImportedAt:  time.Now(), // Optional: defaults to now if not set
    Entries: []docuindex.DataEntry{
        {Content: "Meeting with Acme Corp..."},
        {Content: "Follow-up call scheduled..."},
    },
})

// Upsert custom data (update if source + external_id exists, else create)
doc, err := store.UpsertCustomData(&docuindex.CustomData{
    Source:      "salesforce",
    Name:        "Q4 Opportunities",
    ExternalID:  "opps-q4-2024",  // Optional - enables update-or-create behavior
    ImportedAt:  time.Now(),
    Entries: []docuindex.DataEntry{
        {Content: "Acme Corp - $50k deal in progress..."},
        {Content: "Widget Inc - Renewal pending..."},
    },
})

// Get last import time for incremental updates
lastImport, err := store.GetLastImportTime("crm")
if !lastImport.IsZero() {
    // Fetch only new data since lastImport
}

// Search across all documents (keyword search by default)
results, err := store.Search("query terms", docuindex.WithMaxResults(10))

// Hybrid search (requires embedding provider)
results, err := store.Search("query terms",
    docuindex.WithSearchMode(docuindex.SearchModeHybrid),
    docuindex.WithVectorWeight(0.5),
    docuindex.WithKeywordWeight(0.5),
)

// Search within specific document
results, err := store.SearchInDocument(docID, "query")

// Search with source/tag filtering
results, err := store.Search("query",
    docuindex.WithSources("crm", "pdf"),       // Filter by source or format
    docuindex.WithTags(map[string]string{"team": "sales"}),  // Filter by tags
)

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

// Configure embedding provider (optional)
provider, _ := embedding.NewProvider(embedding.Config{
    Provider: "ollama",
    Model:    "nomic-embed-text",
})
store.SetEmbeddingProvider(provider)

// Check embedding status for a document
hasEmb, err := store.HasEmbeddings(docID)  // Quick check

status, err := store.GetEmbeddingStatus(docID)  // Detailed status
// status.HasEmbeddings   - true if any embeddings exist
// status.IsComplete      - true if all embeddable blocks have vectors
// status.EmbeddedCount   - number of blocks with embeddings
// status.TotalEmbeddable - number of blocks that can be embedded
// status.Model           - embedding model used
// status.Dimension       - vector dimension
// status.LastUpdated     - when embeddings were last updated
// status.Progress()      - returns completion percentage (0-100)

// === AI/Agent Integration Features ===

// Index with progress callbacks (async-friendly)
doc, err := store.IndexDocumentWithProgress(path, func(p docuindex.IndexProgress) {
    fmt.Printf("[%s] %d/%d blocks\n", p.Status, p.ProcessedBlocks, p.TotalBlocks)
})

// Check for duplicate documents before indexing
result, err := store.CheckDuplicate("/path/to/file.pdf")
if result.IsDuplicate {
    fmt.Printf("Duplicate of: %s\n", result.ExistingName)
}

// Check duplicate by content bytes
result, err := store.CheckDuplicateByContent(fileBytes)

// Agent-friendly search with structured output
response, err := store.SearchForAgent("what is machine learning",
    docuindex.WithMaxResults(5),
)
// response.QueryType       - detected intent (factual, summary, etc.)
// response.EstimatedTokens - total tokens in results
// response.Results[0].CitationRef  - e.g., "[1]"
// response.Results[0].TokenCount   - tokens in this result

// Detect query intent
queryType := store.DetectQueryType("summarize the document")
// Returns: QueryTypeSummary

// Query type utilities
description := docuindex.QueryTypeDescription(queryType)  // "Summary/overview request"
searchMode := docuindex.SuggestedSearchMode(queryType)    // SearchModeHybrid

// Token estimation
tokens := docuindex.EstimateTokens("some text content")
budget := docuindex.NewTokenBudget(4096)
budget.AddText("content")
remaining := budget.Remaining()

// Content chunking for LLM context windows
chunks := docuindex.ChunkContent(longText, docuindex.ChunkOptions{
    MaxTokens:     512,
    OverlapTokens: 50,
    ChunkBy:       "paragraph",  // or "sentence", "tokens"
})

// Chunk search results to fit context
chunkedResults := docuindex.ChunkSearchResults(results, 2048)

// Filter DSL for advanced queries
filter := docuindex.NewFilter().
    Sources("pdf", "crm").
    Formats("pdf").
    After(time.Now().AddDate(0, -1, 0)).  // Last month
    MinPages(5).
    Tag("team", "sales").
    HasEmbeddings(true)

results, err := store.Search("query", docuindex.WithFilter(filter))
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
docuindex.WithDedupCheck(bool)         // Enable duplicate detection on indexing
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
docuindex.WithSearchMode(mode)         // keyword, semantic, or hybrid
docuindex.WithVectorWeight(float64)    // Weight for vector search (0-1)
docuindex.WithKeywordWeight(float64)   // Weight for keyword search (0-1)
docuindex.WithSources(...strings)      // Filter by source or format
docuindex.WithTags(map[string]string)  // Filter by tags (AND logic)
docuindex.WithImages(bool)             // Include image paths in results
docuindex.WithFilter(*Filter)          // Use Filter DSL for advanced filtering
docuindex.WithAgentOutput(bool)        // Return AgentSearchResponse format
docuindex.WithEstimateTokens(bool)     // Include token estimates in results
docuindex.WithCitations(bool)          // Add citation references [1], [2], etc.
docuindex.WithChunking(ChunkOptions)   // Configure result chunking
```

### Index Options
```go
docuindex.WithProgressCallback(fn)     // Receive indexing progress updates
```

### Embedding Providers
```go
// Azure OpenAI with API key (uses latest stable API version 2024-10-21 by default)
provider, _ := embedding.NewProvider(embedding.Config{
    Provider: "azure",
    Endpoint: os.Getenv("AZURE_ENDPOINT"),
    APIKey:   os.Getenv("AZURE_KEY"),
    Model:    "text-embedding-3-small",
    // APIVersion: "v1",              // Optional: use new v1 API format
    // APIVersion: "2024-10-21",      // Optional: explicit version (default)
})

// Azure OpenAI with Azure Identity (Managed Identity, DefaultAzureCredential, etc.)
// Requires: go get github.com/Azure/azure-sdk-for-go/sdk/azidentity
import "github.com/Azure/azure-sdk-for-go/sdk/azidentity"

cred, _ := azidentity.NewDefaultAzureCredential(nil)
provider, _ := embedding.NewProvider(embedding.Config{
    Provider:        "azure",
    Endpoint:        os.Getenv("AZURE_ENDPOINT"),
    Model:           "text-embedding-3-small",
    TokenCredential: cred,  // Uses Bearer token instead of api-key header
})
// Note: TokenCredential interface is compatible with Azure SDK's azcore.TokenCredential
// Tokens are cached and automatically refreshed 5 minutes before expiry

// OpenAI
provider, _ := embedding.NewProvider(embedding.Config{
    Provider: "openai",
    APIKey:   os.Getenv("OPENAI_API_KEY"),
    Model:    "text-embedding-3-small",
})

// Ollama (local)
provider, _ := embedding.NewProvider(embedding.Config{
    Provider: "ollama",
    Endpoint: "http://localhost:11434",
    Model:    "nomic-embed-text",
})
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
go run main.go index /path/to/file.pdf         # Index PDF
go run main.go index /path/to/file.docx        # Index DOCX
go run main.go index -debug /path/to/file.pdf  # Index with debug output (20 blocks)
go run main.go index -debug=100 /path/to/file.pdf  # Debug with 100 blocks
go run main.go index -progress /path/to/file.pdf   # Show progress during indexing
go run main.go search "query terms"
go run main.go list
go run main.go info <doc-id>
go run main.go full-test /path/to/file.pdf     # Run all tests with PDF
go run main.go full-test /path/to/file.docx    # Run all tests with DOCX
go run main.go search -show-images "query"     # Search with images in results
go run main.go images -doc <doc-id>            # List all images for document
go run main.go images -doc <doc-id> -section "Section Name"  # Filter by section
go run main.go images -doc <doc-id> -page 5    # Filter by page

# AI/Agent integration commands
go run main.go check-dup /path/to/file.pdf     # Check for duplicate documents
go run main.go search-agent "what is X"        # Agent-friendly search with citations
go run main.go search-agent -chunks "query"    # Show chunked results for LLM
go run main.go detect-intent "summarize this"  # Detect query intent type
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

## DOCX Features Supported

### Document Structure
- ZIP archive parsing via `archive/zip`
- XML content parsing via `encoding/xml`
- word/document.xml - Main content
- word/styles.xml - Style definitions with inheritance
- word/numbering.xml - List/numbering definitions
- word/_rels/document.xml.rels - Relationship files
- docProps/core.xml - Dublin Core metadata
- docProps/app.xml - Application properties (page count, word count)

### Content Extraction
- Paragraphs with text runs
- Style-based heading detection (Heading1-9)
- Font-based heading detection (size, bold)
- Bullet and numbered lists via numbering.xml
- Tables with row/column structure
- Inline and anchored images from word/media/
- Hyperlinks and bookmarks
- Field instructions (instrText) for TOC, page numbers

### Style Resolution
- Style inheritance chain (basedOn)
- Default paragraph/run properties (docDefaults)
- Merged properties from style hierarchy

### Image Formats
- JPEG, PNG, GIF, BMP, TIFF
- EMU to points conversion for dimensions
- Vector formats (EMF, WMF) detected but skipped

### Position Estimation
- Page estimation based on paragraph count
- BoundingBox with estimated Y position
- Letter page size (612x792 points) assumed

## Search Features

- **Full-text search**: BM25 on SQLite with inverted index
- **Semantic search**: Vector similarity via HNSW (optional)
- **Hybrid search**: Combined BM25 + vector with RRF fusion
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
| Modify search ranking | `sqlite/search.go` |
| Add new font type | `pdf/font.go` loadFont() |
| Modify tokenization | `sqlite/search.go` tokenize() |
| Add new DOCX element | `docx/types.go` XML structs |
| Modify DOCX style resolution | `docx/styles.go` |
| Add DOCX content extraction | `docx/text.go` or `docx/semantic.go` |
| Add embedding provider | `embedding/` directory |
| Modify hybrid search fusion | `search/fusion.go` |
| Add custom data indexing | `docuindex.go` IndexCustomData() |
| Add tag filtering | `sqlite/tags.go` |
| Check embedding status | `docuindex.go` GetEmbeddingStatus(), HasEmbeddings() |
| Query images by section/page | `sqlite/images.go` GetImagesBySection(), GetImagesByPage() |
| Include images in search | `docuindex.go` Search() with WithImages(true) |
| Index with progress | `docuindex.go` IndexDocumentWithProgress() |
| Check for duplicates | `docuindex.go` CheckDuplicate(), CheckDuplicateByContent() |
| Agent-friendly search | `docuindex.go` SearchForAgent() |
| Detect query intent | `query.go` DetectQueryType() |
| Estimate tokens | `tokens.go` EstimateTokens(), TokenBudget |
| Chunk content for LLM | `chunking.go` ChunkContent(), ChunkSearchResults() |
| Filter DSL queries | `filter.go` NewFilter() fluent API |
| Add dedup support | `sqlite/dedup.go` CheckDuplicateByChecksum() |
| Modify stop words | `internal/nlp/stopwords.go` IsStopWord() |
| Modify stemming | `internal/nlp/stemmer.go` Stem() |

## Dependencies

- `modernc.org/sqlite` - Pure Go SQLite driver
- `github.com/google/uuid` - UUID generation for images

Standard library:
- `archive/zip` - DOCX ZIP archive reading
- `encoding/xml` - DOCX XML parsing
- `compress/zlib` - FlateDecode decompression
- `compress/lzw` - LZW decompression
- `encoding/binary` - Binary data handling
- `encoding/json` - JSON serialization
- `image/*` - Image encoding/decoding
- `crypto/sha256` - Document checksums
- `database/sql` - SQLite interface

## Error Handling

### Sentinel Errors (`errors.go`)
- `ErrInvalidPDF`, `ErrCorruptedPDF` - Malformed PDF structure
- `ErrInvalidDOCX`, `ErrCorruptedDOCX`, `ErrMissingContent` - Malformed DOCX structure
- `ErrEncryptedPDF` - Encrypted PDFs not supported
- `ErrUnsupportedFeature`, `ErrUnsupportedEncoding` - Unimplemented features
- `ErrDocumentNotFound`, `ErrDocumentExists` - Storage errors
- `ErrSearchFailed`, `ErrInvalidQuery` - Search errors
- `ErrInvalidCustomData`, `ErrMissingSource`, `ErrMissingEntries` - CustomData errors

### Structured Error Types
- `ParseError` - PDF offset + operation context
- `ObjectError` - PDF object number/generation
- `PageError` - Page-specific errors
- `StreamError` - Filter decoding errors
- `FontError` - Font processing errors
- `DOCXError` - DOCX part + message
- `StorageError`, `SearchError` - Operation failures
- `CustomDataError` - CustomData source + message

### Error Checking
```go
if docuindex.IsParseError(err) { ... }
if docuindex.IsDOCXError(err) { ... }
if docuindex.IsStorageError(err) { ... }
if docuindex.IsSearchError(err) { ... }
if docuindex.IsCustomDataError(err) { ... }
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
- SQLite WAL mode enables concurrent reads

## Performance Considerations

- Lazy loading: Page content loaded on demand
- Streaming: Large files processed without full memory load
- Caching: Frequently accessed objects cached
- Parallel: Multi-page extraction can run concurrently
- HNSW: Approximate nearest neighbor for fast vector search
- SQLite page cache: Reduced I/O for repeated queries

## Scale Considerations

| Block Count | Search Type | Expected Performance |
|-------------|-------------|---------------------|
| <10k | BM25 or Brute-force vector | <10ms |
| 10k-100k | BM25 + HNSW | <50ms |
| >100k | BM25 + HNSW (tuned) | May need optimization |

## Current Limitations

- Encrypted PDFs not supported
- JBIG2Decode filter is a stub
- CCITTFaxDecode has limited support
- DOCX position estimation is approximate (no exact positions like PDF)
- DOCX vector images (EMF, WMF) are skipped
