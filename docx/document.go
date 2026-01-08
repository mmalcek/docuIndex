package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Document represents an opened DOCX file
type Document struct {
	path          string
	zipReader     *zip.ReadCloser
	zipReaderMem  *zip.Reader
	data          []byte
	files         map[string]*zip.File

	// Parsed content (lazy loaded)
	contentTypes  *ContentTypes
	rootRels      *Relationships
	docRels       *Relationships
	document      *DocumentXML
	styles        *StylesXML
	numbering     *NumberingXML
	coreProps     *CoreProperties
	appProps      *AppProperties
}

// Open opens a DOCX file from the filesystem
func Open(path string) (*Document, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}

	doc := &Document{
		path:      path,
		zipReader: zr,
		files:     make(map[string]*zip.File),
	}

	// Build file lookup map
	for _, f := range zr.File {
		// Normalize path (remove leading slash if present)
		name := strings.TrimPrefix(f.Name, "/")
		doc.files[name] = f
	}

	// Validate it's a DOCX by checking for required files
	if err := doc.validate(); err != nil {
		zr.Close()
		return nil, err
	}

	return doc, nil
}

// OpenBytes opens a DOCX from a byte slice
func OpenBytes(data []byte) (*Document, error) {
	reader := bytes.NewReader(data)
	zr, err := zip.NewReader(reader, int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip from bytes: %w", err)
	}

	doc := &Document{
		data:         data,
		zipReaderMem: zr,
		files:        make(map[string]*zip.File),
	}

	// Build file lookup map
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "/")
		doc.files[name] = f
	}

	// Validate
	if err := doc.validate(); err != nil {
		return nil, err
	}

	return doc, nil
}

// OpenReader opens a DOCX from an io.Reader
func OpenReader(r io.Reader) (*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	return OpenBytes(data)
}

// Close releases resources held by the document
func (d *Document) Close() error {
	if d.zipReader != nil {
		return d.zipReader.Close()
	}
	return nil
}

// validate checks that the DOCX has required files
func (d *Document) validate() error {
	// Check for [Content_Types].xml
	if _, ok := d.files["[Content_Types].xml"]; !ok {
		return fmt.Errorf("invalid DOCX: missing [Content_Types].xml")
	}

	// Check for word/document.xml
	if _, ok := d.files["word/document.xml"]; !ok {
		return fmt.Errorf("invalid DOCX: missing word/document.xml")
	}

	return nil
}

// readFile reads a file from the ZIP archive
func (d *Document) readFile(name string) ([]byte, error) {
	f, ok := d.files[name]
	if !ok {
		return nil, fmt.Errorf("file not found: %s", name)
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}

	return data, nil
}

// hasFile checks if a file exists in the archive
func (d *Document) hasFile(name string) bool {
	_, ok := d.files[name]
	return ok
}

// GetContentTypes returns the content types (lazy loaded)
func (d *Document) GetContentTypes() (*ContentTypes, error) {
	if d.contentTypes != nil {
		return d.contentTypes, nil
	}

	data, err := d.readFile("[Content_Types].xml")
	if err != nil {
		return nil, err
	}

	var ct ContentTypes
	if err := xml.Unmarshal(data, &ct); err != nil {
		return nil, fmt.Errorf("parse [Content_Types].xml: %w", err)
	}

	d.contentTypes = &ct
	return d.contentTypes, nil
}

// GetRootRelationships returns the root relationships (lazy loaded)
func (d *Document) GetRootRelationships() (*Relationships, error) {
	if d.rootRels != nil {
		return d.rootRels, nil
	}

	data, err := d.readFile("_rels/.rels")
	if err != nil {
		// Root rels might not exist in all DOCX files
		return &Relationships{}, nil
	}

	var rels Relationships
	if err := xml.Unmarshal(data, &rels); err != nil {
		return nil, fmt.Errorf("parse _rels/.rels: %w", err)
	}

	d.rootRels = &rels
	return d.rootRels, nil
}

// GetDocumentRelationships returns document-level relationships (lazy loaded)
func (d *Document) GetDocumentRelationships() (*Relationships, error) {
	if d.docRels != nil {
		return d.docRels, nil
	}

	data, err := d.readFile("word/_rels/document.xml.rels")
	if err != nil {
		// Document rels might not exist
		return &Relationships{}, nil
	}

	var rels Relationships
	if err := xml.Unmarshal(data, &rels); err != nil {
		return nil, fmt.Errorf("parse word/_rels/document.xml.rels: %w", err)
	}

	d.docRels = &rels
	return d.docRels, nil
}

// GetDocument returns the main document content (lazy loaded)
func (d *Document) GetDocument() (*DocumentXML, error) {
	if d.document != nil {
		return d.document, nil
	}

	data, err := d.readFile("word/document.xml")
	if err != nil {
		return nil, err
	}

	var doc DocumentXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse word/document.xml: %w", err)
	}

	d.document = &doc
	return d.document, nil
}

// GetStyles returns the styles (lazy loaded)
func (d *Document) GetStyles() (*StylesXML, error) {
	if d.styles != nil {
		return d.styles, nil
	}

	if !d.hasFile("word/styles.xml") {
		// Styles file is optional
		return nil, nil
	}

	data, err := d.readFile("word/styles.xml")
	if err != nil {
		return nil, err
	}

	var styles StylesXML
	if err := xml.Unmarshal(data, &styles); err != nil {
		return nil, fmt.Errorf("parse word/styles.xml: %w", err)
	}

	d.styles = &styles
	return d.styles, nil
}

// GetNumbering returns the numbering definitions (lazy loaded)
func (d *Document) GetNumbering() (*NumberingXML, error) {
	if d.numbering != nil {
		return d.numbering, nil
	}

	if !d.hasFile("word/numbering.xml") {
		// Numbering file is optional
		return nil, nil
	}

	data, err := d.readFile("word/numbering.xml")
	if err != nil {
		return nil, err
	}

	var numbering NumberingXML
	if err := xml.Unmarshal(data, &numbering); err != nil {
		return nil, fmt.Errorf("parse word/numbering.xml: %w", err)
	}

	d.numbering = &numbering
	return d.numbering, nil
}

// GetCoreProperties returns core document properties (lazy loaded)
func (d *Document) GetCoreProperties() (*CoreProperties, error) {
	if d.coreProps != nil {
		return d.coreProps, nil
	}

	if !d.hasFile("docProps/core.xml") {
		return nil, nil
	}

	data, err := d.readFile("docProps/core.xml")
	if err != nil {
		return nil, err
	}

	var props CoreProperties
	if err := xml.Unmarshal(data, &props); err != nil {
		return nil, fmt.Errorf("parse docProps/core.xml: %w", err)
	}

	d.coreProps = &props
	return d.coreProps, nil
}

// GetAppProperties returns application properties (lazy loaded)
func (d *Document) GetAppProperties() (*AppProperties, error) {
	if d.appProps != nil {
		return d.appProps, nil
	}

	if !d.hasFile("docProps/app.xml") {
		return nil, nil
	}

	data, err := d.readFile("docProps/app.xml")
	if err != nil {
		return nil, err
	}

	var props AppProperties
	if err := xml.Unmarshal(data, &props); err != nil {
		return nil, fmt.Errorf("parse docProps/app.xml: %w", err)
	}

	d.appProps = &props
	return d.appProps, nil
}

// GetMetadata returns document metadata
func (d *Document) GetMetadata() Metadata {
	meta := Metadata{}

	// Get core properties
	if core, err := d.GetCoreProperties(); err == nil && core != nil {
		meta.Title = core.Title
		meta.Author = core.Creator
		meta.Subject = core.Subject
		meta.Keywords = core.Keywords
		meta.Creator = core.Creator
		meta.Created = core.Created
		meta.Modified = core.Modified
	}

	// Get app properties
	if app, err := d.GetAppProperties(); err == nil && app != nil {
		meta.PageCount = app.Pages
		meta.WordCount = app.Words
	}

	return meta
}

// PageCount returns the document page count
func (d *Document) PageCount() (int, error) {
	app, err := d.GetAppProperties()
	if err != nil {
		return 0, err
	}
	if app == nil {
		return 1, nil // Default to 1 page if no app properties
	}
	if app.Pages == 0 {
		return 1, nil
	}
	return app.Pages, nil
}

// ReadImage reads image data from the word/media folder
func (d *Document) ReadImage(target string) ([]byte, error) {
	// Target might be relative to word/ folder
	path := target
	if !strings.HasPrefix(path, "word/") && strings.HasPrefix(target, "media/") {
		path = "word/" + target
	}

	// Try with word/ prefix first
	data, err := d.readFile(path)
	if err != nil {
		// Try without prefix
		data, err = d.readFile(target)
	}

	return data, err
}

// GetImageFormat returns the image format based on filename extension
func GetImageFormat(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "jpeg"
	case ".png":
		return "png"
	case ".gif":
		return "gif"
	case ".bmp":
		return "bmp"
	case ".tiff", ".tif":
		return "tiff"
	case ".emf":
		return "emf"
	case ".wmf":
		return "wmf"
	default:
		return ext[1:] // Remove leading dot
	}
}

// ListImages returns a list of all image files in word/media/
func (d *Document) ListImages() []string {
	var images []string
	for name := range d.files {
		if strings.HasPrefix(name, "word/media/") {
			images = append(images, name)
		}
	}
	return images
}

// Path returns the original file path (empty for byte-based documents)
func (d *Document) Path() string {
	return d.path
}

// Name returns the document filename
func (d *Document) Name() string {
	if d.path != "" {
		return filepath.Base(d.path)
	}
	return ""
}

// Size returns the document size in bytes
func (d *Document) Size() int64 {
	if d.data != nil {
		return int64(len(d.data))
	}
	if d.path != "" {
		if info, err := os.Stat(d.path); err == nil {
			return info.Size()
		}
	}
	return 0
}
