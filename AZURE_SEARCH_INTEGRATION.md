# Azure AI Search Integration for Vector Indexing

## Overview
Proposal, not implemented yet

This document describes how to add optional Azure AI Search support to offload HNSW vector indexing operations. This eliminates the performance bottleneck of local HNSW persistence while keeping SQLite as the source of truth.

## Problem Statement

The current local HNSW implementation has performance bottlenecks:

1. **HNSW saved after every document** (`docuindex.go:323-326`, `docuindex.go:2988-2991`)
   - ~74MB file for 10k vectors = 740ms/doc I/O
   - For 1000 documents: 740+ seconds = 12+ minutes just for I/O

2. **Vectors added one-by-one** (`docuindex.go:317-320`)
   - Not using existing `AddBatch()` method
   - Lock acquired/released for each vector

3. **Inefficient serialization** (`vectorindex/hnsw.go:657-728`)
   - Per-element byte allocations
   - No compression or delta encoding

## Solution: Azure AI Search as Optional Backend

Azure AI Search provides a managed HNSW service with REST API:
- API version: `2025-09-01` (latest GA with HNSW support)
- Batch up to 1000 documents, 16MB max payload
- HNSW params: `m` (4-10), `efConstruction` (100-1000), `efSearch` (100-1000)
- Authentication: API key or Azure AD (TokenCredential)

**Reference:** https://learn.microsoft.com/en-us/azure/search/vector-search-how-to-create-index

---

## Implementation Plan

### Step 1: Create VectorIndex Interface

**New file: `vectorindex/index.go`**

```go
package vectorindex

import "context"

// VectorIndex defines the interface for vector storage and search backends.
type VectorIndex interface {
    Add(ctx context.Context, id string, vector []float32) error
    AddBatch(ctx context.Context, items []VectorItem) error
    Delete(ctx context.Context, id string) error
    DeleteBatch(ctx context.Context, ids []string) error
    Search(ctx context.Context, query []float32, k int) ([]SearchResult, error)
    Size(ctx context.Context) (int, error)
    Sync(ctx context.Context) error  // Save to disk (HNSW) or no-op (Azure)
    Close() error
    Name() string
}

type VectorItem struct {
    ID     string
    Vector []float32
}
```

### Step 2: Create HNSW Adapter

**New file: `vectorindex/hnsw_adapter.go`**

Wraps existing `*HNSW` to implement `VectorIndex` interface.

### Step 3: Create Azure AI Search Implementation

**New file: `vectorindex/azure.go`**

Key features:
- REST API client (api-version 2025-09-01)
- Authentication: API key or TokenCredential (Azure AD)
- Batch operations (up to 1000 docs, 16MB max)
- Auto-create index with HNSW config on first use

### Step 4: Update Configuration

**Modify: `options.go`**

```go
type AzureSearchConfig struct {
    Endpoint        string          // Required: https://myservice.search.windows.net
    IndexName       string          // Required
    APIKey          string          // API key auth
    TokenCredential TokenCredential // Azure AD auth (alternative)
    Dimension       int             // Vector dimension
    M              int              // HNSW m (4-10, default: 4)
    EfConstruction int              // (100-1000, default: 400)
    EfSearch       int              // (100-1000, default: 500)
}

func WithAzureSearch(cfg AzureSearchConfig) StoreOption
```

### Step 5: Update Store

**Modify: `docuindex.go`**

- Change `hnsw *vectorindex.HNSW` to `vectorIndex vectorindex.VectorIndex`
- Select backend in `NewStore()` based on config
- Use `AddBatch()` in `embedDocument()`
- Remove per-document `SaveToFile()` calls

---

## Files to Create

| File | Description |
|------|-------------|
| `vectorindex/index.go` | VectorIndex interface |
| `vectorindex/hnsw_adapter.go` | Adapter wrapping existing HNSW |
| `vectorindex/azure.go` | Azure AI Search implementation |

## Files to Modify

| File | Changes |
|------|---------|
| `options.go` | Add `AzureSearchConfig`, `WithAzureSearch()` |
| `docuindex.go` | Use `VectorIndex` interface, batch operations |
| `errors.go` | Add `VectorIndexError` type |

---

## Usage Examples

### Default Local HNSW (No Changes)

```go
store, err := docuindex.NewStore("/path/to/data")
```

### With Azure AI Search

```go
store, err := docuindex.NewStore("/path/to/data",
    docuindex.WithAzureSearch(docuindex.AzureSearchConfig{
        Endpoint:  "https://myservice.search.windows.net",
        IndexName: "docuindex-vectors",
        APIKey:    os.Getenv("AZURE_SEARCH_KEY"),
        Dimension: 1536,
    }),
)
```

### With Azure AD Authentication

```go
cred, _ := azidentity.NewDefaultAzureCredential(nil)
store, _ := docuindex.NewStore("/path/to/data",
    docuindex.WithAzureSearch(docuindex.AzureSearchConfig{
        Endpoint:        "https://myservice.search.windows.net",
        IndexName:       "docuindex-vectors",
        TokenCredential: cred,
        Dimension:       1536,
    }),
)
```

---

## Performance Comparison

| Metric | Local HNSW | Azure AI Search |
|--------|------------|-----------------|
| Indexing 1000 docs | ~12 min I/O overhead | ~10 sec (batch API) |
| Search latency | <10ms | ~50-100ms (network) |
| Memory usage | All vectors in RAM | Managed by Azure |
| Offline support | Yes | No |

**Recommendation:**
- Use **Azure** for bulk imports and cloud deployments
- Use **local HNSW** for low-latency search and offline use

---

## Azure Limits

| Limit | Value |
|-------|-------|
| Max batch size | 1000 documents |
| Max payload size | 16 MB |
| Max dimensions | 3072 |
| HNSW m parameter | 4-10 |

**Reference:** https://learn.microsoft.com/en-us/azure/search/search-limits-quotas-capacity

---

## Checklist

- [ ] Create `vectorindex/index.go` with VectorIndex interface
- [ ] Create `vectorindex/hnsw_adapter.go` wrapping existing HNSW
- [ ] Create `vectorindex/azure.go` with Azure AI Search implementation
- [ ] Update `options.go` with AzureSearchConfig and WithAzureSearch
- [ ] Update `docuindex.go` to use VectorIndex interface
- [ ] Add error types to `errors.go`
- [ ] Update CLAUDE.md documentation
- [ ] Add tests
