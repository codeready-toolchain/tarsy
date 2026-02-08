// Package agent provides the core agent framework for TARSy.
// Agents investigate alerts using LLM calls and (optionally) MCP tools.
package agent

import "context"

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

// ExecutionResult is returned by Agent.Execute().
// Lightweight — all intermediate state was already written to DB during execution.
type ExecutionResult struct {
	Status        ExecutionStatus
	FinalAnalysis string
	Error         error
	TokensUsed    TokenUsage
}

// TokenUsage aggregates token consumption across multiple LLM calls.
type TokenUsage struct {
	InputTokens    int
	OutputTokens   int
	TotalTokens    int
	ThinkingTokens int
}
