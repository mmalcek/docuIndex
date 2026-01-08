package docuindex

import (
	"errors"
	"fmt"
)

// Sentinel errors for common cases
var (
	// PDF parsing errors
	ErrInvalidPDF         = errors.New("invalid PDF file")
	ErrCorruptedPDF       = errors.New("corrupted PDF structure")
	ErrUnsupportedVersion = errors.New("unsupported PDF version")
	ErrEncryptedPDF       = errors.New("encrypted PDF not supported")

	// DOCX parsing errors
	ErrInvalidDOCX    = errors.New("invalid DOCX file")
	ErrCorruptedDOCX  = errors.New("corrupted DOCX structure")
	ErrMissingContent = errors.New("missing document.xml in DOCX")

	// Feature errors
	ErrUnsupportedFeature  = errors.New("unsupported PDF feature")
	ErrUnsupportedEncoding = errors.New("unsupported text encoding")
	ErrUnsupportedFilter   = errors.New("unsupported stream filter")
	ErrUnsupportedFont     = errors.New("unsupported font type")
	ErrUnsupportedImage    = errors.New("unsupported image format")

	// Storage errors
	ErrDocumentNotFound = errors.New("document not found")
	ErrDocumentExists   = errors.New("document already exists")
	ErrStorageCorrupted = errors.New("storage corrupted")
	ErrStorageFull      = errors.New("storage full")

	// Search errors
	ErrSearchFailed    = errors.New("search failed")
	ErrInvalidQuery    = errors.New("invalid search query")
	ErrIndexCorrupted  = errors.New("search index corrupted")

	// General errors
	ErrInvalidInput = errors.New("invalid input")
	ErrIOError      = errors.New("I/O error")
)

// ParseError provides detailed information about PDF parsing failures
type ParseError struct {
	Op      string // Operation that failed (e.g., "lexer.readToken")
	Offset  int64  // Byte offset in file where error occurred
	Message string // Human-readable message
	Err     error  // Underlying error
}

func (e *ParseError) Error() string {
	if e.Offset >= 0 {
		return fmt.Sprintf("parse error at offset %d in %s: %s", e.Offset, e.Op, e.Message)
	}
	return fmt.Sprintf("parse error in %s: %s", e.Op, e.Message)
}

func (e *ParseError) Unwrap() error {
	return e.Err
}

// NewParseError creates a new ParseError
func NewParseError(op string, offset int64, message string, err error) *ParseError {
	return &ParseError{
		Op:      op,
		Offset:  offset,
		Message: message,
		Err:     err,
	}
}

// ObjectError indicates an error with a specific PDF object
type ObjectError struct {
	ObjectNum int    // Object number
	GenNum    int    // Generation number
	Message   string // What went wrong
	Err       error  // Underlying error
}

func (e *ObjectError) Error() string {
	return fmt.Sprintf("object %d %d: %s", e.ObjectNum, e.GenNum, e.Message)
}

func (e *ObjectError) Unwrap() error {
	return e.Err
}

// NewObjectError creates a new ObjectError
func NewObjectError(objNum, genNum int, message string, err error) *ObjectError {
	return &ObjectError{
		ObjectNum: objNum,
		GenNum:    genNum,
		Message:   message,
		Err:       err,
	}
}

// PageError indicates an error processing a specific page
type PageError struct {
	PageNum int    // 1-indexed page number
	Message string // What went wrong
	Err     error  // Underlying error
}

func (e *PageError) Error() string {
	return fmt.Sprintf("page %d: %s", e.PageNum, e.Message)
}

func (e *PageError) Unwrap() error {
	return e.Err
}

// NewPageError creates a new PageError
func NewPageError(pageNum int, message string, err error) *PageError {
	return &PageError{
		PageNum: pageNum,
		Message: message,
		Err:     err,
	}
}

// StreamError indicates an error decoding a stream
type StreamError struct {
	Filter  string // Filter name (e.g., "FlateDecode")
	Message string // What went wrong
	Err     error  // Underlying error
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("stream filter %s: %s", e.Filter, e.Message)
}

func (e *StreamError) Unwrap() error {
	return e.Err
}

// NewStreamError creates a new StreamError
func NewStreamError(filter, message string, err error) *StreamError {
	return &StreamError{
		Filter:  filter,
		Message: message,
		Err:     err,
	}
}

// FontError indicates an error processing a font
type FontError struct {
	FontName string // Font name
	Message  string // What went wrong
	Err      error  // Underlying error
}

func (e *FontError) Error() string {
	return fmt.Sprintf("font %s: %s", e.FontName, e.Message)
}

func (e *FontError) Unwrap() error {
	return e.Err
}

// NewFontError creates a new FontError
func NewFontError(fontName, message string, err error) *FontError {
	return &FontError{
		FontName: fontName,
		Message:  message,
		Err:      err,
	}
}

// StorageError indicates a storage operation failure
type StorageError struct {
	Op       string // Operation (e.g., "write", "read", "delete")
	Path     string // File or directory path
	Message  string // What went wrong
	Err      error  // Underlying error
}

func (e *StorageError) Error() string {
	return fmt.Sprintf("storage %s %s: %s", e.Op, e.Path, e.Message)
}

func (e *StorageError) Unwrap() error {
	return e.Err
}

// NewStorageError creates a new StorageError
func NewStorageError(op, path, message string, err error) *StorageError {
	return &StorageError{
		Op:      op,
		Path:    path,
		Message: message,
		Err:     err,
	}
}

// SearchError indicates a search operation failure
type SearchError struct {
	Query   string // The search query
	Message string // What went wrong
	Err     error  // Underlying error
}

func (e *SearchError) Error() string {
	return fmt.Sprintf("search '%s': %s", e.Query, e.Message)
}

func (e *SearchError) Unwrap() error {
	return e.Err
}

// NewSearchError creates a new SearchError
func NewSearchError(query, message string, err error) *SearchError {
	return &SearchError{
		Query:   query,
		Message: message,
		Err:     err,
	}
}

// DOCXError indicates a DOCX parsing or processing error
type DOCXError struct {
	Part    string // Which part of the DOCX (e.g., "word/document.xml")
	Message string // What went wrong
	Err     error  // Underlying error
}

func (e *DOCXError) Error() string {
	if e.Part != "" {
		return fmt.Sprintf("DOCX %s: %s", e.Part, e.Message)
	}
	return fmt.Sprintf("DOCX: %s", e.Message)
}

func (e *DOCXError) Unwrap() error {
	return e.Err
}

// NewDOCXError creates a new DOCXError
func NewDOCXError(part, message string, err error) *DOCXError {
	return &DOCXError{
		Part:    part,
		Message: message,
		Err:     err,
	}
}

// IsDOCXError checks if an error is a DOCX error
func IsDOCXError(err error) bool {
	var docxErr *DOCXError
	return errors.As(err, &docxErr)
}

// IsParseError checks if an error is a PDF parsing error
func IsParseError(err error) bool {
	var parseErr *ParseError
	return errors.As(err, &parseErr)
}

// IsObjectError checks if an error is a PDF object error
func IsObjectError(err error) bool {
	var objErr *ObjectError
	return errors.As(err, &objErr)
}

// IsStorageError checks if an error is a storage error
func IsStorageError(err error) bool {
	var storageErr *StorageError
	return errors.As(err, &storageErr)
}

// IsSearchError checks if an error is a search error
func IsSearchError(err error) bool {
	var searchErr *SearchError
	return errors.As(err, &searchErr)
}
