package nlp

import "testing"

// TestStem verifies stemmer produces correct results for various suffix patterns
func TestStem(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Short words unchanged
		{"short word 2 chars", "go", "go"},
		{"short word 3 chars", "the", "the"},

		// Replacement suffixes (longest match wins)
		{"ization to ize", "colonization", "colonize"},
		{"ational to ate", "relational", "relate"},
		{"fulness to ful", "hopefulness", "hopeful"},
		{"ousness to ous", "outrageousness", "outrageous"},
		{"iveness to ive", "effectiveness", "effective"},
		{"tional to tion", "conditional", "condition"},
		{"ation to ate", "sensation", "sensate"},
		{"enci to ence", "dependenci", "dependence"},
		{"anci to ance", "toleranci", "tolerance"},
		{"izer to ize", "colonizer", "colonize"},
		{"ator to ate", "operator", "operate"},
		{"ies to i", "ponies", "poni"},
		{"ied to i", "cried", "cri"},

		// Suffix removal (no replacement)
		{"ing suffix removal", "running", "runn"},
		{"ly suffix removal", "quickly", "quick"},
		{"ness suffix removal", "happiness", "happi"},
		{"ment suffix removal", "development", "develop"},
		{"able suffix removal", "readable", "read"},
		{"ible suffix removal", "visible", "vis"},
		{"ed suffix removal", "walked", "walk"},
		{"es suffix removal", "boxes", "box"},
		{"s suffix removal", "cats", "cat"},
		{"er suffix removal", "worker", "work"},
		{"al suffix removal", "acional", "acion"},
		{"ic suffix removal", "electric", "electr"},

		// Minimum stem length (2 chars after suffix removal)
		{"min stem - keeps longer stem", "stated", "stat"},
		{"min stem - short with suffix", "abed", "ab"},

		// Edge cases
		{"empty string", "", ""},
		{"single char", "a", "a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Stem(tt.input)
			if result != tt.expected {
				t.Errorf("Stem(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestStemDeterminism verifies the stemmer produces consistent results
// This catches issues with non-deterministic map iteration
func TestStemDeterminism(t *testing.T) {
	testWords := []string{
		"internationalization",
		"organizational",
		"effectiveness",
		"colonization",
		"informational",
		"operational",
	}

	for _, word := range testWords {
		expected := Stem(word)
		// Run 100 times to detect any non-determinism
		for i := 0; i < 100; i++ {
			result := Stem(word)
			if result != expected {
				t.Fatalf("Non-deterministic result for %q on iteration %d: got %q, expected %q",
					word, i, result, expected)
			}
		}
	}
}

// TestStemLongestMatchFirst verifies longer suffixes take priority
func TestStemLongestMatchFirst(t *testing.T) {
	// "ization" (8 chars) should match before "ation" (5 chars)
	result := Stem("colonization")
	if result != "colonize" {
		t.Errorf("Expected 'colonize' (ization->ize), got %q", result)
	}

	// "ational" (7 chars) should match before "tional" (6 chars) or "ation" (5 chars)
	result = Stem("relational")
	if result != "relate" {
		t.Errorf("Expected 'relate' (ational->ate), got %q", result)
	}
}

// TestIsStopWord verifies stop word detection
func TestIsStopWord(t *testing.T) {
	tests := []struct {
		word     string
		expected bool
	}{
		// Common stop words (should be true)
		{"the", true},
		{"a", true},
		{"an", true},
		{"and", true},
		{"or", true},
		{"is", true},
		{"are", true},
		{"was", true},
		{"were", true},
		{"be", true},
		{"been", true},
		{"have", true},
		{"has", true},
		{"do", true},
		{"does", true},
		{"did", true},
		{"this", true},
		{"that", true},
		{"it", true},
		{"they", true},
		{"we", true},
		{"you", true},
		{"he", true},
		{"she", true},
		{"not", true},
		{"all", true},
		{"some", true},
		{"very", true},
		{"just", true},
		{"only", true},

		// Newly added stop words
		{"about", true},
		{"after", true},
		{"before", true},
		{"between", true},
		{"where", true},
		{"when", true},
		{"who", true},
		{"what", true},
		{"which", true},
		{"how", true},
		{"why", true},
		{"because", true},
		{"although", true},
		{"however", true},
		{"therefore", true},
		{"myself", true},
		{"yourself", true},
		{"himself", true},
		{"herself", true},
		{"themselves", true},
		{"anyone", true},
		{"everyone", true},
		{"something", true},
		{"nothing", true},

		// Content words (should be false)
		{"machine", false},
		{"learning", false},
		{"document", false},
		{"search", false},
		{"algorithm", false},
		{"database", false},
		{"index", false},
		{"vector", false},
		{"embedding", false},
		{"semantic", false},
	}

	for _, tt := range tests {
		t.Run(tt.word, func(t *testing.T) {
			result := IsStopWord(tt.word)
			if result != tt.expected {
				t.Errorf("IsStopWord(%q) = %v, want %v", tt.word, result, tt.expected)
			}
		})
	}
}

// TestIsStopWordCaseSensitive verifies stop words are lowercase only
func TestIsStopWordCaseSensitive(t *testing.T) {
	// Stop words map is lowercase - uppercase should not match
	if IsStopWord("THE") {
		t.Error("IsStopWord should be case-sensitive, 'THE' should return false")
	}
	if IsStopWord("The") {
		t.Error("IsStopWord should be case-sensitive, 'The' should return false")
	}
	// Lowercase should match
	if !IsStopWord("the") {
		t.Error("IsStopWord('the') should return true")
	}
}

// BenchmarkStem measures stemming performance
func BenchmarkStem(b *testing.B) {
	words := []string{
		"running", "colonization", "effectiveness", "machine", "learning",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, w := range words {
			Stem(w)
		}
	}
}

// BenchmarkIsStopWord measures stop word lookup performance
func BenchmarkIsStopWord(b *testing.B) {
	words := []string{"the", "machine", "is", "learning", "a"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, w := range words {
			IsStopWord(w)
		}
	}
}
