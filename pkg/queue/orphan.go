package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
)

// orphanedScoringError is stored on scores, stages, and executions abandoned
// because the process stopped before scoring reached a terminal status.
const orphanedScoringError = "Scoring interrupted: process stopped while scoring was in progress"

// orphanedPostInvestigationError is stored on post-investigation stages and
// executions abandoned because the process stopped mid-run.
const orphanedPostInvestigationError = "Post-investigation interrupted: process stopped while the agent was running"

// postInvestigationStaleMargin is added to the longest entry timeout before a
// post-investigation stage is considered abandoned.
const postInvestigationStaleMargin = 5 * time.Minute

// orphanState tracks orphan detection metrics (thread-safe).
type orphanState struct {
	mu               sync.Mutex
	lastOrphanScan   time.Time
	orphansRecovered int
}

// runOrphanDetection periodically scans for orphaned sessions.
// All pods run this independently — operations are idempotent.
func (p *WorkerPool) runOrphanDetection(ctx context.Context) {
	ticker := time.NewTicker(p.config.OrphanDetectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			if err := p.detectAndRecoverOrphans(ctx); err != nil {
				slog.Error("Orphan detection failed", "error", err)
			}
		}
	}
}

// detectAndRecoverOrphans finds in_progress sessions with stale heartbeats
// and marks them as timed_out (terminal state).
func (p *WorkerPool) detectAndRecoverOrphans(ctx context.Context) error {
	if err := p.recoverStaleScoring(ctx); err != nil {
		slog.Error("Stale scoring recovery failed", "error", err)
	}
	if err := p.recoverStalePostInvestigation(ctx); err != nil {
		slog.Error("Stale post-investigation recovery failed", "error", err)
	}

	threshold := time.Now().Add(-p.config.OrphanThreshold)

	orphans, err := p.client.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusInProgress),
			alertsession.LastInteractionAtNotNil(),
			alertsession.LastInteractionAtLT(threshold),
			alertsession.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query orphaned sessions: %w", err)
	}

	if len(orphans) == 0 {
		p.orphans.mu.Lock()
		p.orphans.lastOrphanScan = time.Now()
		p.orphans.mu.Unlock()
		return nil
	}

	slog.Warn("Detected orphaned sessions", "count", len(orphans))

	recovered := 0
	failed := 0
	for _, session := range orphans {
		if err := p.recoverOrphanedSession(ctx, session); err != nil {
			slog.Error("Failed to recover orphaned session",
				"session_id", session.ID,
				"error", err)
			failed++
			continue
		}
		recovered++
	}

	p.orphans.mu.Lock()
	p.orphans.lastOrphanScan = time.Now()
	p.orphans.orphansRecovered += recovered
	p.orphans.mu.Unlock()

	if recovered > 0 {
		metrics.OrphansRecoveredTotal.Add(float64(recovered))
	}

	if failed > 0 {
		slog.Warn("Orphan recovery completed with failures",
			"total_orphans", len(orphans),
			"recovered", recovered,
			"failed", failed)
	}

	return nil
}

// recoverOrphanedSession marks a single orphaned session as timed_out.
func (p *WorkerPool) recoverOrphanedSession(ctx context.Context, session *ent.AlertSession) error {
	log := slog.With("session_id", session.ID, "old_pod_id", session.PodID)

	lastHeartbeat := "unknown"
	if session.LastInteractionAt != nil {
		lastHeartbeat = session.LastInteractionAt.Format(time.RFC3339)
	}

	podID := "unknown"
	if session.PodID != nil {
		podID = *session.PodID
	}

	errorMsg := fmt.Sprintf("Orphaned: no heartbeat from pod %s since %s", podID, lastHeartbeat)
	if err := markSessionTimedOut(ctx, p.client, session.ID, errorMsg); err != nil {
		return err
	}

	log.Warn("Orphaned session marked as timed_out", "last_heartbeat", lastHeartbeat)
	return nil
}

// CleanupStartupOrphans performs a one-time cleanup of sessions owned by this pod
// that were in-progress when the pod previously crashed.
// Called once during startup, before the worker pool begins processing.
func CleanupStartupOrphans(ctx context.Context, client *ent.Client, podID string) error {
	orphans, err := client.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusInProgress),
			alertsession.PodIDEQ(podID),
			alertsession.DeletedAtIsNil(),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query startup orphans: %w", err)
	}

	if len(orphans) == 0 {
		return nil
	}

	slog.Warn("Found startup orphans from previous run",
		"pod_id", podID,
		"count", len(orphans))

	for _, session := range orphans {
		errorMsg := fmt.Sprintf("Orphaned: pod %s restarted while session was in progress", podID)
		if err := markSessionTimedOut(ctx, client, session.ID, errorMsg); err != nil {
			slog.Error("Failed to mark startup orphan",
				"session_id", session.ID,
				"error", err)
			continue
		}

		slog.Info("Startup orphan recovered", "session_id", session.ID)
	}

	return nil
}

// markSessionTimedOut is a shared helper that marks a session as timed_out
// and updates any streaming timeline events. Uses a transaction for atomicity.
func markSessionTimedOut(ctx context.Context, client *ent.Client, sessionID, errorMsg string) error {
	now := time.Now()

	// Use transaction to ensure session and timeline events are updated atomically
	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Mark session as timed_out (terminal — no resume)
	err = tx.AlertSession.UpdateOneID(sessionID).
		SetStatus(alertsession.StatusTimedOut).
		SetCompletedAt(now).
		SetErrorMessage(errorMsg).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to mark session as timed_out: %w", err)
	}

	// Mark any incomplete TimelineEvents as timed_out
	if err := tx.TimelineEvent.Update().
		Where(
			timelineevent.SessionIDEQ(sessionID),
			timelineevent.StatusEQ(timelineevent.StatusStreaming),
		).
		SetStatus(timelineevent.StatusTimedOut).
		SetUpdatedAt(now).
		Exec(ctx); err != nil {
		return fmt.Errorf("failed to update timeline events: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// CleanupStartupScoringOrphans marks scoring runs left in progress by a previous
// process as failed. Scoring starts only after the session is already completed,
// so CleanupStartupOrphans does not see these rows.
// Called once during startup, before the worker pool begins processing.
func CleanupStartupScoringOrphans(ctx context.Context, client *ent.Client, podID string) error {
	scores, err := client.SessionScore.Query().
		Where(
			sessionscore.StatusIn(sessionscore.StatusPending, sessionscore.StatusInProgress),
			sessionscore.HasSessionWith(
				alertsession.PodIDEQ(podID),
				alertsession.DeletedAtIsNil(),
			),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query startup scoring orphans: %w", err)
	}
	if len(scores) == 0 {
		return nil
	}

	slog.Warn("Found startup scoring orphans from previous run",
		"pod_id", podID,
		"count", len(scores))

	for _, score := range scores {
		updated, err := markOrphanedScoringFailed(ctx, client, score)
		if err != nil {
			slog.Error("Failed to mark startup scoring orphan",
				"session_id", score.SessionID,
				"score_id", score.ID,
				"error", err)
			continue
		}
		if !updated {
			continue
		}
		slog.Info("Startup scoring orphan recovered",
			"session_id", score.SessionID,
			"score_id", score.ID)
	}

	return nil
}

// recoverStaleScoring marks scoring runs that outlived the scoring timeout as failed.
// A live run is cancelled at scoringTimeout, so anything older was abandoned
// (for example a pod crash while another replica kept running).
func (p *WorkerPool) recoverStaleScoring(ctx context.Context) error {
	threshold := time.Now().Add(-scoringTimeout)

	scores, err := p.client.SessionScore.Query().
		Where(
			sessionscore.StatusIn(sessionscore.StatusPending, sessionscore.StatusInProgress),
			sessionscore.StartedAtLT(threshold),
			sessionscore.HasSessionWith(alertsession.DeletedAtIsNil()),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query stale scoring runs: %w", err)
	}
	if len(scores) == 0 {
		return nil
	}

	slog.Warn("Detected stale scoring runs", "count", len(scores))

	for _, score := range scores {
		updated, err := markOrphanedScoringFailed(ctx, p.client, score)
		if err != nil {
			slog.Error("Failed to recover stale scoring run",
				"session_id", score.SessionID,
				"score_id", score.ID,
				"error", err)
			continue
		}
		if !updated {
			continue
		}
		slog.Info("Stale scoring run marked as failed",
			"session_id", score.SessionID,
			"score_id", score.ID)
	}

	return nil
}

// markOrphanedScoringFailed marks a non-terminal score, its stage, active
// executions, and streaming timeline events as failed. The score and stage
// updates are conditional so a run that finishes concurrently is left unchanged.
// Executions and timeline events are updated only when the stage transitions.
func markOrphanedScoringFailed(ctx context.Context, client *ent.Client, score *ent.SessionScore) (bool, error) {
	now := time.Now()

	tx, err := client.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	n, err := tx.SessionScore.Update().
		Where(
			sessionscore.IDEQ(score.ID),
			sessionscore.StatusIn(sessionscore.StatusPending, sessionscore.StatusInProgress),
		).
		SetStatus(sessionscore.StatusFailed).
		SetCompletedAt(now).
		SetErrorMessage(orphanedScoringError).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to mark session score as failed: %w", err)
	}
	if n == 0 {
		return false, nil
	}

	if score.StageID != nil {
		if _, err := markOrphanedStageFailed(ctx, tx, *score.StageID, orphanedScoringError, now); err != nil {
			return false, err
		}
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return true, nil
}

// markOrphanedStageFailed marks a pending/active stage failed inside tx. When
// the stage transitions, its pending/active executions (sub-agents included)
// are failed and its streaming timeline events timed out. The stage update is
// conditional so a run that finishes concurrently is left unchanged.
func markOrphanedStageFailed(ctx context.Context, tx *ent.Tx, stageID, errMsg string, now time.Time) (bool, error) {
	n, err := tx.Stage.Update().
		Where(
			stage.IDEQ(stageID),
			stage.StatusIn(stage.StatusPending, stage.StatusActive),
		).
		SetStatus(stage.StatusFailed).
		SetCompletedAt(now).
		SetErrorMessage(errMsg).
		Save(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to mark stage as failed: %w", err)
	}
	if n == 0 {
		return false, nil
	}

	if _, err := tx.AgentExecution.Update().
		Where(
			agentexecution.StageIDEQ(stageID),
			agentexecution.StatusIn(agentexecution.StatusPending, agentexecution.StatusActive),
		).
		SetStatus(agentexecution.StatusFailed).
		SetCompletedAt(now).
		SetErrorMessage(errMsg).
		Save(ctx); err != nil {
		return false, fmt.Errorf("failed to mark stage executions as failed: %w", err)
	}

	if _, err := tx.TimelineEvent.Update().
		Where(
			timelineevent.StageIDEQ(stageID),
			timelineevent.StatusEQ(timelineevent.StatusStreaming),
		).
		SetStatus(timelineevent.StatusTimedOut).
		SetUpdatedAt(now).
		Save(ctx); err != nil {
		return false, fmt.Errorf("failed to update stage timeline events: %w", err)
	}

	return true, nil
}

// CleanupStartupPostInvestigationOrphans marks post-investigation stages left
// pending/active by a previous process of this pod as failed. They start after
// the session is terminal, so CleanupStartupOrphans does not see them.
// Called once during startup, before the worker pool begins processing.
func CleanupStartupPostInvestigationOrphans(ctx context.Context, client *ent.Client, podID string) error {
	stages, err := client.Stage.Query().
		Where(
			stage.StageTypeEQ(stage.StageTypePostInvestigation),
			stage.StatusIn(stage.StatusPending, stage.StatusActive),
			stage.HasSessionWith(
				alertsession.PodIDEQ(podID),
				alertsession.DeletedAtIsNil(),
			),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query startup post-investigation orphans: %w", err)
	}
	if len(stages) == 0 {
		return nil
	}

	slog.Warn("Found startup post-investigation orphans from previous run",
		"pod_id", podID,
		"count", len(stages))

	for _, stg := range stages {
		updated, err := markOrphanedPostInvestigationFailed(ctx, client, stg.ID)
		if err != nil {
			slog.Error("Failed to mark startup post-investigation orphan",
				"session_id", stg.SessionID,
				"stage_id", stg.ID,
				"error", err)
			continue
		}
		if updated {
			slog.Info("Startup post-investigation orphan recovered",
				"session_id", stg.SessionID,
				"stage_id", stg.ID)
		}
	}

	return nil
}

// recoverStalePostInvestigation marks post-investigation stages that outlived
// every possible run as failed (e.g. a pod crash while another replica keeps
// running). Active stages are aged by started_at against the longest entry
// timeout. Pending stages have no start time and are aged by the session's
// completed_at, adding scoring's timeout since runs start after auto-scoring.
func (p *WorkerPool) recoverStalePostInvestigation(ctx context.Context) error {
	maxTimeout := p.postInvestigationMaxTimeout
	if maxTimeout <= 0 {
		return nil
	}
	now := time.Now()
	activeThreshold := now.Add(-(maxTimeout + postInvestigationStaleMargin))
	pendingThreshold := now.Add(-(scoringTimeout + maxTimeout + postInvestigationStaleMargin))

	stages, err := p.client.Stage.Query().
		Where(
			stage.StageTypeEQ(stage.StageTypePostInvestigation),
			stage.HasSessionWith(alertsession.DeletedAtIsNil()),
			stage.Or(
				stage.And(
					stage.StatusEQ(stage.StatusActive),
					stage.StartedAtLT(activeThreshold),
				),
				stage.And(
					stage.StatusEQ(stage.StatusPending),
					stage.HasSessionWith(alertsession.CompletedAtLT(pendingThreshold)),
				),
			),
		).
		All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query stale post-investigation stages: %w", err)
	}
	if len(stages) == 0 {
		return nil
	}

	slog.Warn("Detected stale post-investigation stages", "count", len(stages))

	for _, stg := range stages {
		updated, err := markOrphanedPostInvestigationFailed(ctx, p.client, stg.ID)
		if err != nil {
			slog.Error("Failed to recover stale post-investigation stage",
				"session_id", stg.SessionID,
				"stage_id", stg.ID,
				"error", err)
			continue
		}
		if updated {
			slog.Info("Stale post-investigation stage marked as failed",
				"session_id", stg.SessionID,
				"stage_id", stg.ID)
		}
	}

	return nil
}

func markOrphanedPostInvestigationFailed(ctx context.Context, client *ent.Client, stageID string) (bool, error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := markOrphanedStageFailed(ctx, tx, stageID, orphanedPostInvestigationError, time.Now())
	if err != nil || !updated {
		return false, err
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return true, nil
}
