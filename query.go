package docuindex

import (
	"regexp"
	"strings"
)

// DetectQueryType analyzes a search query and returns its detected intent
func DetectQueryType(query string) QueryType {
	q := strings.ToLower(strings.TrimSpace(query))

	// Check patterns in order of specificity
	if detectComparison(q) {
		return QueryTypeComparison
	}
	if detectSummary(q) {
		return QueryTypeSummary
	}
	if detectList(q) {
		return QueryTypeList
	}
	if detectDefinition(q) {
		return QueryTypeDefinition
	}
	if detectNavigation(q) {
		return QueryTypeNavigation
	}
	if detectFactual(q) {
		return QueryTypeFactual
	}

	return QueryTypeUnknown
}

// detectFactual checks for factual questions (What is X?, Who is Y?, When did Z?)
func detectFactual(query string) bool {
	factualPatterns := []string{
		`^what\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had)\b`,
		`^who\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had)\b`,
		`^when\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had)\b`,
		`^where\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had)\b`,
		`^why\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had)\b`,
		`^how\s+(is|are|was|were|does|do|did|can|could|would|should|will|has|have|had|much|many|long|often)\b`,
		`^is\s+there\b`,
		`^are\s+there\b`,
		`^does\b`,
		`^do\b`,
		`^can\b`,
		`^could\b`,
		`^would\b`,
		`^should\b`,
	}

	for _, pattern := range factualPatterns {
		if matched, _ := regexp.MatchString(pattern, query); matched {
			return true
		}
	}

	// Question words without specific patterns
	if strings.HasSuffix(query, "?") {
		return true
	}

	return false
}

// detectNavigation checks for navigation queries (Show me, Find, Navigate to)
func detectNavigation(query string) bool {
	navigationPrefixes := []string{
		"show me",
		"show the",
		"find the",
		"find me",
		"go to",
		"navigate to",
		"take me to",
		"where is the",
		"where can i find",
		"look up",
		"search for",
		"locate",
		"get the",
		"open the",
		"display the",
	}

	for _, prefix := range navigationPrefixes {
		if strings.HasPrefix(query, prefix) {
			return true
		}
	}

	return false
}

// detectSummary checks for summary requests
func detectSummary(query string) bool {
	summaryPatterns := []string{
		`^summarize\b`,
		`^summary\s+of\b`,
		`^give\s+(me\s+)?a\s+summary\b`,
		`^provide\s+(a\s+)?summary\b`,
		`^overview\s+of\b`,
		`^give\s+(me\s+)?an?\s+overview\b`,
		`^provide\s+(an?\s+)?overview\b`,
		`^brief\s+me\b`,
		`^tldr\b`,
		`^tl;dr\b`,
		`^in\s+short\b`,
		`^in\s+brief\b`,
		`^key\s+points\b`,
		`^main\s+points\b`,
		`^highlights?\s+of\b`,
		`^what\s+are\s+the\s+(main|key)\s+points\b`,
		`^condense\b`,
		`^recap\b`,
	}

	for _, pattern := range summaryPatterns {
		if matched, _ := regexp.MatchString(pattern, query); matched {
			return true
		}
	}

	// Check for summary keywords anywhere
	summaryKeywords := []string{"summarize", "summary", "overview", "tldr", "tl;dr", "recap"}
	for _, kw := range summaryKeywords {
		if strings.Contains(query, kw) {
			return true
		}
	}

	return false
}

// detectComparison checks for comparison queries
func detectComparison(query string) bool {
	comparisonPatterns := []string{
		`^compare\b`,
		`^comparison\s+(of|between)\b`,
		`^what\s+(is|are)\s+the\s+difference`,
		`^difference\s+between\b`,
		`^differences?\s+between\b`,
		`^how\s+(is|are|does|do)\s+.+\s+(differ|different|compare)`,
		`\bvs\.?\b`,
		`\bversus\b`,
		`\bcontrast\b`,
		`\bcompared\s+to\b`,
		`\bin\s+comparison\s+to\b`,
		`\bas\s+opposed\s+to\b`,
		`\bwhile\b.+\bdifference\b`,
	}

	for _, pattern := range comparisonPatterns {
		if matched, _ := regexp.MatchString(pattern, query); matched {
			return true
		}
	}

	return false
}

// detectDefinition checks for definition queries
func detectDefinition(query string) bool {
	definitionPatterns := []string{
		`^define\b`,
		`^definition\s+of\b`,
		`^what\s+is\s+the\s+definition\b`,
		`^what\s+does\s+.+\s+mean\b`,
		`^meaning\s+of\b`,
		`^what\s+is\s+meant\s+by\b`,
		`^explain\s+what\b`,
		`^explain\s+the\s+term\b`,
		`^what\s+is\s+a\b`,
		`^what\s+is\s+an\b`,
		`^what\s+are\b`,
	}

	for _, pattern := range definitionPatterns {
		if matched, _ := regexp.MatchString(pattern, query); matched {
			return true
		}
	}

	return false
}

// detectList checks for list/enumeration queries
func detectList(query string) bool {
	listPatterns := []string{
		`^list\b`,
		`^enumerate\b`,
		`^what\s+are\s+all\b`,
		`^what\s+are\s+the\b`,
		`^give\s+(me\s+)?a\s+list\b`,
		`^provide\s+(a\s+)?list\b`,
		`^name\s+all\b`,
		`^name\s+the\b`,
		`^show\s+(me\s+)?all\b`,
		`^all\s+the\b`,
		`^every\b`,
		`^how\s+many\b`,
		`^count\s+(all\s+)?the\b`,
		`^what\s+types\b`,
		`^what\s+kinds\b`,
		`^which\s+.+\s+are\s+there\b`,
	}

	for _, pattern := range listPatterns {
		if matched, _ := regexp.MatchString(pattern, query); matched {
			return true
		}
	}

	return false
}

// QueryTypeDescription returns a human-readable description of the query type
func QueryTypeDescription(qt QueryType) string {
	switch qt {
	case QueryTypeFactual:
		return "Factual question seeking specific information"
	case QueryTypeNavigation:
		return "Navigation request to locate content"
	case QueryTypeSummary:
		return "Request for a summary or overview"
	case QueryTypeComparison:
		return "Comparison between multiple items"
	case QueryTypeDefinition:
		return "Definition or meaning request"
	case QueryTypeList:
		return "Enumeration or listing request"
	case QueryTypeUnknown:
		return "Unknown query intent"
	default:
		return "Unrecognized query type"
	}
}

// SuggestedSearchMode returns the recommended search mode for a query type
func SuggestedSearchMode(qt QueryType) SearchMode {
	switch qt {
	case QueryTypeFactual, QueryTypeDefinition:
		// Factual queries benefit from semantic understanding
		return SearchModeHybrid
	case QueryTypeNavigation:
		// Navigation queries work well with keyword matching
		return SearchModeKeyword
	case QueryTypeSummary, QueryTypeComparison:
		// Summary and comparison benefit from semantic search
		return SearchModeSemantic
	case QueryTypeList:
		// Lists need comprehensive keyword coverage
		return SearchModeKeyword
	default:
		return SearchModeHybrid
	}
}
