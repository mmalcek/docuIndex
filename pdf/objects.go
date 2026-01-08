package pdf

import (
	"fmt"
	"strconv"
	"strings"
)

// Object represents any PDF object
type Object interface {
	// Type returns the PDF type name
	Type() string
	// String returns a string representation
	String() string
}

// Null represents a PDF null object
type Null struct{}

func (Null) Type() string   { return "null" }
func (Null) String() string { return "null" }

// Boolean represents a PDF boolean
type Boolean bool

func (Boolean) Type() string    { return "boolean" }
func (b Boolean) String() string { return strconv.FormatBool(bool(b)) }

// Integer represents a PDF integer
type Integer int64

func (Integer) Type() string    { return "integer" }
func (i Integer) String() string { return strconv.FormatInt(int64(i), 10) }

// Real represents a PDF real number
type Real float64

func (Real) Type() string    { return "real" }
func (r Real) String() string { return strconv.FormatFloat(float64(r), 'f', -1, 64) }

// String represents a PDF string (literal or hex)
type String string

func (String) Type() string   { return "string" }
func (s String) String() string { return fmt.Sprintf("(%s)", string(s)) }

// Bytes returns the string as bytes
func (s String) Bytes() []byte { return []byte(s) }

// Name represents a PDF name object
type Name string

func (Name) Type() string   { return "name" }
func (n Name) String() string { return "/" + string(n) }

// Array represents a PDF array
type Array []Object

func (Array) Type() string { return "array" }
func (a Array) String() string {
	var parts []string
	for _, obj := range a {
		parts = append(parts, obj.String())
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// Get returns the element at index i, or nil if out of bounds
func (a Array) Get(i int) Object {
	if i < 0 || i >= len(a) {
		return nil
	}
	return a[i]
}

// GetInt returns the integer at index i, or 0
func (a Array) GetInt(i int) int64 {
	obj := a.Get(i)
	if obj == nil {
		return 0
	}
	switch v := obj.(type) {
	case Integer:
		return int64(v)
	case Real:
		return int64(v)
	}
	return 0
}

// GetFloat returns the float at index i, or 0
func (a Array) GetFloat(i int) float64 {
	obj := a.Get(i)
	if obj == nil {
		return 0
	}
	switch v := obj.(type) {
	case Real:
		return float64(v)
	case Integer:
		return float64(v)
	}
	return 0
}

// GetName returns the name at index i, or empty string
func (a Array) GetName(i int) string {
	obj := a.Get(i)
	if n, ok := obj.(Name); ok {
		return string(n)
	}
	return ""
}

// Dict represents a PDF dictionary
type Dict map[Name]Object

func (Dict) Type() string { return "dictionary" }
func (d Dict) String() string {
	var parts []string
	for k, v := range d {
		parts = append(parts, k.String()+" "+v.String())
	}
	return "<< " + strings.Join(parts, " ") + " >>"
}

// Get returns the value for a key, or nil
func (d Dict) Get(key string) Object {
	return d[Name(key)]
}

// GetName returns a name value, or empty string
func (d Dict) GetName(key string) string {
	if n, ok := d[Name(key)].(Name); ok {
		return string(n)
	}
	return ""
}

// GetString returns a string value, or empty string
func (d Dict) GetString(key string) string {
	if s, ok := d[Name(key)].(String); ok {
		return string(s)
	}
	return ""
}

// GetInt returns an integer value, or 0
func (d Dict) GetInt(key string) int64 {
	switch v := d[Name(key)].(type) {
	case Integer:
		return int64(v)
	case Real:
		return int64(v)
	}
	return 0
}

// GetFloat returns a float value, or 0
func (d Dict) GetFloat(key string) float64 {
	switch v := d[Name(key)].(type) {
	case Real:
		return float64(v)
	case Integer:
		return float64(v)
	}
	return 0
}

// GetBool returns a boolean value, or false
func (d Dict) GetBool(key string) bool {
	if b, ok := d[Name(key)].(Boolean); ok {
		return bool(b)
	}
	return false
}

// GetArray returns an array value, or nil
func (d Dict) GetArray(key string) Array {
	if a, ok := d[Name(key)].(Array); ok {
		return a
	}
	return nil
}

// GetDict returns a dictionary value, or nil
func (d Dict) GetDict(key string) Dict {
	if dict, ok := d[Name(key)].(Dict); ok {
		return dict
	}
	return nil
}

// GetRef returns a reference value, or nil
func (d Dict) GetRef(key string) *Ref {
	if r, ok := d[Name(key)].(*Ref); ok {
		return r
	}
	return nil
}

// Has returns true if the key exists
func (d Dict) Has(key string) bool {
	_, ok := d[Name(key)]
	return ok
}

// Ref represents an indirect object reference (e.g., "5 0 R")
type Ref struct {
	Num int // Object number
	Gen int // Generation number
}

func (*Ref) Type() string { return "reference" }
func (r *Ref) String() string {
	return fmt.Sprintf("%d %d R", r.Num, r.Gen)
}

// Stream represents a PDF stream object
type Stream struct {
	Dict    Dict   // Stream dictionary
	RawData []byte // Raw (possibly compressed) stream data
	Data    []byte // Decoded stream data (after filters applied)
}

func (*Stream) Type() string { return "stream" }
func (s *Stream) String() string {
	return fmt.Sprintf("stream(%d bytes)", len(s.RawData))
}

// GetFilter returns the filter name(s) for this stream
func (s *Stream) GetFilter() []string {
	filter := s.Dict.Get("Filter")
	if filter == nil {
		return nil
	}

	switch f := filter.(type) {
	case Name:
		return []string{string(f)}
	case Array:
		var filters []string
		for _, item := range f {
			if n, ok := item.(Name); ok {
				filters = append(filters, string(n))
			}
		}
		return filters
	}
	return nil
}

// GetLength returns the stream length
func (s *Stream) GetLength() int64 {
	return s.Dict.GetInt("Length")
}

// IndirectObject represents an indirect object definition (e.g., "5 0 obj ... endobj")
type IndirectObject struct {
	Num    int    // Object number
	Gen    int    // Generation number
	Object Object // The actual object
}

func (*IndirectObject) Type() string { return "indirect" }
func (o *IndirectObject) String() string {
	return fmt.Sprintf("%d %d obj %s endobj", o.Num, o.Gen, o.Object.String())
}

// Parser parses PDF objects from tokens
type Parser struct {
	lexer *Lexer
}

// NewParser creates a new PDF object parser
func NewParser(lexer *Lexer) *Parser {
	return &Parser{lexer: lexer}
}

// ParseObject parses a single PDF object
func (p *Parser) ParseObject() (Object, error) {
	tok, err := p.lexer.Next()
	if err != nil {
		return nil, err
	}

	return p.parseObjectFromToken(tok)
}

// parseObjectFromToken parses an object starting with the given token
func (p *Parser) parseObjectFromToken(tok Token) (Object, error) {
	switch tok.Type {
	case TokenNull:
		return Null{}, nil

	case TokenBool:
		return Boolean(tok.BoolValue()), nil

	case TokenInteger:
		// Could be start of indirect reference (num gen R)
		return p.parseIntegerOrRef(tok)

	case TokenReal:
		return Real(tok.FloatValue()), nil

	case TokenString:
		return String(tok.StringValue()), nil

	case TokenName:
		return Name(tok.StringValue()), nil

	case TokenArrayStart:
		return p.parseArray()

	case TokenDictStart:
		return p.parseDictOrStream()

	case TokenKeyword:
		// Handle keywords that might appear as values
		kw := tok.StringValue()
		switch kw {
		case "null":
			return Null{}, nil
		case "true":
			return Boolean(true), nil
		case "false":
			return Boolean(false), nil
		}
		return nil, fmt.Errorf("unexpected keyword: %s", kw)

	case TokenEOF:
		return nil, fmt.Errorf("unexpected EOF")

	default:
		return nil, fmt.Errorf("unexpected token: %v", tok)
	}
}

// parseIntegerOrRef handles the case where an integer might be part of a reference
func (p *Parser) parseIntegerOrRef(tok Token) (Object, error) {
	num := tok.IntValue()

	// Peek ahead to see if this is a reference
	tok2, err := p.lexer.Peek()
	if err != nil || tok2.Type != TokenInteger {
		return Integer(num), nil
	}

	// We have two integers, check for "R"
	p.lexer.Next() // consume second integer
	gen := tok2.IntValue()

	tok3, err := p.lexer.Peek()
	if err != nil || tok3.Type != TokenKeyword || tok3.StringValue() != "R" {
		// Not a reference - put back the second integer so it can be parsed separately
		p.lexer.Unread(tok2)
		return Integer(num), nil
	}

	p.lexer.Next() // consume "R"
	return &Ref{Num: int(num), Gen: int(gen)}, nil
}

// parseArray parses a PDF array
func (p *Parser) parseArray() (Array, error) {
	var arr Array

	for {
		tok, err := p.lexer.Peek()
		if err != nil {
			return nil, fmt.Errorf("unterminated array: %w", err)
		}

		if tok.Type == TokenArrayEnd {
			p.lexer.Next() // consume ]
			return arr, nil
		}

		obj, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("error parsing array element: %w", err)
		}
		arr = append(arr, obj)
	}
}

// parseDictOrStream parses a dictionary, which might be followed by stream data
func (p *Parser) parseDictOrStream() (Object, error) {
	dict, err := p.parseDict()
	if err != nil {
		return nil, err
	}

	// Check if followed by "stream" keyword
	tok, err := p.lexer.Peek()
	if err != nil || tok.Type != TokenKeyword || tok.StringValue() != "stream" {
		return dict, nil
	}

	// Parse stream
	p.lexer.Next() // consume "stream"

	// Read stream data
	// After "stream", there should be a newline, then data, then "endstream"
	// The Length in the dictionary tells us how many bytes to read
	length := dict.GetInt("Length")
	if length <= 0 {
		return nil, fmt.Errorf("stream has invalid Length: %d", length)
	}

	// Skip to start of stream data (after newline)
	_, err = p.lexer.ReadLine()
	if err != nil {
		return nil, fmt.Errorf("error reading stream start: %w", err)
	}

	// Read stream data
	data, err := p.lexer.ReadBytes(int(length))
	if err != nil {
		return nil, fmt.Errorf("error reading stream data: %w", err)
	}

	// Skip to "endstream"
	// There might be trailing whitespace before endstream
	for {
		tok, err = p.lexer.Next()
		if err != nil {
			return nil, fmt.Errorf("error finding endstream: %w", err)
		}
		if tok.Type == TokenKeyword && tok.StringValue() == "endstream" {
			break
		}
	}

	return &Stream{
		Dict:    dict,
		RawData: data,
	}, nil
}

// parseDict parses just the dictionary part (between << and >>)
func (p *Parser) parseDict() (Dict, error) {
	dict := make(Dict)

	for {
		tok, err := p.lexer.Peek()
		if err != nil {
			return nil, fmt.Errorf("unterminated dictionary: %w", err)
		}

		if tok.Type == TokenDictEnd {
			p.lexer.Next() // consume >>
			return dict, nil
		}

		// Read key (must be a name)
		keyTok, err := p.lexer.Next()
		if err != nil {
			return nil, fmt.Errorf("error reading dictionary key: %w", err)
		}
		if keyTok.Type != TokenName {
			return nil, fmt.Errorf("dictionary key must be a name, got %v", keyTok)
		}

		// Read value
		val, err := p.ParseObject()
		if err != nil {
			return nil, fmt.Errorf("error reading dictionary value for %s: %w", keyTok.StringValue(), err)
		}

		dict[Name(keyTok.StringValue())] = val
	}
}

// ParseIndirectObject parses an indirect object definition
func (p *Parser) ParseIndirectObject() (*IndirectObject, error) {
	// Read object number
	numTok, err := p.lexer.Next()
	if err != nil {
		return nil, err
	}
	if numTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected object number, got %v", numTok)
	}

	// Read generation number
	genTok, err := p.lexer.Next()
	if err != nil {
		return nil, err
	}
	if genTok.Type != TokenInteger {
		return nil, fmt.Errorf("expected generation number, got %v", genTok)
	}

	// Read "obj" keyword
	objTok, err := p.lexer.Next()
	if err != nil {
		return nil, err
	}
	if objTok.Type != TokenKeyword || objTok.StringValue() != "obj" {
		return nil, fmt.Errorf("expected 'obj', got %v", objTok)
	}

	// Parse the object
	obj, err := p.ParseObject()
	if err != nil {
		return nil, fmt.Errorf("error parsing object content: %w", err)
	}

	// Read "endobj" keyword
	endTok, err := p.lexer.Next()
	if err != nil {
		return nil, err
	}
	if endTok.Type != TokenKeyword || endTok.StringValue() != "endobj" {
		return nil, fmt.Errorf("expected 'endobj', got %v", endTok)
	}

	return &IndirectObject{
		Num:    int(numTok.IntValue()),
		Gen:    int(genTok.IntValue()),
		Object: obj,
	}, nil
}

// Utility functions for type assertions

// AsDict attempts to convert an object to a Dict
func AsDict(obj Object) (Dict, bool) {
	d, ok := obj.(Dict)
	return d, ok
}

// AsArray attempts to convert an object to an Array
func AsArray(obj Object) (Array, bool) {
	a, ok := obj.(Array)
	return a, ok
}

// AsStream attempts to convert an object to a Stream
func AsStream(obj Object) (*Stream, bool) {
	s, ok := obj.(*Stream)
	return s, ok
}

// AsRef attempts to convert an object to a Ref
func AsRef(obj Object) (*Ref, bool) {
	r, ok := obj.(*Ref)
	return r, ok
}

// AsInt returns the integer value of an object
func AsInt(obj Object) (int64, bool) {
	switch v := obj.(type) {
	case Integer:
		return int64(v), true
	case Real:
		return int64(v), true
	}
	return 0, false
}

// AsFloat returns the float value of an object
func AsFloat(obj Object) (float64, bool) {
	switch v := obj.(type) {
	case Real:
		return float64(v), true
	case Integer:
		return float64(v), true
	}
	return 0, false
}

// AsString returns the string value of an object
func AsString(obj Object) (string, bool) {
	switch v := obj.(type) {
	case String:
		return string(v), true
	case Name:
		return string(v), true
	}
	return "", false
}

// AsName returns the name value of an object
func AsName(obj Object) (string, bool) {
	if n, ok := obj.(Name); ok {
		return string(n), true
	}
	return "", false
}

// AsBool returns the boolean value of an object
func AsBool(obj Object) (bool, bool) {
	if b, ok := obj.(Boolean); ok {
		return bool(b), true
	}
	return false, false
}
