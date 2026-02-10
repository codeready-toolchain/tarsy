package mcp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{name: "empty string", input: "", expected: 0},
		{name: "single char", input: "a", expected: 1},
		{name: "exactly 4 chars", input: "abcd", expected: 1},
		{name: "5 chars rounds up", input: "abcde", expected: 2},
		{name: "8 chars", input: "abcdefgh", expected: 2},
		{name: "typical sentence", input: "Hello world, this is a test.", expected: 7},
		{name: "unicode text", input: "こんにちは世界", expected: 6}, // 21 bytes in UTF-8
		{name: "long text 1000 chars", input: strings.Repeat("a", 1000), expected: 250},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EstimateTokens(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateAtLineBoundary(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		maxChars int
		marker   string
		expected string
	}{
		{
			name:     "below limit returns unchanged",
			content:  "short text",
			maxChars: 100,
			marker:   "test",
			expected: "short text",
		},
		{
			name:     "at exact limit returns unchanged",
			content:  "abcde",
			maxChars: 5,
			marker:   "test",
			expected: "abcde",
		},
		{
			name:     "zero limit returns unchanged",
			content:  "some text",
			maxChars: 0,
			marker:   "test",
			expected: "some text",
		},
		{
			name:     "negative limit returns unchanged",
			content:  "some text",
			maxChars: -5,
			marker:   "test",
			expected: "some text",
		},
		{
			name:     "cuts at newline boundary",
			content:  "line1\nline2\nline3\nline4",
			maxChars: 15,
			marker:   "test marker",
			expected: "line1\nline2\n\n[TRUNCATED: test marker — Original size: 0KB, limit: 0KB]",
		},
		{
			name:     "hard cuts without newlines",
			content:  "abcdefghijklmnopqrstuvwxyz",
			maxChars: 10,
			marker:   "hard cut",
			expected: "abcdefghij\n\n[TRUNCATED: hard cut — Original size: 0KB, limit: 0KB]",
		},
		{
			name:     "cuts back to last complete line",
			content:  "line1\nline2\nline3\nline4\nline5",
			maxChars: 14, // lands in the middle of "line3"
			marker:   "test",
			expected: "line1\nline2\n\n[TRUNCATED: test — Original size: 0KB, limit: 0KB]",
		},
		{
			name: "preserves complete lines in indented JSON",
			content: `{
  "name": "test",
  "value": 123,
  "nested": {
    "key": "data"
  }
}`,
			maxChars: 40, // lands in the middle of "nested" line
			marker:   "JSON content",
			expected: "{\n  \"name\": \"test\",\n  \"value\": 123," +
				"\n\n[TRUNCATED: JSON content — Original size: 0KB, limit: 0KB]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateAtLineBoundary(tt.content, tt.maxChars, tt.marker)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestTruncateForStorage(t *testing.T) {
	t.Run("small content unchanged", func(t *testing.T) {
		assert.Equal(t, "small result", TruncateForStorage("small result"))
	})

	t.Run("large content truncated at correct limit", func(t *testing.T) {
		maxChars := DefaultStorageMaxTokens * charsPerToken // 32000
		large := strings.Repeat("x", maxChars+1000)
		want := strings.Repeat("x", maxChars) +
			fmt.Sprintf("\n\n[TRUNCATED: Output exceeded storage display limit — Original size: %dKB, limit: %dKB]",
				len(large)/1024, maxChars/1024)
		assert.Equal(t, want, TruncateForStorage(large))
	})
}

func TestTruncateForSummarization(t *testing.T) {
	t.Run("small content unchanged", func(t *testing.T) {
		assert.Equal(t, "small result", TruncateForSummarization("small result"))
	})

	t.Run("large content truncated at correct limit", func(t *testing.T) {
		maxChars := DefaultSummarizationMaxTokens * charsPerToken // 400000
		large := strings.Repeat("x", maxChars+1000)
		want := strings.Repeat("x", maxChars) +
			fmt.Sprintf("\n\n[TRUNCATED: Output exceeded summarization input limit — Original size: %dKB, limit: %dKB]",
				len(large)/1024, maxChars/1024)
		assert.Equal(t, want, TruncateForSummarization(large))
	})
}
