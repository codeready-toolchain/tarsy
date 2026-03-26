package controller

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendLangChain
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

	prevContext := "Agent 1: Pods show high memory.\nAgent 2: Logs show OOMKilled."
	result, err := ctrl.Run(context.Background(), execCtx, prevContext)
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "OOM on web-1")
	require.Equal(t, 150, result.TokensUsed.TotalTokens)
	require.Equal(t, 1, llm.callCount)

	events, err := execCtx.Services.Timeline.GetAgentTimeline(context.Background(), execCtx.ExecutionID)
	require.NoError(t, err)
	found := false
	for _, ev := range events {
		if ev.EventType == timelineevent.EventTypeFinalAnalysis {
			found = true
			assert.Equal(t, "synthesis", ev.Metadata["interaction_type"],
				"final_analysis event should carry the controller's interaction_type")
			break
		}
	}
	require.True(t, found, "final_analysis event must exist")
}

func TestSynthesisController_WithThinking(t *testing.T) {
	// synthesis with google-native backend may produce thinking content
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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendLangChain
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendLangChain
	execCtx.Config.CustomInstructions = "Custom synthesis instructions."
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

	prevContext := "Agent 1: Pods show high memory.\nAgent 2: Logs show OOMKilled."
	result, err := ctrl.Run(context.Background(), execCtx, prevContext)
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)

	require.NotNil(t, llm.lastInput)
	require.GreaterOrEqual(t, len(llm.lastInput.Messages), 2)

	systemMsg := llm.lastInput.Messages[0]
	userMsg := llm.lastInput.Messages[1]

	// System message: SRE instructions + custom instructions (no taskFocus)
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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

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
	execCtx.Config.Type = config.AgentTypeSynthesis
	execCtx.Config.LLMBackend = config.LLMBackendNativeGemini
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

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

func TestSingleShotController_FallbackOnError(t *testing.T) {
	// First call fails with max_retries → fallback → second call succeeds
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "retries exhausted", Code: "max_retries", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesis via fallback."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		{
			ProviderName: "fallback",
			Backend:      config.LLMBackendNativeGemini,
			Config:       &config.LLMProviderConfig{Model: "fallback-model"},
		},
	}

	ctrl := NewSynthesisController(execCtx.PromptBuilder)
	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Synthesis via fallback.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount)

	// Verify fallback provider was used in second call
	require.Equal(t, "fallback-model", llm.capturedInputs[1].Config.Model)
	require.True(t, llm.capturedInputs[1].ClearCache)
}

func TestSingleShotController_FallbackOnProviderError(t *testing.T) {
	// provider_error (e.g. 404 model not found) should trigger fallback on the
	// first failure in single-shot mode. Before the SingleShot fix, the
	// threshold of 2 consecutive errors was unreachable and synthesis would
	// fail without trying fallback providers.
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "model not found", Code: "provider_error", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesis via fallback after provider_error."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		{
			ProviderName: "fallback",
			Backend:      config.LLMBackendNativeGemini,
			Config:       &config.LLMProviderConfig{Model: "fallback-model"},
		},
	}

	ctrl := NewSynthesisController(execCtx.PromptBuilder)
	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Synthesis via fallback after provider_error.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount)

	require.Equal(t, "fallback-model", llm.capturedInputs[1].Config.Model)
	require.True(t, llm.capturedInputs[1].ClearCache)
}

func TestSingleShotController_FallbackOnInvalidRequest(t *testing.T) {
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "invalid request", Code: "invalid_request", Retryable: false},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesis via fallback after invalid_request."},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.LLMProviderName = "primary"
	execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
		{
			ProviderName: "fallback",
			Backend:      config.LLMBackendNativeGemini,
			Config:       &config.LLMProviderConfig{Model: "fallback-model"},
		},
	}

	ctrl := NewSynthesisController(execCtx.PromptBuilder)
	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Synthesis via fallback after invalid_request.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount)
}

func TestSingleShotController_NoFallback_ReturnsError(t *testing.T) {
	// No fallback providers → error returned directly
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ErrorChunk{Message: "retries exhausted", Code: "max_retries", Retryable: false},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})

	ctrl := NewSynthesisController(execCtx.PromptBuilder)
	_, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.Error(t, err)
	require.Contains(t, err.Error(), "synthesis LLM call failed")
}

func TestExecSummaryController_HappyPath(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Pod OOM killed: memory leak in web-1."},
				&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	execCtx.Config.Type = config.AgentTypeExecSummary
	ctrl := NewExecSummaryController(execCtx.PromptBuilder)

	finalAnalysis := "Root cause: memory leak in web-1. Pods OOMKilled repeatedly."
	result, err := ctrl.Run(context.Background(), execCtx, finalAnalysis)
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "OOM killed")
	require.Equal(t, 100, result.TokensUsed.TotalTokens)
	require.Equal(t, 1, llm.callCount)
}

func TestExecSummaryController_NoThinkingFallback(t *testing.T) {
	// Exec summary must NOT use thinking text as fallback when resp.Text is empty.
	// After maxEmptyResponseRetries the empty result is accepted.
	responses := make([]mockLLMResponse, maxEmptyResponseRetries+1)
	for i := range responses {
		responses[i] = mockLLMResponse{chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me think about this..."},
		}}
	}

	llm := &mockLLMClient{responses: responses}
	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	ctrl := NewExecSummaryController(execCtx.PromptBuilder)

	result, err := ctrl.Run(context.Background(), execCtx, "some final analysis")
	require.NoError(t, err)
	// ThinkingFallback is false → FinalAnalysis is empty, not the thinking text
	require.Empty(t, result.FinalAnalysis)
	require.Equal(t, maxEmptyResponseRetries+1, llm.callCount, "should exhaust empty retries")
}

func TestSingleShotController_EmptyResponseRetry(t *testing.T) {
	// First call returns empty, second succeeds.
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.UsageChunk{InputTokens: 5, OutputTokens: 0, TotalTokens: 5},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Here is the synthesis."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 15, TotalTokens: 25},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Here is the synthesis.", result.FinalAnalysis)
	require.Equal(t, 2, llm.callCount, "should retry after empty response")

	// Tokens from both attempts must be accumulated (5 + 25)
	assert.Equal(t, 30, result.TokensUsed.TotalTokens)

	lastMessages := llm.capturedInputs[1].Messages
	lastUserMsg := lastMessages[len(lastMessages)-1]
	assert.Equal(t, agent.RoleUser, lastUserMsg.Role)
	assert.Contains(t, lastUserMsg.Content, "empty")
}

func TestSingleShotController_EmptyResponseSkipsRetryWithThinkingFallback(t *testing.T) {
	// Synthesis has ThinkingFallback=true. If text is empty but thinking is
	// present, no retry needed — thinking serves as the final analysis.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.ThinkingChunk{Content: "Deep analysis here."},
				&agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
			}},
		},
	}

	execCtx := newTestExecCtx(t, llm, &mockToolExecutor{})
	ctrl := NewSynthesisController(execCtx.PromptBuilder)

	result, err := ctrl.Run(context.Background(), execCtx, "Agent 1 analysis text.")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Deep analysis here.", result.FinalAnalysis)
	require.Equal(t, 1, llm.callCount, "should NOT retry — thinking fallback has content")
}
