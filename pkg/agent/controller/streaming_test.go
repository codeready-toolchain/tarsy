package controller

import (
	"context"
	"fmt"
	"testing"

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
// callLLMWithReActStreaming tests
// ============================================================================

// noopEventPublisher implements agent.EventPublisher with no-ops.
// Used to make callLLMWithReActStreaming not short-circuit.
type noopEventPublisher struct {
	createdEvents   []events.TimelineCreatedPayload
	completedEvents []events.TimelineCompletedPayload
	streamChunks    []events.StreamChunkPayload
}

func (p *noopEventPublisher) PublishTimelineCreated(_ context.Context, _ string, payload events.TimelineCreatedPayload) error {
	p.createdEvents = append(p.createdEvents, payload)
	return nil
}
func (p *noopEventPublisher) PublishTimelineCompleted(_ context.Context, _ string, payload events.TimelineCompletedPayload) error {
	p.completedEvents = append(p.completedEvents, payload)
	return nil
}
func (p *noopEventPublisher) PublishStreamChunk(_ context.Context, _ string, payload events.StreamChunkPayload) error {
	p.streamChunks = append(p.streamChunks, payload)
	return nil
}
func (p *noopEventPublisher) PublishSessionStatus(_ context.Context, _ string, _ events.SessionStatusPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishStageStatus(_ context.Context, _ string, _ events.StageStatusPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishChatCreated(_ context.Context, _ string, _ events.ChatCreatedPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishInteractionCreated(_ context.Context, _ string, _ events.InteractionCreatedPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishSessionProgress(_ context.Context, _ events.SessionProgressPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishExecutionProgress(_ context.Context, _ string, _ events.ExecutionProgressPayload) error {
	return nil
}
func (p *noopEventPublisher) PublishExecutionStatus(_ context.Context, _ string, _ events.ExecutionStatusPayload) error {
	return nil
}

func TestCallLLMWithReActStreaming_ThoughtAndAction(t *testing.T) {
	// LLM returns: Thought + Action (across multiple chunks to simulate
	// realistic streaming) → should stream thought, flag ReactThoughtStreamed.
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: I should check the pods"},
			&agent.TextChunk{Content: "\nAction: k8s.list\nAction Input: {}"},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	// Flags
	assert.True(t, streamed.ReactThoughtStreamed, "thought should be streamed")
	assert.False(t, streamed.FinalAnswerStreamed, "no final answer in this response")
	assert.False(t, streamed.TextEventCreated, "ReAct streaming never creates llm_response")

	// Full text still available for parsing
	assert.Contains(t, streamed.Text, "Thought:")
	assert.Contains(t, streamed.Text, "Action:")

	// DB: one llm_thinking event should be created and completed
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 1)
	assert.Equal(t, timelineevent.EventTypeLlmThinking, tlEvents[0].EventType)
	assert.Equal(t, timelineevent.StatusCompleted, tlEvents[0].Status)
	assert.Equal(t, "I should check the pods", tlEvents[0].Content)
	assert.Equal(t, "react", tlEvents[0].Metadata["source"])

	// WS: at least one stream.chunk was published for the thought
	assert.NotEmpty(t, pub.streamChunks, "should have published stream chunks for thought")
}

func TestCallLLMWithReActStreaming_ThoughtAndFinalAnswer(t *testing.T) {
	// LLM returns: Thought + Final Answer (across multiple chunks to simulate
	// realistic streaming) → both streamed, both flags set.
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: The issue is clear."},
			&agent.TextChunk{Content: "\nFinal Answer: The pod is OOMKilled."},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.True(t, streamed.ReactThoughtStreamed)
	assert.True(t, streamed.FinalAnswerStreamed)
	assert.False(t, streamed.TextEventCreated)

	// DB: llm_thinking (react) + final_analysis
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 2)

	assert.Equal(t, timelineevent.EventTypeLlmThinking, tlEvents[0].EventType)
	assert.Equal(t, "The issue is clear.", tlEvents[0].Content)

	assert.Equal(t, timelineevent.EventTypeFinalAnalysis, tlEvents[1].EventType)
	assert.Equal(t, "The pod is OOMKilled.", tlEvents[1].Content)
}

func TestCallLLMWithReActStreaming_DirectFinalAnswer(t *testing.T) {
	// LLM returns Final Answer without Thought → only final answer streamed
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Final Answer: Everything is healthy."},
			&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.False(t, streamed.ReactThoughtStreamed)
	assert.True(t, streamed.FinalAnswerStreamed)

	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 1)
	assert.Equal(t, timelineevent.EventTypeFinalAnalysis, tlEvents[0].EventType)
	assert.Equal(t, "Everything is healthy.", tlEvents[0].Content)
}

func TestCallLLMWithReActStreaming_NoMarkers(t *testing.T) {
	// LLM returns text without ReAct markers → no streaming events, all flags false
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "I'm not sure what to do here."},
			&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.False(t, streamed.ReactThoughtStreamed)
	assert.False(t, streamed.FinalAnswerStreamed)
	assert.False(t, streamed.TextEventCreated)
	assert.Equal(t, "I'm not sure what to do here.", streamed.Text)

	// No timeline events created
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	assert.Empty(t, tlEvents)
}

func TestCallLLMWithReActStreaming_NativeThinkingPlusReAct(t *testing.T) {
	// Hybrid: native thinking chunks + ReAct text (split across chunks to
	// simulate realistic streaming) → both handled independently.
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the namespace issue."},
			&agent.TextChunk{Content: "Thought: I should check pods."},
			&agent.TextChunk{Content: "\nAction: k8s.list\nAction Input: {}"},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.True(t, streamed.ThinkingEventCreated, "native thinking should be created")
	assert.True(t, streamed.ReactThoughtStreamed, "react thought should be streamed")
	assert.False(t, streamed.FinalAnswerStreamed)

	// DB: 2 llm_thinking events (native + react)
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 2)

	assert.Equal(t, "native", tlEvents[0].Metadata["source"])
	assert.Equal(t, "react", tlEvents[1].Metadata["source"])
}

func TestCallLLMWithReActStreaming_NilPublisher(t *testing.T) {
	// No EventPublisher → falls through to collectStream, all flags false
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: Check.\nFinal Answer: Done."},
			&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	// EventPublisher intentionally nil (default from newTestExecCtx)
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.False(t, streamed.ReactThoughtStreamed)
	assert.False(t, streamed.FinalAnswerStreamed)
	assert.False(t, streamed.ThinkingEventCreated)
	assert.Contains(t, streamed.Text, "Thought:")
}

func TestCallLLMWithReActStreaming_LLMError(t *testing.T) {
	// LLM Generate fails → error returned, no streaming events
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{err: fmt.Errorf("connection refused")}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = &noopEventPublisher{}
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.Error(t, err)
	assert.Nil(t, streamed)
	assert.Contains(t, err.Error(), "LLM Generate failed")
}

func TestCallLLMWithReActStreaming_PreambleThenThought(t *testing.T) {
	// Non-ReAct preamble text followed by Thought → idle ignores preamble, streams thought.
	// Note: "Observation:" in the LLM output would trigger shouldStopParsing (hallucination
	// detection), so we use generic preamble text instead. Real observations are injected as
	// separate user messages by react.go, never in the LLM's own text.
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Let me analyze the pod data.\n\n"},
			&agent.TextChunk{Content: "Thought: The pod list shows web-1.\n"},
			&agent.TextChunk{Content: "Action: k8s.get_logs\nAction Input: {\"pod\": \"web-1\"}"},
			&agent.UsageChunk{InputTokens: 20, OutputTokens: 30, TotalTokens: 50},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)

	assert.True(t, streamed.ReactThoughtStreamed)

	// DB: only the thought event, no event for the preamble
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 1)
	assert.Equal(t, timelineevent.EventTypeLlmThinking, tlEvents[0].EventType)
	assert.Equal(t, "The pod list shows web-1.", tlEvents[0].Content)
}

func TestCallLLMWithReActStreaming_StreamChunkDeltas(t *testing.T) {
	// Verify that stream chunks contain progressive deltas, not full content.
	// Each chunk before the transition marker should produce a delta.
	pub := &noopEventPublisher{}
	llm := &mockLLMClient{
		responses: []mockLLMResponse{{chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Thought: first "},
			&agent.TextChunk{Content: "second "},
			&agent.TextChunk{Content: "third"},
			&agent.TextChunk{Content: "\nAction: tool\nAction Input: {}"},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 10, TotalTokens: 20},
		}}},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.EventPublisher = pub
	eventSeq := 0

	streamed, err := callLLMWithReActStreaming(context.Background(), execCtx, llm,
		&agent.GenerateInput{}, &eventSeq)
	require.NoError(t, err)
	assert.True(t, streamed.ReactThoughtStreamed)

	// Collect all stream chunk deltas for the thought event
	require.NotEmpty(t, pub.streamChunks)
	var combinedDelta string
	for _, sc := range pub.streamChunks {
		combinedDelta += sc.Delta
	}
	// Combined deltas should equal the clean thought content
	assert.Equal(t, "first second third", combinedDelta)

	// DB: finalized content should also match
	tlEvents, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	require.Len(t, tlEvents, 1)
	assert.Equal(t, "first second third", tlEvents[0].Content)
}
