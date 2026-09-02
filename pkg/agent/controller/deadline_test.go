package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapUpReserve(t *testing.T) {
	tests := []struct {
		name     string
		llmCall  time.Duration
		expected time.Duration
	}{
		{name: "caps at 3m when LLM timeout is larger", llmCall: 5 * time.Minute, expected: 3 * time.Minute},
		{name: "follows LLM timeout when smaller than 3m", llmCall: 200 * time.Millisecond, expected: 200 * time.Millisecond},
		{name: "equals cap at 3m", llmCall: 3 * time.Minute, expected: 3 * time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, wrapUpReserve(tt.llmCall))
		})
	}
}

func TestShouldWrapUpOnTimeouts(t *testing.T) {
	iter := 6 * time.Minute
	reserve := 3 * time.Minute

	assert.False(t, shouldWrapUpOnTimeouts(40*time.Minute, reserve, iter), "plenty of session time stays failed")
	assert.False(t, shouldWrapUpOnTimeouts(9*time.Minute, reserve, iter), "remaining-reserve equal to iteration timeout stays failed")
	assert.True(t, shouldWrapUpOnTimeouts(4*time.Minute, reserve, iter), "next iteration would be clamped")
	assert.True(t, shouldWrapUpOnTimeouts(5*time.Second, 200*time.Millisecond, iter), "short remaining after child timeouts")
}

func TestWaitErrorAction(t *testing.T) {
	usage := agent.TokenUsage{TotalTokens: 10}

	t.Run("parent cancel is fail-fast", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		wrap, term := waitErrorAction(ctx, context.DeadlineExceeded, usage)
		assert.False(t, wrap)
		require.NotNil(t, term)
		assert.Equal(t, agent.ExecutionStatusCancelled, term.Status)
		assert.ErrorIs(t, term.Error, context.Canceled)
	})

	t.Run("parent already expired is timed_out", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()
		wrap, term := waitErrorAction(ctx, context.DeadlineExceeded, usage)
		assert.False(t, wrap)
		require.NotNil(t, term)
		assert.Equal(t, agent.ExecutionStatusTimedOut, term.Status)
		assert.ErrorIs(t, term.Error, context.DeadlineExceeded)
	})

	t.Run("wait clamp while parent has time wraps up", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		wrap, term := waitErrorAction(ctx, context.DeadlineExceeded, usage)
		assert.True(t, wrap)
		assert.Nil(t, term)
	})

	t.Run("no parent deadline does not wrap", func(t *testing.T) {
		wrap, term := waitErrorAction(t.Context(), context.DeadlineExceeded, usage)
		assert.False(t, wrap)
		assert.Nil(t, term)
	})

	t.Run("unrelated wait error does not wrap", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		wrap, term := waitErrorAction(ctx, errors.New("collector closed"), usage)
		assert.False(t, wrap)
		assert.Nil(t, term)
	})
}
