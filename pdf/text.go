package pdf

import (
	"sort"
	"strings"
	"unicode"
)

// TextBlock represents a block of text with position
type TextBlock struct {
	Text     string
	X        float64
	Y        float64
	Width    float64
	Height   float64
	FontName string
	FontSize float64
	Page     int
}

// TextLine represents a line of text (characters at approximately the same Y)
type TextLine struct {
	Text   string
	Y      float64
	Spans  []TextSpan
	MinX   float64
	MaxX   float64
}

// TextExtractor extracts text from PDF pages
type TextExtractor struct {
	doc    *Document
	spans  []TextSpan
	images []ImageRef
}

// NewTextExtractor creates a new text extractor
func NewTextExtractor(doc *Document) *TextExtractor {
	return &TextExtractor{
		doc: doc,
	}
}

// OnTextSpan implements ContentHandler
func (te *TextExtractor) OnTextSpan(span TextSpan) {
	te.spans = append(te.spans, span)
}

// OnImage implements ContentHandler
func (te *TextExtractor) OnImage(img ImageRef) {
	te.images = append(te.images, img)
}

// ExtractPage extracts text from a specific page
func (te *TextExtractor) ExtractPage(pageNum int) ([]TextBlock, error) {
	te.spans = nil
	te.images = nil

	page, err := te.doc.GetPage(pageNum)
	if err != nil {
		return nil, err
	}

	interpreter := NewContentInterpreter(page, te)
	if err := interpreter.Execute(); err != nil {
		return nil, err
	}

	// Convert spans to blocks
	return te.spansToBlocks(pageNum), nil
}

// ExtractPageText extracts text from a page as a single string
func (te *TextExtractor) ExtractPageText(pageNum int) (string, error) {
	blocks, err := te.ExtractPage(pageNum)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for i, block := range blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(block.Text)
	}
	return sb.String(), nil
}

// ExtractAllText extracts text from all pages
func (te *TextExtractor) ExtractAllText() (string, error) {
	pageCount, err := te.doc.PageCount()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	for i := 1; i <= pageCount; i++ {
		text, err := te.ExtractPageText(i)
		if err != nil {
			continue
		}
		if i > 1 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(text)
	}

	return sb.String(), nil
}

// ExtractPageBlocks extracts all text blocks from a page with full position info
func (te *TextExtractor) ExtractPageBlocks(pageNum int) ([]TextBlock, []ImageRef, error) {
	te.spans = nil
	te.images = nil

	page, err := te.doc.GetPage(pageNum)
	if err != nil {
		return nil, nil, err
	}

	interpreter := NewContentInterpreter(page, te)
	if err := interpreter.Execute(); err != nil {
		return nil, nil, err
	}

	return te.spansToBlocks(pageNum), te.images, nil
}

// spansToBlocks converts text spans into text blocks by grouping into lines
func (te *TextExtractor) spansToBlocks(pageNum int) []TextBlock {
	if len(te.spans) == 0 {
		return nil
	}

	// Group spans into lines (same Y coordinate within tolerance)
	lines := te.groupIntoLines(te.spans)

	// Sort lines by Y (top to bottom)
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].Y > lines[j].Y // Higher Y is earlier (PDF coords)
	})

	// Convert lines to blocks
	var blocks []TextBlock
	for _, line := range lines {
		if strings.TrimSpace(line.Text) == "" {
			continue
		}

		// Get font info from first span
		var fontName string
		var fontSize float64
		if len(line.Spans) > 0 {
			fontName = line.Spans[0].FontName
			fontSize = line.Spans[0].FontSize
		}

		blocks = append(blocks, TextBlock{
			Text:     line.Text,
			X:        line.MinX,
			Y:        line.Y,
			Width:    line.MaxX - line.MinX,
			Height:   fontSize,
			FontName: fontName,
			FontSize: fontSize,
			Page:     pageNum,
		})
	}

	return blocks
}

// getYTolerance calculates Y tolerance based on font size for line grouping
func getYTolerance(fontSize float64) float64 {
	// Use 20% of font size as tolerance, with a minimum of 2.0 points
	tolerance := fontSize * 0.2
	if tolerance < 2.0 {
		tolerance = 2.0
	}
	return tolerance
}

// groupIntoLines groups text spans into lines based on Y coordinate
func (te *TextExtractor) groupIntoLines(spans []TextSpan) []TextLine {
	if len(spans) == 0 {
		return nil
	}

	// Sort spans by Y (descending) then X (ascending)
	sorted := make([]TextSpan, len(spans))
	copy(sorted, spans)

	// Use average font size for initial tolerance calculation
	var totalFontSize float64
	for _, s := range sorted {
		totalFontSize += s.FontSize
	}
	avgFontSize := totalFontSize / float64(len(sorted))
	if avgFontSize < 8 {
		avgFontSize = 12 // Default
	}
	yTolerance := getYTolerance(avgFontSize)

	sort.Slice(sorted, func(i, j int) bool {
		if nearlyEqual(sorted[i].Y, sorted[j].Y, yTolerance) {
			return sorted[i].X < sorted[j].X
		}
		return sorted[i].Y > sorted[j].Y
	})

	var lines []TextLine
	var currentLine *TextLine

	for _, span := range sorted {
		if span.Text == "" {
			continue
		}

		// Use span-specific tolerance when available
		spanTolerance := yTolerance
		if span.FontSize > 0 {
			spanTolerance = getYTolerance(span.FontSize)
		}

		if currentLine == nil || !nearlyEqual(span.Y, currentLine.Y, spanTolerance) {
			// Start new line
			if currentLine != nil {
				currentLine.Text = te.buildLineText(currentLine.Spans)
				lines = append(lines, *currentLine)
			}
			currentLine = &TextLine{
				Y:     span.Y,
				MinX:  span.X,
				MaxX:  span.X + span.Width,
				Spans: []TextSpan{span},
			}
		} else {
			// Add to current line
			currentLine.Spans = append(currentLine.Spans, span)
			if span.X < currentLine.MinX {
				currentLine.MinX = span.X
			}
			if span.X+span.Width > currentLine.MaxX {
				currentLine.MaxX = span.X + span.Width
			}
		}
	}

	// Don't forget the last line
	if currentLine != nil {
		currentLine.Text = te.buildLineText(currentLine.Spans)
		lines = append(lines, *currentLine)
	}

	return lines
}

// buildLineText constructs the text for a line, adding spaces where appropriate
// TJ arrays emit multiple spans with adjustments between them.
//
// Strategy: Space characters in the PDF indicate word breaks. We detect where
// each space belongs by finding which pair of content spans it falls between.
func (te *TextExtractor) buildLineText(spans []TextSpan) string {
	if len(spans) == 0 {
		return ""
	}

	// Separate space spans from content spans first (before sorting)
	var contentSpans []TextSpan
	var spaceSpans []TextSpan

	for _, span := range spans {
		text := strings.TrimSpace(span.Text)
		if text == "" {
			spaceSpans = append(spaceSpans, span)
		} else {
			contentSpans = append(contentSpans, span)
		}
	}

	// Sort content spans by stream order (SeqNum)
	// PDF creators write text in reading order. Position coordinates are used for
	// fine-tuning (kerning, spacing) but don't indicate reading order.
	// The stream order preserves the correct reading sequence.
	sort.Slice(contentSpans, func(i, j int) bool {
		return contentSpans[i].SeqNum < contentSpans[j].SeqNum
	})

	// For each space span, find which content span it should come AFTER
	// by looking at its SeqNum relative to content spans
	spaceAfter := make(map[int]bool) // indices after which to insert space

	for _, space := range spaceSpans {
		// Find the content span whose SeqNum is closest to (but less than) the space's SeqNum
		bestIdx := -1
		for i, cs := range contentSpans {
			if cs.SeqNum < space.SeqNum {
				bestIdx = i
			}
		}
		if bestIdx >= 0 && bestIdx < len(contentSpans)-1 {
			spaceAfter[bestIdx] = true
		}
	}

	var sb strings.Builder

	for i, span := range contentSpans {
		if i > 0 {
			// Check if there was an explicit space between these spans
			needSpace := spaceAfter[i-1]

			if needSpace {
				prevText := sb.String()
				if !strings.HasSuffix(prevText, " ") && !strings.HasSuffix(span.Text, " ") {
					sb.WriteString(" ")
				}
			}
		}
		sb.WriteString(span.Text)
	}

	return sb.String()
}

// estimateCharWidth estimates the average character width for a span
func estimateCharWidth(span TextSpan) float64 {
	if len(span.Text) == 0 {
		return span.FontSize * 0.5
	}
	return span.Width / float64(len(span.Text))
}

// nearlyEqual checks if two floats are nearly equal within tolerance
func nearlyEqual(a, b, tolerance float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= tolerance
}

// ExtractStructuredText extracts text with paragraph detection
func (te *TextExtractor) ExtractStructuredText(pageNum int) ([]Paragraph, error) {
	blocks, err := te.ExtractPage(pageNum)
	if err != nil {
		return nil, err
	}

	return te.blocksToParag(blocks), nil
}

// ExtractStructuredTextWithImages extracts paragraphs and images from a page
func (te *TextExtractor) ExtractStructuredTextWithImages(pageNum int) ([]Paragraph, []ImageRef, error) {
	te.spans = nil
	te.images = nil

	page, err := te.doc.GetPage(pageNum)
	if err != nil {
		return nil, nil, err
	}

	interpreter := NewContentInterpreter(page, te)
	if err := interpreter.Execute(); err != nil {
		return nil, nil, err
	}

	// Convert spans to blocks, then blocks to paragraphs
	blocks := te.spansToBlocks(pageNum)
	paragraphs := te.blocksToParag(blocks)

	return paragraphs, te.images, nil
}

// Paragraph represents a paragraph of text
type Paragraph struct {
	Text      string
	Lines     []string
	X         float64
	Y         float64
	Width     float64
	Height    float64
	FontSize  float64
	FontName  string
	IsHeading bool
	// Bounding box fields for precise positioning
	MinX float64
	MaxX float64
	MinY float64 // Bottom of paragraph (lowest Y)
	MaxY float64 // Top of paragraph (highest Y)
}

// blocksToParag groups text blocks into paragraphs
func (te *TextExtractor) blocksToParag(blocks []TextBlock) []Paragraph {
	if len(blocks) == 0 {
		return nil
	}

	var paragraphs []Paragraph
	var currentPara *Paragraph
	var lastY float64
	var lastFontSize float64

	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}

		// Detect paragraph breaks
		lineGap := lastY - block.Y
		fontChanged := lastFontSize > 0 && !nearlyEqual(block.FontSize, lastFontSize, 1.0)
		bigGap := lineGap > block.FontSize*2

		if currentPara == nil || fontChanged || bigGap {
			// Start new paragraph
			if currentPara != nil {
				currentPara.Text = strings.Join(currentPara.Lines, " ")
				// Calculate final width and height from bounding box
				currentPara.Width = currentPara.MaxX - currentPara.MinX
				currentPara.Height = currentPara.MaxY - currentPara.MinY
				paragraphs = append(paragraphs, *currentPara)
			}

			isHeading := te.detectHeading(block)

			currentPara = &Paragraph{
				Lines:     []string{text},
				X:         block.X,
				Y:         block.Y,
				FontSize:  block.FontSize,
				FontName:  block.FontName,
				IsHeading: isHeading,
				// Initialize bounding box from first block
				MinX: block.X,
				MaxX: block.X + block.Width,
				MinY: block.Y,
				MaxY: block.Y + block.Height,
			}
		} else {
			// Continue paragraph - add line and update bounding box
			currentPara.Lines = append(currentPara.Lines, text)

			// Update bounding box to encompass this block
			if block.X < currentPara.MinX {
				currentPara.MinX = block.X
			}
			if block.X+block.Width > currentPara.MaxX {
				currentPara.MaxX = block.X + block.Width
			}
			if block.Y < currentPara.MinY {
				currentPara.MinY = block.Y
			}
			if block.Y+block.Height > currentPara.MaxY {
				currentPara.MaxY = block.Y + block.Height
			}
		}

		lastY = block.Y
		lastFontSize = block.FontSize
	}

	// Don't forget the last paragraph
	if currentPara != nil {
		currentPara.Text = strings.Join(currentPara.Lines, " ")
		// Calculate final width and height from bounding box
		currentPara.Width = currentPara.MaxX - currentPara.MinX
		currentPara.Height = currentPara.MaxY - currentPara.MinY
		paragraphs = append(paragraphs, *currentPara)
	}

	return paragraphs
}

// detectHeading checks if a block looks like a heading
func (te *TextExtractor) detectHeading(block TextBlock) bool {
	// Larger font size suggests heading
	if block.FontSize > 14 {
		return true
	}

	// Bold font suggests heading
	fontName := strings.ToLower(block.FontName)
	if strings.Contains(fontName, "bold") {
		return true
	}

	// Short line with all caps might be heading
	text := strings.TrimSpace(block.Text)
	if len(text) < 100 && len(text) > 0 {
		allCaps := true
		for _, r := range text {
			if unicode.IsLetter(r) && !unicode.IsUpper(r) {
				allCaps = false
				break
			}
		}
		if allCaps && len(text) > 3 {
			return true
		}
	}

	return false
}

// GetPageImages returns images from a page
func (te *TextExtractor) GetPageImages(pageNum int) ([]ImageRef, error) {
	te.spans = nil
	te.images = nil

	page, err := te.doc.GetPage(pageNum)
	if err != nil {
		return nil, err
	}

	interpreter := NewContentInterpreter(page, te)
	if err := interpreter.Execute(); err != nil {
		return nil, err
	}

	return te.images, nil
}
