package controller

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestSynthesisController_HappyPath(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Based on both agents' findings, the root cause is OOM on web-1."},
				&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesis
	ctrl := NewSynthesisController()

	prevContext := "Agent 1: Pods show high memory.\nAgent 2: Logs show OOMKilled."
	result, err := ctrl.Run(context.Background(), execCtx, prevContext)
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "OOM on web-1")
	require.Equal(t, 150, result.TokensUsed.TotalTokens)
	require.Equal(t, 1, llm.callCount)
}

func TestSynthesisController_WithThinking(t *testing.T) {
	// synthesis-native-thinking may produce thinking content
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ThinkingChunk{Content: "Let me analyze both agents' findings carefully."},
				&agent.TextChunk{Content: "Comprehensive analysis: the system is healthy."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesisNativeThinking
	ctrl := NewSynthesisController()

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 found no issues.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Comprehensive analysis: the system is healthy.", result.FinalAnalysis)
}

func TestSynthesisController_LLMError(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: context.DeadlineExceeded},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesis
	ctrl := NewSynthesisController()

	_, err := ctrl.Run(context.Background(), execCtx, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "synthesis LLM call failed")
}

func TestSynthesisController_PromptBuilderIntegration(t *testing.T) {
	// Verify the prompt builder produces synthesis-specific messages:
	// system msg with SRE instructions + task focus, user msg with synthesis task,
	// alert data, runbook, and previous stage context.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized analysis: OOM on web-1."},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.AlertType = "kubernetes"
	execCtx.RunbookContent = "# Synthesis Runbook\nReview agent findings."
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesis
	execCtx.Config.CustomInstructions = "Custom synthesis instructions."
	ctrl := NewSynthesisController()

	prevContext := "Agent 1: Pods show high memory.\nAgent 2: Logs show OOMKilled."
	result, err := ctrl.Run(context.Background(), execCtx, prevContext)
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	require.NotNil(t, llm.lastInput)
	require.GreaterOrEqual(t, len(llm.lastInput.Messages), 2)

	systemMsg := llm.lastInput.Messages[0]
	userMsg := llm.lastInput.Messages[1]

	// System message: SRE instructions + custom instructions (no ReAct format, no taskFocus)
	require.Equal(t, agent.RoleSystem, systemMsg.Role)
	require.Contains(t, systemMsg.Content, "General SRE Analysis Instructions")
	require.Contains(t, systemMsg.Content, "Custom synthesis instructions.")
	require.NotContains(t, systemMsg.Content, "Focus on investigation") // synthesis has its own focus in custom instructions
	require.NotContains(t, systemMsg.Content, "Action Input:")

	// User message: synthesis-specific structure
	require.Equal(t, agent.RoleUser, userMsg.Role)
	require.Contains(t, userMsg.Content, "Synthesize")
	require.Contains(t, userMsg.Content, "Alert Details")
	require.Contains(t, userMsg.Content, "Runbook Content")
	require.Contains(t, userMsg.Content, "Synthesis Runbook")
	require.Contains(t, userMsg.Content, "Previous Stage Data")
	require.Contains(t, userMsg.Content, "Agent 1: Pods show high memory.")
	require.Contains(t, userMsg.Content, "Agent 2: Logs show OOMKilled.")

	// Synthesis should NOT pass tools
	require.Nil(t, llm.lastInput.Tools)
}

func TestSynthesisController_WithGrounding(t *testing.T) {
	// Synthesis response includes grounding — should create google_search_result event
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "The analysis shows OOM is the root cause."},
				&agent.GroundingChunk{
					WebSearchQueries: []string{"kubernetes OOM troubleshooting"},
					Sources:          []agent.GroundingSource{{URI: "https://k8s.io/docs/oom", Title: "K8s OOM"}},
				},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesisNativeThinking
	ctrl := NewSynthesisController()

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 found OOM.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundSearch := false
	for _, ev := range events {
		if ev.EventType == "google_search_result" {
			foundSearch = true
			require.Contains(t, ev.Content, "kubernetes OOM troubleshooting")
			break
		}
	}
	require.True(t, foundSearch, "google_search_result event should be created in synthesis")
}

func TestSynthesisController_WithCodeExecution(t *testing.T) {
	// Synthesis response includes code execution — should create code_execution event
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "After computing metrics."},
				&agent.CodeExecutionChunk{Code: "avg = sum(values) / len(values)", Result: ""},
				&agent.CodeExecutionChunk{Code: "", Result: "42.5"},
			}},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategySynthesisNativeThinking
	ctrl := NewSynthesisController()

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 collected metrics.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	foundCodeExec := false
	for _, ev := range events {
		if ev.EventType == "code_execution" {
			foundCodeExec = true
			require.Contains(t, ev.Content, "avg = sum(values) / len(values)")
			require.Contains(t, ev.Content, "42.5")
			break
		}
	}
	require.True(t, foundCodeExec, "code_execution event should be created in synthesis")
}
