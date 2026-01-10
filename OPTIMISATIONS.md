# DocuIndex Performance Optimizations

This document covers performance tuning for bulk indexing operations.

## Performance Bottlenecks

### HNSW Index Construction

The HNSW (Hierarchical Navigable Small World) graph has O(log n) insertion complexity, but with high `EfConst` values, each insertion explores many candidates. As the graph grows, this compounds:

| Records | Approx Time per 50 |
|---------|-------------------|
| 0-500 | 1-5 min |
| 500-1000 | 5-10 min |
| 1000-2000 | 10-20 min |
| 2000+ | 20-30+ min |

### Global Statistics Recalculation

BM25 search requires document statistics. Currently recalculated after each document:
- `COUNT(*)` over all documents
- `AVG(total_terms)` over all documents

For bulk imports, this causes O(n^2) total work.

### Sequential Operations

- Image I/O: ~5-10ms per image (synchronous disk write)
- Embedding API: ~100-500ms per batch (network latency)
- HNSW persistence: ~100ms-1s per save (depends on index size)

## Configuration Options

### HNSW Parameters

```go
store, _ := docuindex.NewStore("./data",
    docuindex.WithHNSWConfig(docuindex.HNSWConfig{
        M:        16,   // Max connections per layer (memory vs quality)
        EfConst:  200,  // Construction thoroughness (speed vs quality)
        EfSearch: 50,   // Search thoroughness (speed vs recall)
    }),
)
```

**Parameter Guide:**

| Parameter | Lower Value | Higher Value | Default |
|-----------|------------|--------------|---------|
| M | Less memory, faster insert | Better connectivity, slower insert | 16 |
| EfConst | Faster construction, lower recall | Slower construction, better recall | 200 |
| EfSearch | Faster search, lower recall | Slower search, better recall | 50 |

**Recommended Settings by Dataset Size:**

| Dataset Size | EfConst | EfSearch | Notes |
|--------------|---------|----------|-------|
| < 10k blocks | 200 | 50 | Default - balanced |
| 10k-50k blocks | 100 | 100 | Faster construction |
| 50k-100k blocks | 64 | 100 | Prioritize speed |
| > 100k blocks | 64 | 200 | Consider batch mode |

### Deferred Embedding

For bulk imports, defer embedding to avoid repeated HNSW updates:

```go
// Index documents without embedding
for _, data := range allData {
    _, err := store.IndexCustomData(data,
        docuindex.WithDeferEmbedding(true),
    )
}

// Generate embeddings in optimized batch
err := store.EmbedPendingDocuments()
```

### Batch Indexing

For large imports, use batch method:

```go
docs, err := store.IndexCustomDataBatch(allData,
    docuindex.WithDeferEmbedding(true),
)

// Finalize embeddings
err = store.EmbedPendingDocuments()
```

Benefits:
- Single SQLite transaction for all documents
- Deferred global statistics update (once at end)
- Batch HNSW persistence
- Optional deferred embedding

## Recommended Design Patterns

### Pattern 1: Two-Phase Bulk Import

Best for initial data loads (>1000 records):

```go
// Phase 1: Fast indexing (no embeddings)
store, _ := docuindex.NewStore("./data",
    docuindex.WithHNSWConfig(docuindex.HNSWConfig{
        EfConst: 64, // Fast construction
    }),
)

docs, err := store.IndexCustomDataBatch(allData,
    docuindex.WithDeferEmbedding(true),
)
log.Printf("Indexed %d documents", len(docs))

// Phase 2: Batch embedding
err = store.EmbedPendingDocuments()
log.Println("Embeddings complete")
```

### Pattern 2: Incremental Sync with Batching

Best for ongoing synchronization:

```go
func syncFromSource(store *docuindex.Store, source string) error {
    // Get last sync time
    lastImport, _ := store.GetLastImportTime(source)

    // Fetch changed records from external source
    records := fetchChangedRecords(source, lastImport)

    if len(records) == 0 {
        return nil
    }

    // Batch by chunks of 100
    const batchSize = 100
    for i := 0; i < len(records); i += batchSize {
        end := min(i+batchSize, len(records))
        batch := records[i:end]

        // Convert to CustomData slice
        var data []*docuindex.CustomData
        for _, r := range batch {
            data = append(data, convertToCustomData(r))
        }

        // Index batch with deferred embedding
        _, err := store.IndexCustomDataBatch(data,
            docuindex.WithDeferEmbedding(true),
        )
        if err != nil {
            return err
        }

        log.Printf("Indexed %d/%d records", end, len(records))
    }

    // Finalize embeddings for all new documents
    return store.EmbedPendingDocuments()
}
```

### Pattern 3: Resumable Maintenance Task

For scheduled background processing that survives interruptions:

```go
func runEmbeddingMaintenance(store *docuindex.Store) error {
    // Find all documents without embeddings
    pending, err := store.GetDocumentsWithoutEmbeddings()
    if err != nil {
        return err
    }

    if len(pending) == 0 {
        log.Println("No pending documents")
        return nil
    }

    log.Printf("Found %d documents without embeddings", len(pending))

    // Process in batches (resumable if interrupted)
    const batchSize = 50
    for i := 0; i < len(pending); i += batchSize {
        end := min(i+batchSize, len(pending))
        batch := pending[i:end]

        // Get just the IDs
        var docIDs []string
        for _, doc := range batch {
            docIDs = append(docIDs, doc.ID)
        }

        // Embed this batch
        err := store.EmbedDocuments(docIDs...)
        if err != nil {
            log.Printf("Error at batch %d: %v", i/batchSize, err)
            return err // Will resume from here on next run
        }

        log.Printf("Embedded %d/%d documents", end, len(pending))
    }

    return nil
}

// Schedule this to run periodically or on startup:
// - If interrupted, next run picks up where it left off
// - New documents indexed with WithDeferEmbedding(true) get processed
```

### Pattern 4: Progress Tracking for Large Imports

```go
totalRecords := len(allData)
indexed := 0

for _, data := range allData {
    _, err := store.IndexCustomData(data,
        docuindex.WithDeferEmbedding(true),
    )
    if err != nil {
        log.Printf("Failed to index %s: %v", data.Name, err)
        continue
    }

    indexed++

    // Progress every 50 records
    if indexed%50 == 0 {
        log.Printf("Progress: %d/%d (%.1f%%)",
            indexed, totalRecords,
            float64(indexed)/float64(totalRecords)*100)
    }
}

log.Println("Starting embedding generation...")
err := store.EmbedPendingDocuments()
```

## Performance Comparison

**Before Optimization (5000 records):**
- Total time: ~10+ hours
- Time per 50 at end: ~27 minutes

**After Optimization (5000 records, batch mode):**
- Indexing phase: ~30 minutes
- Embedding phase: ~2-3 hours (API-bound)
- Total: ~3-4 hours

## API Reference

### New Methods

```go
// Get documents that need embeddings
docs, err := store.GetDocumentsWithoutEmbeddings()

// Embed specific documents by ID
err := store.EmbedDocuments(docID1, docID2, ...)

// Embed ALL documents that don't have embeddings yet
err := store.EmbedPendingDocuments()
```

### New Options

```go
// Store creation with HNSW config
store, _ := docuindex.NewStore("./data",
    docuindex.WithHNSWConfig(docuindex.HNSWConfig{
        M:        16,   // Max connections (4-64, default 16)
        EfConst:  64,   // Construction ef (10-500, default 200)
        EfSearch: 100,  // Search ef (10-500, default 50)
    }),
)

// Index without embedding
doc, err := store.IndexCustomData(data,
    docuindex.WithDeferEmbedding(true),
)

// Batch index
docs, err := store.IndexCustomDataBatch(allData,
    docuindex.WithDeferEmbedding(true),
)
```

## Troubleshooting

### Slow Initial Load

1. Reduce EfConst to 64 for construction
2. Use `WithDeferEmbedding(true)`
3. Disable image extraction if not needed: `WithImageExtraction(false)`
4. Disable checksums if not needed: `WithChecksum(false)`

### Memory Usage

HNSW memory grows with:
- Number of vectors: ~4 bytes x dimension x vectors
- M parameter: more connections = more memory

For 100k vectors with dimension 1536:
- Base: ~600MB for vectors
- Graph: ~50-100MB for connections (M=16)

### Search Quality

If recall drops after reducing EfConst:
- Increase EfSearch at query time
- Rebuild index with higher EfConst (requires re-indexing)

### Interrupted Embedding

If embedding was interrupted:
```go
// Run maintenance to complete pending embeddings
pending, _ := store.GetDocumentsWithoutEmbeddings()
log.Printf("%d documents need embedding", len(pending))
err := store.EmbedPendingDocuments()
```
