package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

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
		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()
		time.Sleep(time.Millisecond)

		status, done := StatusFromContextErr(ctx)
		assert.True(t, done)
		assert.Equal(t, ExecutionStatusTimedOut, status)
	})
}
