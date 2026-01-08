package search

import (
	"sort"
)

// FusedResult represents a combined search result
type FusedResult struct {
	DocumentID   string
	DocumentName string
	BlockID      string
	Content      string
	Snippet      string
	Page         int
	Section      string
	Positions    []int

	// Scoring
	KeywordScore  float64 // BM25 score
	VectorScore   float64 // Vector similarity score
	FusedScore    float64 // Combined score
	KeywordRank   int     // Rank in keyword results
	VectorRank    int     // Rank in vector results
}

// FusionConfig configures result fusion
type FusionConfig struct {
	// K parameter for RRF (default: 60)
	K float64

	// Weight for keyword results (default: 0.5)
	KeywordWeight float64

	// Weight for vector results (default: 0.5)
	VectorWeight float64

	// Use weighted combination instead of RRF
	UseWeighted bool
}

// DefaultFusionConfig returns default fusion configuration
func DefaultFusionConfig() *FusionConfig {
	return &FusionConfig{
		K:             60,
		KeywordWeight: 0.5,
		VectorWeight:  0.5,
		UseWeighted:   false,
	}
}

// RRFFusion implements Reciprocal Rank Fusion
// RRF(d) = Σ 1/(k + rank(d))
func RRFFusion(keywordResults, vectorResults []FusedResult, config *FusionConfig) []FusedResult {
	if config == nil {
		config = DefaultFusionConfig()
	}

	// Map to track all results by block ID (doc_id:block_id)
	resultMap := make(map[string]*FusedResult)

	// Process keyword results
	for rank, r := range keywordResults {
		key := r.DocumentID + ":" + r.BlockID
		if existing, ok := resultMap[key]; ok {
			existing.KeywordScore = r.KeywordScore
			existing.KeywordRank = rank + 1
		} else {
			result := r
			result.KeywordRank = rank + 1
			result.VectorRank = len(vectorResults) + 1 // Default to worst rank
			resultMap[key] = &result
		}
	}

	// Process vector results
	for rank, r := range vectorResults {
		key := r.DocumentID + ":" + r.BlockID
		if existing, ok := resultMap[key]; ok {
			existing.VectorScore = r.VectorScore
			existing.VectorRank = rank + 1
		} else {
			result := r
			result.VectorRank = rank + 1
			result.KeywordRank = len(keywordResults) + 1 // Default to worst rank
			resultMap[key] = &result
		}
	}

	// Calculate fused scores
	var results []FusedResult
	for _, r := range resultMap {
		if config.UseWeighted {
			// Weighted combination
			r.FusedScore = config.KeywordWeight*r.KeywordScore + config.VectorWeight*r.VectorScore
		} else {
			// RRF
			rrfKeyword := 1.0 / (config.K + float64(r.KeywordRank))
			rrfVector := 1.0 / (config.K + float64(r.VectorRank))
			r.FusedScore = config.KeywordWeight*rrfKeyword + config.VectorWeight*rrfVector
		}
		results = append(results, *r)
	}

	// Sort by fused score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].FusedScore > results[j].FusedScore
	})

	return results
}

// NormalizeScores normalizes scores to 0-1 range
func NormalizeFusedScores(results []FusedResult) {
	if len(results) == 0 {
		return
	}

	// Find max score
	maxScore := results[0].FusedScore
	for _, r := range results {
		if r.FusedScore > maxScore {
			maxScore = r.FusedScore
		}
	}

	// Normalize
	if maxScore > 0 {
		for i := range results {
			results[i].FusedScore /= maxScore
		}
	}
}

// FilterByFusedScore removes results below a score threshold
func FilterByFusedScore(results []FusedResult, minScore float64) []FusedResult {
	var filtered []FusedResult
	for _, r := range results {
		if r.FusedScore >= minScore {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// LimitResults limits the number of results
func LimitFusedResults(results []FusedResult, limit int) []FusedResult {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

// DiversifyByDocument ensures results from different documents
func DiversifyByDocument(results []FusedResult, maxPerDoc int) []FusedResult {
	docCounts := make(map[string]int)
	var diversified []FusedResult

	for _, result := range results {
		count := docCounts[result.DocumentID]
		if count < maxPerDoc {
			diversified = append(diversified, result)
			docCounts[result.DocumentID]++
		}
	}

	return diversified
}

// MergeResults merges keyword and vector results into FusedResult slices
func KeywordToFusedResults(results []SearchResult) []FusedResult {
	fused := make([]FusedResult, len(results))
	for i, r := range results {
		fused[i] = FusedResult{
			DocumentID:   r.DocumentID,
			DocumentName: r.DocumentName,
			BlockID:      r.BlockID,
			Content:      r.Content,
			Snippet:      r.Snippet,
			Page:         r.Page,
			Section:      r.Section,
			Positions:    r.Positions,
			KeywordScore: r.Score,
		}
	}
	return fused
}

// VectorToFusedResults converts vector results to fused results
func VectorToFusedResults(results []VectorSearchResult) []FusedResult {
	fused := make([]FusedResult, len(results))
	for i, r := range results {
		fused[i] = FusedResult{
			DocumentID:  r.DocumentID,
			BlockID:     r.BlockID,
			VectorScore: float64(r.Score),
		}
	}
	return fused
}

// VectorSearchResult represents a vector search result
type VectorSearchResult struct {
	DocumentID string
	BlockID    string
	Score      float32
	Distance   float32
}
