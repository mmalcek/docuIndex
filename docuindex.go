package docuindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	// Image format decoders for detectImageDimensions
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/google/uuid"
	"github.com/mmalcek/docuIndex/docx"
	"github.com/mmalcek/docuIndex/embedding"
	"github.com/mmalcek/docuIndex/pdf"
	hsearch "github.com/mmalcek/docuIndex/search"
	"github.com/mmalcek/docuIndex/sqlite"
	"github.com/mmalcek/docuIndex/vectorindex"
)

// Store manages document storage, indexing, and search with unified SQLite backend
type Store struct {
	config   *storeConfig
	mu       sync.RWMutex
	db       *sqlite.Store
	hnsw     *vectorindex.HNSW
	embedder embedding.Provider
	hybrid   *hsearch.HybridSearcher

	// Background embedding state
	bgMu      sync.Mutex
	bgRunning bool
	bgCancel  context.CancelFunc
	bgStatus  BackgroundEmbeddingStatus
	bgDone    chan struct{}
}

// NewStore creates a new document store at the specified path using SQLite
func NewStore(basePath string, opts ...StoreOption) (*Store, error) {
	config := defaultStoreConfig()
	config.BasePath = basePath

	for _, opt := range opts {
		opt(config)
	}

	// Create SQLite store
	db, err := sqlite.NewStore(basePath, Version,
		sqlite.WithImageExtraction(config.ExtractImages),
		sqlite.WithSemanticAnalysis(config.ExtractSemantics),
		sqlite.WithChecksum(config.ComputeChecksum),
		sqlite.WithStemming(config.EnableStemming),
		sqlite.WithStopWords(config.EnableStopWords),
	)
	if err != nil {
		return nil, fmt.Errorf("create SQLite store: %w", err)
	}

	s := &Store{
		config: config,
		db:     db,
	}

	// Check vector count before loading HNSW to prevent OOM
	vectorCount, err := db.GetVectorCount()
	if err != nil {
		log.Printf("warning: could not get vector count: %v", err)
	}

	// Load or create HNSW index if within limits
	hnswPath := filepath.Join(basePath, "hnsw.idx")
	var hnswCfg *vectorindex.Config
	if config.HNSWConfig != nil {
		hnswCfg = &vectorindex.Config{
			M:        config.HNSWConfig.M,
			EfConst:  config.HNSWConfig.EfConst,
			EfSearch: config.HNSWConfig.EfSearch,
		}
	}
	s.hnsw = vectorindex.NewHNSW(hnswCfg)

	// Only load HNSW if vector count is within limits
	if config.MaxHNSWVectors > 0 && vectorCount > config.MaxHNSWVectors {
		log.Printf("WARNING: Vector count (%d) exceeds MaxHNSWVectors (%d), using brute-force SQLite search instead of HNSW",
			vectorCount, config.MaxHNSWVectors)
		// Keep empty HNSW, vectorSearch will fall back to brute-force
	} else if _, err := os.Stat(hnswPath); err == nil {
		// Load existing index
		log.Printf("Loading HNSW index (%d vectors)...", vectorCount)
		if err := s.hnsw.LoadFromFile(hnswPath); err != nil {
			log.Printf("WARNING: Failed to load HNSW index: %v, will use brute-force search", err)
		} else {
			log.Printf("HNSW index loaded: %d vectors", s.hnsw.Size())
		}
	}

	// Initialize hybrid searcher
	s.hybrid = hsearch.NewHybridSearcher()
	s.hybrid.KeywordSearch = s.keywordSearch
	s.hybrid.GetBlockContent = s.getBlockContent
	s.hybrid.GetDocumentName = s.getDocumentName

	return s, nil
}

// SetEmbeddingProvider configures the embedding provider after store creation.
// It automatically detects and repairs inconsistencies between the HNSW index
// and SQLite vectors, rebuilding the index if necessary using streaming to avoid OOM.
// If vector count exceeds MaxHNSWVectors, HNSW is skipped and brute-force search is used.
func (s *Store) SetEmbeddingProvider(provider embedding.Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.embedder = provider

	// Set up vector search in hybrid searcher
	s.hybrid.VectorSearch = s.vectorSearch

	// Count vectors in SQLite (memory efficient - no data loaded)
	sqliteCount, err := s.db.GetVectorCount()
	if err != nil {
		return fmt.Errorf("count vectors: %w", err)
	}

	// Check if vector count exceeds HNSW limit
	if s.config.MaxHNSWVectors > 0 && sqliteCount > s.config.MaxHNSWVectors {
		log.Printf("WARNING: Vector count (%d) exceeds MaxHNSWVectors (%d), skipping HNSW rebuild. Using brute-force SQLite search.",
			sqliteCount, s.config.MaxHNSWVectors)
		// Clear HNSW to ensure it's empty, vectorSearch will fall back to brute-force
		s.hnsw = vectorindex.NewHNSW(nil)
		return nil
	}

	// Check for inconsistency between HNSW and SQLite
	hnswSize := s.hnsw.Size()

	if hnswSize == sqliteCount && sqliteCount > 0 {
		// HNSW is in sync, no rebuild needed
		log.Printf("HNSW index in sync with SQLite (%d vectors)", sqliteCount)
		return nil
	}

	if hnswSize != sqliteCount {
		log.Printf("warning: HNSW index (%d vectors) out of sync with SQLite (%d vectors), rebuilding...",
			hnswSize, sqliteCount)

		// Create fresh HNSW index with current config
		var hnswCfg *vectorindex.Config
		if s.config.HNSWConfig != nil {
			hnswCfg = &vectorindex.Config{
				M:        s.config.HNSWConfig.M,
				EfConst:  s.config.HNSWConfig.EfConst,
				EfSearch: s.config.HNSWConfig.EfSearch,
			}
		}
		s.hnsw = vectorindex.NewHNSW(hnswCfg)
	}

	// Rebuild HNSW from SQLite using streaming/batched approach to avoid OOM
	if sqliteCount > 0 {
		const batchSize = 1000
		offset := 0
		totalAdded := 0

		for {
			// Load batch of vectors
			vectors, err := s.db.GetVectorsBatched(offset, batchSize)
			if err != nil {
				return fmt.Errorf("load vectors batch at offset %d: %w", offset, err)
			}

			if len(vectors) == 0 {
				break // No more vectors
			}

			// Convert to HNSW items
			hnswItems := make([]vectorindex.VectorItem, len(vectors))
			for i, v := range vectors {
				hnswItems[i] = vectorindex.VectorItem{
					ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
					Vector: v.Vector,
				}
			}

			// Add batch to HNSW
			if err := s.hnsw.AddBatch(hnswItems); err != nil {
				log.Printf("warning: failed to add batch at offset %d to HNSW: %v", offset, err)
			}

			totalAdded += len(vectors)
			offset += batchSize

			// Log progress for large rebuilds
			if totalAdded%10000 == 0 {
				log.Printf("HNSW rebuild progress: %d/%d vectors", totalAdded, sqliteCount)
			}
		}

		log.Printf("HNSW rebuild completed: %d vectors", totalAdded)
	}

	// Save rebuilt HNSW to disk
	return s.saveHNSWIndex()
}

// pendingImage holds image data to be saved after document creation
type pendingImage struct {
	Data        []byte
	Format      string
	Width       int
	Height      int
	Page        int
	Name        string
	BlockIndex  int    // Index in content.Blocks to update
	BlockID     string // Parent entry block ID (for CustomData images)
	Description string // AI-friendly description (for CustomData images)
}

// isValidImageFormat checks if format is supported
func isValidImageFormat(format string) bool {
	switch strings.ToLower(format) {
	case "png", "jpeg", "jpg", "gif", "bmp", "tiff", "tif":
		return true
	default:
		return false
	}
}

// detectImageDimensions extracts width/height from image data
func detectImageDimensions(data []byte) (int, int, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

// normalizeImageFormat normalizes image format strings (e.g., "jpg" -> "jpeg")
func normalizeImageFormat(format string) string {
	format = strings.ToLower(format)
	if format == "jpg" {
		return "jpeg"
	}
	if format == "tif" {
		return "tiff"
	}
	return format
}

// sourceOrDefault returns source if non-empty, otherwise returns defaultSource
func sourceOrDefault(source, defaultSource string) string {
	if source != "" {
		return source
	}
	return defaultSource
}

// chunkText splits long text into smaller chunks with overlap for embedding.
// If text length is within maxChars, returns the original text as single chunk.
// Uses rune count (not byte count) to handle Unicode correctly.
// Tries to break at natural boundaries (paragraphs, sentences, words).
func chunkText(text string, maxChars, overlap int) []string {
	runeCount := utf8.RuneCountInString(text)
	if runeCount <= maxChars {
		return []string{text}
	}

	runes := []rune(text)
	var chunks []string
	start := 0
	for start < len(runes) {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}

		// Try to break at sentence/paragraph boundary
		if end < len(runes) {
			breakPoint := findBreakPointRunes(runes, start, end)
			if breakPoint > start {
				end = breakPoint
			}
		}

		chunks = append(chunks, string(runes[start:end]))

		// If we've reached the end of text, we're done
		if end >= len(runes) {
			break
		}

		// Move start forward, accounting for overlap
		newStart := end - overlap
		if newStart <= start || overlap <= 0 {
			// No progress would be made, move to end without overlap
			newStart = end
		}
		start = newStart
	}
	return chunks
}

// findBreakPointRunes finds the best break point near the end position.
// Works with runes for proper Unicode support.
// Searches backwards for paragraph breaks, sentence ends, newlines, or word boundaries.
func findBreakPointRunes(runes []rune, start, end int) int {
	// Search within last 500 runes before end for natural break
	searchStart := end - 500
	if searchStart < start {
		searchStart = start
	}

	searchText := string(runes[searchStart:end])

	// Try paragraph break first (\n\n)
	if idx := strings.LastIndex(searchText, "\n\n"); idx >= 0 {
		return searchStart + utf8.RuneCountInString(searchText[:idx]) + 2
	}
	// Try sentence break (. followed by space or newline)
	if idx := strings.LastIndex(searchText, ". "); idx >= 0 {
		return searchStart + utf8.RuneCountInString(searchText[:idx]) + 2
	}
	if idx := strings.LastIndex(searchText, ".\n"); idx >= 0 {
		return searchStart + utf8.RuneCountInString(searchText[:idx]) + 2
	}
	// Try newline
	if idx := strings.LastIndex(searchText, "\n"); idx >= 0 {
		return searchStart + utf8.RuneCountInString(searchText[:idx]) + 1
	}
	// Try space (word boundary)
	if idx := strings.LastIndex(searchText, " "); idx >= 0 {
		return searchStart + utf8.RuneCountInString(searchText[:idx]) + 1
	}
	// No good break point found, use hard cut
	return end
}

// embedWithTruncationFallback embeds texts with automatic truncation if token limit is exceeded.
// Returns the embeddings and potentially truncated texts. Logs warnings when truncation occurs.
func (s *Store) embedWithTruncationFallback(ctx context.Context, texts []string, docID string) ([][]float32, []string, error) {
	embeddings, err := s.embedder.Embed(ctx, texts)

	// Handle token limit errors by truncating content
	if err != nil && strings.Contains(err.Error(), "maximum context length") {
		log.Printf("WARNING: Token limit exceeded for doc %s, truncating content (original sizes: %v chars)", docID, getTextLengths(texts))

		// Truncate each text by 50% and retry
		truncatedTexts := make([]string, len(texts))
		for i, t := range texts {
			truncateLen := len(t) / 2
			if truncateLen > 0 {
				truncatedTexts[i] = t[:truncateLen]
			} else {
				truncatedTexts[i] = t
			}
		}

		embeddings, err = s.embedder.Embed(ctx, truncatedTexts)
		if err != nil && strings.Contains(err.Error(), "maximum context length") {
			// Still failing, try even more aggressive truncation (25%)
			log.Printf("WARNING: Still exceeding token limit after 50%% truncation for doc %s, trying 25%%", docID)
			for i, t := range texts {
				truncateLen := len(t) / 4
				if truncateLen > 0 {
					truncatedTexts[i] = t[:truncateLen]
				}
			}
			embeddings, err = s.embedder.Embed(ctx, truncatedTexts)
		}

		if err == nil {
			return embeddings, truncatedTexts, nil
		}
	}

	return embeddings, texts, err
}

// getTextLengths returns a slice of text lengths for logging
func getTextLengths(texts []string) []int {
	lengths := make([]int, len(texts))
	for i, t := range texts {
		lengths[i] = len(t)
	}
	return lengths
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
	var pendingImages []pendingImage
	var err error

	switch ext {
	case ".pdf":
		doc, pendingImages, err = s.indexPDF(path, config)
	case ".docx":
		doc, pendingImages, err = s.indexDOCX(path, config)
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}

	if err != nil {
		return nil, err
	}

	// Save to SQLite (document must exist before images due to FK constraint)
	if err := s.db.SaveDocument(toSQLiteDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Save tags if provided
	if len(config.Tags) > 0 {
		if err := s.db.SaveDocumentTags(doc.Info.ID, config.Tags); err != nil {
			return nil, fmt.Errorf("save tags: %w", err)
		}
	}

	// Now save images (after document exists in DB)
	for _, img := range pendingImages {
		// Get block ID if we have a matching content block
		blockID := img.BlockID
		if blockID == "" && img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			blockID = doc.Content.Blocks[img.BlockIndex].ID
		}
		imageID, err := s.db.SaveImage(doc.Info.ID, img.Data, img.Format, img.Width, img.Height, img.Page, img.Name, blockID, img.Description)
		if err != nil {
			continue // Skip failed images
		}
		// Update content block reference
		if img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			doc.Content.Blocks[img.BlockIndex].Content = fmt.Sprintf("images/%s", imageID)
		}
	}

	// Index for BM25 search
	blocks := toSQLiteBlocks(doc)
	if err := s.db.IndexDocument(doc.Info.ID, blocks); err != nil {
		return nil, fmt.Errorf("index document: %w", err)
	}

	// Generate embeddings if provider configured and not deferred
	if !config.DeferEmbedding && s.embedder != nil {
		if err := s.embedDocument(doc); err != nil {
			// Log but don't fail - document is already saved
			fmt.Printf("warning: embedding failed: %v\n", err)
		}
	}

	return doc, nil
}

// embedDocument generates and stores embeddings for a document
func (s *Store) embedDocument(doc *Document) error {
	ctx := context.Background()

	// Chunking config with defaults
	maxChars := s.config.MaxChunkChars
	if maxChars <= 0 {
		maxChars = 48000 // Default for models with higher token limits
	}
	overlap := s.config.ChunkOverlap
	if overlap <= 0 {
		overlap = 200
	}

	// Collect text blocks with chunking for long content
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if len(block.Content) > 0 {
				chunks := chunkText(block.Content, maxChars, overlap)
				for i, chunk := range chunks {
					texts = append(texts, chunk)
					// Use block ID with chunk suffix for multi-chunk blocks
					if len(chunks) > 1 {
						blockIDs = append(blockIDs, fmt.Sprintf("%s#%d", block.ID, i))
					} else {
						blockIDs = append(blockIDs, block.ID)
					}
				}
			}
		}
	}

	if len(texts) == 0 {
		return nil
	}

	// Generate embeddings in batches with truncation fallback
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, usedTexts, err := s.embedWithTruncationFallback(ctx, texts[i:end], doc.Info.ID)
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       usedTexts[j],
				Model:      s.embedder.Name(),
			})
		}
	}

	// Save vectors to SQLite
	if err := s.db.SaveVectors(doc.Info.ID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

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

	// Persist HNSW index
	hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
	if err := s.hnsw.SaveToFile(hnswPath); err != nil {
		return fmt.Errorf("save HNSW index: %w", err)
	}

	return nil
}

// indexPDF indexes a PDF file
func (s *Store) indexPDF(path string, config *indexConfig) (*Document, []pendingImage, error) {
	pdfDoc, err := pdf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open PDF: %w", err)
	}
	defer pdfDoc.Close()

	if pdfDoc.IsEncrypted() {
		return nil, nil, ErrEncryptedPDF
	}

	docID := generateID()

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}

	var checksum string
	if s.config.ComputeChecksum {
		checksum, _ = computeChecksum(path)
	}

	pageCount, err := pdfDoc.PageCount()
	if err != nil {
		pageCount = 0
	}

	extractor := pdf.NewSemanticExtractor(pdfDoc)
	pdfContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertPDFContent(pdfContent)

	// Collect images to be saved later (after document is in DB)
	var pending []pendingImage
	if s.config.ExtractImages {
		imageExtractor := pdf.NewImageExtractor(pdfDoc)
		for pageNum := 1; pageNum <= pageCount; pageNum++ {
			images, err := imageExtractor.ExtractPageImages(pageNum)
			if err != nil {
				continue
			}

			for _, img := range images {
				// Find the matching content block index
				blockIndex := -1
				for j := range content.Blocks {
					if content.Blocks[j].Type == BlockTypeImage &&
						content.Blocks[j].Content == img.Name &&
						content.Blocks[j].Page == pageNum {
						blockIndex = j
						break
					}
				}

				pending = append(pending, pendingImage{
					Data:       img.Data,
					Format:     img.Format,
					Width:      img.Width,
					Height:     img.Height,
					Page:       pageNum,
					Name:       img.Name,
					BlockIndex: blockIndex,
				})
			}
		}
	}

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
			Source:       sourceOrDefault(config.Source, string(FormatPDF)),
			Checksum:     checksum,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		Content: content,
	}

	return doc, pending, nil
}

// indexDOCX indexes a DOCX file
func (s *Store) indexDOCX(path string, config *indexConfig) (*Document, []pendingImage, error) {
	docxDoc, err := docx.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open DOCX: %w", err)
	}
	defer docxDoc.Close()

	docID := generateID()

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}

	var checksum string
	if s.config.ComputeChecksum {
		checksum, _ = computeChecksum(path)
	}

	pageCount, _ := docxDoc.PageCount()
	if pageCount == 0 {
		pageCount = 1
	}

	extractor, err := docx.NewSemanticExtractor(docxDoc)
	if err != nil {
		return nil, nil, fmt.Errorf("create extractor: %w", err)
	}

	docxContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertDOCXContent(docxContent)

	// Collect images to be saved later (after document is in DB)
	var pending []pendingImage
	if s.config.ExtractImages {
		imageExtractor, err := docx.NewImageExtractor(docxDoc)
		if err == nil {
			images, err := imageExtractor.ExtractAllImages()
			if err == nil {
				for _, img := range images {
					// Find the matching content block index
					blockIndex := -1
					for j := range content.Blocks {
						if content.Blocks[j].Type == BlockTypeImage &&
							content.Blocks[j].Content == img.Name {
							blockIndex = j
							break
						}
					}

					pending = append(pending, pendingImage{
						Data:       img.Data,
						Format:     img.Format,
						Width:      0,
						Height:     0,
						Page:       0,
						Name:       img.Name,
						BlockIndex: blockIndex,
					})
				}
			}
		}
	}

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
			Format:       FormatDOCX,
			Source:       sourceOrDefault(config.Source, string(FormatDOCX)),
			Checksum:     checksum,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		Content: content,
	}

	return doc, pending, nil
}

// IndexCustomData indexes custom structured data
func (s *Store) IndexCustomData(data *CustomData, opts ...IndexOption) (*Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config := defaultIndexConfig()
	for _, opt := range opts {
		opt(config)
	}

	// Validate input
	if data == nil {
		return nil, NewCustomDataError("", "data is nil", ErrInvalidCustomData)
	}
	if data.Source == "" {
		return nil, NewCustomDataError("", "source is required", ErrMissingSource)
	}
	if data.Name == "" {
		return nil, NewCustomDataError(data.Source, "name is required", ErrInvalidCustomData)
	}
	if len(data.Entries) == 0 {
		return nil, NewCustomDataError(data.Source, "at least one entry is required", ErrMissingEntries)
	}

	docID := generateID()
	now := time.Now()

	// Use provided import time or default to now
	importedAt := data.ImportedAt
	if importedAt.IsZero() {
		importedAt = now
	}

	// Convert entries to content blocks and collect images
	var blocks []ContentBlock
	var pendingImages []pendingImage
	var totalSize int

	for i, entry := range data.Entries {
		entryID := entry.ID
		if entryID == "" {
			entryID = fmt.Sprintf("entry_%d", i+1)
		}

		// Add entry content block
		blocks = append(blocks, ContentBlock{
			ID:      entryID,
			Type:    BlockTypeCustom,
			Content: entry.Content,
			Page:    1, // Custom data doesn't have pages
			Semantic: SemanticInfo{
				Section: data.Source, // Use source as section for grouping
			},
		})

		totalSize += len(entry.Content)

		// Process entry-level images
		for j, img := range entry.Images {
			if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
				continue
			}

			imgID := uuid.New().String()
			format := normalizeImageFormat(img.Format)

			// Auto-detect dimensions if not provided
			width, height := img.Width, img.Height
			if width == 0 || height == 0 {
				if w, h, err := detectImageDimensions(img.Data); err == nil {
					width, height = w, h
				}
			}

			name := img.OriginalName
			if name == "" {
				name = fmt.Sprintf("image_%d.%s", j+1, format)
			}

			// Add image block
			blocks = append(blocks, ContentBlock{
				ID:   imgID,
				Type: BlockTypeImage,
				Page: 1,
				Semantic: SemanticInfo{
					Section: data.Source,
				},
			})

			// Queue image for saving after document exists
			pendingImages = append(pendingImages, pendingImage{
				Data:        img.Data,
				Format:      format,
				Width:       width,
				Height:      height,
				Page:        1,
				Name:        name,
				BlockIndex:  len(blocks) - 1, // Index of the image block just added
				BlockID:     entryID,         // Link to parent entry
				Description: img.Description,
			})
		}
	}

	// Process document-level images
	for j, img := range data.Images {
		if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
			continue
		}

		imgID := uuid.New().String()
		format := normalizeImageFormat(img.Format)

		// Auto-detect dimensions if not provided
		width, height := img.Width, img.Height
		if width == 0 || height == 0 {
			if w, h, err := detectImageDimensions(img.Data); err == nil {
				width, height = w, h
			}
		}

		name := img.OriginalName
		if name == "" {
			name = fmt.Sprintf("doc_image_%d.%s", j+1, format)
		}

		// Add image block
		blocks = append(blocks, ContentBlock{
			ID:   imgID,
			Type: BlockTypeImage,
			Page: 1,
			Semantic: SemanticInfo{
				Section: data.Source,
			},
		})

		// Queue image for saving after document exists
		pendingImages = append(pendingImages, pendingImage{
			Data:        img.Data,
			Format:      format,
			Width:       width,
			Height:      height,
			Page:        1,
			Name:        name,
			BlockIndex:  len(blocks) - 1, // Index of the image block just added
			BlockID:     "",              // No parent entry for document-level images
			Description: img.Description,
		})
	}

	name := data.Name
	if config.Name != "" {
		name = config.Name
	}

	doc := &Document{
		Info: DocumentInfo{
			ID:          docID,
			Name:        name,
			Format:      FormatCustomData,
			SizeBytes:   int64(totalSize),
			PageCount:   1,
			Source:      data.Source,
			Description: data.Description,
			CreatedAt:   now,
			UpdatedAt:   now,
			ImportedAt:  importedAt,
		},
		Content: DocumentContent{
			Version: "1.0",
			Blocks:  blocks,
		},
	}

	// Save to SQLite
	if err := s.db.SaveDocument(toSQLiteDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Save tags if provided
	if len(data.Tags) > 0 {
		if err := s.db.SaveDocumentTags(docID, data.Tags); err != nil {
			return nil, fmt.Errorf("save tags: %w", err)
		}
	}

	// Save images (after document exists in DB)
	for _, img := range pendingImages {
		blockID := img.BlockID
		if blockID == "" && img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			blockID = doc.Content.Blocks[img.BlockIndex].ID
		}
		imageID, err := s.db.SaveImage(doc.Info.ID, img.Data, img.Format, img.Width, img.Height, img.Page, img.Name, blockID, img.Description)
		if err != nil {
			fmt.Printf("warning: failed to save image %s: %v\n", img.Name, err)
			continue
		}
		// Update content block reference
		if img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			doc.Content.Blocks[img.BlockIndex].Content = fmt.Sprintf("images/%s", imageID)
		}
	}

	// Index for BM25 search
	sqliteBlocks := toSQLiteBlocks(doc)
	// Add entry metadata to blocks (only for entry blocks, not image blocks)
	entryIdx := 0
	for i := range sqliteBlocks {
		if sqliteBlocks[i].Type == string(BlockTypeCustom) && entryIdx < len(data.Entries) {
			if len(data.Entries[entryIdx].Metadata) > 0 {
				sqliteBlocks[i].EntryMetadata = data.Entries[entryIdx].Metadata
			}
			entryIdx++
		}
	}

	if err := s.db.IndexDocument(doc.Info.ID, sqliteBlocks); err != nil {
		return nil, fmt.Errorf("index document: %w", err)
	}

	// Generate embeddings if provider configured and not deferred
	if !config.DeferEmbedding && s.embedder != nil {
		if err := s.embedDocument(doc); err != nil {
			fmt.Printf("warning: embedding failed: %v\n", err)
		}
	}

	return doc, nil
}

// UpsertCustomData updates an existing document or creates a new one based on source + external_id.
// If ExternalID is provided and a document with the same source + external_id exists, it will be updated.
// If ExternalID is empty or no matching document exists, a new document is created.
func (s *Store) UpsertCustomData(data *CustomData, opts ...IndexOption) (*Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	config := defaultIndexConfig()
	for _, opt := range opts {
		opt(config)
	}

	// Validate input
	if data == nil {
		return nil, NewCustomDataError("", "data is nil", ErrInvalidCustomData)
	}
	if data.Source == "" {
		return nil, NewCustomDataError("", "source is required", ErrMissingSource)
	}
	if data.Name == "" {
		return nil, NewCustomDataError(data.Source, "name is required", ErrInvalidCustomData)
	}
	if len(data.Entries) == 0 {
		return nil, NewCustomDataError(data.Source, "at least one entry is required", ErrMissingEntries)
	}

	// Check if document exists by source + external_id
	var existingDoc *sqlite.DocumentInfo
	var err error
	if data.ExternalID != "" {
		existingDoc, err = s.db.FindBySourceAndExternalID(data.Source, data.ExternalID)
		if err != nil {
			return nil, fmt.Errorf("find existing document: %w", err)
		}
	}

	now := time.Now()

	// Use provided import time or default to now
	importedAt := data.ImportedAt
	if importedAt.IsZero() {
		importedAt = now
	}

	// Determine document ID - reuse existing or generate new
	var docID string
	var createdAt time.Time
	if existingDoc != nil {
		docID = existingDoc.ID
		createdAt = existingDoc.CreatedAt // Preserve original creation time

		// Delete existing images before re-indexing
		if err := s.db.DeleteImagesForDocument(docID); err != nil {
			return nil, fmt.Errorf("delete existing images: %w", err)
		}

		// Delete existing vectors from HNSW before re-indexing
		if s.hnsw != nil {
			vectors, _ := s.db.GetVectorsForDocument(docID)
			for _, v := range vectors {
				hnswID := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
				s.hnsw.Delete(hnswID)
			}
		}
	} else {
		docID = generateID()
		createdAt = now
	}

	// Convert entries to content blocks and collect images
	var blocks []ContentBlock
	var pendingImages []pendingImage
	var totalSize int

	for i, entry := range data.Entries {
		entryID := entry.ID
		if entryID == "" {
			entryID = fmt.Sprintf("entry_%d", i+1)
		}

		// Add entry content block
		blocks = append(blocks, ContentBlock{
			ID:      entryID,
			Type:    BlockTypeCustom,
			Content: entry.Content,
			Page:    1, // Custom data doesn't have pages
			Semantic: SemanticInfo{
				Section: data.Source, // Use source as section for grouping
			},
		})

		totalSize += len(entry.Content)

		// Process entry-level images
		for j, img := range entry.Images {
			if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
				continue
			}

			imgID := uuid.New().String()
			format := normalizeImageFormat(img.Format)

			// Auto-detect dimensions if not provided
			width, height := img.Width, img.Height
			if width == 0 || height == 0 {
				if w, h, err := detectImageDimensions(img.Data); err == nil {
					width, height = w, h
				}
			}

			name := img.OriginalName
			if name == "" {
				name = fmt.Sprintf("image_%d.%s", j+1, format)
			}

			// Add image block
			blocks = append(blocks, ContentBlock{
				ID:   imgID,
				Type: BlockTypeImage,
				Page: 1,
				Semantic: SemanticInfo{
					Section: data.Source,
				},
			})

			// Queue image for saving after document exists
			pendingImages = append(pendingImages, pendingImage{
				Data:        img.Data,
				Format:      format,
				Width:       width,
				Height:      height,
				Page:        1,
				Name:        name,
				BlockIndex:  len(blocks) - 1, // Index of the image block just added
				BlockID:     entryID,         // Link to parent entry
				Description: img.Description,
			})
		}
	}

	// Process document-level images
	for j, img := range data.Images {
		if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
			continue
		}

		imgID := uuid.New().String()
		format := normalizeImageFormat(img.Format)

		// Auto-detect dimensions if not provided
		width, height := img.Width, img.Height
		if width == 0 || height == 0 {
			if w, h, err := detectImageDimensions(img.Data); err == nil {
				width, height = w, h
			}
		}

		name := img.OriginalName
		if name == "" {
			name = fmt.Sprintf("doc_image_%d.%s", j+1, format)
		}

		// Add image block
		blocks = append(blocks, ContentBlock{
			ID:   imgID,
			Type: BlockTypeImage,
			Page: 1,
			Semantic: SemanticInfo{
				Section: data.Source,
			},
		})

		// Queue image for saving after document exists
		pendingImages = append(pendingImages, pendingImage{
			Data:        img.Data,
			Format:      format,
			Width:       width,
			Height:      height,
			Page:        1,
			Name:        name,
			BlockIndex:  len(blocks) - 1, // Index of the image block just added
			BlockID:     "",              // No parent entry for document-level images
			Description: img.Description,
		})
	}

	name := data.Name
	if config.Name != "" {
		name = config.Name
	}

	doc := &Document{
		Info: DocumentInfo{
			ID:          docID,
			Name:        name,
			Format:      FormatCustomData,
			SizeBytes:   int64(totalSize),
			PageCount:   1,
			Source:      data.Source,
			Description: data.Description,
			CreatedAt:   createdAt,
			UpdatedAt:   now,
			ImportedAt:  importedAt,
			ExternalID:  data.ExternalID,
		},
		Content: DocumentContent{
			Version: "1.0",
			Blocks:  blocks,
		},
	}

	// Save to SQLite (INSERT OR REPLACE)
	if err := s.db.SaveDocument(toSQLiteDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Save tags if provided (this will replace existing tags)
	if len(data.Tags) > 0 {
		if err := s.db.SaveDocumentTags(docID, data.Tags); err != nil {
			return nil, fmt.Errorf("save tags: %w", err)
		}
	}

	// Save images (after document exists in DB)
	for _, img := range pendingImages {
		blockID := img.BlockID
		if blockID == "" && img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			blockID = doc.Content.Blocks[img.BlockIndex].ID
		}
		imageID, err := s.db.SaveImage(doc.Info.ID, img.Data, img.Format, img.Width, img.Height, img.Page, img.Name, blockID, img.Description)
		if err != nil {
			fmt.Printf("warning: failed to save image %s: %v\n", img.Name, err)
			continue
		}
		// Update content block reference
		if img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			doc.Content.Blocks[img.BlockIndex].Content = fmt.Sprintf("images/%s", imageID)
		}
	}

	// Index for BM25 search
	sqliteBlocks := toSQLiteBlocks(doc)
	// Add entry metadata to blocks (only for entry blocks, not image blocks)
	entryIdx := 0
	for i := range sqliteBlocks {
		if sqliteBlocks[i].Type == string(BlockTypeCustom) && entryIdx < len(data.Entries) {
			if len(data.Entries[entryIdx].Metadata) > 0 {
				sqliteBlocks[i].EntryMetadata = data.Entries[entryIdx].Metadata
			}
			entryIdx++
		}
	}

	if err := s.db.IndexDocument(doc.Info.ID, sqliteBlocks); err != nil {
		return nil, fmt.Errorf("index document: %w", err)
	}

	// Generate embeddings if provider configured and not deferred
	if !config.DeferEmbedding && s.embedder != nil {
		if err := s.embedDocument(doc); err != nil {
			fmt.Printf("warning: embedding failed: %v\n", err)
		}
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

	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(name))
	var doc *Document

	switch ext {
	case ".pdf":
		doc, err = s.indexPDFFromBytes(data, name, config)
	case ".docx":
		doc, err = s.indexDOCXFromBytes(data, name, config)
	default:
		return nil, fmt.Errorf("unsupported file format: %s", ext)
	}

	if err != nil {
		return nil, err
	}

	// Save to SQLite
	if err := s.db.SaveDocument(toSQLiteDocument(doc)); err != nil {
		return nil, fmt.Errorf("save document: %w", err)
	}

	// Save tags if provided
	if len(config.Tags) > 0 {
		if err := s.db.SaveDocumentTags(doc.Info.ID, config.Tags); err != nil {
			return nil, fmt.Errorf("save tags: %w", err)
		}
	}

	// Index for BM25 search
	blocks := toSQLiteBlocks(doc)
	if err := s.db.IndexDocument(doc.Info.ID, blocks); err != nil {
		return nil, fmt.Errorf("index document: %w", err)
	}

	// Generate embeddings if provider configured and not deferred
	if !config.DeferEmbedding && s.embedder != nil {
		if err := s.embedDocument(doc); err != nil {
			fmt.Printf("warning: embedding failed: %v\n", err)
		}
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
			Source:    sourceOrDefault(config.Source, string(FormatPDF)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
		Content: content,
	}

	return doc, nil
}

// indexDOCXFromBytes indexes a DOCX from bytes
func (s *Store) indexDOCXFromBytes(data []byte, name string, config *indexConfig) (*Document, error) {
	docxDoc, err := docx.OpenBytes(data)
	if err != nil {
		return nil, fmt.Errorf("open DOCX: %w", err)
	}
	defer docxDoc.Close()

	docID := generateID()

	pageCount, _ := docxDoc.PageCount()
	if pageCount == 0 {
		pageCount = 1
	}

	extractor, err := docx.NewSemanticExtractor(docxDoc)
	if err != nil {
		return nil, fmt.Errorf("create extractor: %w", err)
	}

	docxContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertDOCXContent(docxContent)

	doc := &Document{
		Info: DocumentInfo{
			ID:        docID,
			Name:      name,
			SizeBytes: int64(len(data)),
			PageCount: pageCount,
			Format:    FormatDOCX,
			Source:    sourceOrDefault(config.Source, string(FormatDOCX)),
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

	sqliteDoc, err := s.db.GetDocument(id)
	if err != nil {
		return nil, err
	}
	return fromSQLiteDocument(sqliteDoc), nil
}

// DeleteDocument removes a document from the store
func (s *Store) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Delete from HNSW index
	if s.hnsw != nil {
		vectors, _ := s.db.GetVectorsForDocument(id)
		for _, v := range vectors {
			hnswID := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
			s.hnsw.Delete(hnswID)
		}
	}

	// Delete from SQLite (cascades to blocks, vectors, images)
	if err := s.db.DeleteDocument(id); err != nil {
		return err
	}

	// Persist HNSW index - now properly handles errors
	if s.hnsw != nil && s.hnsw.IsDirty() {
		hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
		if err := s.hnsw.SaveToFile(hnswPath); err != nil {
			return fmt.Errorf("save HNSW index: %w", err)
		}
	}

	return nil
}

// ListDocuments returns all indexed documents
func (s *Store) ListDocuments() ([]*DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	infos, err := s.db.ListDocuments()
	if err != nil {
		return nil, err
	}
	return fromSQLiteDocumentInfos(infos), nil
}

// FindByExternalID finds a document by source and external ID
func (s *Store) FindByExternalID(source, externalID string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqliteDoc, err := s.db.FindBySourceAndExternalID(source, externalID)
	if err != nil {
		return nil, err
	}
	if sqliteDoc == nil {
		return nil, nil
	}

	// Get the full document
	fullDoc, err := s.db.GetDocument(sqliteDoc.ID)
	if err != nil {
		return nil, err
	}
	return fromSQLiteDocument(fullDoc), nil
}

// Search performs a search across all documents
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

	// Resolve document filters from sources and tags
	var filterDocIDs []string

	// Filter by sources
	if len(config.Sources) > 0 {
		sourceDocIDs, err := s.db.GetDocumentIDsBySources(config.Sources)
		if err != nil {
			return nil, fmt.Errorf("filter by sources: %w", err)
		}
		filterDocIDs = sourceDocIDs
	}

	// Filter by tags
	if len(config.Tags) > 0 {
		tagDocIDs, err := s.db.GetDocumentIDsByTags(config.Tags)
		if err != nil {
			return nil, fmt.Errorf("filter by tags: %w", err)
		}

		if filterDocIDs != nil {
			// Intersect with source filter
			filterDocIDs = intersectStrings(filterDocIDs, tagDocIDs)
		} else {
			filterDocIDs = tagDocIDs
		}
	}

	// Merge with explicit document filter
	if filterDocIDs != nil {
		if len(config.DocumentIDs) > 0 {
			filterDocIDs = intersectStrings(filterDocIDs, config.DocumentIDs)
		}
		config.DocumentIDs = filterDocIDs
	}

	// Use hybrid searcher
	hybridOpts := &hsearch.HybridSearchOptions{
		Mode:          hsearch.SearchMode(config.SearchMode),
		MaxResults:    config.MaxResults,
		MinScore:      config.MinScore,
		VectorWeight:  config.VectorWeight,
		KeywordWeight: config.KeywordWeight,
		Timeout:       30 * time.Second,
		EfSearch:      config.EfSearch,
	}

	ctx := context.Background()
	hybridResults, err := s.hybrid.Search(ctx, query, hybridOpts)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	// Convert to SearchResults
	results := make([]SearchResult, len(hybridResults.Results))
	for i, r := range hybridResults.Results {
		results[i] = SearchResult{
			DocumentID:   r.DocumentID,
			DocumentName: r.DocumentName,
			BlockID:      r.BlockID,
			Content:      r.Content,
			Score:        r.FusedScore,
			Page:         r.Page,
			Section:      r.Section,
		}

		// Build snippet
		results[i].Snippet = buildSnippet(r.Content, query, config.HighlightPre, config.HighlightPost)

		// Get context if requested
		if config.ContextWindow > 0 {
			ctx, err := s.getContextLocked(r.DocumentID, r.BlockID, config.ContextWindow)
			if err == nil {
				results[i].Context = append(ctx.Before, ctx.Center)
				results[i].Context = append(results[i].Context, ctx.After...)
			}
		}

		// Get images if requested
		if config.IncludeImages && r.Section != "" {
			images, err := s.db.GetImagesBySection(r.DocumentID, r.Section)
			if err == nil && len(images) > 0 {
				imagePaths := make([]string, len(images))
				for j, img := range images {
					imagePaths[j] = fmt.Sprintf("images/%s.%s", img.ID, img.Format)
				}
				results[i].Images = imagePaths
			}
		}

		// Get document metadata if requested
		if config.IncludeMetadata {
			// Get document info (source, external ID)
			if doc, err := s.db.GetDocument(r.DocumentID); err == nil && doc != nil {
				results[i].Source = doc.Info.Source
				results[i].ExternalID = doc.Info.ExternalID
			}

			// Get document tags
			if tags, err := s.db.GetDocumentTags(r.DocumentID); err == nil && len(tags) > 0 {
				results[i].Tags = tags
			}
		}
	}

	// Apply diversification if requested
	diversifiedFrom := 0
	if config.MaxPerDocument > 0 {
		diversifiedFrom = len(results)
		results = diversifyResults(results, config.MaxPerDocument)
	}

	searchResults := &SearchResults{
		Query:      query,
		TotalHits:  len(results),
		Results:    results,
		SearchTime: time.Since(startTime),
	}

	// Add diagnostics if requested
	if config.IncludeDiagnostics {
		searchResults.Diagnostics = &SearchDiagnostics{
			KeywordResults:  hybridResults.KeywordResults,
			VectorResults:   hybridResults.VectorResults,
			KeywordTime:     hybridResults.KeywordTime,
			VectorTime:      hybridResults.VectorTime,
			FusionTime:      hybridResults.SearchTime - hybridResults.KeywordTime - hybridResults.VectorTime,
			FilteredByScore: hybridResults.FilteredByScore,
			DiversifiedFrom: diversifiedFrom,
		}
	}

	return searchResults, nil
}

// diversifyResults limits results per document to improve variety
func diversifyResults(results []SearchResult, maxPerDoc int) []SearchResult {
	if maxPerDoc <= 0 {
		return results
	}

	docCounts := make(map[string]int)
	var diversified []SearchResult

	for _, result := range results {
		count := docCounts[result.DocumentID]
		if count < maxPerDoc {
			diversified = append(diversified, result)
			docCounts[result.DocumentID]++
		}
	}

	return diversified
}

// keywordSearch performs BM25 search (used by hybrid searcher)
func (s *Store) keywordSearch(query string, limit int) ([]hsearch.SearchResult, error) {
	searchOpts := &sqlite.SearchOptions{
		MaxResults: limit,
	}
	results, err := s.db.Search(query, searchOpts)
	if err != nil {
		return nil, err
	}

	out := make([]hsearch.SearchResult, len(results.Results))
	for i, r := range results.Results {
		out[i] = hsearch.SearchResult{
			DocumentID:   r.DocumentID,
			DocumentName: r.DocumentName,
			BlockID:      r.BlockID,
			Content:      r.Content,
			Snippet:      r.Snippet,
			Score:        r.Score,
			Page:         r.Page,
			Section:      r.Section,
		}
	}
	return out, nil
}

// vectorSearch performs semantic vector search (used by hybrid searcher)
// ef parameter overrides HNSW efSearch (0 = use default)
// Falls back to brute-force SQLite search if HNSW is empty/disabled
func (s *Store) vectorSearch(ctx context.Context, query string, limit int, ef int) ([]hsearch.VectorSearchResult, error) {
	if s.embedder == nil {
		return nil, nil
	}

	// Embed query
	queryVector, err := s.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	var out []hsearch.VectorSearchResult

	// Use HNSW if available, otherwise fall back to brute-force SQLite search
	if s.hnsw != nil && s.hnsw.Size() > 0 {
		// Search HNSW with optional ef override
		hnswResults, err := s.hnsw.SearchWithEf(queryVector, limit, ef)
		if err != nil {
			return nil, fmt.Errorf("HNSW search: %w", err)
		}

		out = make([]hsearch.VectorSearchResult, 0, len(hnswResults))
		for _, r := range hnswResults {
			parts := strings.SplitN(r.ID, ":", 2)
			if len(parts) != 2 {
				continue
			}
			out = append(out, hsearch.VectorSearchResult{
				DocumentID: parts[0],
				BlockID:    parts[1],
				Score:      r.Score,
				Distance:   r.Distance,
			})
		}
	} else {
		// Brute-force SQLite search (slower but no memory overhead)
		sqliteResults, err := s.db.BruteForceSearch(queryVector, limit, nil)
		if err != nil {
			return nil, fmt.Errorf("brute-force search: %w", err)
		}

		out = make([]hsearch.VectorSearchResult, 0, len(sqliteResults))
		for _, r := range sqliteResults {
			out = append(out, hsearch.VectorSearchResult{
				DocumentID: r.DocumentID,
				BlockID:    r.BlockID,
				Score:      r.Score,
				Distance:   r.Distance,
			})
		}
	}

	return out, nil
}

// getBlockContent retrieves content for a block (used by hybrid searcher)
func (s *Store) getBlockContent(docID, blockID string) (content, snippet string, page int, section string, err error) {
	// Strip chunk suffix if present (e.g., "block123#0" -> "block123")
	origBlockID := blockID
	if idx := strings.Index(blockID, "#"); idx >= 0 {
		blockID = blockID[:idx]
	}

	block, err := s.db.GetBlockByID(docID, blockID)
	if err != nil {
		return "", "", 0, "", fmt.Errorf("get block %s (orig: %s): %w", blockID, origBlockID, err)
	}
	return block.Content, "", block.Page, block.Section, nil
}

// getDocumentName retrieves document name (used by hybrid searcher)
func (s *Store) getDocumentName(docID string) string {
	doc, err := s.db.GetDocument(docID)
	if err != nil {
		return ""
	}
	return doc.Info.Name
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
	// Strip chunk suffix if present (e.g., "block123#0" -> "block123")
	if idx := strings.Index(blockID, "#"); idx >= 0 {
		blockID = blockID[:idx]
	}

	before, center, after, err := s.db.GetContextBlocks(docID, blockID, windowSize)
	if err != nil {
		return nil, err
	}

	var centerBlock ContentBlock
	if len(center) > 0 {
		centerBlock = fromSQLiteBlock(&center[0])
	}

	result := &ContextResult{
		DocumentID: docID,
		CenterID:   blockID,
		Center:     centerBlock,
		Before:     make([]ContentBlock, len(before)),
		After:      make([]ContentBlock, len(after)),
	}

	for i := range before {
		result.Before[i] = fromSQLiteBlock(&before[i])
	}
	for i := range after {
		result.After[i] = fromSQLiteBlock(&after[i])
	}

	return result, nil
}

// GetLastImportTime returns the most recent import timestamp for a given source
// Returns zero time if no imports found for the source
func (s *Store) GetLastImportTime(source string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.db.GetLastImportTime(source)
}

// GetImagesByDocumentFiltered returns images for a document with optional section/page filters
func (s *Store) GetImagesByDocumentFiltered(docID, section string, page int) ([]ImageInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var images []sqlite.ImageInfo
	var err error

	if section != "" {
		images, err = s.db.GetImagesBySection(docID, section)
	} else if page > 0 {
		images, err = s.db.GetImagesByPage(docID, page)
	} else {
		images, err = s.db.GetImagesForDocument(docID)
	}

	if err != nil {
		return nil, err
	}

	result := make([]ImageInfo, len(images))
	for i, img := range images {
		result[i] = ImageInfo{
			ID:           img.ID,
			DocumentID:   img.DocumentID,
			BlockID:      img.BlockID,
			Format:       img.Format,
			Width:        img.Width,
			Height:       img.Height,
			Page:         img.Page,
			OriginalName: img.OriginalName,
		}
	}

	return result, nil
}

// GetEmbeddingStatus returns the embedding status for a document
func (s *Store) GetEmbeddingStatus(docID string) (*EmbeddingStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get embeddable block count
	embeddable, err := s.db.GetEmbeddableBlockCountForDocument(docID)
	if err != nil {
		return nil, fmt.Errorf("get embeddable count: %w", err)
	}

	// Get vector info
	vectorInfo, err := s.db.GetVectorInfo(docID)
	if err != nil {
		return nil, fmt.Errorf("get vector info: %w", err)
	}

	status := &EmbeddingStatus{
		HasEmbeddings:   vectorInfo.Count > 0,
		IsComplete:      vectorInfo.Count >= embeddable,
		EmbeddedCount:   vectorInfo.Count,
		TotalEmbeddable: embeddable,
		Model:           vectorInfo.Model,
		Dimension:       vectorInfo.Dimension,
		LastUpdated:     vectorInfo.LastUpdated,
	}

	return status, nil
}

// HasEmbeddings is a convenience method that returns true if a document has any embeddings
func (s *Store) HasEmbeddings(docID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count, err := s.db.GetVectorCountForDocument(docID)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DetectQueryType analyzes a query string and returns its detected intent type
func (s *Store) DetectQueryType(query string) QueryType {
	return DetectQueryType(query)
}

// CheckDuplicate checks if a document at the given path is a duplicate of an existing document.
// It uses file checksum for comparison.
func (s *Store) CheckDuplicate(path string) (*DedupResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Compute checksum
	checksum, err := computeChecksum(path)
	if err != nil {
		return nil, fmt.Errorf("compute checksum: %w", err)
	}

	// Check for duplicate by checksum
	dedupInfo, err := s.db.CheckDuplicateByChecksum(checksum)
	if err != nil {
		return nil, fmt.Errorf("check duplicate: %w", err)
	}

	return &DedupResult{
		IsDuplicate:  dedupInfo.IsDuplicate,
		ExistingID:   dedupInfo.ExistingID,
		ExistingName: dedupInfo.ExistingName,
		Similarity:   1.0, // Exact match if duplicate
		Method:       dedupInfo.Method,
	}, nil
}

// CheckDuplicateByContent checks if content already exists in the store.
// It computes a content hash from the provided data and checks for matches.
func (s *Store) CheckDuplicateByContent(data []byte) (*DedupResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Compute content hash
	hash := sha256.Sum256(data)
	contentHash := fmt.Sprintf("sha256:%x", hash)

	// Check for duplicate by content hash
	dedupInfo, err := s.db.CheckDuplicateByContentHash(contentHash)
	if err != nil {
		return nil, fmt.Errorf("check duplicate: %w", err)
	}

	return &DedupResult{
		IsDuplicate:  dedupInfo.IsDuplicate,
		ExistingID:   dedupInfo.ExistingID,
		ExistingName: dedupInfo.ExistingName,
		Similarity:   1.0, // Exact match if duplicate
		Method:       dedupInfo.Method,
	}, nil
}

// IndexDocumentWithProgress indexes a document with progress callbacks
func (s *Store) IndexDocumentWithProgress(path string, callback ProgressCallback, opts ...IndexOption) (*Document, error) {
	if callback == nil {
		// Fall back to regular indexing if no callback
		return s.IndexDocument(path, opts...)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config := defaultIndexConfig()
	config.SourcePath = path
	config.ProgressCallback = callback

	for _, opt := range opts {
		opt(config)
	}

	startTime := time.Now()
	name := filepath.Base(path)
	if config.Name != "" {
		name = config.Name
	}

	// Initial progress
	progress := IndexProgress{
		DocumentName: name,
		Status:       "parsing",
		StartTime:    startTime,
	}
	callback(progress)

	// Detect format
	ext := strings.ToLower(filepath.Ext(path))
	var doc *Document
	var pendingImages []pendingImage
	var err error

	switch ext {
	case ".pdf":
		doc, pendingImages, err = s.indexPDFWithProgress(path, config, callback, startTime)
	case ".docx":
		doc, pendingImages, err = s.indexDOCXWithProgress(path, config, callback, startTime)
	default:
		progress.Status = "error"
		progress.Error = fmt.Errorf("unsupported file format: %s", ext)
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)
		return nil, progress.Error
	}

	if err != nil {
		progress.Status = "error"
		progress.Error = err
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)
		return nil, err
	}

	// Indexing phase
	progress = IndexProgress{
		DocumentID:   doc.Info.ID,
		DocumentName: name,
		Status:       "indexing",
		TotalPages:   doc.Info.PageCount,
		TotalBlocks:  len(doc.Content.Blocks),
		StartTime:    startTime,
		ElapsedTime:  time.Since(startTime),
	}
	callback(progress)

	// Save to SQLite
	if err := s.db.SaveDocument(toSQLiteDocument(doc)); err != nil {
		progress.Status = "error"
		progress.Error = fmt.Errorf("save document: %w", err)
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)
		return nil, progress.Error
	}

	// Save images
	for _, img := range pendingImages {
		blockID := img.BlockID
		if blockID == "" && img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			blockID = doc.Content.Blocks[img.BlockIndex].ID
		}
		imageID, err := s.db.SaveImage(doc.Info.ID, img.Data, img.Format, img.Width, img.Height, img.Page, img.Name, blockID, img.Description)
		if err != nil {
			continue
		}
		if img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
			doc.Content.Blocks[img.BlockIndex].Content = fmt.Sprintf("images/%s", imageID)
		}
	}

	// Index for BM25 search
	blocks := toSQLiteBlocks(doc)
	if err := s.db.IndexDocument(doc.Info.ID, blocks); err != nil {
		progress.Status = "error"
		progress.Error = fmt.Errorf("index document: %w", err)
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)
		return nil, progress.Error
	}

	// Embedding phase (unless deferred)
	if !config.DeferEmbedding && s.embedder != nil {
		progress.Status = "embedding"
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)

		if err := s.embedDocumentWithProgress(doc, callback, startTime); err != nil {
			// Log but don't fail
			fmt.Printf("warning: embedding failed: %v\n", err)
		}
	}

	// Complete
	progress = IndexProgress{
		DocumentID:      doc.Info.ID,
		DocumentName:    name,
		Status:          "complete",
		TotalPages:      doc.Info.PageCount,
		ProcessedPages:  doc.Info.PageCount,
		TotalBlocks:     len(doc.Content.Blocks),
		ProcessedBlocks: len(doc.Content.Blocks),
		StartTime:       startTime,
		ElapsedTime:     time.Since(startTime),
	}
	callback(progress)

	return doc, nil
}

// indexPDFWithProgress indexes a PDF with progress callbacks
func (s *Store) indexPDFWithProgress(path string, config *indexConfig, callback ProgressCallback, startTime time.Time) (*Document, []pendingImage, error) {
	pdfDoc, err := pdf.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open PDF: %w", err)
	}
	defer pdfDoc.Close()

	if pdfDoc.IsEncrypted() {
		return nil, nil, ErrEncryptedPDF
	}

	docID := generateID()
	pageCount, _ := pdfDoc.PageCount()

	// Progress: extracting
	progress := IndexProgress{
		DocumentID:   docID,
		DocumentName: filepath.Base(path),
		Status:       "extracting",
		TotalPages:   pageCount,
		StartTime:    startTime,
		ElapsedTime:  time.Since(startTime),
	}
	callback(progress)

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}

	var checksum string
	if s.config.ComputeChecksum {
		checksum, _ = computeChecksum(path)
	}

	extractor := pdf.NewSemanticExtractor(pdfDoc)
	pdfContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertPDFContent(pdfContent)

	// Update progress with block count
	progress.TotalBlocks = len(content.Blocks)
	progress.ProcessedPages = pageCount
	progress.ElapsedTime = time.Since(startTime)
	callback(progress)

	// Collect images
	var pending []pendingImage
	if s.config.ExtractImages {
		imageExtractor := pdf.NewImageExtractor(pdfDoc)
		for pageNum := 1; pageNum <= pageCount; pageNum++ {
			images, err := imageExtractor.ExtractPageImages(pageNum)
			if err != nil {
				continue
			}

			for _, img := range images {
				blockIndex := -1
				for j := range content.Blocks {
					if content.Blocks[j].Type == BlockTypeImage &&
						content.Blocks[j].Content == img.Name &&
						content.Blocks[j].Page == pageNum {
						blockIndex = j
						break
					}
				}

				pending = append(pending, pendingImage{
					Data:       img.Data,
					Format:     img.Format,
					Width:      img.Width,
					Height:     img.Height,
					Page:       pageNum,
					Name:       img.Name,
					BlockIndex: blockIndex,
				})
			}
		}
	}

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

	return doc, pending, nil
}

// indexDOCXWithProgress indexes a DOCX with progress callbacks
func (s *Store) indexDOCXWithProgress(path string, config *indexConfig, callback ProgressCallback, startTime time.Time) (*Document, []pendingImage, error) {
	docxDoc, err := docx.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open DOCX: %w", err)
	}
	defer docxDoc.Close()

	docID := generateID()
	pageCount, _ := docxDoc.PageCount()
	if pageCount == 0 {
		pageCount = 1
	}

	// Progress: extracting
	progress := IndexProgress{
		DocumentID:   docID,
		DocumentName: filepath.Base(path),
		Status:       "extracting",
		TotalPages:   pageCount,
		StartTime:    startTime,
		ElapsedTime:  time.Since(startTime),
	}
	callback(progress)

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("stat file: %w", err)
	}

	var checksum string
	if s.config.ComputeChecksum {
		checksum, _ = computeChecksum(path)
	}

	extractor, err := docx.NewSemanticExtractor(docxDoc)
	if err != nil {
		return nil, nil, fmt.Errorf("create extractor: %w", err)
	}

	docxContent, err := extractor.ExtractContent()
	if err != nil {
		return nil, nil, fmt.Errorf("extract content: %w", err)
	}

	content := convertDOCXContent(docxContent)

	// Update progress with block count
	progress.TotalBlocks = len(content.Blocks)
	progress.ProcessedPages = pageCount
	progress.ElapsedTime = time.Since(startTime)
	callback(progress)

	// Collect images
	var pending []pendingImage
	if s.config.ExtractImages {
		imageExtractor, err := docx.NewImageExtractor(docxDoc)
		if err == nil {
			images, err := imageExtractor.ExtractAllImages()
			if err == nil {
				for _, img := range images {
					blockIndex := -1
					for j := range content.Blocks {
						if content.Blocks[j].Type == BlockTypeImage &&
							content.Blocks[j].Content == img.Name {
							blockIndex = j
							break
						}
					}

					pending = append(pending, pendingImage{
						Data:       img.Data,
						Format:     img.Format,
						Width:      0,
						Height:     0,
						Page:       0,
						Name:       img.Name,
						BlockIndex: blockIndex,
					})
				}
			}
		}
	}

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
			Format:       FormatDOCX,
			Checksum:     checksum,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		},
		Content: content,
	}

	return doc, pending, nil
}

// embedDocumentWithProgress generates embeddings with progress callbacks
func (s *Store) embedDocumentWithProgress(doc *Document, callback ProgressCallback, startTime time.Time) error {
	ctx := context.Background()

	// Chunking config with defaults
	maxChars := s.config.MaxChunkChars
	if maxChars <= 0 {
		maxChars = 48000 // Default for models with higher token limits
	}
	overlap := s.config.ChunkOverlap
	if overlap <= 0 {
		overlap = 200
	}

	// Collect text blocks with chunking for long content
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if len(block.Content) > 0 {
				chunks := chunkText(block.Content, maxChars, overlap)
				for i, chunk := range chunks {
					texts = append(texts, chunk)
					// Use block ID with chunk suffix for multi-chunk blocks
					if len(chunks) > 1 {
						blockIDs = append(blockIDs, fmt.Sprintf("%s#%d", block.ID, i))
					} else {
						blockIDs = append(blockIDs, block.ID)
					}
				}
			}
		}
	}

	if len(texts) == 0 {
		return nil
	}

	progress := IndexProgress{
		DocumentID:   doc.Info.ID,
		DocumentName: doc.Info.Name,
		Status:       "embedding",
		TotalBlocks:  len(texts),
		StartTime:    startTime,
	}

	// Generate embeddings in batches with truncation fallback
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, usedTexts, err := s.embedWithTruncationFallback(ctx, texts[i:end], doc.Info.ID)
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       usedTexts[j],
				Model:      s.embedder.Name(),
			})
		}

		// Update progress
		progress.ProcessedBlocks = len(vectors)
		progress.ElapsedTime = time.Since(startTime)
		callback(progress)
	}

	// Save vectors to SQLite
	if err := s.db.SaveVectors(doc.Info.ID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

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

	// Persist HNSW index
	hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
	if err := s.hnsw.SaveToFile(hnswPath); err != nil {
		return fmt.Errorf("save HNSW index: %w", err)
	}

	return nil
}

// SearchForAgent performs a search optimized for AI agent consumption.
// Returns structured output with token estimates, citation references, and chunked results.
func (s *Store) SearchForAgent(query string, opts ...SearchOption) (*AgentSearchResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startTime := time.Now()

	config := defaultSearchConfig()
	config.AgentOutput = true
	config.EstimateTokens = true
	config.IncludeCitations = true

	for _, opt := range opts {
		opt(config)
	}

	if query == "" {
		return nil, ErrInvalidQuery
	}

	// Detect query type
	queryType := DetectQueryType(query)

	// Resolve document filters from sources and tags
	var filterDocIDs []string

	if len(config.Sources) > 0 {
		sourceDocIDs, err := s.db.GetDocumentIDsBySources(config.Sources)
		if err != nil {
			return nil, fmt.Errorf("filter by sources: %w", err)
		}
		filterDocIDs = sourceDocIDs
	}

	if len(config.Tags) > 0 {
		tagDocIDs, err := s.db.GetDocumentIDsByTags(config.Tags)
		if err != nil {
			return nil, fmt.Errorf("filter by tags: %w", err)
		}

		if filterDocIDs != nil {
			filterDocIDs = intersectStrings(filterDocIDs, tagDocIDs)
		} else {
			filterDocIDs = tagDocIDs
		}
	}

	if filterDocIDs != nil {
		if len(config.DocumentIDs) > 0 {
			filterDocIDs = intersectStrings(filterDocIDs, config.DocumentIDs)
		}
		config.DocumentIDs = filterDocIDs
	}

	// Use hybrid searcher
	hybridOpts := &hsearch.HybridSearchOptions{
		Mode:          hsearch.SearchMode(config.SearchMode),
		MaxResults:    config.MaxResults,
		MinScore:      config.MinScore,
		VectorWeight:  config.VectorWeight,
		KeywordWeight: config.KeywordWeight,
		Timeout:       30 * time.Second,
		EfSearch:      config.EfSearch,
	}

	ctx := context.Background()
	hybridResults, err := s.hybrid.Search(ctx, query, hybridOpts)
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	// Convert to AgentSearchResults with citations
	results := make([]AgentSearchResult, len(hybridResults.Results))
	totalTokens := 0

	for i, r := range hybridResults.Results {
		tokenCount := EstimateTokens(r.Content)
		totalTokens += tokenCount

		results[i] = AgentSearchResult{
			DocumentID:   r.DocumentID,
			DocumentName: r.DocumentName,
			BlockID:      r.BlockID,
			Content:      r.Content,
			Snippet:      buildSnippet(r.Content, query, config.HighlightPre, config.HighlightPost),
			Score:        r.FusedScore,
			Page:         r.Page,
			Section:      r.Section,
			CitationRef:  fmt.Sprintf("[%d]", i+1),
			TokenCount:   tokenCount,
		}

		// Get context if requested
		if config.ContextWindow > 0 {
			ctx, err := s.getContextLocked(r.DocumentID, r.BlockID, config.ContextWindow)
			if err == nil {
				results[i].Context = append(ctx.Before, ctx.Center)
				results[i].Context = append(results[i].Context, ctx.After...)

				// Update token count with context
				for _, cb := range results[i].Context {
					results[i].TokenCount += EstimateTokens(cb.Content)
				}
				totalTokens += results[i].TokenCount - tokenCount
			}
		}

		// Get images if requested
		if config.IncludeImages && r.Section != "" {
			images, err := s.db.GetImagesBySection(r.DocumentID, r.Section)
			if err == nil && len(images) > 0 {
				imagePaths := make([]string, len(images))
				for j, img := range images {
					imagePaths[j] = fmt.Sprintf("images/%s.%s", img.ID, img.Format)
				}
				results[i].Images = imagePaths
			}
		}
	}

	return &AgentSearchResponse{
		Query:           query,
		QueryType:       queryType,
		Results:         results,
		TotalHits:       len(results),
		SearchTime:      time.Since(startTime),
		EstimatedTokens: totalTokens,
	}, nil
}

// Close releases resources held by the store
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Save HNSW index
	if s.hnsw != nil {
		hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
		s.hnsw.SaveToFile(hnswPath)
	}

	return s.db.Close()
}

// Stats returns statistics about the store
func (s *Store) Stats() StoreStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	docCount, _ := s.db.GetDocumentCount()
	blockCount, _ := s.db.GetBlockCount()
	imageCount, _ := s.db.GetImageCount()
	termCount, _ := s.db.GetIndexTermCount()
	vectorCount, _ := s.db.GetVectorCount()

	return StoreStats{
		DocumentCount: docCount,
		TotalBlocks:   blockCount,
		TotalImages:   imageCount,
		IndexTerms:    termCount,
		VectorCount:   vectorCount,
	}
}

// StoreStats contains statistics about the store
type StoreStats struct {
	DocumentCount int   `json:"document_count"`
	TotalBlocks   int   `json:"total_blocks"`
	TotalImages   int   `json:"total_images"`
	IndexTerms    int   `json:"index_terms"`
	VectorCount   int   `json:"vector_count"`
	StorageBytes  int64 `json:"storage_bytes"`
}

// SourceStats is an alias to sqlite.SourceStats for API compatibility
type SourceStats = sqlite.SourceStats

// SourceStatsDocIssue is an alias to sqlite.SourceStatsDocIssue for API compatibility
type SourceStatsDocIssue = sqlite.SourceStatsDocIssue

// GetSourceStats returns statistics for a specific source (e.g., "bugtrack", "knowledgebase", "freshdesk")
func (s *Store) GetSourceStats(source string) (*SourceStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.db.GetSourceStats(source)
}

// DatabaseInfo returns information about the database schema and version
func (s *Store) DatabaseInfo() (*DatabaseInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	info, err := s.db.GetDatabaseInfo()
	if err != nil {
		return nil, err
	}

	return &DatabaseInfo{
		SchemaVersion:  info.SchemaVersion,
		LibraryVersion: info.LibraryVersion,
		CreatedAt:      info.CreatedAt,
		LastMigration:  info.LastMigration,
	}, nil
}

// Helper functions

func generateID() string {
	return uuid.New().String()
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
	queryLower := strings.ToLower(query)
	contentLower := strings.ToLower(content)

	idx := strings.Index(contentLower, queryLower)
	if idx < 0 {
		if len(content) > 200 {
			return content[:200] + "..."
		}
		return content
	}

	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + 100
	if end > len(content) {
		end = len(content)
	}

	snippet := content[start:end]

	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(content) {
		snippet = snippet + "..."
	}

	if highlightPre != "" && highlightPost != "" {
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

// convertDOCXContent converts docx.DocumentContent to docuindex.DocumentContent
func convertDOCXContent(docxContent *docx.DocumentContent) DocumentContent {
	content := DocumentContent{
		Version: docxContent.Version,
		Blocks:  make([]ContentBlock, len(docxContent.Blocks)),
	}

	for i, block := range docxContent.Blocks {
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

// toSQLiteDocument converts docuindex.Document to sqlite.Document
func toSQLiteDocument(doc *Document) *sqlite.Document {
	sqliteDoc := &sqlite.Document{
		Info: sqlite.DocumentInfo{
			ID:           doc.Info.ID,
			Name:         doc.Info.Name,
			OriginalPath: doc.Info.OriginalPath,
			SizeBytes:    doc.Info.SizeBytes,
			PageCount:    doc.Info.PageCount,
			Format:       string(doc.Info.Format),
			Checksum:     doc.Info.Checksum,
			CreatedAt:    doc.Info.CreatedAt,
			UpdatedAt:    doc.Info.UpdatedAt,
			Source:       doc.Info.Source,
			Description:  doc.Info.Description,
			ImportedAt:   doc.Info.ImportedAt,
			ExternalID:   doc.Info.ExternalID,
		},
		Blocks: make([]sqlite.ContentBlock, len(doc.Content.Blocks)),
	}

	for i, block := range doc.Content.Blocks {
		sqliteDoc.Blocks[i] = sqlite.ContentBlock{
			ID:           block.ID,
			Type:         string(block.Type),
			Content:      block.Content,
			Page:         block.Page,
			Sequence:     i,
			BBoxX:        block.BBox.X,
			BBoxY:        block.BBox.Y,
			BBoxWidth:    block.BBox.Width,
			BBoxHeight:   block.BBox.Height,
			PageWidth:    block.BBox.PageWidth,
			PageHeight:   block.BBox.PageHeight,
			IsHeading:    block.Semantic.IsHeading,
			HeadingLevel: block.Semantic.HeadingLevel,
			Section:      block.Semantic.Section,
			Keywords:     block.Semantic.Keywords,
			Context:      block.Semantic.Context,
			Children:     block.Children,
		}
		if block.Font != nil {
			sqliteDoc.Blocks[i].FontName = block.Font.Name
			sqliteDoc.Blocks[i].FontSize = block.Font.Size
			sqliteDoc.Blocks[i].FontBold = block.Font.Bold
			sqliteDoc.Blocks[i].FontItalic = block.Font.Italic
		}
	}

	return sqliteDoc
}

// toSQLiteBlocks converts document blocks to sqlite.ContentBlock slice
func toSQLiteBlocks(doc *Document) []sqlite.ContentBlock {
	blocks := make([]sqlite.ContentBlock, len(doc.Content.Blocks))
	for i, block := range doc.Content.Blocks {
		blocks[i] = sqlite.ContentBlock{
			ID:           block.ID,
			Type:         string(block.Type),
			Content:      block.Content,
			Page:         block.Page,
			Sequence:     i,
			BBoxX:        block.BBox.X,
			BBoxY:        block.BBox.Y,
			BBoxWidth:    block.BBox.Width,
			BBoxHeight:   block.BBox.Height,
			PageWidth:    block.BBox.PageWidth,
			PageHeight:   block.BBox.PageHeight,
			IsHeading:    block.Semantic.IsHeading,
			HeadingLevel: block.Semantic.HeadingLevel,
			Section:      block.Semantic.Section,
			Keywords:     block.Semantic.Keywords,
			Context:      block.Semantic.Context,
			Children:     block.Children,
		}
		if block.Font != nil {
			blocks[i].FontName = block.Font.Name
			blocks[i].FontSize = block.Font.Size
			blocks[i].FontBold = block.Font.Bold
			blocks[i].FontItalic = block.Font.Italic
		}
	}
	return blocks
}

// fromSQLiteDocument converts sqlite.Document to docuindex.Document
func fromSQLiteDocument(sqliteDoc *sqlite.Document) *Document {
	doc := &Document{
		Info: DocumentInfo{
			ID:           sqliteDoc.Info.ID,
			Name:         sqliteDoc.Info.Name,
			OriginalPath: sqliteDoc.Info.OriginalPath,
			SizeBytes:    sqliteDoc.Info.SizeBytes,
			PageCount:    sqliteDoc.Info.PageCount,
			Format:       DocumentFormat(sqliteDoc.Info.Format),
			Checksum:     sqliteDoc.Info.Checksum,
			CreatedAt:    sqliteDoc.Info.CreatedAt,
			UpdatedAt:    sqliteDoc.Info.UpdatedAt,
			Source:       sqliteDoc.Info.Source,
			Description:  sqliteDoc.Info.Description,
			ImportedAt:   sqliteDoc.Info.ImportedAt,
			ExternalID:   sqliteDoc.Info.ExternalID,
		},
		Content: DocumentContent{
			Version: "1.0",
			Blocks:  make([]ContentBlock, len(sqliteDoc.Blocks)),
		},
	}

	for i := range sqliteDoc.Blocks {
		doc.Content.Blocks[i] = fromSQLiteBlock(&sqliteDoc.Blocks[i])
	}

	return doc
}

// fromSQLiteBlock converts sqlite.ContentBlock to docuindex.ContentBlock
func fromSQLiteBlock(block *sqlite.ContentBlock) ContentBlock {
	cb := ContentBlock{
		ID:      block.ID,
		Type:    BlockType(block.Type),
		Content: block.Content,
		Page:    block.Page,
		BBox: BoundingBox{
			X:          block.BBoxX,
			Y:          block.BBoxY,
			Width:      block.BBoxWidth,
			Height:     block.BBoxHeight,
			PageWidth:  block.PageWidth,
			PageHeight: block.PageHeight,
		},
		Semantic: SemanticInfo{
			IsHeading:    block.IsHeading,
			HeadingLevel: block.HeadingLevel,
			Section:      block.Section,
			Keywords:     block.Keywords,
			Context:      block.Context,
		},
		Children: block.Children,
	}

	if block.FontName != "" {
		cb.Font = &FontInfo{
			Name:   block.FontName,
			Size:   block.FontSize,
			Bold:   block.FontBold,
			Italic: block.FontItalic,
		}
	}

	return cb
}

// fromSQLiteBlocks converts []sqlite.ContentBlock to []ContentBlock
func fromSQLiteBlocks(blocks []sqlite.ContentBlock) []ContentBlock {
	result := make([]ContentBlock, len(blocks))
	for i := range blocks {
		result[i] = fromSQLiteBlock(&blocks[i])
	}
	return result
}

// fromSQLiteDocumentInfos converts []sqlite.DocumentInfo to []*DocumentInfo
func fromSQLiteDocumentInfos(infos []sqlite.DocumentInfo) []*DocumentInfo {
	out := make([]*DocumentInfo, len(infos))
	for i := range infos {
		out[i] = &DocumentInfo{
			ID:           infos[i].ID,
			Name:         infos[i].Name,
			OriginalPath: infos[i].OriginalPath,
			SizeBytes:    infos[i].SizeBytes,
			PageCount:    infos[i].PageCount,
			Format:       DocumentFormat(infos[i].Format),
			Checksum:     infos[i].Checksum,
			CreatedAt:    infos[i].CreatedAt,
			UpdatedAt:    infos[i].UpdatedAt,
			Source:       infos[i].Source,
			Description:  infos[i].Description,
			ImportedAt:   infos[i].ImportedAt,
			ExternalID:   infos[i].ExternalID,
		}
	}
	return out
}

// intersectStrings returns the intersection of two string slices
func intersectStrings(a, b []string) []string {
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

// GetDocumentsWithoutEmbeddings returns documents that have embeddable content
// but don't have any embeddings yet. Use this for resumable maintenance tasks.
func (s *Store) GetDocumentsWithoutEmbeddings() ([]*DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqliteInfos, err := s.db.GetDocumentsWithoutEmbeddings()
	if err != nil {
		return nil, err
	}

	return fromSQLiteDocumentInfos(sqliteInfos), nil
}

// GetPendingDocumentCount returns the count of documents without embeddings.
// This is memory-efficient - use for status displays instead of GetDocumentsWithoutEmbeddings.
func (s *Store) GetPendingDocumentCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.db.GetPendingDocumentCount()
}

// EmbedDocuments generates embeddings for specific documents by ID.
// This is useful for resumable batch processing.
// OPTIMIZED: Saves HNSW index only once at the end instead of after each document.
func (s *Store) EmbedDocuments(docIDs ...string) error {
	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	if len(docIDs) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, docID := range docIDs {
		doc, err := s.db.GetDocument(docID)
		if err != nil {
			return fmt.Errorf("get document %s: %w", docID, err)
		}

		// Convert to Document for embedDocument
		mainDoc := &Document{
			Info: DocumentInfo{
				ID:   doc.Info.ID,
				Name: doc.Info.Name,
			},
			Content: DocumentContent{
				Blocks: fromSQLiteBlocks(doc.Blocks),
			},
		}

		if err := s.embedDocumentUnlocked(mainDoc); err != nil {
			return fmt.Errorf("embed document %s: %w", docID, err)
		}
	}

	// Save HNSW index ONCE after all documents processed
	return s.saveHNSWIndex()
}

// EmbedPendingDocuments generates embeddings for all documents that don't have them yet.
// This is the main method for deferred embedding patterns.
// OPTIMIZED: Saves HNSW index only once at the end instead of after each document.
func (s *Store) EmbedPendingDocuments() error {
	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	// Get pending documents (no lock needed for read)
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
	for _, info := range pending {
		s.mu.Lock()
		doc, err := s.db.GetDocument(info.ID)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("get document %s: %w", info.ID, err)
		}

		// Convert to Document for embedDocument
		mainDoc := &Document{
			Info: DocumentInfo{
				ID:   doc.Info.ID,
				Name: doc.Info.Name,
			},
			Content: DocumentContent{
				Blocks: fromSQLiteBlocks(doc.Blocks),
			},
		}

		if err := s.embedDocumentUnlocked(mainDoc); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("embed document %s: %w", info.ID, err)
		}
		s.mu.Unlock()
	}

	// Save HNSW index ONCE after all documents processed
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveHNSWIndex()
}

// EmbedDocumentsStreaming processes pending documents one at a time with explicit memory management.
// This is designed for large document sets (10K+) to avoid OOM.
// Parameters:
//   - batchSize: Number of document IDs to fetch per database query (recommended: 100)
//   - onProgress: Optional callback called after each document (completed, total int)
//
// The function:
//   - Fetches document IDs in small batches (memory efficient)
//   - Processes one document at a time
//   - Saves HNSW index after each batch
func (s *Store) EmbedDocumentsStreaming(batchSize int, onProgress func(completed, total int)) error {
	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	if batchSize <= 0 {
		batchSize = 100
	}

	// Get total count upfront for progress reporting
	s.mu.RLock()
	totalPending, err := s.db.GetPendingDocumentCount()
	s.mu.RUnlock()
	if err != nil {
		log.Printf("warning: could not get pending document count: %v", err)
		totalPending = -1 // Fall back to unknown
	}

	totalEmbedded := 0
	batchNum := 0

	for {
		batchNum++

		// Get a batch of pending document IDs only (memory efficient)
		s.mu.RLock()
		docIDs, err := s.db.GetPendingDocumentIDsLimited(batchSize)
		s.mu.RUnlock()
		if err != nil {
			return fmt.Errorf("get pending document IDs: %w", err)
		}

		if len(docIDs) == 0 {
			break // No more pending documents
		}

		// Process each document one at a time
		for _, docID := range docIDs {
			s.mu.Lock()

			doc, err := s.db.GetDocument(docID)
			if err != nil {
				s.mu.Unlock()
				log.Printf("EmbedDocumentsStreaming: error getting document %s: %v", docID, err)
				continue // Skip failed documents
			}

			// Convert to Document for embedDocument
			mainDoc := &Document{
				Info: DocumentInfo{
					ID:   doc.Info.ID,
					Name: doc.Info.Name,
				},
				Content: DocumentContent{
					Blocks: fromSQLiteBlocks(doc.Blocks),
				},
			}

			if err := s.embedDocumentUnlocked(mainDoc); err != nil {
				s.mu.Unlock()
				log.Printf("EmbedDocumentsStreaming: error embedding document %s: %v", docID, err)
				continue // Skip failed documents
			}

			s.mu.Unlock()
			totalEmbedded++

			// Call progress callback if provided
			if onProgress != nil {
				onProgress(totalEmbedded, totalPending)
			}

			// Throttle to avoid rate limits (~2 docs/sec for 120K TPM quota)
			time.Sleep(500 * time.Millisecond)
		}

		// Save HNSW index after each batch
		s.mu.Lock()
		if err := s.saveHNSWIndex(); err != nil {
			log.Printf("EmbedDocumentsStreaming: error saving HNSW index: %v", err)
		}
		s.mu.Unlock()
	}

	log.Printf("EmbedDocumentsStreaming: completed %d documents in %d batches", totalEmbedded, batchNum)
	return nil
}

// EmbedPendingDocumentsAsync starts embedding in background.
// Returns immediately. Use GetBackgroundStatus() to check progress,
// IsBackgroundRunning() to check if still running, or WaitForBackground() to block.
func (s *Store) EmbedPendingDocumentsAsync() error {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()

	if s.bgRunning {
		return fmt.Errorf("background embedding already running")
	}

	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	// Get pending documents count
	s.mu.RLock()
	pending, err := s.db.GetDocumentsWithoutEmbeddings()
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("get pending documents: %w", err)
	}

	if len(pending) == 0 {
		return nil // Nothing to do
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.bgCancel = cancel
	s.bgRunning = true
	s.bgDone = make(chan struct{})
	s.bgStatus = BackgroundEmbeddingStatus{
		Running:        true,
		StartedAt:      time.Now(),
		DocumentsTotal: len(pending),
	}

	go s.runBackgroundEmbedding(ctx, pending)

	return nil
}

// runBackgroundEmbedding is the background goroutine for embedding
func (s *Store) runBackgroundEmbedding(ctx context.Context, pending []sqlite.DocumentInfo) {
	defer func() {
		s.bgMu.Lock()
		s.bgRunning = false
		s.bgStatus.Running = false
		s.bgStatus.ElapsedTime = time.Since(s.bgStatus.StartedAt)
		close(s.bgDone)
		s.bgMu.Unlock()
	}()

	for i, info := range pending {
		select {
		case <-ctx.Done():
			s.bgMu.Lock()
			s.bgStatus.Error = ctx.Err()
			s.bgMu.Unlock()
			return
		default:
		}

		// Update status
		s.bgMu.Lock()
		s.bgStatus.CurrentDocID = info.ID
		s.bgStatus.CurrentDocName = info.Name
		s.bgStatus.ElapsedTime = time.Since(s.bgStatus.StartedAt)
		s.bgMu.Unlock()

		s.mu.Lock()
		doc, err := s.db.GetDocument(info.ID)
		if err != nil {
			s.mu.Unlock()
			s.bgMu.Lock()
			s.bgStatus.Error = fmt.Errorf("get document %s: %w", info.ID, err)
			s.bgMu.Unlock()
			return
		}

		mainDoc := &Document{
			Info: DocumentInfo{
				ID:   doc.Info.ID,
				Name: doc.Info.Name,
			},
			Content: DocumentContent{
				Blocks: fromSQLiteBlocks(doc.Blocks),
			},
		}

		if err := s.embedDocumentUnlocked(mainDoc); err != nil {
			s.mu.Unlock()
			s.bgMu.Lock()
			s.bgStatus.Error = fmt.Errorf("embed document %s: %w", info.ID, err)
			s.bgMu.Unlock()
			return
		}
		s.mu.Unlock()

		// Update progress
		s.bgMu.Lock()
		s.bgStatus.DocumentsDone = i + 1
		s.bgStatus.ElapsedTime = time.Since(s.bgStatus.StartedAt)
		s.bgMu.Unlock()
	}

	// Final save
	s.mu.Lock()
	if err := s.saveHNSWIndex(); err != nil {
		s.bgMu.Lock()
		s.bgStatus.Error = fmt.Errorf("save HNSW index: %w", err)
		s.bgMu.Unlock()
	}
	s.mu.Unlock()
}

// GetBackgroundStatus returns the current status of background embedding.
// Safe to call even when no background operation is running.
func (s *Store) GetBackgroundStatus() BackgroundEmbeddingStatus {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	status := s.bgStatus
	if status.Running {
		status.ElapsedTime = time.Since(status.StartedAt)
	}
	return status
}

// IsBackgroundRunning returns true if background embedding is in progress.
func (s *Store) IsBackgroundRunning() bool {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	return s.bgRunning
}

// WaitForBackground blocks until background embedding completes.
// Returns the error from background embedding if any, or nil if successful.
// Returns nil immediately if no background operation is running.
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

// CancelBackground cancels background embedding if running.
// The operation will stop after the current document completes.
func (s *Store) CancelBackground() {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()

	if s.bgCancel != nil {
		s.bgCancel()
	}
}

// GetDocumentsWithIncompleteEmbeddings returns documents that have some but not all blocks embedded.
// This identifies documents where embedding was interrupted mid-way and need recovery.
func (s *Store) GetDocumentsWithIncompleteEmbeddings() ([]*DocumentInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sqliteInfos, err := s.db.GetDocumentsWithIncompleteEmbeddings()
	if err != nil {
		return nil, err
	}

	return fromSQLiteDocumentInfos(sqliteInfos), nil
}

// ResumeEmbedding continues embedding for a document that was partially embedded.
// Only embeds blocks that don't already have vectors, making it safe to call
// on documents that were interrupted during embedding.
func (s *Store) ResumeEmbedding(docID string) error {
	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Get blocks that still need embedding
	unembeddedBlocks, err := s.db.GetUnembeddedBlocks(docID)
	if err != nil {
		return fmt.Errorf("get unembedded blocks: %w", err)
	}

	if len(unembeddedBlocks) == 0 {
		return nil // All blocks already embedded
	}

	// Collect texts and IDs for embedding
	var texts []string
	var blockIDs []string
	for _, block := range unembeddedBlocks {
		texts = append(texts, block.Content)
		blockIDs = append(blockIDs, block.ID)
	}

	// Generate embeddings in batches with truncation fallback
	ctx := context.Background()
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, usedTexts, err := s.embedWithTruncationFallback(ctx, texts[i:end], docID)
		if err != nil {
			// Save what we have so far before returning error
			if len(vectors) > 0 {
				s.db.SaveVectors(docID, vectors)
				// Add to HNSW using batch operation
				hnswItems := make([]vectorindex.VectorItem, len(vectors))
				for i, v := range vectors {
					hnswItems[i] = vectorindex.VectorItem{
						ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
						Vector: v.Vector,
					}
				}
				s.hnsw.AddBatch(hnswItems)
			}
			return fmt.Errorf("generate embeddings: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: docID,
				Vector:     emb,
				Text:       usedTexts[j],
				Model:      s.embedder.Name(),
			})
		}
	}

	// Save all vectors to SQLite
	if err := s.db.SaveVectors(docID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

	// Add to HNSW index using batch operation
	hnswItems := make([]vectorindex.VectorItem, len(vectors))
	for i, v := range vectors {
		hnswItems[i] = vectorindex.VectorItem{
			ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
			Vector: v.Vector,
		}
	}
	if err := s.hnsw.AddBatch(hnswItems); err != nil {
		log.Printf("warning: failed to add vectors to HNSW: %v", err)
	}

	// Save HNSW to disk
	return s.saveHNSWIndex()
}

// ResumeAllIncompleteEmbeddings resumes embedding for all documents with incomplete embeddings.
// This is useful for recovering from crashes or interruptions during batch embedding.
func (s *Store) ResumeAllIncompleteEmbeddings() error {
	if s.embedder == nil {
		return fmt.Errorf("embedding provider not configured")
	}

	// Get incomplete documents (no lock needed for read)
	s.mu.RLock()
	incomplete, err := s.db.GetDocumentsWithIncompleteEmbeddings()
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("get incomplete documents: %w", err)
	}

	if len(incomplete) == 0 {
		return nil
	}

	log.Printf("warning: found %d documents with incomplete embeddings, resuming...", len(incomplete))

	// Resume each document
	for _, doc := range incomplete {
		if err := s.ResumeEmbedding(doc.ID); err != nil {
			return fmt.Errorf("resume embedding for %s: %w", doc.ID, err)
		}
	}

	return nil
}

// RepairAllMissingBlockEmbeddings finds and embeds all blocks that are missing vector embeddings.
// Unlike ResumeAllIncompleteEmbeddings, this checks actual block/vector counts rather than
// relying on embed_status field. Use this to repair documents where embedding completed but
// some blocks are still missing vectors.
func (s *Store) RepairAllMissingBlockEmbeddings() (int, error) {
	if s.embedder == nil {
		return 0, fmt.Errorf("embedding provider not configured")
	}

	// Get documents with missing block embeddings (sqlite store handles its own mutex)
	docs, err := s.db.GetDocumentsWithMissingBlockEmbeddings()
	if err != nil {
		return 0, fmt.Errorf("get documents with missing embeddings: %w", err)
	}

	if len(docs) == 0 {
		return 0, nil
	}

	repaired := 0
	for _, doc := range docs {
		if err := s.ResumeEmbedding(doc.ID); err != nil {
			log.Printf("[RepairEmbeddings] Error repairing %s: %v", doc.Name, err)
			continue
		}
		repaired++
	}

	return repaired, nil
}

// CheckHealth performs a comprehensive consistency check on the store.
// It checks for HNSW-SQLite synchronization, incomplete embeddings, and other issues.
func (s *Store) CheckHealth() (*StoreHealth, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	health := &StoreHealth{}

	// Get document and block counts
	docCount, err := s.db.GetDocumentCount()
	if err != nil {
		return nil, fmt.Errorf("get document count: %w", err)
	}
	health.DocumentCount = docCount

	blockCount, err := s.db.GetBlockCount()
	if err != nil {
		return nil, fmt.Errorf("get block count: %w", err)
	}
	health.BlockCount = blockCount

	// Check HNSW vs SQLite vector sync
	vectorCount, err := s.db.GetVectorCount()
	if err != nil {
		return nil, fmt.Errorf("get vector count: %w", err)
	}
	health.HNSWSize = s.hnsw.Size()
	health.SQLiteVectorCount = vectorCount
	health.HNSWSynced = health.HNSWSize == health.SQLiteVectorCount

	// Get documents with incomplete embeddings
	incomplete, err := s.db.GetDocumentsWithIncompleteEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("get incomplete embeddings: %w", err)
	}
	for _, doc := range incomplete {
		health.IncompleteEmbeddings = append(health.IncompleteEmbeddings, doc.ID)
	}

	// Get documents pending embeddings
	pending, err := s.db.GetDocumentsWithoutEmbeddings()
	if err != nil {
		return nil, fmt.Errorf("get pending embeddings: %w", err)
	}
	for _, doc := range pending {
		health.PendingEmbeddings = append(health.PendingEmbeddings, doc.ID)
	}

	// Determine overall health
	health.IsHealthy = health.HNSWSynced && len(health.IncompleteEmbeddings) == 0

	return health, nil
}

// Repair fixes detected inconsistencies in the store.
// It rebuilds the HNSW index from SQLite and resumes incomplete embeddings.
// Returns nil if no repairs were needed or all repairs succeeded.
func (s *Store) Repair() error {
	// First check health to see what needs fixing
	health, err := s.CheckHealth()
	if err != nil {
		return fmt.Errorf("check health: %w", err)
	}

	if health.IsHealthy {
		return nil // Nothing to repair
	}

	// Fix HNSW-SQLite desync if needed
	if !health.HNSWSynced {
		log.Printf("warning: HNSW index out of sync (%d vs %d vectors), rebuilding...",
			health.HNSWSize, health.SQLiteVectorCount)

		s.mu.Lock()

		// Create fresh HNSW index
		var hnswCfg *vectorindex.Config
		if s.config.HNSWConfig != nil {
			hnswCfg = &vectorindex.Config{
				M:        s.config.HNSWConfig.M,
				EfConst:  s.config.HNSWConfig.EfConst,
				EfSearch: s.config.HNSWConfig.EfSearch,
			}
		}
		s.hnsw = vectorindex.NewHNSW(hnswCfg)

		// Rebuild from SQLite using streaming/batched approach to avoid OOM
		const batchSize = 1000
		offset := 0
		totalAdded := 0

		for {
			vectors, err := s.db.GetVectorsBatched(offset, batchSize)
			if err != nil {
				s.mu.Unlock()
				return fmt.Errorf("get vectors batch at offset %d: %w", offset, err)
			}

			if len(vectors) == 0 {
				break
			}

			hnswItems := make([]vectorindex.VectorItem, len(vectors))
			for i, v := range vectors {
				hnswItems[i] = vectorindex.VectorItem{
					ID:     fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID),
					Vector: v.Vector,
				}
			}
			if err := s.hnsw.AddBatch(hnswItems); err != nil {
				log.Printf("warning: failed to add batch at offset %d to HNSW: %v", offset, err)
			}

			totalAdded += len(vectors)
			offset += batchSize

			if totalAdded%10000 == 0 {
				log.Printf("HNSW repair progress: %d/%d vectors", totalAdded, health.SQLiteVectorCount)
			}
		}

		// Save rebuilt index
		if err := s.saveHNSWIndex(); err != nil {
			log.Printf("warning: failed to save HNSW index: %v", err)
		}
		s.mu.Unlock()

		log.Printf("info: HNSW index rebuilt with %d vectors", totalAdded)
	}

	// Resume incomplete embeddings if embedder is available
	if s.embedder != nil && len(health.IncompleteEmbeddings) > 0 {
		log.Printf("warning: resuming %d documents with incomplete embeddings...",
			len(health.IncompleteEmbeddings))

		for _, docID := range health.IncompleteEmbeddings {
			if err := s.ResumeEmbedding(docID); err != nil {
				return fmt.Errorf("resume embedding for %s: %w", docID, err)
			}
		}

		log.Printf("info: completed embedding recovery for %d documents",
			len(health.IncompleteEmbeddings))
	}

	return nil
}

// embedDocumentUnlocked generates embeddings without acquiring locks (caller must hold lock).
// NOTE: This function does NOT save the HNSW index - caller is responsible for calling
// saveHNSWIndex() after batch operations complete.
func (s *Store) embedDocumentUnlocked(doc *Document) error {
	if s.embedder == nil {
		return nil
	}

	// Chunking config with defaults
	maxChars := s.config.MaxChunkChars
	if maxChars <= 0 {
		maxChars = 48000 // Default for models with higher token limits
	}
	overlap := s.config.ChunkOverlap
	if overlap <= 0 {
		overlap = 200
	}

	// Collect embeddable text blocks with chunking for long content
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if block.Content != "" {
				chunks := chunkText(block.Content, maxChars, overlap)
				for i, chunk := range chunks {
					texts = append(texts, chunk)
					// Use block ID with chunk suffix for multi-chunk blocks
					if len(chunks) > 1 {
						blockIDs = append(blockIDs, fmt.Sprintf("%s#%d", block.ID, i))
					} else {
						blockIDs = append(blockIDs, block.ID)
					}
				}
			}
		}
	}

	if len(texts) == 0 {
		return nil
	}

	// Generate embeddings in batches with truncation fallback
	ctx := context.Background()
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, usedTexts, err := s.embedWithTruncationFallback(ctx, texts[i:end], doc.Info.ID)
		if err != nil {
			return fmt.Errorf("generate embeddings: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       usedTexts[j],
				Model:      s.embedder.Name(),
			})
		}
	}

	// Save vectors to SQLite
	if err := s.db.SaveVectors(doc.Info.ID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

	// Add to HNSW index only if within limit (prevents OOM during large embedding runs)
	// If limit exceeded, vectors are still in SQLite and brute-force search will be used
	if s.config.MaxHNSWVectors > 0 && s.hnsw.Size()+len(vectors) > s.config.MaxHNSWVectors {
		return nil
	}

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

	return nil
}

// saveHNSWIndex saves the HNSW index to disk if there are pending changes
func (s *Store) saveHNSWIndex() error {
	if s.hnsw.IsDirty() {
		hnswPath := filepath.Join(s.config.BasePath, "hnsw.idx")
		if err := s.hnsw.SaveToFile(hnswPath); err != nil {
			return fmt.Errorf("save HNSW index: %w", err)
		}
	}
	return nil
}

// IndexCustomDataBatch indexes multiple custom data entries efficiently.
// This is optimized for bulk imports with deferred global stats updates.
func (s *Store) IndexCustomDataBatch(data []*CustomData, opts ...IndexOption) ([]*Document, error) {
	if len(data) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	idxCfg := defaultIndexConfig()
	for _, opt := range opts {
		opt(idxCfg)
	}

	var docs []*Document

	for _, d := range data {
		// Validate
		if d == nil {
			continue
		}
		if d.Source == "" {
			return docs, ErrMissingSource
		}
		if d.Name == "" {
			return docs, fmt.Errorf("missing name for source %s", d.Source)
		}
		if len(d.Entries) == 0 {
			return docs, ErrMissingEntries
		}

		// Check if document already exists by ExternalID (for upsert behavior)
		var docID string
		var isUpdate bool
		if d.ExternalID != "" {
			existingDoc, err := s.db.FindBySourceAndExternalID(d.Source, d.ExternalID)
			if err == nil && existingDoc != nil {
				docID = existingDoc.ID
				isUpdate = true
				// Delete old vectors for this document (will be regenerated)
				if s.hnsw != nil {
					oldVectors, err := s.db.GetVectorsForDocument(docID)
					if err != nil {
						log.Printf("warning: could not get old vectors for %s: %v", docID, err)
					}
					for _, v := range oldVectors {
						hnswID := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
						s.hnsw.Delete(hnswID)
					}
				}
				s.db.DeleteVectorsForDocument(docID)
			}
		}
		if docID == "" {
			docID = uuid.New().String()
		}
		if isUpdate {
			log.Printf("Updating existing document %s (source=%s, externalID=%s)", docID, d.Source, d.ExternalID)
		}

		// Convert entries to blocks
		var blocks []ContentBlock
		var totalSize int64
		var pendingImages []pendingImage

		for i, entry := range d.Entries {
			entryID := entry.ID
			if entryID == "" {
				entryID = fmt.Sprintf("entry_%d", i)
			}

			block := ContentBlock{
				ID:      entryID,
				Type:    BlockTypeCustom,
				Content: entry.Content,
				Page:    1,
				Semantic: SemanticInfo{
					Section: d.Source, // Use source as section for grouping
				},
			}

			blocks = append(blocks, block)
			totalSize += int64(len(entry.Content))

			// Process entry-level images
			for j, img := range entry.Images {
				if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
					continue
				}

				imgID := uuid.New().String()
				format := normalizeImageFormat(img.Format)

				// Auto-detect dimensions if not provided
				width, height := img.Width, img.Height
				if width == 0 || height == 0 {
					if w, h, err := detectImageDimensions(img.Data); err == nil {
						width, height = w, h
					}
				}

				name := img.OriginalName
				if name == "" {
					name = fmt.Sprintf("image_%d.%s", j+1, format)
				}

				// Add image block
				blocks = append(blocks, ContentBlock{
					ID:   imgID,
					Type: BlockTypeImage,
					Page: 1,
					Semantic: SemanticInfo{
						Section: d.Source,
					},
				})

				// Queue image for saving after document exists
				pendingImages = append(pendingImages, pendingImage{
					Data:        img.Data,
					Format:      format,
					Width:       width,
					Height:      height,
					Page:        1,
					Name:        name,
					BlockIndex:  len(blocks) - 1, // Index of the image block just added
					BlockID:     entryID,         // Link to parent entry
					Description: img.Description,
				})
			}
		}

		// Process document-level images
		for j, img := range d.Images {
			if len(img.Data) == 0 || !isValidImageFormat(img.Format) {
				continue
			}

			imgID := uuid.New().String()
			format := normalizeImageFormat(img.Format)

			// Auto-detect dimensions if not provided
			width, height := img.Width, img.Height
			if width == 0 || height == 0 {
				if w, h, err := detectImageDimensions(img.Data); err == nil {
					width, height = w, h
				}
			}

			name := img.OriginalName
			if name == "" {
				name = fmt.Sprintf("doc_image_%d.%s", j+1, format)
			}

			// Add image block
			blocks = append(blocks, ContentBlock{
				ID:   imgID,
				Type: BlockTypeImage,
				Page: 1,
				Semantic: SemanticInfo{
					Section: d.Source,
				},
			})

			// Queue image for saving after document exists
			pendingImages = append(pendingImages, pendingImage{
				Data:        img.Data,
				Format:      format,
				Width:       width,
				Height:      height,
				Page:        1,
				Name:        name,
				BlockIndex:  len(blocks) - 1, // Index of the image block just added
				BlockID:     "",              // No parent entry for document-level images
				Description: img.Description,
			})
		}

		// Create document
		now := time.Now().UTC()
		importedAt := d.ImportedAt
		if importedAt.IsZero() {
			importedAt = now
		}

		doc := &Document{
			Info: DocumentInfo{
				ID:          docID,
				Name:        d.Name,
				Format:      "customdata",
				SizeBytes:   totalSize,
				PageCount:   1,
				CreatedAt:   now,
				UpdatedAt:   now,
				Source:      d.Source,
				Description: d.Description,
				ImportedAt:  importedAt,
				ExternalID:  d.ExternalID,
			},
			Content: DocumentContent{
				Blocks: blocks,
			},
		}

		// Save to SQLite
		sqliteDoc := toSQLiteDocument(doc)
		if err := s.db.SaveDocument(sqliteDoc); err != nil {
			return docs, fmt.Errorf("save document: %w", err)
		}

		// Save tags
		if len(d.Tags) > 0 {
			if err := s.db.SaveDocumentTags(docID, d.Tags); err != nil {
				return docs, fmt.Errorf("save tags: %w", err)
			}
		}

		// Save images (after document exists in DB)
		for _, img := range pendingImages {
			blockID := img.BlockID
			if blockID == "" && img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
				blockID = doc.Content.Blocks[img.BlockIndex].ID
			}
			imageID, err := s.db.SaveImage(doc.Info.ID, img.Data, img.Format, img.Width, img.Height, img.Page, img.Name, blockID, img.Description)
			if err != nil {
				fmt.Printf("warning: failed to save image %s: %v\n", img.Name, err)
				continue
			}
			// Update content block reference
			if img.BlockIndex >= 0 && img.BlockIndex < len(doc.Content.Blocks) {
				doc.Content.Blocks[img.BlockIndex].Content = fmt.Sprintf("images/%s", imageID)
			}
		}

		// Index for search (with deferred global stats)
		sqliteBlocks := toSQLiteBlocks(doc)
		if err := s.db.IndexDocumentWithOptions(docID, sqliteBlocks, true); err != nil {
			return docs, fmt.Errorf("index document: %w", err)
		}

		// Generate embeddings unless deferred
		if !idxCfg.DeferEmbedding && s.embedder != nil {
			if err := s.embedDocumentUnlocked(doc); err != nil {
				// Log warning but don't fail
				fmt.Printf("warning: embedding failed for %s: %v\n", docID, err)
			}
		}

		docs = append(docs, doc)
	}

	// Update global stats once at the end
	if err := s.db.UpdateGlobalStats(); err != nil {
		return docs, fmt.Errorf("update global stats: %w", err)
	}

	return docs, nil
}
