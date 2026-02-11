package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// collectStream tests
// ============================================================================

func TestCollectStream(t *testing.T) {
	t.Run("text chunks concatenated", func(t *testing.T) {
		ch := make(chan agent.Chunk, 3)
		ch <- &agent.TextChunk{Content: "Hello "}
		ch <- &agent.TextChunk{Content: "world"}
		ch <- &agent.TextChunk{Content: "!"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Hello world!", resp.Text)
	})

	t.Run("thinking chunks concatenated", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.ThinkingChunk{Content: "Let me think "}
		ch <- &agent.ThinkingChunk{Content: "about this."}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Let me think about this.", resp.ThinkingText)
	})

	t.Run("tool call chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.ToolCallChunk{CallID: "c1", Name: "k8s.pods", Arguments: "{}"}
		ch <- &agent.ToolCallChunk{CallID: "c2", Name: "k8s.logs", Arguments: "{\"pod\": \"web\"}"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.ToolCalls, 2)
		assert.Equal(t, "c1", resp.ToolCalls[0].ID)
		assert.Equal(t, "k8s.pods", resp.ToolCalls[0].Name)
		assert.Equal(t, "c2", resp.ToolCalls[1].ID)
	})

	t.Run("usage chunk captured", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ThinkingTokens: 5}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.NotNil(t, resp.Usage)
		assert.Equal(t, 10, resp.Usage.InputTokens)
		assert.Equal(t, 20, resp.Usage.OutputTokens)
		assert.Equal(t, 30, resp.Usage.TotalTokens)
		assert.Equal(t, 5, resp.Usage.ThinkingTokens)
	})

	t.Run("code execution chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.CodeExecutionChunk{Code: "print('hi')", Result: "hi"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.CodeExecutions, 1)
		assert.Equal(t, "print('hi')", resp.CodeExecutions[0].Code)
		assert.Equal(t, "hi", resp.CodeExecutions[0].Result)
	})

	t.Run("grounding chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.GroundingChunk{
			WebSearchQueries: []string{"query1"},
			Sources: []agent.GroundingSource{
				{URI: "https://example.com", Title: "Example"},
			},
		}
		ch <- &agent.GroundingChunk{
			Sources: []agent.GroundingSource{
				{URI: "https://docs.k8s.io", Title: "K8s Docs"},
			},
		}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.Groundings, 2)
		assert.Equal(t, []string{"query1"}, resp.Groundings[0].WebSearchQueries)
		assert.Equal(t, "https://example.com", resp.Groundings[0].Sources[0].URI)
		assert.Empty(t, resp.Groundings[1].WebSearchQueries)
		assert.Equal(t, "https://docs.k8s.io", resp.Groundings[1].Sources[0].URI)
	})

	t.Run("empty stream has no groundings", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.TextChunk{Content: "hello"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Nil(t, resp.Groundings)
	})

	t.Run("error chunk returns error", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.TextChunk{Content: "partial"}
		ch <- &agent.ErrorChunk{Message: "rate limited", Code: "429", Retryable: true}
		close(ch)

		resp, err := collectStream(ch)
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "rate limited")
		assert.Contains(t, err.Error(), "429")
		assert.Contains(t, err.Error(), "retryable: true")
	})

	t.Run("empty stream returns empty response", func(t *testing.T) {
		ch := make(chan agent.Chunk)
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Empty(t, resp.Text)
		assert.Empty(t, resp.ThinkingText)
		assert.Empty(t, resp.ToolCalls)
		assert.Nil(t, resp.Usage)
	})

	t.Run("mixed chunks collected correctly", func(t *testing.T) {
		ch := make(chan agent.Chunk, 6)
		ch <- &agent.ThinkingChunk{Content: "Thinking..."}
		ch <- &agent.TextChunk{Content: "I'll check pods."}
		ch <- &agent.ToolCallChunk{CallID: "c1", Name: "k8s.pods", Arguments: "{}"}
		ch <- &agent.UsageChunk{InputTokens: 50, OutputTokens: 100, TotalTokens: 150}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Thinking...", resp.ThinkingText)
		assert.Equal(t, "I'll check pods.", resp.Text)
		require.Len(t, resp.ToolCalls, 1)
		require.NotNil(t, resp.Usage)
		assert.Equal(t, 150, resp.Usage.TotalTokens)
	})
}

// ============================================================================
// callLLM tests
// ============================================================================

func TestCallLLM(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		llm := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Hello"},
					&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
				}},
			},
		}

		resp, err := callLLM(context.Background(), llm, &agent.GenerateInput{})
		require.NoError(t, err)
		assert.Equal(t, "Hello", resp.Text)
		assert.Equal(t, 15, resp.Usage.TotalTokens)
	})

	t.Run("generate error", func(t *testing.T) {
		llm := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: fmt.Errorf("connection refused")},
			},
		}

		resp, err := callLLM(context.Background(), llm, &agent.GenerateInput{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "LLM Generate failed")
	})
}

// ============================================================================
// collectStreamWithCallback tests
// ============================================================================

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
		delta     string
	}

	callback := func(chunkType string, delta string) {
		callbacks = append(callbacks, struct {
			chunkType string
			delta     string
		}{chunkType, delta})
	}

	ch := make(chan agent.Chunk, 3)
	ch <- &agent.TextChunk{Content: "Hello "}
	ch <- &agent.TextChunk{Content: "world"}
	ch <- &agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	close(ch)

	resp, err := collectStreamWithCallback(ch, callback)
	require.NoError(t, err)
	assert.Equal(t, "Hello world", resp.Text)

	// Should have 2 text callbacks with delta content (not accumulated)
	require.Len(t, callbacks, 2)
	assert.Equal(t, ChunkTypeText, callbacks[0].chunkType)
	assert.Equal(t, "Hello ", callbacks[0].delta) // First delta
	assert.Equal(t, ChunkTypeText, callbacks[1].chunkType)
	assert.Equal(t, "world", callbacks[1].delta) // Second delta (not accumulated)
}

func TestCollectStreamWithCallback_ThinkingAndTextCallbacks(t *testing.T) {
	var callbacks []struct {
		chunkType string
		delta     string
	}

	callback := func(chunkType string, delta string) {
		callbacks = append(callbacks, struct {
			chunkType string
			delta     string
		}{chunkType, delta})
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

	// 2 thinking deltas + 1 text delta
	require.Len(t, callbacks, 3)
	assert.Equal(t, ChunkTypeThinking, callbacks[0].chunkType)
	assert.Equal(t, "Let me ", callbacks[0].delta)
	assert.Equal(t, ChunkTypeThinking, callbacks[1].chunkType)
	assert.Equal(t, "think...", callbacks[1].delta) // Delta, not accumulated
	assert.Equal(t, ChunkTypeText, callbacks[2].chunkType)
	assert.Equal(t, "The answer is 42.", callbacks[2].delta)
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
