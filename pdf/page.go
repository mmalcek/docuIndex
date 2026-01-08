package pdf

import (
	"fmt"
)

// Page represents a single PDF page
type Page struct {
	doc       *Document
	dict      Dict      // Page dictionary
	number    int       // 1-indexed page number
	resources Dict      // Page resources (may be inherited)
	mediaBox  Rectangle // Page dimensions
	cropBox   Rectangle // Visible region
	contents  []byte    // Decoded content stream(s)
}

// Rectangle represents a PDF rectangle (4 numbers: llx, lly, urx, ury)
type Rectangle struct {
	LLX, LLY float64 // Lower-left corner
	URX, URY float64 // Upper-right corner
}

// Width returns the rectangle width
func (r Rectangle) Width() float64 {
	return r.URX - r.LLX
}

// Height returns the rectangle height
func (r Rectangle) Height() float64 {
	return r.URY - r.LLY
}

// PageIterator allows iterating over pages
type PageIterator struct {
	doc   *Document
	pages []*Ref // All page references in order
	index int
}

// NewPageIterator creates a page iterator for the document
func NewPageIterator(doc *Document) (*PageIterator, error) {
	pagesRef := doc.catalog.GetRef("Pages")
	if pagesRef == nil {
		return nil, fmt.Errorf("missing Pages in catalog")
	}

	pages, err := collectPages(doc, pagesRef)
	if err != nil {
		return nil, err
	}

	return &PageIterator{
		doc:   doc,
		pages: pages,
		index: 0,
	}, nil
}

// collectPages recursively collects all page references from the page tree
func collectPages(doc *Document, ref *Ref) ([]*Ref, error) {
	return collectPagesWithCycleCheck(doc, ref, make(map[int]bool), 0)
}

// collectPagesWithCycleCheck collects pages with cycle detection and depth limiting
func collectPagesWithCycleCheck(doc *Document, ref *Ref, visited map[int]bool, depth int) ([]*Ref, error) {
	// Prevent infinite recursion
	if depth > MaxRecursionDepth {
		return nil, fmt.Errorf("page tree too deep (max %d)", MaxRecursionDepth)
	}

	// Check for cycles
	if visited[ref.Num] {
		return nil, fmt.Errorf("circular reference in page tree at object %d", ref.Num)
	}
	visited[ref.Num] = true

	obj, err := doc.ResolveReference(ref)
	if err != nil {
		return nil, err
	}

	dict, ok := obj.(Dict)
	if !ok {
		return nil, fmt.Errorf("page tree node is not a dictionary")
	}

	nodeType := dict.GetName("Type")

	switch nodeType {
	case "Pages":
		// Pages node - recurse into Kids
		kidsArray := dict.GetArray("Kids")
		if kidsArray == nil {
			return nil, fmt.Errorf("Pages node missing Kids")
		}

		var pages []*Ref
		for _, kid := range kidsArray {
			kidRef, ok := kid.(*Ref)
			if !ok {
				continue
			}
			childPages, err := collectPagesWithCycleCheck(doc, kidRef, visited, depth+1)
			if err != nil {
				return nil, err
			}
			pages = append(pages, childPages...)
		}
		return pages, nil

	case "Page":
		// Leaf page node
		return []*Ref{ref}, nil

	default:
		return nil, fmt.Errorf("unknown page tree node type: %s", nodeType)
	}
}

// Next returns the next page, or nil if no more pages
func (pi *PageIterator) Next() (*Page, error) {
	if pi.index >= len(pi.pages) {
		return nil, nil
	}

	pi.index++
	return pi.doc.GetPage(pi.index) // 1-indexed
}

// Count returns the total number of pages
func (pi *PageIterator) Count() int {
	return len(pi.pages)
}

// Reset resets the iterator to the beginning
func (pi *PageIterator) Reset() {
	pi.index = 0
}

// GetPage returns a specific page (1-indexed)
func (doc *Document) GetPage(pageNum int) (*Page, error) {
	pagesRef := doc.catalog.GetRef("Pages")
	if pagesRef == nil {
		return nil, fmt.Errorf("missing Pages in catalog")
	}

	pages, err := collectPages(doc, pagesRef)
	if err != nil {
		return nil, err
	}

	if pageNum < 1 || pageNum > len(pages) {
		return nil, fmt.Errorf("page %d out of range (1-%d)", pageNum, len(pages))
	}

	pageRef := pages[pageNum-1]
	pageObj, err := doc.ResolveReference(pageRef)
	if err != nil {
		return nil, fmt.Errorf("resolve page: %w", err)
	}

	pageDict, ok := pageObj.(Dict)
	if !ok {
		return nil, fmt.Errorf("page is not a dictionary")
	}

	page := &Page{
		doc:    doc,
		dict:   pageDict,
		number: pageNum,
	}

	// Get resources (may be inherited)
	page.resources, err = page.getInheritedDict("Resources")
	if err != nil {
		// Resources might not exist, that's okay
		page.resources = make(Dict)
	}

	// Get MediaBox (may be inherited)
	page.mediaBox, err = page.getInheritedRectangle("MediaBox")
	if err != nil {
		return nil, fmt.Errorf("get MediaBox: %w", err)
	}

	// Get CropBox (defaults to MediaBox)
	page.cropBox, err = page.getInheritedRectangle("CropBox")
	if err != nil {
		page.cropBox = page.mediaBox
	}

	return page, nil
}

// Number returns the 1-indexed page number
func (p *Page) Number() int {
	return p.number
}

// Width returns the page width in points
func (p *Page) Width() float64 {
	return p.mediaBox.Width()
}

// Height returns the page height in points
func (p *Page) Height() float64 {
	return p.mediaBox.Height()
}

// MediaBox returns the page's media box
func (p *Page) MediaBox() Rectangle {
	return p.mediaBox
}

// CropBox returns the page's crop box
func (p *Page) CropBox() Rectangle {
	return p.cropBox
}

// Resources returns the page's resource dictionary
func (p *Page) Resources() Dict {
	return p.resources
}

// Dict returns the page dictionary
func (p *Page) Dict() Dict {
	return p.dict
}

// GetContents returns the decoded content stream(s) for the page
func (p *Page) GetContents() ([]byte, error) {
	if p.contents != nil {
		return p.contents, nil
	}

	contentsObj := p.dict.Get("Contents")
	if contentsObj == nil {
		return nil, nil // Page with no content
	}

	var data []byte

	switch contents := contentsObj.(type) {
	case *Ref:
		// Single content stream reference
		resolved, err := p.doc.ResolveReference(contents)
		if err != nil {
			return nil, fmt.Errorf("resolve contents: %w", err)
		}
		stream, ok := resolved.(*Stream)
		if !ok {
			return nil, fmt.Errorf("contents is not a stream")
		}
		data, err = DecodeStream(stream)
		if err != nil {
			return nil, fmt.Errorf("decode contents: %w", err)
		}

	case *Stream:
		// Direct stream (rare)
		var err error
		data, err = DecodeStream(contents)
		if err != nil {
			return nil, fmt.Errorf("decode contents: %w", err)
		}

	case Array:
		// Array of content streams - concatenate them
		for i, item := range contents {
			ref, ok := item.(*Ref)
			if !ok {
				continue
			}
			resolved, err := p.doc.ResolveReference(ref)
			if err != nil {
				return nil, fmt.Errorf("resolve contents[%d]: %w", i, err)
			}
			stream, ok := resolved.(*Stream)
			if !ok {
				continue
			}
			streamData, err := DecodeStream(stream)
			if err != nil {
				return nil, fmt.Errorf("decode contents[%d]: %w", i, err)
			}
			data = append(data, streamData...)
			data = append(data, '\n') // Separate streams
		}

	default:
		return nil, fmt.Errorf("unexpected contents type: %T", contents)
	}

	p.contents = data
	return data, nil
}

// GetFonts returns the fonts available on this page
func (p *Page) GetFonts() (map[string]Dict, error) {
	fonts := make(map[string]Dict)

	fontDict := p.resources.GetDict("Font")
	if fontDict == nil {
		return fonts, nil
	}

	for name, obj := range fontDict {
		fontObj, err := p.doc.Resolve(obj)
		if err != nil {
			continue
		}
		if dict, ok := fontObj.(Dict); ok {
			fonts[string(name)] = dict
		}
	}

	return fonts, nil
}

// GetXObjects returns the XObjects (images, forms) available on this page
func (p *Page) GetXObjects() (map[string]Object, error) {
	xobjects := make(map[string]Object)

	xobjDict := p.resources.GetDict("XObject")
	if xobjDict == nil {
		return xobjects, nil
	}

	for name, obj := range xobjDict {
		resolved, err := p.doc.Resolve(obj)
		if err != nil {
			continue
		}
		xobjects[string(name)] = resolved
	}

	return xobjects, nil
}

// GetImages returns image XObjects on this page
func (p *Page) GetImages() (map[string]*Stream, error) {
	images := make(map[string]*Stream)

	xobjects, err := p.GetXObjects()
	if err != nil {
		return nil, err
	}

	for name, obj := range xobjects {
		stream, ok := obj.(*Stream)
		if !ok {
			continue
		}
		if stream.Dict.GetName("Subtype") == "Image" {
			images[name] = stream
		}
	}

	return images, nil
}

// getInheritedDict gets a dictionary value, searching up the page tree
func (p *Page) getInheritedDict(key string) (Dict, error) {
	// Check current page
	if obj := p.dict.Get(key); obj != nil {
		resolved, err := p.doc.Resolve(obj)
		if err != nil {
			return nil, err
		}
		if dict, ok := resolved.(Dict); ok {
			return dict, nil
		}
	}

	// Check parent
	parentRef := p.dict.GetRef("Parent")
	if parentRef == nil {
		return nil, fmt.Errorf("%s not found", key)
	}

	return p.getInheritedDictFromParent(parentRef, key, 0)
}

func (p *Page) getInheritedDictFromParent(parentRef *Ref, key string, depth int) (Dict, error) {
	// Prevent infinite recursion on circular parent references
	if depth > MaxRecursionDepth {
		return nil, fmt.Errorf("page parent chain too deep (max %d)", MaxRecursionDepth)
	}

	parentObj, err := p.doc.ResolveReference(parentRef)
	if err != nil {
		return nil, err
	}

	parent, ok := parentObj.(Dict)
	if !ok {
		return nil, fmt.Errorf("parent is not a dictionary")
	}

	if obj := parent.Get(key); obj != nil {
		resolved, err := p.doc.Resolve(obj)
		if err != nil {
			return nil, err
		}
		if dict, ok := resolved.(Dict); ok {
			return dict, nil
		}
	}

	// Continue up the tree
	grandparentRef := parent.GetRef("Parent")
	if grandparentRef == nil {
		return nil, fmt.Errorf("%s not found", key)
	}

	return p.getInheritedDictFromParent(grandparentRef, key, depth+1)
}

// getInheritedRectangle gets a rectangle value, searching up the page tree
func (p *Page) getInheritedRectangle(key string) (Rectangle, error) {
	var rect Rectangle

	// Check current page
	if arr := p.dict.GetArray(key); arr != nil && len(arr) >= 4 {
		return arrayToRectangle(arr), nil
	}

	// Check parent
	parentRef := p.dict.GetRef("Parent")
	if parentRef == nil {
		return rect, fmt.Errorf("%s not found", key)
	}

	return p.getInheritedRectangleFromParent(parentRef, key, 0)
}

func (p *Page) getInheritedRectangleFromParent(parentRef *Ref, key string, depth int) (Rectangle, error) {
	var rect Rectangle

	// Prevent infinite recursion on circular parent references
	if depth > MaxRecursionDepth {
		return rect, fmt.Errorf("page parent chain too deep (max %d)", MaxRecursionDepth)
	}

	parentObj, err := p.doc.ResolveReference(parentRef)
	if err != nil {
		return rect, err
	}

	parent, ok := parentObj.(Dict)
	if !ok {
		return rect, fmt.Errorf("parent is not a dictionary")
	}

	if arr := parent.GetArray(key); arr != nil && len(arr) >= 4 {
		return arrayToRectangle(arr), nil
	}

	// Continue up the tree
	grandparentRef := parent.GetRef("Parent")
	if grandparentRef == nil {
		return rect, fmt.Errorf("%s not found", key)
	}

	return p.getInheritedRectangleFromParent(grandparentRef, key, depth+1)
}

// arrayToRectangle converts a PDF array to a Rectangle
func arrayToRectangle(arr Array) Rectangle {
	return Rectangle{
		LLX: arr.GetFloat(0),
		LLY: arr.GetFloat(1),
		URX: arr.GetFloat(2),
		URY: arr.GetFloat(3),
	}
}

// GetAnnotations returns annotations on this page
func (p *Page) GetAnnotations() ([]Dict, error) {
	annotsObj := p.dict.Get("Annots")
	if annotsObj == nil {
		return nil, nil
	}

	annotsArray, err := p.doc.ResolveArray(annotsObj)
	if err != nil {
		return nil, err
	}

	var annots []Dict
	for _, item := range annotsArray {
		resolved, err := p.doc.Resolve(item)
		if err != nil {
			continue
		}
		if dict, ok := resolved.(Dict); ok {
			annots = append(annots, dict)
		}
	}

	return annots, nil
}

// GetRotation returns the page rotation in degrees (0, 90, 180, or 270)
func (p *Page) GetRotation() int {
	rot := int(p.dict.GetInt("Rotate"))
	// Normalize to 0, 90, 180, 270
	rot = rot % 360
	if rot < 0 {
		rot += 360
	}
	return rot
}
