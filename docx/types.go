package docx

import (
	"encoding/xml"
)

// BlockType represents the type of content block (local to docx package)
type BlockType string

const (
	BlockTypeText    BlockType = "text"
	BlockTypeHeading BlockType = "heading"
	BlockTypeImage   BlockType = "image"
	BlockTypeList    BlockType = "list"
	BlockTypeTable   BlockType = "table"
)

// DocumentFormat represents the source document format
type DocumentFormat string

const (
	FormatDOCX DocumentFormat = "docx"
)

// BoundingBox represents the position and size of content on a page
type BoundingBox struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PageWidth  float64 `json:"page_width"`
	PageHeight float64 `json:"page_height"`
}

// FontInfo contains font metadata for text content
type FontInfo struct {
	Name   string  `json:"name"`
	Size   float64 `json:"size"`
	Bold   bool    `json:"bold,omitempty"`
	Italic bool    `json:"italic,omitempty"`
}

// SemanticInfo contains AI-friendly metadata about content
type SemanticInfo struct {
	IsHeading    bool     `json:"is_heading,omitempty"`
	HeadingLevel int      `json:"heading_level,omitempty"`
	Section      string   `json:"section,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	Context      string   `json:"context,omitempty"`
}

// ContentBlock represents a unit of content with position and metadata
type ContentBlock struct {
	ID       string       `json:"id"`
	Type     BlockType    `json:"type"`
	Content  string       `json:"content"`
	Page     int          `json:"page"`
	BBox     BoundingBox  `json:"bbox"`
	Font     *FontInfo    `json:"font,omitempty"`
	Semantic SemanticInfo `json:"semantic,omitempty"`
	Children []string     `json:"children,omitempty"`
}

// DocumentContent holds the structured content of a document
type DocumentContent struct {
	Version string         `json:"version"`
	Blocks  []ContentBlock `json:"blocks"`
}

// DocumentInfo contains metadata about a document
type DocumentInfo struct {
	Format    DocumentFormat `json:"format"`
	PageCount int            `json:"page_count"`
	Name      string         `json:"name"`
}

// ============================================================================
// XML Types for DOCX parsing
// ============================================================================

// DocumentXML represents word/document.xml root element
type DocumentXML struct {
	XMLName xml.Name `xml:"document"`
	Body    Body     `xml:"body"`
}

// Body represents the document body containing paragraphs and tables
type Body struct {
	// We use a custom unmarshaler to handle mixed content (paragraphs and tables)
	Items []interface{} `xml:"-"`
	// For direct access when not using custom unmarshal
	Paragraphs []Paragraph `xml:"p"`
	Tables     []Table     `xml:"tbl"`
}

// Paragraph represents a w:p element
type Paragraph struct {
	XMLName    xml.Name              `xml:"p"`
	Properties *ParagraphProperties  `xml:"pPr"`
	Runs       []Run                 `xml:"r"`
	Hyperlinks []Hyperlink           `xml:"hyperlink"`
	BookmarkStart []BookmarkStart    `xml:"bookmarkStart"`
}

// ParagraphProperties represents w:pPr element
type ParagraphProperties struct {
	XMLName    xml.Name    `xml:"pPr"`
	Style      *StyleRef   `xml:"pStyle"`
	NumPr      *NumPr      `xml:"numPr"`
	Justify    *Val        `xml:"jc"`
	Indent     *Indent     `xml:"ind"`
	OutlineLvl *Val        `xml:"outlineLvl"`
	Spacing    *Spacing    `xml:"spacing"`
	RunProps   *RunProperties `xml:"rPr"` // Default run properties for paragraph
}

// StyleRef represents a style reference (w:pStyle or w:rStyle)
type StyleRef struct {
	Val string `xml:"val,attr"`
}

// NumPr represents numbering properties for lists
type NumPr struct {
	ILvl  *Val `xml:"ilvl"`
	NumID *Val `xml:"numId"`
}

// Val represents a simple value element with w:val attribute
type Val struct {
	Val string `xml:"val,attr"`
}

// Indent represents paragraph indentation
type Indent struct {
	Left      string `xml:"left,attr"`
	Right     string `xml:"right,attr"`
	FirstLine string `xml:"firstLine,attr"`
	Hanging   string `xml:"hanging,attr"`
}

// Spacing represents paragraph spacing
type Spacing struct {
	Before string `xml:"before,attr"`
	After  string `xml:"after,attr"`
	Line   string `xml:"line,attr"`
}

// Run represents a w:r element (text run with consistent formatting)
type Run struct {
	XMLName    xml.Name       `xml:"r"`
	Properties *RunProperties `xml:"rPr"`
	Text       []Text         `xml:"t"`
	Tab        []Tab          `xml:"tab"`
	Break      []Break        `xml:"br"`
	Drawing    []Drawing      `xml:"drawing"`
	FieldChar  []FieldChar    `xml:"fldChar"`
	InstrText  []InstrText    `xml:"instrText"`
}

// RunProperties represents w:rPr element
type RunProperties struct {
	XMLName   xml.Name  `xml:"rPr"`
	Bold      *struct{} `xml:"b"`
	Italic    *struct{} `xml:"i"`
	Underline *struct{} `xml:"u"`
	Strike    *struct{} `xml:"strike"`
	Size      *Val      `xml:"sz"`       // Size in half-points (24 = 12pt)
	SizeCs    *Val      `xml:"szCs"`     // Complex script size
	Font      *Fonts    `xml:"rFonts"`
	Color     *Color    `xml:"color"`
	Highlight *Val      `xml:"highlight"`
	VertAlign *Val      `xml:"vertAlign"` // superscript, subscript
	Style     *StyleRef `xml:"rStyle"`
}

// Fonts represents font specification
type Fonts struct {
	ASCII    string `xml:"ascii,attr"`
	HAnsi    string `xml:"hAnsi,attr"`
	CS       string `xml:"cs,attr"`
	EastAsia string `xml:"eastAsia,attr"`
}

// Color represents text color
type Color struct {
	Val       string `xml:"val,attr"`
	ThemeColor string `xml:"themeColor,attr"`
}

// Text represents w:t element (actual text content)
type Text struct {
	Space   string `xml:"space,attr"` // xml:space="preserve"
	Content string `xml:",chardata"`
}

// InstrText represents w:instrText element (field instructions)
type InstrText struct {
	Space   string `xml:"space,attr"`
	Content string `xml:",chardata"`
}

// Tab represents a w:tab element
type Tab struct {
	XMLName xml.Name `xml:"tab"`
}

// Break represents a w:br element
type Break struct {
	XMLName xml.Name `xml:"br"`
	Type    string   `xml:"type,attr"` // page, column, textWrapping
}

// FieldChar represents field character (for complex fields)
type FieldChar struct {
	XMLName   xml.Name `xml:"fldChar"`
	FieldType string   `xml:"fldCharType,attr"` // begin, separate, end
}

// Hyperlink represents a w:hyperlink element
type Hyperlink struct {
	XMLName xml.Name `xml:"hyperlink"`
	ID      string   `xml:"id,attr"` // r:id reference
	Anchor  string   `xml:"anchor,attr"`
	Runs    []Run    `xml:"r"`
}

// BookmarkStart represents a bookmark
type BookmarkStart struct {
	XMLName xml.Name `xml:"bookmarkStart"`
	ID      string   `xml:"id,attr"`
	Name    string   `xml:"name,attr"`
}

// ============================================================================
// Drawing and Image Types
// ============================================================================

// Drawing represents a w:drawing element containing images
type Drawing struct {
	XMLName xml.Name `xml:"drawing"`
	Inline  *Inline  `xml:"inline"`
	Anchor  *Anchor  `xml:"anchor"`
}

// Inline represents wp:inline (inline image)
type Inline struct {
	XMLName     xml.Name  `xml:"inline"`
	DistT       string    `xml:"distT,attr"`
	DistB       string    `xml:"distB,attr"`
	DistL       string    `xml:"distL,attr"`
	DistR       string    `xml:"distR,attr"`
	Extent      *Extent   `xml:"extent"`
	DocPr       *DocPr    `xml:"docPr"`
	Graphic     *Graphic  `xml:"graphic"`
}

// Anchor represents wp:anchor (floating image)
type Anchor struct {
	XMLName     xml.Name  `xml:"anchor"`
	DistT       string    `xml:"distT,attr"`
	DistB       string    `xml:"distB,attr"`
	DistL       string    `xml:"distL,attr"`
	DistR       string    `xml:"distR,attr"`
	SimplePos   string    `xml:"simplePos,attr"`
	RelativeH   string    `xml:"relativeHeight,attr"`
	BehindDoc   string    `xml:"behindDoc,attr"`
	Extent      *Extent   `xml:"extent"`
	DocPr       *DocPr    `xml:"docPr"`
	Graphic     *Graphic  `xml:"graphic"`
}

// Extent represents image dimensions in EMUs (English Metric Units)
type Extent struct {
	CX string `xml:"cx,attr"` // Width in EMUs (914400 EMUs = 1 inch)
	CY string `xml:"cy,attr"` // Height in EMUs
}

// DocPr represents document properties for the drawing
type DocPr struct {
	ID    string `xml:"id,attr"`
	Name  string `xml:"name,attr"`
	Descr string `xml:"descr,attr"`
}

// Graphic represents a:graphic element
type Graphic struct {
	XMLName     xml.Name     `xml:"graphic"`
	GraphicData *GraphicData `xml:"graphicData"`
}

// GraphicData represents a:graphicData element
type GraphicData struct {
	XMLName xml.Name `xml:"graphicData"`
	URI     string   `xml:"uri,attr"`
	Pic     *Pic     `xml:"pic"`
}

// Pic represents pic:pic element
type Pic struct {
	XMLName  xml.Name  `xml:"pic"`
	NvPicPr  *NvPicPr  `xml:"nvPicPr"`
	BlipFill *BlipFill `xml:"blipFill"`
	SpPr     *SpPr     `xml:"spPr"`
}

// NvPicPr represents non-visual picture properties
type NvPicPr struct {
	CNvPr *CNvPr `xml:"cNvPr"`
}

// CNvPr represents common non-visual properties
type CNvPr struct {
	ID    string `xml:"id,attr"`
	Name  string `xml:"name,attr"`
	Descr string `xml:"descr,attr"`
}

// BlipFill represents the image fill
type BlipFill struct {
	Blip *Blip `xml:"blip"`
}

// Blip represents the actual image reference
type Blip struct {
	Embed string `xml:"embed,attr"` // r:embed relationship ID
	Link  string `xml:"link,attr"`  // r:link for linked images
}

// SpPr represents shape properties (for sizing)
type SpPr struct {
	Xfrm *Xfrm `xml:"xfrm"`
}

// Xfrm represents transform (position and size)
type Xfrm struct {
	Off *Off `xml:"off"`
	Ext *Ext `xml:"ext"`
}

// Off represents offset position
type Off struct {
	X string `xml:"x,attr"`
	Y string `xml:"y,attr"`
}

// Ext represents extent (size)
type Ext struct {
	CX string `xml:"cx,attr"`
	CY string `xml:"cy,attr"`
}

// ============================================================================
// Table Types
// ============================================================================

// Table represents a w:tbl element
type Table struct {
	XMLName    xml.Name        `xml:"tbl"`
	Properties *TableProperties `xml:"tblPr"`
	Grid       *TableGrid      `xml:"tblGrid"`
	Rows       []TableRow      `xml:"tr"`
}

// TableProperties represents table properties
type TableProperties struct {
	Style  *StyleRef `xml:"tblStyle"`
	Width  *TableWidth `xml:"tblW"`
	Layout *Val      `xml:"tblLayout"`
}

// TableWidth represents table width
type TableWidth struct {
	W    string `xml:"w,attr"`
	Type string `xml:"type,attr"`
}

// TableGrid represents column definitions
type TableGrid struct {
	Cols []GridCol `xml:"gridCol"`
}

// GridCol represents a grid column
type GridCol struct {
	W string `xml:"w,attr"`
}

// TableRow represents a w:tr element
type TableRow struct {
	XMLName    xml.Name         `xml:"tr"`
	Properties *TableRowProperties `xml:"trPr"`
	Cells      []TableCell      `xml:"tc"`
}

// TableRowProperties represents row properties
type TableRowProperties struct {
	Height *Val `xml:"trHeight"`
	Header *struct{} `xml:"tblHeader"` // Indicates header row
}

// TableCell represents a w:tc element
type TableCell struct {
	XMLName    xml.Name          `xml:"tc"`
	Properties *TableCellProperties `xml:"tcPr"`
	Paragraphs []Paragraph       `xml:"p"`
}

// TableCellProperties represents cell properties
type TableCellProperties struct {
	Width    *TableWidth `xml:"tcW"`
	GridSpan *Val        `xml:"gridSpan"`
	VMerge   *Val        `xml:"vMerge"`
	Shading  *Shading    `xml:"shd"`
}

// Shading represents cell shading/background
type Shading struct {
	Val   string `xml:"val,attr"`
	Color string `xml:"color,attr"`
	Fill  string `xml:"fill,attr"`
}

// ============================================================================
// Styles Types (word/styles.xml)
// ============================================================================

// StylesXML represents word/styles.xml root element
type StylesXML struct {
	XMLName     xml.Name     `xml:"styles"`
	DocDefaults *DocDefaults `xml:"docDefaults"`
	Styles      []Style      `xml:"style"`
}

// DocDefaults represents default paragraph and run properties
type DocDefaults struct {
	RPrDefault *RPrDefault `xml:"rPrDefault"`
	PPrDefault *PPrDefault `xml:"pPrDefault"`
}

// RPrDefault represents default run properties
type RPrDefault struct {
	RPr *RunProperties `xml:"rPr"`
}

// PPrDefault represents default paragraph properties
type PPrDefault struct {
	PPr *ParagraphProperties `xml:"pPr"`
}

// Style represents a single style definition
type Style struct {
	XMLName xml.Name `xml:"style"`
	Type    string   `xml:"type,attr"`    // paragraph, character, table, numbering
	StyleID string   `xml:"styleId,attr"`
	Default string   `xml:"default,attr"` // "1" if default style
	Name    *Val     `xml:"name"`
	BasedOn *Val     `xml:"basedOn"`
	Next    *Val     `xml:"next"`
	Link    *Val     `xml:"link"`
	PPr     *ParagraphProperties `xml:"pPr"`
	RPr     *RunProperties       `xml:"rPr"`
}

// ============================================================================
// Numbering Types (word/numbering.xml)
// ============================================================================

// NumberingXML represents word/numbering.xml root element
type NumberingXML struct {
	XMLName      xml.Name      `xml:"numbering"`
	AbstractNums []AbstractNum `xml:"abstractNum"`
	Nums         []Num         `xml:"num"`
}

// AbstractNum represents an abstract numbering definition
type AbstractNum struct {
	XMLName       xml.Name `xml:"abstractNum"`
	AbstractNumID string   `xml:"abstractNumId,attr"`
	MultiLevelType *Val    `xml:"multiLevelType"`
	Levels        []Level  `xml:"lvl"`
}

// Level represents a numbering level
type Level struct {
	XMLName   xml.Name `xml:"lvl"`
	ILvl      string   `xml:"ilvl,attr"`
	Start     *Val     `xml:"start"`
	NumFmt    *Val     `xml:"numFmt"` // decimal, bullet, lowerLetter, upperLetter, lowerRoman, upperRoman
	LvlText   *Val     `xml:"lvlText"`
	LvlJc     *Val     `xml:"lvlJc"`
	PPr       *ParagraphProperties `xml:"pPr"`
	RPr       *RunProperties       `xml:"rPr"`
}

// Num represents a concrete numbering instance
type Num struct {
	XMLName       xml.Name `xml:"num"`
	NumID         string   `xml:"numId,attr"`
	AbstractNumID *Val     `xml:"abstractNumId"`
}

// ============================================================================
// Relationships Types (_rels/*.rels)
// ============================================================================

// Relationships represents a relationships file
type Relationships struct {
	XMLName       xml.Name       `xml:"Relationships"`
	Relationships []Relationship `xml:"Relationship"`
}

// Relationship represents a single relationship
type Relationship struct {
	XMLName    xml.Name `xml:"Relationship"`
	ID         string   `xml:"Id,attr"`
	Type       string   `xml:"Type,attr"`
	Target     string   `xml:"Target,attr"`
	TargetMode string   `xml:"TargetMode,attr"` // External for hyperlinks
}

// Relationship type constants
const (
	RelTypeDocument   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument"
	RelTypeStyles     = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"
	RelTypeNumbering  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering"
	RelTypeImage      = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
	RelTypeHyperlink  = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink"
	RelTypeCoreProps  = "http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties"
	RelTypeExtProps   = "http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties"
)

// ============================================================================
// Content Types ([Content_Types].xml)
// ============================================================================

// ContentTypes represents [Content_Types].xml
type ContentTypes struct {
	XMLName   xml.Name   `xml:"Types"`
	Defaults  []Default  `xml:"Default"`
	Overrides []Override `xml:"Override"`
}

// Default represents a default content type by extension
type Default struct {
	Extension   string `xml:"Extension,attr"`
	ContentType string `xml:"ContentType,attr"`
}

// Override represents a content type override for a specific part
type Override struct {
	PartName    string `xml:"PartName,attr"`
	ContentType string `xml:"ContentType,attr"`
}

// ============================================================================
// Core Properties (docProps/core.xml)
// ============================================================================

// CoreProperties represents Dublin Core metadata
type CoreProperties struct {
	XMLName     xml.Name `xml:"coreProperties"`
	Title       string   `xml:"title"`
	Subject     string   `xml:"subject"`
	Creator     string   `xml:"creator"`
	Keywords    string   `xml:"keywords"`
	Description string   `xml:"description"`
	LastModBy   string   `xml:"lastModifiedBy"`
	Revision    string   `xml:"revision"`
	Created     string   `xml:"created"`
	Modified    string   `xml:"modified"`
}

// ============================================================================
// App Properties (docProps/app.xml)
// ============================================================================

// AppProperties represents application-specific properties
type AppProperties struct {
	XMLName     xml.Name `xml:"Properties"`
	Application string   `xml:"Application"`
	AppVersion  string   `xml:"AppVersion"`
	Pages       int      `xml:"Pages"`
	Words       int      `xml:"Words"`
	Characters  int      `xml:"Characters"`
	Lines       int      `xml:"Lines"`
	Paragraphs  int      `xml:"Paragraphs"`
	Company     string   `xml:"Company"`
}

// ============================================================================
// Extracted Content Types
// ============================================================================

// TextBlock represents extracted text with formatting info
type TextBlock struct {
	Text      string
	StyleID   string
	FontName  string
	FontSize  float64
	Bold      bool
	Italic    bool
	IsList    bool
	ListLevel int
	ListType  string // "bullet", "decimal", "lowerLetter", etc.
}

// ExtractedImage represents an extracted image
type ExtractedImage struct {
	Name     string
	Format   string // jpeg, png, gif, etc.
	Data     []byte
	Width    float64 // Display width in points
	Height   float64 // Display height in points
	RelID    string  // Relationship ID
	Page     int     // Estimated page number
}

// Metadata holds document metadata
type Metadata struct {
	Title     string
	Author    string
	Subject   string
	Keywords  string
	Creator   string
	Created   string
	Modified  string
	PageCount int
	WordCount int
}
