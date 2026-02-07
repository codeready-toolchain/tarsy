// Package queue provides session queue management and processing infrastructure.
package queue

import (
	"context"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
)

// StubExecutor is a placeholder SessionExecutor for Phase 2.3.
// It immediately returns "completed" without any agent execution.
// Will be replaced by the real executor in Phase 3.
type StubExecutor struct{}

// NewStubExecutor creates a new stub executor.
func NewStubExecutor() *StubExecutor {
	return &StubExecutor{}
}

// Execute returns a completed result immediately.
// Phase 3 will replace this with real agent execution.
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
		FinalAnalysis:    "Stub executor: no agent execution performed (Phase 2.3)",
		ExecutiveSummary: "Stub execution completed successfully",
	}
}
