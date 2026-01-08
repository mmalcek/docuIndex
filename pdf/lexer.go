package pdf

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strconv"
)

// MaxStringLength limits the maximum size of strings/hex strings to prevent memory exhaustion
const MaxStringLength = 100 * 1024 * 1024 // 100MB

// TokenType represents the type of a PDF token
type TokenType int

const (
	TokenEOF TokenType = iota
	TokenError

	// Delimiters
	TokenArrayStart  // [
	TokenArrayEnd    // ]
	TokenDictStart   // <<
	TokenDictEnd     // >>
	TokenStringStart // (
	TokenStreamStart // stream keyword

	// Values
	TokenNull    // null
	TokenBool    // true, false
	TokenInteger // 123, -45
	TokenReal    // 1.23, -0.5
	TokenString  // (hello) or <48656C6C6F>
	TokenName    // /Name
	TokenKeyword // obj, endobj, R, stream, endstream, xref, trailer, startxref

	// Special
	TokenComment // %comment
)

// Token represents a lexical token from a PDF file
type Token struct {
	Type   TokenType
	Value  interface{} // string, int64, float64, bool, or nil
	Raw    []byte      // Raw bytes for the token
	Offset int64       // Byte offset in file
}

func (t Token) String() string {
	switch t.Type {
	case TokenEOF:
		return "EOF"
	case TokenError:
		return fmt.Sprintf("Error(%v)", t.Value)
	case TokenArrayStart:
		return "["
	case TokenArrayEnd:
		return "]"
	case TokenDictStart:
		return "<<"
	case TokenDictEnd:
		return ">>"
	case TokenNull:
		return "null"
	case TokenBool:
		return fmt.Sprintf("Bool(%v)", t.Value)
	case TokenInteger:
		return fmt.Sprintf("Int(%d)", t.Value)
	case TokenReal:
		return fmt.Sprintf("Real(%v)", t.Value)
	case TokenString:
		return fmt.Sprintf("String(%q)", t.Value)
	case TokenName:
		return fmt.Sprintf("Name(/%s)", t.Value)
	case TokenKeyword:
		return fmt.Sprintf("Keyword(%s)", t.Value)
	case TokenComment:
		return fmt.Sprintf("Comment(%s)", t.Value)
	default:
		return fmt.Sprintf("Token(%d, %v)", t.Type, t.Value)
	}
}

// IntValue returns the integer value or 0
func (t Token) IntValue() int64 {
	if v, ok := t.Value.(int64); ok {
		return v
	}
	return 0
}

// FloatValue returns the float value or 0
func (t Token) FloatValue() float64 {
	switch v := t.Value.(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}

// StringValue returns the string value or empty string
func (t Token) StringValue() string {
	if v, ok := t.Value.(string); ok {
		return v
	}
	return ""
}

// BoolValue returns the boolean value or false
func (t Token) BoolValue() bool {
	if v, ok := t.Value.(bool); ok {
		return v
	}
	return false
}

// Lexer tokenizes a PDF file
type Lexer struct {
	r        *bufio.Reader
	buf      bytes.Buffer
	offset   int64
	peeked   *Token
	unread   []Token // Stack of tokens to return before reading new ones
}

// NewLexer creates a new PDF lexer
func NewLexer(r io.Reader) *Lexer {
	return &Lexer{
		r: bufio.NewReaderSize(r, 64*1024), // 64KB buffer
	}
}

// NewLexerFromBytes creates a lexer from a byte slice
func NewLexerFromBytes(data []byte) *Lexer {
	return NewLexer(bytes.NewReader(data))
}

// Offset returns the current byte offset
func (l *Lexer) Offset() int64 {
	return l.offset
}

// Peek returns the next token without consuming it
func (l *Lexer) Peek() (Token, error) {
	if l.peeked != nil {
		return *l.peeked, nil
	}
	tok, err := l.Next()
	if err != nil {
		return tok, err
	}
	l.peeked = &tok
	return tok, nil
}

// Unread pushes a token back to be returned by the next call to Next
func (l *Lexer) Unread(tok Token) {
	l.unread = append(l.unread, tok)
}

// Next returns the next token
func (l *Lexer) Next() (Token, error) {
	// First check unread stack
	if len(l.unread) > 0 {
		tok := l.unread[len(l.unread)-1]
		l.unread = l.unread[:len(l.unread)-1]
		return tok, nil
	}
	// Then check peeked
	if l.peeked != nil {
		tok := *l.peeked
		l.peeked = nil
		return tok, nil
	}
	return l.readToken()
}

// readToken reads the next token from the input
func (l *Lexer) readToken() (Token, error) {
	l.skipWhitespaceAndComments()

	startOffset := l.offset

	b, err := l.readByte()
	if err == io.EOF {
		return Token{Type: TokenEOF, Offset: startOffset}, nil
	}
	if err != nil {
		return l.errorToken(startOffset, err.Error()), err
	}

	switch b {
	case '[':
		return Token{Type: TokenArrayStart, Raw: []byte{'['}, Offset: startOffset}, nil

	case ']':
		return Token{Type: TokenArrayEnd, Raw: []byte{']'}, Offset: startOffset}, nil

	case '(':
		return l.readLiteralString(startOffset)

	case '<':
		// Check for << (dict start) or hex string
		next, err := l.peekByte()
		if err == nil && next == '<' {
			l.readByte() // consume second <
			return Token{Type: TokenDictStart, Raw: []byte{'<', '<'}, Offset: startOffset}, nil
		}
		return l.readHexString(startOffset)

	case '>':
		// Check for >> (dict end)
		next, err := l.peekByte()
		if err == nil && next == '>' {
			l.readByte() // consume second >
			return Token{Type: TokenDictEnd, Raw: []byte{'>', '>'}, Offset: startOffset}, nil
		}
		return l.errorToken(startOffset, "unexpected '>'"), fmt.Errorf("unexpected '>'")

	case '/':
		return l.readName(startOffset)

	case '%':
		return l.readComment(startOffset)

	default:
		l.unreadByte()
		if isDigit(b) || b == '-' || b == '+' || b == '.' {
			return l.readNumber(startOffset)
		}
		if isRegular(b) {
			return l.readKeyword(startOffset)
		}
		return l.errorToken(startOffset, fmt.Sprintf("unexpected byte 0x%02X", b)), fmt.Errorf("unexpected byte 0x%02X", b)
	}
}

// readLiteralString reads a string like (Hello World)
func (l *Lexer) readLiteralString(startOffset int64) (Token, error) {
	l.buf.Reset()
	depth := 1 // we've already read the opening (

	for depth > 0 {
		// Prevent unbounded memory growth
		if l.buf.Len() > MaxStringLength {
			return l.errorToken(startOffset, "string too long"), fmt.Errorf("string exceeds maximum length of %d bytes", MaxStringLength)
		}

		b, err := l.readByte()
		if err != nil {
			return l.errorToken(startOffset, "unterminated string"), fmt.Errorf("unterminated string")
		}

		switch b {
		case '(':
			depth++
			l.buf.WriteByte(b)
		case ')':
			depth--
			if depth > 0 {
				l.buf.WriteByte(b)
			}
		case '\\':
			// Escape sequence
			escaped, err := l.readEscapeSequence()
			if err != nil {
				return l.errorToken(startOffset, err.Error()), err
			}
			l.buf.Write(escaped)
		case '\r':
			// Normalize \r and \r\n to \n
			next, _ := l.peekByte()
			if next == '\n' {
				l.readByte()
			}
			l.buf.WriteByte('\n')
		default:
			l.buf.WriteByte(b)
		}
	}

	return Token{
		Type:   TokenString,
		Value:  l.buf.String(),
		Raw:    l.buf.Bytes(),
		Offset: startOffset,
	}, nil
}

// readEscapeSequence handles \n, \r, \t, \\, \(, \), \ddd (octal)
func (l *Lexer) readEscapeSequence() ([]byte, error) {
	b, err := l.readByte()
	if err != nil {
		return nil, fmt.Errorf("unterminated escape sequence")
	}

	switch b {
	case 'n':
		return []byte{'\n'}, nil
	case 'r':
		return []byte{'\r'}, nil
	case 't':
		return []byte{'\t'}, nil
	case 'b':
		return []byte{'\b'}, nil
	case 'f':
		return []byte{'\f'}, nil
	case '\\':
		return []byte{'\\'}, nil
	case '(':
		return []byte{'('}, nil
	case ')':
		return []byte{')'}, nil
	case '\r':
		// Line continuation: \<newline> means nothing
		next, _ := l.peekByte()
		if next == '\n' {
			l.readByte()
		}
		return nil, nil
	case '\n':
		// Line continuation
		return nil, nil
	default:
		// Check for octal escape \ddd
		if isOctalDigit(b) {
			octal := []byte{b}
			for i := 0; i < 2; i++ {
				next, err := l.peekByte()
				if err != nil || !isOctalDigit(next) {
					break
				}
				l.readByte()
				octal = append(octal, next)
			}
			val, _ := strconv.ParseInt(string(octal), 8, 16)
			return []byte{byte(val)}, nil
		}
		// Unknown escape, just return the character
		return []byte{b}, nil
	}
}

// readHexString reads a hex string like <48656C6C6F>
func (l *Lexer) readHexString(startOffset int64) (Token, error) {
	l.buf.Reset()
	var hexBuf bytes.Buffer

	for {
		// Prevent unbounded memory growth
		if hexBuf.Len() > MaxStringLength*2 {
			return l.errorToken(startOffset, "hex string too long"), fmt.Errorf("hex string exceeds maximum length")
		}

		b, err := l.readByte()
		if err != nil {
			return l.errorToken(startOffset, "unterminated hex string"), fmt.Errorf("unterminated hex string")
		}

		if b == '>' {
			break
		}

		if isWhitespace(b) {
			continue // whitespace is ignored in hex strings
		}

		if !isHexDigit(b) {
			return l.errorToken(startOffset, "invalid hex character"), fmt.Errorf("invalid hex character: %c", b)
		}

		hexBuf.WriteByte(b)
	}

	// Pad with trailing 0 if odd length
	hexStr := hexBuf.String()
	if len(hexStr)%2 == 1 {
		hexStr += "0"
	}

	// Decode hex
	for i := 0; i < len(hexStr); i += 2 {
		val, _ := strconv.ParseInt(hexStr[i:i+2], 16, 16)
		l.buf.WriteByte(byte(val))
	}

	return Token{
		Type:   TokenString,
		Value:  l.buf.String(),
		Raw:    l.buf.Bytes(),
		Offset: startOffset,
	}, nil
}

// readName reads a name like /Type or /Font
func (l *Lexer) readName(startOffset int64) (Token, error) {
	l.buf.Reset()

	for {
		b, err := l.peekByte()
		if err != nil || isDelimiter(b) || isWhitespace(b) {
			break
		}
		l.readByte()

		// Handle #XX hex escapes in names
		if b == '#' {
			hex1, err1 := l.readByte()
			hex2, err2 := l.readByte()
			if err1 != nil || err2 != nil || !isHexDigit(hex1) || !isHexDigit(hex2) {
				return l.errorToken(startOffset, "invalid name escape"), fmt.Errorf("invalid name escape")
			}
			val, _ := strconv.ParseInt(string([]byte{hex1, hex2}), 16, 16)
			l.buf.WriteByte(byte(val))
		} else {
			l.buf.WriteByte(b)
		}
	}

	return Token{
		Type:   TokenName,
		Value:  l.buf.String(),
		Raw:    l.buf.Bytes(),
		Offset: startOffset,
	}, nil
}

// readNumber reads an integer or real number
func (l *Lexer) readNumber(startOffset int64) (Token, error) {
	l.buf.Reset()
	isReal := false

	for {
		b, err := l.peekByte()
		if err != nil || (!isDigit(b) && b != '.' && b != '-' && b != '+') {
			break
		}
		l.readByte()
		if b == '.' {
			isReal = true
		}
		l.buf.WriteByte(b)
	}

	str := l.buf.String()

	if isReal {
		val, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return l.errorToken(startOffset, "invalid real number"), err
		}
		return Token{
			Type:   TokenReal,
			Value:  val,
			Raw:    []byte(str),
			Offset: startOffset,
		}, nil
	}

	val, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		// Could be a real without decimal point but with E notation
		fval, ferr := strconv.ParseFloat(str, 64)
		if ferr == nil {
			return Token{
				Type:   TokenReal,
				Value:  fval,
				Raw:    []byte(str),
				Offset: startOffset,
			}, nil
		}
		return l.errorToken(startOffset, "invalid integer"), err
	}

	return Token{
		Type:   TokenInteger,
		Value:  val,
		Raw:    []byte(str),
		Offset: startOffset,
	}, nil
}

// readKeyword reads a keyword like obj, endobj, true, false, null, R, stream, etc.
func (l *Lexer) readKeyword(startOffset int64) (Token, error) {
	l.buf.Reset()

	for {
		b, err := l.peekByte()
		if err != nil || isDelimiter(b) || isWhitespace(b) {
			break
		}
		l.readByte()
		l.buf.WriteByte(b)
	}

	str := l.buf.String()

	switch str {
	case "null":
		return Token{Type: TokenNull, Value: nil, Raw: []byte(str), Offset: startOffset}, nil
	case "true":
		return Token{Type: TokenBool, Value: true, Raw: []byte(str), Offset: startOffset}, nil
	case "false":
		return Token{Type: TokenBool, Value: false, Raw: []byte(str), Offset: startOffset}, nil
	default:
		return Token{Type: TokenKeyword, Value: str, Raw: []byte(str), Offset: startOffset}, nil
	}
}

// readComment reads a comment starting with %
func (l *Lexer) readComment(startOffset int64) (Token, error) {
	l.buf.Reset()

	for {
		b, err := l.readByte()
		if err != nil || b == '\r' || b == '\n' {
			break
		}
		l.buf.WriteByte(b)
	}

	return Token{
		Type:   TokenComment,
		Value:  l.buf.String(),
		Raw:    l.buf.Bytes(),
		Offset: startOffset,
	}, nil
}

// skipWhitespaceAndComments skips whitespace and comments
func (l *Lexer) skipWhitespaceAndComments() {
	for {
		b, err := l.peekByte()
		if err != nil {
			return
		}

		if isWhitespace(b) {
			l.readByte()
			continue
		}

		if b == '%' {
			// Skip comment line
			for {
				b, err := l.readByte()
				if err != nil || b == '\r' || b == '\n' {
					break
				}
			}
			continue
		}

		return
	}
}

// readByte reads a single byte and updates offset
func (l *Lexer) readByte() (byte, error) {
	b, err := l.r.ReadByte()
	if err == nil {
		l.offset++
	}
	return b, err
}

// unreadByte unreads the last byte
func (l *Lexer) unreadByte() error {
	err := l.r.UnreadByte()
	if err == nil {
		l.offset--
	}
	return err
}

// peekByte looks at the next byte without consuming it
func (l *Lexer) peekByte() (byte, error) {
	b, err := l.r.ReadByte()
	if err != nil {
		return 0, err
	}
	l.r.UnreadByte()
	return b, nil
}

// errorToken creates an error token
func (l *Lexer) errorToken(offset int64, msg string) Token {
	return Token{
		Type:   TokenError,
		Value:  msg,
		Offset: offset,
	}
}

// ReadLine reads bytes until end of line (for reading stream data)
func (l *Lexer) ReadLine() ([]byte, error) {
	line, err := l.r.ReadBytes('\n')
	l.offset += int64(len(line))
	// Trim trailing \r\n or \n
	line = bytes.TrimRight(line, "\r\n")
	return line, err
}

// ReadBytes reads exactly n bytes
func (l *Lexer) ReadBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := io.ReadFull(l.r, buf)
	l.offset += int64(read)
	return buf[:read], err
}

// Skip skips n bytes
func (l *Lexer) Skip(n int64) error {
	skipped, err := l.r.Discard(int(n))
	l.offset += int64(skipped)
	return err
}

// Helper functions

func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f' || b == 0
}

func isDelimiter(b byte) bool {
	return b == '(' || b == ')' || b == '<' || b == '>' ||
		b == '[' || b == ']' || b == '{' || b == '}' ||
		b == '/' || b == '%'
}

func isRegular(b byte) bool {
	return !isWhitespace(b) && !isDelimiter(b)
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isOctalDigit(b byte) bool {
	return b >= '0' && b <= '7'
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}
