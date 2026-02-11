package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

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

	t.Run("nil resp is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 100}
		accumulateUsage(total, nil)
		assert.Equal(t, 100, total.InputTokens)
	})
}

// ============================================================================
// accumulateTokenUsage tests
// ============================================================================

func TestAccumulateTokenUsage(t *testing.T) {
	t.Run("adds usage to total", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
		usage := &agent.TokenUsage{InputTokens: 20, OutputTokens: 30, TotalTokens: 50, ThinkingTokens: 8}

		accumulateTokenUsage(total, usage)
		assert.Equal(t, 30, total.InputTokens)
		assert.Equal(t, 35, total.OutputTokens)
		assert.Equal(t, 65, total.TotalTokens)
		assert.Equal(t, 8, total.ThinkingTokens)
	})

	t.Run("nil usage is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 42}
		accumulateTokenUsage(total, nil)
		assert.Equal(t, 42, total.InputTokens)
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
		{
			name: "nil error returns false",
			err:  nil,
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

	t.Run("nil resp returns zero", func(t *testing.T) {
		usage := tokenUsageFromResp(nil)
		assert.Equal(t, 0, usage.TotalTokens)
	})
}
