// Package search provides hybrid search functionality combining keyword and vector search.
package search

// SearchResult represents a keyword search result for internal fusion.
// This is a minimal type used internally by the search package.
// The main docuindex.SearchResult type has additional fields.
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
