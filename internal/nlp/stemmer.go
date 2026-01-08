package nlp

import "strings"

// Stem applies Porter-like stemming to reduce a word to its root form.
func Stem(word string) string {
	if len(word) <= 3 {
		return word
	}

	suffixes := []string{
		"ational", "tional", "enci", "anci", "izer", "ation", "ator",
		"alism", "iveness", "fulness", "ousness", "aliti", "iviti",
		"biliti", "logi", "ness", "ment", "ent", "ism", "ate", "iti",
		"ous", "ive", "ize", "ing", "ies", "ied", "ion", "ity", "ful",
		"able", "ible", "ant", "al", "er", "ic", "ly", "ed", "es", "s",
	}

	replacements := map[string]string{
		"ational": "ate", "tional": "tion", "enci": "ence", "anci": "ance",
		"izer": "ize", "ization": "ize", "ation": "ate", "ator": "ate",
		"fulness": "ful", "ousness": "ous", "iveness": "ive",
		"ies": "i", "ied": "i",
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
