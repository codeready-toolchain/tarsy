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

	case models.ReviewActionUpdateNote:
		update := tx.AlertSession.Update().
			Where(
				alertsession.IDEQ(sessionID),
				alertsession.ReviewStatusEQ(alertsession.ReviewStatusResolved),
			)
		if req.Note != nil {
			update = update.SetResolutionNote(*req.Note)
		} else {
			update = update.ClearResolutionNote()
		}
		affected, err := update.Save(writeCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to update note: %w", err)
		}
		if affected == 0 {
			return nil, ErrConflict
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
	case models.TriageGroupResolved:
		return []predicate.AlertSession{
			alertsession.ReviewStatusEQ(alertsession.ReviewStatusResolved),
		}
	default:
		return nil
	}
}

// triageRow is the scan target for the triage group query.
type triageRow struct {
	ID               string     `sql:"session_id"`
	AlertType        *string    `sql:"alert_type"`
	ChainID          string     `sql:"chain_id"`
	Status           string     `sql:"status"`
	Author           *string    `sql:"author"`
	CreatedAt        time.Time  `sql:"created_at"`
	StartedAt        *time.Time `sql:"started_at"`
	CompletedAt      *time.Time `sql:"completed_at"`
	ErrorMessage     *string    `sql:"error_message"`
	ExecutiveSummary *string    `sql:"executive_summary"`
	ReviewStatus     *string    `sql:"review_status"`
	Assignee         *string    `sql:"assignee"`
	ResolutionReason *string    `sql:"resolution_reason"`
	ResolutionNote   *string    `sql:"resolution_note"`
	LatestScore      *int       `sql:"latest_score"`
	ScoringStatus    *string    `sql:"scoring_status"`
}

// queryTriageGroup counts and fetches a paginated slice of sessions matching
// the given predicates, returning a fully populated TriageGroup.
func (s *SessionService) queryTriageGroup(ctx context.Context, page, pageSize int, assignee string, predicates ...predicate.AlertSession) (*models.TriageGroup, error) {
	if pageSize < 1 {
		pageSize = 20
	}
	if page < 1 {
		page = 1
	}

	base := s.client.AlertSession.Query().
		Where(alertsession.DeletedAtIsNil()).
		Where(predicates...)

	if assignee != "" {
		base = base.Where(alertsession.AssigneeEQ(assignee))
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
		Order(ent.Desc(alertsession.FieldCreatedAt)).
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
				sel.C(alertsession.FieldResolutionReason),
				sel.C(alertsession.FieldResolutionNote),
			)

			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT total_score FROM session_scores WHERE session_id = %s AND status = 'completed' ORDER BY started_at DESC LIMIT 1)", sid),
				"latest_score",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT status FROM session_scores WHERE session_id = %s ORDER BY started_at DESC LIMIT 1)", sid),
				"scoring_status",
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
			ID:               row.ID,
			AlertType:        row.AlertType,
			ChainID:          row.ChainID,
			Status:           row.Status,
			Author:           row.Author,
			CreatedAt:        row.CreatedAt,
			StartedAt:        row.StartedAt,
			CompletedAt:      row.CompletedAt,
			DurationMs:       durationMs,
			ErrorMessage:     row.ErrorMessage,
			ExecutiveSummary: row.ExecutiveSummary,
			ReviewStatus:     row.ReviewStatus,
			Assignee:         row.Assignee,
			ResolutionReason: row.ResolutionReason,
			ResolutionNote:   row.ResolutionNote,
			LatestScore:      row.LatestScore,
			ScoringStatus:    row.ScoringStatus,
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
