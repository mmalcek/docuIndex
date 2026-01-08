package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ErrDocumentNotFound is returned when a document is not found
var ErrDocumentNotFound = errors.New("document not found")

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

// FileSystemStore implements Store using the local filesystem
type FileSystemStore struct {
	basePath string
	mu       sync.RWMutex
}

// NewFileSystemStore creates a new filesystem-based store
func NewFileSystemStore(basePath string) (*FileSystemStore, error) {
	// Create base directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("create base directory: %w", err)
	}

	return &FileSystemStore{
		basePath: basePath,
	}, nil
}

// GetBasePath returns the base path of the store
func (fs *FileSystemStore) GetBasePath() string {
	return fs.basePath
}

// docPath returns the path to a document's directory
func (fs *FileSystemStore) docPath(id string) string {
	return filepath.Join(fs.basePath, id)
}

// SaveDocument saves a document to the store
func (fs *FileSystemStore) SaveDocument(doc *Document) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	docDir := fs.docPath(doc.Info.ID)

	// Create document directory
	if err := os.MkdirAll(docDir, 0755); err != nil {
		return fmt.Errorf("create document directory: %w", err)
	}

	// Create images directory
	imagesDir := filepath.Join(docDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return fmt.Errorf("create images directory: %w", err)
	}

	// Save info.json
	infoPath := filepath.Join(docDir, "info.json")
	infoData, err := json.MarshalIndent(doc.Info, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal info: %w", err)
	}
	if err := os.WriteFile(infoPath, infoData, 0644); err != nil {
		return fmt.Errorf("write info.json: %w", err)
	}

	// Save content.json
	contentPath := filepath.Join(docDir, "content.json")
	contentData, err := json.MarshalIndent(doc.Content, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}
	if err := os.WriteFile(contentPath, contentData, 0644); err != nil {
		return fmt.Errorf("write content.json: %w", err)
	}

	return nil
}

// GetDocument retrieves a document from the store
func (fs *FileSystemStore) GetDocument(id string) (*Document, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	docDir := fs.docPath(id)

	// Check if document exists
	if _, err := os.Stat(docDir); os.IsNotExist(err) {
		return nil, ErrDocumentNotFound
	}

	doc := &Document{}

	// Load info.json
	infoPath := filepath.Join(docDir, "info.json")
	infoData, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, fmt.Errorf("read info.json: %w", err)
	}
	if err := json.Unmarshal(infoData, &doc.Info); err != nil {
		return nil, fmt.Errorf("unmarshal info: %w", err)
	}

	// Load content.json
	contentPath := filepath.Join(docDir, "content.json")
	contentData, err := os.ReadFile(contentPath)
	if err != nil {
		return nil, fmt.Errorf("read content.json: %w", err)
	}
	if err := json.Unmarshal(contentData, &doc.Content); err != nil {
		return nil, fmt.Errorf("unmarshal content: %w", err)
	}

	return doc, nil
}

// DeleteDocument removes a document from the store
func (fs *FileSystemStore) DeleteDocument(id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	docDir := fs.docPath(id)

	// Check if document exists
	if _, err := os.Stat(docDir); os.IsNotExist(err) {
		return ErrDocumentNotFound
	}

	// Remove the entire document directory
	if err := os.RemoveAll(docDir); err != nil {
		return fmt.Errorf("remove document: %w", err)
	}

	return nil
}

// ListDocuments returns info for all documents in the store
func (fs *FileSystemStore) ListDocuments() ([]*DocumentInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	entries, err := os.ReadDir(fs.basePath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	var docs []*DocumentInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Skip special directories
		if entry.Name() == "index" {
			continue
		}

		infoPath := filepath.Join(fs.basePath, entry.Name(), "info.json")
		infoData, err := os.ReadFile(infoPath)
		if err != nil {
			continue // Skip invalid documents
		}

		var info DocumentInfo
		if err := json.Unmarshal(infoData, &info); err != nil {
			continue
		}

		docs = append(docs, &info)
	}

	return docs, nil
}

// DocumentExists checks if a document exists
func (fs *FileSystemStore) DocumentExists(id string) bool {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	docDir := fs.docPath(id)
	_, err := os.Stat(docDir)
	return err == nil
}

// SaveImage saves an image to the document's images directory
func (fs *FileSystemStore) SaveImage(docID, imageID string, data []byte, format string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	imagesDir := filepath.Join(fs.docPath(docID), "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return fmt.Errorf("create images directory: %w", err)
	}

	// Determine file extension
	ext := "." + format
	if format == "jpeg" {
		ext = ".jpg"
	}

	imagePath := filepath.Join(imagesDir, imageID+ext)
	if err := os.WriteFile(imagePath, data, 0644); err != nil {
		return fmt.Errorf("write image: %w", err)
	}

	// Save image metadata
	metaPath := filepath.Join(imagesDir, imageID+".json")
	meta := map[string]interface{}{
		"id":     imageID,
		"format": format,
		"size":   len(data),
	}
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(metaPath, metaData, 0644)

	return nil
}

// GetImage retrieves an image from the document's images directory
func (fs *FileSystemStore) GetImage(docID, imageID string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	imagesDir := filepath.Join(fs.docPath(docID), "images")

	// Try common extensions
	extensions := []string{".jpg", ".png", ".jpeg", ".jp2"}
	for _, ext := range extensions {
		imagePath := filepath.Join(imagesDir, imageID+ext)
		if data, err := os.ReadFile(imagePath); err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("image not found: %s", imageID)
}

// ListImages returns the IDs of all images in a document
func (fs *FileSystemStore) ListImages(docID string) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	imagesDir := filepath.Join(fs.docPath(docID), "images")

	entries, err := os.ReadDir(imagesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var images []string
	seen := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		base := name[:len(name)-len(ext)]

		// Skip metadata files
		if ext == ".json" {
			continue
		}

		if !seen[base] {
			seen[base] = true
			images = append(images, base)
		}
	}

	return images, nil
}

// SaveIndex saves the search index to disk
func (fs *FileSystemStore) SaveIndex(data []byte) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	indexDir := filepath.Join(fs.basePath, "index")
	if err := os.MkdirAll(indexDir, 0755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}

	indexPath := filepath.Join(indexDir, "index.json")
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return fmt.Errorf("write index: %w", err)
	}

	return nil
}

// GetIndex loads the search index from disk
func (fs *FileSystemStore) GetIndex() ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	indexPath := filepath.Join(fs.basePath, "index", "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return data, nil
}

// Close closes the store (no-op for filesystem)
func (fs *FileSystemStore) Close() error {
	return nil
}

// GetDocumentPath returns the full path to a document directory
func (fs *FileSystemStore) GetDocumentPath(id string) string {
	return fs.docPath(id)
}

// GetImagePath returns the full path to an image file
func (fs *FileSystemStore) GetImagePath(docID, imageID, ext string) string {
	return filepath.Join(fs.docPath(docID), "images", imageID+ext)
}
