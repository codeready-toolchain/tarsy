package controller

import (
	"context"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReActController_ToolCallLifecycleEvents verifies that the new
// streaming tool call lifecycle creates proper timeline events in the DB.
func TestReActController_ToolCallLifecycleEvents(t *testing.T) {
	// LLM calls: 1) tool call 2) final answer
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: I need to check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Pods look good.\nFinal Answer: Everything is healthy."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running\npod-2 Running", IsError: false},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Query timeline events from DB via the same service the controller used
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)

	// Find the llm_tool_call events — expect exactly one
	var toolCallEvents int
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeLlmToolCall {
			toolCallEvents++

			// Verify completed status (lifecycle: streaming -> completed)
			assert.Equal(t, timelineevent.StatusCompleted, ev.Status,
				"tool call event should be completed")

			// Verify metadata has server_name, tool_name, arguments, and is_error
			assert.Contains(t, ev.Metadata, "tool_name")
			assert.Contains(t, ev.Metadata, "is_error")

			// Verify content is the tool result
			assert.Contains(t, ev.Content, "pod-1 Running")
		}
	}
	assert.Equal(t, 1, toolCallEvents, "should have exactly one llm_tool_call event")
}

// TestReActController_ToolCallErrorLifecycle verifies that tool errors
// are properly reflected in the completed llm_tool_call event.
func TestReActController_ToolCallErrorLifecycle(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Could not reach cluster."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutorFunc{
		tools: tools,
		executeFn: func(_ context.Context, _ agent.ToolCall) (*agent.ToolResult, error) {
			return nil, assert.AnError // Tool execution fails
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Query timeline events via the same service
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)

	// Find the llm_tool_call event — should be marked as completed with is_error=true
	found := false
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeLlmToolCall {
			found = true
			assert.Equal(t, timelineevent.StatusCompleted, ev.Status)
			// is_error must be present and true for failed tools
			require.Contains(t, ev.Metadata, "is_error",
				"is_error key must exist in tool call event metadata")
			assert.Equal(t, true, ev.Metadata["is_error"],
				"is_error should be true for a failed tool call")
			break
		}
	}
	assert.True(t, found, "should have an llm_tool_call event for the failed tool")
}

// TestNativeThinkingController_ToolCallLifecycleEvents verifies the
// NativeThinking controller produces the same lifecycle events.
func TestNativeThinkingController_ToolCallLifecycleEvents(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// First response: tool call
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "I'll check the pods."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			// Second response: final answer (no tool calls)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "The pods are all running. Everything is healthy."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running", IsError: false},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Query timeline events via same service
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)

	var toolCallEvents int
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeLlmToolCall {
			toolCallEvents++
			assert.Equal(t, timelineevent.StatusCompleted, ev.Status)
			assert.Contains(t, ev.Metadata, "tool_name")
			assert.Contains(t, ev.Metadata, "is_error")
			assert.Contains(t, ev.Content, "pod-1 Running")
		}
	}
	assert.Equal(t, 1, toolCallEvents, "should have exactly one llm_tool_call event")
}

// TestReActController_SummarizationIntegration verifies that the summarization
// path is exercised when a tool result exceeds the configured threshold.
func TestReActController_SummarizationIntegration(t *testing.T) {
	// LLM calls: 1) tool call, 2) summarization (internal), 3) final answer
	// The mock LLM receives 3 calls: iteration, summarization, iteration
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Iteration 1: tool call
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: I need to check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			// Summarization LLM call (triggered internally by maybeSummarize)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Summary: 50 pods found, 2 are in CrashLoopBackOff."},
			}},
			// Iteration 2: final answer (uses summarized content)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: The summary shows issues.\nFinal Answer: Two pods are crashing."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}

	// Create a large tool result that exceeds the summarization threshold
	largeResult := strings.Repeat("pod-info-line\n", 200) // ~2800 chars = ~700 tokens

	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: largeResult, IsError: false},
		},
	}

	// Create exec context with summarization-enabled MCP registry
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"k8s": {
			Summarization: &config.SummarizationConfig{
				Enabled:              true,
				SizeThresholdTokens:  100, // Low threshold to trigger summarization
				SummaryMaxTokenLimit: 500,
			},
		},
	})
	pb := prompt.NewPromptBuilder(registry)

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.PromptBuilder = pb
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	assert.Contains(t, result.FinalAnalysis, "crashing")

	// Verify the LLM was called 3 times (iteration + summarization + iteration)
	assert.Equal(t, 3, llm.callCount, "LLM should be called 3 times: iteration, summarization, iteration")
}

// TestReActController_SummarizationFailOpen verifies that when summarization
// fails, the raw tool result is used (fail-open behavior).
func TestReActController_SummarizationFailOpen(t *testing.T) {
	// LLM calls: 1) tool call, 2) summarization (fails), 3) final answer
	callCount := 0
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Iteration 1: tool call
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check pods.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			// Summarization call fails
			{err: assert.AnError},
			// Iteration 2: final answer (uses raw content since summarization failed)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Got data.\nFinal Answer: Pods are fine."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	largeResult := strings.Repeat("pod-data\n", 200)

	executor := &mockToolExecutorFunc{
		tools: tools,
		executeFn: func(_ context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
			callCount++
			return &agent.ToolResult{Content: largeResult, IsError: false}, nil
		},
	}

	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"k8s": {
			Summarization: &config.SummarizationConfig{
				Enabled:             true,
				SizeThresholdTokens: 100,
			},
		},
	})
	pb := prompt.NewPromptBuilder(registry)

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.PromptBuilder = pb
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Despite summarization failure, the controller should complete with the final answer
	assert.Contains(t, result.FinalAnalysis, "Pods are fine")
	assert.Equal(t, 1, callCount, "tool should have been called once")
	assert.Equal(t, 3, llm.callCount, "LLM should be called 3 times: iteration, failed summarization, iteration")
}

// TestReActController_StorageTruncation verifies that very large tool
// results are truncated for storage in the timeline event.
func TestReActController_StorageTruncation(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Check.\nAction: k8s.get_pods\nAction Input: {}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: All good."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}

	// Create a massive result (well above storage threshold)
	massiveResult := strings.Repeat("x", 50000) // ~12500 tokens, above 8000 storage limit

	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: massiveResult, IsError: false},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewReActController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Query timeline events — the tool call event content should be truncated
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)

	found := false
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeLlmToolCall {
			found = true
			assert.Less(t, len(ev.Content), len(massiveResult),
				"stored content should be smaller than original")
			assert.Contains(t, ev.Content, "[TRUNCATED:",
				"stored content should have truncation marker")
			break
		}
	}
	assert.True(t, found, "expected llm_tool_call event not found")
}

// TestNativeThinkingController_SummarizationIntegration verifies that
// summarization works in the NativeThinking controller. This exercises a
// different code path than ReAct: tool results are appended as role=tool
// messages with ToolCallID, not as "Observation:" user messages.
func TestNativeThinkingController_SummarizationIntegration(t *testing.T) {
	// LLM calls: 1) tool call, 2) summarization (internal), 3) final answer
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Iteration 1: tool call
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "I'll check the pods."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			// Summarization LLM call (triggered internally by maybeSummarize)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Summary: 50 pods found, 2 are in CrashLoopBackOff."},
			}},
			// Iteration 2: final answer (uses summarized content)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Two pods are crashing in the cluster."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}

	// Large tool result exceeding the summarization threshold
	largeResult := strings.Repeat("pod-info-line\n", 200) // ~2800 chars = ~700 tokens

	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: largeResult, IsError: false},
		},
	}

	// Configure summarization for the "k8s" server
	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"k8s": {
			Summarization: &config.SummarizationConfig{
				Enabled:              true,
				SizeThresholdTokens:  100, // Low threshold to trigger summarization
				SummaryMaxTokenLimit: 500,
			},
		},
	})
	pb := prompt.NewPromptBuilder(registry)

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	execCtx.PromptBuilder = pb
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	assert.Contains(t, result.FinalAnalysis, "crashing")

	// Verify the LLM was called 3 times (iteration + summarization + iteration)
	assert.Equal(t, 3, llm.callCount, "LLM should be called 3 times: iteration, summarization, iteration")
}

// TestNativeThinkingController_SummarizationFailOpen verifies that when
// summarization fails in the NativeThinking controller, the raw tool result
// is used as the tool response message (fail-open behavior).
func TestNativeThinkingController_SummarizationFailOpen(t *testing.T) {
	// LLM calls: 1) tool call, 2) summarization (fails), 3) final answer
	toolCallCount := 0
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Iteration 1: tool call
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Checking pods."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			// Summarization call fails
			{err: assert.AnError},
			// Iteration 2: final answer (uses raw content since summarization failed)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Pods are running correctly."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	largeResult := strings.Repeat("pod-data\n", 200)

	executor := &mockToolExecutorFunc{
		tools: tools,
		executeFn: func(_ context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
			toolCallCount++
			return &agent.ToolResult{Content: largeResult, IsError: false}, nil
		},
	}

	registry := config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
		"k8s": {
			Summarization: &config.SummarizationConfig{
				Enabled:             true,
				SizeThresholdTokens: 100,
			},
		},
	})
	pb := prompt.NewPromptBuilder(registry)

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	execCtx.PromptBuilder = pb
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Despite summarization failure, the controller completes with the final answer
	assert.Contains(t, result.FinalAnalysis, "Pods are running correctly")
	assert.Equal(t, 1, toolCallCount, "tool should have been called once")
	assert.Equal(t, 3, llm.callCount, "LLM should be called 3 times: iteration, failed summarization, iteration")
}

// TestNativeThinkingController_StorageTruncation verifies that very large
// tool results are truncated for storage in NativeThinking tool call events.
func TestNativeThinkingController_StorageTruncation(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Checking pods."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "All pods look fine."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}

	// Massive result exceeding the storage threshold
	massiveResult := strings.Repeat("x", 50000) // ~12500 tokens, above 8000 storage limit

	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: massiveResult, IsError: false},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Query timeline events — the tool call event content should be truncated
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)

	found := false
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeLlmToolCall {
			found = true
			assert.Less(t, len(ev.Content), len(massiveResult),
				"stored content should be smaller than original")
			assert.Contains(t, ev.Content, "[TRUNCATED:",
				"stored content should have truncation marker")
			break
		}
	}
	assert.True(t, found, "expected llm_tool_call event not found")
}
