package vectorindex

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAddToEmptyIndex verifies that adding vectors to an empty HNSW index works correctly
func TestAddToEmptyIndex(t *testing.T) {
	h := NewHNSW(nil)

	// Add first vector - should become entry point
	err := h.Add("vec1", []float32{1.0, 0.0, 0.0})
	if err != nil {
		t.Fatalf("failed to add first vector: %v", err)
	}

	if h.Size() != 1 {
		t.Errorf("expected size 1, got %d", h.Size())
	}

	// Add second vector - this previously could panic if searchLayer returned empty
	err = h.Add("vec2", []float32{0.0, 1.0, 0.0})
	if err != nil {
		t.Fatalf("failed to add second vector: %v", err)
	}

	if h.Size() != 2 {
		t.Errorf("expected size 2, got %d", h.Size())
	}
}

// TestAddMultipleVectorsWithDifferentLevels tests adding vectors that may have different random levels
// This is the scenario that could trigger the panic when searchLayer returns empty results
func TestAddMultipleVectorsWithDifferentLevels(t *testing.T) {
	h := NewHNSW(&Config{
		M:        4,
		EfConst:  50,
		EfSearch: 20,
	})

	// Add many vectors to increase chance of hitting different level combinations
	vectors := []struct {
		id  string
		vec []float32
	}{
		{"v1", []float32{1.0, 0.0, 0.0, 0.0}},
		{"v2", []float32{0.0, 1.0, 0.0, 0.0}},
		{"v3", []float32{0.0, 0.0, 1.0, 0.0}},
		{"v4", []float32{0.0, 0.0, 0.0, 1.0}},
		{"v5", []float32{0.5, 0.5, 0.0, 0.0}},
		{"v6", []float32{0.0, 0.5, 0.5, 0.0}},
		{"v7", []float32{0.0, 0.0, 0.5, 0.5}},
		{"v8", []float32{0.5, 0.0, 0.0, 0.5}},
		{"v9", []float32{0.25, 0.25, 0.25, 0.25}},
		{"v10", []float32{0.1, 0.2, 0.3, 0.4}},
	}

	for _, v := range vectors {
		if err := h.Add(v.id, v.vec); err != nil {
			t.Fatalf("failed to add vector %s: %v", v.id, err)
		}
	}

	if h.Size() != len(vectors) {
		t.Errorf("expected size %d, got %d", len(vectors), h.Size())
	}
}

// TestAddBatchToEmptyIndex tests batch adding to an empty index
func TestAddBatchToEmptyIndex(t *testing.T) {
	h := NewHNSW(nil)

	items := []VectorItem{
		{ID: "b1", Vector: []float32{1.0, 0.0, 0.0}},
		{ID: "b2", Vector: []float32{0.0, 1.0, 0.0}},
		{ID: "b3", Vector: []float32{0.0, 0.0, 1.0}},
		{ID: "b4", Vector: []float32{0.5, 0.5, 0.0}},
		{ID: "b5", Vector: []float32{0.0, 0.5, 0.5}},
	}

	err := h.AddBatch(items)
	if err != nil {
		t.Fatalf("failed to add batch: %v", err)
	}

	if h.Size() != len(items) {
		t.Errorf("expected size %d, got %d", len(items), h.Size())
	}
}

// TestSearchEmptyIndex verifies searching an empty index doesn't panic
func TestSearchEmptyIndex(t *testing.T) {
	h := NewHNSW(nil)

	results, err := h.Search([]float32{1.0, 0.0, 0.0}, 5)
	if err != nil {
		t.Fatalf("search on empty index returned error: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results from empty index, got %d", len(results))
	}
}

// TestSearchAfterAdd verifies search works after adding vectors
func TestSearchAfterAdd(t *testing.T) {
	h := NewHNSW(nil)

	// Add vectors
	h.Add("v1", []float32{1.0, 0.0, 0.0})
	h.Add("v2", []float32{0.0, 1.0, 0.0})
	h.Add("v3", []float32{0.0, 0.0, 1.0})

	// Search for vector similar to v1
	results, err := h.Search([]float32{0.9, 0.1, 0.0}, 2)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// v1 should be the closest
	if results[0].ID != "v1" {
		t.Errorf("expected v1 to be closest, got %s", results[0].ID)
	}
}

// TestDeleteAndReAdd tests deleting all vectors and re-adding
func TestDeleteAndReAdd(t *testing.T) {
	h := NewHNSW(nil)

	// Add vectors
	h.Add("v1", []float32{1.0, 0.0, 0.0})
	h.Add("v2", []float32{0.0, 1.0, 0.0})

	// Delete all
	h.Delete("v1")
	h.Delete("v2")

	if h.Size() != 0 {
		t.Errorf("expected size 0 after deletes, got %d", h.Size())
	}

	// Re-add - this tests the scenario similar to SetEmbeddingProvider
	// where we rebuild from an empty state
	err := h.Add("v3", []float32{1.0, 0.0, 0.0})
	if err != nil {
		t.Fatalf("failed to add after delete: %v", err)
	}

	err = h.Add("v4", []float32{0.0, 1.0, 0.0})
	if err != nil {
		t.Fatalf("failed to add second vector after delete: %v", err)
	}

	if h.Size() != 2 {
		t.Errorf("expected size 2, got %d", h.Size())
	}
}

// TestUpdateExistingVector tests updating an existing vector
func TestUpdateExistingVector(t *testing.T) {
	h := NewHNSW(nil)

	// Add initial vector
	h.Add("v1", []float32{1.0, 0.0, 0.0})

	// Update same ID with different vector
	err := h.Add("v1", []float32{0.0, 1.0, 0.0})
	if err != nil {
		t.Fatalf("failed to update vector: %v", err)
	}

	// Size should still be 1
	if h.Size() != 1 {
		t.Errorf("expected size 1 after update, got %d", h.Size())
	}

	// Search should find updated vector
	results, _ := h.Search([]float32{0.0, 1.0, 0.0}, 1)
	if len(results) == 0 || results[0].ID != "v1" {
		t.Error("expected to find updated vector")
	}
}

// TestDimensionMismatch verifies dimension checking
func TestDimensionMismatch(t *testing.T) {
	h := NewHNSW(nil)

	// Add first vector with dimension 3
	err := h.Add("v1", []float32{1.0, 0.0, 0.0})
	if err != nil {
		t.Fatalf("failed to add first vector: %v", err)
	}

	// Try to add vector with different dimension
	err = h.Add("v2", []float32{1.0, 0.0, 0.0, 0.0})
	if err == nil {
		t.Error("expected dimension mismatch error")
	}
}

// TestSaveAndLoad tests persistence
func TestSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.idx")

	// Create and populate index
	h1 := NewHNSW(nil)
	h1.Add("v1", []float32{1.0, 0.0, 0.0})
	h1.Add("v2", []float32{0.0, 1.0, 0.0})
	h1.Add("v3", []float32{0.0, 0.0, 1.0})

	// Save
	err := h1.SaveToFile(indexPath)
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Load into new index
	h2 := NewHNSW(nil)
	err = h2.LoadFromFile(indexPath)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// Verify size
	if h2.Size() != 3 {
		t.Errorf("expected size 3 after load, got %d", h2.Size())
	}

	// Verify search works
	results, err := h2.Search([]float32{1.0, 0.0, 0.0}, 1)
	if err != nil {
		t.Fatalf("search after load failed: %v", err)
	}
	if len(results) == 0 || results[0].ID != "v1" {
		t.Error("search after load returned unexpected results")
	}
}

// TestLoadNonExistentFile verifies loading non-existent file doesn't error
func TestLoadNonExistentFile(t *testing.T) {
	h := NewHNSW(nil)
	err := h.LoadFromFile("/nonexistent/path/index.idx")
	if err != nil {
		t.Errorf("loading non-existent file should not error, got: %v", err)
	}
}

// TestAddAfterLoadEmpty simulates the SetEmbeddingProvider scenario
// where we load an empty/non-existent index file and then add vectors
func TestAddAfterLoadEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "empty.idx")

	h := NewHNSW(nil)

	// Load non-existent file (simulates fresh start)
	h.LoadFromFile(indexPath)

	// Add vectors (simulates rebuilding from SQLite vectors)
	vectors := []struct {
		id  string
		vec []float32
	}{
		{"doc1:block1", []float32{0.1, 0.2, 0.3, 0.4}},
		{"doc1:block2", []float32{0.2, 0.3, 0.4, 0.5}},
		{"doc2:block1", []float32{0.3, 0.4, 0.5, 0.6}},
	}

	for _, v := range vectors {
		if err := h.Add(v.id, v.vec); err != nil {
			t.Fatalf("failed to add %s: %v", v.id, err)
		}
	}

	if h.Size() != 3 {
		t.Errorf("expected size 3, got %d", h.Size())
	}

	// Verify search works
	results, err := h.Search([]float32{0.1, 0.2, 0.3, 0.4}, 3)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

// TestDeleteEntryPoint tests deleting the entry point node
func TestDeleteEntryPoint(t *testing.T) {
	h := NewHNSW(nil)

	h.Add("v1", []float32{1.0, 0.0, 0.0})
	h.Add("v2", []float32{0.0, 1.0, 0.0})
	h.Add("v3", []float32{0.0, 0.0, 1.0})

	// Delete entry point (v1 is likely entry point as first added)
	h.Delete("v1")

	// Should still be able to search
	results, err := h.Search([]float32{0.0, 1.0, 0.0}, 2)
	if err != nil {
		t.Fatalf("search after entry deletion failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}

// TestDeleteBatch tests batch deletion
func TestDeleteBatch(t *testing.T) {
	h := NewHNSW(nil)

	h.Add("v1", []float32{1.0, 0.0, 0.0})
	h.Add("v2", []float32{0.0, 1.0, 0.0})
	h.Add("v3", []float32{0.0, 0.0, 1.0})
	h.Add("v4", []float32{0.5, 0.5, 0.0})

	err := h.DeleteBatch([]string{"v1", "v3"})
	if err != nil {
		t.Fatalf("batch delete failed: %v", err)
	}

	if h.Size() != 2 {
		t.Errorf("expected size 2 after batch delete, got %d", h.Size())
	}
}

// TestDirtyTracking tests the dirty flag and pending changes tracking
func TestDirtyTracking(t *testing.T) {
	h := NewHNSW(nil)

	if h.IsDirty() {
		t.Error("new index should not be dirty")
	}

	// Add first vector (becomes entry point) - should now track dirty state
	h.Add("v1", []float32{1.0, 0.0, 0.0})

	// First node should now be tracked as dirty (entry point fix)
	if !h.IsDirty() {
		t.Error("index should be dirty after adding first node (entry point)")
	}

	adds, deletes := h.PendingChanges()
	if adds != 1 {
		t.Errorf("expected 1 add after first node, got %d adds", adds)
	}

	// Add second vector
	h.Add("v2", []float32{0.0, 1.0, 0.0})

	adds, deletes = h.PendingChanges()
	if adds != 2 {
		t.Errorf("expected 2 adds after second node, got %d adds", adds)
	}

	h.Delete("v1")

	adds, deletes = h.PendingChanges()
	if deletes != 1 {
		t.Errorf("expected 1 delete, got %d deletes", deletes)
	}

	h.MarkClean()

	if h.IsDirty() {
		t.Error("index should not be dirty after MarkClean")
	}

	adds, deletes = h.PendingChanges()
	if adds != 0 || deletes != 0 {
		t.Errorf("expected 0 adds, 0 deletes after MarkClean, got %d adds, %d deletes", adds, deletes)
	}
}

// TestEntryPointDirtyTracking specifically tests that the first node (entry point) is tracked
func TestEntryPointDirtyTracking(t *testing.T) {
	h := NewHNSW(nil)

	// Verify clean state
	if h.IsDirty() {
		t.Error("new index should not be dirty")
	}

	adds, _ := h.PendingChanges()
	if adds != 0 {
		t.Errorf("new index should have 0 pending adds, got %d", adds)
	}

	// Add single node - this becomes entry point
	err := h.Add("entry", []float32{1.0, 2.0, 3.0})
	if err != nil {
		t.Fatalf("failed to add entry point: %v", err)
	}

	// Entry point should trigger dirty tracking
	if !h.IsDirty() {
		t.Error("index should be dirty after adding entry point node")
	}

	adds, _ = h.PendingChanges()
	if adds != 1 {
		t.Errorf("expected 1 pending add for entry point, got %d", adds)
	}

	// Save and verify clean state
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "entry_test.idx")
	err = h.SaveToFile(indexPath)
	if err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	if h.IsDirty() {
		t.Error("index should not be dirty after save")
	}

	// Verify file was created (single node was persisted)
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("index file should exist after save")
	}
}

// TestSaveIfDirty tests conditional saving
func TestSaveIfDirty(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.idx")

	h := NewHNSW(nil)

	// Should not create file when not dirty
	err := h.SaveIfDirty(indexPath)
	if err != nil {
		t.Fatalf("SaveIfDirty failed: %v", err)
	}

	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Error("file should not exist when index is not dirty")
	}

	// Add first vector (entry point) - now properly triggers dirty flag
	h.Add("v1", []float32{1.0, 0.0, 0.0})

	// Should save now (even single node triggers dirty with fix)
	err = h.SaveIfDirty(indexPath)
	if err != nil {
		t.Fatalf("SaveIfDirty failed: %v", err)
	}

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("file should exist after SaveIfDirty on dirty index (single node)")
	}
}

// TestCosineDistance tests the cosine distance calculation
func TestCosineDistance(t *testing.T) {
	// Identical vectors should have distance 0
	dist := cosineDistance([]float32{1.0, 0.0, 0.0}, []float32{1.0, 0.0, 0.0})
	if dist > 0.0001 {
		t.Errorf("identical vectors should have distance ~0, got %f", dist)
	}

	// Orthogonal vectors should have distance 1
	dist = cosineDistance([]float32{1.0, 0.0, 0.0}, []float32{0.0, 1.0, 0.0})
	if dist < 0.9999 || dist > 1.0001 {
		t.Errorf("orthogonal vectors should have distance ~1, got %f", dist)
	}

	// Opposite vectors should have distance 2
	dist = cosineDistance([]float32{1.0, 0.0, 0.0}, []float32{-1.0, 0.0, 0.0})
	if dist < 1.9999 || dist > 2.0001 {
		t.Errorf("opposite vectors should have distance ~2, got %f", dist)
	}
}

// TestEmptyBatchOperations tests batch operations with empty inputs
func TestEmptyBatchOperations(t *testing.T) {
	h := NewHNSW(nil)

	// Empty add batch should not error
	err := h.AddBatch(nil)
	if err != nil {
		t.Errorf("empty AddBatch should not error: %v", err)
	}

	err = h.AddBatch([]VectorItem{})
	if err != nil {
		t.Errorf("empty AddBatch should not error: %v", err)
	}

	// Empty delete batch should not error
	err = h.DeleteBatch(nil)
	if err != nil {
		t.Errorf("empty DeleteBatch should not error: %v", err)
	}

	err = h.DeleteBatch([]string{})
	if err != nil {
		t.Errorf("empty DeleteBatch should not error: %v", err)
	}
}
