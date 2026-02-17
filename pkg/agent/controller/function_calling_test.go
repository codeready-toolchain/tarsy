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
	"github.com/stretchr/testify/require"
)

func TestFunctionCallingController_HappyPath(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The pods are all running. Everything is healthy.", result.FinalAnalysis)
	require.Equal(t, 70, result.TokensUsed.TotalTokens)
	require.Equal(t, 2, llm.callCount)
}

func TestFunctionCallingController_MultipleToolCalls(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The web-1 pod has OOM issues.", result.FinalAnalysis)
}

func TestFunctionCallingController_ForcedConclusion(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_ThinkingContent(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_ToolExecutionError(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestFunctionCallingController_ConsecutiveTimeouts(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: context.DeadlineExceeded},
			{err: context.DeadlineExceeded},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
}

func TestFunctionCallingController_PrevStageContext(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Based on previous context, the system is healthy."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_ForcedConclusionWithFailedLast(t *testing.T) {
	// Tool calls succeed but last LLM call errors — forced conclusion should fail
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
	require.NotNil(t, result.Error)
	require.Contains(t, result.Error.Error(), "max iterations")
}

func TestFunctionCallingController_LLMErrorRecovery(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "All systems operational.", result.FinalAnalysis)
}

func TestFunctionCallingController_TextAlongsideToolCalls(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Everything is running fine.", result.FinalAnalysis)
}

func TestFunctionCallingController_CodeExecution(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_GoogleSearch(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_UrlContext(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_PromptBuilderIntegration(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	execCtx.Config.CustomInstructions = "Custom native thinking instructions."
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_ForcedConclusionUsesNativeFormat(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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

func TestFunctionCallingController_ForcedConclusionWithGrounding(t *testing.T) {
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
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewFunctionCallingController()

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
