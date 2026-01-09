package main

import (
	"fmt"
	"os"

	"github.com/mariomalcek/docuIndex/pdf"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: test_pdf <file.pdf>")
		os.Exit(1)
	}

	doc, err := pdf.Open(os.Args[1])
	if err != nil {
		fmt.Printf("Error opening PDF: %v\n", err)
		os.Exit(1)
	}
	defer doc.Close()

	pageCount, err := doc.PageCount()
	if err != nil {
		fmt.Printf("Error getting page count: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("PDF has %d pages\n\n", pageCount)

	extractor := pdf.NewTextExtractor(doc)

	for i := 1; i <= pageCount; i++ {
		fmt.Printf("--- Page %d ---\n", i)
		text, err := extractor.ExtractPageText(i)
		if err != nil {
			fmt.Printf("Error extracting page %d: %v\n", i, err)
			continue
		}
		fmt.Println(text)
		fmt.Println()
	}
}
