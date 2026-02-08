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
