package controller

import (
	"context"
	"errors"
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
// accumulateUsage tests
// ============================================================================

func TestAccumulateUsage(t *testing.T) {
	t.Run("accumulates from response with usage", func(t *testing.T) {
		total := &agent.TokenUsage{}
		resp := &LLMResponse{Usage: &agent.TokenUsage{
			InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ThinkingTokens: 5,
		}}

		accumulateUsage(total, resp)
		assert.Equal(t, 10, total.InputTokens)
		assert.Equal(t, 20, total.OutputTokens)
		assert.Equal(t, 30, total.TotalTokens)
		assert.Equal(t, 5, total.ThinkingTokens)

		// Accumulate again
		accumulateUsage(total, resp)
		assert.Equal(t, 20, total.InputTokens)
		assert.Equal(t, 60, total.TotalTokens)
	})

	t.Run("nil usage is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 100}
		resp := &LLMResponse{Usage: nil}

		accumulateUsage(total, resp)
		assert.Equal(t, 100, total.InputTokens)
	})
}

// ============================================================================
// isTimeoutError tests
// ============================================================================

func TestIsTimeoutError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped DeadlineExceeded",
			err:  fmt.Errorf("operation failed: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "timeout in message",
			err:  errors.New("request timeout after 30s"),
			want: true,
		},
		{
			name: "timed out in message",
			err:  errors.New("connection timed out"),
			want: true,
		},
		{
			name: "TIMEOUT uppercase in message",
			err:  errors.New("TIMEOUT occurred"),
			want: true,
		},
		{
			name: "regular error",
			err:  errors.New("connection refused"),
			want: false,
		},
		{
			name: "context.Canceled is not timeout",
			err:  context.Canceled,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTimeoutError(tt.err))
		})
	}
}

// ============================================================================
// buildToolNameSet tests
// ============================================================================

func TestBuildToolNameSet(t *testing.T) {
	t.Run("builds set from tools", func(t *testing.T) {
		tools := []agent.ToolDefinition{
			{Name: "k8s.get_pods"},
			{Name: "k8s.get_logs"},
			{Name: "prom.query"},
		}
		set := buildToolNameSet(tools)
		assert.True(t, set["k8s.get_pods"])
		assert.True(t, set["k8s.get_logs"])
		assert.True(t, set["prom.query"])
		assert.False(t, set["nonexistent"])
	})

	t.Run("empty tools returns empty set", func(t *testing.T) {
		set := buildToolNameSet(nil)
		assert.Empty(t, set)
	})
}

// ============================================================================
// tokenUsageFromResp tests
// ============================================================================

func TestTokenUsageFromResp(t *testing.T) {
	t.Run("with usage", func(t *testing.T) {
		resp := &LLMResponse{Usage: &agent.TokenUsage{
			InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
		}}
		usage := tokenUsageFromResp(resp)
		assert.Equal(t, 30, usage.TotalTokens)
	})

	t.Run("nil usage returns zero", func(t *testing.T) {
		resp := &LLMResponse{}
		usage := tokenUsageFromResp(resp)
		assert.Equal(t, 0, usage.TotalTokens)
	})
}

// ============================================================================
// buildForcedConclusionPrompt tests
// ============================================================================

func TestBuildForcedConclusionPrompt(t *testing.T) {
	prompt := buildForcedConclusionPrompt(5)
	assert.Contains(t, prompt, "5")
	assert.Contains(t, prompt, "maximum number of iterations")
	assert.Contains(t, prompt, "final analysis")
}
