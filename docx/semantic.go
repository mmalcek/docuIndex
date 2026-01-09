package docx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/mariomalcek/docuIndex/internal/nlp"
)

// SemanticExtractor performs semantic analysis on extracted content
type SemanticExtractor struct {
	doc           *Document
	textExt       *TextExtractor
	imageExt      *ImageExtractor
	tableExt      *TableExtractor
	styleResolver *StyleResolver
}

// NewSemanticExtractor creates a new semantic extractor
func NewSemanticExtractor(doc *Document) (*SemanticExtractor, error) {
	textExt, err := NewTextExtractor(doc)
	if err != nil {
		return nil, fmt.Errorf("create text extractor: %w", err)
	}

	imageExt, err := NewImageExtractor(doc)
	if err != nil {
		return nil, fmt.Errorf("create image extractor: %w", err)
	}

	return &SemanticExtractor{
		doc:           doc,
		textExt:       textExt,
		imageExt:      imageExt,
		tableExt:      NewTableExtractor(textExt),
		styleResolver: textExt.GetStyleResolver(),
	}, nil
}

// ExtractContent extracts all content from the document with semantic analysis
func (se *SemanticExtractor) ExtractContent() (*DocumentContent, error) {
	docXML, err := se.doc.GetDocument()
	if err != nil {
		return nil, fmt.Errorf("get document: %w", err)
	}

	pageCount, _ := se.doc.PageCount()
	if pageCount == 0 {
		pageCount = 1
	}

	content := &DocumentContent{
		Version: "1.0",
		Blocks:  make([]ContentBlock, 0),
	}

	var currentSection string
	blockID := 0
	paraIndex := 0

	// Count total paragraphs for position estimation
	totalParas := len(docXML.Body.Paragraphs)
	for _, table := range docXML.Body.Tables {
		totalParas += len(table.Rows)
	}

	// Process paragraphs
	for i := range docXML.Body.Paragraphs {
		para := &docXML.Body.Paragraphs[i]

		// Extract text block
		textBlock := se.textExt.ExtractParagraph(para)
		if textBlock == nil || strings.TrimSpace(textBlock.Text) == "" {
			paraIndex++
			continue
		}

		blockID++
		cb := se.createContentBlock(blockID, textBlock, para, paraIndex, totalParas, pageCount)

		// Check for heading
		isHeading, headingLevel := se.textExt.IsHeadingParagraph(para)
		if isHeading {
			cb.Type = BlockTypeHeading
			cb.Semantic.IsHeading = true
			cb.Semantic.HeadingLevel = headingLevel
			currentSection = textBlock.Text
		} else {
			cb.Semantic.Section = currentSection
		}

		// Check for list
		if textBlock.IsList {
			cb.Type = BlockTypeList
		}

		// Also check by analyzing the text block itself
		if !isHeading && se.detectHeading(textBlock) {
			cb.Type = BlockTypeHeading
			cb.Semantic.IsHeading = true
			cb.Semantic.HeadingLevel = se.detectHeadingLevel(textBlock)
			currentSection = textBlock.Text
		}

		if se.detectList(textBlock) {
			cb.Type = BlockTypeList
		}

		content.Blocks = append(content.Blocks, cb)

		// Process images in this paragraph
		for _, run := range para.Runs {
			for _, drawing := range run.Drawing {
				img := se.imageExt.ExtractFromDrawing(&drawing)
				if img != nil {
					blockID++
					imgBlock := se.createImageBlock(blockID, img, paraIndex, totalParas, pageCount)
					imgBlock.Semantic.Section = currentSection
					content.Blocks = append(content.Blocks, imgBlock)
				}
			}
		}

		paraIndex++
	}

	// Process tables
	for i := range docXML.Body.Tables {
		table := &docXML.Body.Tables[i]
		extracted := se.tableExt.ExtractTable(table)

		if extracted != nil && !extracted.IsEmpty() {
			blockID++
			cb := ContentBlock{
				ID:      formatBlockID(blockID),
				Type:    BlockTypeTable,
				Content: extracted.ToText(),
				Page:    estimatePage(paraIndex, totalParas, pageCount),
				BBox:    estimatePosition(paraIndex, totalParas, pageCount),
				Semantic: SemanticInfo{
					Section: currentSection,
				},
			}
			content.Blocks = append(content.Blocks, cb)
		}

		paraIndex += len(table.Rows)
	}

	// Extract keywords from content
	se.extractKeywords(content)

	return content, nil
}

// createContentBlock creates a ContentBlock from a TextBlock
func (se *SemanticExtractor) createContentBlock(id int, block *TextBlock, para *Paragraph, paraIndex, totalParas, pageCount int) ContentBlock {
	cb := ContentBlock{
		ID:      formatBlockID(id),
		Type:    BlockTypeText,
		Content: strings.TrimSpace(block.Text),
		Page:    estimatePage(paraIndex, totalParas, pageCount),
		BBox:    estimatePosition(paraIndex, totalParas, pageCount),
		Font: &FontInfo{
			Name:   block.FontName,
			Size:   block.FontSize,
			Bold:   block.Bold,
			Italic: block.Italic,
		},
		Semantic: SemanticInfo{},
	}

	return cb
}

// createImageBlock creates a ContentBlock for an image
func (se *SemanticExtractor) createImageBlock(id int, img *ExtractedImage, paraIndex, totalParas, pageCount int) ContentBlock {
	return ContentBlock{
		ID:      formatBlockID(id),
		Type:    BlockTypeImage,
		Content: img.Name, // Will be replaced with actual path when stored
		Page:    estimatePage(paraIndex, totalParas, pageCount),
		BBox: BoundingBox{
			X:          72, // Default margin
			Y:          0,
			Width:      img.Width,
			Height:     img.Height,
			PageWidth:  612,
			PageHeight: 792,
		},
		Semantic: SemanticInfo{},
	}
}

// detectHeading determines if a text block is a heading based on content analysis
func (se *SemanticExtractor) detectHeading(block *TextBlock) bool {
	text := strings.TrimSpace(block.Text)
	if len(text) == 0 {
		return false
	}

	// Large font size (14pt or more)
	if block.FontSize >= 14 {
		return true
	}

	// Bold font with reasonable length
	if block.Bold && len(text) < 200 {
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
func (se *SemanticExtractor) detectHeadingLevel(block *TextBlock) int {
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
	case block.FontSize >= 12 && block.Bold:
		return 5
	default:
		return 6
	}
}

// detectList determines if a text block is a list item
func (se *SemanticExtractor) detectList(block *TextBlock) bool {
	if block.IsList {
		return true
	}

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

	// Numbered list patterns
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

// estimatePage estimates the page number for a paragraph
func estimatePage(paraIndex, totalParas, pageCount int) int {
	if totalParas == 0 || pageCount == 0 {
		return 1
	}

	parasPerPage := totalParas / pageCount
	if parasPerPage == 0 {
		parasPerPage = 1
	}

	page := (paraIndex / parasPerPage) + 1
	if page > pageCount {
		page = pageCount
	}

	return page
}

// estimatePosition estimates position for a paragraph (DOCX lacks exact positions)
func estimatePosition(paraIndex, totalParas, pageCount int) BoundingBox {
	// Standard page dimensions (Letter size in points)
	const pageWidth = 612.0
	const pageHeight = 792.0
	const marginTop = 72.0
	const marginLeft = 72.0
	const lineHeight = 14.0

	usableHeight := pageHeight - marginTop*2
	parasPerPage := int(usableHeight / lineHeight)
	if parasPerPage == 0 {
		parasPerPage = 50
	}

	positionInPage := paraIndex % parasPerPage
	y := pageHeight - marginTop - (float64(positionInPage) * lineHeight)

	return BoundingBox{
		X:          marginLeft,
		Y:          y,
		Width:      pageWidth - 2*marginLeft,
		Height:     lineHeight,
		PageWidth:  pageWidth,
		PageHeight: pageHeight,
	}
}

// ExtractDocumentInfo extracts document metadata
func (se *SemanticExtractor) ExtractDocumentInfo() DocumentInfo {
	info := DocumentInfo{
		Format: FormatDOCX,
	}

	// Get page count
	pageCount, err := se.doc.PageCount()
	if err == nil {
		info.PageCount = pageCount
	}

	// Get metadata
	meta := se.doc.GetMetadata()
	if meta.Title != "" {
		info.Name = meta.Title
	}

	return info
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
