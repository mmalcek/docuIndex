package search

import (
	"testing"
)

func TestRRFFusion(t *testing.T) {
	keyword := []FusedResult{
		{DocumentID: "d1", BlockID: "b1", KeywordScore: 1.0},
		{DocumentID: "d2", BlockID: "b2", KeywordScore: 0.8},
	}

	vector := []FusedResult{
		{DocumentID: "d2", BlockID: "b2", VectorScore: 0.9},
		{DocumentID: "d3", BlockID: "b3", VectorScore: 0.7},
	}

	config := DefaultFusionConfig()
	result := RRFFusion(keyword, vector, config)

	// Should have 3 unique results
	if len(result) != 3 {
		t.Fatalf("Expected 3 fused results, got %d", len(result))
	}

	// d2 appears in both lists, should have highest fused score
	if result[0].DocumentID != "d2" {
		t.Errorf("Expected d2 to be first (in both lists), got %s", result[0].DocumentID)
	}

	// Verify d2 has both ranks set
	if result[0].KeywordRank != 2 {
		t.Errorf("d2 KeywordRank = %d, want 2", result[0].KeywordRank)
	}
	if result[0].VectorRank != 1 {
		t.Errorf("d2 VectorRank = %d, want 1", result[0].VectorRank)
	}
}

func TestRRFFusionEmptyInputs(t *testing.T) {
	// Empty keyword results
	result := RRFFusion(nil, []FusedResult{{DocumentID: "d1", BlockID: "b1", VectorScore: 0.9}}, nil)
	if len(result) != 1 {
		t.Errorf("Expected 1 result with empty keyword, got %d", len(result))
	}

	// Empty vector results
	result = RRFFusion([]FusedResult{{DocumentID: "d1", BlockID: "b1", KeywordScore: 1.0}}, nil, nil)
	if len(result) != 1 {
		t.Errorf("Expected 1 result with empty vector, got %d", len(result))
	}

	// Both empty
	result = RRFFusion(nil, nil, nil)
	if len(result) != 0 {
		t.Errorf("Expected 0 results with both empty, got %d", len(result))
	}
}

func TestRRFFusionWithWeights(t *testing.T) {
	keyword := []FusedResult{{DocumentID: "d1", BlockID: "b1", KeywordScore: 1.0}}
	vector := []FusedResult{{DocumentID: "d2", BlockID: "b2", VectorScore: 1.0}}

	// Heavy keyword weight
	config := &FusionConfig{K: 60, KeywordWeight: 0.9, VectorWeight: 0.1}
	result := RRFFusion(keyword, vector, config)

	if result[0].DocumentID != "d1" {
		t.Error("With heavy keyword weight, d1 should rank first")
	}

	// Heavy vector weight
	config = &FusionConfig{K: 60, KeywordWeight: 0.1, VectorWeight: 0.9}
	result = RRFFusion(keyword, vector, config)

	if result[0].DocumentID != "d2" {
		t.Error("With heavy vector weight, d2 should rank first")
	}
}

func TestRRFFusionWithWeightedCombination(t *testing.T) {
	keyword := []FusedResult{{DocumentID: "d1", BlockID: "b1", KeywordScore: 10.0}}
	vector := []FusedResult{{DocumentID: "d2", BlockID: "b2", VectorScore: 0.9}}

	config := &FusionConfig{
		UseWeighted:   true,
		KeywordWeight: 0.5,
		VectorWeight:  0.5,
	}
	result := RRFFusion(keyword, vector, config)

	// With weighted combination, d1 should score higher (10*0.5 vs 0.9*0.5)
	if result[0].DocumentID != "d1" {
		t.Error("With weighted combination and higher keyword score, d1 should rank first")
	}
}

func TestRRFFusionDefaultRanks(t *testing.T) {
	// When a result is missing from one list, it gets rank len(list)+1
	keyword := []FusedResult{
		{DocumentID: "d1", BlockID: "b1"},
		{DocumentID: "d2", BlockID: "b2"},
	}
	vector := []FusedResult{
		{DocumentID: "d3", BlockID: "b3"},
	}

	result := RRFFusion(keyword, vector, nil)

	// Find d3 in results - it should have KeywordRank = 3 (len(keyword)+1)
	for _, r := range result {
		if r.DocumentID == "d3" {
			if r.KeywordRank != 3 {
				t.Errorf("d3 KeywordRank = %d, want 3 (len(keyword)+1)", r.KeywordRank)
			}
		}
		if r.DocumentID == "d1" || r.DocumentID == "d2" {
			if r.VectorRank != 2 {
				t.Errorf("%s VectorRank = %d, want 2 (len(vector)+1)", r.DocumentID, r.VectorRank)
			}
		}
	}
}

func TestNormalizeFusedScores(t *testing.T) {
	results := []FusedResult{
		{DocumentID: "d1", FusedScore: 10.0},
		{DocumentID: "d2", FusedScore: 5.0},
		{DocumentID: "d3", FusedScore: 2.5},
	}

	NormalizeFusedScores(results)

	// Max should be 1.0
	if results[0].FusedScore != 1.0 {
		t.Errorf("Max score should be 1.0, got %f", results[0].FusedScore)
	}

	// Others should be proportional
	if results[1].FusedScore != 0.5 {
		t.Errorf("Expected 0.5, got %f", results[1].FusedScore)
	}
	if results[2].FusedScore != 0.25 {
		t.Errorf("Expected 0.25, got %f", results[2].FusedScore)
	}
}

func TestNormalizeFusedScoresEmpty(t *testing.T) {
	// Should not panic on empty
	NormalizeFusedScores(nil)
	NormalizeFusedScores([]FusedResult{})
}

func TestNormalizeFusedScoresZeroMax(t *testing.T) {
	results := []FusedResult{
		{DocumentID: "d1", FusedScore: 0.0},
	}

	// Should not divide by zero
	NormalizeFusedScores(results)

	if results[0].FusedScore != 0.0 {
		t.Errorf("Score should remain 0, got %f", results[0].FusedScore)
	}
}

func TestFilterByFusedScore(t *testing.T) {
	results := []FusedResult{
		{DocumentID: "d1", FusedScore: 0.9},
		{DocumentID: "d2", FusedScore: 0.5},
		{DocumentID: "d3", FusedScore: 0.1},
	}

	filtered := FilterByFusedScore(results, 0.4)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 results above 0.4, got %d", len(filtered))
	}

	// Verify d3 is filtered out
	for _, r := range filtered {
		if r.DocumentID == "d3" {
			t.Error("d3 should be filtered out (score < 0.4)")
		}
	}
}

func TestLimitFusedResults(t *testing.T) {
	results := []FusedResult{
		{DocumentID: "d1"},
		{DocumentID: "d2"},
		{DocumentID: "d3"},
		{DocumentID: "d4"},
	}

	limited := LimitFusedResults(results, 2)
	if len(limited) != 2 {
		t.Errorf("Expected 2 results, got %d", len(limited))
	}

	// Limit of 0 should return all
	limited = LimitFusedResults(results, 0)
	if len(limited) != 4 {
		t.Errorf("Expected 4 results with limit 0, got %d", len(limited))
	}

	// Limit greater than length should return all
	limited = LimitFusedResults(results, 10)
	if len(limited) != 4 {
		t.Errorf("Expected 4 results with limit > len, got %d", len(limited))
	}
}

func TestDiversifyByDocument(t *testing.T) {
	results := []FusedResult{
		{DocumentID: "d1", BlockID: "b1"},
		{DocumentID: "d1", BlockID: "b2"},
		{DocumentID: "d1", BlockID: "b3"},
		{DocumentID: "d2", BlockID: "b1"},
		{DocumentID: "d2", BlockID: "b2"},
	}

	diversified := DiversifyByDocument(results, 2)

	// Count results per document
	counts := make(map[string]int)
	for _, r := range diversified {
		counts[r.DocumentID]++
	}

	if counts["d1"] > 2 {
		t.Errorf("Expected max 2 from d1, got %d", counts["d1"])
	}
	if counts["d2"] > 2 {
		t.Errorf("Expected max 2 from d2, got %d", counts["d2"])
	}

	// Total should be 4 (2 from each)
	if len(diversified) != 4 {
		t.Errorf("Expected 4 diversified results, got %d", len(diversified))
	}
}

func TestKeywordToFusedResults(t *testing.T) {
	keyword := []SearchResult{
		{
			DocumentID:   "d1",
			DocumentName: "doc1.pdf",
			BlockID:      "b1",
			Content:      "test content",
			Snippet:      "test...",
			Score:        1.5,
			Page:         1,
			Section:      "intro",
			Positions:    []int{0, 5},
		},
	}

	fused := KeywordToFusedResults(keyword)

	if len(fused) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(fused))
	}

	r := fused[0]
	if r.DocumentID != "d1" {
		t.Errorf("DocumentID = %q, want %q", r.DocumentID, "d1")
	}
	if r.KeywordScore != 1.5 {
		t.Errorf("KeywordScore = %f, want %f", r.KeywordScore, 1.5)
	}
	if r.Content != "test content" {
		t.Errorf("Content = %q, want %q", r.Content, "test content")
	}
}

func TestVectorToFusedResults(t *testing.T) {
	vector := []VectorSearchResult{
		{
			DocumentID: "d1",
			BlockID:    "b1",
			Score:      0.95,
			Distance:   0.05,
		},
	}

	fused := VectorToFusedResults(vector)

	if len(fused) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(fused))
	}

	r := fused[0]
	if r.DocumentID != "d1" {
		t.Errorf("DocumentID = %q, want %q", r.DocumentID, "d1")
	}
	// Use approximate comparison for float32 to float64 conversion
	if r.VectorScore < 0.94 || r.VectorScore > 0.96 {
		t.Errorf("VectorScore = %f, want ~0.95", r.VectorScore)
	}
}

func TestDefaultFusionConfig(t *testing.T) {
	cfg := DefaultFusionConfig()

	if cfg.K != 60 {
		t.Errorf("K = %f, want 60", cfg.K)
	}
	if cfg.KeywordWeight != 0.5 {
		t.Errorf("KeywordWeight = %f, want 0.5", cfg.KeywordWeight)
	}
	if cfg.VectorWeight != 0.5 {
		t.Errorf("VectorWeight = %f, want 0.5", cfg.VectorWeight)
	}
	if cfg.UseWeighted {
		t.Error("UseWeighted should be false by default")
	}
}
