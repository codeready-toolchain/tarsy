package slack

import (
	"strings"
	"testing"
	"unicode/utf8"

	goslack "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStartedMessage(t *testing.T) {
	blocks := BuildStartedMessage("session-123", "https://tarsy.example.com")

	require.Len(t, blocks, 1)

	section, ok := blocks[0].(*goslack.SectionBlock)
	require.True(t, ok)
	assert.Contains(t, section.Text.Text, ":arrows_counterclockwise:")
	assert.Contains(t, section.Text.Text, "Processing started")
	assert.Contains(t, section.Text.Text, "https://tarsy.example.com/sessions/session-123")
}

func TestBuildTerminalMessage_Completed(t *testing.T) {
	input := SessionCompletedInput{
		SessionID:        "sess-1",
		Status:           "completed",
		ExecutiveSummary: "The pod crashed due to OOM.",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	require.GreaterOrEqual(t, len(blocks), 3)

	header := blocks[0].(*goslack.SectionBlock)
	assert.Contains(t, header.Text.Text, ":white_check_mark:")
	assert.Contains(t, header.Text.Text, "Analysis Complete")

	content := blocks[1].(*goslack.SectionBlock)
	assert.Contains(t, content.Text.Text, "The pod crashed due to OOM.")

	action := blocks[2].(*goslack.ActionBlock)
	require.Len(t, action.Elements.ElementSet, 1)
	btn, ok := action.Elements.ElementSet[0].(*goslack.ButtonBlockElement)
	require.True(t, ok)
	assert.Equal(t, "View Full Analysis", btn.Text.Text)
	assert.Contains(t, btn.URL, "https://dash.example.com/sessions/sess-1")
}

func TestBuildTerminalMessage_CompletedFallbackToFinalAnalysis(t *testing.T) {
	input := SessionCompletedInput{
		SessionID:     "sess-2",
		Status:        "completed",
		FinalAnalysis: "Fallback analysis content.",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	require.GreaterOrEqual(t, len(blocks), 3)
	content := blocks[1].(*goslack.SectionBlock)
	assert.Contains(t, content.Text.Text, "Fallback analysis content.")
}

func TestBuildTerminalMessage_CompletedNoContent(t *testing.T) {
	input := SessionCompletedInput{
		SessionID: "sess-3",
		Status:    "completed",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	require.Len(t, blocks, 2)
	header := blocks[0].(*goslack.SectionBlock)
	assert.Contains(t, header.Text.Text, "Analysis Complete")
}

func TestBuildTerminalMessage_Failed(t *testing.T) {
	input := SessionCompletedInput{
		SessionID:    "sess-4",
		Status:       "failed",
		ErrorMessage: "timeout waiting for LLM",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	require.GreaterOrEqual(t, len(blocks), 2)

	header := blocks[0].(*goslack.SectionBlock)
	assert.Contains(t, header.Text.Text, ":x:")
	assert.Contains(t, header.Text.Text, "Analysis Failed")
	assert.Contains(t, header.Text.Text, "timeout waiting for LLM")

	action := blocks[1].(*goslack.ActionBlock)
	btn := action.Elements.ElementSet[0].(*goslack.ButtonBlockElement)
	assert.Equal(t, "View Details", btn.Text.Text)
}

func TestBuildTerminalMessage_TimedOut(t *testing.T) {
	input := SessionCompletedInput{
		SessionID: "sess-5",
		Status:    "timed_out",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	header := blocks[0].(*goslack.SectionBlock)
	assert.Contains(t, header.Text.Text, ":hourglass:")
	assert.Contains(t, header.Text.Text, "Analysis Timed Out")
}

func TestBuildTerminalMessage_Cancelled(t *testing.T) {
	input := SessionCompletedInput{
		SessionID: "sess-6",
		Status:    "cancelled",
	}
	blocks := BuildTerminalMessage(input, "https://dash.example.com")

	header := blocks[0].(*goslack.SectionBlock)
	assert.Contains(t, header.Text.Text, ":no_entry_sign:")
	assert.Contains(t, header.Text.Text, "Analysis Cancelled")
}

func TestTruncateForSlack(t *testing.T) {
	t.Run("short text unchanged", func(t *testing.T) {
		assert.Equal(t, "hello", truncateForSlack("hello"))
	})

	t.Run("exact limit unchanged", func(t *testing.T) {
		text := strings.Repeat("a", maxBlockTextLength)
		assert.Equal(t, text, truncateForSlack(text))
	})

	t.Run("over limit truncated", func(t *testing.T) {
		text := strings.Repeat("a", maxBlockTextLength+100)
		result := truncateForSlack(text)
		assert.True(t, len(result) < len(text))
		assert.Contains(t, result, "truncated")
	})

	t.Run("multi-byte runes not split", func(t *testing.T) {
		text := strings.Repeat("🔥", maxBlockTextLength+10)
		result := truncateForSlack(text)
		assert.Contains(t, result, "truncated")
		// Verify it's valid UTF-8 by ensuring no broken runes.
		assert.True(t, utf8.ValidString(result), "result should be valid UTF-8")
		// Should contain exactly maxBlockTextLength emoji runes before the suffix.
		prefix := strings.Split(result, "\n\n_...")[0]
		assert.Equal(t, maxBlockTextLength, utf8.RuneCountInString(prefix))
	})
}
