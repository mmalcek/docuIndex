package docuindex

import (
	"strings"
	"unicode"
)

// ChunkContent splits content into LLM-friendly chunks based on the provided options
func ChunkContent(content string, opts ChunkOptions) []Chunk {
	if content == "" {
		return nil
	}

	// Apply defaults
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 512
	}
	if opts.ChunkBy == "" {
		opts.ChunkBy = "paragraph"
	}

	var chunks []Chunk

	switch opts.ChunkBy {
	case "sentence":
		chunks = chunkBySentence(content, opts)
	case "tokens":
		chunks = chunkByTokens(content, opts)
	default: // "paragraph"
		chunks = chunkByParagraph(content, opts)
	}

	return chunks
}

// chunkByParagraph splits content by paragraphs, respecting token limits
func chunkByParagraph(content string, opts ChunkOptions) []Chunk {
	paragraphs := splitByParagraph(content)
	return mergeUnitsIntoChunks(paragraphs, content, opts)
}

// chunkBySentence splits content by sentences, respecting token limits
func chunkBySentence(content string, opts ChunkOptions) []Chunk {
	sentences := splitBySentence(content)
	return mergeUnitsIntoChunks(sentences, content, opts)
}

// chunkByTokens splits content purely by token count
func chunkByTokens(content string, opts ChunkOptions) []Chunk {
	var chunks []Chunk

	// Approximate chars per chunk
	charsPerToken := 4.0
	charsPerChunk := int(float64(opts.MaxTokens) * charsPerToken)
	overlapChars := int(float64(opts.OverlapTokens) * charsPerToken)

	idx := 0
	for idx < len(content) {
		endIdx := idx + charsPerChunk
		if endIdx > len(content) {
			endIdx = len(content)
		}

		// Try to break at word boundary
		if endIdx < len(content) {
			// Look backward for a space
			for i := endIdx; i > idx+charsPerChunk/2; i-- {
				if unicode.IsSpace(rune(content[i])) {
					endIdx = i
					break
				}
			}
		}

		chunkContent := strings.TrimSpace(content[idx:endIdx])
		if chunkContent != "" {
			chunks = append(chunks, Chunk{
				Content:    chunkContent,
				StartIdx:   idx,
				EndIdx:     endIdx,
				TokenCount: EstimateTokens(chunkContent),
			})
		}

		// Move forward, accounting for overlap
		idx = endIdx - overlapChars
		if idx < 0 || idx >= endIdx {
			idx = endIdx
		}
	}

	return chunks
}

// mergeUnitsIntoChunks merges text units (paragraphs/sentences) into chunks respecting token limits
func mergeUnitsIntoChunks(units []string, originalContent string, opts ChunkOptions) []Chunk {
	var chunks []Chunk
	var currentContent strings.Builder
	currentStartIdx := 0
	searchIdx := 0

	for i, unit := range units {
		unitTokens := EstimateTokens(unit)
		currentTokens := EstimateTokens(currentContent.String())

		// If adding this unit would exceed limit, flush current chunk
		if currentContent.Len() > 0 && currentTokens+unitTokens > opts.MaxTokens {
			chunkText := strings.TrimSpace(currentContent.String())
			if chunkText != "" {
				endIdx := searchIdx
				chunks = append(chunks, Chunk{
					Content:    chunkText,
					StartIdx:   currentStartIdx,
					EndIdx:     endIdx,
					TokenCount: EstimateTokens(chunkText),
				})
			}

			// Start new chunk, potentially with overlap from previous units
			currentContent.Reset()
			if opts.OverlapTokens > 0 && len(chunks) > 0 {
				// Add overlap from end of previous chunk
				overlap := getOverlapText(chunks[len(chunks)-1].Content, opts.OverlapTokens)
				if overlap != "" {
					currentContent.WriteString(overlap)
					currentContent.WriteString(" ")
				}
			}
			currentStartIdx = searchIdx
		}

		// Find position of this unit in original content
		unitIdx := strings.Index(originalContent[searchIdx:], unit)
		if unitIdx >= 0 {
			if currentContent.Len() == 0 {
				currentStartIdx = searchIdx + unitIdx
			}
			searchIdx = searchIdx + unitIdx + len(unit)
		}

		// Add unit to current chunk
		if currentContent.Len() > 0 {
			currentContent.WriteString(" ")
		}
		currentContent.WriteString(unit)

		// Handle last unit
		if i == len(units)-1 {
			chunkText := strings.TrimSpace(currentContent.String())
			if chunkText != "" {
				chunks = append(chunks, Chunk{
					Content:    chunkText,
					StartIdx:   currentStartIdx,
					EndIdx:     searchIdx,
					TokenCount: EstimateTokens(chunkText),
				})
			}
		}
	}

	return chunks
}

// getOverlapText returns the last N tokens worth of text
func getOverlapText(text string, overlapTokens int) string {
	if overlapTokens <= 0 {
		return ""
	}

	// Approximate character count for overlap
	charsNeeded := overlapTokens * 4

	if len(text) <= charsNeeded {
		return text
	}

	// Get last portion
	overlap := text[len(text)-charsNeeded:]

	// Try to start at word boundary
	spaceIdx := strings.IndexFunc(overlap, unicode.IsSpace)
	if spaceIdx > 0 && spaceIdx < len(overlap)/2 {
		overlap = strings.TrimSpace(overlap[spaceIdx:])
	}

	return overlap
}

// splitByParagraph splits text into paragraphs
func splitByParagraph(content string) []string {
	// Split on double newlines or single newlines
	paragraphs := strings.Split(content, "\n\n")

	var result []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			// Further split on single newlines if paragraphs are too long
			if EstimateTokens(p) > 200 {
				subParagraphs := strings.Split(p, "\n")
				for _, sp := range subParagraphs {
					sp = strings.TrimSpace(sp)
					if sp != "" {
						result = append(result, sp)
					}
				}
			} else {
				result = append(result, p)
			}
		}
	}

	return result
}

// splitBySentence splits text into sentences
func splitBySentence(content string) []string {
	var sentences []string
	var current strings.Builder

	runes := []rune(content)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		current.WriteRune(r)

		// Check for sentence endings
		if r == '.' || r == '!' || r == '?' {
			// Look ahead to verify it's a sentence end
			isEnd := true
			if i+1 < len(runes) {
				next := runes[i+1]
				// Not a sentence end if followed by a letter (abbreviation)
				if unicode.IsLetter(next) {
					isEnd = false
				}
				// Check for common abbreviations
				if r == '.' {
					text := current.String()
					if endsWithAbbreviation(text) {
						isEnd = false
					}
				}
			}

			if isEnd {
				sentence := strings.TrimSpace(current.String())
				if sentence != "" {
					sentences = append(sentences, sentence)
				}
				current.Reset()
			}
		}
	}

	// Add remaining text
	remaining := strings.TrimSpace(current.String())
	if remaining != "" {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// endsWithAbbreviation checks if text ends with a common abbreviation
func endsWithAbbreviation(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	abbreviations := []string{
		"mr.", "mrs.", "ms.", "dr.", "prof.",
		"sr.", "jr.", "vs.", "etc.", "e.g.",
		"i.e.", "inc.", "ltd.", "co.", "corp.",
		"st.", "ave.", "blvd.", "rd.", "fig.",
		"no.", "vol.", "pg.", "pp.", "dept.",
	}
	for _, abbr := range abbreviations {
		if strings.HasSuffix(text, abbr) {
			return true
		}
	}
	return false
}

// ChunkBlocks regroups content blocks based on token limits
func ChunkBlocks(blocks []ContentBlock, opts ChunkOptions) [][]ContentBlock {
	if len(blocks) == 0 {
		return nil
	}

	// Apply defaults
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 512
	}

	var result [][]ContentBlock
	var currentGroup []ContentBlock
	currentTokens := 0

	for _, block := range blocks {
		blockTokens := EstimateBlockTokens(&block)

		// If this single block exceeds the limit, it gets its own group
		if blockTokens > opts.MaxTokens {
			// Flush current group first
			if len(currentGroup) > 0 {
				result = append(result, currentGroup)
				currentGroup = nil
				currentTokens = 0
			}
			result = append(result, []ContentBlock{block})
			continue
		}

		// If adding this block would exceed limit, flush current group
		if currentTokens+blockTokens > opts.MaxTokens && len(currentGroup) > 0 {
			result = append(result, currentGroup)
			currentGroup = nil
			currentTokens = 0
		}

		currentGroup = append(currentGroup, block)
		currentTokens += blockTokens
	}

	// Add remaining group
	if len(currentGroup) > 0 {
		result = append(result, currentGroup)
	}

	return result
}

// CombineChunkedBlocks combines a group of blocks into a single string
func CombineChunkedBlocks(blocks []ContentBlock, separator string) string {
	if len(blocks) == 0 {
		return ""
	}

	if separator == "" {
		separator = "\n\n"
	}

	var parts []string
	for _, block := range blocks {
		content := strings.TrimSpace(block.Content)
		if content != "" {
			parts = append(parts, content)
		}
	}

	return strings.Join(parts, separator)
}

// ChunkSearchResults chunks search results to fit within a token budget
func ChunkSearchResults(results []SearchResult, maxTokens int) [][]SearchResult {
	if len(results) == 0 {
		return nil
	}

	var chunked [][]SearchResult
	var currentGroup []SearchResult
	currentTokens := 0

	for _, result := range results {
		resultTokens := EstimateTokens(result.Content)

		// If adding this result would exceed limit, flush current group
		if currentTokens+resultTokens > maxTokens && len(currentGroup) > 0 {
			chunked = append(chunked, currentGroup)
			currentGroup = nil
			currentTokens = 0
		}

		currentGroup = append(currentGroup, result)
		currentTokens += resultTokens
	}

	// Add remaining group
	if len(currentGroup) > 0 {
		chunked = append(chunked, currentGroup)
	}

	return chunked
}
