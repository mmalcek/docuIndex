package docx

import (
	"time"
)

// ExtractMetadata extracts all metadata from a DOCX document
func ExtractMetadata(doc *Document) *Metadata {
	meta := &Metadata{}

	// Get core properties
	if core, err := doc.GetCoreProperties(); err == nil && core != nil {
		meta.Title = core.Title
		meta.Author = core.Creator
		meta.Subject = core.Subject
		meta.Keywords = core.Keywords
		meta.Creator = core.Creator
		meta.Created = core.Created
		meta.Modified = core.Modified
	}

	// Get app properties
	if app, err := doc.GetAppProperties(); err == nil && app != nil {
		meta.PageCount = app.Pages
		meta.WordCount = app.Words
	}

	return meta
}

// ParseCreatedDate parses the created date from metadata
func (m *Metadata) ParseCreatedDate() (time.Time, error) {
	if m.Created == "" {
		return time.Time{}, nil
	}
	return parseDocxDate(m.Created)
}

// ParseModifiedDate parses the modified date from metadata
func (m *Metadata) ParseModifiedDate() (time.Time, error) {
	if m.Modified == "" {
		return time.Time{}, nil
	}
	return parseDocxDate(m.Modified)
}

// parseDocxDate parses various date formats used in DOCX metadata
func parseDocxDate(s string) (time.Time, error) {
	// DOCX typically uses ISO 8601 format: 2024-01-15T10:30:00Z
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02",
	}

	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}

	return time.Time{}, nil
}

// GetTitle returns the document title, or filename if no title
func (m *Metadata) GetTitle() string {
	if m.Title != "" {
		return m.Title
	}
	return ""
}

// GetAuthor returns the document author
func (m *Metadata) GetAuthor() string {
	if m.Author != "" {
		return m.Author
	}
	return m.Creator
}

// IsEmpty checks if metadata is essentially empty
func (m *Metadata) IsEmpty() bool {
	return m.Title == "" && m.Author == "" && m.Subject == "" &&
		m.Keywords == "" && m.PageCount == 0
}
