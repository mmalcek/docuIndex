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

	// Update global stats
	if err := updateGlobalStats(tx); err != nil {
		return err
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

// stem applies Porter-like stemming
func stem(word string) string {
	if len(word) <= 3 {
		return word
	}

	suffixes := []string{
		"ational", "tional", "enci", "anci", "izer", "ation", "ator",
		"alism", "iveness", "fulness", "ousness", "aliti", "iviti",
		"biliti", "logi", "ness", "ment", "ent", "ism", "ate", "iti",
		"ous", "ive", "ize", "ing", "ies", "ied", "ion", "ity", "ful",
		"able", "ible", "ant", "al", "er", "ic", "ly", "ed", "es", "s",
	}

	replacements := map[string]string{
		"ational": "ate", "tional": "tion", "enci": "ence", "anci": "ance",
		"izer": "ize", "ization": "ize", "ation": "ate", "ator": "ate",
		"fulness": "ful", "ousness": "ous", "iveness": "ive",
		"ies": "i", "ied": "i",
	}

	for suffix, replacement := range replacements {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 2 {
			return word[:len(word)-len(suffix)] + replacement
		}
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 2 {
			return word[:len(word)-len(suffix)]
		}
	}

	return word
}

// isStopWord checks if word is a stop word
func isStopWord(word string) bool {
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "and": true, "or": true,
		"but": true, "in": true, "on": true, "at": true, "to": true,
		"for": true, "of": true, "with": true, "by": true, "from": true,
		"as": true, "is": true, "was": true, "are": true, "were": true,
		"been": true, "be": true, "have": true, "has": true, "had": true,
		"do": true, "does": true, "did": true, "will": true, "would": true,
		"could": true, "should": true, "may": true, "might": true, "must": true,
		"shall": true, "can": true, "this": true, "that": true, "these": true,
		"those": true, "it": true, "its": true, "they": true, "their": true,
		"them": true, "we": true, "our": true, "you": true, "your": true,
		"he": true, "she": true, "him": true, "her": true, "his": true,
		"not": true, "no": true, "all": true, "each": true, "every": true,
		"both": true, "few": true, "more": true, "most": true, "other": true,
		"some": true, "such": true, "than": true, "too": true, "very": true,
		"just": true, "also": true, "now": true, "only": true, "so": true,
	}
	return stopWords[word]
}

// generateSnippet creates a highlighted snippet
func generateSnippet(content string, terms []string, highlightPre, highlightPost string) string {
	snippet := content
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}

	if highlightPre != "" && highlightPost != "" {
		lower := strings.ToLower(snippet)
		for _, term := range terms {
			idx := strings.Index(lower, term)
			if idx >= 0 {
				// Simple highlight - could be improved
				snippet = snippet[:idx] + highlightPre + snippet[idx:idx+len(term)] + highlightPost + snippet[idx+len(term):]
				lower = strings.ToLower(snippet)
			}
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
