// Package queue provides session queue management and processing infrastructure.
package queue

import (
	"context"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
)

// StubExecutor is a test/placeholder SessionExecutor that returns "completed" immediately.
// The real implementation is RealSessionExecutor in executor.go.
type StubExecutor struct{}

// NewStubExecutor creates a new stub executor.
func NewStubExecutor() *StubExecutor {
	return &StubExecutor{}
}

// Execute returns a completed result immediately (no-op).
func (e *StubExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
	sessionID := ""
	chainID := ""
	alertType := ""
	if session != nil {
		sessionID = session.ID
		chainID = session.ChainID
		alertType = session.AlertType
	}
	slog.Info("Stub executor: session processing (no-op)",
		"session_id", sessionID,
		"chain_id", chainID,
		"alert_type", alertType,
	)

	// Check if context is already cancelled
	if ctx.Err() != nil {
		return &ExecutionResult{
			Status: alertsession.StatusCancelled,
			Error:  ctx.Err(),
		}
	}

	return &ExecutionResult{
		Status:           alertsession.StatusCompleted,
		FinalAnalysis:    "Stub executor: no agent execution performed",
		ExecutiveSummary: "Stub execution completed successfully",
	}
}
