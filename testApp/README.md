# DocuIndex Test Application

A command-line test application for the DocuIndex PDF indexing library.

## Building

```bash
go build -o testApp .
```

## Usage

```bash
./testApp <command> [arguments]
```

## Commands

| Command | Description |
|---------|-------------|
| `index <pdf_file>` | Index a PDF file |
| `search <query>` | Search indexed documents |
| `list` | List all indexed documents |
| `info <doc_id>` | Show document information |
| `delete <doc_id>` | Delete a document from the store |
| `stats` | Show store statistics |
| `full-test <pdf_file>` | Run full test suite with a PDF |
| `cleanup` | Remove all test data |

## Examples

### Index a PDF

```bash
./testApp index /path/to/document.pdf
```

Output:
```
Indexing PDF: /path/to/document.pdf
--------------------------------------------------
Document indexed successfully!

Document Info:
  ID:         abc123...
  Name:       document.pdf
  Pages:      42
  Size:       1234567 bytes
  Checksum:   sha256:...
  Created:    2024-01-08 10:30:00

Content Summary:
  Text blocks:  156
  Image blocks: 12
```

### Run Full Test Suite

```bash
./testApp full-test /path/to/document.pdf
```

Runs all tests:
1. Document indexing
2. Document listing
3. Document retrieval
4. Content extraction
5. Full-text search
6. Context retrieval (RAG)
7. Store statistics
8. Document-specific search

### Search Documents

```bash
./testApp search "machine learning"
```

Output:
```
Searching for: "machine learning"
--------------------------------------------------
Found 5 results in 12.3ms

Result 1 (Score: 2.4521)
  Document: paper.pdf
  Page:     3
  Section:  Introduction
  Snippet:  ...advances in **machine learning** have enabled...
```

### List Indexed Documents

```bash
./testApp list
```

### View Document Info

```bash
./testApp info <document_id>
```

### View Statistics

```bash
./testApp stats
```

Output:
```
Store Statistics
--------------------------------------------------
Documents:    5
Total Blocks: 842
Total Images: 47
Index Terms:  3256
```

### Delete a Document

```bash
./testApp delete <document_id>
```

### Clean Up Test Data

```bash
./testApp cleanup
```

## Data Storage

Test data is stored in `./test_data/` directory with the following structure:

```
test_data/
  {document_id}/
    info.json       # Document metadata
    content.json    # Extracted content blocks
    images/         # Extracted images (if any)
```

## Configuration

The test app uses these default settings:
- Image extraction: enabled
- Checksum computation: enabled
- Porter stemming: enabled
- Stop word filtering: enabled
