package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/predicate"
	"github.com/codeready-toolchain/tarsy/ent/sessionreviewactivity"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// UpdateReviewStatus performs an atomic compare-and-transition on the session's
// review_status. Returns the updated session or ErrConflict if the precondition
// (expected current review_status) was not met.
func (s *SessionService) UpdateReviewStatus(_ context.Context, sessionID string, req models.UpdateReviewRequest) (*ent.AlertSession, error) {
	if !models.ValidReviewAction(req.Action) {
		return nil, NewValidationError("action", fmt.Sprintf("unknown action %q", req.Action))
	}

	writeCtx, cancel := context.WithTimeoutCause(
		context.Background(), 5*time.Second,
		fmt.Errorf("update review status for %s: db write timed out", sessionID),
	)
	defer cancel()

	tx, err := s.client.Tx(writeCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()

	switch models.ReviewAction(req.Action) {
	case models.ReviewActionClaim:
		if err := s.doClaim(writeCtx, tx, sessionID, req.Actor, now); err != nil {
			return nil, err
		}

	case models.ReviewActionUnclaim:
		affected, err := tx.AlertSession.Update().
			Where(
				alertsession.IDEQ(sessionID),
				alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
			).
			SetReviewStatus(alertsession.ReviewStatusNeedsReview).
			ClearAssignee().
			ClearAssignedAt().
			Save(writeCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to unclaim session: %w", err)
		}
		if affected == 0 {
			return nil, ErrConflict
		}
		if err := s.insertActivity(writeCtx, tx, sessionID, req.Actor,
			sessionreviewactivity.ActionUnclaim,
			ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
			sessionreviewactivity.ToStatusNeedsReview,
			nil, req.Note, now); err != nil {
			return nil, err
		}

	case models.ReviewActionResolve:
		if req.ResolutionReason == nil {
			return nil, NewValidationError("resolution_reason", "required for resolve action")
		}
		if err := s.doResolve(writeCtx, tx, sessionID, req.Actor, *req.ResolutionReason, req.Note, now); err != nil {
			return nil, err
		}

	case models.ReviewActionReopen:
		affected, err := tx.AlertSession.Update().
			Where(
				alertsession.IDEQ(sessionID),
				alertsession.ReviewStatusEQ(alertsession.ReviewStatusResolved),
			).
			SetReviewStatus(alertsession.ReviewStatusNeedsReview).
			ClearAssignee().
			ClearAssignedAt().
			ClearResolvedAt().
			ClearResolutionReason().
			ClearResolutionNote().
			Save(writeCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to reopen session: %w", err)
		}
		if affected == 0 {
			return nil, ErrConflict
		}
		if err := s.insertActivity(writeCtx, tx, sessionID, req.Actor,
			sessionreviewactivity.ActionReopen,
			ptrFromStatus(sessionreviewactivity.FromStatusResolved),
			sessionreviewactivity.ToStatusNeedsReview,
			nil, req.Note, now); err != nil {
			return nil, err
		}
	}

	session, err := tx.AlertSession.Get(writeCtx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to read updated session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit review status update: %w", err)
	}

	return session, nil
}

// doClaim handles both initial claim (needs_review -> in_progress) and
// reassignment (in_progress -> in_progress). Returns ErrConflict if
// the session is not in a claimable state.
func (s *SessionService) doClaim(ctx context.Context, tx *ent.Tx, sessionID, actor string, now time.Time) error {
	// Try claim from needs_review first.
	affected, err := tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusNeedsReview),
		).
		SetReviewStatus(alertsession.ReviewStatusInProgress).
		SetAssignee(actor).
		SetAssignedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to claim session: %w", err)
	}
	if affected > 0 {
		return s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionClaim,
			ptrFromStatus(sessionreviewactivity.FromStatusNeedsReview),
			sessionreviewactivity.ToStatusInProgress,
			nil, nil, now)
	}

	// Try reassignment from in_progress.
	affected, err = tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
		).
		SetAssignee(actor).
		SetAssignedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to reassign session: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}
	return s.insertActivity(ctx, tx, sessionID, actor,
		sessionreviewactivity.ActionClaim,
		ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
		sessionreviewactivity.ToStatusInProgress,
		nil, nil, now)
}

// doResolve handles both direct resolve (needs_review -> resolved) and
// standard resolve (in_progress -> resolved).
func (s *SessionService) doResolve(ctx context.Context, tx *ent.Tx, sessionID, actor, reason string, note *string, now time.Time) error {
	resReason := alertsession.ResolutionReason(reason)

	// Try resolve from in_progress first.
	update := tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
		).
		SetReviewStatus(alertsession.ReviewStatusResolved).
		SetResolvedAt(now).
		SetResolutionReason(resReason)
	if note != nil {
		update = update.SetResolutionNote(*note)
	}

	affected, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to resolve session: %w", err)
	}
	if affected > 0 {
		activityReason := sessionreviewactivity.ResolutionReason(reason)
		return s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionResolve,
			ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
			sessionreviewactivity.ToStatusResolved,
			&activityReason, note, now)
	}

	// Try direct resolve from needs_review (auto-claims first).
	update = tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusNeedsReview),
		).
		SetReviewStatus(alertsession.ReviewStatusResolved).
		SetAssignee(actor).
		SetAssignedAt(now).
		SetResolvedAt(now).
		SetResolutionReason(resReason)
	if note != nil {
		update = update.SetResolutionNote(*note)
	}

	affected, err = update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to direct-resolve session: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}

	// Two activity rows: implicit claim + resolution.
	if err := s.insertActivity(ctx, tx, sessionID, actor,
		sessionreviewactivity.ActionClaim,
		ptrFromStatus(sessionreviewactivity.FromStatusNeedsReview),
		sessionreviewactivity.ToStatusInProgress,
		nil, nil, now); err != nil {
		return err
	}
	activityReason := sessionreviewactivity.ResolutionReason(reason)
	return s.insertActivity(ctx, tx, sessionID, actor,
		sessionreviewactivity.ActionResolve,
		ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
		sessionreviewactivity.ToStatusResolved,
		&activityReason, note, now)
}

// insertActivity creates a SessionReviewActivity record within the transaction.
func (s *SessionService) insertActivity(
	ctx context.Context, tx *ent.Tx,
	sessionID, actor string,
	action sessionreviewactivity.Action,
	fromStatus *sessionreviewactivity.FromStatus,
	toStatus sessionreviewactivity.ToStatus,
	resolutionReason *sessionreviewactivity.ResolutionReason,
	note *string,
	createdAt time.Time,
) error {
	create := tx.SessionReviewActivity.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetActor(actor).
		SetAction(action).
		SetToStatus(toStatus).
		SetCreatedAt(createdAt).
		SetNillableFromStatus(fromStatus).
		SetNillableResolutionReason(resolutionReason).
		SetNillableNote(note)

	if err := create.Exec(ctx); err != nil {
		return fmt.Errorf("failed to insert review activity: %w", err)
	}
	return nil
}

// GetReviewActivity returns all review activity records for a session,
// ordered by created_at ascending.
func (s *SessionService) GetReviewActivity(ctx context.Context, sessionID string) ([]*ent.SessionReviewActivity, error) {
	// Verify session exists.
	exists, err := s.client.AlertSession.Query().
		Where(alertsession.IDEQ(sessionID), alertsession.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check session: %w", err)
	}
	if !exists {
		return nil, ErrNotFound
	}

	activities, err := s.client.SessionReviewActivity.Query().
		Where(sessionreviewactivity.SessionIDEQ(sessionID)).
		Order(sessionreviewactivity.ByCreatedAt()).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query review activity: %w", err)
	}
	return activities, nil
}

// GetTriageSessions returns sessions grouped by review status for the triage view.
func (s *SessionService) GetTriageSessions(ctx context.Context, params models.TriageParams) (*models.TriageResponse, error) {
	if params.ResolvedLimit <= 0 {
		params.ResolvedLimit = 20
	}

	investigating, err := s.queryTriageGroup(ctx, nil, params.Assignee,
		alertsession.StatusIn(alertsession.StatusPending, alertsession.StatusInProgress, alertsession.StatusCancelling),
		alertsession.ReviewStatusIsNil(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query investigating sessions: %w", err)
	}

	needsReview, err := s.queryTriageGroup(ctx, nil, params.Assignee,
		alertsession.ReviewStatusEQ(alertsession.ReviewStatusNeedsReview),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query needs_review sessions: %w", err)
	}

	inProgress, err := s.queryTriageGroup(ctx, nil, params.Assignee,
		alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query in_progress sessions: %w", err)
	}

	resolvedLimit := params.ResolvedLimit + 1
	resolved, err := s.queryTriageGroup(ctx, &resolvedLimit, params.Assignee,
		alertsession.ReviewStatusEQ(alertsession.ReviewStatusResolved),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query resolved sessions: %w", err)
	}

	hasMore := len(resolved) > params.ResolvedLimit
	if hasMore {
		resolved = resolved[:params.ResolvedLimit]
	}

	return &models.TriageResponse{
		Investigating: models.TriageGroup{Count: len(investigating), Sessions: investigating},
		NeedsReview:   models.TriageGroup{Count: len(needsReview), Sessions: needsReview},
		InProgress:    models.TriageGroup{Count: len(inProgress), Sessions: inProgress},
		Resolved:      models.TriageGroup{Count: len(resolved), Sessions: resolved, HasMore: hasMore},
	}, nil
}

// queryTriageGroup queries sessions matching the given predicates and maps them
// to DashboardSessionItem. If limit is non-nil, at most *limit sessions are returned.
func (s *SessionService) queryTriageGroup(ctx context.Context, limit *int, assignee string, predicates ...predicate.AlertSession) ([]models.DashboardSessionItem, error) {
	q := s.client.AlertSession.Query().
		Where(alertsession.DeletedAtIsNil()).
		Where(predicates...).
		Order(ent.Desc(alertsession.FieldCreatedAt))

	if assignee != "" {
		q = q.Where(alertsession.AssigneeEQ(assignee))
	}
	if limit != nil {
		q = q.Limit(*limit)
	}

	sessions, err := q.All(ctx)
	if err != nil {
		return nil, err
	}

	items := make([]models.DashboardSessionItem, 0, len(sessions))
	for _, sess := range sessions {
		items = append(items, sessionToTriageItem(sess))
	}
	return items, nil
}

// sessionToTriageItem maps an ent.AlertSession to a DashboardSessionItem with
// the fields available directly on the entity. Aggregated stats (token counts,
// stage counts, etc.) are not populated — the triage view doesn't display them.
func sessionToTriageItem(sess *ent.AlertSession) models.DashboardSessionItem {
	var alertType *string
	if sess.AlertType != "" {
		alertType = &sess.AlertType
	}

	var durationMs *int64
	if sess.StartedAt != nil && sess.CompletedAt != nil {
		ms := sess.CompletedAt.Sub(*sess.StartedAt).Milliseconds()
		durationMs = &ms
	}

	var reviewStatus *string
	if sess.ReviewStatus != nil {
		s := string(*sess.ReviewStatus)
		reviewStatus = &s
	}

	var resolutionReason *string
	if sess.ResolutionReason != nil {
		s := string(*sess.ResolutionReason)
		resolutionReason = &s
	}

	return models.DashboardSessionItem{
		ID:               sess.ID,
		AlertType:        alertType,
		ChainID:          sess.ChainID,
		Status:           string(sess.Status),
		Author:           sess.Author,
		CreatedAt:        sess.CreatedAt,
		StartedAt:        sess.StartedAt,
		CompletedAt:      sess.CompletedAt,
		DurationMs:       durationMs,
		ErrorMessage:     sess.ErrorMessage,
		ExecutiveSummary: sess.ExecutiveSummary,
		ReviewStatus:     reviewStatus,
		Assignee:         sess.Assignee,
		ResolutionReason: resolutionReason,
	}
}

func ptrFromStatus(s sessionreviewactivity.FromStatus) *sessionreviewactivity.FromStatus {
	return &s
}
