package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// XRefEntry represents a single entry in the cross-reference table
type XRefEntry struct {
	Offset     int64 // Byte offset of the object (for in-use objects)
	Generation int   // Generation number
	InUse      bool  // true if object is in use, false if free
	Compressed bool  // true if object is in an object stream
	StreamNum  int   // Object number of the containing stream (if compressed)
	StreamIdx  int   // Index within the stream (if compressed)
}

// XRefTable holds the complete cross-reference information
type XRefTable struct {
	Entries map[int]*XRefEntry // Object number -> entry
	Trailer Dict               // Trailer dictionary
}

// NewXRefTable creates a new empty xref table
func NewXRefTable() *XRefTable {
	return &XRefTable{
		Entries: make(map[int]*XRefEntry),
	}
}

// Get returns the entry for an object number
func (x *XRefTable) Get(objNum int) *XRefEntry {
	return x.Entries[objNum]
}

// Set adds or updates an entry
func (x *XRefTable) Set(objNum int, entry *XRefEntry) {
	x.Entries[objNum] = entry
}

// Size returns the number of entries
func (x *XRefTable) Size() int {
	return len(x.Entries)
}

// MaxObjectNum returns the highest object number
func (x *XRefTable) MaxObjectNum() int {
	max := 0
	for num := range x.Entries {
		if num > max {
			max = num
		}
	}
	return max
}

// XRefParser parses cross-reference tables
type XRefParser struct {
	data   []byte
	offset int64
}

// NewXRefParser creates a new xref parser
func NewXRefParser(data []byte) *XRefParser {
	return &XRefParser{data: data}
}

// FindStartXRef finds the startxref offset from the end of the file
func (p *XRefParser) FindStartXRef() (int64, error) {
	// Look for "startxref" near the end of the file
	// Search last 1024 bytes
	searchLen := 1024
	if len(p.data) < searchLen {
		searchLen = len(p.data)
	}

	tail := p.data[len(p.data)-searchLen:]
	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("startxref not found")
	}

	// Find the offset value after "startxref"
	startIdx := len(p.data) - searchLen + idx + len("startxref")

	// Skip whitespace
	for startIdx < len(p.data) && isWhitespace(p.data[startIdx]) {
		startIdx++
	}

	// Read the number
	endIdx := startIdx
	for endIdx < len(p.data) && p.data[endIdx] >= '0' && p.data[endIdx] <= '9' {
		endIdx++
	}

	if startIdx == endIdx {
		return 0, fmt.Errorf("invalid startxref value")
	}

	offset, err := strconv.ParseInt(string(p.data[startIdx:endIdx]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid startxref offset: %w", err)
	}

	return offset, nil
}

// ParseXRef parses the cross-reference table starting at the given offset
func (p *XRefParser) ParseXRef(offset int64) (*XRefTable, error) {
	if offset < 0 || offset >= int64(len(p.data)) {
		return nil, fmt.Errorf("invalid xref offset: %d", offset)
	}

	p.offset = offset

	// Check if this is a traditional xref table or xref stream
	// Traditional starts with "xref", stream starts with object number

	// Skip whitespace
	for p.offset < int64(len(p.data)) && isWhitespace(p.data[p.offset]) {
		p.offset++
	}

	if p.offset+4 <= int64(len(p.data)) && string(p.data[p.offset:p.offset+4]) == "xref" {
		return p.parseTraditionalXRef()
	}

	// Try parsing as xref stream
	return p.parseXRefStream()
}

// parseTraditionalXRef parses a traditional cross-reference table
func (p *XRefParser) parseTraditionalXRef() (*XRefTable, error) {
	xref := NewXRefTable()

	// Skip "xref" and whitespace
	p.offset += 4
	p.skipWhitespace()

	// Parse subsections until we hit "trailer"
	for {
		// Check for "trailer"
		if p.offset+7 <= int64(len(p.data)) && string(p.data[p.offset:p.offset+7]) == "trailer" {
			p.offset += 7
			break
		}

		// Read subsection header: first_object_num count
		firstObjNum, err := p.readInt()
		if err != nil {
			return nil, fmt.Errorf("error reading xref subsection start: %w", err)
		}

		count, err := p.readInt()
		if err != nil {
			return nil, fmt.Errorf("error reading xref subsection count: %w", err)
		}

		// Read entries
		for i := 0; i < count; i++ {
			entry, err := p.readXRefEntry()
			if err != nil {
				return nil, fmt.Errorf("error reading xref entry %d: %w", firstObjNum+i, err)
			}
			xref.Set(firstObjNum+i, entry)
		}
	}

	// Parse trailer dictionary
	p.skipWhitespace()

	lexer := NewLexerFromBytes(p.data[p.offset:])
	parser := NewParser(lexer)

	obj, err := parser.ParseObject()
	if err != nil {
		return nil, fmt.Errorf("error parsing trailer: %w", err)
	}

	trailer, ok := obj.(Dict)
	if !ok {
		return nil, fmt.Errorf("trailer is not a dictionary")
	}
	xref.Trailer = trailer

	// Handle Prev pointer for incremental updates
	if prev := trailer.GetInt("Prev"); prev > 0 {
		prevXRef, err := p.ParseXRef(prev)
		if err != nil {
			return nil, fmt.Errorf("error parsing previous xref at %d: %w", prev, err)
		}
		// Merge previous xref (current entries take precedence)
		for num, entry := range prevXRef.Entries {
			if xref.Entries[num] == nil {
				xref.Entries[num] = entry
			}
		}
	}

	return xref, nil
}

// readXRefEntry reads a single xref entry (20 bytes: offset generation n/f)
func (p *XRefParser) readXRefEntry() (*XRefEntry, error) {
	p.skipWhitespace()

	// Read 10-digit offset
	if p.offset+10 > int64(len(p.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	offsetStr := string(p.data[p.offset : p.offset+10])
	p.offset += 10

	offset, err := strconv.ParseInt(strings.TrimSpace(offsetStr), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid offset: %s", offsetStr)
	}

	// Skip space
	p.offset++

	// Read 5-digit generation
	if p.offset+5 > int64(len(p.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	genStr := string(p.data[p.offset : p.offset+5])
	p.offset += 5

	gen, err := strconv.Atoi(strings.TrimSpace(genStr))
	if err != nil {
		return nil, fmt.Errorf("invalid generation: %s", genStr)
	}

	// Skip space
	p.offset++

	// Read n or f
	if p.offset >= int64(len(p.data)) {
		return nil, io.ErrUnexpectedEOF
	}
	flag := p.data[p.offset]
	p.offset++

	// Skip trailing whitespace (typically \r\n or \n)
	for p.offset < int64(len(p.data)) && (p.data[p.offset] == '\r' || p.data[p.offset] == '\n' || p.data[p.offset] == ' ') {
		p.offset++
	}

	return &XRefEntry{
		Offset:     offset,
		Generation: gen,
		InUse:      flag == 'n',
	}, nil
}

// parseXRefStream parses a cross-reference stream (PDF 1.5+)
func (p *XRefParser) parseXRefStream() (*XRefTable, error) {
	xref := NewXRefTable()

	// Parse the stream object
	lexer := NewLexerFromBytes(p.data[p.offset:])
	parser := NewParser(lexer)

	indirectObj, err := parser.ParseIndirectObject()
	if err != nil {
		return nil, fmt.Errorf("error parsing xref stream object: %w", err)
	}

	stream, ok := indirectObj.Object.(*Stream)
	if !ok {
		return nil, fmt.Errorf("xref object is not a stream")
	}

	// Verify it's an XRef stream
	if stream.Dict.GetName("Type") != "XRef" {
		return nil, fmt.Errorf("stream is not an XRef type")
	}

	xref.Trailer = stream.Dict

	// Get the W array (field widths)
	wArray := stream.Dict.GetArray("W")
	if wArray == nil || len(wArray) != 3 {
		return nil, fmt.Errorf("invalid or missing W array in xref stream")
	}

	w1 := int(wArray.GetInt(0)) // Type field width
	w2 := int(wArray.GetInt(1)) // Field 2 width (offset or object number)
	w3 := int(wArray.GetInt(2)) // Field 3 width (generation or index)
	entrySize := w1 + w2 + w3

	// Validate entry size to prevent infinite loop
	if entrySize <= 0 {
		return nil, fmt.Errorf("invalid W array in xref stream: entry size is %d", entrySize)
	}

	// Get Index array (defaults to [0 Size])
	indexArray := stream.Dict.GetArray("Index")
	size := stream.Dict.GetInt("Size")

	var subsections [][2]int64 // [start, count] pairs
	if indexArray != nil {
		// Index array must have even number of elements (start, count pairs)
		if len(indexArray)%2 != 0 {
			return nil, fmt.Errorf("invalid Index array in xref stream: odd number of elements")
		}
		for i := 0; i+1 < len(indexArray); i += 2 {
			start := indexArray.GetInt(i)
			count := indexArray.GetInt(i + 1)
			if count < 0 {
				return nil, fmt.Errorf("invalid Index array in xref stream: negative count")
			}
			subsections = append(subsections, [2]int64{start, count})
		}
	} else {
		subsections = [][2]int64{{0, size}}
	}

	// Decode stream data
	data, err := DecodeStream(stream)
	if err != nil {
		return nil, fmt.Errorf("error decoding xref stream: %w", err)
	}

	// Parse entries
	pos := 0
	for _, subsection := range subsections {
		startObj := int(subsection[0])
		count := int(subsection[1])

		for i := 0; i < count; i++ {
			if pos+entrySize > len(data) {
				return nil, fmt.Errorf("xref stream data too short")
			}

			// Read fields
			typeField := readIntFromBytes(data[pos:pos+w1], w1)
			field2 := readIntFromBytes(data[pos+w1:pos+w1+w2], w2)
			field3 := readIntFromBytes(data[pos+w1+w2:pos+w1+w2+w3], w3)
			pos += entrySize

			objNum := startObj + i
			var entry *XRefEntry

			// Default type is 1 if w1 is 0
			if w1 == 0 {
				typeField = 1
			}

			switch typeField {
			case 0: // Free object
				entry = &XRefEntry{
					Offset:     field2, // Next free object number
					Generation: int(field3),
					InUse:      false,
				}
			case 1: // Uncompressed object
				entry = &XRefEntry{
					Offset:     field2,
					Generation: int(field3),
					InUse:      true,
				}
			case 2: // Compressed object in object stream
				entry = &XRefEntry{
					Offset:     0,
					Generation: 0,
					InUse:      true,
					Compressed: true,
					StreamNum:  int(field2), // Object stream number
					StreamIdx:  int(field3), // Index in stream
				}
			}

			if entry != nil {
				xref.Set(objNum, entry)
			}
		}
	}

	// Handle Prev pointer for incremental updates
	if prev := xref.Trailer.GetInt("Prev"); prev > 0 {
		prevXRef, err := p.ParseXRef(prev)
		if err != nil {
			return nil, fmt.Errorf("error parsing previous xref at %d: %w", prev, err)
		}
		// Merge previous xref (current entries take precedence)
		for num, entry := range prevXRef.Entries {
			if xref.Entries[num] == nil {
				xref.Entries[num] = entry
			}
		}
	}

	return xref, nil
}

// readInt reads an integer from the current position
func (p *XRefParser) readInt() (int, error) {
	p.skipWhitespace()

	start := p.offset
	for p.offset < int64(len(p.data)) {
		b := p.data[p.offset]
		if b < '0' || b > '9' {
			break
		}
		p.offset++
	}

	if start == p.offset {
		return 0, fmt.Errorf("expected integer")
	}

	val, err := strconv.Atoi(string(p.data[start:p.offset]))
	if err != nil {
		return 0, err
	}

	p.skipWhitespace()
	return val, nil
}

// skipWhitespace skips whitespace characters
func (p *XRefParser) skipWhitespace() {
	for p.offset < int64(len(p.data)) && isWhitespace(p.data[p.offset]) {
		p.offset++
	}
}

// readIntFromBytes reads a big-endian integer from bytes
func readIntFromBytes(data []byte, width int) int64 {
	if width == 0 {
		return 0
	}
	var val int64
	for i := 0; i < width; i++ {
		val = (val << 8) | int64(data[i])
	}
	return val
}

