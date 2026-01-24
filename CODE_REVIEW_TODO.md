# DocuIndex Code Review - TODO Checklist

**Generated:** 2026-01-11
**Reviewer:** Claude Code Review
**Total Issues:** 100+

This document tracks all issues identified during the comprehensive code review. Check off items as they are fixed.

---

## Table of Contents

1. [Critical Security Issues](#1-critical-security-issues)
2. [Critical Data Integrity Bugs](#2-critical-data-integrity-bugs)
3. [Critical Algorithm Bugs](#3-critical-algorithm-bugs)
4. [High-Priority Performance Issues](#4-high-priority-performance-issues)
5. [Medium-Priority Error Handling](#5-medium-priority-error-handling)
6. [Medium-Priority Thread Safety](#6-medium-priority-thread-safety)
7. [Medium-Priority Missing Features](#7-medium-priority-missing-features)
8. [Low-Priority Code Quality](#8-low-priority-code-quality)
9. [Low-Priority API Design](#9-low-priority-api-design)
10. [Low-Priority Documentation](#10-low-priority-documentation)
11. [Testing Improvements](#11-testing-improvements)
12. [Future Optimizations](#12-future-optimizations)

---

## 1. Critical Security Issues

> **Priority:** CRITICAL - Fix before production deployment
> **Risk:** Security vulnerabilities, DoS attacks, data corruption

### 1.1 PDF Parser Security

- [ ] **SEC-PDF-001**: Integer overflow in predictor calculation
  - **File:** `pdf/stream.go:156`
  - **Issue:** `columns*colors*bitsPerComponent` can overflow int32 before division
  - **Impact:** Memory exhaustion, potential buffer overflow
  - **Fix:**
    ```go
    // Change from:
    bytesPerRow := (columns*colors*bitsPerComponent + 7) / 8

    // To:
    bytesPerRow := (int64(columns)*int64(colors)*int64(bitsPerComponent) + 7) / 8
    if bytesPerRow > int64(MaxDecompressedSize) {
        return nil, fmt.Errorf("predictor row size exceeds limit")
    }
    ```
  - **Test:** Add test with columns=100000, colors=100, bitsPerComponent=32

- [ ] **SEC-PDF-002**: XRef entry offset not validated
  - **File:** `pdf/xref.go:254-257`
  - **Issue:** Offset could be negative or beyond file size
  - **Impact:** Out-of-bounds read on malformed PDF
  - **Fix:** Add validation: `if offset < 0 || offset > fileSize { return error }`

- [ ] **SEC-PDF-003**: Missing XRef stream entry size validation
  - **File:** `pdf/xref.go:296-299`
  - **Issue:** W array could specify enormous entry sizes
  - **Impact:** Buffer overrun on malformed xref stream
  - **Fix:** Add check: `if entrySize > 1024 { return error }`

- [ ] **SEC-PDF-004**: CID width array parsing unbounded loop
  - **File:** `pdf/font.go:238-278`
  - **Issue:** W array with start=0, end=2147483647 creates billions of entries
  - **Impact:** Memory exhaustion
  - **Fix:** Add maximum range limit: `if c2-c1 > 65536 { return error }`

- [ ] **SEC-PDF-005**: Bounds check missing in array decoding
  - **File:** `pdf/font.go:253-256`
  - **Issue:** `int(c1)+j` could overflow/wrap negative
  - **Impact:** Out-of-bounds write
  - **Fix:** Add bounds check before array assignment

### 1.2 DOCX Parser Security

- [ ] **SEC-DOCX-001**: Zip bomb vulnerability
  - **File:** `docx/document.go:62-88`
  - **Issue:** `OpenBytes()` decompresses entire DOCX without size limits
  - **Impact:** Memory exhaustion on malicious files
  - **Fix:**
    ```go
    const MaxDOCXUncompressedSize = 500 * 1024 * 1024 // 500MB

    func OpenBytes(data []byte) (*Document, error) {
        // Track total uncompressed size during extraction
        // Return error if exceeds limit
    }
    ```

- [ ] **SEC-DOCX-002**: Path traversal in image paths
  - **File:** `docx/relationships.go:121-135`
  - **Issue:** Only removes `../` prefix, doesn't validate final path
  - **Impact:** Could access files outside word/media/
  - **Fix:**
    ```go
    func NormalizeImagePath(target string) string {
        normalized := filepath.Clean(target)
        normalized = strings.TrimPrefix(normalized, "word/")
        if !strings.HasPrefix(normalized, "media/") {
            return "" // Reject paths outside media/
        }
        if strings.Contains(normalized, "..") {
            return "" // Reject any remaining traversal
        }
        return normalized
    }
    ```

- [ ] **SEC-DOCX-003**: No string size limits
  - **File:** `docx/text.go`
  - **Issue:** Text extraction has no maximum length
  - **Impact:** Could extract extremely long paragraphs, memory exhaustion
  - **Fix:** Add MaxStringLength constant similar to PDF parser

### 1.3 Embedding Provider Security

- [ ] **SEC-EMB-001**: Unbounded response reading in Azure provider
  - **File:** `embedding/azure.go:218`
  - **Issue:** `io.ReadAll(resp.Body)` has no size limit
  - **Impact:** Malicious API could send gigabytes
  - **Fix:**
    ```go
    const MaxEmbeddingResponseSize = 100 * 1024 * 1024 // 100MB
    respBody, err := io.ReadAll(io.LimitReader(resp.Body, MaxEmbeddingResponseSize))
    ```

- [ ] **SEC-EMB-002**: Unbounded response reading in OpenAI provider
  - **File:** `embedding/openai.go:162`
  - **Issue:** Same as SEC-EMB-001
  - **Fix:** Same pattern with io.LimitReader

- [ ] **SEC-EMB-003**: Unbounded response reading in Ollama provider
  - **File:** `embedding/ollama.go:139`
  - **Issue:** Same as SEC-EMB-001
  - **Fix:** Same pattern with io.LimitReader

---

## 2. Critical Data Integrity Bugs

> **Priority:** CRITICAL - Can cause data loss or corruption
> **Risk:** Lost embeddings, incorrect search results, inconsistent state

### 2.1 Concurrent Data Access

- [ ] **DATA-001**: Lost updates in concurrent embedding
  - **File:** `sqlite/vectors.go:31-71`
  - **Issue:** `INSERT OR REPLACE` overwrites without reading previous state
  - **Scenario:**
    1. Job1 embeds blocks 1-50, commits
    2. Job2 embeds blocks 51-100, commits with OR REPLACE
    3. Job2's commit replaces Job1's vectors completely
  - **Impact:** Permanent data loss of embeddings
  - **Fix:** Use upsert with explicit merge or document-level locking
    ```go
    // Option 1: Use INSERT ... ON CONFLICT DO UPDATE
    // Option 2: Lock at document level during embedding
    // Option 3: Use transactions with version checking
    ```

- [ ] **DATA-002**: Race condition in dimension auto-detect
  - **File:** `embedding/azure.go:115-117`
  - **File:** `embedding/openai.go:117`
  - **File:** `embedding/ollama.go:94`
  - **Issue:** Multiple concurrent Embed() calls can race on dimension write
  - **Impact:** Incorrect dimension value, wrong vector matching
  - **Fix:**
    ```go
    // Add mutex protection
    p.mu.Lock()
    if p.dimension == 0 && len(result) > 0 && len(result[0]) > 0 {
        p.dimension = len(result[0])
    }
    p.mu.Unlock()

    // Or use atomic:
    atomic.CompareAndSwapInt32(&p.dimension, 0, int32(len(result[0])))
    ```

- [ ] **DATA-003**: Background embedding thread safety
  - **File:** `docuindex.go:40-45, 81-95`
  - **Issue:** `bgRunning`, `bgCancel`, `bgStatus` accessed without synchronization
  - **Impact:** Race conditions in background embedding state
  - **Fix:** Always use `bgMu` when accessing these fields
    ```go
    func (s *Store) IsBackgroundRunning() bool {
        s.bgMu.Lock()
        defer s.bgMu.Unlock()
        return s.bgRunning
    }
    ```

### 2.2 Transaction Handling

- [ ] **DATA-004**: No transaction support for upserts
  - **File:** `docuindex.go:803-844`
  - **Issue:** Multiple steps without transaction guarantee
  - **Scenario:** If SaveDocument fails after HNSW delete, index is corrupted
  - **Fix:** Wrap in transaction with rollback capability

- [ ] **DATA-005**: Silent error in HNSW vector retrieval during upsert
  - **File:** `docuindex.go:835`
  - **Issue:** `vectors, _ := s.db.GetVectorsForDocument(docID)` ignores error
  - **Impact:** Incomplete HNSW cleanup on upsert
  - **Fix:** Handle error and log warning

- [ ] **DATA-006**: Transaction isolation not configured
  - **File:** `sqlite/store.go:160-162`
  - **Issue:** Uses default isolation (DEFERRED) not IMMEDIATE
  - **Impact:** Write conflicts in high concurrency
  - **Fix:**
    ```go
    func (s *Store) beginTx() (*sql.Tx, error) {
        return s.db.BeginTx(context.Background(), &sql.TxOptions{
            Isolation: sql.LevelSerializable, // or use IMMEDIATE via raw SQL
        })
    }
    ```

---

## 3. Critical Algorithm Bugs

> **Priority:** CRITICAL - Produces incorrect results
> **Risk:** Wrong search results, incorrect document processing

### 3.1 Text Processing Bugs

- [ ] **ALG-001**: Chunk position tracking bug
  - **File:** `chunking.go:133-139`
  - **Issue:** `strings.Index` finds first occurrence, not the one being processed
  - **Scenario:** Text "the dog and the cat" - second "the" position incorrect
  - **Impact:** Incorrect StartIdx/EndIdx in chunks
  - **Fix:** Track position during unit iteration, not via strings.Index
    ```go
    // Maintain running position counter
    var runningPos int
    for _, unit := range units {
        unitPos := strings.Index(originalContent[runningPos:], unit)
        if unitPos >= 0 {
            absolutePos := runningPos + unitPos
            // Use absolutePos for positioning
            runningPos = absolutePos + len(unit)
        }
    }
    ```

- [ ] **ALG-002**: Unicode byte/rune length confusion in stemmer
  - **File:** `internal/nlp/stemmer.go:42, 48, 55`
  - **Issue:** Uses `len(word)` (bytes) instead of `utf8.RuneCountInString(word)` (characters)
  - **Impact:** Unicode words processed with wrong length calculations
  - **Example:** "café" = 5 bytes but 4 characters
  - **Fix:**
    ```go
    import "unicode/utf8"

    func Stem(word string) string {
        if utf8.RuneCountInString(word) <= 3 {
            return word
        }
        // ... similar fixes for lines 48, 55
    }
    ```

- [ ] **ALG-003**: Extra space in chunk overlap
  - **File:** `chunking.go:125-126`
  - **Issue:** Adds space even if overlap is empty
  - **Impact:** Malformed chunks with extra spaces
  - **Fix:**
    ```go
    if overlap != "" {
        currentContent.WriteString(overlap)
        if !strings.HasSuffix(overlap, " ") {
            currentContent.WriteString(" ")
        }
    }
    ```

### 3.2 Embedding Provider Bugs

- [ ] **ALG-004**: Ollama missing rate limit check
  - **File:** `embedding/ollama.go:144`
  - **Issue:** Doesn't check for HTTP 429 like Azure/OpenAI do
  - **Impact:** Silent failures when rate-limited
  - **Fix:**
    ```go
    if resp.StatusCode == 429 {
        return nil, ErrRateLimited
    }
    ```

- [ ] **ALG-005**: Ollama batch truncation on legacy response
  - **File:** `embedding/ollama.go:164-169`
  - **Issue:** If new API returns empty but legacy works, returns only 1 embedding
  - **Impact:** Batch requests silently truncated to single response
  - **Fix:** Verify response count matches input count

- [ ] **ALG-006**: Missing embedding index bounds validation
  - **File:** `embedding/azure.go:243`, `embedding/openai.go:187`
  - **Issue:** Response with Index >= len(texts) silently dropped
  - **Impact:** Missing embeddings not detected
  - **Fix:** Log warning when index out of bounds

### 3.3 Search Algorithm Bugs

- [ ] **ALG-007**: RRF default rank penalizes valid results
  - **File:** `search/fusion.go:70, 84`
  - **Issue:** Missing results get worst-possible rank (len+1)
  - **Impact:** Semantic-only or keyword-only results unfairly penalized
  - **Fix:** Use average rank or scaled rank for missing results
    ```go
    // Instead of: result.VectorRank = len(vectorResults) + 1
    // Use: result.VectorRank = (len(vectorResults) + 1) / 2
    ```

- [ ] **ALG-008**: PDF xref incremental update merging incorrect
  - **File:** `pdf/xref.go:191-202`
  - **Issue:** Doesn't handle objects marked as free in new xref
  - **Impact:** Freed objects may still be accessed
  - **Fix:** Check if object is freed before merging

---

## 4. High-Priority Performance Issues

> **Priority:** HIGH - Significant performance impact at scale
> **Risk:** Slow queries, timeouts, resource exhaustion

### 4.1 Database Performance

- [ ] **PERF-DB-001**: N+1 query for document names in search
  - **File:** `sqlite/search.go:281-283`
  - **Issue:** Runs one query per document match
  - **Impact:** 100 results = 100 queries
  - **Fix:** Use JOIN in initial query
    ```sql
    SELECT st.*, d.name
    FROM search_terms st
    JOIN documents d ON st.document_id = d.id
    WHERE st.term IN (?)
    ```

- [ ] **PERF-DB-002**: O(n×m) tag filtering queries
  - **File:** `sqlite/search.go:735-775`
  - **Issue:** One COUNT query per document per tag
  - **Impact:** 100 docs × 5 tags = 500 queries
  - **Fix:** Use INTERSECT pattern like `sqlite/tags.go:107`

- [ ] **PERF-DB-003**: Missing composite index on search_terms
  - **File:** `sqlite/schema.go`
  - **Issue:** No index on (term, document_id) combination
  - **Impact:** Full table scan for each query term
  - **Fix:**
    ```sql
    CREATE INDEX idx_search_term_doc ON search_terms(term, document_id);
    ```

- [ ] **PERF-DB-004**: Missing index on documents.created_at
  - **File:** `sqlite/schema.go`
  - **Issue:** Date range filters do full table scan
  - **Fix:**
    ```sql
    CREATE INDEX idx_documents_created ON documents(created_at);
    ```

- [ ] **PERF-DB-005**: Missing index on documents.page_count
  - **File:** `sqlite/schema.go`
  - **Issue:** Page count filters scan all documents
  - **Fix:**
    ```sql
    CREATE INDEX idx_documents_pages ON documents(page_count);
    ```

- [ ] **PERF-DB-006**: Unbounded result accumulation before limiting
  - **File:** `sqlite/search.go:279-322`
  - **Issue:** All results accumulated then sorted before limiting
  - **Impact:** 10M matches but only want 20 = loads all into memory
  - **Fix:** Use heap-based top-k algorithm
    ```go
    // Use container/heap to maintain top-k results
    type topKHeap []SearchResult
    // Push results, pop minimum if > k
    ```

- [ ] **PERF-DB-007**: updateGlobalStats recalculates all stats
  - **File:** `sqlite/search.go:150-169`
  - **Issue:** Scans entire search_terms table
  - **Impact:** Slow after every index operation
  - **Fix:** Use incremental updates or caching

### 4.2 PDF Parser Performance

- [ ] **PERF-PDF-001**: GetPage() recollects all pages every call
  - **File:** `pdf/page.go:142-157`
  - **Issue:** `collectPages()` traverses entire page tree for EACH page
  - **Impact:** O(N) per page access on large documents
  - **Fix:**
    ```go
    type Document struct {
        cachedPages []Reference  // Add cache
    }

    func (doc *Document) GetPage(pageNum int) (*Page, error) {
        if doc.cachedPages == nil {
            pages, err := collectPages(doc, pagesRef)
            if err != nil { return nil, err }
            doc.cachedPages = pages
        }
        return doc.cachedPages[pageNum-1], nil
    }
    ```

- [ ] **PERF-PDF-002**: Object cache never flushed
  - **File:** `pdf/document.go:22, 231`
  - **Issue:** All resolved objects cached indefinitely
  - **Impact:** Memory exhaustion on large PDFs
  - **Fix:** Implement LRU cache with configurable size limit
    ```go
    type lruCache struct {
        maxSize int
        cache   map[int]Object
        order   []int  // LRU order
    }
    ```

- [ ] **PERF-PDF-003**: Regex compiled in tight loops
  - **File:** `pdf/semantic.go:315, 326, 369`
  - **Issue:** `regexp.MustCompile()` called on every function invocation
  - **Impact:** Significant CPU overhead in semantic analysis
  - **Fix:**
    ```go
    // Package-level pre-compiled regex
    var (
        numberedPattern = regexp.MustCompile(`^[\d.]+\s+[A-Z]`)
        bulletPattern   = regexp.MustCompile(`^[\u2022\u2023\u25E6\u2043\u2219]`)
    )
    ```

- [ ] **PERF-PDF-004**: Text sorting repeated for large pages
  - **File:** `pdf/text.go:205-210`
  - **Issue:** Repeated sorting during line grouping
  - **Impact:** Slow text extraction on pages with many spans
  - **Fix:** Use more efficient line detection algorithm

### 4.3 HNSW Performance

- [ ] **PERF-HNSW-001**: String IDs in Friends lists
  - **File:** `vectorindex/hnsw.go:45, 149`
  - **Issue:** Friends stored as `[][]string` instead of indices
  - **Impact:** 8x memory overhead, slower lookups
  - **Fix:**
    ```go
    type node struct {
        Index   uint64
        Vector  []float32
        Friends [][]uint64  // Changed from [][]string
    }

    // Add ID-to-index mapping
    idToIndex map[string]uint64
    indexToID []string
    ```

- [ ] **PERF-HNSW-002**: Query vector re-embedding in hybrid search
  - **File:** `search/hybrid.go:203-211`
  - **Issue:** Query embedded inside vectorSearch, but also needed outside
  - **Impact:** Redundant embedding computation
  - **Fix:** Embed once in HybridSearch, pass to vectorSearch
    ```go
    func (hs *HybridSearcher) HybridSearch(...) {
        queryVector, err := hs.embedder.EmbedSingle(ctx, query)
        // Pass queryVector to vectorSearch instead of query string
    }
    ```

- [ ] **PERF-HNSW-003**: Vector normalization not cached
  - **File:** `vectorindex/hnsw.go:841-844`
  - **Issue:** cosineDistance recalculates norms each time
  - **Impact:** O(n×searches) sqrt calls
  - **Fix:** Pre-normalize vectors on insert, store as unit vectors
    ```go
    func (h *HNSW) Add(id string, vector []float32) error {
        normalized := normalize(vector)
        // Store normalized vector
    }

    func cosineDistanceNormalized(a, b []float32) float32 {
        // Just dot product for unit vectors
        return 1.0 - dotProduct(a, b)
    }
    ```

- [ ] **PERF-HNSW-004**: Vector loading loads all into memory
  - **File:** `sqlite/vectors.go:95-122`
  - **Issue:** GetAllVectors() loads everything unconditionally
  - **Impact:** Memory exhaustion with millions of vectors
  - **Fix:** Implement streaming/pagination for HNSW rebuild

### 4.4 General Performance

- [ ] **PERF-GEN-001**: JSON marshaling on hot path
  - **File:** `sqlite/blocks.go:70-72`
  - **Issue:** `jsonMarshal()` called for every block save
  - **Impact:** GC pressure with thousands of blocks
  - **Fix:** Consider object pooling for JSON encoders

- [ ] **PERF-GEN-002**: Token estimation counts unused categories
  - **File:** `tokens.go:28-42`
  - **Issue:** Counts letters, digits, spaces, punctuation, other - but only uses total and spaces
  - **Impact:** Wasted computation
  - **Fix:** Only count what's needed

---

## 5. Medium-Priority Error Handling

> **Priority:** MEDIUM - Silent failures, hard to debug
> **Risk:** Mysterious bugs, incomplete data

### 5.1 Silent Failures

- [ ] **ERR-001**: Silent image extraction failures in PDF
  - **File:** `docuindex.go:260, 402-403`
  - **Issue:** Errors silently skipped with `continue`
  - **Impact:** Users unaware of missing images
  - **Fix:** Collect errors and return in result or log warnings

- [ ] **ERR-002**: Silent image extraction failures in DOCX
  - **File:** `docuindex.go:495-496`, `docx/image.go:43-48`
  - **Issue:** Same as ERR-001
  - **Fix:** Same as ERR-001

- [ ] **ERR-003**: Embedding failures logged with fmt.Printf
  - **File:** `docuindex.go:275-280`
  - **Issue:** Uses `fmt.Printf` instead of proper logging
  - **Impact:** No way for callers to handle embedding failures
  - **Fix:**
    ```go
    type IndexResult struct {
        Document     *Document
        EmbeddingErr error  // Non-fatal embedding error
    }
    ```

- [ ] **ERR-004**: JSON unmarshal error ignored in search
  - **File:** `sqlite/search.go:272`
  - **Issue:** `json.Unmarshal([]byte(positionsJSON), &pos)` error ignored
  - **Impact:** Corrupted position data silently skipped
  - **Fix:** Log error and skip result with warning

- [ ] **ERR-005**: Checksum computation error ignored
  - **File:** `docuindex.go:379, 471`
  - **Issue:** `checksum, _ = computeChecksum(path)` blanks error
  - **Impact:** Documents without checksum if computation fails
  - **Fix:** Log error if checksum computation fails

- [ ] **ERR-006**: File deletion error ignored
  - **File:** `sqlite/images.go:49`
  - **Issue:** `os.Remove(imagePath)` error ignored
  - **Impact:** Orphaned files on disk
  - **Fix:** Log error or retry

- [ ] **ERR-007**: Time parse error ignored
  - **File:** `sqlite/vectors.go:287`
  - **Issue:** `time.Parse()` error ignored
  - **Impact:** Zero timestamps mask data corruption
  - **Fix:** Log warning on parse failure

### 5.2 Missing Error Context

- [ ] **ERR-008**: Token credential error not wrapped with context
  - **File:** `embedding/azure.go:156`
  - **Issue:** Error doesn't include endpoint/model for debugging
  - **Fix:** `fmt.Errorf("get token for %s: %w", p.endpoint, err)`

- [ ] **ERR-009**: Inconsistent error wrapping in PDF parser
  - **File:** `pdf/document.go`, `pdf/xref.go`
  - **Issue:** Some errors wrapped, some not
  - **Fix:** Standardize on `fmt.Errorf("operation: %w", err)`

### 5.3 Missing Retry-After Handling

- [ ] **ERR-010**: No Retry-After header handling in providers
  - **File:** `embedding/provider.go:163-196`
  - **Issue:** Ignores API-provided retry timing
  - **Fix:**
    ```go
    if resp.StatusCode == 429 {
        if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
            seconds, _ := strconv.Atoi(retryAfter)
            time.Sleep(time.Duration(seconds) * time.Second)
        }
    }
    ```

---

## 6. Medium-Priority Thread Safety

> **Priority:** MEDIUM - Potential race conditions
> **Risk:** Occasional crashes, data corruption under load

- [ ] **THREAD-001**: Image save holds lock during file I/O
  - **File:** `sqlite/images.go:26-49`
  - **Issue:** Holds mutex while writing to filesystem
  - **Impact:** Blocks all operations for 100ms+ per image
  - **Fix:** Release lock before file I/O, reacquire for metadata
    ```go
    func (s *Store) SaveImage(...) {
        // Generate path without lock
        imagePath := filepath.Join(s.imagesDir, imageID+"."+format)

        // Write file without lock
        if err := os.WriteFile(imagePath, data, 0644); err != nil {
            return "", err
        }

        // Lock only for metadata
        s.mu.Lock()
        defer s.mu.Unlock()
        // Insert metadata into DB
    }
    ```

- [ ] **THREAD-002**: Long I/O under lock during indexing
  - **File:** `docuindex.go:251-266`
  - **Issue:** Holds lock while saving images
  - **Fix:** Similar to THREAD-001

- [ ] **THREAD-003**: GetBlockByID not locked
  - **File:** `sqlite/blocks.go:149`
  - **Issue:** Called without lock verification
  - **Impact:** Could race with writes (low probability)
  - **Fix:** Add lock or document caller requirements

- [ ] **THREAD-004**: No random seeding verification
  - **File:** `vectorindex/hnsw.go:619`
  - **Issue:** Uses global rand without seeding
  - **Impact:** Non-deterministic level assignment
  - **Fix:** Seed in init() or use local rand source

---

## 7. Medium-Priority Missing Features

> **Priority:** MEDIUM - Documented but not implemented
> **Risk:** User confusion, feature gaps

### 7.1 DOCX Missing Features

- [ ] **FEAT-DOCX-001**: Headers and footers extraction
  - **File:** `docx/`
  - **Issue:** No extraction from word/header*.xml or word/footer*.xml
  - **Status:** Claimed in CLAUDE.md but not implemented

- [ ] **FEAT-DOCX-002**: Footnotes and endnotes
  - **File:** `docx/`
  - **Issue:** No FootnoteReference or EndnoteReference types
  - **Impact:** Academic papers missing content

- [ ] **FEAT-DOCX-003**: Track changes
  - **File:** `docx/`
  - **Issue:** No support for w:del, w:ins elements
  - **Impact:** Revision content completely lost

- [ ] **FEAT-DOCX-004**: Mathematical equations
  - **File:** `docx/`
  - **Issue:** No m:oMathPara or m:oMath elements
  - **Impact:** Math content not extracted

- [ ] **FEAT-DOCX-005**: Sections and section properties
  - **File:** `docx/`
  - **Issue:** w:sectPr not parsed
  - **Impact:** Page break information lost

- [ ] **FEAT-DOCX-006**: Table cell merge handling
  - **File:** `docx/types.go:410-412`
  - **Issue:** VMerge parsed but never used
  - **Impact:** Merged cells treated as independent

- [ ] **FEAT-DOCX-007**: Comments extraction
  - **File:** `docx/`
  - **Issue:** No w:commentStart, w:commentEnd support
  - **Impact:** Document comments lost

### 7.2 PDF Missing Features

- [ ] **FEAT-PDF-001**: JBIG2Decode filter
  - **File:** `pdf/stream.go:82`
  - **Issue:** Returns error, content skipped
  - **Impact:** Some PDFs with JBIG2 compressed images fail

- [ ] **FEAT-PDF-002**: Complete CCITTFaxDecode
  - **File:** `pdf/stream.go:451-468`
  - **Issue:** Limited stub implementation
  - **Impact:** Fax-compressed content may fail

---

## 8. Low-Priority Code Quality

> **Priority:** LOW - Code smells, maintainability
> **Risk:** Technical debt, harder maintenance

### 8.1 Code Organization

- [ ] **QUAL-001**: pendingImage struct declared mid-file
  - **File:** `docuindex.go:156-167`
  - **Issue:** Internal struct in middle of file
  - **Fix:** Move to types.go or group at file top

- [ ] **QUAL-002**: Helper functions lack documentation
  - **File:** `docuindex.go:200-206`
  - **Issue:** `sourceOrDefault()`, `isValidImageFormat()`, `detectImageDimensions()` undocumented
  - **Fix:** Add godoc comments

- [ ] **QUAL-003**: Magic numbers in heading detection
  - **File:** `docx/semantic.go:206, 211, 222`
  - **Issue:** 14, 200, 100 hardcoded
  - **Fix:** Extract to named constants
    ```go
    const (
        MinHeadingFontSize = 14
        MaxHeadingLength   = 200
        MaxAllCapsLength   = 100
    )
    ```

- [ ] **QUAL-004**: Magic numbers in PDF parser
  - **File:** `pdf/lexer.go:232`, `pdf/xref.go:176`
  - **Issue:** Undocumented buffer sizes
  - **Fix:** Extract to named constants with documentation

### 8.2 Unused Code

- [ ] **QUAL-005**: Token estimation constants defined but unused
  - **File:** `tokens.go`
  - **Issue:** `codeAdjustment`, `numbersAdjustment`, `whitespaceAdjust` never applied
  - **Fix:** Remove or implement code detection

### 8.3 Panic Risk

- [ ] **QUAL-006**: jsonMarshal can panic
  - **File:** `sqlite/documents.go:342`
  - **Issue:** `panic(fmt.Sprintf(...))` on marshal error
  - **Fix:** Return error instead of panicking

---

## 9. Low-Priority API Design

> **Priority:** LOW - Usability improvements
> **Risk:** User confusion, API inconsistency

### 9.1 Naming Issues

- [ ] **API-001**: defer_ parameter naming
  - **File:** `options.go:294`
  - **Issue:** `defer_ bool` violates Go conventions
  - **Fix:** Use `deferred bool` or `skip bool`

- [ ] **API-002**: Confusing source option names
  - **File:** `options.go:269-307`
  - **Issue:** WithName, WithSourcePath, WithIndexSource confusion
  - **Fix:** Rename for clarity:
    - `WithDocumentName()` instead of `WithName()`
    - `WithOriginalPath()` instead of `WithSourcePath()`
    - `WithCategory()` or `WithDataSource()` instead of `WithIndexSource()`

### 9.2 Validation Gaps

- [ ] **API-003**: WithMaxResults accepts invalid values
  - **File:** `options.go:183`
  - **Issue:** n <= 0 silently ignored
  - **Fix:** Log warning or set minimum 1

- [ ] **API-004**: WithMaxConcurrency accepts invalid values
  - **File:** `options.go:52`
  - **Issue:** n <= 0 silently ignored
  - **Fix:** Same as API-003

- [ ] **API-005**: Weights not validated to sum to 1
  - **File:** `options.go:337-352`
  - **Issue:** VectorWeight + KeywordWeight can be any values
  - **Fix:** Add validation or documentation

- [ ] **API-006**: Filter builder accumulates instead of replaces
  - **File:** `filter.go:27-29`
  - **Issue:** Calling `Sources()` twice accumulates
  - **Fix:** Document behavior or replace instead of append

### 9.3 Type Safety

- [ ] **API-007**: SearchMode is string enum
  - **File:** `options.go:318-327`
  - **Issue:** Can accept any string, no compile-time safety
  - **Note:** Consider migration to iota-based enum

- [ ] **API-008**: QueryType has same issue
  - **File:** `types.go:49-66`
  - **Issue:** Same as API-007
  - **Note:** Consistent with SearchMode, but less safe

---

## 10. Low-Priority Documentation

> **Priority:** LOW - Documentation improvements
> **Risk:** Harder onboarding, support burden

- [ ] **DOC-001**: Thread safety not documented on Store struct
  - **File:** `docuindex.go`
  - **Fix:** Add godoc explaining concurrent access guarantees

- [ ] **DOC-002**: Many options lack side effect documentation
  - **File:** `options.go`
  - **Fix:** Document what each option affects

- [ ] **DOC-003**: Image processing failure not documented
  - **File:** Various
  - **Fix:** Document that image failures are silent

- [ ] **DOC-004**: Stemmer is simplified Porter variant
  - **File:** `internal/nlp/stemmer.go`
  - **Fix:** Document that this is not full Porter implementation

- [ ] **DOC-005**: Update CLAUDE.md for unimplemented features
  - **File:** `CLAUDE.md`
  - **Fix:** Remove or mark as not-implemented: headers/footers, footnotes, track changes

---

## 11. Testing Improvements

> **Priority:** LOW - Test coverage improvements
> **Risk:** Regressions, undetected bugs

### 11.1 Missing Test Coverage

- [ ] **TEST-001**: No race condition tests
  - **Location:** All test files
  - **Fix:** Run `go test -race ./...` in CI
  - **Add:** Concurrent operation tests

- [ ] **TEST-002**: No Unicode tests for stemmer
  - **File:** `internal/nlp/nlp_test.go`
  - **Fix:** Add tests for "café", "naïve", "façade"

- [ ] **TEST-003**: No zip bomb test for DOCX
  - **File:** `docx/`
  - **Fix:** Add test with malicious nested compression

- [ ] **TEST-004**: No path traversal test for DOCX images
  - **File:** `docx/relationships_test.go`
  - **Fix:** Add test with `../../etc/passwd` paths

- [ ] **TEST-005**: No integer overflow test for PDF predictor
  - **File:** `pdf/stream_test.go`
  - **Fix:** Add test with extreme column/color values

### 11.2 Missing Edge Case Tests

- [ ] **TEST-006**: Empty batch handling in embedding
  - **Fix:** Test with empty text array

- [ ] **TEST-007**: Concurrent embedding tests
  - **Fix:** Test multiple goroutines embedding same document

- [ ] **TEST-008**: Large result set tests
  - **Fix:** Test search with 100k+ results

---

## 12. Future Optimizations

> **Priority:** FUTURE - Nice to have improvements
> **Risk:** None immediate

### 12.1 Memory Optimizations

- [ ] **OPT-001**: Lazy level array allocation in HNSW
  - **File:** `vectorindex/hnsw.go:113, 278`
  - **Benefit:** 20-40% memory reduction

- [ ] **OPT-002**: Object pooling for JSON encoders
  - **File:** `sqlite/blocks.go`
  - **Benefit:** Reduced GC pressure

- [ ] **OPT-003**: Pre-allocated slice for image pending
  - **File:** `docuindex.go:252-266`
  - **Benefit:** Fewer allocations

### 12.2 Algorithmic Optimizations

- [ ] **OPT-004**: Heap-based top-k for search
  - **File:** `sqlite/search.go`
  - **Benefit:** Memory efficiency for large result sets

- [ ] **OPT-005**: Cached RRF normalization
  - **File:** `search/fusion.go:113-132`
  - **Benefit:** 20-30% faster fusion

- [ ] **OPT-006**: Ef parameter auto-tuning
  - **File:** `vectorindex/hnsw.go`
  - **Benefit:** Automatic speed/recall tradeoff

### 12.3 Scaling Improvements

- [ ] **OPT-007**: Tiered HNSW for 1M+ vectors
  - **Benefit:** Better scaling

- [ ] **OPT-008**: Vector quantization (float32 to int8)
  - **Benefit:** 4x memory reduction

- [ ] **OPT-009**: Parallel layer building in HNSW
  - **Benefit:** Faster index construction

---

## Progress Tracking

### Summary by Priority

| Priority | Total | Fixed | Remaining |
|----------|-------|-------|-----------|
| Critical Security | 10 | 0 | 10 |
| Critical Data Integrity | 6 | 0 | 6 |
| Critical Algorithm | 8 | 0 | 8 |
| High Performance | 17 | 0 | 17 |
| Medium Error Handling | 10 | 0 | 10 |
| Medium Thread Safety | 4 | 0 | 4 |
| Medium Missing Features | 9 | 0 | 9 |
| Low Code Quality | 6 | 0 | 6 |
| Low API Design | 8 | 0 | 8 |
| Low Documentation | 5 | 0 | 5 |
| Testing | 8 | 0 | 8 |
| Future Optimizations | 9 | 0 | 9 |
| **TOTAL** | **100** | **0** | **100** |

### Version History

| Date | Version | Changes |
|------|---------|---------|
| 2026-01-11 | 1.0 | Initial comprehensive review |

---

## Quick Reference: Fix Order

**Week 1: Critical Security**
1. SEC-PDF-001 (integer overflow)
2. SEC-EMB-001/002/003 (unbounded response)
3. SEC-DOCX-001 (zip bomb)
4. SEC-DOCX-002 (path traversal)

**Week 2: Critical Data Integrity**
5. DATA-001 (concurrent embedding)
6. DATA-002 (dimension race)
7. DATA-003 (background thread safety)

**Week 3: Critical Algorithm**
8. ALG-001 (chunk positioning)
9. ALG-002 (Unicode stemmer)
10. ALG-004/005 (Ollama bugs)

**Week 4+: Performance & Quality**
11. PERF-DB-001/002/003 (database queries)
12. PERF-PDF-001/002/003 (PDF caching)
13. Remaining items by priority

---

## Notes

- Check items by adding `x` inside brackets: `- [x]`
- Update "Fixed" counts in Progress Tracking table
- Add notes below items when partially fixed
- Create PRs referencing issue IDs (e.g., "Fix SEC-PDF-001")
