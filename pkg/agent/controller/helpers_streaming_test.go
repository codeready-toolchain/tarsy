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

func TestCollectStreamWithCallback_EmptyStream(t *testing.T) {
	ch := make(chan agent.Chunk)
	close(ch) // Immediately closed — no chunks

	resp, err := collectStreamWithCallback(ch, nil)
	require.NoError(t, err)
	assert.Equal(t, "", resp.Text)
	assert.Equal(t, "", resp.ThinkingText)
	assert.Nil(t, resp.ToolCalls)
	assert.Nil(t, resp.Usage)
	assert.Nil(t, resp.Groundings)
	assert.Nil(t, resp.CodeExecutions)
}

func TestCollectStreamWithCallback_GroundingChunks(t *testing.T) {
	ch := make(chan agent.Chunk, 2)
	ch <- &agent.GroundingChunk{
		Sources: []agent.GroundingSource{
			{URI: "https://example.com", Title: "Example"},
		},
		WebSearchQueries: []string{"test query"},
	}
	ch <- &agent.TextChunk{Content: "Based on search results..."}
	close(ch)

	resp, err := collectStreamWithCallback(ch, nil)
	require.NoError(t, err)
	assert.Equal(t, "Based on search results...", resp.Text)
	require.Len(t, resp.Groundings, 1)
	assert.Equal(t, "https://example.com", resp.Groundings[0].Sources[0].URI)
	assert.Equal(t, []string{"test query"}, resp.Groundings[0].WebSearchQueries)
}

func TestCollectStreamWithCallback_CodeExecutionChunks(t *testing.T) {
	ch := make(chan agent.Chunk, 3)
	ch <- &agent.CodeExecutionChunk{Code: "print('hello')", Result: ""}
	ch <- &agent.CodeExecutionChunk{Code: "", Result: "hello"}
	ch <- &agent.TextChunk{Content: "Executed successfully."}
	close(ch)

	resp, err := collectStreamWithCallback(ch, nil)
	require.NoError(t, err)
	assert.Equal(t, "Executed successfully.", resp.Text)
	require.Len(t, resp.CodeExecutions, 2)
	assert.Equal(t, "print('hello')", resp.CodeExecutions[0].Code)
	assert.Equal(t, "hello", resp.CodeExecutions[1].Result)
}

func TestCollectStreamWithCallback_AllChunkTypes(t *testing.T) {
	// Comprehensive test: all chunk types in one stream
	var callbacks []string

	callback := func(chunkType string, _ string) {
		callbacks = append(callbacks, chunkType)
	}

	ch := make(chan agent.Chunk, 10)
	ch <- &agent.ThinkingChunk{Content: "Hmm..."}
	ch <- &agent.TextChunk{Content: "Answer: "}
	ch <- &agent.TextChunk{Content: "42"}
	ch <- &agent.ToolCallChunk{CallID: "tc-1", Name: "get_info", Arguments: "{}"}
	ch <- &agent.CodeExecutionChunk{Code: "x = 1", Result: "1"}
	ch <- &agent.GroundingChunk{
		Sources: []agent.GroundingSource{{URI: "http://example.com"}},
	}
	ch <- &agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150, ThinkingTokens: 20}
	close(ch)

	resp, err := collectStreamWithCallback(ch, callback)
	require.NoError(t, err)
	assert.Equal(t, "Answer: 42", resp.Text)
	assert.Equal(t, "Hmm...", resp.ThinkingText)
	require.Len(t, resp.ToolCalls, 1)
	require.Len(t, resp.CodeExecutions, 1)
	require.Len(t, resp.Groundings, 1)
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 150, resp.Usage.TotalTokens)
	assert.Equal(t, 20, resp.Usage.ThinkingTokens)

	// Callback should fire for thinking (1) + text (2) = 3 times
	// (Tool calls, code executions, groundings, usage don't trigger callback)
	assert.Equal(t, []string{ChunkTypeThinking, ChunkTypeText, ChunkTypeText}, callbacks)
}
