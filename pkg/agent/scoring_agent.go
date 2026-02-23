package agent

import (
	"context"
	"errors"
	"fmt"
)

// ScoringAgent evaluates session quality by delegating to a Controller.
// Unlike BaseAgent, it does not call UpdateAgentExecutionStatus because
// the scoring lifecycle is managed externally by ScoringService via the
// session_scores table.
type ScoringAgent struct {
	controller Controller
}

// NewScoringAgent creates a scoring agent with the given iteration controller.
// Panics if controller is nil (programming error in the factory).
func NewScoringAgent(controller Controller) *ScoringAgent {
	if controller == nil {
		panic("NewScoringAgent: controller must not be nil")
	}
	return &ScoringAgent{controller: controller}
}

// Execute runs the scoring evaluation by delegating to the controller.
//
// All outcomes are returned as (*ExecutionResult, nil); no path returns (nil, error).
// Errors from the controller are mapped to ExecutionResult.Status values directly.
func (a *ScoringAgent) Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error) {
	result, err := a.controller.Run(ctx, execCtx, prevStageContext)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return &ExecutionResult{Status: ExecutionStatusTimedOut, Error: err}, nil
		}
		if errors.Is(err, context.Canceled) {
			return &ExecutionResult{Status: ExecutionStatusCancelled, Error: err}, nil
		}
		return &ExecutionResult{Status: ExecutionStatusFailed, Error: err}, nil
	}

	if result == nil {
		return &ExecutionResult{
			Status: ExecutionStatusFailed,
			Error:  fmt.Errorf("controller returned nil result"),
		}, nil
	}

	return result, nil
}
