package sqlite

import (
	"path/filepath"
	"testing"
	"time"
)

// setupTestStore creates a temporary test store
func setupTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDir := t.TempDir()
	store, err := NewStore(tmpDir, "test-1.0.0")
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return store
}

func TestNewStore(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, "1.0.0")
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Verify directories created
	if store.DataDir() != tmpDir {
		t.Errorf("DataDir = %q, want %q", store.DataDir(), tmpDir)
	}

	expectedImagesDir := filepath.Join(tmpDir, "images")
	if store.ImagesDir() != expectedImagesDir {
		t.Errorf("ImagesDir = %q, want %q", store.ImagesDir(), expectedImagesDir)
	}
}

func TestStoreWithOptions(t *testing.T) {
	tmpDir := t.TempDir()

	store, err := NewStore(tmpDir, "1.0.0",
		WithImageExtraction(true),
		WithStemming(false),
		WithStopWords(false),
		WithChecksum(true),
	)
	if err != nil {
		t.Fatalf("NewStore with options failed: %v", err)
	}
	defer store.Close()

	cfg := store.Config()
	if !cfg.ExtractImages {
		t.Error("ExtractImages should be true")
	}
	if cfg.UseStemming {
		t.Error("UseStemming should be false")
	}
	if cfg.UseStopWords {
		t.Error("UseStopWords should be false")
	}
	if !cfg.ComputeChecksum {
		t.Error("ComputeChecksum should be true")
	}
}

func TestSaveAndGetDocument(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)

	doc := &Document{
		Info: DocumentInfo{
			ID:           "doc1",
			Name:         "test.pdf",
			OriginalPath: "/path/to/test.pdf",
			Format:       "pdf",
			SizeBytes:    1234,
			PageCount:    5,
			Checksum:     "abc123",
			CreatedAt:    now,
			UpdatedAt:    now,
			Source:       "test-source",
			Description:  "Test document",
			ExternalID:   "ext-123",
		},
		Blocks: []ContentBlock{
			{ID: "block1", Type: "text", Content: "Hello world", Page: 1, Sequence: 0},
			{ID: "block2", Type: "heading", Content: "Chapter 1", Page: 1, Sequence: 1, IsHeading: true},
		},
	}

	err := store.SaveDocument(doc)
	if err != nil {
		t.Fatalf("SaveDocument failed: %v", err)
	}

	retrieved, err := store.GetDocument("doc1")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}

	// Verify document info
	if retrieved.Info.ID != "doc1" {
		t.Errorf("ID = %q, want %q", retrieved.Info.ID, "doc1")
	}
	if retrieved.Info.Name != "test.pdf" {
		t.Errorf("Name = %q, want %q", retrieved.Info.Name, "test.pdf")
	}
	if retrieved.Info.Format != "pdf" {
		t.Errorf("Format = %q, want %q", retrieved.Info.Format, "pdf")
	}
	if retrieved.Info.SizeBytes != 1234 {
		t.Errorf("SizeBytes = %d, want %d", retrieved.Info.SizeBytes, 1234)
	}
	if retrieved.Info.PageCount != 5 {
		t.Errorf("PageCount = %d, want %d", retrieved.Info.PageCount, 5)
	}
	if retrieved.Info.Source != "test-source" {
		t.Errorf("Source = %q, want %q", retrieved.Info.Source, "test-source")
	}
	if retrieved.Info.ExternalID != "ext-123" {
		t.Errorf("ExternalID = %q, want %q", retrieved.Info.ExternalID, "ext-123")
	}

	// Verify blocks
	if len(retrieved.Blocks) != 2 {
		t.Fatalf("Blocks count = %d, want 2", len(retrieved.Blocks))
	}
}

func TestTimestampParsing(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	importTime := now.Add(-time.Hour)

	doc := &Document{
		Info: DocumentInfo{
			ID:         "doc1",
			Name:       "test.pdf",
			Format:     "pdf",
			CreatedAt:  now,
			UpdatedAt:  now,
			ImportedAt: importTime,
		},
	}

	err := store.SaveDocument(doc)
	if err != nil {
		t.Fatalf("SaveDocument failed: %v", err)
	}

	retrieved, err := store.GetDocument("doc1")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}

	if !retrieved.Info.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", retrieved.Info.CreatedAt, now)
	}
	if !retrieved.Info.UpdatedAt.Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", retrieved.Info.UpdatedAt, now)
	}
	if !retrieved.Info.ImportedAt.Equal(importTime) {
		t.Errorf("ImportedAt = %v, want %v", retrieved.Info.ImportedAt, importTime)
	}
}

func TestTimestampZeroValue(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)

	// Document with zero ImportedAt
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: now,
			UpdatedAt: now,
			// ImportedAt is zero
		},
	}

	err := store.SaveDocument(doc)
	if err != nil {
		t.Fatalf("SaveDocument failed: %v", err)
	}

	retrieved, err := store.GetDocument("doc1")
	if err != nil {
		t.Fatalf("GetDocument failed: %v", err)
	}

	// Zero ImportedAt should remain zero
	if !retrieved.Info.ImportedAt.IsZero() {
		t.Errorf("ImportedAt should be zero, got %v", retrieved.Info.ImportedAt)
	}
}

func TestListDocuments(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Insert documents with different timestamps
	baseTime := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 3; i++ {
		doc := &Document{
			Info: DocumentInfo{
				ID:        "doc" + string(rune('0'+i)),
				Name:      "test" + string(rune('0'+i)) + ".pdf",
				Format:    "pdf",
				CreatedAt: baseTime.Add(time.Duration(i) * time.Hour),
				UpdatedAt: baseTime,
			},
		}
		if err := store.SaveDocument(doc); err != nil {
			t.Fatalf("SaveDocument failed: %v", err)
		}
	}

	docs, err := store.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments failed: %v", err)
	}

	if len(docs) != 3 {
		t.Fatalf("Expected 3 documents, got %d", len(docs))
	}

	// Should be ordered by created_at DESC (newest first)
	if docs[0].ID != "doc2" {
		t.Errorf("First document should be doc2 (most recent), got %s", docs[0].ID)
	}
	if docs[1].ID != "doc1" {
		t.Errorf("Second document should be doc1, got %s", docs[1].ID)
	}
	if docs[2].ID != "doc0" {
		t.Errorf("Third document should be doc0 (oldest), got %s", docs[2].ID)
	}
}

func TestDeleteDocument(t *testing.T) {
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
			{ID: "block1", Type: "text", Content: "Hello world"},
		},
	}

	store.SaveDocument(doc)

	// Verify exists
	exists, _ := store.DocumentExists("doc1")
	if !exists {
		t.Fatal("Document should exist before delete")
	}

	// Delete
	err := store.DeleteDocument("doc1")
	if err != nil {
		t.Fatalf("DeleteDocument failed: %v", err)
	}

	// Verify deleted
	exists, _ = store.DocumentExists("doc1")
	if exists {
		t.Error("Document should not exist after delete")
	}
}

func TestDeleteNonExistentDocument(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	err := store.DeleteDocument("nonexistent")
	if err == nil {
		t.Error("Expected error when deleting non-existent document")
	}
}

func TestDocumentExists(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Should not exist initially
	exists, err := store.DocumentExists("doc1")
	if err != nil {
		t.Fatalf("DocumentExists failed: %v", err)
	}
	if exists {
		t.Error("Document should not exist initially")
	}

	// Save document
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		},
	}
	store.SaveDocument(doc)

	// Should exist now
	exists, err = store.DocumentExists("doc1")
	if err != nil {
		t.Fatalf("DocumentExists failed: %v", err)
	}
	if !exists {
		t.Error("Document should exist after save")
	}
}

func TestGetDocumentCount(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	// Initially zero
	count, err := store.GetDocumentCount()
	if err != nil {
		t.Fatalf("GetDocumentCount failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Initial count = %d, want 0", count)
	}

	// Add documents
	for i := 0; i < 3; i++ {
		doc := &Document{
			Info: DocumentInfo{
				ID:        "doc" + string(rune('0'+i)),
				Name:      "test.pdf",
				Format:    "pdf",
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			},
		}
		store.SaveDocument(doc)
	}

	count, err = store.GetDocumentCount()
	if err != nil {
		t.Fatalf("GetDocumentCount failed: %v", err)
	}
	if count != 3 {
		t.Errorf("Count = %d, want 3", count)
	}
}

func TestFindBySourceAndExternalID(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	doc := &Document{
		Info: DocumentInfo{
			ID:         "doc1",
			Name:       "test.pdf",
			Format:     "pdf",
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
			Source:     "crm",
			ExternalID: "CRM-12345",
		},
	}
	store.SaveDocument(doc)

	// Find by source and external ID
	found, err := store.FindBySourceAndExternalID("crm", "CRM-12345")
	if err != nil {
		t.Fatalf("FindBySourceAndExternalID failed: %v", err)
	}
	if found == nil {
		t.Fatal("Expected to find document")
	}
	if found.ID != "doc1" {
		t.Errorf("Found document ID = %q, want %q", found.ID, "doc1")
	}

	// Not found
	notFound, err := store.FindBySourceAndExternalID("crm", "nonexistent")
	if err != nil {
		t.Fatalf("FindBySourceAndExternalID failed: %v", err)
	}
	if notFound != nil {
		t.Error("Expected nil for non-existent document")
	}

	// Empty source/external ID returns nil
	result, _ := store.FindBySourceAndExternalID("", "CRM-12345")
	if result != nil {
		t.Error("Expected nil for empty source")
	}
}

func TestUpdateDocumentTimestamp(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	originalTime := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: originalTime,
			UpdatedAt: originalTime,
		},
	}
	store.SaveDocument(doc)

	// Wait a bit and update timestamp
	time.Sleep(10 * time.Millisecond)
	err := store.UpdateDocumentTimestamp("doc1")
	if err != nil {
		t.Fatalf("UpdateDocumentTimestamp failed: %v", err)
	}

	retrieved, _ := store.GetDocument("doc1")
	if retrieved.Info.UpdatedAt.Equal(originalTime) {
		t.Error("UpdatedAt should have changed")
	}
	if retrieved.Info.UpdatedAt.Before(originalTime) {
		t.Error("UpdatedAt should be after original time")
	}
}

func TestSaveDocumentUpdate(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	doc := &Document{
		Info: DocumentInfo{
			ID:        "doc1",
			Name:      "test.pdf",
			Format:    "pdf",
			CreatedAt: now,
			UpdatedAt: now,
		},
		Blocks: []ContentBlock{
			{ID: "block1", Type: "text", Content: "Original content"},
		},
	}
	store.SaveDocument(doc)

	// Update document
	doc.Info.Name = "updated.pdf"
	doc.Blocks = []ContentBlock{
		{ID: "block2", Type: "text", Content: "Updated content"},
	}
	store.SaveDocument(doc)

	retrieved, _ := store.GetDocument("doc1")
	if retrieved.Info.Name != "updated.pdf" {
		t.Errorf("Name = %q, want %q", retrieved.Info.Name, "updated.pdf")
	}
	if len(retrieved.Blocks) != 1 {
		t.Fatalf("Expected 1 block, got %d", len(retrieved.Blocks))
	}
	if retrieved.Blocks[0].ID != "block2" {
		t.Errorf("Block ID = %q, want %q", retrieved.Blocks[0].ID, "block2")
	}
}

func TestGetNonExistentDocument(t *testing.T) {
	store := setupTestStore(t)
	defer store.Close()

	_, err := store.GetDocument("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent document")
	}
}

// TestParseTimestamp tests the timestamp parsing helper
func TestParseTimestamp(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		isZero  bool
	}{
		{"valid RFC3339", "2024-01-15T10:30:00Z", false, false},
		{"valid with timezone", "2024-01-15T10:30:00+05:00", false, false},
		{"empty string", "", false, true},
		{"invalid format", "2024-01-15", true, true},
		{"garbage", "not-a-timestamp", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseTimestamp(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseTimestamp(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if result.IsZero() != tt.isZero {
				t.Errorf("parseTimestamp(%q) isZero = %v, want %v", tt.input, result.IsZero(), tt.isZero)
			}
		})
	}
}

// TestParseTimestampOrZero tests the backwards-compatible helper
func TestParseTimestampOrZero(t *testing.T) {
	// Valid timestamp should parse
	result := parseTimestampOrZero("2024-01-15T10:30:00Z")
	if result.IsZero() {
		t.Error("Valid timestamp should not return zero time")
	}

	// Invalid timestamp should return zero (no panic)
	result = parseTimestampOrZero("invalid")
	if !result.IsZero() {
		t.Error("Invalid timestamp should return zero time")
	}

	// Empty string should return zero
	result = parseTimestampOrZero("")
	if !result.IsZero() {
		t.Error("Empty string should return zero time")
	}
}
