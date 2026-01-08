package search

import (
	"strings"
	"unicode"
)

// Tokenizer handles text tokenization for indexing and search
type Tokenizer struct {
	enableStemming  bool
	enableStopWords bool
}

// NewTokenizer creates a new tokenizer
func NewTokenizer(enableStemming, enableStopWords bool) *Tokenizer {
	return &Tokenizer{
		enableStemming:  enableStemming,
		enableStopWords: enableStopWords,
	}
}

// Token represents a single token with position info
type Token struct {
	Text     string
	Position int
	Start    int // Start offset in original text
	End      int // End offset in original text
}

// Tokenize splits text into tokens
func (t *Tokenizer) Tokenize(text string) []Token {
	var tokens []Token
	var current strings.Builder
	position := 0
	startOffset := 0

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if current.Len() == 0 {
				startOffset = i
			}
			current.WriteRune(unicode.ToLower(r))
		} else {
			if current.Len() > 0 {
				word := current.String()
				current.Reset()

				if t.shouldInclude(word) {
					if t.enableStemming {
						word = stem(word)
					}
					tokens = append(tokens, Token{
						Text:     word,
						Position: position,
						Start:    startOffset,
						End:      i,
					})
					position++
				}
			}
		}
	}

	// Don't forget last word
	if current.Len() > 0 {
		word := current.String()
		if t.shouldInclude(word) {
			if t.enableStemming {
				word = stem(word)
			}
			tokens = append(tokens, Token{
				Text:     word,
				Position: position,
				Start:    startOffset,
				End:      len(runes),
			})
		}
	}

	return tokens
}

// TokenizeToStrings returns just the token strings
func (t *Tokenizer) TokenizeToStrings(text string) []string {
	tokens := t.Tokenize(text)
	result := make([]string, len(tokens))
	for i, tok := range tokens {
		result[i] = tok.Text
	}
	return result
}

// shouldInclude checks if a word should be included in the index
func (t *Tokenizer) shouldInclude(word string) bool {
	if len(word) < 2 {
		return false
	}

	if t.enableStopWords && isStopWord(word) {
		return false
	}

	return true
}

// stem applies a simple Porter-like stemming algorithm
func stem(word string) string {
	if len(word) <= 3 {
		return word
	}

	// Simple suffix removal rules
	suffixes := []string{
		"ational", "tional", "enci", "anci", "izer", "ation", "ator",
		"alism", "iveness", "fulness", "ousness", "aliti", "iviti",
		"biliti", "logi", "alli", "entli", "eli", "ousli", "ization",
		"ation", "ator", "alism", "iveness", "fulness", "ousness",
		"ness", "ment", "ent", "ism", "ate", "iti", "ous", "ive", "ize",
		"ing", "ies", "ied", "ion", "ity", "ful", "ness", "ment",
		"able", "ible", "ant", "ent", "ism", "ate", "iti", "ous",
		"ive", "ize", "al", "er", "ic", "ly", "ed", "es", "s",
	}

	replacements := map[string]string{
		"ational": "ate",
		"tional":  "tion",
		"enci":    "ence",
		"anci":    "ance",
		"izer":    "ize",
		"ization": "ize",
		"ation":   "ate",
		"ator":    "ate",
		"fulness": "ful",
		"ousness": "ous",
		"iveness": "ive",
		"ies":     "i",
		"ied":     "i",
	}

	for suffix, replacement := range replacements {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 2 {
			return word[:len(word)-len(suffix)] + replacement
		}
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 2 {
			return word[:len(word)-len(suffix)]
		}
	}

	return word
}

// isStopWord checks if a word is a common stop word
func isStopWord(word string) bool {
	stopWords := map[string]bool{
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
	return stopWords[word]
}

// GenerateNGrams generates n-grams from text for fuzzy matching
func GenerateNGrams(text string, n int) []string {
	text = strings.ToLower(text)
	if len(text) < n {
		return []string{text}
	}

	var ngrams []string
	runes := []rune(text)
	for i := 0; i <= len(runes)-n; i++ {
		ngrams = append(ngrams, string(runes[i:i+n]))
	}
	return ngrams
}
