# DocuIndex Implementation Checklist

Track implementation progress and correctness of all features.

## Legend
- [ ] Not started
- [~] In progress
- [x] Implemented
- [T] Tested
- [!] Has known issues

---

## Phase 1: PDF Core Parser

### Lexer (pdf/lexer.go)
- [ ] Token types defined
- [ ] Whitespace/comment handling
- [ ] Number parsing (integer, real)
- [ ] String parsing (literal, escape sequences)
- [ ] Hex string parsing
- [ ] Name parsing
- [ ] Array start/end tokens
- [ ] Dictionary start/end tokens
- [ ] Stream keyword detection
- [ ] Indirect reference parsing (R)
- [ ] Unit tests

### Objects (pdf/objects.go)
- [ ] PDFObject interface
- [ ] PDFNull
- [ ] PDFBool
- [ ] PDFInteger
- [ ] PDFReal
- [ ] PDFString
- [ ] PDFName
- [ ] PDFArray
- [ ] PDFDict
- [ ] PDFStream
- [ ] PDFRef (indirect reference)
- [ ] Unit tests

### Cross-Reference (pdf/xref.go)
- [ ] Find startxref
- [ ] Parse traditional xref table
- [ ] Parse xref streams (PDF 1.5+)
- [ ] Parse trailer dictionary
- [ ] Build object offset table
- [ ] Handle multiple xref sections
- [ ] Unit tests

### Document Structure (pdf/document.go)
- [ ] Open PDF file
- [ ] Parse header/version
- [ ] Locate and parse xref
- [ ] Build object table
- [ ] Resolve indirect references
- [ ] Find document catalog
- [ ] Unit tests

### Stream Decompression (pdf/stream.go)
- [ ] FlateDecode (zlib)
- [ ] ASCIIHexDecode
- [ ] ASCII85Decode
- [ ] LZWDecode
- [ ] Predictor handling (PNG)
- [ ] Filter chaining
- [ ] Unit tests

---

## Phase 2: Content Extraction

### Page Navigation (pdf/page.go)
- [ ] Parse page tree
- [ ] Iterate pages
- [ ] Get page resources
- [ ] Get MediaBox/CropBox
- [ ] Get content streams
- [ ] Handle inherited resources
- [ ] Unit tests

### Font Handling (pdf/font.go)
- [ ] Standard 14 fonts
- [ ] WinAnsiEncoding
- [ ] MacRomanEncoding
- [ ] Custom encoding vectors
- [ ] ToUnicode CMap parsing
- [ ] Font metrics (widths)
- [ ] Unit tests

### Content Interpreter (pdf/content.go)
- [ ] Operand stack
- [ ] Graphics state stack (q/Q)
- [ ] Transformation matrix (cm)
- [ ] Text state operators (Tf, Tc, Tw, etc.)
- [ ] Text positioning (Td, TD, Tm, T*)
- [ ] Text showing (Tj, TJ, ', ")
- [ ] BT/ET handling
- [ ] Unit tests

### Text Extraction (pdf/text.go)
- [ ] Character positioning
- [ ] Word detection (spacing)
- [ ] Line detection
- [ ] Paragraph grouping
- [ ] Bounding box calculation
- [ ] Unicode output
- [ ] Unit tests

### Image Extraction (pdf/image.go)
- [ ] Find XObject images
- [ ] DCTDecode (JPEG) passthrough
- [ ] FlateDecode to PNG
- [ ] Get image dimensions
- [ ] Get color space
- [ ] Extract to file
- [ ] Unit tests

### Semantic Analysis (pdf/semantic.go)
- [ ] Heading detection (font size)
- [ ] Bold/italic detection
- [ ] Paragraph detection
- [ ] List detection
- [ ] Section hierarchy
- [ ] Keyword extraction
- [ ] Unit tests

---

## Phase 3: Storage Layer

### Store Interface (storage/store.go)
- [ ] Store interface defined
- [ ] Thread-safe operations
- [ ] CRUD operations
- [ ] Document listing
- [ ] Unit tests

### Filesystem Store (storage/filesystem.go)
- [ ] Create document folder
- [ ] Write info.json
- [ ] Write content.json
- [ ] Store images
- [ ] Read operations
- [ ] Delete operations
- [ ] Concurrent access safety
- [ ] Unit tests

### Document Model (storage/document.go)
- [ ] Document struct
- [ ] ContentBlock struct
- [ ] JSON serialization
- [ ] Validation
- [ ] Unit tests

---

## Phase 4: Search

### Tokenizer (search/tokenizer.go)
- [ ] Unicode tokenization
- [ ] Lowercasing
- [ ] Stop word removal
- [ ] Stemming (Porter)
- [ ] N-gram generation
- [ ] Unit tests

### Inverted Index (search/index.go)
- [ ] Term -> Posting list
- [ ] Document frequency
- [ ] Persistence (save/load)
- [ ] Thread safety
- [ ] Incremental updates
- [ ] Unit tests

### Query Parser (search/query.go)
- [ ] Token extraction
- [ ] Boolean operators (AND/OR/NOT)
- [ ] Phrase search ("...")
- [ ] Field search (section:...)
- [ ] Unit tests

### Ranking (search/ranking.go)
- [ ] BM25 implementation
- [ ] Score calculation
- [ ] Result sorting
- [ ] Unit tests

### Search API
- [ ] Full-text search
- [ ] Section filtering
- [ ] Context window extraction
- [ ] Result formatting for RAG
- [ ] Integration tests

---

## Phase 5: Main API (docuindex.go)

- [ ] NewStore() function
- [ ] IndexDocument()
- [ ] IndexReader()
- [ ] GetDocument()
- [ ] DeleteDocument()
- [ ] ListDocuments()
- [ ] Search()
- [ ] SearchInDocument()
- [ ] GetContext()
- [ ] Integration tests

---

## Phase 6: DOCX Support (Future)

### Parser (docx/parser.go)
- [ ] ZIP extraction
- [ ] document.xml parsing
- [ ] styles.xml parsing
- [ ] Unit tests

### Content (docx/content.go)
- [ ] Paragraph extraction
- [ ] Text runs
- [ ] Style detection
- [ ] Image extraction
- [ ] Unit tests

---

## End-to-End Tests

- [ ] Parse simple PDF -> extract text -> verify
- [ ] Parse PDF with images -> extract all -> verify
- [ ] Index multiple PDFs -> search -> verify results
- [ ] Concurrent indexing test
- [ ] Large PDF performance test
- [ ] Real-world PDF compatibility

---

## Known Issues

*(Track any discovered bugs or limitations here)*

---

## Performance Benchmarks

| Operation | Target | Actual | Status |
|-----------|--------|--------|--------|
| Parse 1MB PDF | <1s | - | - |
| Parse 10MB PDF | <5s | - | - |
| Index 100 docs | <30s | - | - |
| Search 100 docs | <100ms | - | - |
