# HNSW Optimization Plan

## Problem Summary

`EmbedPendingDocuments()` is slow because:

1. **HNSW saved after EVERY document** - N saves for N documents
2. **Vectors added one-by-one** - `Add()` called in loop instead of `AddBatch()`
3. **`AddBatch()` already exists but is unused!**

### Current Flow (Slow)
```
EmbedPendingDocuments()
  └─ for each document:
       └─ embedDocumentUnlocked()
            ├─ for each vector: s.hnsw.Add()     ← N lock acquisitions
            └─ s.hnsw.SaveToFile()               ← SAVES EVERY TIME!
```

### Target Flow (Fast)
```
EmbedPendingDocuments()
  └─ for each document:
       └─ embedDocumentUnlockedDeferred()
            └─ s.hnsw.AddBatch(vectors)          ← 1 lock acquisition
  └─ s.hnsw.SaveToFile()                         ← SAVE ONCE AT END
```

---

## Option 1: Core Optimization (Required)

### Changes Overview

| File | Function | Change |
|------|----------|--------|
| `docuindex.go` | `embedDocumentUnlocked()` | Remove SaveToFile, use AddBatch |
| `docuindex.go` | `EmbedPendingDocuments()` | Add single SaveToFile at end |
| `docuindex.go` | `EmbedDocuments()` | Add single SaveToFile at end |
| `docuindex.go` | `embedDocument()` | Use AddBatch (optional, already efficient) |
| `docuindex.go` | `ResumeEmbedding()` | Use AddBatch |
| `docuindex.go` | `SetEmbeddingProvider()` | Use AddBatch |
| `docuindex.go` | `Repair()` | Use AddBatch |

---

### Step 1: Modify `embedDocumentUnlocked()` (lines 2926-2994)

**Current code (lines 2982-2991):**
```go
// Add to HNSW index
for _, v := range vectors {
    id := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
    s.hnsw.Add(id, v.Vector)
}

// Persist HNSW index
hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
if err := s.hnsw.SaveToFile(hnswPath); err != nil {
    return fmt.Errorf("save HNSW index: %w", err)
}
```

**New code:**
```go
// Add to HNSW index using batch operation
hnswItems := make([]vectorindex.VectorItem, len(vectors))
for i, v := range vectors {
    hnswItems[i] = vectorindex.VectorItem{
        ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
        Vector: v.Vector,
    }
}
if err := s.hnsw.AddBatch(hnswItems); err != nil {
    return fmt.Errorf("add vectors to HNSW: %w", err)
}

// NOTE: Caller is responsible for saving HNSW index
// This allows batch operations to save once at the end
```

---

### Step 2: Modify `EmbedPendingDocuments()` (lines 2615-2660)

**Add save at end (after line 2657, before final return):**
```go
// Save HNSW index once after all documents processed
if s.hnsw.IsDirty() {
    hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
    if err := s.hnsw.SaveToFile(hnswPath); err != nil {
        return fmt.Errorf("save HNSW index: %w", err)
    }
}

return nil
```

**Note:** Need to acquire lock for the final save since we release lock between documents.

**Full modified function:**
```go
func (s *Store) EmbedPendingDocuments() error {
    if s.embedder == nil {
        return fmt.Errorf("no embedding provider configured")
    }

    // Get documents without embeddings
    s.mu.RLock()
    pending, err := s.db.GetDocumentsWithoutEmbeddings()
    s.mu.RUnlock()
    if err != nil {
        return fmt.Errorf("get pending documents: %w", err)
    }

    if len(pending) == 0 {
        return nil
    }

    // Process each document
    for _, docID := range pending {
        s.mu.Lock()

        // Get document
        doc, err := s.db.GetDocument(docID)
        if err != nil {
            s.mu.Unlock()
            return fmt.Errorf("get document %s: %w", docID, err)
        }

        // Get blocks
        blocks, err := s.db.GetBlocks(docID)
        if err != nil {
            s.mu.Unlock()
            return fmt.Errorf("get blocks for %s: %w", docID, err)
        }

        mainDoc := &Document{
            Info:    doc,
            Content: &Content{Blocks: blocks},
        }

        // Embed document (no longer saves HNSW internally)
        if err := s.embedDocumentUnlocked(mainDoc); err != nil {
            s.mu.Unlock()
            return fmt.Errorf("embed document %s: %w", docID, err)
        }

        s.mu.Unlock()
    }

    // Save HNSW index ONCE after all documents processed
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.hnsw.IsDirty() {
        hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
        if err := s.hnsw.SaveToFile(hnswPath); err != nil {
            return fmt.Errorf("save HNSW index: %w", err)
        }
    }

    return nil
}
```

---

### Step 3: Modify `EmbedDocuments()` (lines 2576-2611)

**Add save at end (before return nil):**
```go
// Save HNSW index once after all documents processed
if s.hnsw.IsDirty() {
    hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
    if err := s.hnsw.SaveToFile(hnswPath); err != nil {
        return fmt.Errorf("save HNSW index: %w", err)
    }
}

return nil
```

---

### Step 4: Update other functions to use AddBatch

**`embedDocument()` (lines 317-320):**
```go
// Current:
for _, v := range vectors {
    id := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
    s.hnsw.Add(id, v.Vector)
}

// New:
hnswItems := make([]vectorindex.VectorItem, len(vectors))
for i, v := range vectors {
    hnswItems[i] = vectorindex.VectorItem{
        ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
        Vector: v.Vector,
    }
}
if err := s.hnsw.AddBatch(hnswItems); err != nil {
    return fmt.Errorf("add vectors to HNSW: %w", err)
}
```

**`ResumeEmbedding()` (lines 2747-2752):**
Same pattern - convert loop to AddBatch.

**`SetEmbeddingProvider()` (lines 132-137):**
Same pattern - convert loop to AddBatch.

**`Repair()` (lines 2880-2890):**
Same pattern - convert loop to AddBatch.

---

## Option 3: Background HNSW Building (Optional Feature)

### Design

Add ability to build HNSW index in background while main operations continue. Since SQLite is the source of truth, HNSW can be rebuilt anytime.

### New Types

```go
// BackgroundIndexStatus represents the status of background HNSW building
type BackgroundIndexStatus struct {
    Running      bool      // Is background build in progress
    StartedAt    time.Time // When build started
    DocumentsTotal int     // Total documents to process
    DocumentsDone  int     // Documents processed so far
    VectorsTotal   int     // Total vectors to index
    VectorsDone    int     // Vectors indexed so far
    Error        error     // Error if failed
}
```

### New Methods

```go
// EmbedPendingDocumentsAsync starts embedding in background
// Returns immediately. Use GetBackgroundStatus() to check progress.
func (s *Store) EmbedPendingDocumentsAsync() error

// GetBackgroundStatus returns the status of background embedding
func (s *Store) GetBackgroundStatus() BackgroundIndexStatus

// IsBackgroundRunning returns true if background embedding is in progress
func (s *Store) IsBackgroundRunning() bool

// WaitForBackground blocks until background embedding completes
func (s *Store) WaitForBackground() error

// CancelBackground cancels background embedding (if running)
func (s *Store) CancelBackground()
```

### Implementation

**Add to Store struct:**
```go
type Store struct {
    // ... existing fields ...

    // Background embedding state
    bgMu       sync.Mutex
    bgRunning  bool
    bgCancel   context.CancelFunc
    bgStatus   BackgroundIndexStatus
    bgDone     chan struct{}
}
```

**EmbedPendingDocumentsAsync:**
```go
func (s *Store) EmbedPendingDocumentsAsync() error {
    s.bgMu.Lock()
    defer s.bgMu.Unlock()

    if s.bgRunning {
        return fmt.Errorf("background embedding already running")
    }

    if s.embedder == nil {
        return fmt.Errorf("no embedding provider configured")
    }

    // Get pending documents count
    s.mu.RLock()
    pending, err := s.db.GetDocumentsWithoutEmbeddings()
    s.mu.RUnlock()
    if err != nil {
        return err
    }

    if len(pending) == 0 {
        return nil // Nothing to do
    }

    ctx, cancel := context.WithCancel(context.Background())
    s.bgCancel = cancel
    s.bgRunning = true
    s.bgDone = make(chan struct{})
    s.bgStatus = BackgroundIndexStatus{
        Running:        true,
        StartedAt:      time.Now(),
        DocumentsTotal: len(pending),
    }

    go s.runBackgroundEmbedding(ctx, pending)

    return nil
}

func (s *Store) runBackgroundEmbedding(ctx context.Context, pending []string) {
    defer func() {
        s.bgMu.Lock()
        s.bgRunning = false
        s.bgStatus.Running = false
        close(s.bgDone)
        s.bgMu.Unlock()
    }()

    for i, docID := range pending {
        select {
        case <-ctx.Done():
            s.bgMu.Lock()
            s.bgStatus.Error = ctx.Err()
            s.bgMu.Unlock()
            return
        default:
        }

        s.mu.Lock()

        doc, err := s.db.GetDocument(docID)
        if err != nil {
            s.mu.Unlock()
            s.bgMu.Lock()
            s.bgStatus.Error = err
            s.bgMu.Unlock()
            return
        }

        blocks, err := s.db.GetBlocks(docID)
        if err != nil {
            s.mu.Unlock()
            s.bgMu.Lock()
            s.bgStatus.Error = err
            s.bgMu.Unlock()
            return
        }

        mainDoc := &Document{
            Info:    doc,
            Content: &Content{Blocks: blocks},
        }

        if err := s.embedDocumentUnlocked(mainDoc); err != nil {
            s.mu.Unlock()
            s.bgMu.Lock()
            s.bgStatus.Error = err
            s.bgMu.Unlock()
            return
        }

        s.mu.Unlock()

        // Update progress
        s.bgMu.Lock()
        s.bgStatus.DocumentsDone = i + 1
        s.bgMu.Unlock()
    }

    // Final save
    s.mu.Lock()
    if s.hnsw.IsDirty() {
        hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
        if err := s.hnsw.SaveToFile(hnswPath); err != nil {
            s.bgMu.Lock()
            s.bgStatus.Error = err
            s.bgMu.Unlock()
        }
    }
    s.mu.Unlock()
}

func (s *Store) GetBackgroundStatus() BackgroundIndexStatus {
    s.bgMu.Lock()
    defer s.bgMu.Unlock()
    return s.bgStatus
}

func (s *Store) IsBackgroundRunning() bool {
    s.bgMu.Lock()
    defer s.bgMu.Unlock()
    return s.bgRunning
}

func (s *Store) WaitForBackground() error {
    s.bgMu.Lock()
    if !s.bgRunning {
        err := s.bgStatus.Error
        s.bgMu.Unlock()
        return err
    }
    done := s.bgDone
    s.bgMu.Unlock()

    <-done

    s.bgMu.Lock()
    err := s.bgStatus.Error
    s.bgMu.Unlock()
    return err
}

func (s *Store) CancelBackground() {
    s.bgMu.Lock()
    defer s.bgMu.Unlock()

    if s.bgCancel != nil {
        s.bgCancel()
    }
}
```

### Usage Example

```go
// Start background embedding
if err := store.EmbedPendingDocumentsAsync(); err != nil {
    log.Fatal(err)
}

// Do other work while embedding runs...
for store.IsBackgroundRunning() {
    status := store.GetBackgroundStatus()
    fmt.Printf("Progress: %d/%d documents\n",
        status.DocumentsDone, status.DocumentsTotal)
    time.Sleep(time.Second)
}

// Or wait for completion
if err := store.WaitForBackground(); err != nil {
    log.Printf("Background embedding failed: %v", err)
}
```

---

## Performance Impact Estimate

### Before Optimization (N documents, M vectors each)

| Operation | Count | Notes |
|-----------|-------|-------|
| `hnsw.Add()` calls | N × M | Lock acquired each time |
| `hnsw.SaveToFile()` calls | N | ~740ms each for 10k vectors |
| **Total I/O time** | N × 740ms | For 1000 docs = 12+ minutes |

### After Optimization

| Operation | Count | Notes |
|-----------|-------|-------|
| `hnsw.AddBatch()` calls | N | 1 lock per document |
| `hnsw.SaveToFile()` calls | 1 | Single save at end |
| **Total I/O time** | ~740ms | Reduced from 12+ min to <1 sec |

**Expected speedup: 100x+ for bulk operations**

---

## Files to Modify

| File | Lines | Change |
|------|-------|--------|
| `docuindex.go` | 2982-2991 | `embedDocumentUnlocked()`: Use AddBatch, remove SaveToFile |
| `docuindex.go` | 2615-2660 | `EmbedPendingDocuments()`: Add single save at end |
| `docuindex.go` | 2576-2611 | `EmbedDocuments()`: Add single save at end |
| `docuindex.go` | 317-320 | `embedDocument()`: Use AddBatch |
| `docuindex.go` | 2003-2006 | `embedDocumentWithProgress()`: Use AddBatch |
| `docuindex.go` | 2747-2752 | `ResumeEmbedding()`: Use AddBatch |
| `docuindex.go` | 132-137 | `SetEmbeddingProvider()`: Use AddBatch |
| `docuindex.go` | 2880-2890 | `Repair()`: Use AddBatch |

---

## Testing Checklist

- [ ] `EmbedPendingDocuments()` saves HNSW only once
- [ ] `EmbedDocuments()` saves HNSW only once
- [ ] All Add loops replaced with AddBatch
- [ ] Search still works after batch embedding
- [ ] Interrupt recovery still works (SQLite has vectors, HNSW rebuilt on restart)
- [ ] Background embedding status tracking works
- [ ] Background embedding can be cancelled
- [ ] Concurrent searches work during background embedding

---

## API Documentation Updates

### CLAUDE.md additions

```markdown
## Bulk Import Optimization

For best performance when indexing many documents:

1. Use `WithDeferEmbedding(true)` during indexing
2. Call `EmbedPendingDocuments()` once at the end

This batches HNSW operations and saves the index only once.

### Background Embedding (Optional)

For non-blocking embedding:

\`\`\`go
// Start background embedding
store.EmbedPendingDocumentsAsync()

// Check progress
status := store.GetBackgroundStatus()
fmt.Printf("%d/%d done\n", status.DocumentsDone, status.DocumentsTotal)

// Wait for completion
err := store.WaitForBackground()

// Or cancel if needed
store.CancelBackground()
\`\`\`
```
