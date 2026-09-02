// Package agent provides the core agent framework for TARSy.
// Agents investigate alerts using LLM calls and (optionally) MCP tools.
package agent

import (
	"context"
	"errors"
)

// Agent defines the interface for all TARSy agents.
// Agents are created per-execution (not shared between sessions).
type Agent interface {
	// Execute runs the agent's investigation.
	// ctx carries the session timeout and cancellation signal.
	// execCtx provides all execution dependencies and state.
	// prevStageContext is the output from the previous stage (empty for first stage).
	//
	// Returns (*ExecutionResult, nil) on completion — check Result.Status and
	// Result.Error for agent-level failures (e.g., LLM errors, tool failures).
	// Returns (nil, error) only for infrastructure failures where no meaningful
	// result exists (e.g., cannot mark execution as active in DB).
	Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}

// ExecutionStatus represents the status of an agent execution.
type ExecutionStatus string

const (
	ExecutionStatusPending   ExecutionStatus = "pending"
	ExecutionStatusActive    ExecutionStatus = "active"
	ExecutionStatusCompleted ExecutionStatus = "completed"
	ExecutionStatusFailed    ExecutionStatus = "failed"
	ExecutionStatusTimedOut  ExecutionStatus = "timed_out"
	ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

// StatusFromContextErr maps a context error to the appropriate ExecutionStatus.
// Returns ("", false) if the context is still active.
func StatusFromContextErr(ctx context.Context) (ExecutionStatus, bool) {
	if ctx.Err() == nil {
		return "", false
	}
	return StatusFromErr(ctx.Err()), true
}

// StatusFromErr maps an error to the appropriate ExecutionStatus.
// Returns TimedOut for DeadlineExceeded, Cancelled for context.Canceled,
// and Failed for everything else (including nil).
func StatusFromErr(err error) ExecutionStatus {
	if errors.Is(err, context.DeadlineExceeded) {
		return ExecutionStatusTimedOut
	}
	if errors.Is(err, context.Canceled) {
		return ExecutionStatusCancelled
	}
	return ExecutionStatusFailed
}

// WrapUpReason explains why an iterating agent force-concluded.
// Empty when the agent finished normally (text with no tool calls).
type WrapUpReason string

const (
	WrapUpReasonMaxIterations WrapUpReason = "max_iterations"
	WrapUpReasonTimeBudget    WrapUpReason = "time_budget"
)

// Display returns the human-readable phrase for formatters, or "".
func (r WrapUpReason) Display() string {
	switch r {
	case WrapUpReasonTimeBudget:
		return "time budget"
	case WrapUpReasonMaxIterations:
		return "max iterations"
	default:
		return ""
	}
}

// ExecutionResult is returned by Agent.Execute().
// Lightweight — all intermediate state was already written to DB during execution.
type ExecutionResult struct {
	Status        ExecutionStatus
	FinalAnalysis string
	Error         error
	TokensUsed    TokenUsage
	WrapUpReason  WrapUpReason // set when force-concluding; empty on a normal finish
}

// KeepCompletedWrapUp reports whether a successful wrap-up should keep status
// completed when the parent context has since hit its deadline (not cancel).
func (r *ExecutionResult) KeepCompletedWrapUp(ctxErr error) bool {
	if r == nil {
		return false
	}
	return r.Status == ExecutionStatusCompleted &&
		r.WrapUpReason != "" &&
		r.FinalAnalysis != "" &&
		errors.Is(ctxErr, context.DeadlineExceeded)
}

// TokenUsage aggregates token consumption across multiple LLM calls.
type TokenUsage struct {
	InputTokens         int
	OutputTokens        int
	TotalTokens         int
	ThinkingTokens      int
	CacheReadTokens     int
	CacheCreationTokens int
}
