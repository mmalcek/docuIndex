package docuindex

import (
	"strings"
	"unicode"
)

// Token estimation constants
// These are approximations based on typical tokenizer behavior (cl100k_base/GPT-4/Claude)
const (
	// Average characters per token for English text
	avgCharsPerToken = 4.0

	// Adjustment factors for different content types
	codeAdjustment     = 0.8 // Code tends to have more tokens per char
	numbersAdjustment  = 0.5 // Numbers tokenize more efficiently
	whitespaceAdjust   = 0.0 // Whitespace is often merged
)

// EstimateTokens estimates the token count for a given text string.
// This uses an approximation based on cl100k_base tokenizer behavior.
// For English text, it averages ~4 characters per token.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}

	// Count different character types for more accurate estimation
	var letters, digits, spaces, punctuation, other int
	for _, r := range text {
		switch {
		case unicode.IsLetter(r):
			letters++
		case unicode.IsDigit(r):
			digits++
		case unicode.IsSpace(r):
			spaces++
		case unicode.IsPunct(r):
			punctuation++
		default:
			other++
		}
	}

	total := len(text)

	// Base estimation: total chars / avg chars per token
	baseTokens := float64(total) / avgCharsPerToken

	// Adjust for content composition
	// Spaces contribute less (often merged with adjacent tokens)
	spaceAdjust := float64(spaces) * 0.5 / avgCharsPerToken

	// Punctuation often becomes separate tokens
	punctAdjust := float64(punctuation) * 0.3

	// Numbers tokenize more efficiently
	digitAdjust := float64(digits) * -0.2 / avgCharsPerToken

	estimated := baseTokens - spaceAdjust + punctAdjust + digitAdjust

	// Ensure minimum of 1 token for non-empty text
	if estimated < 1 {
		return 1
	}

	return int(estimated + 0.5) // Round to nearest
}

// EstimateBlockTokens estimates tokens for a content block
func EstimateBlockTokens(block *ContentBlock) int {
	if block == nil {
		return 0
	}

	tokens := EstimateTokens(block.Content)

	// Add tokens for semantic context if present
	if block.Semantic.Section != "" {
		tokens += EstimateTokens(block.Semantic.Section)
	}

	return tokens
}

// EstimateResultTokens estimates total tokens for search results
func EstimateResultTokens(results []SearchResult) int {
	total := 0
	for _, r := range results {
		total += EstimateTokens(r.Content)
		// Include snippet if different from content
		if r.Snippet != "" && r.Snippet != r.Content {
			// Snippet usually overlaps with content, add just a portion
			total += EstimateTokens(r.Snippet) / 3
		}
	}
	return total
}

// EstimateAgentResultTokens estimates total tokens for agent search results
func EstimateAgentResultTokens(results []AgentSearchResult) int {
	total := 0
	for _, r := range results {
		total += r.TokenCount
	}
	return total
}

// EstimateContextTokens estimates tokens for a context window
func EstimateContextTokens(ctx *ContextResult) int {
	if ctx == nil {
		return 0
	}

	total := EstimateBlockTokens(&ctx.Center)

	for i := range ctx.Before {
		total += EstimateBlockTokens(&ctx.Before[i])
	}

	for i := range ctx.After {
		total += EstimateBlockTokens(&ctx.After[i])
	}

	return total
}

// TruncateToTokenLimit truncates text to approximately fit within a token limit
func TruncateToTokenLimit(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return text
	}

	estimated := EstimateTokens(text)
	if estimated <= maxTokens {
		return text
	}

	// Calculate approximate character limit
	ratio := float64(maxTokens) / float64(estimated)
	targetLen := int(float64(len(text)) * ratio * 0.95) // 5% safety margin

	if targetLen >= len(text) {
		return text
	}

	// Try to break at word boundary
	truncated := text[:targetLen]
	lastSpace := strings.LastIndexFunc(truncated, unicode.IsSpace)
	if lastSpace > targetLen/2 {
		truncated = truncated[:lastSpace]
	}

	return strings.TrimSpace(truncated) + "..."
}

// FitsInContext checks if content fits within a token budget
func FitsInContext(text string, maxTokens int) bool {
	return EstimateTokens(text) <= maxTokens
}

// TokenBudget helps track token usage across multiple operations
type TokenBudget struct {
	MaxTokens  int
	UsedTokens int
}

// NewTokenBudget creates a new token budget tracker
func NewTokenBudget(maxTokens int) *TokenBudget {
	return &TokenBudget{
		MaxTokens:  maxTokens,
		UsedTokens: 0,
	}
}

// Add adds tokens to the budget, returns true if within budget
func (b *TokenBudget) Add(tokens int) bool {
	b.UsedTokens += tokens
	return b.UsedTokens <= b.MaxTokens
}

// AddText estimates and adds tokens for text, returns true if within budget
func (b *TokenBudget) AddText(text string) bool {
	return b.Add(EstimateTokens(text))
}

// Remaining returns remaining token budget
func (b *TokenBudget) Remaining() int {
	remaining := b.MaxTokens - b.UsedTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsExhausted returns true if budget is exhausted
func (b *TokenBudget) IsExhausted() bool {
	return b.UsedTokens >= b.MaxTokens
}

// Reset resets the budget to zero usage
func (b *TokenBudget) Reset() {
	b.UsedTokens = 0
}

// Usage returns the current usage percentage (0-100)
func (b *TokenBudget) Usage() float64 {
	if b.MaxTokens == 0 {
		return 100
	}
	return float64(b.UsedTokens) / float64(b.MaxTokens) * 100
}
