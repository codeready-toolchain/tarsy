package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
)

func TestNativeThinkingController_HappyPath(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The pods are all running. Everything is healthy.", result.FinalAnalysis)
	require.Equal(t, 70, result.TokensUsed.TotalTokens)
	require.Equal(t, 2, llm.callCount)
}

func TestNativeThinkingController_MultipleToolCalls(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "The web-1 pod has OOM issues.", result.FinalAnalysis)
}

func TestNativeThinkingController_ForcedConclusion(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Contains(t, result.FinalAnalysis, "system is healthy")
}

func TestNativeThinkingController_ThinkingContent(t *testing.T) {
	// Verify thinking content is recorded
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestNativeThinkingController_ToolExecutionError(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
}

func TestNativeThinkingController_ConsecutiveTimeouts(t *testing.T) {
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: context.DeadlineExceeded},
			{err: context.DeadlineExceeded},
		},
	}

	executor := &mockToolExecutor{tools: []agent.ToolDefinition{}}
	execCtx := newTestExecCtx(t, llm, executor)
	execCtx.Config.IterationStrategy = config.IterationStrategyNativeThinking
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
}

func TestNativeThinkingController_PrevStageContext(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

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

func TestNativeThinkingController_ForcedConclusionWithFailedLast(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusFailed, result.Status)
	require.NotNil(t, result.Error)
	require.Contains(t, result.Error.Error(), "max iterations")
}

func TestNativeThinkingController_LLMErrorRecovery(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "All systems operational.", result.FinalAnalysis)
}

func TestNativeThinkingController_TextAlongsideToolCalls(t *testing.T) {
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
	ctrl := NewNativeThinkingController()

	result, err := ctrl.Run(context.Background(), execCtx, "")
	require.NoError(t, err)
	require.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	require.Equal(t, "Everything is running fine.", result.FinalAnalysis)
}
