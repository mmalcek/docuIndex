package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mmalcek/docuIndex/internal/nlp"
)

// SearchResult represents a single search hit
type SearchResult struct {
	DocumentID   string
	DocumentName string
	BlockID      string
	Content      string
	Snippet      string
	Score        float64
	Page         int
	Section      string
	Positions    []int
}

// SearchResults contains search results with metadata
type SearchResults struct {
	Query      string
	TotalHits  int
	Results    []SearchResult
	SearchTime time.Duration
}

// SearchOptions configures search behavior
type SearchOptions struct {
	MaxResults    int
	MinScore      float64
	ContextWindow int
	HighlightPre  string
	HighlightPost string
	DocumentIDs   []string // Filter to specific documents
	PageRange     [2]int   // Filter to page range [start, end]
	Filter        *FilterConfig
}

// FilterConfig represents advanced filter options from Filter DSL
type FilterConfig struct {
	Sources       []string
	Formats       []string
	Tags          map[string]string
	DateStart     time.Time
	DateEnd       time.Time
	MinPageCount  int
	MaxPageCount  int
	HasEmbeddings *bool
	ExternalIDs   []string
}

// DefaultSearchOptions returns default search options
func DefaultSearchOptions() *SearchOptions {
	return &SearchOptions{
		MaxResults:    20,
		MinScore:      0.0,
		ContextWindow: 0,
		HighlightPre:  "",
		HighlightPost: "",
	}
}

// BM25 parameters
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// IndexDocument indexes a document's blocks for search
func (s *Store) IndexDocument(documentID string, blocks []ContentBlock) error {
	return s.IndexDocumentWithOptions(documentID, blocks, false)
}

// IndexDocumentWithOptions indexes a document's blocks with optional deferred global stats.
// Set deferGlobalStats=true for batch operations to avoid O(n) recalculation per document.
// Call UpdateGlobalStats() once after all documents are indexed.
func (s *Store) IndexDocumentWithOptions(documentID string, blocks []ContentBlock, deferGlobalStats bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing terms for this document
	_, err = tx.Exec(`DELETE FROM search_terms WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete old terms: %w", err)
	}

	// Prepare insert statement
	stmt, err := tx.Prepare(`
		INSERT INTO search_terms (term, document_id, block_id, positions, term_frequency)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	// Index each block
	var totalTerms int
	termCounts := make(map[string]int)

	for _, block := range blocks {
		if block.Type == "image" {
			continue // Skip image blocks
		}

		tokens := tokenize(block.Content, s.config.UseStemming, s.config.UseStopWords)
		if len(tokens) == 0 {
			continue
		}

		// Group tokens by term for this block
		termPositions := make(map[string][]int)
		for _, tok := range tokens {
			termPositions[tok.Text] = append(termPositions[tok.Text], tok.Position)
			termCounts[tok.Text]++
			totalTerms++
		}

		// Insert term postings
		for term, positions := range termPositions {
			posJSON, _ := json.Marshal(positions)
			tf := float64(len(positions)) / float64(len(tokens))

			_, err := stmt.Exec(term, documentID, block.ID, string(posJSON), tf)
			if err != nil {
				return fmt.Errorf("insert term %s: %w", term, err)
			}
		}
	}

	// Update document statistics
	avgBlockLen := float64(0)
	if len(blocks) > 0 {
		avgBlockLen = float64(totalTerms) / float64(len(blocks))
	}

	_, err = tx.Exec(`
		INSERT OR REPLACE INTO document_stats (document_id, total_terms, unique_terms, avg_block_length)
		VALUES (?, ?, ?, ?)
	`, documentID, totalTerms, len(termCounts), avgBlockLen)
	if err != nil {
		return fmt.Errorf("update document stats: %w", err)
	}

	// Update global stats unless deferred
	if !deferGlobalStats {
		if err := updateGlobalStats(tx); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Search performs a BM25 search across all documents
func (s *Store) Search(query string, opts *SearchOptions) (*SearchResults, error) {
	if opts == nil {
		opts = DefaultSearchOptions()
	}

	start := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Apply filter to get allowed document IDs
	if opts.Filter != nil {
		filteredIDs, err := s.applyFilter(opts.Filter)
		if err != nil {
			return nil, fmt.Errorf("apply filter: %w", err)
		}
		if len(filteredIDs) == 0 {
			// No documents match filter
			return &SearchResults{Query: query, SearchTime: time.Since(start)}, nil
		}
		// Merge with existing document IDs filter
		if len(opts.DocumentIDs) > 0 {
			opts.DocumentIDs = intersectStringSlices(opts.DocumentIDs, filteredIDs)
		} else {
			opts.DocumentIDs = filteredIDs
		}
	}

	// Parse and tokenize query
	queryTerms := tokenizeQuery(query, s.config.UseStemming, s.config.UseStopWords)
	if len(queryTerms) == 0 {
		return &SearchResults{Query: query, SearchTime: time.Since(start)}, nil
	}

	// Get global statistics
	totalDocs, avgDocLen, err := s.getGlobalStats()
	if err != nil {
		return nil, err
	}

	if totalDocs == 0 {
		return &SearchResults{Query: query, SearchTime: time.Since(start)}, nil
	}

	// Score blocks
	scores := make(map[string]map[string]float64) // doc_id -> block_id -> score
	positions := make(map[string]map[string][]int)

	for _, term := range queryTerms {
		// Get document frequency
		var df int
		err := s.db.QueryRow(`
			SELECT COUNT(DISTINCT document_id) FROM search_terms WHERE term = ?
		`, term).Scan(&df)
		if err != nil {
			continue
		}

		if df == 0 {
			continue
		}

		// Get postings for this term
		rows, err := s.db.Query(`
			SELECT st.document_id, st.block_id, st.term_frequency, st.positions, ds.total_terms
			FROM search_terms st
			JOIN document_stats ds ON st.document_id = ds.document_id
			WHERE st.term = ?
		`, term)
		if err != nil {
			continue
		}

		for rows.Next() {
			var docID, blockID, positionsJSON string
			var tf float64
			var docLen int

			if err := rows.Scan(&docID, &blockID, &tf, &positionsJSON, &docLen); err != nil {
				continue
			}

			// Apply document filter if specified
			if len(opts.DocumentIDs) > 0 && !contains(opts.DocumentIDs, docID) {
				continue
			}

			// Calculate BM25 score
			idf := math.Log((float64(totalDocs)-float64(df)+0.5)/(float64(df)+0.5) + 1)
			tfNorm := (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*(float64(docLen)/avgDocLen)))
			score := idf * tfNorm

			// Accumulate scores
			if scores[docID] == nil {
				scores[docID] = make(map[string]float64)
				positions[docID] = make(map[string][]int)
			}
			scores[docID][blockID] += score

			// Parse positions
			var pos []int
			json.Unmarshal([]byte(positionsJSON), &pos)
			positions[docID][blockID] = append(positions[docID][blockID], pos...)
		}
		rows.Close()
	}

	// Convert to results
	var results []SearchResult
	for docID, blockScores := range scores {
		// Get document name
		var docName string
		s.db.QueryRow(`SELECT name FROM documents WHERE id = ?`, docID).Scan(&docName)

		for blockID, score := range blockScores {
			if score < opts.MinScore {
				continue
			}

			// Get block content
			block, err := s.GetBlockByID(docID, blockID)
			if err != nil {
				continue
			}

			// Apply page filter
			if opts.PageRange[1] > 0 && (block.Page < opts.PageRange[0] || block.Page > opts.PageRange[1]) {
				continue
			}

			// Boost headings
			if block.IsHeading {
				score *= 1.5
			}

			result := SearchResult{
				DocumentID:   docID,
				DocumentName: docName,
				BlockID:      blockID,
				Content:      block.Content,
				Score:        score,
				Page:         block.Page,
				Section:      block.Section,
				Positions:    positions[docID][blockID],
			}

			// Generate snippet
			result.Snippet = generateSnippet(block.Content, queryTerms, opts.HighlightPre, opts.HighlightPost)

			results = append(results, result)
		}
	}

	// Sort by score
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}

	return &SearchResults{
		Query:      query,
		TotalHits:  len(results),
		Results:    results,
		SearchTime: time.Since(start),
	}, nil
}

// SearchInDocument searches within a specific document
func (s *Store) SearchInDocument(documentID, query string, opts *SearchOptions) (*SearchResults, error) {
	if opts == nil {
		opts = DefaultSearchOptions()
	}
	opts.DocumentIDs = []string{documentID}
	return s.Search(query, opts)
}

// DeleteDocumentIndex removes search index for a document
func (s *Store) DeleteDocumentIndex(documentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`DELETE FROM search_terms WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete search terms: %w", err)
	}

	_, err = tx.Exec(`DELETE FROM document_stats WHERE document_id = ?`, documentID)
	if err != nil {
		return fmt.Errorf("delete document stats: %w", err)
	}

	if err := updateGlobalStats(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// getGlobalStats retrieves global index statistics
func (s *Store) getGlobalStats() (totalDocs int, avgDocLen float64, err error) {
	err = s.db.QueryRow(`SELECT value FROM index_stats WHERE key = 'total_documents'`).Scan(&totalDocs)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("get total docs: %w", err)
	}

	err = s.db.QueryRow(`SELECT value FROM index_stats WHERE key = 'avg_doc_length'`).Scan(&avgDocLen)
	if err == sql.ErrNoRows {
		avgDocLen = 0
	} else if err != nil {
		return 0, 0, fmt.Errorf("get avg doc length: %w", err)
	}

	return totalDocs, avgDocLen, nil
}

// updateGlobalStats updates global index statistics
func updateGlobalStats(tx *sql.Tx) error {
	// Count total documents
	var totalDocs int
	err := tx.QueryRow(`SELECT COUNT(*) FROM document_stats`).Scan(&totalDocs)
	if err != nil {
		return fmt.Errorf("count documents: %w", err)
	}

	// Calculate average document length
	var avgLen float64
	if totalDocs > 0 {
		err = tx.QueryRow(`SELECT AVG(total_terms) FROM document_stats`).Scan(&avgLen)
		if err != nil {
			return fmt.Errorf("calc avg length: %w", err)
		}
	}

	// Update stats
	_, err = tx.Exec(`INSERT OR REPLACE INTO index_stats (key, value) VALUES ('total_documents', ?)`, totalDocs)
	if err != nil {
		return fmt.Errorf("update total docs: %w", err)
	}

	_, err = tx.Exec(`INSERT OR REPLACE INTO index_stats (key, value) VALUES ('avg_doc_length', ?)`, avgLen)
	if err != nil {
		return fmt.Errorf("update avg length: %w", err)
	}

	return nil
}

// UpdateGlobalStats recalculates and updates global index statistics.
// Call this after batch indexing with deferGlobalStats=true.
func (s *Store) UpdateGlobalStats() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.beginTx()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := updateGlobalStats(tx); err != nil {
		return err
	}

	return tx.Commit()
}

// GetIndexTermCount returns the number of unique terms in the index
func (s *Store) GetIndexTermCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT term) FROM search_terms`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count terms: %w", err)
	}
	return count, nil
}

// Token represents a token with position
type tokenWithPos struct {
	Text     string
	Position int
}

// tokenize splits text into tokens with optional stemming and stop word filtering
func tokenize(text string, stemming, stopWords bool) []tokenWithPos {
	var tokens []tokenWithPos
	var current strings.Builder
	position := 0

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				word := current.String()
				current.Reset()

				if len(word) >= 2 && (!stopWords || !isStopWord(word)) {
					if stemming {
						word = stem(word)
					}
					tokens = append(tokens, tokenWithPos{
						Text:     word,
						Position: position,
					})
					position++
				}
			}
		}
	}

	if current.Len() > 0 {
		word := current.String()
		if len(word) >= 2 && (!stopWords || !isStopWord(word)) {
			if stemming {
				word = stem(word)
			}
			tokens = append(tokens, tokenWithPos{
				Text:     word,
				Position: position,
			})
		}
	}

	return tokens
}

// tokenizeQuery extracts query terms
func tokenizeQuery(query string, stemming, stopWords bool) []string {
	tokens := tokenize(query, stemming, stopWords)
	terms := make([]string, len(tokens))
	for i, t := range tokens {
		terms[i] = t.Text
	}
	return terms
}

// stem applies Porter-like stemming using the shared nlp package
func stem(word string) string {
	return nlp.Stem(word)
}

// isStopWord checks if word is a stop word using the shared nlp package
func isStopWord(word string) bool {
	return nlp.IsStopWord(word)
}

// generateSnippet creates a highlighted snippet
func generateSnippet(content string, terms []string, highlightPre, highlightPost string) string {
	snippet := content
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}

	if highlightPre == "" || highlightPost == "" || len(terms) == 0 {
		return snippet
	}

	// Find all match positions first to avoid index shifting issues
	type match struct {
		start, end int
	}
	var matches []match
	lower := strings.ToLower(snippet)

	for _, term := range terms {
		termLower := strings.ToLower(term)
		idx := 0
		for {
			pos := strings.Index(lower[idx:], termLower)
			if pos == -1 {
				break
			}
			absPos := idx + pos
			matches = append(matches, match{absPos, absPos + len(term)})
			idx = absPos + len(term)
		}
	}

	if len(matches) == 0 {
		return snippet
	}

	// Sort by position descending (process from end to avoid shift issues)
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].start > matches[j].start
	})

	// Remove overlapping matches (keep the earlier one in original order)
	var filtered []match
	for _, m := range matches {
		overlaps := false
		for _, f := range filtered {
			// Check if m overlaps with any already filtered match
			if m.start < f.end && m.end > f.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			filtered = append(filtered, m)
		}
	}

	// Apply highlights from end to start (positions stay valid)
	for _, m := range filtered {
		if m.start >= 0 && m.end <= len(snippet) {
			snippet = snippet[:m.start] + highlightPre + snippet[m.start:m.end] + highlightPost + snippet[m.end:]
		}
	}

	return snippet
}

// contains checks if slice contains value
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// intersectStringSlices returns the intersection of two string slices
func intersectStringSlices(a, b []string) []string {
	set := make(map[string]bool)
	for _, s := range a {
		set[s] = true
	}
	var result []string
	for _, s := range b {
		if set[s] {
			result = append(result, s)
		}
	}
	return result
}

// applyFilter applies filter configuration and returns matching document IDs
func (s *Store) applyFilter(filter *FilterConfig) ([]string, error) {
	if filter == nil {
		return nil, nil
	}

	// Build dynamic WHERE clause
	var conditions []string
	var args []interface{}

	// Filter by sources (source field or format field)
	if len(filter.Sources) > 0 {
		placeholders := make([]string, len(filter.Sources))
		for i, src := range filter.Sources {
			placeholders[i] = "?"
			args = append(args, src)
		}
		// Match either source or format
		conditions = append(conditions,
			fmt.Sprintf("(source IN (%s) OR format IN (%s))",
				strings.Join(placeholders, ","),
				strings.Join(placeholders, ",")))
		// Duplicate args for format
		for _, src := range filter.Sources {
			args = append(args, src)
		}
	}

	// Filter by formats only
	if len(filter.Formats) > 0 {
		placeholders := make([]string, len(filter.Formats))
		for i, fmt := range filter.Formats {
			placeholders[i] = "?"
			args = append(args, fmt)
		}
		conditions = append(conditions, fmt.Sprintf("format IN (%s)", strings.Join(placeholders, ",")))
	}

	// Filter by date range
	if !filter.DateStart.IsZero() {
		conditions = append(conditions, "created_at >= ?")
		args = append(args, filter.DateStart.Format(time.RFC3339))
	}
	if !filter.DateEnd.IsZero() {
		conditions = append(conditions, "created_at <= ?")
		args = append(args, filter.DateEnd.Format(time.RFC3339))
	}

	// Filter by page count
	if filter.MinPageCount > 0 {
		conditions = append(conditions, "page_count >= ?")
		args = append(args, filter.MinPageCount)
	}
	if filter.MaxPageCount > 0 {
		conditions = append(conditions, "page_count <= ?")
		args = append(args, filter.MaxPageCount)
	}

	// Filter by external IDs
	if len(filter.ExternalIDs) > 0 {
		placeholders := make([]string, len(filter.ExternalIDs))
		for i, id := range filter.ExternalIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		conditions = append(conditions, fmt.Sprintf("external_id IN (%s)", strings.Join(placeholders, ",")))
	}

	// Build query
	query := "SELECT id FROM documents"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("filter query: %w", err)
	}
	defer rows.Close()

	var docIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		docIDs = append(docIDs, id)
	}

	// Apply tag filter if specified (requires join with document_tags)
	if len(filter.Tags) > 0 {
		docIDs, err = s.filterByTags(docIDs, filter.Tags)
		if err != nil {
			return nil, err
		}
	}

	// Apply embeddings filter if specified
	if filter.HasEmbeddings != nil {
		docIDs, err = s.filterByEmbeddings(docIDs, *filter.HasEmbeddings)
		if err != nil {
			return nil, err
		}
	}

	return docIDs, nil
}

// filterByTags filters document IDs by tags (AND logic).
// Supports negation with "!" prefix (e.g., "!Closed" means not equal to "Closed").
func (s *Store) filterByTags(docIDs []string, tags map[string]string) ([]string, error) {
	if len(tags) == 0 {
		return docIDs, nil
	}

	var result []string
	for _, docID := range docIDs {
		match := true
		for key, value := range tags {
			var count int
			var err error

			if len(value) > 0 && value[0] == '!' {
				// Negation: document must NOT have this tag value
				negatedValue := value[1:]
				err = s.db.QueryRow(`
					SELECT COUNT(*) FROM document_tags
					WHERE document_id = ? AND tag_key = ? AND tag_value = ?
				`, docID, key, negatedValue).Scan(&count)
				if err != nil || count > 0 {
					match = false
					break
				}
			} else {
				// Normal: document must have this exact tag value
				err = s.db.QueryRow(`
					SELECT COUNT(*) FROM document_tags
					WHERE document_id = ? AND tag_key = ? AND tag_value = ?
				`, docID, key, value).Scan(&count)
				if err != nil || count == 0 {
					match = false
					break
				}
			}
		}
		if match {
			result = append(result, docID)
		}
	}
	return result, nil
}

// filterByEmbeddings filters document IDs by embedding presence
func (s *Store) filterByEmbeddings(docIDs []string, hasEmbeddings bool) ([]string, error) {
	var result []string
	for _, docID := range docIDs {
		var count int
		err := s.db.QueryRow(`
			SELECT COUNT(*) FROM vectors WHERE document_id = ?
		`, docID).Scan(&count)
		if err != nil {
			continue
		}

		if hasEmbeddings && count > 0 {
			result = append(result, docID)
		} else if !hasEmbeddings && count == 0 {
			result = append(result, docID)
		}
	}
	return result, nil
}
