package docx

import (
	"strconv"
)

// NumberingResolver resolves numbering/list definitions
type NumberingResolver struct {
	abstractNums map[string]*AbstractNum
	nums         map[string]*Num
}

// ListInfo contains information about a list item
type ListInfo struct {
	IsListItem bool
	Level      int
	NumFmt     string // decimal, bullet, lowerLetter, upperLetter, lowerRoman, upperRoman
	LvlText    string // Format pattern like "%1." or "%1.%2"
	IsBullet   bool
}

// NewNumberingResolver creates a numbering resolver from parsed numbering
func NewNumberingResolver(numbering *NumberingXML) *NumberingResolver {
	r := &NumberingResolver{
		abstractNums: make(map[string]*AbstractNum),
		nums:         make(map[string]*Num),
	}

	if numbering != nil {
		for i := range numbering.AbstractNums {
			an := &numbering.AbstractNums[i]
			r.abstractNums[an.AbstractNumID] = an
		}

		for i := range numbering.Nums {
			num := &numbering.Nums[i]
			r.nums[num.NumID] = num
		}
	}

	return r
}

// GetListInfo returns list information for a numId and indent level
func (r *NumberingResolver) GetListInfo(numID string, ilvl int) *ListInfo {
	if numID == "" || numID == "0" {
		return nil
	}

	// Get the num definition
	num := r.nums[numID]
	if num == nil {
		return nil
	}

	// Get the abstract num
	if num.AbstractNumID == nil {
		return nil
	}

	abstractNum := r.abstractNums[num.AbstractNumID.Val]
	if abstractNum == nil {
		return nil
	}

	// Find the level
	var level *Level
	for i := range abstractNum.Levels {
		lvl := &abstractNum.Levels[i]
		if lvlNum, err := strconv.Atoi(lvl.ILvl); err == nil && lvlNum == ilvl {
			level = lvl
			break
		}
	}

	if level == nil && len(abstractNum.Levels) > 0 {
		// Use first level if exact match not found
		level = &abstractNum.Levels[0]
	}

	if level == nil {
		return &ListInfo{
			IsListItem: true,
			Level:      ilvl,
			NumFmt:     "decimal",
			IsBullet:   false,
		}
	}

	info := &ListInfo{
		IsListItem: true,
		Level:      ilvl,
	}

	if level.NumFmt != nil {
		info.NumFmt = level.NumFmt.Val
	}

	if level.LvlText != nil {
		info.LvlText = level.LvlText.Val
	}

	// Determine if it's a bullet
	info.IsBullet = isBulletFormat(info.NumFmt)

	return info
}

// isBulletFormat checks if a numFmt indicates a bullet list
func isBulletFormat(numFmt string) bool {
	switch numFmt {
	case "bullet":
		return true
	case "none":
		return true // Treat "none" as bullet for display purposes
	default:
		return false
	}
}

// IsListParagraph checks if paragraph properties indicate a list item
func (r *NumberingResolver) IsListParagraph(pPr *ParagraphProperties) bool {
	if pPr == nil || pPr.NumPr == nil {
		return false
	}

	if pPr.NumPr.NumID == nil {
		return false
	}

	numID := pPr.NumPr.NumID.Val
	return numID != "" && numID != "0"
}

// GetParagraphListInfo returns list info for a paragraph
func (r *NumberingResolver) GetParagraphListInfo(pPr *ParagraphProperties) *ListInfo {
	if pPr == nil || pPr.NumPr == nil {
		return nil
	}

	if pPr.NumPr.NumID == nil {
		return nil
	}

	numID := pPr.NumPr.NumID.Val
	ilvl := 0

	if pPr.NumPr.ILvl != nil {
		if lvl, err := strconv.Atoi(pPr.NumPr.ILvl.Val); err == nil {
			ilvl = lvl
		}
	}

	return r.GetListInfo(numID, ilvl)
}

// GetListPrefix returns the display prefix for a list item
// For bullets, returns the bullet character
// For numbered lists, returns a placeholder pattern
func (r *NumberingResolver) GetListPrefix(listInfo *ListInfo) string {
	if listInfo == nil {
		return ""
	}

	if listInfo.IsBullet {
		return getBulletChar(listInfo.Level)
	}

	// For numbered lists, just return a pattern indicator
	switch listInfo.NumFmt {
	case "decimal":
		return "#."
	case "lowerLetter":
		return "a."
	case "upperLetter":
		return "A."
	case "lowerRoman":
		return "i."
	case "upperRoman":
		return "I."
	default:
		return "#."
	}
}

// getBulletChar returns the bullet character for a level
func getBulletChar(level int) string {
	switch level {
	case 0:
		return "\u2022" // Bullet •
	case 1:
		return "\u25E6" // White bullet ◦
	case 2:
		return "\u25AA" // Black small square ▪
	default:
		return "\u2022" // Default bullet •
	}
}

// GetListType returns a human-readable list type
func GetListType(numFmt string) string {
	switch numFmt {
	case "bullet":
		return "bullet"
	case "decimal":
		return "numbered"
	case "lowerLetter":
		return "lower-alpha"
	case "upperLetter":
		return "upper-alpha"
	case "lowerRoman":
		return "lower-roman"
	case "upperRoman":
		return "upper-roman"
	default:
		return numFmt
	}
}
