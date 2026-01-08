package search

import (
	"regexp"
	"strings"
)

// QueryType represents the type of query
type QueryType int

const (
	QueryTypeSimple QueryType = iota
	QueryTypePhrase
	QueryTypeBoolean
)

// Query represents a parsed search query
type Query struct {
	Type     QueryType
	Terms    []string
	Phrases  []string
	Required []string // Terms that MUST appear (AND)
	Excluded []string // Terms that must NOT appear (NOT)
	Optional []string // Terms that may appear (OR)
}

// QueryParser parses search query strings
type QueryParser struct {
	tokenizer *Tokenizer
}

// NewQueryParser creates a new query parser
func NewQueryParser(tokenizer *Tokenizer) *QueryParser {
	return &QueryParser{tokenizer: tokenizer}
}

// Parse parses a query string
func (qp *QueryParser) Parse(query string) *Query {
	q := &Query{
		Type: QueryTypeSimple,
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return q
	}

	// Extract quoted phrases
	phraseRe := regexp.MustCompile(`"([^"]+)"`)
	phrases := phraseRe.FindAllStringSubmatch(query, -1)
	for _, match := range phrases {
		if len(match) >= 2 {
			q.Phrases = append(q.Phrases, match[1])
		}
	}

	// Remove quoted phrases from query
	query = phraseRe.ReplaceAllString(query, "")

	// Check for boolean operators
	hasBoolean := strings.Contains(query, " AND ") ||
		strings.Contains(query, " OR ") ||
		strings.Contains(query, " NOT ") ||
		strings.Contains(query, "+") ||
		strings.Contains(query, "-")

	if hasBoolean {
		q.Type = QueryTypeBoolean
		qp.parseBoolean(query, q)
	} else if len(q.Phrases) > 0 {
		q.Type = QueryTypePhrase
		// Add remaining terms
		tokens := qp.tokenizer.TokenizeToStrings(query)
		q.Terms = tokens
	} else {
		// Simple query - all terms
		tokens := qp.tokenizer.TokenizeToStrings(query)
		q.Terms = tokens
	}

	return q
}

// parseBoolean parses boolean operators in the query
func (qp *QueryParser) parseBoolean(query string, q *Query) {
	// Split by OR first
	orParts := regexp.MustCompile(`\s+OR\s+`).Split(query, -1)

	for _, orPart := range orParts {
		// Split by AND
		andParts := regexp.MustCompile(`\s+AND\s+`).Split(orPart, -1)

		for _, andPart := range andParts {
			andPart = strings.TrimSpace(andPart)

			// Check for NOT
			if strings.HasPrefix(andPart, "NOT ") {
				rest := strings.TrimPrefix(andPart, "NOT ")
				tokens := qp.tokenizer.TokenizeToStrings(rest)
				q.Excluded = append(q.Excluded, tokens...)
				continue
			}

			// Check for +/- prefix
			if strings.HasPrefix(andPart, "+") {
				rest := strings.TrimPrefix(andPart, "+")
				tokens := qp.tokenizer.TokenizeToStrings(rest)
				q.Required = append(q.Required, tokens...)
				continue
			}

			if strings.HasPrefix(andPart, "-") {
				rest := strings.TrimPrefix(andPart, "-")
				tokens := qp.tokenizer.TokenizeToStrings(rest)
				q.Excluded = append(q.Excluded, tokens...)
				continue
			}

			// Regular term
			tokens := qp.tokenizer.TokenizeToStrings(andPart)
			if len(orParts) > 1 {
				q.Optional = append(q.Optional, tokens...)
			} else {
				q.Required = append(q.Required, tokens...)
			}
		}
	}

	// Combine all terms for backward compatibility
	q.Terms = append(q.Terms, q.Required...)
	q.Terms = append(q.Terms, q.Optional...)
}

// MatchesDocument checks if a query matches a document (for post-filtering)
func (q *Query) MatchesDocument(text string, tokenizer *Tokenizer) bool {
	tokens := tokenizer.TokenizeToStrings(text)
	tokenSet := make(map[string]bool)
	for _, t := range tokens {
		tokenSet[t] = true
	}

	// Check required terms
	for _, term := range q.Required {
		if !tokenSet[term] {
			return false
		}
	}

	// Check excluded terms
	for _, term := range q.Excluded {
		if tokenSet[term] {
			return false
		}
	}

	// Check phrases
	for _, phrase := range q.Phrases {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(phrase)) {
			return false
		}
	}

	return true
}

// HighlightMatches highlights query terms in text
func (q *Query) HighlightMatches(text, pre, post string) string {
	result := text

	// Highlight phrases first
	for _, phrase := range q.Phrases {
		re := regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(phrase) + `)`)
		result = re.ReplaceAllString(result, pre+"$1"+post)
	}

	// Highlight individual terms
	allTerms := append(q.Terms, q.Required...)
	allTerms = append(allTerms, q.Optional...)

	for _, term := range allTerms {
		// Match word boundaries
		re := regexp.MustCompile(`(?i)\b(` + regexp.QuoteMeta(term) + `\w*)\b`)
		result = re.ReplaceAllString(result, pre+"$1"+post)
	}

	return result
}

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
