package pdf

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mmalcek/docuIndex/pdf/encoding"
)

// Font represents a PDF font and handles character to Unicode mapping
type Font struct {
	Name            string             // Font name (e.g., "Helvetica", "F1")
	Subtype         string             // Font subtype (Type1, TrueType, Type0, etc.)
	BaseFont        string             // Base font name
	Encoding        string             // Encoding name or "Identity-H"
	ToUnicode       map[uint16]rune    // CMap for character code to Unicode (single char)
	ToUnicodeString map[uint16]string  // CMap for character code to Unicode string (multi-char)
	Differences     map[byte]string    // Encoding Differences: char code -> glyph name
	Widths          map[int]float64    // Character widths
	FirstChar       int                // First character code
	LastChar        int                // Last character code
	IsCID           bool               // True if this is a CID font
	WMode           int                // Writing mode: 0=horizontal (default), 1=vertical
	doc             *Document          // Reference to document
	dict            Dict               // Font dictionary
}

// LoadFont creates a Font from a font dictionary
func LoadFont(doc *Document, name string, fontDict Dict) (*Font, error) {
	font := &Font{
		Name:            name,
		doc:             doc,
		dict:            fontDict,
		ToUnicode:       make(map[uint16]rune),
		ToUnicodeString: make(map[uint16]string),
		Differences:     make(map[byte]string),
		Widths:          make(map[int]float64),
	}

	// Get subtype
	font.Subtype = fontDict.GetName("Subtype")
	font.BaseFont = fontDict.GetName("BaseFont")

	// Handle different font types
	switch font.Subtype {
	case "Type0":
		// Composite font (CID font)
		font.IsCID = true
		if err := font.loadCIDFont(fontDict); err != nil {
			return font, nil // Continue with partial font
		}
	case "Type1", "TrueType", "MMType1":
		// Simple font
		if err := font.loadSimpleFont(fontDict); err != nil {
			return font, nil
		}
	case "Type3":
		// Type 3 font - bitmap font
		font.loadType3Font(fontDict)
	}

	// Load ToUnicode CMap if present
	if toUnicodeObj := fontDict.Get("ToUnicode"); toUnicodeObj != nil {
		if err := font.loadToUnicodeFromObj(toUnicodeObj); err != nil {
			// Continue without ToUnicode
		}
	}

	return font, nil
}

// loadSimpleFont loads a simple (non-CID) font
func (f *Font) loadSimpleFont(fontDict Dict) error {
	// Get encoding
	encodingObj := fontDict.Get("Encoding")
	if encodingObj != nil {
		switch enc := encodingObj.(type) {
		case Name:
			f.Encoding = string(enc)
		case Dict:
			// Encoding dictionary with Differences
			f.Encoding = enc.GetName("BaseEncoding")
			f.parseDifferences(enc)
		case *Ref:
			resolved, err := f.doc.Resolve(enc)
			if err == nil {
				if encDict, ok := resolved.(Dict); ok {
					f.Encoding = encDict.GetName("BaseEncoding")
					f.parseDifferences(encDict)
				} else if name, ok := resolved.(Name); ok {
					f.Encoding = string(name)
				}
			}
		}
	}

	// Get character widths
	f.FirstChar = int(fontDict.GetInt("FirstChar"))
	f.LastChar = int(fontDict.GetInt("LastChar"))

	if widthsObj := fontDict.Get("Widths"); widthsObj != nil {
		widths, err := f.doc.ResolveArray(widthsObj)
		if err == nil {
			for i, w := range widths {
				charCode := f.FirstChar + i
				if width, ok := AsFloat(w); ok {
					f.Widths[charCode] = width
				}
			}
		}
	}

	return nil
}

// parseDifferences parses the Differences array from an encoding dictionary
// Format: [code1 /name1 /name2 ... code2 /nameN ...]
// Each integer starts a new sequence at that character code
func (f *Font) parseDifferences(encDict Dict) {
	diffsObj := encDict.Get("Differences")
	if diffsObj == nil {
		return
	}

	// Resolve if it's a reference
	var diffs Array
	switch d := diffsObj.(type) {
	case Array:
		diffs = d
	case *Ref:
		resolved, err := f.doc.Resolve(d)
		if err != nil {
			return
		}
		if arr, ok := resolved.(Array); ok {
			diffs = arr
		} else {
			return
		}
	default:
		return
	}

	// Parse the Differences array
	// Format: [charCode /glyphName /glyphName ... charCode /glyphName ...]
	var currentCode int = 0
	for _, item := range diffs {
		switch v := item.(type) {
		case Integer:
			// New starting character code
			currentCode = int(v)
		case Name:
			// Glyph name for current code
			if currentCode >= 0 && currentCode <= 255 {
				f.Differences[byte(currentCode)] = string(v)
			}
			currentCode++
		}
	}
}

// loadCIDFont loads a CID (composite) font
func (f *Font) loadCIDFont(fontDict Dict) error {
	// Get descendant fonts
	descendantsObj := fontDict.Get("DescendantFonts")
	if descendantsObj == nil {
		return fmt.Errorf("missing DescendantFonts")
	}

	descendants, err := f.doc.ResolveArray(descendantsObj)
	if err != nil {
		return err
	}

	if len(descendants) == 0 {
		return fmt.Errorf("empty DescendantFonts")
	}

	// Get the CID font dictionary
	cidFontDict, err := f.doc.ResolveDict(descendants[0])
	if err != nil {
		return err
	}

	// Detect WMode from encoding name
	// Identity-V and other -V encodings indicate vertical writing mode
	encodingObj := fontDict.Get("Encoding")
	if encodingObj != nil {
		switch enc := encodingObj.(type) {
		case Name:
			f.Encoding = string(enc)
			if strings.HasSuffix(f.Encoding, "-V") {
				f.WMode = 1 // Vertical
			}
		case *Ref:
			resolved, resolveErr := f.doc.Resolve(enc)
			if resolveErr == nil {
				if name, ok := resolved.(Name); ok {
					f.Encoding = string(name)
					if strings.HasSuffix(f.Encoding, "-V") {
						f.WMode = 1
					}
				}
				// If it's a CMap stream, check for WMode
				if stream, ok := resolved.(*Stream); ok {
					if wmodeVal := stream.Dict.GetInt("WMode"); wmodeVal > 0 {
						f.WMode = int(wmodeVal)
					}
				}
			}
		}
	}

	// Get CID widths
	if wObj := cidFontDict.Get("W"); wObj != nil {
		f.loadCIDWidths(wObj)
	}

	// Get default width
	if dw := cidFontDict.GetInt("DW"); dw > 0 {
		// Default width for CID font
	}

	return nil
}

// loadCIDWidths loads width information from CID font W array
func (f *Font) loadCIDWidths(wObj Object) {
	wArray, err := f.doc.ResolveArray(wObj)
	if err != nil {
		return
	}

	i := 0
	for i < len(wArray) {
		c1, ok := AsInt(wArray[i])
		if !ok {
			break
		}
		i++

		if i >= len(wArray) {
			break
		}

		// Next element is either an array of widths or a range end
		switch next := wArray[i].(type) {
		case Array:
			// c1 [w1 w2 w3 ...] - individual widths starting at c1
			for j, w := range next {
				if width, ok := AsFloat(w); ok {
					f.Widths[int(c1)+j] = width
				}
			}
			i++
		default:
			// c1 c2 w - range from c1 to c2 with width w
			if i+1 >= len(wArray) {
				break
			}
			c2, ok := AsInt(wArray[i])
			if !ok {
				break
			}
			i++
			width, ok := AsFloat(wArray[i])
			if !ok {
				break
			}
			i++
			for c := c1; c <= c2; c++ {
				f.Widths[int(c)] = width
			}
		}
	}
}

// loadType3Font loads a Type 3 font
func (f *Font) loadType3Font(fontDict Dict) {
	// Type 3 fonts use their own encoding
	f.FirstChar = int(fontDict.GetInt("FirstChar"))
	f.LastChar = int(fontDict.GetInt("LastChar"))
}

// loadToUnicodeFromObj loads ToUnicode CMap from various object types
func (f *Font) loadToUnicodeFromObj(obj Object) error {
	var stream *Stream
	var err error

	switch v := obj.(type) {
	case *Ref:
		stream, err = f.doc.ResolveStream(v)
		if err != nil {
			return err
		}
	case *Stream:
		stream = v
	default:
		// Try to resolve it
		resolved, resolveErr := f.doc.Resolve(obj)
		if resolveErr != nil {
			return resolveErr
		}
		if s, ok := resolved.(*Stream); ok {
			stream = s
		} else {
			return fmt.Errorf("ToUnicode is not a stream")
		}
	}

	data, err := DecodeStream(stream)
	if err != nil {
		return err
	}

	return f.parseToUnicodeCMap(data)
}

// parseToUnicodeCMap parses a ToUnicode CMap
func (f *Font) parseToUnicodeCMap(data []byte) error {
	// Remove comments before parsing (lines starting with %)
	content := removePostScriptComments(string(data))

	// Extract bfchar sections and parse them
	bfcharSectionRe := regexp.MustCompile(`(?s)beginbfchar\s*(.*?)\s*endbfchar`)
	bfcharSections := bfcharSectionRe.FindAllStringSubmatch(content, -1)
	for _, section := range bfcharSections {
		if len(section) >= 2 {
			f.parseBfcharSection(section[1])
		}
	}

	// Extract bfrange sections and parse them
	bfrangeSectionRe := regexp.MustCompile(`(?s)beginbfrange\s*(.*?)\s*endbfrange`)
	bfrangeSections := bfrangeSectionRe.FindAllStringSubmatch(content, -1)
	for _, section := range bfrangeSections {
		if len(section) >= 2 {
			f.parseBfrangeSection(section[1])
		}
	}

	return nil
}

// removePostScriptComments removes PostScript-style comments (% to end of line)
func removePostScriptComments(s string) string {
	var result strings.Builder
	inComment := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inComment {
			if ch == '\n' || ch == '\r' {
				inComment = false
				result.WriteByte(ch)
			}
			// Skip other characters in comment
		} else if ch == '%' {
			inComment = true
		} else {
			result.WriteByte(ch)
		}
	}

	return result.String()
}

// parseBfcharSection parses individual bfchar mappings
func (f *Font) parseBfcharSection(section string) {
	// Match <srcCode> <dstCode> pairs
	pairRe := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	matches := pairRe.FindAllStringSubmatch(section, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			srcCode, err := strconv.ParseUint(m[1], 16, 16)
			if err != nil {
				continue
			}
			dstHex := m[2]
			decoded := f.decodeUTF16BEHexToString(dstHex)
			if len(decoded) == 0 {
				continue
			}
			// Clean up the decoded string - strip trailing junk added by some PDFs
			decoded = cleanToUnicodeMapping(decoded)
			if len(decoded) == 1 {
				f.ToUnicode[uint16(srcCode)] = rune(decoded[0])
			} else if len(decoded) > 1 {
				f.ToUnicodeString[uint16(srcCode)] = decoded
			}
		}
	}
}

// cleanToUnicodeMapping removes trailing spaces and control characters from CMap mappings
// Some PDFs add spaces or control chars after each character in ToUnicode
func cleanToUnicodeMapping(s string) string {
	// Trim trailing whitespace and control characters (0x00-0x1F)
	runes := []rune(s)
	end := len(runes)
	for end > 0 {
		r := runes[end-1]
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || (r >= 0 && r <= 0x1F) {
			end--
		} else {
			break
		}
	}
	if end == 0 {
		return s // Don't return empty string, keep original
	}
	return string(runes[:end])
}

// parseBfrangeSection parses bfrange mappings
func (f *Font) parseBfrangeSection(section string) {
	// Match <start> <end> <dstStart> format
	rangeRe := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`)
	// Match <start> <end> [<dst1> <dst2> ...] format
	arrayRe := regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*\[((?:[^]]+))\]`)

	// First try array format
	arrayMatches := arrayRe.FindAllStringSubmatch(section, -1)
	for _, m := range arrayMatches {
		if len(m) >= 4 {
			start, _ := strconv.ParseUint(m[1], 16, 16)
			arrayStr := m[3]
			hexRe := regexp.MustCompile(`<([0-9A-Fa-f]+)>`)
			hexMatches := hexRe.FindAllStringSubmatch(arrayStr, -1)
			for i, hm := range hexMatches {
				if len(hm) >= 2 {
					dstHex := hm[1]
					decoded := f.decodeUTF16BEHexToString(dstHex)
					decoded = cleanToUnicodeMapping(decoded)
					code := uint16(start) + uint16(i)
					if len(decoded) == 1 {
						f.ToUnicode[code] = rune(decoded[0])
					} else if len(decoded) > 1 {
						f.ToUnicodeString[code] = decoded
					}
				}
			}
		}
	}

	// Then parse simple range format (but skip lines already handled by array format)
	rangeMatches := rangeRe.FindAllStringSubmatch(section, -1)
	for _, m := range rangeMatches {
		if len(m) >= 4 {
			start, _ := strconv.ParseUint(m[1], 16, 16)
			end, _ := strconv.ParseUint(m[2], 16, 16)

			// Skip if this is part of an array format (check if already populated)
			if _, exists := f.ToUnicode[uint16(start)]; exists {
				continue
			}
			if _, exists := f.ToUnicodeString[uint16(start)]; exists {
				continue
			}

			dstHex := m[3]
			decoded := f.decodeUTF16BEHexToString(dstHex)
			decoded = cleanToUnicodeMapping(decoded)

			if len(decoded) == 1 {
				// Single character - can increment
				r := rune(decoded[0])
				for code := start; code <= end; code++ {
					f.ToUnicode[uint16(code)] = r
					r++
				}
			} else if len(decoded) > 1 {
				// Multi-char - increment last character
				runes := []rune(decoded)
				for code := start; code <= end; code++ {
					f.ToUnicodeString[uint16(code)] = string(runes)
					runes[len(runes)-1]++
				}
			}
		}
	}
}

// decodeUTF16BEHexToString decodes a hex string to a Go string
// Handles both UTF-16BE (4 hex chars per code unit) and single-byte (2 hex chars) formats
func (f *Font) decodeUTF16BEHexToString(hex string) string {
	if len(hex) == 0 {
		return ""
	}

	// Pad to even length if needed
	if len(hex)%2 != 0 {
		hex = "0" + hex
	}

	var result strings.Builder

	// Handle different hex string lengths
	if len(hex) == 2 {
		// Single byte - treat as direct character code
		code, err := strconv.ParseUint(hex, 16, 8)
		if err == nil {
			result.WriteRune(rune(code))
		}
		return result.String()
	}

	// For 4+ hex chars, process as UTF-16BE
	// Pad to multiple of 4 if needed (prepend zeros)
	for len(hex)%4 != 0 {
		hex = "00" + hex
	}

	for i := 0; i < len(hex); i += 4 {
		code, err := strconv.ParseUint(hex[i:i+4], 16, 16)
		if err != nil {
			continue
		}

		// Check for high surrogate
		if code >= 0xD800 && code <= 0xDBFF {
			// Need a low surrogate to follow
			if i+8 <= len(hex) {
				low, err := strconv.ParseUint(hex[i+4:i+8], 16, 16)
				if err == nil && low >= 0xDC00 && low <= 0xDFFF {
					// Valid surrogate pair - decode it
					r := rune(0x10000 + ((code-0xD800)<<10 | (low - 0xDC00)))
					result.WriteRune(r)
					i += 4 // Skip the low surrogate
					continue
				}
			}
			// Unpaired high surrogate - use replacement character
			result.WriteRune(ReplacementChar)
			continue
		}

		// Check for lone low surrogate (invalid)
		if code >= 0xDC00 && code <= 0xDFFF {
			result.WriteRune(ReplacementChar)
			continue
		}

		result.WriteRune(rune(code))
	}

	return result.String()
}

// decodeUTF16BEHex decodes a hex string as UTF-16BE to a rune (first char only)
func (f *Font) decodeUTF16BEHex(hex string) rune {
	s := f.decodeUTF16BEHexToString(hex)
	if len(s) == 0 {
		return 0
	}
	return rune(s[0])
}

// ReplacementChar is used for unmapped characters (U+FFFD)
const ReplacementChar = '\uFFFD'

// Decode converts character codes to Unicode string
func (f *Font) Decode(data []byte) string {
	var result strings.Builder

	if f.IsCID {
		// CID fonts use 2-byte character codes
		for i := 0; i+1 < len(data); i += 2 {
			code := uint16(data[i])<<8 | uint16(data[i+1])
			// Try ToUnicodeString first (for multi-char mappings)
			if s, ok := f.ToUnicodeString[code]; ok {
				result.WriteString(s)
				continue
			}
			if r, ok := f.ToUnicode[code]; ok {
				result.WriteRune(r)
			} else {
				// CID codes are NOT valid Unicode - use replacement character
				// instead of directly casting code to rune which produces garbage
				result.WriteRune(ReplacementChar)
			}
		}
	} else {
		// Simple fonts use 1-byte character codes
		for _, b := range data {
			code := uint16(b)

			// Try ToUnicodeString first (for multi-char mappings)
			if s, ok := f.ToUnicodeString[code]; ok {
				result.WriteString(s)
				continue
			}

			// Try ToUnicode single-char mapping
			if r, ok := f.ToUnicode[code]; ok {
				result.WriteRune(r)
				continue
			}

			// Fall back to encoding
			r := f.decodeChar(b)
			result.WriteRune(r)
		}
	}

	return result.String()
}

// decodeChar decodes a single character using the font's encoding
func (f *Font) decodeChar(code byte) rune {
	// First, check if we have a Differences entry for this code
	if glyphName, ok := f.Differences[code]; ok {
		// Look up the glyph name in the Adobe Glyph List
		if r, found := encoding.LookupGlyph(glyphName); found {
			return r
		}
		// If glyph name is a single character like "A", use it directly
		if len(glyphName) == 1 {
			return rune(glyphName[0])
		}
		// Handle uniXXXX format glyph names (e.g., "uni0041" for 'A')
		if len(glyphName) == 7 && strings.HasPrefix(glyphName, "uni") {
			if code, err := strconv.ParseUint(glyphName[3:], 16, 16); err == nil {
				return rune(code)
			}
		}
	}

	// Fall back to base encoding
	switch f.Encoding {
	case "WinAnsiEncoding":
		return encoding.WinAnsiEncoding[code]
	case "MacRomanEncoding":
		return encoding.MacRomanEncoding[code]
	case "StandardEncoding":
		return encoding.StandardEncoding[code]
	default:
		// Check if it's a standard font
		if f.isStandardFont() {
			return encoding.WinAnsiEncoding[code]
		}
		// Default to WinAnsi for unknown encodings
		if code >= 32 && code < 127 {
			return rune(code)
		}
		return encoding.WinAnsiEncoding[code]
	}
}

// isStandardFont checks if this is one of the 14 standard PDF fonts
func (f *Font) isStandardFont() bool {
	standardFonts := map[string]bool{
		"Courier":               true,
		"Courier-Bold":          true,
		"Courier-Oblique":       true,
		"Courier-BoldOblique":   true,
		"Helvetica":             true,
		"Helvetica-Bold":        true,
		"Helvetica-Oblique":     true,
		"Helvetica-BoldOblique": true,
		"Times-Roman":           true,
		"Times-Bold":            true,
		"Times-Italic":          true,
		"Times-BoldItalic":      true,
		"Symbol":                true,
		"ZapfDingbats":          true,
	}

	return standardFonts[f.BaseFont]
}

// GetWidth returns the width of a character in the font
func (f *Font) GetWidth(code int) float64 {
	if w, ok := f.Widths[code]; ok {
		return w
	}

	// Try standard font widths if this is a standard font
	if f.isStandardFont() {
		if w := getStandardFontWidth(f.BaseFont, code); w > 0 {
			return w
		}
	}

	// Default width - use 600 (average for proportional fonts) instead of 1000
	// 1000 is way too wide and causes spacing issues
	return 600
}

// getStandardFontWidth returns the width of a character in standard PDF fonts
// Widths are in 1/1000 em units
func getStandardFontWidth(fontName string, code int) float64 {
	// Helvetica is the most common font, use it as default for unknown fonts
	// These are the actual Adobe font metrics for the 14 standard fonts

	// Get the base font name without style suffix for lookup
	baseName := fontName

	switch baseName {
	case "Courier", "Courier-Bold", "Courier-Oblique", "Courier-BoldOblique":
		// Courier is monospace - all characters are 600
		return 600

	case "Helvetica", "Helvetica-Bold", "Helvetica-Oblique", "Helvetica-BoldOblique":
		return getHelveticaWidth(code)

	case "Times-Roman", "Times-Bold", "Times-Italic", "Times-BoldItalic":
		return getTimesWidth(code)

	case "Symbol":
		return getSymbolWidth(code)

	case "ZapfDingbats":
		return getZapfDingbatsWidth(code)
	}

	// Default: use Helvetica metrics
	return getHelveticaWidth(code)
}

// getHelveticaWidth returns character widths for Helvetica
// Based on Adobe Font Metrics for Helvetica
func getHelveticaWidth(code int) float64 {
	// Common ASCII characters (32-127)
	helveticaWidths := map[int]float64{
		32: 278,  // space
		33: 278,  // exclam
		34: 355,  // quotedbl
		35: 556,  // numbersign
		36: 556,  // dollar
		37: 889,  // percent
		38: 667,  // ampersand
		39: 191,  // quotesingle
		40: 333,  // parenleft
		41: 333,  // parenright
		42: 389,  // asterisk
		43: 584,  // plus
		44: 278,  // comma
		45: 333,  // hyphen
		46: 278,  // period
		47: 278,  // slash
		48: 556,  // zero
		49: 556,  // one
		50: 556,  // two
		51: 556,  // three
		52: 556,  // four
		53: 556,  // five
		54: 556,  // six
		55: 556,  // seven
		56: 556,  // eight
		57: 556,  // nine
		58: 278,  // colon
		59: 278,  // semicolon
		60: 584,  // less
		61: 584,  // equal
		62: 584,  // greater
		63: 556,  // question
		64: 1015, // at
		65: 667,  // A
		66: 667,  // B
		67: 722,  // C
		68: 722,  // D
		69: 667,  // E
		70: 611,  // F
		71: 778,  // G
		72: 722,  // H
		73: 278,  // I
		74: 500,  // J
		75: 667,  // K
		76: 556,  // L
		77: 833,  // M
		78: 722,  // N
		79: 778,  // O
		80: 667,  // P
		81: 778,  // Q
		82: 722,  // R
		83: 667,  // S
		84: 611,  // T
		85: 722,  // U
		86: 667,  // V
		87: 944,  // W
		88: 667,  // X
		89: 667,  // Y
		90: 611,  // Z
		91: 278,  // bracketleft
		92: 278,  // backslash
		93: 278,  // bracketright
		94: 469,  // asciicircum
		95: 556,  // underscore
		96: 333,  // grave
		97: 556,  // a
		98: 556,  // b
		99: 500,  // c
		100: 556, // d
		101: 556, // e
		102: 278, // f
		103: 556, // g
		104: 556, // h
		105: 222, // i
		106: 222, // j
		107: 500, // k
		108: 222, // l
		109: 833, // m
		110: 556, // n
		111: 556, // o
		112: 556, // p
		113: 556, // q
		114: 333, // r
		115: 500, // s
		116: 278, // t
		117: 556, // u
		118: 500, // v
		119: 722, // w
		120: 500, // x
		121: 500, // y
		122: 500, // z
		123: 334, // braceleft
		124: 260, // bar
		125: 334, // braceright
		126: 584, // asciitilde
	}

	if w, ok := helveticaWidths[code]; ok {
		return w
	}
	return 556 // Average width for unknown characters
}

// getTimesWidth returns character widths for Times-Roman
func getTimesWidth(code int) float64 {
	timesWidths := map[int]float64{
		32: 250,  // space
		33: 333,  // exclam
		34: 408,  // quotedbl
		35: 500,  // numbersign
		36: 500,  // dollar
		37: 833,  // percent
		38: 778,  // ampersand
		39: 180,  // quotesingle
		40: 333,  // parenleft
		41: 333,  // parenright
		42: 500,  // asterisk
		43: 564,  // plus
		44: 250,  // comma
		45: 333,  // hyphen
		46: 250,  // period
		47: 278,  // slash
		48: 500,  // zero
		49: 500,  // one
		50: 500,  // two
		51: 500,  // three
		52: 500,  // four
		53: 500,  // five
		54: 500,  // six
		55: 500,  // seven
		56: 500,  // eight
		57: 500,  // nine
		58: 278,  // colon
		59: 278,  // semicolon
		60: 564,  // less
		61: 564,  // equal
		62: 564,  // greater
		63: 444,  // question
		64: 921,  // at
		65: 722,  // A
		66: 667,  // B
		67: 667,  // C
		68: 722,  // D
		69: 611,  // E
		70: 556,  // F
		71: 722,  // G
		72: 722,  // H
		73: 333,  // I
		74: 389,  // J
		75: 722,  // K
		76: 611,  // L
		77: 889,  // M
		78: 722,  // N
		79: 722,  // O
		80: 556,  // P
		81: 722,  // Q
		82: 667,  // R
		83: 556,  // S
		84: 611,  // T
		85: 722,  // U
		86: 722,  // V
		87: 944,  // W
		88: 722,  // X
		89: 722,  // Y
		90: 611,  // Z
		91: 333,  // bracketleft
		92: 278,  // backslash
		93: 333,  // bracketright
		94: 469,  // asciicircum
		95: 500,  // underscore
		96: 333,  // grave
		97: 444,  // a
		98: 500,  // b
		99: 444,  // c
		100: 500, // d
		101: 444, // e
		102: 333, // f
		103: 500, // g
		104: 500, // h
		105: 278, // i
		106: 278, // j
		107: 500, // k
		108: 278, // l
		109: 778, // m
		110: 500, // n
		111: 500, // o
		112: 500, // p
		113: 500, // q
		114: 333, // r
		115: 389, // s
		116: 278, // t
		117: 500, // u
		118: 500, // v
		119: 722, // w
		120: 500, // x
		121: 500, // y
		122: 444, // z
		123: 480, // braceleft
		124: 200, // bar
		125: 480, // braceright
		126: 541, // asciitilde
	}

	if w, ok := timesWidths[code]; ok {
		return w
	}
	return 500 // Average width for unknown characters
}

// getSymbolWidth returns widths for Symbol font (simplified)
func getSymbolWidth(code int) float64 {
	return 500 // Most Symbol characters are around 500
}

// getZapfDingbatsWidth returns widths for ZapfDingbats (simplified)
func getZapfDingbatsWidth(code int) float64 {
	return 700 // Most dingbats are fairly wide
}

// IsBold checks if the font name suggests bold weight
func (f *Font) IsBold() bool {
	name := strings.ToLower(f.BaseFont)
	return strings.Contains(name, "bold") || strings.Contains(name, "black") || strings.Contains(name, "heavy")
}

// IsItalic checks if the font name suggests italic style
func (f *Font) IsItalic() bool {
	name := strings.ToLower(f.BaseFont)
	return strings.Contains(name, "italic") || strings.Contains(name, "oblique")
}

// IsVertical returns true if the font uses vertical writing mode
func (f *Font) IsVertical() bool {
	return f.WMode == 1
}

// FontCache caches loaded fonts for a document
type FontCache struct {
	doc   *Document
	fonts map[string]*Font
}

// NewFontCache creates a new font cache
func NewFontCache(doc *Document) *FontCache {
	return &FontCache{
		doc:   doc,
		fonts: make(map[string]*Font),
	}
}

// GetFont gets or loads a font
func (fc *FontCache) GetFont(name string, fontDict Dict) (*Font, error) {
	if font, ok := fc.fonts[name]; ok {
		return font, nil
	}

	font, err := LoadFont(fc.doc, name, fontDict)
	if err != nil {
		return nil, err
	}

	fc.fonts[name] = font
	return font, nil
}

// DecodeTextString decodes a PDF text string (handles BOM for UTF-16BE)
func DecodeTextString(data []byte) string {
	if len(data) >= 2 {
		// Check for UTF-16BE BOM
		if data[0] == 0xFE && data[1] == 0xFF {
			return decodeUTF16BE(data[2:])
		}
		// Check for UTF-8 BOM
		if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
			return string(data[3:])
		}
	}
	// Use PDFDocEncoding
	return encoding.DecodePDFDoc(data)
}

// decodeUTF16BE decodes UTF-16BE encoded data
func decodeUTF16BE(data []byte) string {
	var result bytes.Buffer
	for i := 0; i+1 < len(data); i += 2 {
		code := rune(data[i])<<8 | rune(data[i+1])
		// Handle surrogate pairs
		if code >= 0xD800 && code <= 0xDBFF && i+3 < len(data) {
			high := code
			low := rune(data[i+2])<<8 | rune(data[i+3])
			if low >= 0xDC00 && low <= 0xDFFF {
				code = 0x10000 + ((high-0xD800)<<10 | (low - 0xDC00))
				i += 2
			}
		}
		result.WriteRune(code)
	}
	return result.String()
}
