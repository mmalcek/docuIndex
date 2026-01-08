# DocuIndex - Deferred Features and Future Enhancements

This document tracks features that were identified during development but deferred for future implementation.

## Deferred Enhancements

### 1. Streaming/Async API (Full Implementation)

**Status:** Partially implemented (progress callbacks added)

**What's Done:**
- `IndexDocumentWithProgress()` with `ProgressCallback` for status updates
- `IndexProgress` struct with status, timing, and progress information

**What's Remaining:**
- Channel-based async API for non-blocking document processing
- Cancellation support via `context.Context`
- Concurrent multi-document indexing with work queues
- Event streaming for real-time progress updates

**Example of potential API:**
```go
// Future: Channel-based async processing
resultChan := store.IndexDocumentAsync(ctx, path)
for result := range resultChan {
    switch result.Type {
    case "progress":
        fmt.Printf("Progress: %d%%\n", result.Progress)
    case "complete":
        fmt.Printf("Done: %s\n", result.Document.Info.ID)
    case "error":
        log.Printf("Error: %v\n", result.Error)
    }
}
```

---

### 2. Reranking API (Enhancement #3)

**Status:** Not implemented

**Description:**
LLM-based cross-encoder reranking for improved result relevance. This would use a language model to score query-document pairs for more accurate ranking.

**Proposed API:**
```go
// Rerank results using LLM
reranked, err := store.Rerank(results, query, docuindex.RerankOptions{
    Model:      "cross-encoder/ms-marco-MiniLM-L-6-v2",
    TopK:       10,
    MaxTokens:  512,
})
```

**Implementation Notes:**
- Requires additional embedding provider methods
- May need new `Reranker` interface
- Consider caching rerank scores for repeated queries

---

### 3. Batch Operations API (Enhancement #7)

**Status:** Not implemented

**Description:**
Parallel multi-document indexing with progress tracking and error handling.

**Proposed API:**
```go
// Index multiple documents in parallel
results, err := store.IndexBatch(paths, docuindex.BatchOptions{
    Concurrency:      4,
    ContinueOnError:  true,
    ProgressCallback: func(p BatchProgress) { ... },
})

for _, r := range results {
    if r.Error != nil {
        log.Printf("Failed: %s - %v\n", r.Path, r.Error)
    } else {
        fmt.Printf("Indexed: %s\n", r.Document.Info.ID)
    }
}
```

---

### 4. Export/Import Bundle (Enhancement #9)

**Status:** Not implemented

**Description:**
Portable index format for deployment and backup. Allows exporting the entire index (documents, vectors, HNSW) to a single file and importing on another system.

**Proposed API:**
```go
// Export index to portable bundle
err := store.ExportBundle("/path/to/backup.docuindex")

// Import on another system
err := store.ImportBundle("/path/to/backup.docuindex")

// Export with options
err := store.ExportBundle(path, docuindex.ExportOptions{
    IncludeImages:    true,
    IncludeVectors:   true,
    Compress:         true,
})
```

---

### 5. Logging Interface

**Status:** Not implemented

**Description:**
Replace `fmt.Printf` calls with proper structured logging interface.

**Proposed API:**
```go
type Logger interface {
    Debug(msg string, fields ...Field)
    Info(msg string, fields ...Field)
    Warn(msg string, fields ...Field)
    Error(msg string, fields ...Field)
}

// Configure custom logger
store, err := docuindex.NewStore(path,
    docuindex.WithLogger(myLogger),
)
```

---

### 6. Stop Words Configuration

**Status:** Not implemented

**Description:**
Per-language and per-domain stop words configuration for better search relevance.

**Proposed API:**
```go
// Custom stop words
store, err := docuindex.NewStore(path,
    docuindex.WithStopWords(true),
    docuindex.WithStopWordsList([]string{"custom", "words"}),
    docuindex.WithLanguage("en"),  // Language-specific stop words
)
```

---

### 7. Query Result Caching

**Status:** Not implemented

**Description:**
Cache frequently executed searches for faster response times.

**Proposed API:**
```go
// Enable query caching
store, err := docuindex.NewStore(path,
    docuindex.WithQueryCache(true),
    docuindex.WithQueryCacheSize(1000),
    docuindex.WithQueryCacheTTL(5 * time.Minute),
)

// Manual cache control
store.InvalidateQueryCache()
store.InvalidateQueryCacheForDocument(docID)
```

---

### 8. ZIP Bomb Protection

**Status:** Not implemented

**Description:**
Size limits for DOCX decompression to prevent ZIP bomb attacks.

**Implementation Notes:**
- Limit total decompressed size
- Limit individual file sizes within the ZIP
- Limit number of files in the archive
- Add configuration options for limits

---

## Minor Improvements

### Code Quality
- [ ] Add comprehensive unit tests for new features
- [ ] Add integration tests for agent workflow
- [ ] Benchmark token estimation accuracy
- [ ] Profile memory usage during large document indexing

### Documentation
- [ ] Add examples directory with common use cases
- [ ] Document Filter DSL patterns
- [ ] Add agent integration cookbook

### Performance
- [ ] Optimize HNSW incremental saves
- [ ] Add connection pooling for embedding providers
- [ ] Batch token estimation for large documents

---

## Implementation Priority

| Feature | Complexity | Impact | Suggested Priority |
|---------|------------|--------|-------------------|
| Batch Operations API | Medium | High | 1 |
| Export/Import Bundle | Medium | High | 2 |
| Reranking API | High | Medium | 3 |
| Logging Interface | Low | Medium | 4 |
| Query Result Caching | Medium | Medium | 5 |
| Stop Words Config | Low | Low | 6 |
| Full Async API | High | Medium | 7 |
| ZIP Bomb Protection | Low | Low | 8 |
