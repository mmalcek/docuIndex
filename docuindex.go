package docuindex

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mariomalcek/docuindex/pdf"
	"github.com/mariomalcek/docuindex/search"
	"github.com/mariomalcek/docuindex/storage"
)

// Store manages document storage, indexing, and search
type Store struct {
	config *storeConfig
	mu     sync.RWMutex
	fs     *storage.FileSystemStore
	index  *search.Index
}

// NewStore creates a new document store at the specified path
func NewStore(basePath string, opts ...StoreOption) (*Store, error) {
	config := defaultStoreConfig()
	config.BasePath = basePath

	for _, opt := range opts {
		opt(config)
	}

	fs, err := storage.NewFileSystemStore(basePath)
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}

	s := &Store{
		config: config,
		fs:     fs,
		index:  search.NewIndex(config.EnableStemming, config.EnableStopWords),
	}

	// Load existing index if present
	if indexData, err := fs.GetIndex(); err == nil && indexData != nil {
		s.index.Deserialize(indexData)
	}

	return s, nil
}

// IndexDocument indexes a document from a file path
func (s *Store) IndexDocument(path string, opts ...IndexOption) (*Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config := defaultIndexConfig()
	config.SourcePath = path

	for _, opt := range opts {
		opt(config)
	}

	// Detect format
	ext := strings.ToLower(filepath.Ext(path))
	var doc *Document
	var err error

	switch ext {
	case ".pdf":
		doc, err = s.indexPDF(path, config)
	case ".docx":
		return nil, fmt.Errorf("DOCX support not yet implemented")
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}

	if err != nil {
		return nil, err
	}

	// Save to storage
	if err := s.fs.SaveDocument(toStorageDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Add to search index
	s.index.AddDocument(toSearchDocument(doc))

	// Save updated index
	if indexData, err := s.index.Serialize(); err == nil {
		s.fs.SaveIndex(indexData)
	}

	return doc, nil
}

// indexPDF indexes a PDF file
func (s *Store) indexPDF(path string, config *indexConfig) (*Document, error) {
	// Open PDF
	pdfDoc, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer pdfDoc.Close()

	// Check if encrypted
	if pdfDoc.IsEncrypted() {
		return nil, ErrEncryptedPDF
	}

	// Generate document ID
	docID := generateID()

	// Get file info
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	// Compute checksum
	var checksum string
	if s.config.ComputeChecksum {
		checksum, _ = computeChecksum(path)
	}

	// Get page count
	pageCount, err := pdfDoc.PageCount()
	if err != nil {
		pageCount = 0
	}

	// Create semantic extractor
	extractor := pdf.NewSemanticExtractor(pdfDoc)

	// Extract content
	pdfContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, fmt.Errorf("extract content: %w", err)
	}

	// Convert pdf.DocumentContent to docuindex.DocumentContent
	content := convertPDFContent(pdfContent)

	// Extract and save images if enabled
	if s.config.ExtractImages {
		imageExtractor := pdf.NewImageExtractor(pdfDoc)
		for pageNum := 1; pageNum <= pageCount; pageNum++ {
			images, err := imageExtractor.ExtractPageImages(pageNum)
			if err != nil {
				continue
			}

			for i, img := range images {
				imageID := fmt.Sprintf("img_%03d_%03d", pageNum, i+1)
				s.fs.SaveImage(docID, imageID, img.Data, img.Format)

				// Update content block references to images
				for j := range content.Blocks {
					if content.Blocks[j].Type == BlockTypeImage &&
						content.Blocks[j].Content == img.Name &&
						content.Blocks[j].Page == pageNum {
						content.Blocks[j].Content = fmt.Sprintf("images/%s%s", imageID, img.FileExtension())
					}
				}
			}
		}
	}

	// Build document
	name := filepath.Base(path)
	if config.Name != "" {
		name = config.Name
	}

	doc := &Document{
		Info: DocumentInfo{
			ID:           docID,
			Name:         name,
			OriginalPath: path,
			SizeBytes:    fileInfo.Size(),
			PageCount:    pageCount,
			Format:       FormatPDF,
			Checksum:     checksum,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		Content: content,
	}

	return doc, nil
}

// IndexReader indexes a document from an io.Reader
func (s *Store) IndexReader(r io.Reader, name string, opts ...IndexOption) (*Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config := defaultIndexConfig()
	config.Name = name

	for _, opt := range opts {
		opt(config)
	}

	// Read all data
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	// Detect format from name
	ext := strings.ToLower(filepath.Ext(name))

	var doc *Document

	switch ext {
	case ".pdf":
		doc, err = s.indexPDFFromBytes(data, name, config)
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}

	if err != nil {
		return nil, err
	}

	// Save to storage
	if err := s.fs.SaveDocument(toStorageDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Add to search index
	s.index.AddDocument(toSearchDocument(doc))

	// Save updated index
	if indexData, err := s.index.Serialize(); err == nil {
		s.fs.SaveIndex(indexData)
	}

	return doc, nil
}

// indexPDFFromBytes indexes a PDF from bytes
func (s *Store) indexPDFFromBytes(data []byte, name string, config *indexConfig) (*Document, error) {
	pdfDoc, err := pdf.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer pdfDoc.Close()

	if pdfDoc.IsEncrypted() {
		return nil, ErrEncryptedPDF
	}

	docID := generateID()

	pageCount, _ := pdfDoc.PageCount()

	extractor := pdf.NewSemanticExtractor(pdfDoc)
	pdfContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertPDFContent(pdfContent)

	doc := &Document{
		Info: DocumentInfo{
			ID:        docID,
			Name:      name,
			SizeBytes: int64(len(data)),
			PageCount: pageCount,
			Format:    FormatPDF,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Content: content,
	}

	return doc, nil
}

// GetDocument retrieves a document by ID
func (s *Store) GetDocument(id string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	storageDoc, err := s.fs.GetDocument(id)
	if err != nil {
		return nil, err
	}
	return fromStorageDocument(storageDoc), nil
}

// DeleteDocument removes a document from the store
func (s *Store) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove from index
	s.index.RemoveDocument(id)

	// Remove from storage
	if err := s.fs.DeleteDocument(id); err != nil {
		return err
	}

	// Save updated index
	if indexData, err := s.index.Serialize(); err == nil {
		s.fs.SaveIndex(indexData)
	}

	return nil
}

// ListDocuments returns all indexed documents
func (s *Store) ListDocuments() ([]*DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos, err := s.fs.ListDocuments()
	if err != nil {
		return nil, err
	}
	return fromStorageDocumentInfos(infos), nil
}

// Search performs a full-text search across all documents
func (s *Store) Search(query string, opts ...SearchOption) (*SearchResults, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startTime := time.Now()

	config := defaultSearchConfig()
	for _, opt := range opts {
		opt(config)
	}

	if query == "" {
		return nil, ErrInvalidQuery
	}

	// Search the index
	searchResults := s.index.Search(query, config.MaxResults)

	// Filter by minimum score
	if config.MinScore > 0 {
		searchResults = search.FilterByScore(searchResults, config.MinScore)
	}

	// Convert to our type and enrich results with content and context
	results := make([]SearchResult, len(searchResults))
	for i := range searchResults {
		results[i] = SearchResult{
			DocumentID:   searchResults[i].DocumentID,
			DocumentName: searchResults[i].DocumentName,
			BlockID:      searchResults[i].BlockID,
			Score:        searchResults[i].Score,
			Positions:    searchResults[i].Positions,
		}

		storageDoc, err := s.fs.GetDocument(searchResults[i].DocumentID)
		if err != nil {
			continue
		}
		doc := fromStorageDocument(storageDoc)

		block := doc.GetBlockByID(searchResults[i].BlockID)
		if block != nil {
			results[i].Content = block.Content
			results[i].Page = block.Page
			results[i].Section = block.Semantic.Section

			// Build snippet
			results[i].Snippet = buildSnippet(block.Content, query, config.HighlightPre, config.HighlightPost)

			// Get context if requested
			if config.ContextWindow > 0 {
				ctx, err := s.getContextLocked(searchResults[i].DocumentID, searchResults[i].BlockID, config.ContextWindow)
				if err == nil {
					results[i].Context = append(ctx.Before, ctx.Center)
					results[i].Context = append(results[i].Context, ctx.After...)
				}
			}
		}
	}

	return &SearchResults{
		Query:      query,
		TotalHits:  len(results),
		Results:    results,
		SearchTime: time.Since(startTime),
	}, nil
}

// SearchInDocument searches within a specific document
func (s *Store) SearchInDocument(docID, query string, opts ...SearchOption) (*SearchResults, error) {
	opts = append(opts, WithDocuments(docID))
	return s.Search(query, opts...)
}

// GetContext retrieves content blocks around a specific block
func (s *Store) GetContext(docID, blockID string, windowSize int) (*ContextResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.getContextLocked(docID, blockID, windowSize)
}

// getContextLocked gets context (caller must hold read lock)
func (s *Store) getContextLocked(docID, blockID string, windowSize int) (*ContextResult, error) {
	storageDoc, err := s.fs.GetDocument(docID)
	if err != nil {
		return nil, err
	}
	doc := fromStorageDocument(storageDoc)

	var centerIdx int = -1
	for i, b := range doc.Content.Blocks {
		if b.ID == blockID {
			centerIdx = i
			break
		}
	}

	if centerIdx < 0 {
		return nil, ErrInvalidInput
	}

	result := &ContextResult{
		DocumentID: docID,
		CenterID:   blockID,
		Center:     doc.Content.Blocks[centerIdx],
	}

	start := centerIdx - windowSize
	if start < 0 {
		start = 0
	}
	for i := start; i < centerIdx; i++ {
		result.Before = append(result.Before, doc.Content.Blocks[i])
	}

	end := centerIdx + windowSize + 1
	if end > len(doc.Content.Blocks) {
		end = len(doc.Content.Blocks)
	}
	for i := centerIdx + 1; i < end; i++ {
		result.After = append(result.After, doc.Content.Blocks[i])
	}

	return result, nil
}

// Close releases resources held by the store
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Save index before closing
	if indexData, err := s.index.Serialize(); err == nil {
		s.fs.SaveIndex(indexData)
	}

	return s.fs.Close()
}

// Stats returns statistics about the store
func (s *Store) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docs, _ := s.fs.ListDocuments()
	indexStats := s.index.Stats()

	var totalBlocks, totalImages int
	for _, docInfo := range docs {
		storageDoc, err := s.fs.GetDocument(docInfo.ID)
		if err != nil {
			continue
		}
		doc := fromStorageDocument(storageDoc)
		totalBlocks += len(doc.Content.Blocks)
		for _, block := range doc.Content.Blocks {
			if block.Type == BlockTypeImage {
				totalImages++
			}
		}
	}

	return StoreStats{
		DocumentCount: len(docs),
		TotalBlocks:   totalBlocks,
		TotalImages:   totalImages,
		IndexTerms:    indexStats.TermCount,
	}
}

// StoreStats contains statistics about the store
type StoreStats struct {
	DocumentCount int   `json:"document_count"`
	TotalBlocks   int   `json:"total_blocks"`
	TotalImages   int   `json:"total_images"`
	IndexTerms    int   `json:"index_terms"`
	StorageBytes  int64 `json:"storage_bytes"`
}

// Helper functions

func generateID() string {
	// Simple UUID-like ID
	now := time.Now().UnixNano()
	h := sha256.Sum256([]byte(fmt.Sprintf("%d", now)))
	return fmt.Sprintf("%x", h[:16])
}

func computeChecksum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

func buildSnippet(content, query, highlightPre, highlightPost string) string {
	// Simple snippet building - get context around first match
	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(content)

	idx := strings.Index(contentLower, queryLower)
	if idx < 0 {
		// No exact match, just return beginning
		if len(content) > 200 {
			return content[:200] + "..."
		}
		return content
	}

	// Get context around match
	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 100
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]

	// Add ellipsis
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	// Highlight matches (simple approach)
	if highlightPre != "" && highlightPost != "" {
		// Case-insensitive replace
		for i := 0; i <= len(snippet)-len(query); i++ {
			if strings.EqualFold(snippet[i:i+len(query)], query) {
				snippet = snippet[:i] + highlightPre + snippet[i:i+len(query)] + highlightPost + snippet[i+len(query):]
				i += len(highlightPre) + len(highlightPost)
			}
		}
	}

	return snippet
}

// convertPDFContent converts pdf.DocumentContent to docuindex.DocumentContent
func convertPDFContent(pdfContent *pdf.DocumentContent) DocumentContent {
	content := DocumentContent{
		Version: pdfContent.Version,
		Blocks:  make([]ContentBlock, len(pdfContent.Blocks)),
	}

	for i, block := range pdfContent.Blocks {
		content.Blocks[i] = ContentBlock{
			ID:      block.ID,
			Type:    BlockType(block.Type),
			Content: block.Content,
			Page:    block.Page,
			BBox: BoundingBox{
				X:          block.BBox.X,
				Y:          block.BBox.Y,
				Width:      block.BBox.Width,
				Height:     block.BBox.Height,
				PageWidth:  block.BBox.PageWidth,
				PageHeight: block.BBox.PageHeight,
			},
			Semantic: SemanticInfo{
				IsHeading:    block.Semantic.IsHeading,
				HeadingLevel: block.Semantic.HeadingLevel,
				Section:      block.Semantic.Section,
				Keywords:     block.Semantic.Keywords,
				Context:      block.Semantic.Context,
			},
			Children: block.Children,
		}

		if block.Font != nil {
			content.Blocks[i].Font = &FontInfo{
				Name:   block.Font.Name,
				Size:   block.Font.Size,
				Bold:   block.Font.Bold,
				Italic: block.Font.Italic,
			}
		}
	}

	return content
}

// toStorageDocument converts docuindex.Document to storage.Document
func toStorageDocument(doc *Document) *storage.Document {
	storageDoc := &storage.Document{
		Info: storage.DocumentInfo{
			ID:           doc.Info.ID,
			Name:         doc.Info.Name,
			OriginalPath: doc.Info.OriginalPath,
			SizeBytes:    doc.Info.SizeBytes,
			PageCount:    doc.Info.PageCount,
			Format:       storage.DocumentFormat(doc.Info.Format),
			Checksum:     doc.Info.Checksum,
			CreatedAt:    doc.Info.CreatedAt,
			UpdatedAt:    doc.Info.UpdatedAt,
		},
		Content: storage.DocumentContent{
			Version: doc.Content.Version,
			Blocks:  make([]storage.ContentBlock, len(doc.Content.Blocks)),
		},
	}

	for i, block := range doc.Content.Blocks {
		storageDoc.Content.Blocks[i] = storage.ContentBlock{
			ID:      block.ID,
			Type:    storage.BlockType(block.Type),
			Content: block.Content,
			Page:    block.Page,
			BBox: storage.BoundingBox{
				X:          block.BBox.X,
				Y:          block.BBox.Y,
				Width:      block.BBox.Width,
				Height:     block.BBox.Height,
				PageWidth:  block.BBox.PageWidth,
				PageHeight: block.BBox.PageHeight,
			},
			Semantic: storage.SemanticInfo{
				IsHeading:    block.Semantic.IsHeading,
				HeadingLevel: block.Semantic.HeadingLevel,
				Section:      block.Semantic.Section,
				Keywords:     block.Semantic.Keywords,
				Context:      block.Semantic.Context,
			},
			Children: block.Children,
		}
		if block.Font != nil {
			storageDoc.Content.Blocks[i].Font = &storage.FontInfo{
				Name:   block.Font.Name,
				Size:   block.Font.Size,
				Bold:   block.Font.Bold,
				Italic: block.Font.Italic,
			}
		}
	}

	return storageDoc
}

// fromStorageDocument converts storage.Document to docuindex.Document
func fromStorageDocument(storageDoc *storage.Document) *Document {
	doc := &Document{
		Info: DocumentInfo{
			ID:           storageDoc.Info.ID,
			Name:         storageDoc.Info.Name,
			OriginalPath: storageDoc.Info.OriginalPath,
			SizeBytes:    storageDoc.Info.SizeBytes,
			PageCount:    storageDoc.Info.PageCount,
			Format:       DocumentFormat(storageDoc.Info.Format),
			Checksum:     storageDoc.Info.Checksum,
			CreatedAt:    storageDoc.Info.CreatedAt,
			UpdatedAt:    storageDoc.Info.UpdatedAt,
		},
		Content: DocumentContent{
			Version: storageDoc.Content.Version,
			Blocks:  make([]ContentBlock, len(storageDoc.Content.Blocks)),
		},
	}

	for i, block := range storageDoc.Content.Blocks {
		doc.Content.Blocks[i] = ContentBlock{
			ID:      block.ID,
			Type:    BlockType(block.Type),
			Content: block.Content,
			Page:    block.Page,
			BBox: BoundingBox{
				X:          block.BBox.X,
				Y:          block.BBox.Y,
				Width:      block.BBox.Width,
				Height:     block.BBox.Height,
				PageWidth:  block.BBox.PageWidth,
				PageHeight: block.BBox.PageHeight,
			},
			Semantic: SemanticInfo{
				IsHeading:    block.Semantic.IsHeading,
				HeadingLevel: block.Semantic.HeadingLevel,
				Section:      block.Semantic.Section,
				Keywords:     block.Semantic.Keywords,
				Context:      block.Semantic.Context,
			},
			Children: block.Children,
		}
		if block.Font != nil {
			doc.Content.Blocks[i].Font = &FontInfo{
				Name:   block.Font.Name,
				Size:   block.Font.Size,
				Bold:   block.Font.Bold,
				Italic: block.Font.Italic,
			}
		}
	}

	return doc
}

// toSearchDocument converts docuindex.Document to search.Document
func toSearchDocument(doc *Document) *search.Document {
	searchDoc := &search.Document{
		Info: search.DocumentInfo{
			ID:           doc.Info.ID,
			Name:         doc.Info.Name,
			OriginalPath: doc.Info.OriginalPath,
			SizeBytes:    doc.Info.SizeBytes,
			PageCount:    doc.Info.PageCount,
			Format:       search.DocumentFormat(doc.Info.Format),
			Checksum:     doc.Info.Checksum,
			CreatedAt:    doc.Info.CreatedAt,
			UpdatedAt:    doc.Info.UpdatedAt,
		},
		Content: search.DocumentContent{
			Version: doc.Content.Version,
			Blocks:  make([]search.ContentBlock, len(doc.Content.Blocks)),
		},
	}

	for i, block := range doc.Content.Blocks {
		searchDoc.Content.Blocks[i] = search.ContentBlock{
			ID:      block.ID,
			Type:    search.BlockType(block.Type),
			Content: block.Content,
			Page:    block.Page,
			BBox: search.BoundingBox{
				X:          block.BBox.X,
				Y:          block.BBox.Y,
				Width:      block.BBox.Width,
				Height:     block.BBox.Height,
				PageWidth:  block.BBox.PageWidth,
				PageHeight: block.BBox.PageHeight,
			},
			Semantic: search.SemanticInfo{
				IsHeading:    block.Semantic.IsHeading,
				HeadingLevel: block.Semantic.HeadingLevel,
				Section:      block.Semantic.Section,
				Keywords:     block.Semantic.Keywords,
				Context:      block.Semantic.Context,
			},
			Children: block.Children,
		}
		if block.Font != nil {
			searchDoc.Content.Blocks[i].Font = &search.FontInfo{
				Name:   block.Font.Name,
				Size:   block.Font.Size,
				Bold:   block.Font.Bold,
				Italic: block.Font.Italic,
			}
		}
	}

	return searchDoc
}

// fromSearchResults converts []search.SearchResult to []SearchResult
func fromSearchResults(results []search.SearchResult) []SearchResult {
	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{
			DocumentID:   r.DocumentID,
			DocumentName: r.DocumentName,
			BlockID:      r.BlockID,
			Content:      r.Content,
			Snippet:      r.Snippet,
			Score:        r.Score,
			Page:         r.Page,
			Section:      r.Section,
			Positions:    r.Positions,
		}
	}
	return out
}

// fromStorageDocumentInfos converts []*storage.DocumentInfo to []*DocumentInfo
func fromStorageDocumentInfos(infos []*storage.DocumentInfo) []*DocumentInfo {
	out := make([]*DocumentInfo, len(infos))
	for i, info := range infos {
		out[i] = &DocumentInfo{
			ID:           info.ID,
			Name:         info.Name,
			OriginalPath: info.OriginalPath,
			SizeBytes:    info.SizeBytes,
			PageCount:    info.PageCount,
			Format:       DocumentFormat(info.Format),
			Checksum:     info.Checksum,
			CreatedAt:    info.CreatedAt,
			UpdatedAt:    info.UpdatedAt,
		}
	}
	return out
}
