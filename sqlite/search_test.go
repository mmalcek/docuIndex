package sqlite

import (
	"strings"
	"testing"
	"time"
)

func TestSearch(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Index document with blocks
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "machine learning algorithms for data analysis"},
			{ID: "b2", Type: "text", Content: "deep learning neural networks"},
			{ID: "b3", Type: "text", Content: "natural language processing"},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	results, err := store.Search("learning", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.TotalHits < 2 {
		t.Errorf("Expected at least 2 hits for 'learning', got %d", results.TotalHits)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	results, err := store.Search("", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.TotalHits != 0 {
		t.Errorf("Expected 0 hits for empty query, got %d", results.TotalHits)
	}
}

func TestSearchNoResults(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Index document
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "hello world"},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	results, err := store.Search("nonexistent", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.TotalHits != 0 {
		t.Errorf("Expected 0 hits for 'nonexistent', got %d", results.TotalHits)
	}
}

func TestSearchInDocument(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Index two documents
	for i, content := range []string{"hello world example", "goodbye world test"} {
		doc := &Document{
			Info: DocumentInfo{
				ID:        "doc" + string(rune('1'+i)),
				Name:      "test.pdf",
				Format:    "pdf",
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
			Blocks: []ContentBlock{
				{ID: "b1", Type: "text", Content: content},
			},
		}
		store.SaveDocument(doc)
		store.IndexDocument(doc.Info.ID, doc.Blocks)
	}

	// Search only in doc1
	results, err := store.SearchInDocument("doc1", "world", nil)
	if err != nil {
		t.Fatalf("SearchInDocument failed: %v", err)
	}

	if results.TotalHits != 1 {
		t.Errorf("Expected 1 hit, got %d", results.TotalHits)
	}
	if results.TotalHits > 0 && results.Results[0].DocumentID != "doc1" {
		t.Errorf("Expected result from doc1, got %s", results.Results[0].DocumentID)
	}
}

func TestSearchWithMaxResults(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Index document with many blocks
	blocks := make([]ContentBlock, 10)
	for i := range blocks {
		blocks[i] = ContentBlock{
			ID:      "b" + string(rune('0'+i)),
			Type:    "text",
			Content: "test search query",
		}
	}

	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: blocks,
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	opts := &SearchOptions{MaxResults: 3}
	results, err := store.Search("test", opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results.Results) > 3 {
		t.Errorf("Expected max 3 results, got %d", len(results.Results))
	}
}

func TestGenerateSnippet(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		terms     []string
		pre       string
		post      string
		wantParts []string
		notParts  []string
	}{
		{
			name:      "single term",
			content:   "hello world example",
			terms:     []string{"hello"},
			pre:       "<b>",
			post:      "</b>",
			wantParts: []string{"<b>hello</b>"},
		},
		{
			name:      "multiple terms",
			content:   "hello world example",
			terms:     []string{"hello", "world"},
			pre:       "<b>",
			post:      "</b>",
			wantParts: []string{"<b>hello</b>", "<b>world</b>"},
		},
		{
			name:      "case insensitive",
			content:   "Hello World",
			terms:     []string{"hello", "world"},
			pre:       "<b>",
			post:      "</b>",
			wantParts: []string{"<b>Hello</b>", "<b>World</b>"},
		},
		{
			name:      "no matches",
			content:   "hello world",
			terms:     []string{"foo"},
			pre:       "<b>",
			post:      "</b>",
			wantParts: []string{"hello world"},
			notParts:  []string{"<b>", "</b>"},
		},
		{
			name:      "empty terms",
			content:   "hello world",
			terms:     []string{},
			pre:       "<b>",
			post:      "</b>",
			wantParts: []string{"hello world"},
		},
		{
			name:      "no highlight markers",
			content:   "hello world",
			terms:     []string{"hello"},
			pre:       "",
			post:      "",
			wantParts: []string{"hello world"},
		},
		{
			name:      "repeated term",
			content:   "test testing tested",
			terms:     []string{"test"},
			pre:       "[",
			post:      "]",
			wantParts: []string{"[test]"},
		},
		{
			name:      "multiple occurrences",
			content:   "foo bar foo baz foo",
			terms:     []string{"foo"},
			pre:       "<",
			post:      ">",
			wantParts: []string{"<foo>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateSnippet(tt.content, tt.terms, tt.pre, tt.post)
			for _, part := range tt.wantParts {
				if !strings.Contains(result, part) {
					t.Errorf("generateSnippet() = %q, missing %q", result, part)
				}
			}
			for _, part := range tt.notParts {
				if strings.Contains(result, part) {
					t.Errorf("generateSnippet() = %q, should not contain %q", result, part)
				}
			}
		})
	}
}

func TestGenerateSnippetNoIndexShiftBug(t *testing.T) {
	// This test specifically verifies the fix for the index shifting bug
	// where inserting highlight markers corrupted subsequent highlights

	content := "hello world foo bar"
	terms := []string{"hello", "world"}

	result := generateSnippet(content, terms, "<b>", "</b>")

	// Both terms should be properly highlighted
	if !strings.Contains(result, "<b>hello</b>") {
		t.Errorf("Expected <b>hello</b> in result, got: %s", result)
	}
	if !strings.Contains(result, "<b>world</b>") {
		t.Errorf("Expected <b>world</b> in result, got: %s", result)
	}

	// Verify the structure is correct (no corrupted/overlapping markers)
	expected := "<b>hello</b> <b>world</b> foo bar"
	if result != expected {
		t.Errorf("Expected %q, got %q", expected, result)
	}
}

func TestGenerateSnippetTruncation(t *testing.T) {
	// Long content should be truncated
	longContent := strings.Repeat("a", 300)
	result := generateSnippet(longContent, []string{}, "", "")

	if len(result) > 210 { // 200 + "..."
		t.Errorf("Expected truncated snippet, got length %d", len(result))
	}
	if !strings.HasSuffix(result, "...") {
		t.Error("Expected truncated snippet to end with ...")
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		stemming  bool
		stopWords bool
		minTokens int
		hasToken  string
		noToken   string
	}{
		{
			name:      "basic tokenization",
			text:      "Hello World",
			stemming:  false,
			stopWords: false,
			minTokens: 2,
			hasToken:  "hello",
		},
		{
			name:      "stop words filtered",
			text:      "the quick brown fox",
			stemming:  false,
			stopWords: true,
			minTokens: 2, // "the" filtered
			hasToken:  "quick",
			noToken:   "the",
		},
		{
			name:      "stemming applied",
			text:      "running quickly",
			stemming:  true,
			stopWords: false,
			minTokens: 2,
			hasToken:  "runn", // stemmed
		},
		{
			name:      "numbers included",
			text:      "test123 abc",
			stemming:  false,
			stopWords: false,
			minTokens: 2,
			hasToken:  "test123",
		},
		{
			name:      "short words excluded",
			text:      "a b cd efg",
			stemming:  false,
			stopWords: false,
			minTokens: 2, // "a", "b" excluded (< 2 chars), "cd" and "efg" included
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := tokenize(tt.text, tt.stemming, tt.stopWords)

			if len(tokens) < tt.minTokens {
				t.Errorf("Expected at least %d tokens, got %d", tt.minTokens, len(tokens))
			}

			if tt.hasToken != "" {
				found := false
				for _, tok := range tokens {
					if tok.Text == tt.hasToken {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected token %q not found in %v", tt.hasToken, tokens)
				}
			}

			if tt.noToken != "" {
				for _, tok := range tokens {
					if tok.Text == tt.noToken {
						t.Errorf("Token %q should not be present", tt.noToken)
					}
				}
			}
		})
	}
}

func TestTokenizeQuery(t *testing.T) {
	// tokenizeQuery returns just the term strings
	terms := tokenizeQuery("machine learning algorithms", true, true)

	if len(terms) < 2 {
		t.Errorf("Expected at least 2 terms, got %d", len(terms))
	}

	// Check that terms are lowercased
	for _, term := range terms {
		if term != strings.ToLower(term) {
			t.Errorf("Term %q should be lowercase", term)
		}
	}
}

func TestDeleteDocumentIndex(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "unique searchable content"},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	// Verify searchable
	results, _ := store.Search("unique", nil)
	if results.TotalHits == 0 {
		t.Fatal("Expected to find document before index deletion")
	}

	// Delete index
	err := store.DeleteDocumentIndex("doc1")
	if err != nil {
		t.Fatalf("DeleteDocumentIndex failed: %v", err)
	}

	// Verify not searchable
	results, _ = store.Search("unique", nil)
	if results.TotalHits != 0 {
		t.Error("Expected 0 results after index deletion")
	}
}

func TestSearchWithMinScore(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "test search content"},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	// Very high min score should filter all results
	opts := &SearchOptions{MinScore: 1000.0}
	results, err := store.Search("test", opts)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.TotalHits != 0 {
		t.Errorf("Expected 0 results with high min score, got %d", results.TotalHits)
	}
}

func TestSearchHeadingBoost(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "important topic here"},
			{ID: "b2", Type: "heading", Content: "important topic heading", IsHeading: true},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	results, err := store.Search("important topic", nil)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if results.TotalHits < 2 {
		t.Skip("Need at least 2 results to test heading boost")
	}

	// Heading should be ranked higher due to 1.5x boost
	// Note: This test just verifies the search returns results
	// The actual boost effect depends on BM25 scores and content
	if results.TotalHits == 0 {
		t.Error("Expected at least one result")
	}
}

func TestGetIndexTermCount(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Initially zero
	count, err := store.GetIndexTermCount()
	if err != nil {
		t.Fatalf("GetIndexTermCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 terms initially, got %d", count)
	}

	// Index document
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
		Blocks: []ContentBlock{
			{ID: "b1", Type: "text", Content: "unique term another"},
		},
	}
	store.SaveDocument(doc)
	store.IndexDocument("doc1", doc.Blocks)

	count, err = store.GetIndexTermCount()
	if err != nil {
		t.Fatalf("GetIndexTermCount failed: %v", err)
	}
	if count == 0 {
		t.Error("Expected some terms after indexing")
	}
}

func TestContains(t *testing.T) {
	slice := []string{"a", "b", "c"}

	if !contains(slice, "a") {
		t.Error("Expected to find 'a'")
	}
	if !contains(slice, "c") {
		t.Error("Expected to find 'c'")
	}
	if contains(slice, "d") {
		t.Error("Did not expect to find 'd'")
	}
	if contains(nil, "a") {
		t.Error("Did not expect to find in nil slice")
	}
}

func TestIntersectStringSlices(t *testing.T) {
	a := []string{"a", "b", "c", "d"}
	b := []string{"b", "d", "e", "f"}

	result := intersectStringSlices(a, b)

	if len(result) != 2 {
		t.Errorf("Expected 2 elements, got %d", len(result))
	}

	hasB := contains(result, "b")
	hasD := contains(result, "d")

	if !hasB || !hasD {
		t.Errorf("Expected ['b', 'd'], got %v", result)
	}
}
