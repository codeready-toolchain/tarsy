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
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	tarsyslack "github.com/codeready-toolchain/tarsy/pkg/slack"
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
	eventPublisher  agent.EventPublisher
	slackService    *tarsyslack.Service
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
// eventPublisher may be nil (streaming disabled).
// slackService may be nil (Slack notifications disabled).
func NewWorker(id, podID string, client *ent.Client, cfg *config.QueueConfig, executor SessionExecutor, pool SessionRegistry, eventPublisher agent.EventPublisher, slackService *tarsyslack.Service) *Worker {
	return &Worker{
		id:              id,
		podID:           podID,
		client:          client,
		config:          cfg,
		sessionExecutor: executor,
		eventPublisher:  eventPublisher,
		slackService:    slackService,
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
		Status:            w.status,
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

	// Publish session status "in_progress" to both session and global channels
	w.publishSessionStatus(ctx, session.ID, alertsession.StatusInProgress)

	// Send Slack start notification (only if fingerprint present, resolves threadTS)
	slackThreadTS := w.notifySlackStart(ctx, session)

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

	// 10a. Publish terminal session status event
	w.publishSessionStatus(context.Background(), session.ID, result.Status)

	// 10b. Send Slack terminal notification
	w.notifySlackTerminal(context.Background(), session, result, slackThreadTS)

	// 11. Cleanup transient events after grace period (60s) to allow clients
	// to receive final events before they are deleted.
	w.scheduleEventCleanup(session.ID)

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

	if result.FinalAnalysis != "" {
		update = update.SetFinalAnalysis(result.FinalAnalysis)
	}
	if result.ExecutiveSummary != "" {
		update = update.SetExecutiveSummary(result.ExecutiveSummary)
	}
	if result.ExecutiveSummaryError != "" {
		update = update.SetExecutiveSummaryError(result.ExecutiveSummaryError)
	}
	if result.Error != nil {
		update = update.SetErrorMessage(result.Error.Error())
	}

	return update.Exec(ctx)
}

// publishSessionStatus publishes a session status event to both the session-specific
// and global channels for real-time WebSocket delivery. Non-blocking: errors are logged.
func (w *Worker) publishSessionStatus(ctx context.Context, sessionID string, status alertsession.Status) {
	if w.eventPublisher == nil {
		return
	}
	if err := w.eventPublisher.PublishSessionStatus(ctx, sessionID, events.SessionStatusPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeSessionStatus,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		Status: status,
	}); err != nil {
		slog.Warn("Failed to publish session status",
			"session_id", sessionID, "status", status, "error", err)
	}
}

// scheduleEventCleanup schedules deletion of transient events after a 60-second
// grace period, allowing WebSocket clients to receive final events.
func (w *Worker) scheduleEventCleanup(sessionID string) {
	time.AfterFunc(60*time.Second, func() {
		if err := w.cleanupSessionEvents(context.Background(), sessionID); err != nil {
			slog.Warn("Failed to cleanup session events after grace period",
				"session_id", sessionID, "error", err)
		}
	})
}

// cleanupSessionEvents removes transient Event records used for WebSocket delivery.
func (w *Worker) cleanupSessionEvents(ctx context.Context, sessionID string) error {
	_, err := w.client.Event.Delete().
		Where(event.SessionIDEQ(sessionID)).
		Exec(ctx)
	return err
}

// notifySlackStart sends a Slack start notification if the session has a fingerprint.
// Returns the resolved threadTS for reuse by terminal notification.
func (w *Worker) notifySlackStart(ctx context.Context, session *ent.AlertSession) string {
	if w.slackService == nil {
		return ""
	}

	var fingerprint string
	if session.SlackMessageFingerprint != nil {
		fingerprint = *session.SlackMessageFingerprint
	}

	return w.slackService.NotifySessionStarted(ctx, tarsyslack.SessionStartedInput{
		SessionID:               session.ID,
		AlertType:               session.AlertType,
		SlackMessageFingerprint: fingerprint,
	})
}

// notifySlackTerminal sends a Slack terminal status notification.
func (w *Worker) notifySlackTerminal(ctx context.Context, session *ent.AlertSession, result *ExecutionResult, threadTS string) {
	if w.slackService == nil {
		return
	}

	var fingerprint string
	if session.SlackMessageFingerprint != nil {
		fingerprint = *session.SlackMessageFingerprint
	}

	var errMsg string
	if result.Error != nil {
		errMsg = result.Error.Error()
	}

	w.slackService.NotifySessionCompleted(ctx, tarsyslack.SessionCompletedInput{
		SessionID:               session.ID,
		AlertType:               session.AlertType,
		Status:                  string(result.Status),
		ExecutiveSummary:        result.ExecutiveSummary,
		FinalAnalysis:           result.FinalAnalysis,
		ErrorMessage:            errMsg,
		SlackMessageFingerprint: fingerprint,
		ThreadTS:                threadTS,
	})
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
