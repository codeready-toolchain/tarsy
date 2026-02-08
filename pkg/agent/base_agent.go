package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
)

// Controller defines the iteration strategy interface.
// Each controller implements a different investigation pattern.
type Controller interface {
	Run(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}

// BaseAgent provides the common agent implementation.
// It delegates iteration logic to a controller (strategy pattern).
type BaseAgent struct {
	controller Controller
}

// NewBaseAgent creates an agent with the given iteration controller.
// Panics if controller is nil (programming error in the factory).
func NewBaseAgent(controller Controller) *BaseAgent {
	if controller == nil {
		panic("NewBaseAgent: controller must not be nil")
	}
	return &BaseAgent{controller: controller}
}

// Execute runs the agent's investigation by delegating to the controller.
func (a *BaseAgent) Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error) {
	// 1. Mark agent execution as active
	if err := execCtx.Services.Stage.UpdateAgentExecutionStatus(
		ctx, execCtx.ExecutionID, agentexecution.StatusActive, "",
	); err != nil {
		return nil, fmt.Errorf("failed to mark execution active: %w", err)
	}

	// 2. Delegate to iteration controller
	result, err := a.controller.Run(ctx, execCtx, prevStageContext)

	// 3. Handle context cancellation/timeout.
	// Use errors.Is on the returned error (not ctx.Err()) so that a concurrent
	// context expiration doesn't misclassify an unrelated failure as timed-out.
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &ExecutionResult{Status: ExecutionStatusTimedOut, Error: err}, nil
		}
		if errors.Is(err, context.Canceled) {
			return &ExecutionResult{Status: ExecutionStatusCancelled, Error: err}, nil
		}
		return &ExecutionResult{Status: ExecutionStatusFailed, Error: err}, nil
	}

	// 4. Defensive nil-check: ensure controller returned a valid result.
	// A nil result without an error indicates a programming bug in the controller.
	if result == nil {
		return &ExecutionResult{
			Status: ExecutionStatusFailed,
			Error:  fmt.Errorf("controller returned nil result"),
		}, nil
	}

	return result, nil
}
