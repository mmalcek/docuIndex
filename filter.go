package docuindex

import (
	"time"
)

// Filter provides a fluent API for building search filters
type Filter struct {
	sources       []string
	formats       []string
	tags          map[string]string
	dateRange     *DateRange
	minPageCount  int
	maxPageCount  int
	hasEmbeddings *bool
	externalIDs   []string
}

// NewFilter creates a new empty filter
func NewFilter() *Filter {
	return &Filter{
		tags: make(map[string]string),
	}
}

// Sources filters by source identifiers (e.g., "crm", "faq")
func (f *Filter) Sources(sources ...string) *Filter {
	f.sources = append(f.sources, sources...)
	return f
}

// Formats filters by document format (e.g., "pdf", "docx", "customdata")
func (f *Filter) Formats(formats ...string) *Filter {
	f.formats = append(f.formats, formats...)
	return f
}

// Tags filters by multiple tags (AND logic - all must match)
func (f *Filter) Tags(tags map[string]string) *Filter {
	for k, v := range tags {
		f.tags[k] = v
	}
	return f
}

// Tag adds a single tag filter
func (f *Filter) Tag(key, value string) *Filter {
	f.tags[key] = value
	return f
}

// DateRange filters documents created/imported within a time range
func (f *Filter) DateRange(start, end time.Time) *Filter {
	f.dateRange = &DateRange{Start: start, End: end}
	return f
}

// After filters documents created/imported after the given time
func (f *Filter) After(t time.Time) *Filter {
	if f.dateRange == nil {
		f.dateRange = &DateRange{}
	}
	f.dateRange.Start = t
	return f
}

// Before filters documents created/imported before the given time
func (f *Filter) Before(t time.Time) *Filter {
	if f.dateRange == nil {
		f.dateRange = &DateRange{}
	}
	f.dateRange.End = t
	return f
}

// MinPages filters documents with at least n pages
func (f *Filter) MinPages(n int) *Filter {
	f.minPageCount = n
	return f
}

// MaxPages filters documents with at most n pages
func (f *Filter) MaxPages(n int) *Filter {
	f.maxPageCount = n
	return f
}

// HasEmbeddings filters documents that have (or don't have) embeddings
func (f *Filter) HasEmbeddings(has bool) *Filter {
	f.hasEmbeddings = &has
	return f
}

// ExternalIDs filters by external identifiers
func (f *Filter) ExternalIDs(ids ...string) *Filter {
	f.externalIDs = append(f.externalIDs, ids...)
	return f
}

// IsEmpty returns true if no filters are set
func (f *Filter) IsEmpty() bool {
	return len(f.sources) == 0 &&
		len(f.formats) == 0 &&
		len(f.tags) == 0 &&
		f.dateRange == nil &&
		f.minPageCount == 0 &&
		f.maxPageCount == 0 &&
		f.hasEmbeddings == nil &&
		len(f.externalIDs) == 0
}

// FilterConfig is the internal representation used by search
type FilterConfig struct {
	Sources       []string
	Formats       []string
	Tags          map[string]string
	DateStart     time.Time
	DateEnd       time.Time
	MinPageCount  int
	MaxPageCount  int
	HasEmbeddings *bool
	ExternalIDs   []string
}

// Build converts the Filter to FilterConfig for internal use
func (f *Filter) Build() *FilterConfig {
	if f == nil {
		return nil
	}

	cfg := &FilterConfig{
		Sources:       f.sources,
		Formats:       f.formats,
		Tags:          f.tags,
		MinPageCount:  f.minPageCount,
		MaxPageCount:  f.maxPageCount,
		HasEmbeddings: f.hasEmbeddings,
		ExternalIDs:   f.externalIDs,
	}

	if f.dateRange != nil {
		cfg.DateStart = f.dateRange.Start
		cfg.DateEnd = f.dateRange.End
	}

	return cfg
}

// GetSources returns the source filters
func (f *Filter) GetSources() []string {
	return f.sources
}

// GetFormats returns the format filters
func (f *Filter) GetFormats() []string {
	return f.formats
}

// GetTags returns the tag filters
func (f *Filter) GetTags() map[string]string {
	return f.tags
}

// GetDateRange returns the date range filter
func (f *Filter) GetDateRange() *DateRange {
	return f.dateRange
}

// GetMinPageCount returns the minimum page count filter
func (f *Filter) GetMinPageCount() int {
	return f.minPageCount
}

// GetMaxPageCount returns the maximum page count filter
func (f *Filter) GetMaxPageCount() int {
	return f.maxPageCount
}

// GetHasEmbeddings returns the embeddings filter
func (f *Filter) GetHasEmbeddings() *bool {
	return f.hasEmbeddings
}

// GetExternalIDs returns the external ID filters
func (f *Filter) GetExternalIDs() []string {
	return f.externalIDs
}
