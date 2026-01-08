package search

import (
	"encoding/json"
	"math"
	"sync"
	"time"
)

// BlockType represents the type of content block
type BlockType string

const (
	BlockTypeText    BlockType = "text"
	BlockTypeHeading BlockType = "heading"
	BlockTypeImage   BlockType = "image"
	BlockTypeList    BlockType = "list"
	BlockTypeTable   BlockType = "table"
)

// DocumentFormat represents the source document format
type DocumentFormat string

const (
	FormatPDF  DocumentFormat = "pdf"
	FormatDOCX DocumentFormat = "docx"
)

// BoundingBox represents the position and size of content on a page
type BoundingBox struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PageWidth  float64 `json:"page_width"`
	PageHeight float64 `json:"page_height"`
}

// FontInfo contains font metadata for text content
type FontInfo struct {
	Name   string  `json:"name"`
	Size   float64 `json:"size"`
	Bold   bool    `json:"bold,omitempty"`
	Italic bool    `json:"italic,omitempty"`
}

// SemanticInfo contains AI-friendly metadata about content
type SemanticInfo struct {
	IsHeading    bool     `json:"is_heading,omitempty"`
	HeadingLevel int      `json:"heading_level,omitempty"`
	Section      string   `json:"section,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	Context      string   `json:"context,omitempty"`
}

// ContentBlock represents a unit of content with position and metadata
type ContentBlock struct {
	ID       string       `json:"id"`
	Type     BlockType    `json:"type"`
	Content  string       `json:"content"`
	Page     int          `json:"page"`
	BBox     BoundingBox  `json:"bbox"`
	Font     *FontInfo    `json:"font,omitempty"`
	Semantic SemanticInfo `json:"semantic,omitempty"`
	Children []string     `json:"children,omitempty"`
}

// DocumentInfo contains metadata about an indexed document
type DocumentInfo struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	OriginalPath string         `json:"original_path"`
	SizeBytes    int64          `json:"size_bytes"`
	PageCount    int            `json:"page_count"`
	Format       DocumentFormat `json:"format"`
	Checksum     string         `json:"checksum"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// DocumentContent holds the structured content of a document
type DocumentContent struct {
	Version string         `json:"version"`
	Blocks  []ContentBlock `json:"blocks"`
}

// Document represents a fully indexed document
type Document struct {
	Info    DocumentInfo    `json:"info"`
	Content DocumentContent `json:"content"`
}

// GetBlockByID finds a block by its ID
func (d *Document) GetBlockByID(id string) *ContentBlock {
	for i := range d.Content.Blocks {
		if d.Content.Blocks[i].ID == id {
			return &d.Content.Blocks[i]
		}
	}
	return nil
}

// SearchResult represents a single search hit
type SearchResult struct {
	DocumentID   string         `json:"document_id"`
	DocumentName string         `json:"document_name"`
	BlockID      string         `json:"block_id"`
	Content      string         `json:"content"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	Page         int            `json:"page"`
	Section      string         `json:"section"`
	Context      []ContentBlock `json:"context"`
	Positions    []int          `json:"positions"`
}

// Posting represents a single occurrence of a term
type Posting struct {
	DocumentID string `json:"doc_id"`
	BlockID    string `json:"block_id"`
	Positions  []int  `json:"positions"` // Token positions within the block
}

// TermEntry contains all postings for a term
type TermEntry struct {
	Term     string    `json:"term"`
	DF       int       `json:"df"` // Document frequency
	Postings []Posting `json:"postings"`
}

// DocumentEntry contains metadata about an indexed document
type DocumentEntry struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BlockCount int    `json:"block_count"`
	TotalTerms int    `json:"total_terms"`
}

// Index is an inverted index for full-text search
type Index struct {
	mu        sync.RWMutex
	terms     map[string]*TermEntry        // term -> postings
	documents map[string]*DocumentEntry    // docID -> doc info
	avgDL     float64                      // Average document length
	tokenizer *Tokenizer
}

// IndexData is the serializable form of the index
type IndexData struct {
	Terms     map[string]*TermEntry     `json:"terms"`
	Documents map[string]*DocumentEntry `json:"documents"`
	AvgDL     float64                   `json:"avg_dl"`
}

// NewIndex creates a new search index
func NewIndex(enableStemming, enableStopWords bool) *Index {
	return &Index{
		terms:     make(map[string]*TermEntry),
		documents: make(map[string]*DocumentEntry),
		tokenizer: NewTokenizer(enableStemming, enableStopWords),
	}
}

// AddDocument adds a document to the index
func (idx *Index) AddDocument(doc *Document) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	docID := doc.Info.ID
	totalTerms := 0

	// Remove old entries if document exists
	idx.removeDocumentLocked(docID)

	// Index each text block
	for _, block := range doc.Content.Blocks {
		if block.Type != BlockTypeText && block.Type != BlockTypeHeading {
			continue
		}

		tokens := idx.tokenizer.Tokenize(block.Content)
		totalTerms += len(tokens)

		// Group positions by term
		termPositions := make(map[string][]int)
		for _, tok := range tokens {
			termPositions[tok.Text] = append(termPositions[tok.Text], tok.Position)
		}

		// Add postings
		for term, positions := range termPositions {
			entry, ok := idx.terms[term]
			if !ok {
				entry = &TermEntry{Term: term}
				idx.terms[term] = entry
			}

			// Check if this doc already has a posting for this term
			found := false
			for i := range entry.Postings {
				if entry.Postings[i].DocumentID == docID && entry.Postings[i].BlockID == block.ID {
					entry.Postings[i].Positions = append(entry.Postings[i].Positions, positions...)
					found = true
					break
				}
			}

			if !found {
				entry.Postings = append(entry.Postings, Posting{
					DocumentID: docID,
					BlockID:    block.ID,
					Positions:  positions,
				})
			}
		}
	}

	// Update document frequency counts
	docTerms := make(map[string]bool)
	for _, block := range doc.Content.Blocks {
		if block.Type != BlockTypeText && block.Type != BlockTypeHeading {
			continue
		}
		tokens := idx.tokenizer.Tokenize(block.Content)
		for _, tok := range tokens {
			docTerms[tok.Text] = true
		}
	}
	for term := range docTerms {
		if entry, ok := idx.terms[term]; ok {
			entry.DF++
		}
	}

	// Save document info
	idx.documents[docID] = &DocumentEntry{
		ID:         docID,
		Name:       doc.Info.Name,
		BlockCount: len(doc.Content.Blocks),
		TotalTerms: totalTerms,
	}

	// Update average document length
	idx.updateAvgDL()
}

// RemoveDocument removes a document from the index
func (idx *Index) RemoveDocument(docID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeDocumentLocked(docID)
}

// removeDocumentLocked removes a document (caller must hold lock)
func (idx *Index) removeDocumentLocked(docID string) {
	// Remove postings for this document
	for term, entry := range idx.terms {
		var newPostings []Posting
		for _, p := range entry.Postings {
			if p.DocumentID != docID {
				newPostings = append(newPostings, p)
			}
		}
		if len(newPostings) == 0 {
			delete(idx.terms, term)
		} else {
			entry.Postings = newPostings
			entry.DF = len(idx.uniqueDocs(newPostings))
		}
	}

	delete(idx.documents, docID)
	idx.updateAvgDL()
}

// uniqueDocs counts unique documents in postings
func (idx *Index) uniqueDocs(postings []Posting) map[string]bool {
	docs := make(map[string]bool)
	for _, p := range postings {
		docs[p.DocumentID] = true
	}
	return docs
}

// updateAvgDL updates the average document length
func (idx *Index) updateAvgDL() {
	if len(idx.documents) == 0 {
		idx.avgDL = 0
		return
	}

	var total int
	for _, doc := range idx.documents {
		total += doc.TotalTerms
	}
	idx.avgDL = float64(total) / float64(len(idx.documents))
}

// Search performs a search query
func (idx *Index) Search(query string, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	// Tokenize query
	queryTokens := idx.tokenizer.TokenizeToStrings(query)
	if len(queryTokens) == 0 {
		return nil
	}

	// Collect matching postings and calculate BM25 scores
	scores := make(map[string]float64)      // docID -> score
	blocks := make(map[string][]string)     // docID -> blockIDs
	positions := make(map[string][]int)     // docID:blockID -> positions

	N := float64(len(idx.documents))

	for _, term := range queryTokens {
		entry, ok := idx.terms[term]
		if !ok {
			continue
		}

		// BM25 parameters
		k1 := 1.2
		b := 0.75

		// IDF for this term
		df := float64(entry.DF)
		idf := math.Log((N-df+0.5)/(df+0.5) + 1)

		for _, posting := range entry.Postings {
			docEntry := idx.documents[posting.DocumentID]
			if docEntry == nil {
				continue
			}

			// Term frequency
			tf := float64(len(posting.Positions))

			// Document length normalization
			dl := float64(docEntry.TotalTerms)
			norm := 1 - b + b*(dl/idx.avgDL)

			// BM25 score for this term
			score := idf * ((tf * (k1 + 1)) / (tf + k1*norm))

			scores[posting.DocumentID] += score

			// Track blocks and positions
			blocks[posting.DocumentID] = append(blocks[posting.DocumentID], posting.BlockID)
			key := posting.DocumentID + ":" + posting.BlockID
			positions[key] = append(positions[key], posting.Positions...)
		}
	}

	// Sort by score
	type scoredDoc struct {
		docID string
		score float64
	}
	var sorted []scoredDoc
	for docID, score := range scores {
		sorted = append(sorted, scoredDoc{docID, score})
	}
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].score > sorted[i].score {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// Limit results
	if maxResults > 0 && len(sorted) > maxResults {
		sorted = sorted[:maxResults]
	}

	// Build results
	var results []SearchResult
	for _, sd := range sorted {
		docEntry := idx.documents[sd.docID]
		if docEntry == nil {
			continue
		}

		// Get best matching block
		blockID := ""
		if len(blocks[sd.docID]) > 0 {
			blockID = blocks[sd.docID][0]
		}

		key := sd.docID + ":" + blockID
		pos := positions[key]

		results = append(results, SearchResult{
			DocumentID:   sd.docID,
			DocumentName: docEntry.Name,
			BlockID:      blockID,
			Score:        sd.score,
			Positions:    pos,
		})
	}

	return results
}

// SearchInDocument searches within a specific document
func (idx *Index) SearchInDocument(docID, query string, maxResults int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryTokens := idx.tokenizer.TokenizeToStrings(query)
	if len(queryTokens) == 0 {
		return nil
	}

	var results []SearchResult

	for _, term := range queryTokens {
		entry, ok := idx.terms[term]
		if !ok {
			continue
		}

		for _, posting := range entry.Postings {
			if posting.DocumentID != docID {
				continue
			}

			docEntry := idx.documents[docID]
			if docEntry == nil {
				continue
			}

			// Simple TF-based scoring for single doc search
			score := float64(len(posting.Positions))

			results = append(results, SearchResult{
				DocumentID:   docID,
				DocumentName: docEntry.Name,
				BlockID:      posting.BlockID,
				Score:        score,
				Positions:    posting.Positions,
			})
		}
	}

	// Sort by score
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if maxResults > 0 && len(results) > maxResults {
		results = results[:maxResults]
	}

	return results
}

// Serialize serializes the index to JSON
func (idx *Index) Serialize() ([]byte, error) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	data := IndexData{
		Terms:     idx.terms,
		Documents: idx.documents,
		AvgDL:     idx.avgDL,
	}

	return json.Marshal(data)
}

// Deserialize loads the index from JSON
func (idx *Index) Deserialize(data []byte) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	var indexData IndexData
	if err := json.Unmarshal(data, &indexData); err != nil {
		return err
	}

	idx.terms = indexData.Terms
	idx.documents = indexData.Documents
	idx.avgDL = indexData.AvgDL

	if idx.terms == nil {
		idx.terms = make(map[string]*TermEntry)
	}
	if idx.documents == nil {
		idx.documents = make(map[string]*DocumentEntry)
	}

	return nil
}

// Stats returns index statistics
func (idx *Index) Stats() IndexStats {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	totalPostings := 0
	for _, entry := range idx.terms {
		totalPostings += len(entry.Postings)
	}

	return IndexStats{
		DocumentCount: len(idx.documents),
		TermCount:     len(idx.terms),
		PostingCount:  totalPostings,
		AvgDocLength:  idx.avgDL,
	}
}

// IndexStats contains statistics about the index
type IndexStats struct {
	DocumentCount int     `json:"document_count"`
	TermCount     int     `json:"term_count"`
	PostingCount  int     `json:"posting_count"`
	AvgDocLength  float64 `json:"avg_doc_length"`
}

// GetDocumentIDs returns all indexed document IDs
func (idx *Index) GetDocumentIDs() []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	ids := make([]string, 0, len(idx.documents))
	for id := range idx.documents {
		ids = append(ids, id)
	}
	return ids
}

// HasDocument checks if a document is in the index
func (idx *Index) HasDocument(docID string) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.documents[docID]
	return ok
}
