package controller

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectStreamWithCallback_NilCallback(t *testing.T) {
	// nil callback should behave like collectStream
	ch := make(chan agent.Chunk, 3)
	ch <- &agent.TextChunk{Content: "Hello "}
	ch <- &agent.TextChunk{Content: "world"}
	ch <- &agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	close(ch)

	resp, err := collectStreamWithCallback(ch, nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello world", resp.Text)
	assert.Equal(t, 15, resp.Usage.TotalTokens)
}

func TestCollectStreamWithCallback_TextCallback(t *testing.T) {
	var callbacks []struct {
		chunkType string
		content   string
	}

	callback := func(chunkType string, content string) {
		callbacks = append(callbacks, struct {
			chunkType string
			content   string
		}{chunkType, content})
	}

	ch := make(chan agent.Chunk, 3)
	ch <- &agent.TextChunk{Content: "Hello "}
	ch <- &agent.TextChunk{Content: "world"}
	ch <- &agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	close(ch)

	resp, err := collectStreamWithCallback(ch, callback)
	require.NoError(t, err)
	assert.Equal(t, "Hello world", resp.Text)

	// Should have 2 text callbacks with accumulated content
	require.Len(t, callbacks, 2)
	assert.Equal(t, ChunkTypeText, callbacks[0].chunkType)
	assert.Equal(t, "Hello ", callbacks[0].content) // First chunk
	assert.Equal(t, ChunkTypeText, callbacks[1].chunkType)
	assert.Equal(t, "Hello world", callbacks[1].content) // Accumulated
}

func TestCollectStreamWithCallback_ThinkingAndTextCallbacks(t *testing.T) {
	var callbacks []struct {
		chunkType string
		content   string
	}

	callback := func(chunkType string, content string) {
		callbacks = append(callbacks, struct {
			chunkType string
			content   string
		}{chunkType, content})
	}

	ch := make(chan agent.Chunk, 4)
	ch <- &agent.ThinkingChunk{Content: "Let me "}
	ch <- &agent.ThinkingChunk{Content: "think..."}
	ch <- &agent.TextChunk{Content: "The answer is 42."}
	close(ch)

	resp, err := collectStreamWithCallback(ch, callback)
	require.NoError(t, err)
	assert.Equal(t, "The answer is 42.", resp.Text)
	assert.Equal(t, "Let me think...", resp.ThinkingText)

	// 2 thinking callbacks + 1 text callback
	require.Len(t, callbacks, 3)
	assert.Equal(t, ChunkTypeThinking, callbacks[0].chunkType)
	assert.Equal(t, "Let me ", callbacks[0].content)
	assert.Equal(t, ChunkTypeThinking, callbacks[1].chunkType)
	assert.Equal(t, "Let me think...", callbacks[1].content) // Accumulated
	assert.Equal(t, ChunkTypeText, callbacks[2].chunkType)
	assert.Equal(t, "The answer is 42.", callbacks[2].content)
}

func TestCollectStreamWithCallback_ErrorChunk(t *testing.T) {
	ch := make(chan agent.Chunk, 3)
	ch <- &agent.TextChunk{Content: "partial "}
	ch <- &agent.ErrorChunk{Message: "rate limit exceeded", Code: "429", Retryable: true}
	close(ch)

	callbackCount := 0
	callback := func(chunkType string, content string) {
		callbackCount++
	}

	resp, err := collectStreamWithCallback(ch, callback)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rate limit exceeded")
	assert.Equal(t, 1, callbackCount) // Only the first text chunk callback fired
}

func TestCollectStreamWithCallback_ToolCalls(t *testing.T) {
	ch := make(chan agent.Chunk, 3)
	ch <- &agent.TextChunk{Content: "Let me check that."}
	ch <- &agent.ToolCallChunk{CallID: "tc-1", Name: "get_pods", Arguments: `{"namespace":"default"}`}
	close(ch)

	resp, err := collectStreamWithCallback(ch, nil)
	require.NoError(t, err)
	assert.Equal(t, "Let me check that.", resp.Text)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "get_pods", resp.ToolCalls[0].Name)
}
