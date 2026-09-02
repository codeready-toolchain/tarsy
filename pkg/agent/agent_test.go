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

func TestWrapUpReasonDisplay(t *testing.T) {
	assert.Equal(t, "time budget", WrapUpReasonTimeBudget.Display())
	assert.Equal(t, "max iterations", WrapUpReasonMaxIterations.Display())
	assert.Equal(t, "", WrapUpReason("").Display())
	assert.Equal(t, "", WrapUpReason("unknown").Display())
}

func TestKeepCompletedWrapUp(t *testing.T) {
	completed := &ExecutionResult{
		Status:        ExecutionStatusCompleted,
		FinalAnalysis: "report",
		WrapUpReason:  WrapUpReasonTimeBudget,
	}
	tests := []struct {
		name   string
		result *ExecutionResult
		ctxErr error
		want   bool
	}{
		{name: "deadline after successful wrap-up", result: completed, ctxErr: context.DeadlineExceeded, want: true},
		{name: "wrapped deadline after successful wrap-up", result: completed, ctxErr: fmt.Errorf("session: %w", context.DeadlineExceeded), want: true},
		{name: "max-iterations wrap-up", result: &ExecutionResult{
			Status: ExecutionStatusCompleted, FinalAnalysis: "report", WrapUpReason: WrapUpReasonMaxIterations,
		}, ctxErr: context.DeadlineExceeded, want: true},
		{name: "cancel does not preserve wrap-up", result: completed, ctxErr: context.Canceled, want: false},
		{name: "empty analysis", result: &ExecutionResult{
			Status: ExecutionStatusCompleted, WrapUpReason: WrapUpReasonTimeBudget,
		}, ctxErr: context.DeadlineExceeded, want: false},
		{name: "normal completed has no wrap-up reason", result: &ExecutionResult{
			Status: ExecutionStatusCompleted, FinalAnalysis: "report",
		}, ctxErr: context.DeadlineExceeded, want: false},
		{name: "failed wrap-up", result: &ExecutionResult{
			Status: ExecutionStatusFailed, FinalAnalysis: "report", WrapUpReason: WrapUpReasonTimeBudget,
		}, ctxErr: context.DeadlineExceeded, want: false},
		{name: "nil result", result: nil, ctxErr: context.DeadlineExceeded, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.result.KeepCompletedWrapUp(tt.ctxErr))
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
