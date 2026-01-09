package pdf

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mmalcek/docuIndex/internal/nlp"
)

// BlockType represents the type of content block (local to pdf package)
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
	FormatPDF DocumentFormat = "pdf"
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

// DocumentContent holds the structured content of a document
type DocumentContent struct {
	Version string         `json:"version"`
	Blocks  []ContentBlock `json:"blocks"`
}

// DocumentInfo contains metadata about a document
type DocumentInfo struct {
	Format    DocumentFormat `json:"format"`
	PageCount int            `json:"page_count"`
	Name      string         `json:"name"`
}

// SemanticExtractor performs semantic analysis on extracted content
type SemanticExtractor struct {
	doc         *Document
	textExt     *TextExtractor
	imageExt    *ImageExtractor
}

// NewSemanticExtractor creates a new semantic extractor
func NewSemanticExtractor(doc *Document) *SemanticExtractor {
	return &SemanticExtractor{
		doc:      doc,
		textExt:  NewTextExtractor(doc),
		imageExt: NewImageExtractor(doc),
	}
}

// ExtractContent extracts all content from the document with semantic analysis
func (se *SemanticExtractor) ExtractContent() (*DocumentContent, error) {
	pageCount, err := se.doc.PageCount()
	if err != nil {
		return nil, err
	}

	content := &DocumentContent{
		Version: "1.0",
		Blocks:  make([]ContentBlock, 0),
	}

	var currentSection string
	blockID := 0

	for pageNum := 1; pageNum <= pageCount; pageNum++ {
		page, err := se.doc.GetPage(pageNum)
		if err != nil {
			continue
		}

		pageWidth := page.Width()
		pageHeight := page.Height()

		// Extract paragraphs and images (paragraph-level granularity)
		paragraphs, images, err := se.textExt.ExtractStructuredTextWithImages(pageNum)
		if err != nil {
			continue
		}

		// Process paragraphs
		for _, para := range paragraphs {
			blockID++
			cb := se.createContentBlockFromParagraph(blockID, para, pageNum, pageWidth, pageHeight)

			// Update section tracking
			if cb.Semantic.IsHeading {
				currentSection = para.Text
			} else {
				cb.Semantic.Section = currentSection
			}

			content.Blocks = append(content.Blocks, cb)
		}

		// Process images
		for _, img := range images {
			blockID++
			cb := se.createImageBlock(blockID, img, pageNum, pageWidth, pageHeight)
			cb.Semantic.Section = currentSection
			content.Blocks = append(content.Blocks, cb)
		}
	}

	// Extract keywords from content
	se.extractKeywords(content)

	return content, nil
}

// createContentBlock creates a ContentBlock from a TextBlock
func (se *SemanticExtractor) createContentBlock(id int, block TextBlock, pageNum int, pageWidth, pageHeight float64) ContentBlock {
	isHeading := se.detectHeading(block)
	headingLevel := 0
	blockType := BlockTypeText

	if isHeading {
		blockType = BlockTypeHeading
		headingLevel = se.detectHeadingLevel(block)
	} else if se.detectList(block) {
		blockType = BlockTypeList
	}

	return ContentBlock{
		ID:      formatBlockID(id),
		Type:    blockType,
		Content: strings.TrimSpace(block.Text),
		Page:    pageNum,
		BBox: BoundingBox{
			X:          block.X,
			Y:          block.Y,
			Width:      block.Width,
			Height:     block.Height,
			PageWidth:  pageWidth,
			PageHeight: pageHeight,
		},
		Font: &FontInfo{
			Name:   block.FontName,
			Size:   block.FontSize,
			Bold:   isBoldFont(block.FontName),
			Italic: isItalicFont(block.FontName),
		},
		Semantic: SemanticInfo{
			IsHeading:    isHeading,
			HeadingLevel: headingLevel,
		},
	}
}

// createContentBlockFromParagraph creates a ContentBlock from a Paragraph
func (se *SemanticExtractor) createContentBlockFromParagraph(id int, para Paragraph, pageNum int, pageWidth, pageHeight float64) ContentBlock {
	blockType := BlockTypeText
	headingLevel := 0

	if para.IsHeading {
		blockType = BlockTypeHeading
		headingLevel = se.detectHeadingLevelFromParagraph(para)
	} else if se.detectListFromParagraph(para) {
		blockType = BlockTypeList
	}

	return ContentBlock{
		ID:      formatBlockID(id),
		Type:    blockType,
		Content: strings.TrimSpace(para.Text),
		Page:    pageNum,
		BBox: BoundingBox{
			X:          para.MinX,
			Y:          para.MinY,
			Width:      para.Width,
			Height:     para.Height,
			PageWidth:  pageWidth,
			PageHeight: pageHeight,
		},
		Font: &FontInfo{
			Name:   para.FontName,
			Size:   para.FontSize,
			Bold:   isBoldFont(para.FontName),
			Italic: isItalicFont(para.FontName),
		},
		Semantic: SemanticInfo{
			IsHeading:    para.IsHeading,
			HeadingLevel: headingLevel,
		},
	}
}

// detectHeadingLevelFromParagraph determines the heading level (1-6) from a Paragraph
func (se *SemanticExtractor) detectHeadingLevelFromParagraph(para Paragraph) int {
	switch {
	case para.FontSize >= 24:
		return 1
	case para.FontSize >= 20:
		return 2
	case para.FontSize >= 16:
		return 3
	case para.FontSize >= 14:
		return 4
	case para.FontSize >= 12 && isBoldFont(para.FontName):
		return 5
	default:
		return 6
	}
}

// detectListFromParagraph determines if a paragraph is a list item
func (se *SemanticExtractor) detectListFromParagraph(para Paragraph) bool {
	text := strings.TrimSpace(para.Text)
	if len(text) == 0 {
		return false
	}

	// Bullet points
	bulletPrefixes := []string{"•", "●", "○", "■", "□", "▪", "▫", "-", "*", "‣", "◦"}
	for _, prefix := range bulletPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}

	// Numbered list (1. 2. 3. or (1) (2) (3) or a) b) c))
	numberedPattern := regexp.MustCompile(`^(\d+[.)]\s|[(]\d+[)]\s|[a-z][.)]\s)`)
	if numberedPattern.MatchString(text) {
		return true
	}

	return false
}

// createImageBlock creates a ContentBlock for an image
func (se *SemanticExtractor) createImageBlock(id int, img ImageRef, pageNum int, pageWidth, pageHeight float64) ContentBlock {
	return ContentBlock{
		ID:      formatBlockID(id),
		Type:    BlockTypeImage,
		Content: img.Name, // Will be replaced with actual path when stored
		Page:    pageNum,
		BBox: BoundingBox{
			X:          img.X,
			Y:          img.Y,
			Width:      img.Width,
			Height:     img.Height,
			PageWidth:  pageWidth,
			PageHeight: pageHeight,
		},
		Semantic: SemanticInfo{},
	}
}

// detectHeading determines if a text block is a heading
func (se *SemanticExtractor) detectHeading(block TextBlock) bool {
	text := strings.TrimSpace(block.Text)
	if len(text) == 0 {
		return false
	}

	// Large font size
	if block.FontSize >= 14 {
		return true
	}

	// Bold font
	if isBoldFont(block.FontName) && len(text) < 200 {
		return true
	}

	// Numbered heading pattern (1. Introduction, 1.1 Overview)
	numberedPattern := regexp.MustCompile(`^[\d.]+\s+[A-Z]`)
	if numberedPattern.MatchString(text) && len(text) < 150 {
		return true
	}

	// All caps short text
	if len(text) < 100 && isAllCaps(text) && len(text) > 3 {
		return true
	}

	// Chapter/Section keywords
	prefixPattern := regexp.MustCompile(`^(Chapter|Section|Part|CHAPTER|SECTION|PART)\s+\d+`)
	if prefixPattern.MatchString(text) {
		return true
	}

	return false
}

// detectHeadingLevel determines the heading level (1-6)
func (se *SemanticExtractor) detectHeadingLevel(block TextBlock) int {
	// Based on font size
	switch {
	case block.FontSize >= 24:
		return 1
	case block.FontSize >= 20:
		return 2
	case block.FontSize >= 16:
		return 3
	case block.FontSize >= 14:
		return 4
	case block.FontSize >= 12 && isBoldFont(block.FontName):
		return 5
	default:
		return 6
	}
}

// detectList determines if a text block is a list item
func (se *SemanticExtractor) detectList(block TextBlock) bool {
	text := strings.TrimSpace(block.Text)
	if len(text) == 0 {
		return false
	}

	// Bullet points
	bulletPrefixes := []string{"•", "●", "○", "■", "□", "▪", "▫", "-", "*", "‣", "◦"}
	for _, prefix := range bulletPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}

	// Numbered list (1. 2. 3. or (1) (2) (3) or a) b) c))
	numberedPattern := regexp.MustCompile(`^(\d+[.)]\s|[(]\d+[)]\s|[a-z][.)]\s)`)
	if numberedPattern.MatchString(text) {
		return true
	}

	return false
}

// extractKeywords extracts keywords from content blocks
func (se *SemanticExtractor) extractKeywords(content *DocumentContent) {
	// Collect all text
	var allText strings.Builder
	for _, block := range content.Blocks {
		if block.Type == BlockTypeText || block.Type == BlockTypeHeading {
			allText.WriteString(block.Content)
			allText.WriteString(" ")
		}
	}

	// Extract keywords using TF analysis
	keywords := extractTopKeywords(allText.String(), 20)

	// Assign keywords to headings and their sections
	var currentHeading int = -1
	for i := range content.Blocks {
		if content.Blocks[i].Semantic.IsHeading {
			currentHeading = i
			// Assign document-level keywords to headings
			if len(keywords) > 0 {
				content.Blocks[i].Semantic.Keywords = keywords[:min(5, len(keywords))]
			}
		} else if currentHeading >= 0 && i-currentHeading <= 5 {
			// Assign section keywords to nearby blocks
			blockKeywords := extractTopKeywords(content.Blocks[i].Content, 5)
			content.Blocks[i].Semantic.Keywords = blockKeywords
		}
	}
}

// extractTopKeywords extracts the top N keywords from text
func extractTopKeywords(text string, n int) []string {
	// Tokenize and count
	wordCounts := make(map[string]int)
	words := tokenize(text)

	for _, word := range words {
		word = strings.ToLower(word)
		if len(word) < 3 || isStopWord(word) {
			continue
		}
		wordCounts[word]++
	}

	// Sort by frequency
	type wordCount struct {
		word  string
		count int
	}
	var sorted []wordCount
	for word, count := range wordCounts {
		sorted = append(sorted, wordCount{word, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	// Get top N
	result := make([]string, 0, n)
	for i := 0; i < len(sorted) && i < n; i++ {
		result = append(result, sorted[i].word)
	}

	return result
}

// tokenize splits text into words
func tokenize(text string) []string {
	var words []string
	var current strings.Builder

	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				words = append(words, current.String())
				current.Reset()
			}
		}
	}

	if current.Len() > 0 {
		words = append(words, current.String())
	}

	return words
}

// isStopWord checks if a word is a common stop word using the shared nlp package
func isStopWord(word string) bool {
	return nlp.IsStopWord(word)
}

// formatBlockID formats a block ID
func formatBlockID(id int) string {
	return fmt.Sprintf("blk_%05d", id)
}

// isBoldFont checks if font name indicates bold
func isBoldFont(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "bold") || strings.Contains(lower, "black") || strings.Contains(lower, "heavy")
}

// isItalicFont checks if font name indicates italic
func isItalicFont(name string) bool {
	lower := strings.ToLower(name)
	return strings.Contains(lower, "italic") || strings.Contains(lower, "oblique")
}

// isAllCaps checks if text is all uppercase letters
func isAllCaps(text string) bool {
	hasLetter := false
	for _, r := range text {
		if unicode.IsLetter(r) {
			hasLetter = true
			if !unicode.IsUpper(r) {
				return false
			}
		}
	}
	return hasLetter
}

// ExtractDocumentInfo extracts document metadata
func (se *SemanticExtractor) ExtractDocumentInfo() DocumentInfo {
	info := DocumentInfo{
		Format: FormatPDF,
	}

	// Get page count
	pageCount, err := se.doc.PageCount()
	if err == nil {
		info.PageCount = pageCount
	}

	// Get metadata from PDF info dictionary
	meta := se.doc.GetMetadata()
	if meta.Title != "" {
		info.Name = meta.Title
	}

	return info
}
