package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// SessionService manages alert session lifecycle
type SessionService struct {
	client *ent.Client
}

// NewSessionService creates a new SessionService
func NewSessionService(client *ent.Client) *SessionService {
	return &SessionService{client: client}
}

// CreateSession creates a new alert session with initial stage and agent execution
func (s *SessionService) CreateSession(httpCtx context.Context, req models.CreateSessionRequest) (*ent.AlertSession, error) {
	// Validate input
	if req.SessionID == "" {
		return nil, NewValidationError("session_id", "required")
	}
	if req.AlertData == "" {
		return nil, NewValidationError("alert_data", "required")
	}
	if req.AgentType == "" {
		return nil, NewValidationError("agent_type", "required")
	}
	if req.ChainID == "" {
		return nil, NewValidationError("chain_id", "required")
	}

	// Use background context with timeout for critical write
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Convert MCP selection to JSON if provided
	var mcpSelectionJSON map[string]any
	if req.MCPSelection != nil {
		mcpBytes, err := json.Marshal(req.MCPSelection)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal mcp_selection: %w", err)
		}
		if err := json.Unmarshal(mcpBytes, &mcpSelectionJSON); err != nil {
			return nil, fmt.Errorf("failed to unmarshal mcp_selection: %w", err)
		}
	}

	// Create session
	sessionBuilder := tx.AlertSession.Create().
		SetID(req.SessionID).
		SetAlertData(req.AlertData).
		SetAgentType(req.AgentType).
		SetChainID(req.ChainID).
		SetStatus(alertsession.StatusPending).
		SetStartedAt(time.Now())

	if req.AlertType != "" {
		sessionBuilder.SetAlertType(req.AlertType)
	}
	if req.Author != "" {
		sessionBuilder.SetAuthor(req.Author)
	}
	if req.RunbookURL != "" {
		sessionBuilder.SetRunbookURL(req.RunbookURL)
	}
	if mcpSelectionJSON != nil {
		sessionBuilder.SetMcpSelection(mcpSelectionJSON)
	}
	if req.SessionMetadata != nil {
		sessionBuilder.SetSessionMetadata(req.SessionMetadata)
	}

	session, err := sessionBuilder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, ErrAlreadyExists
		}
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Create initial stage (stage 0)
	stageID := uuid.New().String()
	stg, err := tx.Stage.Create().
		SetID(stageID).
		SetSessionID(session.ID).
		SetStageName("Initial Analysis").
		SetStageIndex(0).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusPending).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create initial stage: %w", err)
	}

	// Create initial agent execution
	executionID := uuid.New().String()
	_, err = tx.AgentExecution.Create().
		SetID(executionID).
		SetStageID(stg.ID).
		SetSessionID(session.ID).
		SetAgentName(req.AgentType). // Use agent_type as initial agent name
		SetAgentIndex(1).
		SetStatus(agentexecution.StatusPending).
		SetIterationStrategy("react"). // Default strategy
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create initial agent execution: %w", err)
	}

	// Update session with current stage
	session, err = session.Update().
		SetCurrentStageIndex(0).
		SetCurrentStageID(stg.ID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update session current stage: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return session, nil
}

// GetSession retrieves a session by ID with optional edge loading
func (s *SessionService) GetSession(ctx context.Context, sessionID string, withEdges bool) (*ent.AlertSession, error) {
	query := s.client.AlertSession.Query().Where(alertsession.IDEQ(sessionID))

	if withEdges {
		query = query.
			WithStages(func(q *ent.StageQuery) {
				q.WithAgentExecutions().Order(ent.Asc(stage.FieldStageIndex))
			}).
			WithChat()
	}

	session, err := query.Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	return session, nil
}

// ListSessions lists sessions with filtering and pagination
func (s *SessionService) ListSessions(ctx context.Context, filters models.SessionFilters) (*models.SessionListResponse, error) {
	query := s.client.AlertSession.Query()

	// Apply filters
	if filters.Status != "" {
		query = query.Where(alertsession.StatusEQ(alertsession.Status(filters.Status)))
	}
	if filters.AgentType != "" {
		query = query.Where(alertsession.AgentTypeEQ(filters.AgentType))
	}
	if filters.AlertType != "" {
		query = query.Where(alertsession.AlertTypeEQ(filters.AlertType))
	}
	if filters.ChainID != "" {
		query = query.Where(alertsession.ChainIDEQ(filters.ChainID))
	}
	if filters.Author != "" {
		query = query.Where(alertsession.AuthorEQ(filters.Author))
	}
	if filters.StartedAt != nil {
		query = query.Where(alertsession.StartedAtGTE(*filters.StartedAt))
	}
	if filters.StartedBefore != nil {
		query = query.Where(alertsession.StartedAtLT(*filters.StartedBefore))
	}
	if !filters.IncludeDeleted {
		query = query.Where(alertsession.DeletedAtIsNil())
	}

	// Count total
	totalCount, err := query.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count sessions: %w", err)
	}

	// Apply pagination
	limit := filters.Limit
	if limit <= 0 {
		limit = 20 // Default
	}
	offset := filters.Offset
	if offset < 0 {
		offset = 0
	}

	// Get sessions
	sessions, err := query.
		Limit(limit).
		Offset(offset).
		Order(ent.Desc(alertsession.FieldStartedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	return &models.SessionListResponse{
		Sessions:   sessions,
		TotalCount: totalCount,
		Limit:      limit,
		Offset:     offset,
	}, nil
}

// UpdateSessionStatus updates a session's status
func (s *SessionService) UpdateSessionStatus(ctx context.Context, sessionID string, status alertsession.Status) error {
	// Use background context with timeout for critical write
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := s.client.AlertSession.UpdateOneID(sessionID).
		SetStatus(status).
		SetLastInteractionAt(time.Now())

	if status == alertsession.StatusCompleted ||
		status == alertsession.StatusFailed ||
		status == alertsession.StatusCancelled ||
		status == alertsession.StatusTimedOut {
		update = update.SetCompletedAt(time.Now())
	}

	err := update.Exec(writeCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to update session status: %w", err)
	}

	return nil
}

// ClaimNextPendingSession atomically claims a pending session
// Note: This uses a simple transaction approach. In production with high concurrency,
// consider using UPDATE ... WHERE ... RETURNING with FOR UPDATE SKIP LOCKED via raw SQL.
func (s *SessionService) ClaimNextPendingSession(ctx context.Context, podID string) (*ent.AlertSession, error) {
	// Use background context with timeout
	claimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := s.client.Tx(claimCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	// Find first pending session
	session, err := tx.AlertSession.Query().
		Where(alertsession.StatusEQ(alertsession.StatusPending)).
		Order(ent.Asc(alertsession.FieldStartedAt)).
		First(claimCtx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending sessions
		}
		return nil, fmt.Errorf("failed to query pending session: %w", err)
	}

	// Conditional update: only update if still pending
	count, err := tx.AlertSession.Update().
		Where(
			alertsession.IDEQ(session.ID),
			alertsession.StatusEQ(alertsession.StatusPending),
		).
		SetStatus(alertsession.StatusInProgress).
		SetPodID(podID).
		SetLastInteractionAt(time.Now()).
		Save(claimCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to claim session: %w", err)
	}

	// Check if the update actually claimed the row
	if count == 0 {
		// Session was already claimed by another process
		return nil, nil
	}

	// Refetch the updated session
	session, err = tx.AlertSession.Get(claimCtx, session.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to refetch claimed session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claim: %w", err)
	}

	return session, nil
}

// FindOrphanedSessions finds sessions stuck in-progress past timeout
func (s *SessionService) FindOrphanedSessions(ctx context.Context, timeoutDuration time.Duration) ([]*ent.AlertSession, error) {
	threshold := time.Now().Add(-timeoutDuration)

	sessions, err := s.client.AlertSession.Query().
		Where(
			alertsession.StatusEQ(alertsession.StatusInProgress),
			alertsession.LastInteractionAtNotNil(),
			alertsession.LastInteractionAtLT(threshold),
		).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to find orphaned sessions: %w", err)
	}

	return sessions, nil
}

// SoftDeleteOldSessions soft deletes sessions older than retention period
func (s *SessionService) SoftDeleteOldSessions(ctx context.Context, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, fmt.Errorf("retention_days must be positive, got %d", retentionDays)
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	// Use background context with timeout
	deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := s.client.AlertSession.Update().
		Where(
			alertsession.CompletedAtLT(cutoff),
			alertsession.DeletedAtIsNil(),
		).
		SetDeletedAt(time.Now()).
		Save(deleteCtx)
	if err != nil {
		return 0, fmt.Errorf("failed to soft delete sessions: %w", err)
	}

	return count, nil
}

// RestoreSession restores a soft-deleted session
func (s *SessionService) RestoreSession(ctx context.Context, sessionID string) error {
	// Use background context with timeout
	restoreCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.client.AlertSession.UpdateOneID(sessionID).
		ClearDeletedAt().
		Exec(restoreCtx)
	if err != nil {
		if ent.IsNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("failed to restore session: %w", err)
	}

	return nil
}

// SearchSessions performs full-text search on alert_data and final_analysis
func (s *SessionService) SearchSessions(ctx context.Context, query string, limit int) ([]*ent.AlertSession, error) {
	if limit <= 0 {
		limit = 20
	}

	sessions, err := s.client.AlertSession.Query().
		Where(alertsession.DeletedAtIsNil()).
		Where(func(sel *sql.Selector) {
			sel.Where(sql.Or(
				sql.ExprP("to_tsvector('english', alert_data) @@ plainto_tsquery($1)", query),
				sql.ExprP("to_tsvector('english', COALESCE(final_analysis, '')) @@ plainto_tsquery($2)", query),
			))
		}).
		Limit(limit).
		Order(ent.Desc(alertsession.FieldStartedAt)).
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to search sessions: %w", err)
	}

	return sessions, nil
}
