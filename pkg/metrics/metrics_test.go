package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestObserveLLMCall(t *testing.T) {
	t.Run("success with tokens", func(t *testing.T) {
		before := testutil.ToFloat64(LLMCallsTotal.WithLabelValues("anthropic", "claude-4"))

		ObserveLLMCall("anthropic", "claude-4", 5*time.Second, &LLMTokens{
			Input: 100, Output: 200, Thinking: 50,
		}, nil)

		assert.Equal(t, before+1, testutil.ToFloat64(LLMCallsTotal.WithLabelValues("anthropic", "claude-4")))
		assert.InDelta(t, 100, testutil.ToFloat64(LLMTokensTotal.WithLabelValues("anthropic", "claude-4", "input")), 200)
		assert.InDelta(t, 200, testutil.ToFloat64(LLMTokensTotal.WithLabelValues("anthropic", "claude-4", "output")), 200)
		assert.InDelta(t, 50, testutil.ToFloat64(LLMTokensTotal.WithLabelValues("anthropic", "claude-4", "thinking")), 200)
	})

	t.Run("error records error counter", func(t *testing.T) {
		before := testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("openai", "gpt-4", "error"))

		ObserveLLMCall("openai", "gpt-4", time.Second, nil, fmt.Errorf("rate limited"))

		assert.Equal(t, before+1, testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("openai", "gpt-4", "error")))
	})

	t.Run("timeout error classified correctly", func(t *testing.T) {
		before := testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("google", "gemini", "timeout"))

		ObserveLLMCall("google", "gemini", 30*time.Second, nil, context.DeadlineExceeded)

		assert.Equal(t, before+1, testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("google", "gemini", "timeout")))
	})

	t.Run("canceled error classified correctly", func(t *testing.T) {
		before := testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("google", "gemini-2", "canceled"))

		ObserveLLMCall("google", "gemini-2", time.Second, nil, context.Canceled)

		assert.Equal(t, before+1, testutil.ToFloat64(LLMErrorsTotal.WithLabelValues("google", "gemini-2", "canceled")))
	})

	t.Run("nil tokens is safe", func(t *testing.T) {
		before := testutil.ToFloat64(LLMCallsTotal.WithLabelValues("test-nil", "model"))

		ObserveLLMCall("test-nil", "model", time.Second, nil, nil)

		assert.Equal(t, before+1, testutil.ToFloat64(LLMCallsTotal.WithLabelValues("test-nil", "model")))
	})

	t.Run("zero thinking tokens skips thinking counter", func(t *testing.T) {
		before := testutil.ToFloat64(LLMTokensTotal.WithLabelValues("test-no-think", "m", "thinking"))

		ObserveLLMCall("test-no-think", "m", time.Second, &LLMTokens{
			Input: 10, Output: 20, Thinking: 0,
		}, nil)

		assert.Equal(t, before, testutil.ToFloat64(LLMTokensTotal.WithLabelValues("test-no-think", "m", "thinking")))
	})
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"deadline exceeded", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"wrapped deadline", fmt.Errorf("call failed: %w", context.DeadlineExceeded), "timeout"},
		{"wrapped canceled", fmt.Errorf("call failed: %w", context.Canceled), "canceled"},
		{"generic error", fmt.Errorf("something broke"), "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, errorCode(tt.err))
		})
	}
}
