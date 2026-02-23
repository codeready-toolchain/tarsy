package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockedController lets tests control the result and error returned by Run.
type mockedController struct {
	result *ExecutionResult
	err    error
}

func (c *mockedController) Run(_ context.Context, _ *ExecutionContext, _ string) (*ExecutionResult, error) {
	return c.result, c.err
}

func TestScoringAgent_Execute(t *testing.T) {
	t.Run("timeout mapping", func(t *testing.T) {
		agent := NewScoringAgent(&mockedController{
			err: context.DeadlineExceeded,
		})

		result, err := agent.Execute(context.Background(), &ExecutionContext{}, "")
		require.NoError(t, err)
		assert.Equal(t, ExecutionStatusTimedOut, result.Status)
		assert.ErrorIs(t, result.Error, context.DeadlineExceeded)
	})

	t.Run("cancellation mapping", func(t *testing.T) {
		agent := NewScoringAgent(&mockedController{
			err: context.Canceled,
		})

		result, err := agent.Execute(context.Background(), &ExecutionContext{}, "")
		require.NoError(t, err)
		assert.Equal(t, ExecutionStatusCancelled, result.Status)
		assert.ErrorIs(t, result.Error, context.Canceled)
	})

	t.Run("generic error mapping", func(t *testing.T) {
		agent := NewScoringAgent(&mockedController{
			err: errors.New("llm call failed"),
		})

		result, err := agent.Execute(context.Background(), &ExecutionContext{}, "")
		require.NoError(t, err)
		assert.Equal(t, ExecutionStatusFailed, result.Status)
		assert.Contains(t, result.Error.Error(), "llm call failed")
	})

	t.Run("nil result from controller", func(t *testing.T) {
		agent := NewScoringAgent(&mockedController{
			result: nil,
			err:    nil,
		})

		result, err := agent.Execute(context.Background(), &ExecutionContext{}, "")
		require.NoError(t, err)
		assert.Equal(t, ExecutionStatusFailed, result.Status)
		assert.Contains(t, result.Error.Error(), "controller returned nil result")
	})

	t.Run("successful execution", func(t *testing.T) {
		expected := &ExecutionResult{
			Status:        ExecutionStatusCompleted,
			FinalAnalysis: "score: 85/100",
		}
		agent := NewScoringAgent(&mockedController{
			result: expected,
		})

		result, err := agent.Execute(context.Background(), &ExecutionContext{}, "")
		require.NoError(t, err)
		assert.Equal(t, expected, result)
	})

	t.Run("does not access stage service", func(t *testing.T) {
		// ExecutionContext with nil Services proves ScoringAgent never calls
		// UpdateAgentExecutionStatus — a nil-pointer dereference would panic.
		agent := NewScoringAgent(&mockedController{
			result: &ExecutionResult{Status: ExecutionStatusCompleted},
		})

		execCtx := &ExecutionContext{Services: nil}
		result, err := agent.Execute(context.Background(), execCtx, "")
		require.NoError(t, err)
		assert.Equal(t, ExecutionStatusCompleted, result.Status)
	})
}

func TestNewScoringAgent_NilControllerPanics(t *testing.T) {
	assert.PanicsWithValue(t, "NewScoringAgent: controller must not be nil", func() {
		NewScoringAgent(nil)
	})
}
