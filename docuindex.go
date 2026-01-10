package docuindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

	// Load or create HNSW index if embeddings are available
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
	if _, err := os.Stat(hnswPath); err == nil {
		// Load existing index
		s.hnsw.LoadFromFile(hnswPath)
	}

	// Initialize hybrid searcher
	s.hybrid = hsearch.NewHybridSearcher()
	s.hybrid.KeywordSearch = s.keywordSearch
	s.hybrid.GetBlockContent = s.getBlockContent
	s.hybrid.GetDocumentName = s.getDocumentName

	return s, nil
}

// SetEmbeddingProvider configures the embedding provider after store creation
func (s *Store) SetEmbeddingProvider(provider embedding.Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.embedder = provider

	// Set up vector search in hybrid searcher
	s.hybrid.VectorSearch = s.vectorSearch

	// Rebuild HNSW from stored vectors
	vectors, err := s.db.GetAllVectors()
	if err != nil {
		return fmt.Errorf("load vectors: %w", err)
	}

	for _, v := range vectors {
		id := fmt.Sprintf("%s:%s", v.DocumentID, v.BlockID)
		s.hnsw.Add(id, v.Vector)
	}

	return nil
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

	// Generate embeddings if provider configured
	if s.embedder != nil {
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

	// Collect text blocks
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if len(block.Content) > 0 {
				texts = append(texts, block.Content)
				blockIDs = append(blockIDs, block.ID)
			}
		}
	}

	if len(texts) == 0 {
		return nil
	}

	// Generate embeddings in batches
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, err := s.embedder.Embed(ctx, texts[i:end])
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       texts[i+j],
				Model:      s.embedder.Name(),
			})
		}
	}

	// Save vectors to SQLite
	if err := s.db.SaveVectors(doc.Info.ID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

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

	// Generate embeddings if provider configured
	if s.embedder != nil {
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

	// Generate embeddings if provider configured
	if s.embedder != nil {
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

	// Index for BM25 search
	blocks := toSQLiteBlocks(doc)
	if err := s.db.IndexDocument(doc.Info.ID, blocks); err != nil {
		return nil, fmt.Errorf("index document: %w", err)
	}

	// Generate embeddings if provider configured
	if s.embedder != nil {
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
	}

	return &SearchResults{
		Query:      query,
		TotalHits:  len(results),
		Results:    results,
		SearchTime: time.Since(startTime),
	}, nil
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
func (s *Store) vectorSearch(ctx context.Context, query string, limit int) ([]hsearch.VectorSearchResult, error) {
	if s.embedder == nil || s.hnsw == nil {
		return nil, nil
	}

	// Embed query
	queryVector, err := s.embedder.EmbedSingle(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Search HNSW
	hnswResults, err := s.hnsw.Search(queryVector, limit)
	if err != nil {
		return nil, fmt.Errorf("HNSW search: %w", err)
	}

	// Convert results
	out := make([]hsearch.VectorSearchResult, 0, len(hnswResults))
	for _, r := range hnswResults {
		// Parse document:block ID
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

	return out, nil
}

// getBlockContent retrieves content for a block (used by hybrid searcher)
func (s *Store) getBlockContent(docID, blockID string) (content, snippet string, page int, section string, err error) {
	block, err := s.db.GetBlockByID(docID, blockID)
	if err != nil {
		return "", "", 0, "", err
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

	// Embedding phase
	if s.embedder != nil {
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

	// Collect text blocks
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if len(block.Content) > 0 {
				texts = append(texts, block.Content)
				blockIDs = append(blockIDs, block.ID)
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

	// Generate embeddings in batches
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, err := s.embedder.Embed(ctx, texts[i:end])
		if err != nil {
			return fmt.Errorf("embed batch: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       texts[i+j],
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

// EmbedDocuments generates embeddings for specific documents by ID.
// This is useful for resumable batch processing.
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

	return nil
}

// EmbedPendingDocuments generates embeddings for all documents that don't have them yet.
// This is the main method for deferred embedding patterns.
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

	return nil
}

// embedDocumentUnlocked generates embeddings without acquiring locks (caller must hold lock)
func (s *Store) embedDocumentUnlocked(doc *Document) error {
	if s.embedder == nil {
		return nil
	}

	// Collect embeddable text blocks
	var texts []string
	var blockIDs []string

	for _, block := range doc.Content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading || block.Type == BlockTypeCustom {
			if block.Content != "" {
				texts = append(texts, block.Content)
				blockIDs = append(blockIDs, block.ID)
			}
		}
	}

	if len(texts) == 0 {
		return nil
	}

	// Generate embeddings in batches
	ctx := context.Background()
	batchSize := s.embedder.MaxBatchSize()
	var vectors []sqlite.VectorItem

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		embeddings, err := s.embedder.Embed(ctx, texts[i:end])
		if err != nil {
			return fmt.Errorf("generate embeddings: %w", err)
		}

		for j, emb := range embeddings {
			vectors = append(vectors, sqlite.VectorItem{
				BlockID:    blockIDs[i+j],
				DocumentID: doc.Info.ID,
				Vector:     emb,
				Text:       texts[i+j],
				Model:      s.embedder.Name(),
			})
		}
	}

	// Save vectors to SQLite
	if err := s.db.SaveVectors(doc.Info.ID, vectors); err != nil {
		return fmt.Errorf("save vectors: %w", err)
	}

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

		// Generate document ID
		docID := uuid.New().String()

		// Convert entries to blocks
		var blocks []ContentBlock
		var totalSize int64

		for i, entry := range d.Entries {
			blockID := entry.ID
			if blockID == "" {
				blockID = fmt.Sprintf("entry_%d", i)
			}

			block := ContentBlock{
				ID:      blockID,
				Type:    BlockTypeCustom,
				Content: entry.Content,
				Page:    1,
				Semantic: SemanticInfo{
					Section: d.Source, // Use source as section for grouping
				},
			}

			blocks = append(blocks, block)
			totalSize += int64(len(entry.Content))
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
