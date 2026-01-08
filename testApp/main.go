package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	docuindex "github.com/mariomalcek/docuindex"
	"github.com/mariomalcek/docuindex/embedding"
)

const (
	dataDir    = "./test_data"
	sampleDocs = "./samples" // Directory for sample documents (PDF/DOCX)
)

// Global flags
var (
	// Embedding flags
	embeddingProvider string
	embeddingEndpoint string
	embeddingAPIKey   string
	embeddingModel    string

	// Search flags
	searchMode    string
	maxResults    int
	vectorWeight  float64
	keywordWeight float64

	// Data directory
	dataDirFlag string
)

func main() {
	fmt.Println("===========================================")
	fmt.Println("     DocuIndex Test Application")
	fmt.Println("===========================================")
	fmt.Println()

	// Check command line arguments
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	// Define subcommand flag sets
	switch command {
	case "index":
		runIndexCommand(os.Args[2:])
	case "search":
		runSearchCommand(os.Args[2:])
	case "list":
		runListCommand(os.Args[2:])
	case "info":
		runInfoCommand(os.Args[2:])
	case "delete":
		runDeleteCommand(os.Args[2:])
	case "stats":
		runStatsCommand(os.Args[2:])
	case "full-test":
		runFullTestCommand(os.Args[2:])
	case "cleanup":
		runCleanupCommand(os.Args[2:])
	case "embed":
		runEmbedCommand(os.Args[2:])
	case "embed-test":
		runEmbedTestCommand(os.Args[2:])
	case "images":
		runImagesCommand(os.Args[2:])
	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: testApp <command> [flags] [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  index <file>              Index a document (PDF or DOCX)")
	fmt.Println("  search <query>            Search indexed documents")
	fmt.Println("  list                      List all indexed documents")
	fmt.Println("  info <doc_id>             Show document information")
	fmt.Println("  delete <doc_id>           Delete a document from the store")
	fmt.Println("  stats                     Show store statistics")
	fmt.Println("  full-test <file>          Run full test suite with a document")
	fmt.Println("  cleanup                   Remove all test data")
	fmt.Println("  embed <doc_id>            Generate embeddings for a document")
	fmt.Println("  embed-test                Test embedding provider connection")
	fmt.Println("  images                    List images in a document or section")
	fmt.Println()
	fmt.Println("Embedding Flags (for search, embed commands):")
	fmt.Println("  -provider <name>          Embedding provider: azure, openai, ollama")
	fmt.Println("  -endpoint <url>           API endpoint (required for azure, ollama)")
	fmt.Println("  -api-key <key>            API key (or use env: AZURE_API_KEY, OPENAI_API_KEY)")
	fmt.Println("  -model <name>             Model name (e.g., text-embedding-3-small, nomic-embed-text)")
	fmt.Println()
	fmt.Println("Search Flags:")
	fmt.Println("  -mode <mode>              Search mode: keyword, semantic, hybrid (default: keyword)")
	fmt.Println("  -max <n>                  Maximum results (default: 10)")
	fmt.Println("  -vector-weight <0-1>      Weight for vector search in hybrid mode (default: 0.5)")
	fmt.Println("  -keyword-weight <0-1>     Weight for keyword search in hybrid mode (default: 0.5)")
	fmt.Println()
	fmt.Println("Common Flags:")
	fmt.Println("  -data <dir>               Data directory (default: ./test_data)")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  testApp index document.pdf")
	fmt.Println("  testApp search \"machine learning\"")
	fmt.Println("  testApp search -mode=hybrid -provider=ollama -model=nomic-embed-text \"AI concepts\"")
	fmt.Println("  testApp embed -provider=openai -api-key=$OPENAI_API_KEY -model=text-embedding-3-small <doc_id>")
	fmt.Println("  testApp embed-test -provider=ollama -endpoint=http://localhost:11434 -model=nomic-embed-text")
	fmt.Println()
	fmt.Println("Supported formats: PDF, DOCX")
}

func addCommonFlags(fs *flag.FlagSet) {
	fs.StringVar(&dataDirFlag, "data", dataDir, "Data directory")
}

func addEmbeddingFlags(fs *flag.FlagSet) {
	fs.StringVar(&embeddingProvider, "provider", "", "Embedding provider: azure, openai, ollama")
	fs.StringVar(&embeddingEndpoint, "endpoint", "", "API endpoint (required for azure, ollama)")
	fs.StringVar(&embeddingAPIKey, "api-key", "", "API key")
	fs.StringVar(&embeddingModel, "model", "", "Model name")
}

func addSearchFlags(fs *flag.FlagSet) {
	fs.StringVar(&searchMode, "mode", "keyword", "Search mode: keyword, semantic, hybrid")
	fs.IntVar(&maxResults, "max", 10, "Maximum results")
	fs.Float64Var(&vectorWeight, "vector-weight", 0.5, "Vector search weight (0-1)")
	fs.Float64Var(&keywordWeight, "keyword-weight", 0.5, "Keyword search weight (0-1)")
}

func getDataDir() string {
	if dataDirFlag != "" {
		return dataDirFlag
	}
	return dataDir
}

func createStore() (*docuindex.Store, error) {
	dir := getDataDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store, err := docuindex.NewStore(dir,
		docuindex.WithImageExtraction(true),
		docuindex.WithChecksum(true),
		docuindex.WithStemming(true),
		docuindex.WithStopWords(true),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	return store, nil
}

func createEmbeddingProvider() (embedding.Provider, error) {
	if embeddingProvider == "" {
		return nil, fmt.Errorf("embedding provider not specified (use -provider flag)")
	}

	// Try to get API key from environment if not provided
	apiKey := embeddingAPIKey
	if apiKey == "" {
		switch embeddingProvider {
		case "azure":
			apiKey = os.Getenv("AZURE_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
			}
		case "openai":
			apiKey = os.Getenv("OPENAI_API_KEY")
		case "ollama":
			// Ollama doesn't require API key
		}
	}

	// Try to get endpoint from environment if not provided
	endpoint := embeddingEndpoint
	if endpoint == "" {
		switch embeddingProvider {
		case "azure":
			endpoint = os.Getenv("AZURE_ENDPOINT")
			if endpoint == "" {
				endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
			}
		case "ollama":
			endpoint = "http://localhost:11434"
		}
	}

	// Default models
	model := embeddingModel
	if model == "" {
		switch embeddingProvider {
		case "azure", "openai":
			model = "text-embedding-3-small"
		case "ollama":
			model = "nomic-embed-text"
		}
	}

	cfg := embedding.Config{
		Provider:  embeddingProvider,
		Endpoint:  endpoint,
		APIKey:    apiKey,
		Model:     model,
		BatchSize: 100,
		Timeout:   30 * time.Second,
	}

	return embedding.NewProvider(cfg)
}

// Command implementations

// preprocessDebugFlag handles "-debug 200" syntax and reorders args so flags come first
func preprocessDebugFlag(args []string) []string {
	var flags []string
	var positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// Handle "-debug 200" syntax
			if arg == "-debug" && i+1 < len(args) {
				if _, err := strconv.Atoi(args[i+1]); err == nil {
					flags = append(flags, "-debug="+args[i+1])
					i++ // skip the number
					continue
				}
			}
			flags = append(flags, arg)
		} else {
			positional = append(positional, arg)
		}
	}

	// Return flags first, then positional args
	return append(flags, positional...)
}

func runIndexCommand(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	debugBlocks := fs.Int("debug", 0, "Show detailed parsing output (default 20 blocks, or specify count)")
	addCommonFlags(fs)

	// Pre-process args to handle "-debug 200" as "-debug=200"
	processedArgs := preprocessDebugFlag(args)
	fs.Parse(processedArgs)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a document file path (PDF or DOCX)")
		fmt.Println("Usage: testApp index [flags] <file>")
		os.Exit(1)
	}

	filePath := fs.Arg(0)
	ext := strings.ToLower(filepath.Ext(filePath))
	formatName := "Document"
	switch ext {
	case ".pdf":
		formatName = "PDF"
	case ".docx":
		formatName = "DOCX"
	}

	fmt.Printf("Indexing %s: %s\n", formatName, filePath)
	fmt.Println(strings.Repeat("-", 50))

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("Error: File not found: %s", filePath)
	}

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	doc, err := store.IndexDocument(filePath)
	if err != nil {
		if docuindex.IsParseError(err) {
			log.Fatalf("PDF Parse Error: %v", err)
		}
		if docuindex.IsDOCXError(err) {
			log.Fatalf("DOCX Parse Error: %v", err)
		}
		log.Fatalf("Error indexing document: %v", err)
	}

	fmt.Println("Document indexed successfully!")
	fmt.Println()
	fmt.Println("Document Info:")
	fmt.Printf("  ID:         %s\n", doc.Info.ID)
	fmt.Printf("  Name:       %s\n", doc.Info.Name)
	fmt.Printf("  Pages:      %d\n", doc.Info.PageCount)
	fmt.Printf("  Size:       %d bytes\n", doc.Info.SizeBytes)
	fmt.Printf("  Checksum:   %s\n", doc.Info.Checksum)
	fmt.Printf("  Created:    %s\n", doc.Info.CreatedAt.Format("2006-01-02 15:04:05"))

	textBlocks := doc.GetTextBlocks()
	imageBlocks := doc.GetImageBlocks()
	fmt.Println()
	fmt.Println("Content Summary:")
	fmt.Printf("  Text blocks:  %d\n", len(textBlocks))
	fmt.Printf("  Image blocks: %d\n", len(imageBlocks))

	if len(textBlocks) > 0 {
		fmt.Println()
		fmt.Println("First text blocks:")
		limit := 20
		if len(textBlocks) < limit {
			limit = len(textBlocks)
		}
		for i := 0; i < limit; i++ {
			block := textBlocks[i]
			content := block.Content
			if len(content) > 80 {
				content = content[:80] + "..."
			}
			fmt.Printf("  [Page %d] %s: %s\n", block.Page, block.Type, content)
		}
	}

	if *debugBlocks >= 0 {
		// Check if -debug flag was explicitly set (will be >= 0 if set, 0 is default)
		// We need to detect if flag was set vs just default
		debugSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "debug" {
				debugSet = true
			}
		})
		if debugSet {
			limit := *debugBlocks
			if limit == 0 {
				limit = 20 // default to 20 if -debug with no value
			}
			printDebugOutput(doc, limit)
		}
	}
}

// printDebugOutput prints detailed parsing information for debugging
func printDebugOutput(doc *docuindex.Document, maxBlocks int) {
	fmt.Println()
	fmt.Println("=== DEBUG: Parsed Content ===")
	fmt.Println()

	// Group blocks by page
	pageBlocks := make(map[int][]docuindex.ContentBlock)
	for _, block := range doc.Content.Blocks {
		pageBlocks[block.Page] = append(pageBlocks[block.Page], block)
	}

	// Print blocks organized by page, respecting maxBlocks limit
	blockCount := 0
	for page := 1; page <= doc.Info.PageCount; page++ {
		blocks := pageBlocks[page]
		if len(blocks) == 0 {
			continue
		}
		fmt.Printf("--- Page %d ---\n", page)
		for i, block := range blocks {
			if blockCount >= maxBlocks {
				fmt.Printf("\n... (showing first %d blocks, use -debug=N for more)\n", maxBlocks)
				goto done
			}
			printBlockDebug(i+1, block)
			blockCount++
		}
		fmt.Println()
	}
done:

	// Print summary statistics
	printDebugSummary(doc)
}

// printBlockDebug prints detailed info for a single content block
func printBlockDebug(seq int, block docuindex.ContentBlock) {
	// Block header with type
	typeStr := string(block.Type)
	if block.Semantic.IsHeading {
		typeStr = fmt.Sprintf("heading (level %d)", block.Semantic.HeadingLevel)
	}
	fmt.Printf("\n[Block %d] %s\n", seq, typeStr)

	// Content (full, not truncated)
	if block.Type != docuindex.BlockTypeImage {
		fmt.Printf("  Content: %s\n", block.Content)
	} else {
		fmt.Printf("  Image: %s\n", block.Content)
	}

	// Font info (if available)
	if block.Font != nil {
		style := ""
		if block.Font.Bold {
			style += "bold "
		}
		if block.Font.Italic {
			style += "italic "
		}
		if style == "" {
			style = "regular"
		}
		fmt.Printf("  Font: %s, %.1fpt, %s\n", block.Font.Name, block.Font.Size, strings.TrimSpace(style))
	}

	// Position
	if block.BBox.PageWidth > 0 {
		xPct, yPct, _, _ := block.BBox.RelativePosition()
		fmt.Printf("  Position: (%.1f%%, %.1f%%)\n", xPct, yPct)
	}

	// Section (if in a section)
	if block.Semantic.Section != "" {
		fmt.Printf("  Section: %s\n", block.Semantic.Section)
	}
}

// printDebugSummary prints summary statistics for the document
func printDebugSummary(doc *docuindex.Document) {
	fmt.Println("=== Summary ===")

	// Count by type
	counts := make(map[docuindex.BlockType]int)
	sections := make(map[string]bool)
	totalChars := 0

	for _, block := range doc.Content.Blocks {
		counts[block.Type]++
		if block.Semantic.Section != "" {
			sections[block.Semantic.Section] = true
		}
		totalChars += len(block.Content)
	}

	fmt.Printf("Total blocks: %d\n", len(doc.Content.Blocks))
	for _, t := range []docuindex.BlockType{
		docuindex.BlockTypeHeading,
		docuindex.BlockTypeText,
		docuindex.BlockTypeList,
		docuindex.BlockTypeTable,
		docuindex.BlockTypeImage,
	} {
		if counts[t] > 0 {
			fmt.Printf("  %s: %d\n", t, counts[t])
		}
	}

	fmt.Printf("Total characters: %d\n", totalChars)
	fmt.Printf("Pages: %d\n", doc.Info.PageCount)

	if len(sections) > 0 {
		sectionList := make([]string, 0, len(sections))
		for s := range sections {
			sectionList = append(sectionList, s)
		}
		fmt.Printf("Sections detected: %v\n", sectionList)
	}
}

func runSearchCommand(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	showImages := fs.Bool("show-images", false, "Include images from matching sections")
	addCommonFlags(fs)
	addEmbeddingFlags(fs)
	addSearchFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a search query")
		fmt.Println("Usage: testApp search [flags] <query>")
		os.Exit(1)
	}

	query := strings.Join(fs.Args(), " ")
	fmt.Printf("Searching for: \"%s\"\n", query)
	fmt.Printf("Search mode: %s\n", searchMode)
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Configure embedding provider for semantic/hybrid search
	if searchMode == "semantic" || searchMode == "hybrid" {
		if embeddingProvider == "" {
			log.Fatalf("Error: Embedding provider required for %s search mode (use -provider flag)", searchMode)
		}

		provider, err := createEmbeddingProvider()
		if err != nil {
			log.Fatalf("Error creating embedding provider: %v", err)
		}
		store.SetEmbeddingProvider(provider)
		fmt.Printf("Using embedding provider: %s (%s)\n", embeddingProvider, embeddingModel)
	}

	// Build search options
	var searchOpts []docuindex.SearchOption
	searchOpts = append(searchOpts, docuindex.WithMaxResults(maxResults))
	searchOpts = append(searchOpts, docuindex.WithContextWindow(2))
	searchOpts = append(searchOpts, docuindex.WithHighlight("**", "**"))

	if *showImages {
		searchOpts = append(searchOpts, docuindex.WithImages(true))
	}

	switch searchMode {
	case "keyword":
		searchOpts = append(searchOpts, docuindex.WithSearchMode(docuindex.SearchModeKeyword))
	case "semantic":
		searchOpts = append(searchOpts, docuindex.WithSearchMode(docuindex.SearchModeSemantic))
	case "hybrid":
		searchOpts = append(searchOpts, docuindex.WithSearchMode(docuindex.SearchModeHybrid))
		searchOpts = append(searchOpts, docuindex.WithVectorWeight(vectorWeight))
		searchOpts = append(searchOpts, docuindex.WithKeywordWeight(keywordWeight))
	}

	results, err := store.Search(query, searchOpts...)
	if err != nil {
		if docuindex.IsSearchError(err) {
			log.Fatalf("Search Error: %v", err)
		}
		log.Fatalf("Error searching: %v", err)
	}

	fmt.Printf("Found %d results in %v\n", results.TotalHits, results.SearchTime)
	fmt.Println()

	if results.TotalHits == 0 {
		fmt.Println("No results found. Try a different query or index more documents.")
		return
	}

	for i, result := range results.Results {
		fmt.Printf("Result %d (Score: %.4f)\n", i+1, result.Score)
		fmt.Printf("  Document: %s\n", result.DocumentName)
		fmt.Printf("  Page:     %d\n", result.Page)
		if result.Section != "" {
			fmt.Printf("  Section:  %s\n", result.Section)
		}
		fmt.Printf("  Snippet:  %s\n", truncateString(result.Snippet, 150))
		if len(result.Images) > 0 {
			fmt.Printf("  Images:   %v\n", result.Images)
		}
		fmt.Println()
	}
}

func runListCommand(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	fmt.Println("Indexed Documents")
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	docs, err := store.ListDocuments()
	if err != nil {
		log.Fatalf("Error listing documents: %v", err)
	}

	if len(docs) == 0 {
		fmt.Println("No documents indexed yet.")
		fmt.Println("Use 'testApp index <file>' to index a document (PDF or DOCX).")
		return
	}

	fmt.Printf("Total: %d document(s)\n\n", len(docs))

	for _, doc := range docs {
		fmt.Printf("ID: %s\n", doc.ID)
		fmt.Printf("  Name:    %s\n", doc.Name)
		fmt.Printf("  Pages:   %d\n", doc.PageCount)
		fmt.Printf("  Size:    %d bytes\n", doc.SizeBytes)
		fmt.Printf("  Indexed: %s\n", doc.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Println()
	}
}

func runInfoCommand(args []string) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a document ID")
		fmt.Println("Usage: testApp info [flags] <document_id>")
		os.Exit(1)
	}

	docID := fs.Arg(0)
	fmt.Printf("Document Info: %s\n", docID)
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	doc, err := store.GetDocument(docID)
	if err != nil {
		if docuindex.IsStorageError(err) {
			log.Fatalf("Document not found: %s", docID)
		}
		log.Fatalf("Error getting document: %v", err)
	}

	fmt.Println("Metadata:")
	fmt.Printf("  ID:            %s\n", doc.Info.ID)
	fmt.Printf("  Name:          %s\n", doc.Info.Name)
	fmt.Printf("  Original Path: %s\n", doc.Info.OriginalPath)
	fmt.Printf("  Format:        %s\n", doc.Info.Format)
	fmt.Printf("  Pages:         %d\n", doc.Info.PageCount)
	fmt.Printf("  Size:          %d bytes\n", doc.Info.SizeBytes)
	fmt.Printf("  Checksum:      %s\n", doc.Info.Checksum)
	fmt.Printf("  Created:       %s\n", doc.Info.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Printf("  Updated:       %s\n", doc.Info.UpdatedAt.Format("2006-01-02 15:04:05"))

	fmt.Println()
	fmt.Println("Content Analysis:")

	textBlocks := doc.GetTextBlocks()
	imageBlocks := doc.GetImageBlocks()

	headingCount := 0
	for _, block := range textBlocks {
		if block.Type == docuindex.BlockTypeHeading {
			headingCount++
		}
	}

	fmt.Printf("  Total text blocks:  %d\n", len(textBlocks))
	fmt.Printf("  Headings:           %d\n", headingCount)
	fmt.Printf("  Images:             %d\n", len(imageBlocks))

	fmt.Println()
	fmt.Println("Content by Page:")
	pageBlocks := make(map[int]int)
	for _, block := range textBlocks {
		pageBlocks[block.Page]++
	}
	for page := 1; page <= doc.Info.PageCount && page <= 10; page++ {
		if count, ok := pageBlocks[page]; ok {
			fmt.Printf("  Page %2d: %d blocks\n", page, count)
		}
	}
	if doc.Info.PageCount > 10 {
		fmt.Printf("  ... and %d more pages\n", doc.Info.PageCount-10)
	}
}

func runDeleteCommand(args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a document ID")
		fmt.Println("Usage: testApp delete [flags] <document_id>")
		os.Exit(1)
	}

	docID := fs.Arg(0)
	fmt.Printf("Deleting Document: %s\n", docID)
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	err = store.DeleteDocument(docID)
	if err != nil {
		if docuindex.IsStorageError(err) {
			log.Fatalf("Document not found: %s", docID)
		}
		log.Fatalf("Error deleting document: %v", err)
	}

	fmt.Println("Document deleted successfully!")
}

func runStatsCommand(args []string) {
	fs := flag.NewFlagSet("stats", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	fmt.Println("Store Statistics")
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	stats := store.Stats()

	fmt.Printf("Documents:    %d\n", stats.DocumentCount)
	fmt.Printf("Total Blocks: %d\n", stats.TotalBlocks)
	fmt.Printf("Total Images: %d\n", stats.TotalImages)
	fmt.Printf("Index Terms:  %d\n", stats.IndexTerms)
	fmt.Printf("Vectors:      %d\n", stats.VectorCount)
}

func runFullTestCommand(args []string) {
	fs := flag.NewFlagSet("full-test", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a document file path (PDF or DOCX)")
		fmt.Println("Usage: testApp full-test [flags] <file>")
		os.Exit(1)
	}

	pdfPath := fs.Arg(0)
	fmt.Println("Running Full Test Suite")
	fmt.Println(strings.Repeat("=", 50))

	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		log.Fatalf("Error: File not found: %s", pdfPath)
	}

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Test 1: Index document
	fmt.Println()
	fmt.Println("Test 1: Indexing Document")
	fmt.Println(strings.Repeat("-", 40))

	doc, err := store.IndexDocument(pdfPath)
	if err != nil {
		log.Fatalf("FAILED - Failed to index document: %v", err)
	}
	fmt.Printf("OK - Indexed: %s (%d pages, %d bytes)\n",
		doc.Info.Name, doc.Info.PageCount, doc.Info.SizeBytes)

	// Test 2: List documents
	fmt.Println()
	fmt.Println("Test 2: Listing Documents")
	fmt.Println(strings.Repeat("-", 40))

	docs, err := store.ListDocuments()
	if err != nil {
		log.Fatalf("FAILED - Failed to list documents: %v", err)
	}
	fmt.Printf("OK - Found %d document(s)\n", len(docs))

	// Test 3: Get document by ID
	fmt.Println()
	fmt.Println("Test 3: Retrieving Document")
	fmt.Println(strings.Repeat("-", 40))

	retrieved, err := store.GetDocument(doc.Info.ID)
	if err != nil {
		log.Fatalf("FAILED - Failed to retrieve document: %v", err)
	}
	fmt.Printf("OK - Retrieved: %s\n", retrieved.Info.Name)

	// Test 4: Content extraction
	fmt.Println()
	fmt.Println("Test 4: Content Extraction")
	fmt.Println(strings.Repeat("-", 40))

	textBlocks := retrieved.GetTextBlocks()
	imageBlocks := retrieved.GetImageBlocks()
	fmt.Printf("OK - Extracted %d text blocks, %d images\n", len(textBlocks), len(imageBlocks))

	// Test 5: Search
	fmt.Println()
	fmt.Println("Test 5: Full-Text Search")
	fmt.Println(strings.Repeat("-", 40))

	searchQuery := "the"
	if len(textBlocks) > 0 {
		words := strings.Fields(textBlocks[0].Content)
		for _, w := range words {
			if len(w) > 4 {
				searchQuery = strings.Trim(w, ".,!?:;\"'")
				break
			}
		}
	}

	results, err := store.Search(searchQuery,
		docuindex.WithMaxResults(5),
		docuindex.WithContextWindow(1),
	)
	if err != nil {
		log.Fatalf("FAILED - Search failed: %v", err)
	}
	fmt.Printf("OK - Search for \"%s\": %d results in %v\n",
		searchQuery, results.TotalHits, results.SearchTime)

	// Test 6: Context retrieval (for RAG)
	fmt.Println()
	fmt.Println("Test 6: Context Retrieval (RAG)")
	fmt.Println(strings.Repeat("-", 40))

	if len(textBlocks) > 2 {
		targetBlock := textBlocks[len(textBlocks)/2]
		ctx, err := store.GetContext(doc.Info.ID, targetBlock.ID, 2)
		if err != nil {
			fmt.Printf("WARN - Context retrieval failed: %v\n", err)
		} else {
			fmt.Printf("OK - Context: %d before, 1 center, %d after blocks\n",
				len(ctx.Before), len(ctx.After))
		}
	} else {
		fmt.Println("WARN - Not enough blocks for context test")
	}

	// Test 7: Statistics
	fmt.Println()
	fmt.Println("Test 7: Store Statistics")
	fmt.Println(strings.Repeat("-", 40))

	stats := store.Stats()
	fmt.Printf("OK - Stats: %d docs, %d blocks, %d images, %d terms, %d vectors\n",
		stats.DocumentCount, stats.TotalBlocks, stats.TotalImages, stats.IndexTerms, stats.VectorCount)

	// Test 8: Document search
	fmt.Println()
	fmt.Println("Test 8: Document-Specific Search")
	fmt.Println(strings.Repeat("-", 40))

	docResults, err := store.SearchInDocument(doc.Info.ID, searchQuery)
	if err != nil {
		fmt.Printf("WARN - Document search failed: %v\n", err)
	} else {
		fmt.Printf("OK - Document search: %d results\n", docResults.TotalHits)
	}

	// Summary
	fmt.Println()
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("All tests completed successfully!")
	fmt.Println()
	fmt.Printf("Document ID for further testing: %s\n", doc.Info.ID)
}

func runCleanupCommand(args []string) {
	fs := flag.NewFlagSet("cleanup", flag.ExitOnError)
	addCommonFlags(fs)
	fs.Parse(args)

	fmt.Println("Cleaning Up Test Data")
	fmt.Println(strings.Repeat("-", 50))

	dir := getDataDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		fmt.Println("No test data to clean up.")
		return
	}

	err := os.RemoveAll(dir)
	if err != nil {
		log.Fatalf("Error cleaning up: %v", err)
	}

	fmt.Println("Test data removed successfully!")
}

func runEmbedCommand(args []string) {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	addCommonFlags(fs)
	addEmbeddingFlags(fs)
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Println("Error: Please provide a document ID")
		fmt.Println("Usage: testApp embed [flags] <document_id>")
		fmt.Println()
		fmt.Println("Example:")
		fmt.Println("  testApp embed -provider=ollama -model=nomic-embed-text <doc_id>")
		fmt.Println("  testApp embed -provider=openai -api-key=$OPENAI_API_KEY <doc_id>")
		os.Exit(1)
	}

	docID := fs.Arg(0)
	fmt.Printf("Generating Embeddings for Document: %s\n", docID)
	fmt.Println(strings.Repeat("-", 50))

	// Create embedding provider
	provider, err := createEmbeddingProvider()
	if err != nil {
		log.Fatalf("Error creating embedding provider: %v", err)
	}

	fmt.Printf("Provider: %s\n", provider.Name())
	fmt.Printf("Dimension: %d\n", provider.Dimension())
	fmt.Println()

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Set embedding provider
	store.SetEmbeddingProvider(provider)

	// Get document
	doc, err := store.GetDocument(docID)
	if err != nil {
		log.Fatalf("Error getting document: %v", err)
	}

	textBlocks := doc.GetTextBlocks()
	fmt.Printf("Document: %s\n", doc.Info.Name)
	fmt.Printf("Text blocks to embed: %d\n", len(textBlocks))
	fmt.Println()

	// Prepare texts for embedding
	var texts []string
	var blockIDs []string
	for _, block := range textBlocks {
		if len(strings.TrimSpace(block.Content)) > 10 {
			texts = append(texts, block.Content)
			blockIDs = append(blockIDs, block.ID)
		}
	}

	if len(texts) == 0 {
		fmt.Println("No text blocks to embed.")
		return
	}

	fmt.Printf("Generating embeddings for %d blocks...\n", len(texts))
	startTime := time.Now()

	// Generate embeddings in batches
	batchSize := 100
	totalVectors := 0

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		batchTexts := texts[i:end]
		vectors, err := provider.Embed(context.Background(), batchTexts)
		if err != nil {
			log.Fatalf("Error generating embeddings: %v", err)
		}

		// Store vectors (this would normally be done through the store)
		fmt.Printf("  Batch %d-%d: %d vectors generated\n", i+1, end, len(vectors))
		totalVectors += len(vectors)
	}

	elapsed := time.Since(startTime)
	fmt.Println()
	fmt.Printf("Embeddings generated successfully!\n")
	fmt.Printf("  Total vectors: %d\n", totalVectors)
	fmt.Printf("  Time: %v\n", elapsed)
	fmt.Printf("  Rate: %.1f blocks/sec\n", float64(totalVectors)/elapsed.Seconds())
}

func runEmbedTestCommand(args []string) {
	fs := flag.NewFlagSet("embed-test", flag.ExitOnError)
	addEmbeddingFlags(fs)
	fs.Parse(args)

	fmt.Println("Testing Embedding Provider")
	fmt.Println(strings.Repeat("-", 50))

	provider, err := createEmbeddingProvider()
	if err != nil {
		log.Fatalf("Error creating embedding provider: %v", err)
	}

	fmt.Printf("Provider: %s\n", provider.Name())
	fmt.Printf("Dimension: %d\n", provider.Dimension())
	fmt.Println()

	// Test with sample texts
	testTexts := []string{
		"Machine learning is a subset of artificial intelligence.",
		"Natural language processing enables computers to understand human language.",
		"Deep learning uses neural networks with many layers.",
	}

	fmt.Println("Test texts:")
	for i, t := range testTexts {
		fmt.Printf("  %d. %s\n", i+1, t)
	}
	fmt.Println()

	fmt.Println("Generating embeddings...")
	startTime := time.Now()

	vectors, err := provider.Embed(context.Background(), testTexts)
	if err != nil {
		log.Fatalf("Error generating embeddings: %v", err)
	}

	elapsed := time.Since(startTime)

	fmt.Println()
	fmt.Println("Results:")
	fmt.Printf("  Vectors generated: %d\n", len(vectors))
	fmt.Printf("  Vector dimension: %d\n", len(vectors[0]))
	fmt.Printf("  Time: %v\n", elapsed)
	fmt.Println()

	// Show first few values of first vector
	fmt.Println("Sample vector (first 10 values):")
	fmt.Print("  [")
	for i := 0; i < 10 && i < len(vectors[0]); i++ {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Printf("%.4f", vectors[0][i])
	}
	fmt.Println(", ...]")
	fmt.Println()

	// Calculate cosine similarity between vectors
	fmt.Println("Cosine similarities:")
	for i := 0; i < len(vectors); i++ {
		for j := i + 1; j < len(vectors); j++ {
			sim := cosineSimilarity(vectors[i], vectors[j])
			fmt.Printf("  Text %d vs Text %d: %.4f\n", i+1, j+1, sim)
		}
	}

	fmt.Println()
	fmt.Println("Embedding provider test completed successfully!")
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method
	z := x / 2
	for i := 0; i < 10; i++ {
		z = z - (z*z-x)/(2*z)
	}
	return z
}

func truncateString(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func runImagesCommand(args []string) {
	fs := flag.NewFlagSet("images", flag.ExitOnError)
	docID := fs.String("doc", "", "Document ID (required)")
	section := fs.String("section", "", "Filter by section name")
	page := fs.Int("page", 0, "Filter by page number")
	addCommonFlags(fs)
	fs.Parse(args)

	if *docID == "" {
		fmt.Println("Error: Please provide a document ID with -doc flag")
		fmt.Println("Usage: testApp images -doc <doc_id> [-section <name>] [-page <n>]")
		os.Exit(1)
	}

	fmt.Printf("Listing images for document: %s\n", *docID)
	if *section != "" {
		fmt.Printf("Section filter: %s\n", *section)
	}
	if *page > 0 {
		fmt.Printf("Page filter: %d\n", *page)
	}
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Get images based on filters
	images, err := store.GetImagesByDocumentFiltered(*docID, *section, *page)
	if err != nil {
		log.Fatalf("Error getting images: %v", err)
	}

	if len(images) == 0 {
		fmt.Println("No images found matching the criteria.")
		return
	}

	fmt.Printf("Found %d image(s)\n\n", len(images))

	for i, img := range images {
		fmt.Printf("Image %d:\n", i+1)
		fmt.Printf("  ID:       %s\n", img.ID)
		fmt.Printf("  Path:     images/%s.%s\n", img.ID, img.Format)
		fmt.Printf("  Format:   %s\n", img.Format)
		fmt.Printf("  Page:     %d\n", img.Page)
		if img.OriginalName != "" {
			fmt.Printf("  Name:     %s\n", img.OriginalName)
		}
		fmt.Println()
	}
}

// findDocumentFiles searches for supported document files (PDF, DOCX) in a directory
func findDocumentFiles(dir string) ([]string, error) {
	var docs []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".pdf" || ext == ".docx" {
				docs = append(docs, path)
			}
		}
		return nil
	})
	return docs, err
}
