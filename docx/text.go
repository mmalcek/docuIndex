package docx

import (
	"strconv"
	"strings"
)

// TextExtractor extracts text content from DOCX paragraphs
type TextExtractor struct {
	doc           *Document
	styleResolver *StyleResolver
	numbering     *NumberingResolver
	rels          *RelationshipResolver
}

// NewTextExtractor creates a new text extractor
func NewTextExtractor(doc *Document) (*TextExtractor, error) {
	te := &TextExtractor{
		doc: doc,
	}

	// Load styles
	styles, err := doc.GetStyles()
	if err != nil {
		return nil, err
	}
	te.styleResolver = NewStyleResolver(styles)

	// Load numbering
	numbering, err := doc.GetNumbering()
	if err != nil {
		return nil, err
	}
	te.numbering = NewNumberingResolver(numbering)

	// Load relationships
	docRels, err := doc.GetDocumentRelationships()
	if err != nil {
		return nil, err
	}
	te.rels = NewRelationshipResolver(docRels)

	return te, nil
}

// ExtractParagraph extracts text and formatting from a paragraph
func (te *TextExtractor) ExtractParagraph(para *Paragraph) *TextBlock {
	if para == nil {
		return nil
	}

	block := &TextBlock{}

	// Get paragraph style
	var styleID string
	if para.Properties != nil && para.Properties.Style != nil {
		styleID = para.Properties.Style.Val
		block.StyleID = styleID
	}

	// Check for list
	if para.Properties != nil && te.numbering != nil {
		listInfo := te.numbering.GetParagraphListInfo(para.Properties)
		if listInfo != nil {
			block.IsList = true
			block.ListLevel = listInfo.Level
			block.ListType = listInfo.NumFmt
		}
	}

	// Extract text from runs
	var textParts []string
	var dominantStyle *ResolvedStyle

	for _, run := range para.Runs {
		runText := te.extractRunText(&run)
		if runText != "" {
			textParts = append(textParts, runText)

			// Get run style (use first run with actual text for dominant style)
			if dominantStyle == nil {
				var runStyleID string
				var runProps *RunProperties

				if run.Properties != nil {
					runProps = run.Properties
					if run.Properties.Style != nil {
						runStyleID = run.Properties.Style.Val
					}
				}

				// Merge paragraph style with run properties
				if styleID != "" || runStyleID != "" {
					effectiveStyleID := runStyleID
					if effectiveStyleID == "" {
						effectiveStyleID = styleID
					}
					dominantStyle = te.styleResolver.ResolveRunProperties(effectiveStyleID, runProps)
				} else if runProps != nil {
					dominantStyle = te.styleResolver.ResolveRunProperties("", runProps)
				}
			}
		}
	}

	// Also extract text from hyperlinks
	for _, hyperlink := range para.Hyperlinks {
		for _, run := range hyperlink.Runs {
			runText := te.extractRunText(&run)
			if runText != "" {
				textParts = append(textParts, runText)
			}
		}
	}

	block.Text = strings.Join(textParts, "")

	// Apply style information
	if dominantStyle != nil {
		block.FontName = dominantStyle.FontName
		block.FontSize = dominantStyle.FontSize
		block.Bold = dominantStyle.Bold
		block.Italic = dominantStyle.Italic
	} else {
		// Use defaults
		block.FontName = te.styleResolver.GetDefaultFontName()
		block.FontSize = te.styleResolver.GetDefaultFontSize()
	}

	return block
}

// extractRunText extracts text content from a run
func (te *TextExtractor) extractRunText(run *Run) string {
	if run == nil {
		return ""
	}

	var parts []string

	// Process all child elements in order
	for _, t := range run.Text {
		parts = append(parts, t.Content)
	}

	// Handle tabs
	for range run.Tab {
		parts = append(parts, "\t")
	}

	// Handle breaks
	for _, br := range run.Break {
		if br.Type == "page" {
			parts = append(parts, "\n\n")
		} else {
			parts = append(parts, "\n")
		}
	}

	return strings.Join(parts, "")
}

// ExtractAllParagraphs extracts all paragraphs from the document
func (te *TextExtractor) ExtractAllParagraphs() ([]*TextBlock, error) {
	doc, err := te.doc.GetDocument()
	if err != nil {
		return nil, err
	}

	var blocks []*TextBlock

	for i := range doc.Body.Paragraphs {
		block := te.ExtractParagraph(&doc.Body.Paragraphs[i])
		if block != nil && strings.TrimSpace(block.Text) != "" {
			blocks = append(blocks, block)
		}
	}

	return blocks, nil
}

// ExtractTableText extracts text from a table as a single block
func (te *TextExtractor) ExtractTableText(table *Table) *TextBlock {
	if table == nil || len(table.Rows) == 0 {
		return nil
	}

	var rows []string

	for _, row := range table.Rows {
		var cells []string
		for _, cell := range row.Cells {
			var cellText []string
			for i := range cell.Paragraphs {
				block := te.ExtractParagraph(&cell.Paragraphs[i])
				if block != nil && block.Text != "" {
					cellText = append(cellText, block.Text)
				}
			}
			cells = append(cells, strings.Join(cellText, " "))
		}
		rows = append(rows, strings.Join(cells, " | "))
	}

	return &TextBlock{
		Text:     strings.Join(rows, "\n"),
		FontName: te.styleResolver.GetDefaultFontName(),
		FontSize: te.styleResolver.GetDefaultFontSize(),
	}
}

// GetStyleResolver returns the style resolver
func (te *TextExtractor) GetStyleResolver() *StyleResolver {
	return te.styleResolver
}

// GetNumberingResolver returns the numbering resolver
func (te *TextExtractor) GetNumberingResolver() *NumberingResolver {
	return te.numbering
}

// GetRelationshipResolver returns the relationship resolver
func (te *TextExtractor) GetRelationshipResolver() *RelationshipResolver {
	return te.rels
}

// IsHeadingParagraph checks if a paragraph is a heading
func (te *TextExtractor) IsHeadingParagraph(para *Paragraph) (bool, int) {
	if para == nil || para.Properties == nil {
		return false, 0
	}

	// Check paragraph style
	if para.Properties.Style != nil {
		if isHeading, level := te.styleResolver.IsHeadingStyle(para.Properties.Style.Val); isHeading {
			return true, level
		}
	}

	// Check outline level
	if para.Properties.OutlineLvl != nil {
		if level, err := strconv.Atoi(para.Properties.OutlineLvl.Val); err == nil {
			return true, level + 1 // OutlineLvl is 0-indexed, heading levels are 1-indexed
		}
	}

	return false, 0
}

// HasImages checks if a paragraph contains images
func (te *TextExtractor) HasImages(para *Paragraph) bool {
	if para == nil {
		return false
	}

	for _, run := range para.Runs {
		if len(run.Drawing) > 0 {
			return true
		}
	}

	return false
}

// GetParagraphImages returns all image references from a paragraph
func (te *TextExtractor) GetParagraphImages(para *Paragraph) []string {
	if para == nil {
		return nil
	}

	var images []string

	for _, run := range para.Runs {
		for _, drawing := range run.Drawing {
			relID := te.getDrawingRelID(&drawing)
			if relID != "" {
				imagePath := te.rels.GetImagePath(relID)
				if imagePath != "" {
					images = append(images, imagePath)
				}
			}
		}
	}

	return images
}

// getDrawingRelID extracts the relationship ID from a drawing
func (te *TextExtractor) getDrawingRelID(drawing *Drawing) string {
	if drawing == nil {
		return ""
	}

	// Try inline
	if drawing.Inline != nil && drawing.Inline.Graphic != nil &&
		drawing.Inline.Graphic.GraphicData != nil &&
		drawing.Inline.Graphic.GraphicData.Pic != nil &&
		drawing.Inline.Graphic.GraphicData.Pic.BlipFill != nil &&
		drawing.Inline.Graphic.GraphicData.Pic.BlipFill.Blip != nil {
		return drawing.Inline.Graphic.GraphicData.Pic.BlipFill.Blip.Embed
	}

	// Try anchor
	if drawing.Anchor != nil && drawing.Anchor.Graphic != nil &&
		drawing.Anchor.Graphic.GraphicData != nil &&
		drawing.Anchor.Graphic.GraphicData.Pic != nil &&
		drawing.Anchor.Graphic.GraphicData.Pic.BlipFill != nil &&
		drawing.Anchor.Graphic.GraphicData.Pic.BlipFill.Blip != nil {
		return drawing.Anchor.Graphic.GraphicData.Pic.BlipFill.Blip.Embed
	}

	return ""
}
