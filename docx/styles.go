package docx

import (
	"strconv"
	"strings"
)

// StyleResolver resolves styles with inheritance
type StyleResolver struct {
	styles   map[string]*Style
	defaults *DocDefaults
}

// ResolvedStyle contains fully resolved style properties
type ResolvedStyle struct {
	StyleID      string
	StyleName    string
	IsHeading    bool
	HeadingLevel int
	FontName     string
	FontSize     float64 // in points
	Bold         bool
	Italic       bool
	Underline    bool
}

// NewStyleResolver creates a style resolver from parsed styles
func NewStyleResolver(styles *StylesXML) *StyleResolver {
	r := &StyleResolver{
		styles: make(map[string]*Style),
	}

	if styles != nil {
		r.defaults = styles.DocDefaults

		for i := range styles.Styles {
			style := &styles.Styles[i]
			r.styles[style.StyleID] = style
		}
	}

	return r
}

// Resolve resolves a style by ID, following inheritance chain
func (r *StyleResolver) Resolve(styleID string) *ResolvedStyle {
	result := &ResolvedStyle{
		StyleID:  styleID,
		FontSize: 11, // Default Word font size
		FontName: "Calibri", // Default Word font
	}

	if styleID == "" {
		return result
	}

	// Apply defaults first
	r.applyDefaults(result)

	// Build inheritance chain (child to parent)
	chain := r.buildInheritanceChain(styleID)

	// Apply styles from parent to child (so child overrides parent)
	for i := len(chain) - 1; i >= 0; i-- {
		r.applyStyle(result, chain[i])
	}

	// Check if this is a heading style
	r.detectHeading(result, styleID)

	return result
}

// buildInheritanceChain builds the style inheritance chain
func (r *StyleResolver) buildInheritanceChain(styleID string) []*Style {
	var chain []*Style
	visited := make(map[string]bool)

	current := styleID
	for current != "" && !visited[current] {
		visited[current] = true
		style := r.styles[current]
		if style == nil {
			break
		}
		chain = append(chain, style)
		if style.BasedOn != nil {
			current = style.BasedOn.Val
		} else {
			break
		}
	}

	return chain
}

// applyDefaults applies document defaults to the result
func (r *StyleResolver) applyDefaults(result *ResolvedStyle) {
	if r.defaults == nil {
		return
	}

	// Apply default run properties
	if r.defaults.RPrDefault != nil && r.defaults.RPrDefault.RPr != nil {
		rPr := r.defaults.RPrDefault.RPr
		r.applyRunProps(result, rPr)
	}
}

// applyStyle applies a style's properties to the result
func (r *StyleResolver) applyStyle(result *ResolvedStyle, style *Style) {
	if style.Name != nil {
		result.StyleName = style.Name.Val
	}

	// Apply run properties from style
	if style.RPr != nil {
		r.applyRunProps(result, style.RPr)
	}

	// Apply run properties from paragraph properties
	if style.PPr != nil && style.PPr.RunProps != nil {
		r.applyRunProps(result, style.PPr.RunProps)
	}
}

// applyRunProps applies run properties to the result
func (r *StyleResolver) applyRunProps(result *ResolvedStyle, rPr *RunProperties) {
	if rPr == nil {
		return
	}

	if rPr.Bold != nil {
		result.Bold = true
	}

	if rPr.Italic != nil {
		result.Italic = true
	}

	if rPr.Underline != nil {
		result.Underline = true
	}

	if rPr.Size != nil {
		// Size is in half-points, convert to points
		if size, err := strconv.ParseFloat(rPr.Size.Val, 64); err == nil {
			result.FontSize = size / 2.0
		}
	}

	if rPr.Font != nil {
		if rPr.Font.ASCII != "" {
			result.FontName = rPr.Font.ASCII
		} else if rPr.Font.HAnsi != "" {
			result.FontName = rPr.Font.HAnsi
		}
	}
}

// detectHeading checks if a style is a heading and sets level
func (r *StyleResolver) detectHeading(result *ResolvedStyle, styleID string) {
	// Check by styleID pattern (Heading1, Heading2, etc.)
	if strings.HasPrefix(styleID, "Heading") {
		result.IsHeading = true
		levelStr := strings.TrimPrefix(styleID, "Heading")
		if level, err := strconv.Atoi(levelStr); err == nil && level >= 1 && level <= 9 {
			result.HeadingLevel = level
			return
		}
	}

	// Check by style name
	styleName := strings.ToLower(result.StyleName)
	if strings.Contains(styleName, "heading") {
		result.IsHeading = true

		// Try to extract level from name (e.g., "Heading 1", "heading 2")
		for i := 1; i <= 9; i++ {
			if strings.Contains(styleName, strconv.Itoa(i)) {
				result.HeadingLevel = i
				return
			}
		}

		// Default to level 1 if no specific level found
		result.HeadingLevel = 1
	}

	// Check for Title style
	if styleID == "Title" || strings.ToLower(result.StyleName) == "title" {
		result.IsHeading = true
		result.HeadingLevel = 1
	}

	// Check for Subtitle style
	if styleID == "Subtitle" || strings.ToLower(result.StyleName) == "subtitle" {
		result.IsHeading = true
		result.HeadingLevel = 2
	}
}

// IsHeadingStyle checks if a style ID is a heading style
func (r *StyleResolver) IsHeadingStyle(styleID string) (bool, int) {
	resolved := r.Resolve(styleID)
	return resolved.IsHeading, resolved.HeadingLevel
}

// GetStyle returns the raw style definition by ID
func (r *StyleResolver) GetStyle(styleID string) *Style {
	return r.styles[styleID]
}

// GetDefaultFontSize returns the default font size in points
func (r *StyleResolver) GetDefaultFontSize() float64 {
	if r.defaults != nil && r.defaults.RPrDefault != nil && r.defaults.RPrDefault.RPr != nil {
		if r.defaults.RPrDefault.RPr.Size != nil {
			if size, err := strconv.ParseFloat(r.defaults.RPrDefault.RPr.Size.Val, 64); err == nil {
				return size / 2.0
			}
		}
	}
	return 11.0 // Word default
}

// GetDefaultFontName returns the default font name
func (r *StyleResolver) GetDefaultFontName() string {
	if r.defaults != nil && r.defaults.RPrDefault != nil && r.defaults.RPrDefault.RPr != nil {
		if r.defaults.RPrDefault.RPr.Font != nil {
			if r.defaults.RPrDefault.RPr.Font.ASCII != "" {
				return r.defaults.RPrDefault.RPr.Font.ASCII
			}
		}
	}
	return "Calibri" // Word default
}

// ResolveRunProperties resolves run properties with style fallback
func (r *StyleResolver) ResolveRunProperties(styleID string, rPr *RunProperties) *ResolvedStyle {
	// Start with style resolution
	result := r.Resolve(styleID)

	// Override with direct run properties
	if rPr != nil {
		if rPr.Bold != nil {
			result.Bold = true
		}

		if rPr.Italic != nil {
			result.Italic = true
		}

		if rPr.Underline != nil {
			result.Underline = true
		}

		if rPr.Size != nil {
			if size, err := strconv.ParseFloat(rPr.Size.Val, 64); err == nil {
				result.FontSize = size / 2.0
			}
		}

		if rPr.Font != nil {
			if rPr.Font.ASCII != "" {
				result.FontName = rPr.Font.ASCII
			} else if rPr.Font.HAnsi != "" {
				result.FontName = rPr.Font.HAnsi
			}
		}

		// Check for nested style reference
		if rPr.Style != nil && rPr.Style.Val != "" {
			charStyle := r.Resolve(rPr.Style.Val)
			if charStyle.Bold {
				result.Bold = true
			}
			if charStyle.Italic {
				result.Italic = true
			}
		}
	}

	return result
}

// ListAllStyles returns all defined styles
func (r *StyleResolver) ListAllStyles() []*Style {
	result := make([]*Style, 0, len(r.styles))
	for _, style := range r.styles {
		result = append(result, style)
	}
	return result
}

// GetHeadingStyles returns all heading styles
func (r *StyleResolver) GetHeadingStyles() []*Style {
	var result []*Style
	for _, style := range r.styles {
		if isHeading, _ := r.IsHeadingStyle(style.StyleID); isHeading {
			result = append(result, style)
		}
	}
	return result
}
