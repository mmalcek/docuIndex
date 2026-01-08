package search

import (
	"math"
	"sort"
)

// BM25Parameters holds BM25 ranking parameters
type BM25Parameters struct {
	K1 float64 // Term frequency saturation parameter (default 1.2)
	B  float64 // Document length normalization (default 0.75)
}

// DefaultBM25Parameters returns default BM25 parameters
func DefaultBM25Parameters() BM25Parameters {
	return BM25Parameters{
		K1: 1.2,
		B:  0.75,
	}
}

// Ranker ranks search results
type Ranker struct {
	params BM25Parameters
}

// NewRanker creates a new ranker
func NewRanker(params BM25Parameters) *Ranker {
	return &Ranker{params: params}
}

// RankResults sorts and scores results
func (r *Ranker) RankResults(results []SearchResult) []SearchResult {
	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results
}

// CalculateBM25 calculates BM25 score for a term in a document
func (r *Ranker) CalculateBM25(tf, df, N, dl, avgdl float64) float64 {
	// IDF component
	idf := math.Log((N-df+0.5)/(df+0.5) + 1)

	// TF component with length normalization
	tfNorm := (tf * (r.params.K1 + 1)) / (tf + r.params.K1*(1-r.params.B+r.params.B*(dl/avgdl)))

	return idf * tfNorm
}

// BoostHeadings applies a boost to results from heading blocks
func BoostHeadings(results []SearchResult, docs map[string]*Document, boost float64) {
	for i := range results {
		doc, ok := docs[results[i].DocumentID]
		if !ok {
			continue
		}

		block := doc.GetBlockByID(results[i].BlockID)
		if block != nil && block.Semantic.IsHeading {
			results[i].Score *= boost
		}
	}
}

// BoostExactMatch applies a boost to results with exact phrase match
func BoostExactMatch(results []SearchResult, query string, boost float64) {
	queryLower := query
	for i := range results {
		if containsExact(results[i].Content, queryLower) {
			results[i].Score *= boost
		}
	}
}

// containsExact checks for exact phrase match (case insensitive)
func containsExact(text, phrase string) bool {
	// Simple contains check - could be enhanced with word boundary detection
	textLower := text
	for i := 0; i <= len(textLower)-len(phrase); i++ {
		if textLower[i:i+len(phrase)] == phrase {
			return true
		}
	}
	return false
}

// DiversifyResults ensures results from different documents/sections
func DiversifyResults(results []SearchResult, maxPerDoc int) []SearchResult {
	docCounts := make(map[string]int)
	var diversified []SearchResult

	for _, result := range results {
		count := docCounts[result.DocumentID]
		if count < maxPerDoc {
			diversified = append(diversified, result)
			docCounts[result.DocumentID]++
		}
	}

	return diversified
}

// ScoreByRecency could apply time-based boosting (placeholder for future use)
func ScoreByRecency(results []SearchResult, docs map[string]*Document) {
	// This would boost more recent documents if timestamps are available
	// Placeholder for future implementation
}

// CombineScores combines multiple scoring signals
func CombineScores(bm25Score, headingBoost, recencyBoost float64) float64 {
	return bm25Score * headingBoost * recencyBoost
}

// NormalizeScores normalizes scores to 0-1 range
func NormalizeScores(results []SearchResult) {
	if len(results) == 0 {
		return
	}

	maxScore := results[0].Score
	for _, r := range results {
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}

	if maxScore > 0 {
		for i := range results {
			results[i].Score /= maxScore
		}
	}
}

// FilterByScore removes results below a score threshold
func FilterByScore(results []SearchResult, minScore float64) []SearchResult {
	var filtered []SearchResult
	for _, r := range results {
		if r.Score >= minScore {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// GroupByDocument groups results by document
func GroupByDocument(results []SearchResult) map[string][]SearchResult {
	grouped := make(map[string][]SearchResult)
	for _, r := range results {
		grouped[r.DocumentID] = append(grouped[r.DocumentID], r)
	}
	return grouped
}

// TopResultPerDocument returns the top result for each document
func TopResultPerDocument(results []SearchResult) []SearchResult {
	grouped := GroupByDocument(results)
	var top []SearchResult

	for _, docResults := range grouped {
		if len(docResults) > 0 {
			// Results should already be sorted by score
			top = append(top, docResults[0])
		}
	}

	// Sort by score
	sort.Slice(top, func(i, j int) bool {
		return top[i].Score > top[j].Score
	})

	return top
}
