# DocuIndex Test Application

A command-line test application for the DocuIndex document indexing library.

## Building

```bash
go build -o testApp .
```

## Usage

```bash
./testApp <command> [flags] [arguments]
```

## Commands

| Command | Description |
|---------|-------------|
| `index <file>` | Index a document (PDF or DOCX) |
| `search <query>` | Search indexed documents |
| `list` | List all indexed documents |
| `info <doc_id>` | Show document information |
| `delete <doc_id>` | Delete a document from the store |
| `stats` | Show store statistics |
| `full-test <file>` | Run full test suite with a document |
| `cleanup` | Remove all test data |
| `embed <doc_id>` | Generate embeddings for a document |
| `embed-test` | Test embedding provider connection |

## Flags

### Embedding Flags

Used with `search`, `embed`, and `embed-test` commands:

| Flag | Description |
|------|-------------|
| `-provider <name>` | Embedding provider: `azure`, `openai`, `ollama` |
| `-endpoint <url>` | API endpoint (required for azure, ollama) |
| `-api-key <key>` | API key (or use environment variables) |
| `-model <name>` | Model name (e.g., `text-embedding-3-small`, `nomic-embed-text`) |

### Search Flags

| Flag | Description |
|------|-------------|
| `-mode <mode>` | Search mode: `keyword` (default), `semantic`, `hybrid` |
| `-max <n>` | Maximum results (default: 10) |
| `-vector-weight <0-1>` | Weight for vector search in hybrid mode (default: 0.5) |
| `-keyword-weight <0-1>` | Weight for keyword search in hybrid mode (default: 0.5) |

### Index Flags

| Flag | Description |
|------|-------------|
| `-debug [N]` | Show detailed parsing output (default: 20 blocks, or specify count with `-debug=100`) |

### Common Flags

| Flag | Description |
|------|-------------|
| `-data <dir>` | Data directory (default: `./test_data`) |

### Environment Variables

Instead of passing API keys via flags, you can set environment variables:

| Provider | Environment Variable |
|----------|---------------------|
| Azure | `AZURE_API_KEY` or `AZURE_OPENAI_API_KEY` |
| Azure | `AZURE_ENDPOINT` or `AZURE_OPENAI_ENDPOINT` |
| OpenAI | `OPENAI_API_KEY` |

## Examples

### Index a Document

```bash
./testApp index /path/to/document.pdf
./testApp index /path/to/document.docx
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

### Index with Debug Output

Use the `-debug` flag to see detailed parsing information (shows first 20 blocks by default):

```bash
./testApp index -debug /path/to/document.pdf
./testApp index -debug=100 /path/to/document.pdf  # Show first 100 blocks
```

Output:
```
Indexing PDF: /path/to/document.pdf
--------------------------------------------------
Document indexed successfully!
...

=== DEBUG: Parsed Content ===

--- Page 1 ---

[Block 1] heading (level 1)
  Content: Introduction to Machine Learning
  Font: Helvetica-Bold, 18.0pt, bold
  Position: (10.5%, 5.2%)

[Block 2] text
  Content: Machine learning is a subset of artificial intelligence...
  Font: Helvetica, 12.0pt, regular
  Position: (10.5%, 15.3%)
  Section: Introduction to Machine Learning

...

=== Summary ===
Total blocks: 45
  heading: 8
  text: 32
  list: 3
  image: 2
Total characters: 12345
Pages: 5
Sections detected: [Introduction to Machine Learning Methods Results]
```

### Search Documents

#### Keyword Search (Default)

```bash
./testApp search "machine learning"
```

#### Semantic Search (Requires Embedding Provider)

```bash
./testApp search -mode=semantic -provider=ollama -model=nomic-embed-text "concepts of AI"
```

#### Hybrid Search (BM25 + Vector)

```bash
./testApp search -mode=hybrid -provider=openai -model=text-embedding-3-small "neural networks"
```

With custom weights:
```bash
./testApp search -mode=hybrid -provider=ollama -vector-weight=0.7 -keyword-weight=0.3 "deep learning"
```

Output:
```
Searching for: "machine learning"
Search mode: keyword
--------------------------------------------------
Found 5 results in 12.3ms

Result 1 (Score: 2.4521)
  Document: paper.pdf
  Page:     3
  Section:  Introduction
  Snippet:  ...advances in **machine learning** have enabled...
```

### Test Embedding Provider

Test connection to an embedding provider without indexing:

```bash
# Test Ollama (local)
./testApp embed-test -provider=ollama -model=nomic-embed-text

# Test OpenAI
./testApp embed-test -provider=openai -api-key=$OPENAI_API_KEY

# Test Azure OpenAI
./testApp embed-test -provider=azure -endpoint=$AZURE_ENDPOINT -api-key=$AZURE_API_KEY -model=text-embedding-3-small
```

Output:
```
Testing Embedding Provider
--------------------------------------------------
Provider: ollama/nomic-embed-text
Dimension: 768

Test texts:
  1. Machine learning is a subset of artificial intelligence.
  2. Natural language processing enables computers to understand human language.
  3. Deep learning uses neural networks with many layers.

Generating embeddings...

Results:
  Vectors generated: 3
  Vector dimension: 768
  Time: 245.123ms

Sample vector (first 10 values):
  [0.0123, -0.0456, 0.0789, ...]

Cosine similarities:
  Text 1 vs Text 2: 0.8234
  Text 1 vs Text 3: 0.8567
  Text 2 vs Text 3: 0.7891

Embedding provider test completed successfully!
```

### Generate Embeddings for a Document

```bash
./testApp embed -provider=ollama -model=nomic-embed-text <document_id>
```

Output:
```
Generating Embeddings for Document: abc123...
--------------------------------------------------
Provider: ollama/nomic-embed-text
Dimension: 768

Document: report.pdf
Text blocks to embed: 156

Generating embeddings for 156 blocks...
  Batch 1-100: 100 vectors generated
  Batch 101-156: 56 vectors generated

Embeddings generated successfully!
  Total vectors: 156
  Time: 12.345s
  Rate: 12.6 blocks/sec
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
Vectors:      842
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
├── docuindex.db           # SQLite database (all metadata)
├── hnsw.idx               # HNSW vector index (if embeddings used)
└── images/                # Extracted images with UUID names
    └── {uuid}.{ext}
```

## Configuration

The test app uses these default settings:
- Image extraction: enabled
- Checksum computation: enabled
- Porter stemming: enabled
- Stop word filtering: enabled

## Embedding Providers

### Ollama (Local)

Run embedding models locally with Ollama:

```bash
# Install Ollama and pull a model
ollama pull nomic-embed-text

# Test
./testApp embed-test -provider=ollama -model=nomic-embed-text
```

Default endpoint: `http://localhost:11434`

### OpenAI

```bash
export OPENAI_API_KEY="your-api-key"
./testApp embed-test -provider=openai -model=text-embedding-3-small
```

Supported models: `text-embedding-3-small`, `text-embedding-3-large`, `text-embedding-ada-002`

### Azure OpenAI

```bash
export AZURE_ENDPOINT="https://your-resource.openai.azure.com"
export AZURE_API_KEY="your-api-key"
./testApp embed-test -provider=azure -model=your-deployment-name
```
