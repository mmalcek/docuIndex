package docx

import (
	"strings"
)

// TableExtractor extracts table content from DOCX
type TableExtractor struct {
	textExt *TextExtractor
}

// ExtractedTable represents an extracted table
type ExtractedTable struct {
	Rows      [][]string // Cell content as strings
	RowCount  int
	ColCount  int
	HasHeader bool
}

// NewTableExtractor creates a new table extractor
func NewTableExtractor(textExt *TextExtractor) *TableExtractor {
	return &TableExtractor{
		textExt: textExt,
	}
}

// ExtractTable extracts content from a table
func (te *TableExtractor) ExtractTable(table *Table) *ExtractedTable {
	if table == nil || len(table.Rows) == 0 {
		return nil
	}

	extracted := &ExtractedTable{
		RowCount: len(table.Rows),
	}

	// Determine max columns
	for _, row := range table.Rows {
		if len(row.Cells) > extracted.ColCount {
			extracted.ColCount = len(row.Cells)
		}
	}

	// Extract cell content
	for i, row := range table.Rows {
		var rowData []string

		// Check if this is a header row
		if i == 0 && row.Properties != nil && row.Properties.Header != nil {
			extracted.HasHeader = true
		}

		for _, cell := range row.Cells {
			cellText := te.extractCellText(&cell)
			rowData = append(rowData, cellText)
		}

		// Pad row to consistent column count
		for len(rowData) < extracted.ColCount {
			rowData = append(rowData, "")
		}

		extracted.Rows = append(extracted.Rows, rowData)
	}

	return extracted
}

// extractCellText extracts text from a table cell
func (te *TableExtractor) extractCellText(cell *TableCell) string {
	if cell == nil {
		return ""
	}

	var paragraphs []string

	for i := range cell.Paragraphs {
		block := te.textExt.ExtractParagraph(&cell.Paragraphs[i])
		if block != nil && strings.TrimSpace(block.Text) != "" {
			paragraphs = append(paragraphs, strings.TrimSpace(block.Text))
		}
	}

	return strings.Join(paragraphs, " ")
}

// ToMarkdown converts a table to markdown format
func (et *ExtractedTable) ToMarkdown() string {
	if et == nil || len(et.Rows) == 0 {
		return ""
	}

	var lines []string

	for i, row := range et.Rows {
		line := "| " + strings.Join(row, " | ") + " |"
		lines = append(lines, line)

		// Add separator after header row
		if i == 0 {
			var sep []string
			for range row {
				sep = append(sep, "---")
			}
			lines = append(lines, "| "+strings.Join(sep, " | ")+" |")
		}
	}

	return strings.Join(lines, "\n")
}

// ToText converts a table to plain text format
func (et *ExtractedTable) ToText() string {
	if et == nil || len(et.Rows) == 0 {
		return ""
	}

	var lines []string

	for _, row := range et.Rows {
		lines = append(lines, strings.Join(row, " | "))
	}

	return strings.Join(lines, "\n")
}

// GetHeaders returns the header row if present
func (et *ExtractedTable) GetHeaders() []string {
	if et == nil || len(et.Rows) == 0 {
		return nil
	}
	return et.Rows[0]
}

// GetDataRows returns all non-header rows
func (et *ExtractedTable) GetDataRows() [][]string {
	if et == nil || len(et.Rows) <= 1 {
		return nil
	}
	return et.Rows[1:]
}

// IsEmpty checks if the table has no content
func (et *ExtractedTable) IsEmpty() bool {
	if et == nil || len(et.Rows) == 0 {
		return true
	}

	for _, row := range et.Rows {
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				return false
			}
		}
	}

	return true
}
