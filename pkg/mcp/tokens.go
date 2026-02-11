package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// charsPerToken is the approximate number of characters per token for English text.
// Used for threshold estimation only — not exact token counting.
const charsPerToken = 4

// DefaultStorageMaxTokens is the maximum token count for storage-truncated tool output.
// Protects the dashboard from rendering massive text blobs.
const DefaultStorageMaxTokens = 8000

// DefaultSummarizationMaxTokens is the maximum token count for summarization LLM input.
// Safety net — summarization prompt + truncated output must fit in the model's context window.
const DefaultSummarizationMaxTokens = 100000

// EstimateTokens returns an approximate token count for the given text.
// Uses the common heuristic of ~4 characters per token for English text.
// This is intentionally approximate — exact counts would require a tokenizer
// library and add a dependency for minimal benefit (the threshold is a
// configurable soft limit, not a hard boundary).
//
// Note: len(text) counts bytes, not Unicode characters. For multi-byte UTF-8
// content (CJK, emoji), this overestimates the character count and therefore
// the token count. This is a safe direction to err — summarization triggers
// slightly earlier than necessary, which is preferable to missing it.
func EstimateTokens(text string) int {
	if len(text) == 0 {
		return 0
	}
	return (len(text) + charsPerToken - 1) / charsPerToken // Round up
}

// truncateAtLineBoundary is the shared truncation logic. It cuts at the last newline
// before the limit to avoid splitting mid-line — important when the content is
// indented JSON, YAML, or log output (preserves logical line boundaries).
//
// Note: maxChars is a byte limit (consistent with EstimateTokens using len()).
// The cut point is adjusted backwards to avoid splitting multi-byte UTF-8
// characters, then further adjusted to the last newline when possible.
func truncateAtLineBoundary(content string, maxChars int, marker string) string {
	if maxChars <= 0 || len(content) <= maxChars {
		return content
	}
	// Ensure we don't split a multi-byte UTF-8 character
	cut := maxChars
	for cut > 0 && !utf8.RuneStart(content[cut]) {
		cut--
	}
	truncated := content[:cut]
	if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
		truncated = truncated[:idx]
	}
	return truncated + fmt.Sprintf(
		"\n\n[TRUNCATED: %s — Original size: %s, limit: %s]",
		marker, formatSize(len(content)), formatSize(maxChars),
	)
}

// formatSize returns a human-readable size string. Uses bytes for values under
// 1KB to avoid confusing "0KB" output on small content.
func formatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	return fmt.Sprintf("%dKB", bytes/1024)
}

// TruncateForStorage truncates tool output for llm_tool_call completion content
// and MCPInteraction records. Protects the UI from rendering massive text blobs.
// Applied to ALL raw results, regardless of whether summarization is triggered.
func TruncateForStorage(content string) string {
	return truncateAtLineBoundary(content, DefaultStorageMaxTokens*charsPerToken,
		"Output exceeded storage display limit")
}

// TruncateForSummarization truncates tool output before sending to the summarization LLM.
// Safety net — summarization prompt + truncated output must fit in the model's context window.
// Uses a larger limit than storage truncation to give the summarizer maximum data.
func TruncateForSummarization(content string) string {
	return truncateAtLineBoundary(content, DefaultSummarizationMaxTokens*charsPerToken,
		"Output exceeded summarization input limit")
}
