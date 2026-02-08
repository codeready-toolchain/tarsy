package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIterationState_ShouldAbortOnTimeouts(t *testing.T) {
	tests := []struct {
		name               string
		consecutiveTimeouts int
		want               bool
	}{
		{
			name:               "zero timeouts - no abort",
			consecutiveTimeouts: 0,
			want:               false,
		},
		{
			name:               "one timeout - no abort",
			consecutiveTimeouts: 1,
			want:               false,
		},
		{
			name:               "at threshold - abort",
			consecutiveTimeouts: MaxConsecutiveTimeouts,
			want:               true,
		},
		{
			name:               "above threshold - abort",
			consecutiveTimeouts: MaxConsecutiveTimeouts + 1,
			want:               true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &IterationState{ConsecutiveTimeoutFailures: tt.consecutiveTimeouts}
			assert.Equal(t, tt.want, state.ShouldAbortOnTimeouts())
		})
	}
}

func TestIterationState_RecordSuccess(t *testing.T) {
	state := &IterationState{
		LastInteractionFailed:      true,
		LastErrorMessage:           "some error",
		ConsecutiveTimeoutFailures: 3,
	}

	state.RecordSuccess()

	assert.False(t, state.LastInteractionFailed)
	assert.Empty(t, state.LastErrorMessage)
	assert.Equal(t, 0, state.ConsecutiveTimeoutFailures)
}

func TestIterationState_RecordFailure(t *testing.T) {
	t.Run("timeout failure increments counter", func(t *testing.T) {
		state := &IterationState{}

		state.RecordFailure("deadline exceeded", true)
		assert.True(t, state.LastInteractionFailed)
		assert.Equal(t, "deadline exceeded", state.LastErrorMessage)
		assert.Equal(t, 1, state.ConsecutiveTimeoutFailures)

		state.RecordFailure("deadline exceeded again", true)
		assert.Equal(t, 2, state.ConsecutiveTimeoutFailures)
	})

	t.Run("non-timeout failure resets counter", func(t *testing.T) {
		state := &IterationState{ConsecutiveTimeoutFailures: 3}

		state.RecordFailure("connection error", false)
		assert.True(t, state.LastInteractionFailed)
		assert.Equal(t, "connection error", state.LastErrorMessage)
		assert.Equal(t, 0, state.ConsecutiveTimeoutFailures)
	})

	t.Run("success then timeout then non-timeout", func(t *testing.T) {
		state := &IterationState{}

		state.RecordFailure("timeout 1", true)
		require.Equal(t, 1, state.ConsecutiveTimeoutFailures)

		state.RecordSuccess()
		require.Equal(t, 0, state.ConsecutiveTimeoutFailures)

		state.RecordFailure("timeout 2", true)
		require.Equal(t, 1, state.ConsecutiveTimeoutFailures)

		state.RecordFailure("regular error", false)
		assert.Equal(t, 0, state.ConsecutiveTimeoutFailures)
	})
}

func TestMaxConsecutiveTimeouts_Value(t *testing.T) {
	// Verify the constant matches the design doc value
	assert.Equal(t, 2, MaxConsecutiveTimeouts)
}
