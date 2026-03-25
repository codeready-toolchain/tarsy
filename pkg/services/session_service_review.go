package services

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/predicate"
	"github.com/codeready-toolchain/tarsy/ent/sessionreviewactivity"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// UpdateReviewStatus applies a review action to one or more sessions.
// Each session is processed independently in its own transaction; a failure
// on one does not affect others. The parent context is checked between
// iterations so bulk operations abort early if the caller disconnects.
// Returns the per-session results and the list of successfully updated
// ent sessions (for event publishing).
func (s *SessionService) UpdateReviewStatus(ctx context.Context, req models.UpdateReviewRequest) (models.UpdateReviewResponse, []*ent.AlertSession) {
	if err := validateReviewRequest(req); err != nil {
		results := make([]models.UpdateReviewResult, len(req.SessionIDs))
		for i, sid := range req.SessionIDs {
			results[i] = models.UpdateReviewResult{SessionID: sid, Success: false, Error: err.Error()}
		}
		return models.UpdateReviewResponse{Results: results}, nil
	}

	results := make([]models.UpdateReviewResult, 0, len(req.SessionIDs))
	var updated []*ent.AlertSession
	for _, sid := range req.SessionIDs {
		if err := ctx.Err(); err != nil {
			results = append(results, models.UpdateReviewResult{
				SessionID: sid,
				Success:   false,
				Error:     err.Error(),
			})
			continue
		}
		session, err := s.updateSingleReview(sid, req)
		if err != nil {
			results = append(results, models.UpdateReviewResult{
				SessionID: sid,
				Success:   false,
				Error:     err.Error(),
			})
		} else {
			results = append(results, models.UpdateReviewResult{
				SessionID: sid,
				Success:   true,
			})
			updated = append(updated, session)

			if req.QualityRating != nil {
				metrics.ReviewsCompletedTotal.WithLabelValues(*req.QualityRating).Inc()
			}
		}
	}
	return models.UpdateReviewResponse{Results: results}, updated
}

var validQualityRatings = map[string]bool{
	string(alertsession.QualityRatingAccurate):          true,
	string(alertsession.QualityRatingPartiallyAccurate): true,
	string(alertsession.QualityRatingInaccurate):        true,
}

func validateReviewRequest(req models.UpdateReviewRequest) error {
	if !models.ValidReviewAction(req.Action) {
		return NewValidationError("action", fmt.Sprintf("unknown action %q", req.Action))
	}
	switch models.ReviewAction(req.Action) {
	case models.ReviewActionComplete:
		if req.QualityRating == nil {
			return NewValidationError("quality_rating", "required for complete action")
		}
		if !validQualityRatings[*req.QualityRating] {
			return NewValidationError("quality_rating", fmt.Sprintf("invalid value %q", *req.QualityRating))
		}
	case models.ReviewActionUpdateFeedback:
		if req.QualityRating == nil && req.ActionTaken == nil && req.InvestigationFeedback == nil {
			return NewValidationError("update_feedback", "at least one field must be provided")
		}
		if req.QualityRating != nil && !validQualityRatings[*req.QualityRating] {
			return NewValidationError("quality_rating", fmt.Sprintf("invalid value %q", *req.QualityRating))
		}
	}
	return nil
}

// updateSingleReview performs an atomic compare-and-transition on one session's
// review_status. Returns the updated session or ErrConflict if the precondition
// (expected current review_status) was not met.
// Caller must validate req via validateReviewRequest before calling.
func (s *SessionService) updateSingleReview(sessionID string, req models.UpdateReviewRequest) (*ent.AlertSession, error) {
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
			nil, nil, nil, now); err != nil {
			return nil, err
		}

	case models.ReviewActionComplete:
		if err := s.doComplete(writeCtx, tx, sessionID, req.Actor, *req.QualityRating, req.ActionTaken, req.InvestigationFeedback, now); err != nil {
			return nil, err
		}

	case models.ReviewActionReopen:
		affected, err := tx.AlertSession.Update().
			Where(
				alertsession.IDEQ(sessionID),
				alertsession.ReviewStatusEQ(alertsession.ReviewStatusReviewed),
			).
			SetReviewStatus(alertsession.ReviewStatusNeedsReview).
			ClearAssignee().
			ClearAssignedAt().
			ClearReviewedAt().
			ClearQualityRating().
			ClearActionTaken().
			ClearInvestigationFeedback().
			Save(writeCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to reopen session: %w", err)
		}
		if affected == 0 {
			return nil, ErrConflict
		}
		if err := s.insertActivity(writeCtx, tx, sessionID, req.Actor,
			sessionreviewactivity.ActionReopen,
			ptrFromStatus(sessionreviewactivity.FromStatusReviewed),
			sessionreviewactivity.ToStatusNeedsReview,
			nil, nil, nil, now); err != nil {
			return nil, err
		}

	case models.ReviewActionUpdateFeedback:
		// Read current session to build a full post-update snapshot for the activity log.
		current, err := tx.AlertSession.Get(writeCtx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to read session for feedback update: %w", err)
		}
		if current.ReviewStatus == nil || *current.ReviewStatus != alertsession.ReviewStatusReviewed {
			return nil, ErrConflict
		}

		update := tx.AlertSession.Update().
			Where(
				alertsession.IDEQ(sessionID),
				alertsession.ReviewStatusEQ(alertsession.ReviewStatusReviewed),
			)
		if req.QualityRating != nil {
			update = update.SetQualityRating(alertsession.QualityRating(*req.QualityRating))
		}
		if req.ActionTaken != nil {
			update = update.SetActionTaken(*req.ActionTaken)
		}
		if req.InvestigationFeedback != nil {
			update = update.SetInvestigationFeedback(*req.InvestigationFeedback)
		}
		affected, err := update.Save(writeCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to update feedback: %w", err)
		}
		if affected == 0 {
			return nil, ErrConflict
		}

		// Merge request values with existing session values for a complete snapshot.
		snapshotRating := req.QualityRating
		if snapshotRating == nil && current.QualityRating != nil {
			qr := string(*current.QualityRating)
			snapshotRating = &qr
		}
		snapshotActionTaken := req.ActionTaken
		if snapshotActionTaken == nil {
			snapshotActionTaken = current.ActionTaken
		}
		snapshotFeedback := req.InvestigationFeedback
		if snapshotFeedback == nil {
			snapshotFeedback = current.InvestigationFeedback
		}

		if err := s.insertActivity(writeCtx, tx, sessionID, req.Actor,
			sessionreviewactivity.ActionUpdateFeedback,
			ptrFromStatus(sessionreviewactivity.FromStatusReviewed),
			sessionreviewactivity.ToStatusReviewed,
			snapshotRating, snapshotActionTaken, snapshotFeedback, now); err != nil {
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
			nil, nil, nil, now)
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
	if affected > 0 {
		return s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionClaim,
			ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
			sessionreviewactivity.ToStatusInProgress,
			nil, nil, nil, now)
	}

	// Try claim from NULL review_status (session still investigating or pre-migration).
	affected, err = tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.ReviewStatusIsNil(),
		).
		SetReviewStatus(alertsession.ReviewStatusInProgress).
		SetAssignee(actor).
		SetAssignedAt(now).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to claim session from uninitialized state: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}
	return s.insertActivity(ctx, tx, sessionID, actor,
		sessionreviewactivity.ActionClaim,
		nil,
		sessionreviewactivity.ToStatusInProgress,
		nil, nil, nil, now)
}

// doComplete handles both direct complete (needs_review -> reviewed) and
// standard complete (in_progress -> reviewed).
func (s *SessionService) doComplete(ctx context.Context, tx *ent.Tx, sessionID, actor, rating string, actionTaken, feedback *string, now time.Time) error {
	qr := alertsession.QualityRating(rating)

	buildUpdate := func(base *ent.AlertSessionUpdate) *ent.AlertSessionUpdate {
		u := base.
			SetReviewStatus(alertsession.ReviewStatusReviewed).
			SetReviewedAt(now).
			SetQualityRating(qr)
		if actionTaken != nil {
			u = u.SetActionTaken(*actionTaken)
		}
		if feedback != nil {
			u = u.SetInvestigationFeedback(*feedback)
		}
		return u
	}

	// Try complete from in_progress first.
	update := buildUpdate(tx.AlertSession.Update().Where(
		alertsession.IDEQ(sessionID),
		alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
	))

	affected, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete review: %w", err)
	}
	if affected > 0 {
		return s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionComplete,
			ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
			sessionreviewactivity.ToStatusReviewed,
			&rating, actionTaken, feedback, now)
	}

	// Try direct complete from needs_review (auto-claims first).
	update = buildUpdate(tx.AlertSession.Update().Where(
		alertsession.IDEQ(sessionID),
		alertsession.ReviewStatusEQ(alertsession.ReviewStatusNeedsReview),
	)).
		SetAssignee(actor).
		SetAssignedAt(now)

	affected, err = update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to direct-complete review: %w", err)
	}
	if affected > 0 {
		// Two activity rows: implicit claim + completion.
		// Use distinct timestamps so ORDER BY created_at is deterministic.
		// PostgreSQL timestamptz has microsecond precision, so delta must be >= 1µs.
		claimTime := now
		completeTime := now.Add(time.Microsecond)
		if err := s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionClaim,
			ptrFromStatus(sessionreviewactivity.FromStatusNeedsReview),
			sessionreviewactivity.ToStatusInProgress,
			nil, nil, nil, claimTime); err != nil {
			return err
		}
		return s.insertActivity(ctx, tx, sessionID, actor,
			sessionreviewactivity.ActionComplete,
			ptrFromStatus(sessionreviewactivity.FromStatusInProgress),
			sessionreviewactivity.ToStatusReviewed,
			&rating, actionTaken, feedback, completeTime)
	}

	// Try complete from NULL review_status (session still investigating or pre-migration).
	update = buildUpdate(tx.AlertSession.Update().Where(
		alertsession.IDEQ(sessionID),
		alertsession.ReviewStatusIsNil(),
	)).
		SetAssignee(actor).
		SetAssignedAt(now)

	affected, err = update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete review from uninitialized state: %w", err)
	}
	if affected == 0 {
		return ErrConflict
	}

	return s.insertActivity(ctx, tx, sessionID, actor,
		sessionreviewactivity.ActionComplete,
		nil,
		sessionreviewactivity.ToStatusReviewed,
		&rating, actionTaken, feedback, now)
}

// insertActivity creates a SessionReviewActivity record within the transaction.
func (s *SessionService) insertActivity(
	ctx context.Context, tx *ent.Tx,
	sessionID, actor string,
	action sessionreviewactivity.Action,
	fromStatus *sessionreviewactivity.FromStatus,
	toStatus sessionreviewactivity.ToStatus,
	qualityRating *string,
	actionTaken *string,
	investigationFeedback *string,
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
		SetNillableNote(actionTaken). // note column stores the action_taken snapshot
		SetNillableInvestigationFeedback(investigationFeedback)

	if qualityRating != nil {
		create = create.SetQualityRating(sessionreviewactivity.QualityRating(*qualityRating))
	}

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

// GetTriageGroup returns a single paginated triage group.
func (s *SessionService) GetTriageGroup(ctx context.Context, group models.TriageGroupKey, params models.TriageGroupParams) (*models.TriageGroup, error) {
	predicates := triageGroupPredicates(group)
	if len(predicates) == 0 {
		return nil, NewValidationError("group", fmt.Sprintf("unknown triage group %q", group))
	}

	result, err := s.queryTriageGroup(ctx, params.Page, params.PageSize, params.Assignee, predicates...)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s sessions: %w", group, err)
	}
	return result, nil
}

// triageGroupPredicates returns the ent predicates for a given triage group key.
func triageGroupPredicates(group models.TriageGroupKey) []predicate.AlertSession {
	switch group {
	case models.TriageGroupInvestigating:
		return []predicate.AlertSession{
			alertsession.StatusIn(alertsession.StatusPending, alertsession.StatusInProgress, alertsession.StatusCancelling),
			alertsession.ReviewStatusIsNil(),
		}
	case models.TriageGroupNeedsReview:
		return []predicate.AlertSession{
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusNeedsReview),
		}
	case models.TriageGroupInProgress:
		return []predicate.AlertSession{
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusInProgress),
		}
	case models.TriageGroupReviewed:
		return []predicate.AlertSession{
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusReviewed),
		}
	default:
		return nil
	}
}

// triageRow is the scan target for the triage group query.
type triageRow struct {
	ID                    string     `sql:"session_id"`
	AlertType             *string    `sql:"alert_type"`
	ChainID               string     `sql:"chain_id"`
	Status                string     `sql:"status"`
	Author                *string    `sql:"author"`
	CreatedAt             time.Time  `sql:"created_at"`
	StartedAt             *time.Time `sql:"started_at"`
	CompletedAt           *time.Time `sql:"completed_at"`
	ErrorMessage          *string    `sql:"error_message"`
	ExecutiveSummary      *string    `sql:"executive_summary"`
	ReviewStatus          *string    `sql:"review_status"`
	Assignee              *string    `sql:"assignee"`
	QualityRating         *string    `sql:"quality_rating"`
	ActionTaken           *string    `sql:"action_taken"`
	InvestigationFeedback *string    `sql:"investigation_feedback"`
	HasParallel           int        `sql:"has_parallel"`
	HasSubAgents          int        `sql:"has_sub_agents"`
	HasActionStages       int        `sql:"has_action_stages"`
	ActionsExecuted       *bool      `sql:"actions_executed"`
	ChatMsgCount          int        `sql:"chat_msg_count"`
	FallbackCount         int        `sql:"fallback_count"`
	LatestScore           *int       `sql:"latest_score"`
	ScoringStatus         *string    `sql:"scoring_status"`
	FeedbackEdited        int        `sql:"feedback_edited"`
}

// queryTriageGroup counts and fetches a paginated slice of sessions matching
// the given predicates, returning a fully populated TriageGroup.
func (s *SessionService) queryTriageGroup(ctx context.Context, page, pageSize int, assignee *string, predicates ...predicate.AlertSession) (*models.TriageGroup, error) {
	if pageSize < 1 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}

	base := s.client.AlertSession.Query().
		Where(alertsession.DeletedAtIsNil()).
		Where(predicates...)

	if assignee != nil {
		if *assignee == "" {
			base = base.Where(alertsession.AssigneeIsNil())
		} else {
			base = base.Where(alertsession.AssigneeEQ(*assignee))
		}
	}

	total, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count: %w", err)
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * pageSize
	var rows []triageRow
	err = base.Clone().
		Order(ent.Desc(alertsession.FieldCreatedAt), ent.Desc(alertsession.FieldID)).
		Offset(offset).
		Limit(pageSize).
		Modify(func(sel *sql.Selector) {
			t := sel.TableName()
			sid := fmt.Sprintf("%q.%q", t, alertsession.FieldID)

			sel.Select(
				sel.C(alertsession.FieldID),
				sel.C(alertsession.FieldAlertType),
				sel.C(alertsession.FieldChainID),
				sel.C(alertsession.FieldStatus),
				sel.C(alertsession.FieldAuthor),
				sel.C(alertsession.FieldCreatedAt),
				sel.C(alertsession.FieldStartedAt),
				sel.C(alertsession.FieldCompletedAt),
				sel.C(alertsession.FieldErrorMessage),
				sel.C(alertsession.FieldExecutiveSummary),
				sel.C(alertsession.FieldReviewStatus),
				sel.C(alertsession.FieldAssignee),
				sel.C(alertsession.FieldQualityRating),
				sel.C(alertsession.FieldActionTaken),
				sel.C(alertsession.FieldInvestigationFeedback),
			)

			sel.AppendSelectAs(
				fmt.Sprintf("(CASE WHEN EXISTS(SELECT 1 FROM stages WHERE session_id = %s AND parallel_type IS NOT NULL) THEN 1 ELSE 0 END)", sid),
				"has_parallel",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(CASE WHEN EXISTS(SELECT 1 FROM agent_executions WHERE session_id = %s AND parent_execution_id IS NOT NULL) THEN 1 ELSE 0 END)", sid),
				"has_sub_agents",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(CASE WHEN EXISTS(SELECT 1 FROM stages WHERE session_id = %s AND stage_type = '%s' AND actions_executed IS NOT NULL) THEN 1 ELSE 0 END)", sid, stage.StageTypeAction),
				"has_action_stages",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT bool_or(actions_executed) FROM stages WHERE session_id = %s AND stage_type = '%s' AND actions_executed IS NOT NULL)", sid, stage.StageTypeAction),
				"actions_executed",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM chat_user_messages WHERE chat_id IN (SELECT chat_id FROM chats WHERE session_id = %s))", sid),
				"chat_msg_count",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM timeline_events WHERE session_id = %s AND event_type = 'provider_fallback')", sid),
				"fallback_count",
			)

			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT total_score FROM session_scores WHERE session_id = %s AND status = 'completed' ORDER BY started_at DESC LIMIT 1)", sid),
				"latest_score",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT status FROM session_scores WHERE session_id = %s ORDER BY started_at DESC LIMIT 1)", sid),
				"scoring_status",
			)

			sel.AppendSelectAs(
				fmt.Sprintf("(CASE WHEN EXISTS(SELECT 1 FROM session_review_activities WHERE session_id = %s AND action = 'update_feedback') THEN 1 ELSE 0 END)", sid),
				"feedback_edited",
			)
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	items := make([]models.DashboardSessionItem, 0, len(rows))
	for _, row := range rows {
		var durationMs *int64
		if row.StartedAt != nil && row.CompletedAt != nil {
			ms := row.CompletedAt.Sub(*row.StartedAt).Milliseconds()
			durationMs = &ms
		}
		items = append(items, models.DashboardSessionItem{
			ID:                    row.ID,
			AlertType:             row.AlertType,
			ChainID:               row.ChainID,
			Status:                row.Status,
			Author:                row.Author,
			CreatedAt:             row.CreatedAt,
			StartedAt:             row.StartedAt,
			CompletedAt:           row.CompletedAt,
			DurationMs:            durationMs,
			ErrorMessage:          row.ErrorMessage,
			ExecutiveSummary:      row.ExecutiveSummary,
			ReviewStatus:          row.ReviewStatus,
			Assignee:              row.Assignee,
			QualityRating:         row.QualityRating,
			ActionTaken:           row.ActionTaken,
			InvestigationFeedback: row.InvestigationFeedback,
			FeedbackEdited:        row.FeedbackEdited != 0,
			HasParallelStages:     row.HasParallel != 0,
			HasSubAgents:          row.HasSubAgents != 0,
			HasActionStages:       row.HasActionStages != 0,
			ActionsExecuted:       row.ActionsExecuted,
			ChatMessageCount:      row.ChatMsgCount,
			ProviderFallbackCount: row.FallbackCount,
			LatestScore:           row.LatestScore,
			ScoringStatus:         row.ScoringStatus,
		})
	}

	return &models.TriageGroup{
		Count:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
		Sessions:   items,
	}, nil
}

func ptrFromStatus(s sessionreviewactivity.FromStatus) *sessionreviewactivity.FromStatus {
	return &s
}
