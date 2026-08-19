package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestStatusFromErr(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ExecutionStatus
	}{
		{"deadline exceeded", context.DeadlineExceeded, ExecutionStatusTimedOut},
		{"context canceled", context.Canceled, ExecutionStatusCancelled},
		{"generic error", fmt.Errorf("something broke"), ExecutionStatusFailed},
		{"nil error", nil, ExecutionStatusFailed},
		{"wrapped deadline", fmt.Errorf("call failed: %w", context.DeadlineExceeded), ExecutionStatusTimedOut},
		{"wrapped canceled", fmt.Errorf("call failed: %w", context.Canceled), ExecutionStatusCancelled},
		{"double wrapped deadline", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", context.DeadlineExceeded)), ExecutionStatusTimedOut},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, StatusFromErr(tt.err))
		})
	}
}

func TestStatusFromContextErr(t *testing.T) {
	t.Run("active context", func(t *testing.T) {
		status, done := StatusFromContextErr(context.Background())
		assert.False(t, done)
		assert.Equal(t, ExecutionStatus(""), status)
	})

	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		status, done := StatusFromContextErr(ctx)
		assert.True(t, done)
		assert.Equal(t, ExecutionStatusCancelled, status)
	})

	t.Run("timed out context", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		status, done := StatusFromContextErr(ctx)
		assert.True(t, done)
		assert.Equal(t, ExecutionStatusTimedOut, status)
	})
}

func TestResolvedAgentConfig_ModelName(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		var cfg *ResolvedAgentConfig
		assert.Equal(t, "", cfg.ModelName())
	})

	t.Run("nil provider", func(t *testing.T) {
		cfg := &ResolvedAgentConfig{}
		assert.Equal(t, "", cfg.ModelName())
	})

	t.Run("returns configured model", func(t *testing.T) {
		cfg := &ResolvedAgentConfig{
			LLMProvider: &config.LLMProviderConfig{Model: "gemini-3.7-flash"},
		}
		assert.Equal(t, "gemini-3.7-flash", cfg.ModelName())
	})
}
