package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	docuindex "github.com/mariomalcek/docuindex"
)

const (
	dataDir    = "./test_data"
	sampleDocs = "./samples" // Directory for sample documents (PDF/DOCX)
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

	switch command {
	case "index":
		if len(os.Args) < 3 {
			fmt.Println("Error: Please provide a document file path (PDF or DOCX)")
			fmt.Println("Usage: testApp index <file>")
			os.Exit(1)
		}
		runIndexTest(os.Args[2])

	case "search":
		if len(os.Args) < 3 {
			fmt.Println("Error: Please provide a search query")
			fmt.Println("Usage: testApp search <query>")
			os.Exit(1)
		}
		runSearchTest(strings.Join(os.Args[2:], " "))

	case "list":
		runListDocuments()

	case "info":
		if len(os.Args) < 3 {
			fmt.Println("Error: Please provide a document ID")
			fmt.Println("Usage: testApp info <document_id>")
			os.Exit(1)
		}
		runDocumentInfo(os.Args[2])

	case "delete":
		if len(os.Args) < 3 {
			fmt.Println("Error: Please provide a document ID")
			fmt.Println("Usage: testApp delete <document_id>")
			os.Exit(1)
		}
		runDeleteDocument(os.Args[2])

	case "stats":
		runStats()

	case "full-test":
		if len(os.Args) < 3 {
			fmt.Println("Error: Please provide a document file path (PDF or DOCX)")
			fmt.Println("Usage: testApp full-test <file>")
			os.Exit(1)
		}
		runFullTest(os.Args[2])

	case "cleanup":
		runCleanup()

	default:
		fmt.Printf("Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: testApp <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  index <file>          Index a document (PDF or DOCX)")
	fmt.Println("  search <query>        Search indexed documents")
	fmt.Println("  list                  List all indexed documents")
	fmt.Println("  info <doc_id>         Show document information")
	fmt.Println("  delete <doc_id>       Delete a document from the store")
	fmt.Println("  stats                 Show store statistics")
	fmt.Println("  full-test <file>      Run full test suite with a document")
	fmt.Println("  cleanup               Remove all test data")
	fmt.Println()
	fmt.Println("Supported formats: PDF, DOCX")
}

func createStore() (*docuindex.Store, error) {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	store, err := docuindex.NewStore(dataDir,
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

func runIndexTest(filePath string) {
	// Detect format
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

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Fatalf("Error: File not found: %s", filePath)
	}

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Index the document
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

	// Show content summary
	textBlocks := doc.GetTextBlocks()
	imageBlocks := doc.GetImageBlocks()
	fmt.Println()
	fmt.Println("Content Summary:")
	fmt.Printf("  Text blocks:  %d\n", len(textBlocks))
	fmt.Printf("  Image blocks: %d\n", len(imageBlocks))

	// Show first few blocks
	if len(textBlocks) > 0 {
		fmt.Println()
		fmt.Println("First few text blocks:")
		limit := 3
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
}

func runSearchTest(query string) {
	fmt.Printf("Searching for: \"%s\"\n", query)
	fmt.Println(strings.Repeat("-", 50))

	store, err := createStore()
	if err != nil {
		log.Fatalf("Error creating store: %v", err)
	}
	defer store.Close()

	// Perform search
	results, err := store.Search(query,
		docuindex.WithMaxResults(10),
		docuindex.WithContextWindow(2),
		docuindex.WithHighlight("**", "**"),
	)
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
		fmt.Println()
	}
}

func runListDocuments() {
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

func runDocumentInfo(docID string) {
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

	// Count block types
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

	// Show page distribution
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

func runDeleteDocument(docID string) {
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

func runStats() {
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
}

func runFullTest(pdfPath string) {
	fmt.Println("Running Full Test Suite")
	fmt.Println(strings.Repeat("=", 50))

	// Verify file exists
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

	// Use first significant word from the document
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
	fmt.Printf("OK - Stats: %d docs, %d blocks, %d images, %d terms\n",
		stats.DocumentCount, stats.TotalBlocks, stats.TotalImages, stats.IndexTerms)

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

func runCleanup() {
	fmt.Println("Cleaning Up Test Data")
	fmt.Println(strings.Repeat("-", 50))

	// Check if data directory exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		fmt.Println("No test data to clean up.")
		return
	}

	// Remove data directory
	err := os.RemoveAll(dataDir)
	if err != nil {
		log.Fatalf("Error cleaning up: %v", err)
	}

	fmt.Println("Test data removed successfully!")
}

func truncateString(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
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
