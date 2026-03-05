package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIteratingController_HappyPath(t *testing.T) {
	// LLM calls: 1) tool call 2) final answer (no tools)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ThinkingChunk{Content: "Let me check the pods."},
				&agent.TextChunk{Content: "I'll check the pods."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
			{chunks: []agent.Chunk{
				&agent.ThinkingChunk{Content: "Pods look healthy."},
				&agent.TextChunk{Content: "The pods are all running. Everything is healthy."},
				&agent.UsageChunk{InputTokens: 15, OutputTokens: 25, TotalTokens: 40},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running\npod-2 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The pods are all running. Everything is healthy.", result.FinalAnalysis)
	require.Equal(t, 70, result.TokensUsed.TotalTokens)
	require.Equal(t, 2, llm.callCount)
}

func TestIteratingController_EmptyResponseRetry(t *testing.T) {
	// LLM returns empty text on first attempt, then responds on retry.
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 0, TotalTokens: 5},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Here is my response."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 15, TotalTokens: 25},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Here is my response.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount, "should retry after empty response")

	// The retry message should be in the second call's messages
	lastMessages := llm.capturedInputs[1].Messages
	lastUserMsg := lastMessages[len(lastMessages)-1]
	assert.Equal(t, agent.RoleUser, lastUserMsg.Role)
	assert.Contains(t, lastUserMsg.Content, "empty")
}

func TestIteratingController_EmptyResponseRetry_ExhaustsRetries(t *testing.T) {
	// LLM returns empty text every time; after maxEmptyResponseRetries we accept it.
	responses := make([]mockLLMResponse, maxEmptyResponseRetries+1)
	for i := range responses {
		responses[i] = mockLLMResponse{chunks: []agent.Chunk{
			&agent.UsageChunk{InputTokens: 5, OutputTokens: 0, TotalTokens: 5},
		}}
	}

	llm := &mockLLMClient{responses: responses}
	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "", result.FinalAnalysis, "should accept empty after retries exhausted")
	require.Equal(t, maxEmptyResponseRetries+1, llm.callCount)
}

func TestIteratingController_EmptyResponseRetry_SkipsOnCancelledContext(t *testing.T) {
	// When the context is cancelled, empty responses are a side-effect of
	// stream closure — not a genuine empty reply. No retry should fire.
	// The controller propagates the DB error from storeAssistantMessage;
	// the caller (executor) maps it to the correct cancelled status.
	ctx, cancel := context.WithCancel(context.Background())

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 0, TotalTokens: 5},
			}},
		},
		onGenerate: func(_ int) { cancel() },
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewIteratingController()

	_, err := ctrl.Run(ctx, execCtx, "")
	require.Equal(t, 1, llm.callCount, "should NOT retry — context is cancelled")
	require.ErrorIs(t, err, context.Canceled)
}

func TestIteratingController_EmptyResponseRetry_ResetsAcrossTurns(t *testing.T) {
	// Empty response → retry → tool call → empty response again.
	// The second empty occurrence must get fresh retries (counter was reset
	// by the intervening tool-call turn).
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Turn 1: empty response (emptyRetries → 1)
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 2, OutputTokens: 0, TotalTokens: 2},
			}},
			// Turn 1 retry: tool call (emptyRetries reset → 0)
			{chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: "call-1", Name: "test.tool", Arguments: "{}"},
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 5, TotalTokens: 10},
			}},
			// Turn 2: empty response again (emptyRetries → 1, NOT 2)
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 3, OutputTokens: 0, TotalTokens: 3},
			}},
			// Turn 2 retry: final answer
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Done."},
				&agent.UsageChunk{InputTokens: 8, OutputTokens: 10, TotalTokens: 18},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "test.tool", Description: "A test tool"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"test.tool": {Content: "tool result"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Done.", result.FinalAnalysis)
	require.Equal(t, 4, llm.callCount, "2 empty + 1 tool call + 1 final = 4 calls")
}

func TestIteratingController_MultipleToolCalls(t *testing.T) {
	// Single LLM response with multiple tool calls
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Let me check pods and logs simultaneously."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
				&agent.ToolCallChunk{CallID: "call-2", Name: "k8s.get_logs", Arguments: "{\"pod\": \"web-1\"}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "The web-1 pod has OOM issues."},
			}},
		},
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "Get pods"},
		{Name: "k8s.get_logs", Description: "Get logs"},
	}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "web-1 Running"},
			"k8s.get_logs": {Content: "OOMKilled"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The web-1 pod has OOM issues.", result.FinalAnalysis)
}

func TestIteratingController_ForcedConclusion(t *testing.T) {
	// LLM keeps calling tools, never produces text-only response
	var responses []mockLLMResponse
	for i := 0; i < 3; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "k8s.get_pods", Arguments: "{}"},
			},
		})
	}
	// Forced conclusion response (no tools)
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Based on investigation: system is healthy."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "system is healthy")

	// Verify forced conclusion metadata on final_analysis timeline event
	events, qErr := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, qErr)
	found := false
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeFinalAnalysis {
			found = true
			require.Equal(t, true, ev.Metadata["forced_conclusion"], "final_analysis should have forced_conclusion=true")
			require.EqualValues(t, 3, ev.Metadata["iterations_used"], "should report 3 iterations used")
			require.EqualValues(t, 3, ev.Metadata["max_iterations"], "should report max_iterations=3")
			break
		}
	}
	require.True(t, found, "expected final_analysis timeline event")
}

func TestIteratingController_ThinkingContent(t *testing.T) {
	// Verify thinking content is processed without error and the LLM receives
	// the thinking chunk. Timeline event verification would require querying the
	// DB for the event (the mock executor doesn't expose recorded events), so we
	// verify the LLM was called and completed successfully with thinking input.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ThinkingChunk{Content: "I need to analyze this carefully."},
				&agent.TextChunk{Content: "The system appears to be functioning normally."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The system appears to be functioning normally.", result.FinalAnalysis)
	require.Equal(t, 1, llm.callCount, "LLM should be called exactly once for thinking+text response")

	// Verify thinking content was persisted as a timeline event by querying the DB
	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundThinking := false
	for _, ev := range events {
		if ev.EventType == "llm_thinking" && strings.Contains(ev.Content, "I need to analyze this carefully") {
			foundThinking = true
			break
		}
	}
	require.True(t, foundThinking, "thinking content should be recorded as a timeline event")
}

func TestIteratingController_ToolExecutionError(t *testing.T) {
	// Tool fails, LLM recovers
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Despite the tool error, I can conclude the system is healthy."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools:   tools,
		results: map[string]*agent.ToolResult{},
		// get_pods will return error because it's not in results
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestIteratingController_ConsecutiveTimeouts(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: context.DeadlineExceeded},
			{err: context.DeadlineExceeded},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
}

func TestIteratingController_PrevStageContext(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Based on previous context, the system is healthy."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 found high CPU usage on node-3.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Verify prev stage context was included in messages sent to LLM
	require.NotNil(t, llm.lastInput)
	found := false
	for _, msg := range llm.lastInput.Messages {
		if strings.Contains(msg.Content, "Agent 1 found high CPU usage on node-3") {
			found = true
			break
		}
	}
	require.True(t, found, "previous stage context not found in LLM messages")
}

func TestIteratingController_ForcedConclusionWithFailedLastLLM(t *testing.T) {
	// Last LLM call errors — forced conclusion should still be attempted
	var responses []mockLLMResponse
	for i := 0; i < 2; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "k8s.get_pods", Arguments: "{}"},
			},
		})
	}
	// 3rd iteration (last): LLM error
	responses = append(responses, mockLLMResponse{
		err: fmt.Errorf("connection reset"),
	})
	// Forced conclusion response (called without tools after max iterations)
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Despite issues, the system appears healthy."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "system appears healthy")
}

func TestIteratingController_ForcedConclusionWithFailedToolCall(t *testing.T) {
	// Tool call fails on the last iteration — forced conclusion should still succeed.
	// This covers the case where an MCP tool returns an error (not a tarsy issue),
	// and we happen to hit the iteration limit right after.
	var responses []mockLLMResponse
	for i := 0; i < 3; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "k8s.exec_command", Arguments: "{}"},
			},
		})
	}
	// Forced conclusion response
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "The exec command was denied, but investigation is complete."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.exec_command", Description: "Execute command"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.exec_command": {
				Content: "Error: admission webhook denied the request",
				IsError: true,
			},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "investigation is complete")
}

func TestIteratingController_LLMErrorRecovery(t *testing.T) {
	// First call errors, second succeeds with a final answer
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: fmt.Errorf("temporary failure")},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "All systems operational."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "All systems operational.", result.FinalAnalysis)
}

func TestIteratingController_RecoversFromPartialStreamError(t *testing.T) {
	partial := "Partial analysis before provider failure."
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: partial},
				&agent.ErrorChunk{
					Message:   "Stream failed after partial output (30 chunks): [test-id] Google API server error: 500 INTERNAL",
					Code:      "partial_stream_error",
					Retryable: false,
				},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Recovered and completed."},
			}},
		},
	}

	executor := &mockToolExecutor{
		tools: []agent.ToolDefinition{
			{Name: "k8s.get_pods", Description: "Get pods"},
		},
	}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.Config.MaxIterations = 2
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Recovered and completed.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount, "controller should issue a follow-up LLM call after partial stream failure")

	// The second call should include the retry context with partial output.
	require.Len(t, llm.capturedInputs, 2)
	foundRetryWithPartial := false
	for _, msg := range llm.capturedInputs[1].Messages {
		if strings.Contains(msg.Content, "Your partial response before the error:") &&
			strings.Contains(msg.Content, partial) {
			foundRetryWithPartial = true
			break
		}
	}
	require.True(t, foundRetryWithPartial, "follow-up LLM call should include partial output retry context")

	// Verify the second call used the regular iteration path (with tools), not forceConclusion (no tools).
	require.NotNil(t, llm.capturedInputs[1].Tools, "second call should carry iteration tools")
	require.Len(t, llm.capturedInputs[1].Tools, 1, "second call should be regular iteration, not forceConclusion")
	require.Equal(t, "k8s.get_pods", llm.capturedInputs[1].Tools[0].Name)
}

func TestIteratingController_TextAlongsideToolCalls(t *testing.T) {
	// LLM returns text AND tool calls — text should be recorded as llm_response
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "I'll check the cluster status."},
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Everything is running fine."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Everything is running fine.", result.FinalAnalysis)
}

func TestIteratingController_CodeExecution(t *testing.T) {
	// LLM returns code execution chunks alongside text — should create code_execution events
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "I computed the result."},
				&agent.CodeExecutionChunk{Code: "print(2 + 2)", Result: ""},
				&agent.CodeExecutionChunk{Code: "", Result: "4"},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Verify code_execution event was recorded
	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundCodeExec := false
	for _, ev := range events {
		if ev.EventType == "code_execution" {
			foundCodeExec = true
			require.Contains(t, ev.Content, "print(2 + 2)")
			require.Contains(t, ev.Content, "4")
			break
		}
	}
	require.True(t, foundCodeExec, "code_execution event should be recorded")
}

func TestIteratingController_GoogleSearch(t *testing.T) {
	// LLM returns grounding with search queries — should create google_search_result event
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "According to my research, Kubernetes 1.30 was released."},
				&agent.GroundingChunk{
					WebSearchQueries: []string{"Kubernetes latest version"},
					Sources:          []agent.GroundingSource{{URI: "https://k8s.io", Title: "Kubernetes"}},
				},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Verify google_search_result event was recorded
	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundSearch := false
	for _, ev := range events {
		if ev.EventType == "google_search_result" {
			foundSearch = true
			require.Contains(t, ev.Content, "Kubernetes latest version")
			require.Contains(t, ev.Content, "https://k8s.io")
			break
		}
	}
	require.True(t, foundSearch, "google_search_result event should be recorded")
}

func TestIteratingController_UrlContext(t *testing.T) {
	// LLM returns grounding WITHOUT search queries — should create url_context_result event
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Based on the documentation."},
				&agent.GroundingChunk{
					Sources: []agent.GroundingSource{{URI: "https://docs.example.com/guide", Title: "Guide"}},
				},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundURL := false
	for _, ev := range events {
		if ev.EventType == "url_context_result" {
			foundURL = true
			require.Contains(t, ev.Content, "https://docs.example.com/guide")
			break
		}
	}
	require.True(t, foundURL, "url_context_result event should be recorded")
}

func TestIteratingController_PromptBuilderIntegration(t *testing.T) {
	// Verify the prompt builder produces the expected message structure for function calling:
	// system msg with three-tier instructions, user msg WITHOUT tool descriptions (tools are bound natively).
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "The system is healthy."},
			}},
		},
	}

	tools := []agent.ToolDefinition{
		{Name: "k8s.get_pods", Description: "List pods"},
	}
	executor := &mockToolExecutor{tools: tools, results: map[string]*agent.ToolResult{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.AlertType = "kubernetes"
	execCtx.RunbookContent = "# Test Runbook\nStep 1: Check pods"
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.Config.CustomInstructions = "Custom native thinking instructions."
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "Previous stage data.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	require.NotNil(t, llm.lastInput)
	require.GreaterOrEqual(t, len(llm.lastInput.Messages), 2)

	systemMsg := llm.lastInput.Messages[0]
	userMsg := llm.lastInput.Messages[1]

	// System message should have SRE instructions and task focus
	require.Equal(t, "system", systemMsg.Role)
	require.Contains(t, systemMsg.Content, "General SRE Agent Instructions")
	require.Contains(t, systemMsg.Content, "Focus on investigation")
	require.Contains(t, systemMsg.Content, "Custom native thinking instructions.")
	require.NotContains(t, systemMsg.Content, "Action Input:")
	require.NotContains(t, systemMsg.Content, "REQUIRED FORMAT")

	// User message should have alert data, runbook, chain context, task — but NO tool descriptions
	require.Equal(t, "user", userMsg.Role)
	require.NotContains(t, userMsg.Content, "Available tools")
	require.Contains(t, userMsg.Content, "Alert Details")
	require.Contains(t, userMsg.Content, "Runbook Content")
	require.Contains(t, userMsg.Content, "Test Runbook")
	require.Contains(t, userMsg.Content, "Previous Stage Data")
	require.Contains(t, userMsg.Content, "Previous stage data.")
	require.Contains(t, userMsg.Content, "Your Task")

	// Native thinking should pass tools natively (not in text)
	require.NotNil(t, llm.lastInput.Tools)
	require.Len(t, llm.lastInput.Tools, 1)
	// Tool names stay in canonical "server.tool" format; LLM service handles encoding
	require.Equal(t, "k8s.get_pods", llm.lastInput.Tools[0].Name)
}

func TestIteratingController_ForcedConclusionUsesNativeFormat(t *testing.T) {
	// Verify the forced conclusion prompt uses native thinking format (no "Final Answer:" marker)
	var responses []mockLLMResponse
	for i := 0; i < 3; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "k8s.get_pods", Arguments: "{}"},
			},
		})
	}
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "System is healthy."},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools:   tools,
		results: map[string]*agent.ToolResult{"k8s.get_pods": {Content: "pod-1 Running"}},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// The forced conclusion prompt should use function calling format
	require.NotNil(t, llm.lastInput)
	lastUserMsg := ""
	for i := len(llm.lastInput.Messages) - 1; i >= 0; i-- {
		if llm.lastInput.Messages[i].Role == "user" {
			lastUserMsg = llm.lastInput.Messages[i].Content
			break
		}
	}
	require.Contains(t, lastUserMsg, "iteration limit")
	require.Contains(t, lastUserMsg, "structured conclusion")
	require.NotContains(t, lastUserMsg, "Final Answer:")
}

func TestIteratingController_ForcedConclusionWithGrounding(t *testing.T) {
	// Verify grounding events are created during forced conclusion too
	var responses []mockLLMResponse
	for i := 0; i < 3; i++ {
		responses = append(responses, mockLLMResponse{
			chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: fmt.Sprintf("call-%d", i), Name: "k8s.get_pods", Arguments: "{}"},
			},
		})
	}
	// Forced conclusion response with grounding
	responses = append(responses, mockLLMResponse{
		chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Based on investigation and research."},
			&agent.GroundingChunk{
				WebSearchQueries: []string{"k8s troubleshooting"},
				Sources:          []agent.GroundingSource{{URI: "https://k8s.io/docs", Title: "K8s Docs"}},
			},
		},
	})

	llm := &mockLLMClient{responses: responses}
	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 3
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewIteratingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundSearch := false
	for _, ev := range events {
		if ev.EventType == "google_search_result" {
			foundSearch = true
			break
		}
	}
	require.True(t, foundSearch, "google_search_result event should be created during forced conclusion")
}

// ─── Sub-agent drain/wait tests ─────────────────────────────────────────────

func TestIteratingController_DrainSubAgentResults(t *testing.T) {
	// Sub-agent results are available before the LLM call. They should be
	// drained and included in the messages sent to the LLM.
	subAgentMsg := agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: "[Sub-agent completed] LogAnalyzer (exec abc): Found 42 errors",
	}

	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// First LLM call: after drain, LLM produces final answer
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Based on the sub-agent findings..."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.SubAgentCollector = &mockSubAgentCollector{
		drainResults: []agent.ConversationMessage{subAgentMsg},
		pending:      false,
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Based on the sub-agent findings...", result.FinalAnalysis)

	// Verify the LLM saw the drained sub-agent result in its messages
	require.Len(t, llm.capturedInputs, 1)
	foundSubAgentMsg := false
	for _, m := range llm.capturedInputs[0].Messages {
		if strings.Contains(m.Content, "Found 42 errors") {
			foundSubAgentMsg = true
			break
		}
	}
	require.True(t, foundSubAgentMsg, "LLM should see the drained sub-agent result")
}

func TestIteratingController_WaitForPendingSubAgents(t *testing.T) {
	// LLM returns no tool calls, but sub-agents are pending. Controller
	// should wait for a result, inject it, and give the LLM another turn.
	subAgentMsg := agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: "[Sub-agent completed] MetricChecker (exec def): Latency spike at 14:30",
	}

	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// First call: LLM has no tool calls (triggers wait)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Waiting for sub-agent results..."},
			}},
			// Second call: after wait result injected, LLM produces final answer
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "The latency spike was caused by..."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.SubAgentCollector = &mockSubAgentCollector{
		waitResults: []agent.ConversationMessage{subAgentMsg},
		pending:     true,
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The latency spike was caused by...", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount, "LLM should be called twice: once before wait, once after")

	// Second LLM call should include the waited sub-agent result
	foundSubAgentMsg := false
	for _, m := range llm.capturedInputs[1].Messages {
		if strings.Contains(m.Content, "Latency spike at 14:30") {
			foundSubAgentMsg = true
			break
		}
	}
	require.True(t, foundSubAgentMsg, "second LLM call should see the waited sub-agent result")
}

func TestIteratingController_DrainAndWait(t *testing.T) {
	// Combined scenario: drain picks up a result before the first LLM call,
	// LLM makes a tool call, then on iteration 2 returns no tools with
	// pending sub-agents → wait delivers a second result → iteration 3
	// the LLM produces the final answer.
	drainMsg := agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: "[Sub-agent completed] LogAnalyzer (exec abc): Found 42 errors",
	}
	waitMsg := agent.ConversationMessage{
		Role:    agent.RoleUser,
		Content: "[Sub-agent completed] MetricChecker (exec def): Latency spike at 14:30",
	}

	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Iteration 1: after drain, LLM makes a tool call
			{chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: "call-1", Name: "k8s.get_pods", Arguments: "{}"},
			}},
			// Iteration 2: no tool calls → triggers wait (pending=true)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Processing sub-agent results..."},
			}},
			// Iteration 3: final answer after wait result injected
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Both agents found issues: errors and latency."},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "k8s.get_pods", Description: "Get pods"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"k8s.get_pods": {Content: "pod-1 Running"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.SubAgentCollector = &mockSubAgentCollector{
		drainResults: []agent.ConversationMessage{drainMsg},
		waitResults:  []agent.ConversationMessage{waitMsg},
		pending:      true,
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Both agents found issues: errors and latency.", result.FinalAnalysis)
	require.Equal(t, 3, llm.callCount)

	// Iteration 1: LLM should see the drained result
	foundDrain := false
	for _, m := range llm.capturedInputs[0].Messages {
		if strings.Contains(m.Content, "Found 42 errors") {
			foundDrain = true
			break
		}
	}
	require.True(t, foundDrain, "first LLM call should include drained sub-agent result")

	// Iteration 3: LLM should see the waited result
	foundWait := false
	for _, m := range llm.capturedInputs[2].Messages {
		if strings.Contains(m.Content, "Latency spike at 14:30") {
			foundWait = true
			break
		}
	}
	require.True(t, foundWait, "third LLM call should include waited sub-agent result")
}

func TestIteratingController_WaitCancelledReturnsCancelled(t *testing.T) {
	// WaitForResult returns context.Canceled → return cancelled result (not failed)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Waiting for sub-agents..."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.SubAgentCollector = &mockSubAgentCollector{
		waitResults: []agent.ConversationMessage{{}},
		waitErrors:  []error{context.Canceled},
		pending:     true,
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCancelled, result.Status)
	require.Contains(t, result.Error.Error(), "sub-agent wait interrupted")
	require.Equal(t, 1, llm.callCount)
}

func TestIteratingController_WaitTimeoutReturnsTimedOut(t *testing.T) {
	// WaitForResult returns context.DeadlineExceeded → return timed_out result
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Waiting for sub-agents..."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	execCtx.SubAgentCollector = &mockSubAgentCollector{
		waitResults: []agent.ConversationMessage{{}},
		waitErrors:  []error{context.DeadlineExceeded},
		pending:     true,
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusTimedOut, result.Status)
	require.Contains(t, result.Error.Error(), "sub-agent wait interrupted")
	require.Equal(t, 1, llm.callCount)
}

func TestIteratingController_LLMErrorWithCancelledContextReturnsCancelled(t *testing.T) {
	// When the parent context is cancelled during an LLM call, the controller
	// should return cancelled immediately instead of retrying through max iterations.
	ctx, cancel := context.WithCancel(context.Background())

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: fmt.Errorf("gRPC Generate call failed: %w", context.Canceled)},
		},
		onGenerate: func(_ int) { cancel() },
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini

	ctrl := NewIteratingController()
	result, err := ctrl.Run(ctx, execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCancelled, result.Status)
	require.Contains(t, result.Error.Error(), "execution interrupted")
	require.Equal(t, 1, llm.callCount)
}

func TestIteratingController_NilCollectorSkipsDrainWait(t *testing.T) {
	// With nil SubAgentCollector, controller behaves exactly as before —
	// no drain, no wait, just the normal iteration loop.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Everything is fine."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	// SubAgentCollector is nil by default

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Everything is fine.", result.FinalAnalysis)
	require.Equal(t, 1, llm.callCount)
}

func TestIteratingController_FallbackOnMaxRetries(t *testing.T) {
	// First call: max_retries error → immediate fallback
	// Second call (with fallback provider): success
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "all retries exhausted", Code: "max_retries", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Analysis complete with fallback provider."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMProviderName = "primary-provider"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		makeFallbackEntry("fallback-provider", config.LLMBackendNativeGemini, "fallback-model"),
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Analysis complete with fallback provider.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount, "should have made 2 LLM calls")

	// Verify the second call used the fallback provider config
	require.Len(t, llm.capturedInputs, 2)
	require.Equal(t, "fallback-model", llm.capturedInputs[1].Config.Model)
	require.True(t, llm.capturedInputs[1].ClearCache, "second call should have ClearCache set")

	// Verify provider was swapped in execCtx
	require.Equal(t, "fallback-provider", execCtx.Config.LLMProviderName)
	require.Equal(t, config.LLMBackendNativeGemini, execCtx.Config.LLMBackend)
}

func TestIteratingController_FallbackOnCredentials_Immediate(t *testing.T) {
	// credentials errors trigger immediate fallback (no Go retry needed)
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "API key invalid", Code: "credentials", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Success with fallback."},
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMProviderName = "bad-creds-provider"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		makeFallbackEntry("good-provider", config.LLMBackendNativeGemini, "good-model"),
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, 2, llm.callCount, "credentials should trigger immediate fallback, no extra retry")
	require.Equal(t, "good-model", llm.capturedInputs[1].Config.Model)
}

func TestIteratingController_FallbackProviderError_RequiresOneRetry(t *testing.T) {
	// provider_error requires 1 Go retry before fallback.
	// Call 1: provider_error → no fallback (first occurrence)
	// Call 2: provider_error → fallback triggered (second consecutive)
	// Call 3: success with fallback provider
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "provider down", Code: "provider_error", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "provider still down", Code: "provider_error", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Recovered via fallback."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		makeFallbackEntry("fallback", config.LLMBackendLangChain, "fallback-model"),
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, 3, llm.callCount)

	// First two calls with primary, third with fallback
	require.Equal(t, "test-model", llm.capturedInputs[0].Config.Model)
	require.Equal(t, "test-model", llm.capturedInputs[1].Config.Model)
	require.Equal(t, "fallback-model", llm.capturedInputs[2].Config.Model)
}

func TestIteratingController_FallbackInForcedConclusion(t *testing.T) {
	// Use maxIter=1, first call returns a tool call (consuming the iteration),
	// then forced conclusion fails with max_retries → fallback → success.
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Iteration 1: tool call
			{chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: "call-1", Name: "test.tool", Arguments: "{}"},
			}},
			// Forced conclusion: max_retries error → fallback
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "retries exhausted", Code: "max_retries", Retryable: false},
			}},
			// Forced conclusion retry with fallback: success
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Forced conclusion via fallback."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "test.tool", Description: "A test tool"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"test.tool": {Content: "tool result"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 1
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		makeFallbackEntry("fallback", config.LLMBackendNativeGemini, "fallback-model"),
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Forced conclusion via fallback.", result.FinalAnalysis)
	require.Equal(t, 3, llm.callCount)

	// Third call should use fallback provider with ClearCache
	require.Equal(t, "fallback-model", llm.capturedInputs[2].Config.Model)
	require.True(t, llm.capturedInputs[2].ClearCache)
}

func TestIteratingController_ForcedConclusionEmptyRetry(t *testing.T) {
	// maxIter=1 with a tool call consumes the iteration, triggering forced
	// conclusion. First forced conclusion returns empty → retry → success.
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Iteration 1: tool call (consumes the only iteration)
			{chunks: []agent.Chunk{
				&agent.ToolCallChunk{CallID: "call-1", Name: "test.tool", Arguments: "{}"},
			}},
			// Forced conclusion: empty response
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 0, TotalTokens: 5},
			}},
			// Forced conclusion retry: success
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Final answer after retry."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	tools := []agent.ToolDefinition{{Name: "test.tool", Description: "A test tool"}}
	executor := &mockToolExecutor{
		tools: tools,
		results: map[string]*agent.ToolResult{
			"test.tool": {Content: "tool result"},
		},
	}

	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.MaxIterations = 1

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Final answer after retry.", result.FinalAnalysis)
	require.Equal(t, 3, llm.callCount)

	// Tokens from both forced-conclusion attempts must be accumulated (5 + 30)
	assert.Equal(t, 35, result.TokensUsed.TotalTokens)

	// Verify the retry nudge was injected into the third call
	lastMessages := llm.capturedInputs[2].Messages
	lastUserMsg := lastMessages[len(lastMessages)-1]
	assert.Equal(t, agent.RoleUser, lastUserMsg.Role)
	assert.Contains(t, lastUserMsg.Content, "empty")
}

func TestIteratingController_NoFallbackWithEmptyList(t *testing.T) {
	// When no fallback providers are configured, errors go through normal retry path
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "retries exhausted", Code: "max_retries", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Recovered on retry."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	// No fallback providers (default)

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, 2, llm.callCount, "should have retried with same provider")
}

func TestIteratingController_FallbackSetsTimelineEvent(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "retries exhausted", Code: "max_retries", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Success."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		makeFallbackEntry("fallback", config.LLMBackendLangChain, "fallback-model"),
	}

	ctrl := NewIteratingController()
	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	// Verify provider_fallback timeline event was created
	events, listErr := execCtx.Services.Timeline.GetSessionTimeline(
		context.Background(), execCtx.SessionID)
	require.NoError(t, listErr)

	var foundFallbackEvent bool
	for _, evt := range events {
		if evt.EventType == timelineevent.EventTypeProviderFallback {
			foundFallbackEvent = true
			require.Contains(t, evt.Content, "primary")
			require.Contains(t, evt.Content, "fallback")

			require.NotNil(t, evt.Metadata, "fallback event should have metadata")
			assert.Equal(t, "max_retries", evt.Metadata["error_code"])
			assert.Equal(t, false, evt.Metadata["error_retryable"])
			assert.Equal(t, "primary", evt.Metadata["original_provider"])
			assert.Equal(t, "fallback", evt.Metadata["fallback_provider"])
		}
	}
	require.True(t, foundFallbackEvent, "should have a provider_fallback timeline event")
}
