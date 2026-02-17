package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
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

// ============================================================================
// mergeMetadata tests
// ============================================================================

func TestMergeMetadata(t *testing.T) {
	t.Run("nil extra returns base", func(t *testing.T) {
		base := map[string]interface{}{"source": "native"}
		result := mergeMetadata(base, nil)
		assert.Equal(t, base, result)
	})

	t.Run("nil base returns extra", func(t *testing.T) {
		extra := map[string]interface{}{"forced_conclusion": true}
		result := mergeMetadata(nil, extra)
		assert.Equal(t, extra, result)
	})

	t.Run("both nil returns nil", func(t *testing.T) {
		result := mergeMetadata(nil, nil)
		assert.Nil(t, result)
	})

	t.Run("merges base and extra", func(t *testing.T) {
		base := map[string]interface{}{"source": "native"}
		extra := map[string]interface{}{
			"forced_conclusion": true,
			"iterations_used":   1,
			"max_iterations":    1,
		}
		result := mergeMetadata(base, extra)
		assert.Equal(t, map[string]interface{}{
			"source":            "native",
			"forced_conclusion": true,
			"iterations_used":   1,
			"max_iterations":    1,
		}, result)
	})

	t.Run("extra overrides base on conflict", func(t *testing.T) {
		base := map[string]interface{}{"key": "old"}
		extra := map[string]interface{}{"key": "new"}
		result := mergeMetadata(base, extra)
		assert.Equal(t, "new", result["key"])
	})

	t.Run("does not mutate base", func(t *testing.T) {
		base := map[string]interface{}{"source": "native"}
		extra := map[string]interface{}{"forced_conclusion": true}
		_ = mergeMetadata(base, extra)
		assert.Len(t, base, 1, "base should not be mutated")
		assert.Equal(t, "native", base["source"])
	})
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

// ============================================================================
// noopEventPublisher — satisfies agent.EventPublisher for streaming-path tests
// ============================================================================

type noopEventPublisher struct{}

func (noopEventPublisher) PublishTimelineCreated(context.Context, string, events.TimelineCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishTimelineCompleted(context.Context, string, events.TimelineCompletedPayload) error {
	return nil
}
func (noopEventPublisher) PublishStreamChunk(context.Context, string, events.StreamChunkPayload) error {
	return nil
}
func (noopEventPublisher) PublishSessionStatus(context.Context, string, events.SessionStatusPayload) error {
	return nil
}
func (noopEventPublisher) PublishStageStatus(context.Context, string, events.StageStatusPayload) error {
	return nil
}
func (noopEventPublisher) PublishChatCreated(context.Context, string, events.ChatCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishInteractionCreated(context.Context, string, events.InteractionCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishSessionProgress(context.Context, events.SessionProgressPayload) error {
	return nil
}
func (noopEventPublisher) PublishExecutionProgress(context.Context, string, events.ExecutionProgressPayload) error {
	return nil
}
func (noopEventPublisher) PublishExecutionStatus(context.Context, string, events.ExecutionStatusPayload) error {
	return nil
}

// contextExpiryErrorLLMClient sends initial chunks immediately, then waits
// for the caller's context to expire before sending an error chunk. This
// deterministically simulates a stream that outlives its parent context
// deadline — no timing margins or sleeps needed.
type contextExpiryErrorLLMClient struct {
	initialChunks []agent.Chunk
	errorMessage  string
}

func (m *contextExpiryErrorLLMClient) Generate(ctx context.Context, _ *agent.GenerateInput) (<-chan agent.Chunk, error) {
	ch := make(chan agent.Chunk, len(m.initialChunks)+1)

	// Send initial chunks immediately (buffered — no blocking).
	for _, c := range m.initialChunks {
		ch <- c
	}

	// Wait for the caller's context to expire, then send the error.
	// This guarantees the error always arrives AFTER context cancellation,
	// regardless of CI speed — fully deterministic.
	go func() {
		<-ctx.Done()
		ch <- &agent.ErrorChunk{Message: m.errorMessage, Code: "timeout", Retryable: false}
		close(ch)
	}()

	return ch, nil
}

func (m *contextExpiryErrorLLMClient) Close() error { return nil }

// ============================================================================
// callLLMWithStreaming — expired-context cleanup test
// ============================================================================

// TestCallLLMWithStreaming_ExpiredContextCleanup verifies the context-detachment
// fix: when the parent context expires and the LLM stream returns an error,
// streaming timeline events must be marked as failed (not stuck at "streaming").
//
// Reproduces the bug fixed in streaming.go where markStreamingEventsFailed
// used the caller's (expired) context for DB cleanup, causing silent failures.
func TestCallLLMWithStreaming_ExpiredContextCleanup(t *testing.T) {
	execCtx := newTestExecCtx(t, nil, nil)
	execCtx.EventPublisher = noopEventPublisher{}

	// Context expires in 500ms — generous margin for the callback to create
	// timeline events in the DB (involves real PostgreSQL queries) even on
	// slow CI. The mock waits on ctx.Done() before sending the error, so the
	// actual test duration equals this timeout, not a separate sleep.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	llm := &contextExpiryErrorLLMClient{
		initialChunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "analyzing the problem..."},
			&agent.TextChunk{Content: "here is my analysis"},
		},
		errorMessage: "stream deadline exceeded",
	}

	eventSeq := 0
	resp, err := callLLMWithStreaming(ctx, execCtx, llm, &agent.GenerateInput{}, &eventSeq)

	// Stream must return an error.
	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "stream deadline exceeded")

	// Parent context must be expired by now (sanity check).
	require.Error(t, ctx.Err(), "parent context should be expired")

	// Query DB with a fresh context — events must NOT be stuck at "streaming".
	dbEvents, dbErr := execCtx.Services.Timeline.GetAgentTimeline(
		context.Background(), execCtx.ExecutionID,
	)
	require.NoError(t, dbErr)

	// The callback should have created at least one streaming event
	// (thinking or text) before the error arrived.
	require.NotEmpty(t, dbEvents, "expected at least one timeline event to be created")

	for _, evt := range dbEvents {
		assert.NotEqual(t, timelineevent.StatusStreaming, evt.Status,
			"event %s (type=%s) should not be stuck at streaming status", evt.ID, evt.EventType)
		assert.Equal(t, timelineevent.StatusFailed, evt.Status,
			"event %s (type=%s) should be marked as failed", evt.ID, evt.EventType)
		assert.Contains(t, evt.Content, "Streaming failed",
			"event %s content should indicate streaming failure", evt.ID)
	}
}
