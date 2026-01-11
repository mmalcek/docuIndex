package pdf

import (
	"fmt"
	"math"
)

// Matrix represents a 2D transformation matrix [a b c d e f]
// Transforms: x' = a*x + c*y + e, y' = b*x + d*y + f
type Matrix [6]float64

// IdentityMatrix is the identity transformation
var IdentityMatrix = Matrix{1, 0, 0, 1, 0, 0}

// Multiply multiplies two matrices
func (m Matrix) Multiply(n Matrix) Matrix {
	return Matrix{
		m[0]*n[0] + m[1]*n[2],
		m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2],
		m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4],
		m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

// Transform applies the matrix to a point
func (m Matrix) Transform(x, y float64) (float64, float64) {
	return m[0]*x + m[2]*y + m[4], m[1]*x + m[3]*y + m[5]
}

// TranslateMatrix creates a translation matrix
func TranslateMatrix(tx, ty float64) Matrix {
	return Matrix{1, 0, 0, 1, tx, ty}
}

// ScaleMatrix creates a scaling matrix
func ScaleMatrix(sx, sy float64) Matrix {
	return Matrix{sx, 0, 0, sy, 0, 0}
}

// RotateMatrix creates a rotation matrix (angle in radians)
func RotateMatrix(angle float64) Matrix {
	cos := math.Cos(angle)
	sin := math.Sin(angle)
	return Matrix{cos, sin, -sin, cos, 0, 0}
}

// ScaleFactor returns the average scaling factor of the matrix
// This is used to convert font sizes from text space to user space
func (m Matrix) ScaleFactor() float64 {
	// The scaling is determined by how a unit vector transforms
	// Use the average of x and y scale for isotropic-ish scaling
	sx := math.Sqrt(m[0]*m[0] + m[1]*m[1])
	sy := math.Sqrt(m[2]*m[2] + m[3]*m[3])
	return (sx + sy) / 2
}

// GraphicsState holds the current graphics state
type GraphicsState struct {
	CTM           Matrix  // Current Transformation Matrix
	LineWidth     float64
	LineCap       int
	LineJoin      int
	MiterLimit    float64
	DashArray     []float64
	DashPhase     float64
	StrokeColor   Color
	FillColor     Color
	TextState     TextState
}

// TextState holds text-specific state
type TextState struct {
	CharSpace   float64 // Character spacing (Tc)
	WordSpace   float64 // Word spacing (Tw)
	Scale       float64 // Horizontal scaling (Tz), 100 = normal
	Leading     float64 // Text leading (TL)
	FontName    string  // Current font name
	FontSize    float64 // Current font size (Tf)
	RenderMode  int     // Text rendering mode (Tr)
	Rise        float64 // Text rise (Ts)
	Tm          Matrix  // Text matrix
	Tlm         Matrix  // Text line matrix
}

// Color represents a color value
type Color struct {
	ColorSpace string    // DeviceGray, DeviceRGB, DeviceCMYK, etc.
	Values     []float64 // Color component values
}

// TextSpan represents extracted text with position
type TextSpan struct {
	Text     string
	X, Y     float64 // Position in user space
	Width    float64 // Width of the text span
	FontName string
	FontSize float64
	SeqNum   int     // Sequence number in the content stream (for ordering)
}

// ImageRef represents a reference to an image in the content stream
type ImageRef struct {
	Name   string  // XObject name
	X, Y   float64 // Position
	Width  float64 // Display width
	Height float64 // Display height
}

// ContentHandler receives content stream events
type ContentHandler interface {
	// Text operations
	OnTextSpan(span TextSpan)
	// Image operations
	OnImage(img ImageRef)
}

// MaxGraphicsStateDepth limits graphics state stack to prevent memory exhaustion
const MaxGraphicsStateDepth = 1000

// MaxFormXObjectDepth limits nested form XObject recursion
const MaxFormXObjectDepth = 50

// MaxOperandStackSize limits operand stack to prevent memory exhaustion from malformed PDFs
const MaxOperandStackSize = 10000

// ContentInterpreter interprets PDF content streams
type ContentInterpreter struct {
	page             *Page
	fonts            *FontCache
	handler          ContentHandler
	gstack           []GraphicsState // Graphics state stack
	state            GraphicsState   // Current state
	operands         []Object        // Operand stack
	textObject       bool            // Inside BT...ET
	formXObjectDepth int             // Current depth of form XObject recursion
	spanSeqNum       int             // Sequence number for text span ordering
}

// NewContentInterpreter creates a new content stream interpreter
func NewContentInterpreter(page *Page, handler ContentHandler) *ContentInterpreter {
	return &ContentInterpreter{
		page:    page,
		fonts:   NewFontCache(page.doc),
		handler: handler,
		gstack:  make([]GraphicsState, 0, 16),
		state: GraphicsState{
			CTM:       IdentityMatrix,
			LineWidth: 1.0,
			TextState: TextState{
				Scale: 100,
				Tm:    IdentityMatrix,
				Tlm:   IdentityMatrix,
			},
		},
	}
}

// Execute interprets the page's content stream
func (ci *ContentInterpreter) Execute() error {
	content, err := ci.page.GetContents()
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return nil
	}

	lexer := NewLexerFromBytes(content)

	for {
		tok, err := lexer.Next()
		if err != nil {
			return fmt.Errorf("lexer error: %w", err)
		}

		if tok.Type == TokenEOF {
			break
		}

		switch tok.Type {
		case TokenKeyword:
			if err := ci.executeOperator(tok.StringValue()); err != nil {
				// Log error but continue
			}
			ci.operands = ci.operands[:0] // Clear operands after operator
		case TokenInteger:
			ci.pushOperand(Integer(tok.IntValue()))
		case TokenReal:
			ci.pushOperand(Real(tok.FloatValue()))
		case TokenString:
			ci.pushOperand(String(tok.StringValue()))
		case TokenName:
			ci.pushOperand(Name(tok.StringValue()))
		case TokenArrayStart:
			// Parse inline array
			arr, err := ci.parseInlineArray(lexer)
			if err != nil {
				return err
			}
			ci.pushOperand(arr)
		case TokenBool:
			ci.pushOperand(Boolean(tok.BoolValue()))
		}
	}

	return nil
}

// parseInlineArray parses an array from the content stream
func (ci *ContentInterpreter) parseInlineArray(lexer *Lexer) (Array, error) {
	var arr Array
	for {
		tok, err := lexer.Next()
		if err != nil {
			return nil, err
		}
		if tok.Type == TokenArrayEnd {
			return arr, nil
		}
		switch tok.Type {
		case TokenInteger:
			arr = append(arr, Integer(tok.IntValue()))
		case TokenReal:
			arr = append(arr, Real(tok.FloatValue()))
		case TokenString:
			arr = append(arr, String(tok.StringValue()))
		case TokenName:
			arr = append(arr, Name(tok.StringValue()))
		}
	}
}

// executeOperator executes a PDF operator
func (ci *ContentInterpreter) executeOperator(op string) error {
	switch op {
	// Graphics state operators
	case "q":
		// Limit stack depth to prevent memory exhaustion
		if len(ci.gstack) < MaxGraphicsStateDepth {
			ci.gstack = append(ci.gstack, ci.state)
		}
	case "Q":
		if len(ci.gstack) > 0 {
			ci.state = ci.gstack[len(ci.gstack)-1]
			ci.gstack = ci.gstack[:len(ci.gstack)-1]
		}
	case "cm":
		if len(ci.operands) >= 6 {
			m := Matrix{
				ci.getFloat(0), ci.getFloat(1),
				ci.getFloat(2), ci.getFloat(3),
				ci.getFloat(4), ci.getFloat(5),
			}
			ci.state.CTM = ci.state.CTM.Multiply(m)
		}
	case "w":
		if len(ci.operands) >= 1 {
			ci.state.LineWidth = ci.getFloat(0)
		}
	case "J":
		if len(ci.operands) >= 1 {
			ci.state.LineCap = int(ci.getFloat(0))
		}
	case "j":
		if len(ci.operands) >= 1 {
			ci.state.LineJoin = int(ci.getFloat(0))
		}
	case "M":
		if len(ci.operands) >= 1 {
			ci.state.MiterLimit = ci.getFloat(0)
		}
	case "d":
		// Dash pattern [array phase]
		if len(ci.operands) >= 2 {
			if arr, ok := ci.operands[0].(Array); ok {
				ci.state.DashArray = make([]float64, len(arr))
				for i, v := range arr {
					if f, ok := AsFloat(v); ok {
						ci.state.DashArray[i] = f
					}
				}
			}
			ci.state.DashPhase = ci.getFloat(1)
		}

	// Text operators
	case "BT":
		// BT should not be nested - if already in text object, ignore
		if !ci.textObject {
			ci.textObject = true
			ci.state.TextState.Tm = IdentityMatrix
			ci.state.TextState.Tlm = IdentityMatrix
		}
	case "ET":
		// ET without BT is ignored
		ci.textObject = false
	case "Tc":
		if len(ci.operands) >= 1 {
			ci.state.TextState.CharSpace = ci.getFloat(0)
		}
	case "Tw":
		if len(ci.operands) >= 1 {
			ci.state.TextState.WordSpace = ci.getFloat(0)
		}
	case "Tz":
		if len(ci.operands) >= 1 {
			ci.state.TextState.Scale = ci.getFloat(0)
		}
	case "TL":
		if len(ci.operands) >= 1 {
			ci.state.TextState.Leading = ci.getFloat(0)
		}
	case "Tf":
		if len(ci.operands) >= 2 {
			ci.state.TextState.FontName = ci.getName(0)
			ci.state.TextState.FontSize = ci.getFloat(1)
		}
	case "Tr":
		if len(ci.operands) >= 1 {
			ci.state.TextState.RenderMode = int(ci.getFloat(0))
		}
	case "Ts":
		if len(ci.operands) >= 1 {
			ci.state.TextState.Rise = ci.getFloat(0)
		}
	case "Td":
		if len(ci.operands) >= 2 {
			tx, ty := ci.getFloat(0), ci.getFloat(1)
			ci.state.TextState.Tlm = ci.state.TextState.Tlm.Multiply(TranslateMatrix(tx, ty))
			ci.state.TextState.Tm = ci.state.TextState.Tlm
		}
	case "TD":
		if len(ci.operands) >= 2 {
			tx, ty := ci.getFloat(0), ci.getFloat(1)
			ci.state.TextState.Leading = -ty
			ci.state.TextState.Tlm = ci.state.TextState.Tlm.Multiply(TranslateMatrix(tx, ty))
			ci.state.TextState.Tm = ci.state.TextState.Tlm
		}
	case "Tm":
		if len(ci.operands) >= 6 {
			ci.state.TextState.Tm = Matrix{
				ci.getFloat(0), ci.getFloat(1),
				ci.getFloat(2), ci.getFloat(3),
				ci.getFloat(4), ci.getFloat(5),
			}
			ci.state.TextState.Tlm = ci.state.TextState.Tm
		}
	case "T*":
		ci.state.TextState.Tlm = ci.state.TextState.Tlm.Multiply(
			TranslateMatrix(0, -ci.state.TextState.Leading))
		ci.state.TextState.Tm = ci.state.TextState.Tlm
	case "Tj":
		if len(ci.operands) >= 1 {
			ci.showText(ci.getString(0))
		}
	case "TJ":
		if len(ci.operands) >= 1 {
			ci.showTextArray(ci.operands[0])
		}
	case "'":
		// Move to next line and show text
		ci.state.TextState.Tlm = ci.state.TextState.Tlm.Multiply(
			TranslateMatrix(0, -ci.state.TextState.Leading))
		ci.state.TextState.Tm = ci.state.TextState.Tlm
		if len(ci.operands) >= 1 {
			ci.showText(ci.getString(0))
		}
	case "\"":
		// Set word and char spacing, move to next line and show text
		if len(ci.operands) >= 3 {
			ci.state.TextState.WordSpace = ci.getFloat(0)
			ci.state.TextState.CharSpace = ci.getFloat(1)
			ci.state.TextState.Tlm = ci.state.TextState.Tlm.Multiply(
				TranslateMatrix(0, -ci.state.TextState.Leading))
			ci.state.TextState.Tm = ci.state.TextState.Tlm
			ci.showText(ci.getString(2))
		}

	// XObject (images, forms)
	case "Do":
		if len(ci.operands) >= 1 {
			ci.doXObject(ci.getName(0))
		}

	// Color operators
	case "g":
		if len(ci.operands) >= 1 {
			ci.state.FillColor = Color{ColorSpace: "DeviceGray", Values: []float64{ci.getFloat(0)}}
		}
	case "G":
		if len(ci.operands) >= 1 {
			ci.state.StrokeColor = Color{ColorSpace: "DeviceGray", Values: []float64{ci.getFloat(0)}}
		}
	case "rg":
		if len(ci.operands) >= 3 {
			ci.state.FillColor = Color{ColorSpace: "DeviceRGB", Values: []float64{
				ci.getFloat(0), ci.getFloat(1), ci.getFloat(2),
			}}
		}
	case "RG":
		if len(ci.operands) >= 3 {
			ci.state.StrokeColor = Color{ColorSpace: "DeviceRGB", Values: []float64{
				ci.getFloat(0), ci.getFloat(1), ci.getFloat(2),
			}}
		}
	case "k":
		if len(ci.operands) >= 4 {
			ci.state.FillColor = Color{ColorSpace: "DeviceCMYK", Values: []float64{
				ci.getFloat(0), ci.getFloat(1), ci.getFloat(2), ci.getFloat(3),
			}}
		}
	case "K":
		if len(ci.operands) >= 4 {
			ci.state.StrokeColor = Color{ColorSpace: "DeviceCMYK", Values: []float64{
				ci.getFloat(0), ci.getFloat(1), ci.getFloat(2), ci.getFloat(3),
			}}
		}

	// Path construction operators (we skip these for text extraction)
	case "m", "l", "c", "v", "y", "h", "re":
		// Path construction - skip
	case "S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n":
		// Path painting - skip
	case "W", "W*":
		// Clipping - skip
	}

	return nil
}

// showText processes a text string
func (ci *ContentInterpreter) showText(text string) {
	if ci.handler == nil || text == "" {
		return
	}

	// Text rendering mode 3 = invisible text (used for OCR layers, etc.)
	// Don't extract invisible text as visible content
	if ci.state.TextState.RenderMode == 3 {
		// Still need to advance position even for invisible text
		fonts, _ := ci.page.GetFonts()
		if fontDict, ok := fonts[ci.state.TextState.FontName]; ok {
			if font, err := ci.fonts.GetFont(ci.state.TextState.FontName, fontDict); err == nil {
				ci.advanceTextPosition(text, font)
			}
		}
		return
	}

	// Get font
	fonts, _ := ci.page.GetFonts()
	fontDict, ok := fonts[ci.state.TextState.FontName]
	if !ok {
		return
	}

	font, err := ci.fonts.GetFont(ci.state.TextState.FontName, fontDict)
	if err != nil {
		return
	}

	// Emit the entire text string as one span, but store accurate positions
	// The text matrix already tracks position correctly via showTextArray adjustments
	// We just need to record where each string starts and its decoded content

	// Get current position (this is where the text string starts)
	x, y := ci.getTextPosition()

	// Decode the entire text
	decoded := font.Decode([]byte(text))
	if decoded == "" {
		// Still need to advance position for proper TJ handling
		ci.advanceTextPosition(text, font)
		return
	}

	// Advance text matrix by text-space width
	data := []byte(text)
	var textSpaceWidth float64

	if font.IsCID {
		for i := 0; i+1 < len(data); i += 2 {
			code := int(data[i])<<8 | int(data[i+1])
			w := font.GetWidth(code) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			if code == 0x0020 {
				w += ci.state.TextState.WordSpace
			}
			textSpaceWidth += w
		}
	} else {
		for _, b := range data {
			w := font.GetWidth(int(b)) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			if b == ' ' {
				w += ci.state.TextState.WordSpace
			}
			textSpaceWidth += w
		}
	}
	textSpaceWidth *= ci.state.TextState.Scale / 100.0

	// Advance text matrix
	ci.state.TextState.Tm = ci.state.TextState.Tm.Multiply(TranslateMatrix(textSpaceWidth, 0))

	// Calculate actual user-space width by measuring position change
	// This accounts for CTM transformation and any scaling
	x2, _ := ci.getTextPosition()
	userWidth := x2 - x
	if userWidth < 0 {
		userWidth = 0
	}

	// Calculate effective font size in user space
	// The raw FontSize is in text space; we need to scale it by CTM
	ctmScale := ci.state.CTM.ScaleFactor()
	effectiveFontSize := ci.state.TextState.FontSize * ctmScale

	// Emit the span - buildLineText will use X positions from TJ adjustments
	// to detect word boundaries, not the Width field
	ci.spanSeqNum++
	ci.handler.OnTextSpan(TextSpan{
		Text:     decoded,
		X:        x,
		Y:        y,
		Width:    userWidth,
		FontName: ci.state.TextState.FontName,
		FontSize: effectiveFontSize,
		SeqNum:   ci.spanSeqNum,
	})
}

// showTextArray processes a TJ array
func (ci *ContentInterpreter) showTextArray(obj Object) {
	arr, ok := obj.(Array)
	if !ok {
		return
	}

	for _, item := range arr {
		switch v := item.(type) {
		case String:
			ci.showText(string(v))
		case Integer, Real:
			adjust, _ := AsFloat(v)

			// TJ array: numeric values adjust text position in thousandths of em
			// Negative adjust -> positive tx (move right/increase position)
			// Positive adjust -> negative tx (move left/decrease position)
			//
			// We don't emit spaces here - let buildLineText handle spacing
			// based on actual position gaps
			tx := -adjust / 1000.0 * ci.state.TextState.FontSize * ci.state.TextState.Scale / 100.0
			ci.state.TextState.Tm = ci.state.TextState.Tm.Multiply(TranslateMatrix(tx, 0))
		}
	}
}

// getTextPosition returns the current text position in user space
func (ci *ContentInterpreter) getTextPosition() (float64, float64) {
	// Apply text matrix, then CTM
	tm := ci.state.TextState.Tm
	ctm := ci.state.CTM
	combined := tm.Multiply(ctm)
	x, y := combined[4], combined[5]

	// Apply page rotation correction
	rotation := ci.page.GetRotation()
	if rotation != 0 {
		mediaBox := ci.page.MediaBox()
		width := mediaBox.Width()
		height := mediaBox.Height()

		switch rotation {
		case 90:
			// Rotate 90 CW: (x, y) -> (y, width - x)
			x, y = y, width-x
		case 180:
			// Rotate 180: (x, y) -> (width - x, height - y)
			x, y = width-x, height-y
		case 270:
			// Rotate 270 CW (90 CCW): (x, y) -> (height - y, x)
			x, y = height-y, x
		}
	}

	return x, y
}

// calculateTextWidth calculates the width of text in user space units
func (ci *ContentInterpreter) calculateTextWidth(text string, font *Font) float64 {
	data := []byte(text)
	var width float64

	if font.IsCID {
		// CID fonts use 2-byte character codes
		for i := 0; i+1 < len(data); i += 2 {
			code := int(data[i])<<8 | int(data[i+1])
			w := font.GetWidth(code) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			// Word space applies to single-byte space (0x20) which maps to CID space
			if code == 0x0020 {
				w += ci.state.TextState.WordSpace
			}
			width += w
		}
	} else {
		// Simple fonts use 1-byte character codes
		for _, b := range data {
			w := font.GetWidth(int(b)) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			if b == ' ' {
				w += ci.state.TextState.WordSpace
			}
			width += w
		}
	}

	// Apply horizontal scaling
	width *= ci.state.TextState.Scale / 100.0

	// Transform width through the CTM to get user space width
	// We only need the horizontal scaling component
	ctm := ci.state.CTM
	return width * ctm[0]
}

// advanceTextPosition advances the text position
func (ci *ContentInterpreter) advanceTextPosition(text string, font *Font) {
	data := []byte(text)
	var width float64

	if font.IsCID {
		// CID fonts use 2-byte character codes
		for i := 0; i+1 < len(data); i += 2 {
			code := int(data[i])<<8 | int(data[i+1])
			w := font.GetWidth(code) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			if code == 0x0020 {
				w += ci.state.TextState.WordSpace
			}
			width += w
		}
	} else {
		// Simple fonts use 1-byte character codes
		for _, b := range data {
			w := font.GetWidth(int(b)) / 1000.0 * ci.state.TextState.FontSize
			w += ci.state.TextState.CharSpace
			if b == ' ' {
				w += ci.state.TextState.WordSpace
			}
			width += w
		}
	}

	// Apply horizontal scaling
	width *= ci.state.TextState.Scale / 100.0

	// Move text position
	ci.state.TextState.Tm = ci.state.TextState.Tm.Multiply(TranslateMatrix(width, 0))
}

// doXObject processes an XObject reference
func (ci *ContentInterpreter) doXObject(name string) {
	xobjects, err := ci.page.GetXObjects()
	if err != nil {
		return
	}

	xobj, ok := xobjects[name]
	if !ok {
		return
	}

	stream, ok := xobj.(*Stream)
	if !ok {
		return
	}

	subtype := stream.Dict.GetName("Subtype")
	switch subtype {
	case "Image":
		if ci.handler != nil {
			// Get image dimensions from current transformation
			width := ci.state.CTM[0]
			height := ci.state.CTM[3]
			x, y := ci.state.CTM[4], ci.state.CTM[5]

			ci.handler.OnImage(ImageRef{
				Name:   name,
				X:      x,
				Y:      y,
				Width:  width,
				Height: height,
			})
		}
	case "Form":
		ci.processFormXObject(stream)
	}
}

// processFormXObject recursively processes a form XObject content stream
func (ci *ContentInterpreter) processFormXObject(stream *Stream) {
	// Prevent infinite recursion
	if ci.formXObjectDepth >= MaxFormXObjectDepth {
		return
	}
	ci.formXObjectDepth++
	defer func() { ci.formXObjectDepth-- }()

	// Save current graphics state
	ci.gstack = append(ci.gstack, ci.state)
	defer func() {
		if len(ci.gstack) > 0 {
			ci.state = ci.gstack[len(ci.gstack)-1]
			ci.gstack = ci.gstack[:len(ci.gstack)-1]
		}
	}()

	// Apply the form's transformation matrix if present
	if matrixArr := stream.Dict.GetArray("Matrix"); matrixArr != nil && len(matrixArr) >= 6 {
		formMatrix := Matrix{
			matrixArr.GetFloat(0), matrixArr.GetFloat(1),
			matrixArr.GetFloat(2), matrixArr.GetFloat(3),
			matrixArr.GetFloat(4), matrixArr.GetFloat(5),
		}
		ci.state.CTM = formMatrix.Multiply(ci.state.CTM)
	}

	// Get form resources (merge with page resources)
	formResources := stream.Dict.GetDict("Resources")
	if formResources != nil {
		// Merge form fonts with page fonts
		if formFonts := formResources.GetDict("Font"); formFonts != nil {
			for fontName, fontObj := range formFonts {
				resolved, err := ci.page.doc.Resolve(fontObj)
				if err == nil {
					if fontDict, ok := resolved.(Dict); ok {
						ci.fonts.GetFont(string(fontName), fontDict)
					}
				}
			}
		}
	}

	// Decode and execute the form content stream
	data, err := DecodeStream(stream)
	if err != nil {
		return
	}

	// Parse and execute the content stream
	lexer := NewLexerFromBytes(data)
	for {
		tok, err := lexer.Next()
		if err != nil || tok.Type == TokenEOF {
			break
		}

		switch tok.Type {
		case TokenKeyword:
			ci.executeOperator(tok.StringValue())
			ci.operands = ci.operands[:0]
		case TokenInteger:
			ci.pushOperand(Integer(tok.IntValue()))
		case TokenReal:
			ci.pushOperand(Real(tok.FloatValue()))
		case TokenString:
			ci.pushOperand(String(tok.StringValue()))
		case TokenName:
			ci.pushOperand(Name(tok.StringValue()))
		case TokenArrayStart:
			arr, err := ci.parseInlineArray(lexer)
			if err == nil {
				ci.pushOperand(arr)
			}
		case TokenBool:
			ci.pushOperand(Boolean(tok.BoolValue()))
		}
	}
}

// pushOperand adds an operand to the stack if within size limits
// Returns true if added, false if stack is full (prevents memory exhaustion)
func (ci *ContentInterpreter) pushOperand(obj Object) bool {
	if len(ci.operands) >= MaxOperandStackSize {
		return false // Stack full, ignore to prevent memory exhaustion
	}
	ci.operands = append(ci.operands, obj)
	return true
}

// Helper methods to get operands
func (ci *ContentInterpreter) getFloat(i int) float64 {
	if i >= len(ci.operands) {
		return 0
	}
	f, _ := AsFloat(ci.operands[i])
	return f
}

func (ci *ContentInterpreter) getString(i int) string {
	if i >= len(ci.operands) {
		return ""
	}
	s, _ := AsString(ci.operands[i])
	return s
}

func (ci *ContentInterpreter) getName(i int) string {
	if i >= len(ci.operands) {
		return ""
	}
	if n, ok := ci.operands[i].(Name); ok {
		return string(n)
	}
	return ""
}
