// Package nlp provides natural language processing utilities for text analysis.
package nlp

// stopWords contains common English stop words that are typically filtered during text processing.
// This list includes ~175 common stop words for improved search quality.
var stopWords = map[string]bool{
	// Articles and determiners
	"the": true, "a": true, "an": true,
	"this": true, "that": true, "these": true, "those": true,

	// Conjunctions
	"and": true, "or": true, "but": true, "nor": true, "yet": true,
	"so": true, "for": true, "because": true, "although": true, "though": true,
	"while": true, "whereas": true, "unless": true, "if": true, "then": true,

	// Prepositions
	"in": true, "on": true, "at": true, "to": true, "of": true,
	"with": true, "by": true, "from": true, "as": true, "into": true,
	"through": true, "during": true, "before": true, "after": true, "above": true,
	"below": true, "between": true, "under": true, "over": true, "out": true,
	"up": true, "down": true, "off": true, "about": true, "against": true,
	"among": true, "around": true, "within": true, "without": true, "upon": true,
	"along": true, "across": true, "behind": true, "beyond": true, "toward": true,
	"towards": true, "beside": true, "besides": true, "beneath": true, "onto": true,

	// Be verbs
	"is": true, "was": true, "are": true, "were": true, "been": true, "be": true,
	"being": true, "am": true,

	// Have verbs
	"have": true, "has": true, "had": true, "having": true,

	// Do verbs
	"do": true, "does": true, "did": true, "doing": true, "done": true,

	// Modal verbs
	"will": true, "would": true, "could": true, "should": true,
	"may": true, "might": true, "must": true, "shall": true, "can": true,

	// Pronouns
	"it": true, "its": true, "itself": true,
	"they": true, "their": true, "theirs": true, "them": true, "themselves": true,
	"we": true, "our": true, "ours": true, "us": true, "ourselves": true,
	"you": true, "your": true, "yours": true, "yourself": true, "yourselves": true,
	"he": true, "she": true, "him": true, "her": true, "his": true, "hers": true,
	"himself": true, "herself": true,
	"i": true, "me": true, "my": true, "mine": true, "myself": true,
	"who": true, "whom": true, "whose": true, "which": true, "what": true,

	// Negation
	"not": true, "no": true, "none": true, "never": true, "neither": true,

	// Quantifiers
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "any": true,
	"many": true, "much": true, "several": true, "enough": true, "less": true,
	"least": true, "little": true,

	// Adverbs
	"such": true, "than": true, "too": true, "very": true, "just": true,
	"also": true, "now": true, "only": true, "here": true, "there": true,
	"where": true, "when": true, "why": true, "how": true,
	"again": true, "further": true, "once": true, "already": true, "always": true,
	"ever": true, "still": true, "even": true, "well": true, "back": true,
	"else": true, "however": true, "therefore": true, "thus": true, "hence": true,
	"otherwise": true, "rather": true, "quite": true, "perhaps": true, "maybe": true,

	// Other common words
	"own": true, "same": true, "either": true, "another": true, "anything": true,
	"everything": true, "nothing": true, "something": true, "someone": true,
	"anyone": true, "everyone": true, "nobody": true, "somebody": true, "anybody": true,
	"one": true, "two": true, "first": true, "last": true, "next": true,
	"new": true, "old": true, "high": true, "long": true,
}

// IsStopWord checks if a word is a common English stop word.
func IsStopWord(word string) bool {
	return stopWords[word]
}
