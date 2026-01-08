package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
)

// MaxRecursionDepth limits recursive operations to prevent stack overflow
const MaxRecursionDepth = 100

// Document represents a parsed PDF document
type Document struct {
	data    []byte      // Raw file data
	version string      // PDF version (e.g., "1.7")
	xref    *XRefTable  // Cross-reference table
	catalog Dict        // Document catalog
	info    Dict        // Document info dictionary (optional)
	objects map[int]Object // Cached resolved objects
}

// Open opens a PDF file and parses its structure
func Open(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return OpenReader(bytes.NewReader(data))
}

// OpenReader opens a PDF from an io.Reader
func OpenReader(r io.Reader) (*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}
	return OpenBytes(data)
}

// OpenBytes opens a PDF from a byte slice
func OpenBytes(data []byte) (*Document, error) {
	doc := &Document{
		data:    data,
		objects: make(map[int]Object),
	}

	// Parse header
	if err := doc.parseHeader(); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	// Find and parse xref
	xrefParser := NewXRefParser(data)
	startxref, err := xrefParser.FindStartXRef()
	if err != nil {
		return nil, fmt.Errorf("find startxref: %w", err)
	}

	doc.xref, err = xrefParser.ParseXRef(startxref)
	if err != nil {
		return nil, fmt.Errorf("parse xref: %w", err)
	}

	// Get document catalog
	rootRef := doc.xref.Trailer.GetRef("Root")
	if rootRef == nil {
		return nil, fmt.Errorf("missing Root in trailer")
	}

	rootObj, err := doc.ResolveReference(rootRef)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}

	catalog, ok := rootObj.(Dict)
	if !ok {
		return nil, fmt.Errorf("root is not a dictionary")
	}
	doc.catalog = catalog

	// Get info dictionary (optional)
	if infoRef := doc.xref.Trailer.GetRef("Info"); infoRef != nil {
		if infoObj, err := doc.ResolveReference(infoRef); err == nil {
			if info, ok := infoObj.(Dict); ok {
				doc.info = info
			}
		}
	}

	return doc, nil
}

// parseHeader parses the PDF header to get version
func (d *Document) parseHeader() error {
	// PDF header: %PDF-X.Y
	if len(d.data) < 8 {
		return fmt.Errorf("file too short")
	}

	if !bytes.HasPrefix(d.data, []byte("%PDF-")) {
		return fmt.Errorf("not a PDF file")
	}

	// Extract version
	end := bytes.IndexByte(d.data[:min(20, len(d.data))], '\n')
	if end < 0 {
		end = 8
	}
	header := string(d.data[:end])

	re := regexp.MustCompile(`%PDF-(\d+\.\d+)`)
	matches := re.FindStringSubmatch(header)
	if len(matches) < 2 {
		return fmt.Errorf("invalid PDF header")
	}
	d.version = matches[1]

	return nil
}

// Version returns the PDF version string
func (d *Document) Version() string {
	return d.version
}

// Catalog returns the document catalog dictionary
func (d *Document) Catalog() Dict {
	return d.catalog
}

// Info returns the document info dictionary (may be nil)
func (d *Document) Info() Dict {
	return d.info
}

// Title returns the document title from info dictionary
func (d *Document) Title() string {
	if d.info == nil {
		return ""
	}
	return d.info.GetString("Title")
}

// Author returns the document author from info dictionary
func (d *Document) Author() string {
	if d.info == nil {
		return ""
	}
	return d.info.GetString("Author")
}

// PageCount returns the number of pages in the document
func (d *Document) PageCount() (int, error) {
	pagesRef := d.catalog.GetRef("Pages")
	if pagesRef == nil {
		return 0, fmt.Errorf("missing Pages in catalog")
	}

	pagesObj, err := d.ResolveReference(pagesRef)
	if err != nil {
		return 0, fmt.Errorf("resolve pages: %w", err)
	}

	pages, ok := pagesObj.(Dict)
	if !ok {
		return 0, fmt.Errorf("pages is not a dictionary")
	}

	return int(pages.GetInt("Count")), nil
}

// ResolveReference resolves an indirect reference to its actual object
func (d *Document) ResolveReference(ref *Ref) (Object, error) {
	return d.resolveReferenceWithDepth(ref, make(map[int]bool), 0)
}

// resolveReferenceWithDepth resolves a reference with cycle detection and depth limiting
func (d *Document) resolveReferenceWithDepth(ref *Ref, resolving map[int]bool, depth int) (Object, error) {
	if ref == nil {
		return nil, fmt.Errorf("nil reference")
	}

	// Prevent infinite recursion
	if depth > MaxRecursionDepth {
		return nil, fmt.Errorf("maximum recursion depth exceeded resolving object %d", ref.Num)
	}

	// Check for circular reference (currently being resolved in this chain)
	if resolving[ref.Num] {
		return nil, fmt.Errorf("circular reference detected for object %d", ref.Num)
	}

	// Check cache
	if obj, ok := d.objects[ref.Num]; ok {
		return obj, nil
	}

	// Mark as being resolved
	resolving[ref.Num] = true
	defer delete(resolving, ref.Num)

	// Find in xref
	entry := d.xref.Get(ref.Num)
	if entry == nil {
		return nil, fmt.Errorf("object %d not found in xref", ref.Num)
	}

	if !entry.InUse {
		return Null{}, nil // Free object
	}

	var obj Object
	var err error

	if entry.Compressed {
		// Object is in an object stream
		obj, err = d.resolveCompressedObject(ref.Num, entry)
	} else {
		// Object is at a byte offset
		obj, err = d.resolveObjectAtOffset(ref.Num, entry.Offset)
	}

	if err != nil {
		return nil, err
	}

	// Cache the result
	d.objects[ref.Num] = obj
	return obj, nil
}

// resolveObjectAtOffset reads an object at the given byte offset
func (d *Document) resolveObjectAtOffset(objNum int, offset int64) (Object, error) {
	if offset < 0 || offset >= int64(len(d.data)) {
		return nil, fmt.Errorf("invalid object offset: %d", offset)
	}

	lexer := NewLexerFromBytes(d.data[offset:])
	parser := NewParser(lexer)

	indirectObj, err := parser.ParseIndirectObject()
	if err != nil {
		return nil, fmt.Errorf("parse object %d: %w", objNum, err)
	}

	if indirectObj.Num != objNum {
		return nil, fmt.Errorf("object number mismatch: expected %d, got %d", objNum, indirectObj.Num)
	}

	return indirectObj.Object, nil
}

// resolveCompressedObject reads an object from an object stream
func (d *Document) resolveCompressedObject(objNum int, entry *XRefEntry) (Object, error) {
	// Get the object stream
	streamRef := &Ref{Num: entry.StreamNum, Gen: 0}
	streamObj, err := d.ResolveReference(streamRef)
	if err != nil {
		return nil, fmt.Errorf("resolve object stream %d: %w", entry.StreamNum, err)
	}

	stream, ok := streamObj.(*Stream)
	if !ok {
		return nil, fmt.Errorf("object stream %d is not a stream", entry.StreamNum)
	}

	// Verify it's an object stream
	if stream.Dict.GetName("Type") != "ObjStm" {
		return nil, fmt.Errorf("stream %d is not an object stream", entry.StreamNum)
	}

	// Decode the stream
	data, err := DecodeStream(stream)
	if err != nil {
		return nil, fmt.Errorf("decode object stream: %w", err)
	}

	// Get stream parameters
	n := int(stream.Dict.GetInt("N"))     // Number of objects
	first := int(stream.Dict.GetInt("First")) // Offset of first object

	if entry.StreamIdx >= n {
		return nil, fmt.Errorf("object index %d out of range (max %d)", entry.StreamIdx, n)
	}

	// Parse the object number/offset pairs from the beginning
	lexer := NewLexerFromBytes(data[:first])

	offsets := make([]int, n)
	for i := 0; i < n; i++ {
		// Read object number
		numTok, err := lexer.Next()
		if err != nil {
			return nil, fmt.Errorf("read object number: %w", err)
		}

		// Read offset
		offsetTok, err := lexer.Next()
		if err != nil {
			return nil, fmt.Errorf("read object offset: %w", err)
		}

		_ = numTok // We trust the xref entry for the object number
		offsets[i] = int(offsetTok.IntValue())
	}

	// Find the object data
	objOffset := first + offsets[entry.StreamIdx]
	var objEnd int
	if entry.StreamIdx+1 < n {
		objEnd = first + offsets[entry.StreamIdx+1]
	} else {
		objEnd = len(data)
	}

	if objOffset >= len(data) || objEnd > len(data) {
		return nil, fmt.Errorf("object offset out of range")
	}

	// Parse the object
	objLexer := NewLexerFromBytes(data[objOffset:objEnd])
	objParser := NewParser(objLexer)
	return objParser.ParseObject()
}

// Resolve resolves any object, following references
func (d *Document) Resolve(obj Object) (Object, error) {
	if ref, ok := obj.(*Ref); ok {
		return d.ResolveReference(ref)
	}
	return obj, nil
}

// ResolveDict resolves an object and returns it as a Dict
func (d *Document) ResolveDict(obj Object) (Dict, error) {
	resolved, err := d.Resolve(obj)
	if err != nil {
		return nil, err
	}
	dict, ok := resolved.(Dict)
	if !ok {
		return nil, fmt.Errorf("expected dictionary, got %T", resolved)
	}
	return dict, nil
}

// ResolveArray resolves an object and returns it as an Array
func (d *Document) ResolveArray(obj Object) (Array, error) {
	resolved, err := d.Resolve(obj)
	if err != nil {
		return nil, err
	}
	arr, ok := resolved.(Array)
	if !ok {
		return nil, fmt.Errorf("expected array, got %T", resolved)
	}
	return arr, nil
}

// ResolveStream resolves an object and returns it as a Stream
func (d *Document) ResolveStream(obj Object) (*Stream, error) {
	resolved, err := d.Resolve(obj)
	if err != nil {
		return nil, err
	}
	stream, ok := resolved.(*Stream)
	if !ok {
		return nil, fmt.Errorf("expected stream, got %T", resolved)
	}
	return stream, nil
}

// GetObject returns the object with the given number
func (d *Document) GetObject(num int) (Object, error) {
	return d.ResolveReference(&Ref{Num: num, Gen: 0})
}

// ObjectCount returns the number of objects in the document
func (d *Document) ObjectCount() int {
	return d.xref.Size()
}

// Close releases resources associated with the document
func (d *Document) Close() error {
	d.data = nil
	d.objects = nil
	return nil
}

// Utility function for min (Go 1.21+)
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetTrailer returns the trailer dictionary
func (d *Document) GetTrailer() Dict {
	return d.xref.Trailer
}

// Metadata contains document metadata
type Metadata struct {
	Title        string
	Author       string
	Subject      string
	Keywords     string
	Creator      string
	Producer     string
	CreationDate string
	ModDate      string
}

// GetMetadata extracts document metadata
func (d *Document) GetMetadata() Metadata {
	m := Metadata{}
	if d.info == nil {
		return m
	}

	m.Title = d.info.GetString("Title")
	m.Author = d.info.GetString("Author")
	m.Subject = d.info.GetString("Subject")
	m.Keywords = d.info.GetString("Keywords")
	m.Creator = d.info.GetString("Creator")
	m.Producer = d.info.GetString("Producer")
	m.CreationDate = d.info.GetString("CreationDate")
	m.ModDate = d.info.GetString("ModDate")

	return m
}

// IsEncrypted checks if the document is encrypted
func (d *Document) IsEncrypted() bool {
	return d.xref.Trailer.Has("Encrypt")
}

// IsLinearized checks if the document is linearized (optimized for web)
func (d *Document) IsLinearized() bool {
	// Check first object for Linearized key
	if len(d.data) < 1024 {
		return false
	}

	// Look for /Linearized in the first part of the file
	return bytes.Contains(d.data[:min(1024, len(d.data))], []byte("/Linearized"))
}

// DumpObjectAt prints the object at a given offset (for debugging)
func (d *Document) DumpObjectAt(offset int64) (string, error) {
	if offset < 0 || offset >= int64(len(d.data)) {
		return "", fmt.Errorf("invalid offset")
	}

	lexer := NewLexerFromBytes(d.data[offset:])
	parser := NewParser(lexer)

	obj, err := parser.ParseIndirectObject()
	if err != nil {
		return "", err
	}

	return obj.String(), nil
}

// FindString searches for a string in the raw PDF data (for debugging)
func (d *Document) FindString(s string) []int64 {
	var offsets []int64
	data := d.data
	needle := []byte(s)

	offset := int64(0)
	for {
		idx := bytes.Index(data[offset:], needle)
		if idx < 0 {
			break
		}
		offsets = append(offsets, offset+int64(idx))
		offset += int64(idx) + 1
	}

	return offsets
}

// IterateObjects iterates over all objects in the document
func (d *Document) IterateObjects(fn func(num int, obj Object) error) error {
	for num := range d.xref.Entries {
		entry := d.xref.Entries[num]
		if !entry.InUse {
			continue
		}

		obj, err := d.GetObject(num)
		if err != nil {
			continue // Skip problematic objects
		}

		if err := fn(num, obj); err != nil {
			return err
		}
	}
	return nil
}

// GetRawData returns the raw PDF data (for advanced use)
func (d *Document) GetRawData() []byte {
	return d.data
}

// ParseObjectAtOffset parses an object at a specific offset (for testing)
func ParseObjectAtOffset(data []byte, offset int64) (Object, error) {
	lexer := NewLexerFromBytes(data[offset:])
	parser := NewParser(lexer)
	return parser.ParseObject()
}

// Helper to parse a number from a string
func parseNumber(s string) (int, error) {
	return strconv.Atoi(s)
}
