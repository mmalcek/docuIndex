package search

import (
	"strings"
)

// ExtractSnippet extracts a snippet around matched terms
func ExtractSnippet(text string, positions []int, windowSize int) string {
	if len(text) == 0 {
		return ""
	}

	if len(positions) == 0 || windowSize <= 0 {
		// Return beginning of text
		if len(text) > 200 {
			return text[:200] + "..."
		}
		return text
	}

	// Find the best window containing the most matches
	runes := []rune(text)

	// Simple approach: take context around first match position
	// Positions are token positions, not character positions
	// We'll estimate character position based on word count

	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	// Find word at approximate position
	targetWord := positions[0]
	if targetWord >= len(words) {
		targetWord = len(words) - 1
	}

	// Build snippet around target word
	start := targetWord - windowSize
	if start < 0 {
		start = 0
	}
	end := targetWord + windowSize + 1
	if end > len(words) {
		end = len(words)
	}

	snippet := strings.Join(words[start:end], " ")

	// Add ellipsis
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(words) {
		snippet = snippet + "..."
	}

	// Limit total length
	if len(runes) > 300 {
		return string(runes[:300]) + "..."
	}

	return snippet
}
