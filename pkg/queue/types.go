// Package queue provides session queue management and processing infrastructure.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
)

// Sentinel errors for queue operations.
var (
	// ErrNoSessionsAvailable indicates no pending sessions are in the queue.
	ErrNoSessionsAvailable = errors.New("no sessions available")

	// ErrAtCapacity indicates the global concurrent session limit has been reached.
	ErrAtCapacity = errors.New("at capacity")
)

// SessionExecutor is the interface for session processing.
//
// The executor owns the ENTIRE session lifecycle internally:
//   - Executes all stages sequentially (from chain config)
//   - If a stage fails, the session stops immediately
//   - Always forces conclusion at max iterations (no pause/resume)
//
// The executor writes results PROGRESSIVELY during execution, not at the end.
// The worker only handles: claiming, heartbeat, terminal status update, and event cleanup.
type SessionExecutor interface {
	Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult
}

// ExecutionResult is lightweight — just the terminal state.
// All intermediate state (TimelineEvents, Interactions, Stages) was already
// written to DB by the executor during processing.
type ExecutionResult struct {
	Status           alertsession.Status // completed, failed, timed_out, cancelled
	FinalAnalysis    string              // Final analysis text (if completed)
	ExecutiveSummary string              // Executive summary (if completed)
	Error            error               // Error details (if failed/timed_out)
}

// PoolHealth contains health information for the entire worker pool.
type PoolHealth struct {
	IsHealthy        bool           `json:"is_healthy"`
	DBReachable      bool           `json:"db_reachable"`
	DBError          string         `json:"db_error,omitempty"`
	PodID            string         `json:"pod_id"`
	ActiveWorkers    int            `json:"active_workers"`
	TotalWorkers     int            `json:"total_workers"`
	ActiveSessions   int            `json:"active_sessions"`
	MaxConcurrent    int            `json:"max_concurrent"`
	QueueDepth       int            `json:"queue_depth"`
	WorkerStats      []WorkerHealth `json:"worker_stats"`
	LastOrphanScan   time.Time      `json:"last_orphan_scan"`
	OrphansRecovered int            `json:"orphans_recovered"`
}

// WorkerHealth contains health information for a single worker.
type WorkerHealth struct {
	ID                string    `json:"id"`
	Status            string    `json:"status"` // "idle" or "working"
	CurrentSessionID  string    `json:"current_session_id,omitempty"`
	SessionsProcessed int       `json:"sessions_processed"`
	LastActivity      time.Time `json:"last_activity"`
}
