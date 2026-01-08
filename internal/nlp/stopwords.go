// Package nlp provides natural language processing utilities for text analysis.
package nlp

// stopWords contains common English stop words that are typically filtered during text processing.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true,
	"but": true, "in": true, "on": true, "at": true, "to": true,
	"for": true, "of": true, "with": true, "by": true, "from": true,
	"as": true, "is": true, "was": true, "are": true, "were": true,
	"been": true, "be": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true,
	"could": true, "should": true, "may": true, "might": true, "must": true,
	"shall": true, "can": true, "this": true, "that": true, "these": true,
	"those": true, "it": true, "its": true, "they": true, "their": true,
	"them": true, "we": true, "our": true, "you": true, "your": true,
	"he": true, "she": true, "him": true, "her": true, "his": true,
	"not": true, "no": true, "all": true, "each": true, "every": true,
	"both": true, "few": true, "more": true, "most": true, "other": true,
	"some": true, "such": true, "than": true, "too": true, "very": true,
	"just": true, "also": true, "now": true, "only": true, "so": true,
}

// IsStopWord checks if a word is a common English stop word.
func IsStopWord(word string) bool {
	return stopWords[word]
}
