package nlp

import "strings"

// orderedReplacements defines suffix replacements in order of priority.
// Longer suffixes are checked first to ensure deterministic behavior.
var orderedReplacements = []struct {
	suffix      string
	replacement string
}{
	// Longest suffixes first for correct matching
	{"ization", "ize"},
	{"ational", "ate"},
	{"fulness", "ful"},
	{"ousness", "ous"},
	{"iveness", "ive"},
	{"tional", "tion"},
	{"ation", "ate"},
	{"enci", "ence"},
	{"anci", "ance"},
	{"izer", "ize"},
	{"ator", "ate"},
	{"ies", "i"},
	{"ied", "i"},
}

// orderedSuffixes defines suffixes to remove (no replacement), longest first.
var orderedSuffixes = []string{
	"iveness", "fulness", "ousness",
	"ational", "alism", "aliti", "iviti", "biliti",
	"tional", "ation", "ator", "izer",
	"ness", "ment", "logi",
	"able", "ible",
	"ism", "ate", "iti", "ous", "ive", "ize", "ing", "ion", "ity", "ful", "ant", "ent",
	"al", "er", "ic", "ly", "ed", "es",
	"s",
}

// Stem applies Porter-like stemming to reduce a word to its root form.
// The function is deterministic - the same input always produces the same output.
func Stem(word string) string {
	if len(word) <= 3 {
		return word
	}

	// Check ordered replacements first (longest match wins)
	for _, r := range orderedReplacements {
		if strings.HasSuffix(word, r.suffix) && len(word)-len(r.suffix) >= 2 {
			return word[:len(word)-len(r.suffix)] + r.replacement
		}
	}

	// Then check suffix removal (ordered by length)
	for _, suffix := range orderedSuffixes {
		if strings.HasSuffix(word, suffix) && len(word)-len(suffix) >= 2 {
			return word[:len(word)-len(suffix)]
		}
	}

	return word
}
