package queue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/event"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// WorkerStatus represents the current state of a worker.
type WorkerStatus string

// Worker status constants.
const (
	WorkerStatusIdle    WorkerStatus = "idle"
	WorkerStatusWorking WorkerStatus = "working"
)

// Worker is a single queue worker that polls for and processes sessions.
type Worker struct {
	id              string
	podID           string
	client          *ent.Client
	config          *config.QueueConfig
	sessionExecutor SessionExecutor
	pool            SessionRegistry
	stopCh          chan struct{}
	stopOnce        sync.Once
	wg              sync.WaitGroup

	// Health tracking
	mu                sync.RWMutex
	status            WorkerStatus
	currentSessionID  string
	sessionsProcessed int
	lastActivity      time.Time
}

// SessionRegistry is the subset of WorkerPool used by Worker for session registration.
type SessionRegistry interface {
	RegisterSession(sessionID string, cancel context.CancelFunc)
	UnregisterSession(sessionID string)
}

// NewWorker creates a new queue worker.
func NewWorker(id, podID string, client *ent.Client, cfg *config.QueueConfig, executor SessionExecutor, pool SessionRegistry) *Worker {
	return &Worker{
		id:              id,
		podID:           podID,
		client:          client,
		config:          cfg,
		sessionExecutor: executor,
		pool:            pool,
		stopCh:          make(chan struct{}),
		status:          WorkerStatusIdle,
		lastActivity:    time.Now(),
	}
}

// Start begins the worker polling loop in a goroutine.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.run(ctx)
}

// Stop signals the worker to stop and waits for it to finish.
// It is safe to call Stop multiple times.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

// Health returns the current worker health status.
func (w *Worker) Health() WorkerHealth {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return WorkerHealth{
		ID:                w.id,
		Status:            string(w.status),
		CurrentSessionID:  w.currentSessionID,
		SessionsProcessed: w.sessionsProcessed,
		LastActivity:      w.lastActivity,
	}
}

// run is the main worker loop.
func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()

	log := slog.With("worker_id", w.id, "pod_id", w.podID)
	log.Info("Worker started")

	for {
		select {
		case <-w.stopCh:
			log.Info("Worker shutting down")
			return
		case <-ctx.Done():
			log.Info("Context cancelled, worker shutting down")
			return
		default:
			if err := w.pollAndProcess(ctx); err != nil {
				if errors.Is(err, ErrNoSessionsAvailable) || errors.Is(err, ErrAtCapacity) {
					w.sleep(w.pollInterval())
					continue
				}
				log.Error("Error processing session", "error", err)
				w.sleep(time.Second) // Brief backoff on error
			}
		}
	}
}

// sleep waits for the given duration or until stop is signalled.
func (w *Worker) sleep(d time.Duration) {
	select {
	case <-w.stopCh:
	case <-time.After(d):
	}
}

// pollAndProcess checks capacity, claims a session, and processes it.
func (w *Worker) pollAndProcess(ctx context.Context) error {
	// 1. Check global capacity (best-effort; racy with concurrent workers but
	//    bounded by WorkerCount and mitigated by poll jitter).
	activeCount, err := w.client.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusInProgress)).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("checking active sessions: %w", err)
	}
	if activeCount >= w.config.MaxConcurrentSessions {
		return ErrAtCapacity
	}

	// 2. Claim next session
	session, err := w.claimNextSession(ctx)
	if err != nil {
		return err
	}

	log := slog.With("session_id", session.ID, "worker_id", w.id)
	log.Info("Session claimed")

	w.setStatus(WorkerStatusWorking, session.ID)
	defer w.setStatus(WorkerStatusIdle, "")

	// 3. Create session context with timeout
	sessionCtx, cancelSession := context.WithTimeout(ctx, w.config.SessionTimeout)
	defer cancelSession()

	// 4. Register cancel function for API-triggered cancellation
	w.pool.RegisterSession(session.ID, cancelSession)
	defer w.pool.UnregisterSession(session.ID)

	// 5. Start heartbeat
	heartbeatCtx, cancelHeartbeat := context.WithCancel(sessionCtx)
	defer cancelHeartbeat()
	go w.runHeartbeat(heartbeatCtx, session.ID)

	// 6. Execute session
	result := w.sessionExecutor.Execute(sessionCtx, session)

	// 6a. Nil-guard: synthesize a safe result if executor returned nil
	if result == nil {
		switch {
		case errors.Is(sessionCtx.Err(), context.DeadlineExceeded):
			result = &ExecutionResult{
				Status: alertsession.StatusTimedOut,
				Error:  fmt.Errorf("session timed out after %v", w.config.SessionTimeout),
			}
		case errors.Is(sessionCtx.Err(), context.Canceled):
			result = &ExecutionResult{
				Status: alertsession.StatusCancelled,
				Error:  context.Canceled,
			}
		default:
			result = &ExecutionResult{
				Status: alertsession.StatusFailed,
				Error:  fmt.Errorf("executor returned nil result"),
			}
		}
	}

	// 7. Handle timeout
	if result.Status == "" && errors.Is(sessionCtx.Err(), context.DeadlineExceeded) {
		result = &ExecutionResult{
			Status: alertsession.StatusTimedOut,
			Error:  fmt.Errorf("session timed out after %v", w.config.SessionTimeout),
		}
	}

	// 8. Handle cancellation
	if result.Status == "" && errors.Is(sessionCtx.Err(), context.Canceled) {
		result = &ExecutionResult{
			Status: alertsession.StatusCancelled,
			Error:  context.Canceled,
		}
	}

	// 9. Stop heartbeat
	cancelHeartbeat()

	// 10. Update terminal status (use background context — session ctx may be cancelled)
	if err := w.updateSessionTerminalStatus(context.Background(), session, result); err != nil {
		log.Error("Failed to update session terminal status", "error", err)
		return err
	}

	// 11. Cleanup transient events
	if err := w.cleanupSessionEvents(context.Background(), session.ID); err != nil {
		log.Warn("Failed to cleanup session events", "error", err)
		// Non-fatal
	}

	w.mu.Lock()
	w.sessionsProcessed++
	w.mu.Unlock()

	log.Info("Session processing complete", "status", result.Status)
	return nil
}

// claimNextSession atomically claims the next pending session using FOR UPDATE SKIP LOCKED.
func (w *Worker) claimNextSession(ctx context.Context) (*ent.AlertSession, error) {
	tx, err := w.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// SELECT ... FOR UPDATE SKIP LOCKED
	// Order by created_at for FIFO processing
	session, err := tx.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusPending),
			alertsession.DeletedAtIsNil(),
		).
		Order(ent.Asc(alertsession.FieldCreatedAt)).
		Limit(1).
		ForUpdate(sql.WithLockAction(sql.SkipLocked)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNoSessionsAvailable
		}
		return nil, fmt.Errorf("failed to query pending session: %w", err)
	}

	// Claim: set in_progress, pod_id, started_at, last_interaction_at
	// This is when actual execution starts (mirrors Stage and AgentExecution behavior)
	now := time.Now()
	session, err = session.Update().
		SetStatus(alertsession.StatusInProgress).
		SetPodID(w.podID).
		SetStartedAt(now).
		SetLastInteractionAt(now).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to claim session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claim: %w", err)
	}

	return session, nil
}

// runHeartbeat periodically updates last_interaction_at for orphan detection.
func (w *Worker) runHeartbeat(ctx context.Context, sessionID string) {
	ticker := time.NewTicker(w.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.client.AlertSession.UpdateOneID(sessionID).
				SetLastInteractionAt(time.Now()).
				Exec(ctx); err != nil {
				slog.Warn("Heartbeat update failed", "session_id", sessionID, "error", err)
			}
		}
	}
}

// updateSessionTerminalStatus writes the final session status.
func (w *Worker) updateSessionTerminalStatus(ctx context.Context, session *ent.AlertSession, result *ExecutionResult) error {
	update := w.client.AlertSession.UpdateOneID(session.ID).
		SetStatus(result.Status).
		SetCompletedAt(time.Now())

	if result.Error != nil {
		update = update.SetErrorMessage(result.Error.Error())
	}
	if result.FinalAnalysis != "" {
		update = update.SetFinalAnalysis(result.FinalAnalysis)
	}
	if result.ExecutiveSummary != "" {
		update = update.SetExecutiveSummary(result.ExecutiveSummary)
	}

	return update.Exec(ctx)
}

// cleanupSessionEvents removes transient Event records used for WebSocket delivery.
func (w *Worker) cleanupSessionEvents(ctx context.Context, sessionID string) error {
	_, err := w.client.Event.Delete().
		Where(event.SessionIDEQ(sessionID)).
		Exec(ctx)
	return err
}

// pollInterval returns the poll duration with jitter.
func (w *Worker) pollInterval() time.Duration {
	base := w.config.PollInterval
	jitter := w.config.PollIntervalJitter
	if jitter <= 0 {
		return base
	}
	// Range: [base - jitter, base + jitter]
	offset := time.Duration(rand.Int64N(int64(2 * jitter)))
	return base - jitter + offset
}

// setStatus updates the worker's health tracking state.
func (w *Worker) setStatus(status WorkerStatus, sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
	w.currentSessionID = sessionID
	w.lastActivity = time.Now()
}
