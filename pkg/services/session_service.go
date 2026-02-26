package services

import (
	"context"
	stdsql "database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// SessionService manages alert session lifecycle
type SessionService struct {
	client            *ent.Client
	chainRegistry     *config.ChainRegistry
	mcpServerRegistry *config.MCPServerRegistry
}

// NewSessionService creates a new SessionService with configuration registries
func NewSessionService(client *ent.Client, chainRegistry *config.ChainRegistry, mcpServerRegistry *config.MCPServerRegistry) *SessionService {
	if chainRegistry == nil || mcpServerRegistry == nil {
		panic("NewSessionService: chainRegistry and mcpServerRegistry must not be nil")
	}
	return &SessionService{
		client:            client,
		chainRegistry:     chainRegistry,
		mcpServerRegistry: mcpServerRegistry,
	}
}

// CreateSession creates a new alert session with initial stage and agent execution
func (s *SessionService) CreateSession(_ context.Context, req models.CreateSessionRequest) (*ent.AlertSession, error) {
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

	// Validate chain exists in configuration (NEW)
	if _, err := s.chainRegistry.Get(req.ChainID); err != nil {
		return nil, NewValidationError("chain_id", fmt.Sprintf("invalid chain '%s': %v", req.ChainID, err))
	}

	// Validate MCP override if provided (NEW)
	if req.MCPSelection != nil {
		if err := s.validateMCPOverride(req.MCPSelection); err != nil {
			return nil, NewValidationError("mcp_selection", fmt.Sprintf("invalid: %v", err))
		}
	}

	// Use background context with timeout for critical write
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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
	// Note: created_at is set automatically by schema default
	// started_at will be set by the worker when it claims the session
	sessionBuilder := tx.AlertSession.Create().
		SetID(req.SessionID).
		SetAlertData(req.AlertData).
		SetAgentType(req.AgentType).
		SetChainID(req.ChainID).
		SetStatus(alertsession.StatusPending)

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
		SetLlmBackend(string(config.LLMBackendLangChain)). // Default LLM backend
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
	// Order by created_at (submission time) for consistent ordering
	sessions, err := query.
		Limit(limit).
		Offset(offset).
		Order(ent.Desc(alertsession.FieldCreatedAt)).
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
func (s *SessionService) UpdateSessionStatus(_ context.Context, sessionID string, status alertsession.Status) error {
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

// CancelSession requests cancellation of an in-progress session.
// Sets the DB status to "cancelling" (intermediate state).
// The owning worker detects this and propagates cancellation.
func (s *SessionService) CancelSession(_ context.Context, sessionID string) error {
	// Use background context with timeout for critical write
	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Conditional update: only update if session exists and is in_progress
	// This prevents TOCTOU race conditions
	count, err := s.client.AlertSession.Update().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.StatusEQ(alertsession.StatusInProgress),
		).
		SetStatus(alertsession.StatusCancelling).
		Save(bgCtx)
	if err != nil {
		return fmt.Errorf("failed to cancel session: %w", err)
	}

	// Check if the update actually modified a row
	if count == 0 {
		// Distinguish "not found" from "not in cancellable state"
		exists, err := s.client.AlertSession.Query().
			Where(alertsession.IDEQ(sessionID)).
			Exist(bgCtx)
		if err != nil {
			return fmt.Errorf("failed to check session existence: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
		return ErrNotCancellable
	}

	return nil
}

// SoftDeleteOldSessions soft deletes sessions older than retention period.
// Targets two categories:
//   - Completed/terminal sessions where completed_at < cutoff
//   - Pending sessions where created_at < cutoff (never claimed, safety net)
func (s *SessionService) SoftDeleteOldSessions(_ context.Context, retentionDays int) (int, error) {
	if retentionDays <= 0 {
		return 0, fmt.Errorf("retention_days must be positive, got %d", retentionDays)
	}

	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	deleteCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	count, err := s.client.AlertSession.Update().
		Where(
			alertsession.DeletedAtIsNil(),
			alertsession.Or(
				alertsession.CompletedAtLT(cutoff),
				alertsession.And(
					alertsession.StatusEQ(alertsession.StatusPending),
					alertsession.CreatedAtLT(cutoff),
				),
			),
		).
		SetDeletedAt(time.Now()).
		Save(deleteCtx)
	if err != nil {
		return 0, fmt.Errorf("failed to soft delete sessions: %w", err)
	}

	return count, nil
}

// RestoreSession restores a soft-deleted session
func (s *SessionService) RestoreSession(_ context.Context, sessionID string) error {
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
		Order(ent.Desc(alertsession.FieldCreatedAt)).
		All(ctx)

	if err != nil {
		return nil, fmt.Errorf("failed to search sessions: %w", err)
	}

	return sessions, nil
}

// GetSessionDetail returns an enriched session detail DTO with computed fields.
func (s *SessionService) GetSessionDetail(ctx context.Context, sessionID string) (*models.SessionDetailResponse, error) {
	session, err := s.client.AlertSession.Query().
		Where(alertsession.IDEQ(sessionID), alertsession.DeletedAtIsNil()).
		WithStages(func(q *ent.StageQuery) {
			q.Order(ent.Asc(stage.FieldStageIndex))
			q.WithAgentExecutions(func(eq *ent.AgentExecutionQuery) {
				eq.Where(agentexecution.ParentExecutionIDIsNil())
				eq.Order(ent.Asc(agentexecution.FieldAgentIndex))
				eq.WithSubAgents(func(sq *ent.AgentExecutionQuery) {
					sq.Order(ent.Asc(agentexecution.FieldAgentIndex))
				})
			})
		}).
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	// Compute token and interaction stats via aggregate queries.
	llmCount, inputTokens, outputTokens, totalTokens, err := s.aggregateLLMStats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	mcpCount, err := s.countMCPInteractions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Aggregate per-execution token stats in a single query.
	execTokens, err := s.aggregateExecutionTokenStats(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Compute stage stats.
	totalStages := len(session.Edges.Stages)
	completedStages := 0
	failedStages := 0
	hasParallel := false

	stages := make([]models.StageOverview, 0, totalStages)
	for _, stg := range session.Edges.Stages {
		if stg.Status == stage.StatusCompleted {
			completedStages++
		}
		if stg.Status == stage.StatusFailed {
			failedStages++
		}
		if stg.ParallelType != nil {
			hasParallel = true
		}

		var pt *string
		if stg.ParallelType != nil {
			s := string(*stg.ParallelType)
			pt = &s
		}

		// Build execution overviews for this stage.
		// The query already filters for top-level executions only;
		// sub-agents are nested via the SubAgents field.
		var execOverviews []models.ExecutionOverview
		if stg.Edges.AgentExecutions != nil {
			execOverviews = make([]models.ExecutionOverview, 0, len(stg.Edges.AgentExecutions))
			for _, exec := range stg.Edges.AgentExecutions {
				overview := buildExecutionOverview(exec, execTokens)

				if exec.Edges.SubAgents != nil {
					overview.SubAgents = make([]models.ExecutionOverview, 0, len(exec.Edges.SubAgents))
					for _, sub := range exec.Edges.SubAgents {
						overview.SubAgents = append(overview.SubAgents, buildExecutionOverview(sub, execTokens))
					}
				}

				execOverviews = append(execOverviews, overview)
			}
		}

		stages = append(stages, models.StageOverview{
			ID:                 stg.ID,
			StageName:          stg.StageName,
			StageIndex:         stg.StageIndex,
			Status:             string(stg.Status),
			ParallelType:       pt,
			ExpectedAgentCount: stg.ExpectedAgentCount,
			StartedAt:          stg.StartedAt,
			CompletedAt:        stg.CompletedAt,
			Executions:         execOverviews,
		})
	}

	// Compute chat info — enabled by default unless explicitly disabled in chain config.
	chatEnabled := true
	if chain, chainErr := s.chainRegistry.Get(session.ChainID); chainErr == nil {
		if chain.Chat != nil && !chain.Chat.Enabled {
			chatEnabled = false
		}
	}
	var chatID *string
	chatMessageCount := 0
	if session.Edges.Chat != nil {
		chatID = &session.Edges.Chat.ID
		count, countErr := session.Edges.Chat.QueryUserMessages().Count(ctx)
		if countErr != nil {
			return nil, fmt.Errorf("failed to count chat messages: %w", countErr)
		}
		chatMessageCount = count
	}

	// Compute duration.
	var durationMs *int64
	if session.StartedAt != nil && session.CompletedAt != nil {
		ms := session.CompletedAt.Sub(*session.StartedAt).Milliseconds()
		durationMs = &ms
	}

	// Map optional string fields — Ent uses string for optional fields with omitempty.
	var alertType *string
	if session.AlertType != "" {
		alertType = &session.AlertType
	}

	return &models.SessionDetailResponse{
		ID:                      session.ID,
		AlertData:               session.AlertData,
		AlertType:               alertType,
		Status:                  string(session.Status),
		ChainID:                 session.ChainID,
		Author:                  session.Author,
		ErrorMessage:            session.ErrorMessage,
		FinalAnalysis:           session.FinalAnalysis,
		ExecutiveSummary:        session.ExecutiveSummary,
		ExecutiveSummaryError:   session.ExecutiveSummaryError,
		RunbookURL:              session.RunbookURL,
		SlackMessageFingerprint: session.SlackMessageFingerprint,
		MCPSelection:            session.McpSelection,
		CreatedAt:               session.CreatedAt,
		StartedAt:               session.StartedAt,
		CompletedAt:             session.CompletedAt,
		DurationMs:              durationMs,
		ChatEnabled:             chatEnabled,
		ChatID:                  chatID,
		ChatMessageCount:        chatMessageCount,
		TotalStages:             totalStages,
		CompletedStages:         completedStages,
		FailedStages:            failedStages,
		HasParallelStages:       hasParallel,
		InputTokens:             inputTokens,
		OutputTokens:            outputTokens,
		TotalTokens:             totalTokens,
		LLMInteractionCount:     llmCount,
		MCPInteractionCount:     mcpCount,
		CurrentStageIndex:       session.CurrentStageIndex,
		CurrentStageID:          session.CurrentStageID,
		Stages:                  stages,
	}, nil
}

// GetSessionSummary returns lightweight statistics for a session.
func (s *SessionService) GetSessionSummary(ctx context.Context, sessionID string) (*models.SessionSummaryResponse, error) {
	session, err := s.client.AlertSession.Query().
		Where(alertsession.IDEQ(sessionID), alertsession.DeletedAtIsNil()).
		WithStages().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	llmCount, inputTokens, outputTokens, totalTokens, err := s.aggregateLLMStats(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	mcpCount, err := s.countMCPInteractions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	totalStages := len(session.Edges.Stages)
	completedStages := 0
	failedStages := 0
	for _, stg := range session.Edges.Stages {
		if stg.Status == stage.StatusCompleted {
			completedStages++
		}
		if stg.Status == stage.StatusFailed {
			failedStages++
		}
	}

	var durationMs *int64
	if session.StartedAt != nil && session.CompletedAt != nil {
		ms := session.CompletedAt.Sub(*session.StartedAt).Milliseconds()
		durationMs = &ms
	}

	return &models.SessionSummaryResponse{
		SessionID:         sessionID,
		TotalInteractions: llmCount + mcpCount,
		LLMInteractions:   llmCount,
		MCPInteractions:   mcpCount,
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		TotalTokens:       totalTokens,
		TotalDurationMs:   durationMs,
		ChainStatistics: models.ChainStatistics{
			TotalStages:       totalStages,
			CompletedStages:   completedStages,
			FailedStages:      failedStages,
			CurrentStageIndex: session.CurrentStageIndex,
		},
	}, nil
}

// GetSessionStatus returns the minimal polling-friendly status for a session.
// Single PK lookup, no edge-loading, no aggregate queries.
func (s *SessionService) GetSessionStatus(ctx context.Context, sessionID string) (*models.SessionStatusResponse, error) {
	session, err := s.client.AlertSession.Query().
		Where(
			alertsession.IDEQ(sessionID),
			alertsession.DeletedAtIsNil(),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to get session status: %w", err)
	}

	return &models.SessionStatusResponse{
		ID:               session.ID,
		Status:           string(session.Status),
		FinalAnalysis:    session.FinalAnalysis,
		ExecutiveSummary: session.ExecutiveSummary,
		ErrorMessage:     session.ErrorMessage,
	}, nil
}

// GetActiveSessions returns in-progress + pending sessions.
func (s *SessionService) GetActiveSessions(ctx context.Context) (*models.ActiveSessionsResponse, error) {
	// Active sessions (in_progress or cancelling).
	activeSessions, err := s.client.AlertSession.Query().
		Where(
			alertsession.DeletedAtIsNil(),
			alertsession.StatusIn(alertsession.StatusInProgress, alertsession.StatusCancelling),
		).
		Order(ent.Asc(alertsession.FieldStartedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query active sessions: %w", err)
	}

	// Queued sessions (pending).
	queuedSessions, err := s.client.AlertSession.Query().
		Where(
			alertsession.DeletedAtIsNil(),
			alertsession.StatusEQ(alertsession.StatusPending),
		).
		Order(ent.Asc(alertsession.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query queued sessions: %w", err)
	}

	active := make([]models.ActiveSessionItem, 0, len(activeSessions))
	for _, sess := range activeSessions {
		var alertType *string
		if sess.AlertType != "" {
			alertType = &sess.AlertType
		}

		active = append(active, models.ActiveSessionItem{
			ID:                sess.ID,
			AlertType:         alertType,
			ChainID:           sess.ChainID,
			Status:            string(sess.Status),
			Author:            sess.Author,
			CreatedAt:         sess.CreatedAt,
			StartedAt:         sess.StartedAt,
			CurrentStageIndex: sess.CurrentStageIndex,
			CurrentStageID:    sess.CurrentStageID,
		})
	}

	queued := make([]models.QueuedSessionItem, 0, len(queuedSessions))
	for i, sess := range queuedSessions {
		var alertType *string
		if sess.AlertType != "" {
			alertType = &sess.AlertType
		}

		queued = append(queued, models.QueuedSessionItem{
			ID:            sess.ID,
			AlertType:     alertType,
			ChainID:       sess.ChainID,
			Status:        string(sess.Status),
			Author:        sess.Author,
			CreatedAt:     sess.CreatedAt,
			QueuePosition: i + 1,
		})
	}

	return &models.ActiveSessionsResponse{
		Active: active,
		Queued: queued,
	}, nil
}

// dashboardRow is the scan target for the single-query dashboard list.
// Uses explicit sql tags for column mapping since Scan doesn't support entity embedding.
type dashboardRow struct {
	// Entity fields.
	ID                string     `sql:"session_id"`
	AlertType         *string    `sql:"alert_type"` // nullable in DB
	ChainID           string     `sql:"chain_id"`
	Status            string     `sql:"status"`
	Author            *string    `sql:"author"`
	CreatedAt         time.Time  `sql:"created_at"`
	StartedAt         *time.Time `sql:"started_at"`
	CompletedAt       *time.Time `sql:"completed_at"`
	ErrorMessage      *string    `sql:"error_message"`
	ExecutiveSummary  *string    `sql:"executive_summary"`
	CurrentStageIndex *int       `sql:"current_stage_index"`
	CurrentStageID    *string    `sql:"current_stage_id"`
	// Aggregated columns from subqueries.
	LLMCount        int   `sql:"llm_count"`
	LLMInputTokens  int64 `sql:"llm_input_tokens"`
	LLMOutputTokens int64 `sql:"llm_output_tokens"`
	LLMTotalTokens  int64 `sql:"llm_total_tokens"`
	MCPCount        int   `sql:"mcp_count"`
	TotalStages     int   `sql:"total_stages"`
	CompletedStages int   `sql:"completed_stages"`
	HasParallel     int   `sql:"has_parallel"` // 0/1, mapped to bool on output
	ChatMsgCount    int   `sql:"chat_msg_count"`
}

// ListSessionsForDashboard returns a paginated, filtered session list with aggregated stats.
// All aggregated statistics are computed via SQL subqueries in a single query to avoid N+1.
func (s *SessionService) ListSessionsForDashboard(ctx context.Context, params models.DashboardListParams) (*models.DashboardListResponse, error) {
	query := s.client.AlertSession.Query().Where(alertsession.DeletedAtIsNil())

	// Apply filters.
	if params.Status != "" {
		statuses := strings.Split(params.Status, ",")
		entStatuses := make([]alertsession.Status, 0, len(statuses))
		for _, st := range statuses {
			entStatuses = append(entStatuses, alertsession.Status(st))
		}
		query = query.Where(alertsession.StatusIn(entStatuses...))
	}
	if params.AlertType != "" {
		query = query.Where(alertsession.AlertTypeEQ(params.AlertType))
	}
	if params.ChainID != "" {
		query = query.Where(alertsession.ChainIDEQ(params.ChainID))
	}
	if params.Search != "" {
		query = query.Where(func(sel *sql.Selector) {
			sel.Where(sql.Or(
				sql.ContainsFold(alertsession.FieldAlertData, params.Search),
				sql.ContainsFold(alertsession.FieldFinalAnalysis, params.Search),
			))
		})
	}
	if params.StartDate != nil {
		query = query.Where(alertsession.CreatedAtGTE(*params.StartDate))
	}
	if params.EndDate != nil {
		query = query.Where(alertsession.CreatedAtLT(*params.EndDate))
	}

	// Count total (before pagination).
	totalCount, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count sessions: %w", err)
	}

	// Apply sorting. Duration sort uses a computed expression; others use Ent helpers.
	isDurationSort := params.SortBy == "duration"
	if !isDurationSort {
		orderFunc := ent.Desc(alertsession.FieldCreatedAt)
		switch params.SortBy {
		case "created_at":
			if params.SortOrder == "asc" {
				orderFunc = ent.Asc(alertsession.FieldCreatedAt)
			} else {
				orderFunc = ent.Desc(alertsession.FieldCreatedAt)
			}
		case "status":
			if params.SortOrder == "asc" {
				orderFunc = ent.Asc(alertsession.FieldStatus)
			} else {
				orderFunc = ent.Desc(alertsession.FieldStatus)
			}
		case "alert_type":
			if params.SortOrder == "asc" {
				orderFunc = ent.Asc(alertsession.FieldAlertType)
			} else {
				orderFunc = ent.Desc(alertsession.FieldAlertType)
			}
		case "author":
			if params.SortOrder == "asc" {
				orderFunc = ent.Asc(alertsession.FieldAuthor)
			} else {
				orderFunc = ent.Desc(alertsession.FieldAuthor)
			}
		}
		query = query.Order(orderFunc)
	}

	// Clamp pagination params to safe values (defensive against zero/negative).
	pageSize := params.PageSize
	if pageSize < 1 {
		pageSize = 25
	}
	page := params.Page
	if page < 1 {
		page = 1
	}

	// Paginate.
	offset := (page - 1) * pageSize

	// Scan with aggregate subqueries in a single query.
	var rows []dashboardRow
	err = query.
		Limit(pageSize).
		Offset(offset).
		Modify(func(sel *sql.Selector) {
			t := sel.TableName()
			sid := fmt.Sprintf("%q.%q", t, alertsession.FieldID)

			// Explicitly select only the entity columns we need (avoids unmapped column errors).
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
				sel.C(alertsession.FieldCurrentStageIndex),
				sel.C(alertsession.FieldCurrentStageID),
			)

			// LLM interaction aggregates.
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM llm_interactions WHERE session_id = %s)", sid),
				"llm_count",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COALESCE(SUM(input_tokens), 0) FROM llm_interactions WHERE session_id = %s)", sid),
				"llm_input_tokens",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COALESCE(SUM(output_tokens), 0) FROM llm_interactions WHERE session_id = %s)", sid),
				"llm_output_tokens",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COALESCE(SUM(total_tokens), 0) FROM llm_interactions WHERE session_id = %s)", sid),
				"llm_total_tokens",
			)

			// MCP interaction count.
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM mcp_interactions WHERE session_id = %s)", sid),
				"mcp_count",
			)

			// Stage aggregates.
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM stages WHERE session_id = %s)", sid),
				"total_stages",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM stages WHERE session_id = %s AND status = 'completed')", sid),
				"completed_stages",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(CASE WHEN EXISTS(SELECT 1 FROM stages WHERE session_id = %s AND parallel_type IS NOT NULL) THEN 1 ELSE 0 END)", sid),
				"has_parallel",
			)

			// Chat message count (chat_user_messages → chats → session).
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COUNT(*) FROM chat_user_messages WHERE chat_id IN (SELECT chat_id FROM chats WHERE session_id = %s))", sid),
				"chat_msg_count",
			)

			// Duration sort: ORDER BY (completed_at - started_at).
			if isDurationSort {
				dir := "DESC"
				if params.SortOrder == "asc" {
					dir = "ASC"
				}
				sel.OrderExpr(sql.Expr(fmt.Sprintf("(completed_at - started_at) %s NULLS LAST", dir)))
			}
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	// Build response items from scanned rows.
	items := make([]models.DashboardSessionItem, 0, len(rows))
	for _, row := range rows {
		var durationMs *int64
		if row.StartedAt != nil && row.CompletedAt != nil {
			ms := row.CompletedAt.Sub(*row.StartedAt).Milliseconds()
			durationMs = &ms
		}

		items = append(items, models.DashboardSessionItem{
			ID:                  row.ID,
			AlertType:           row.AlertType,
			ChainID:             row.ChainID,
			Status:              row.Status,
			Author:              row.Author,
			CreatedAt:           row.CreatedAt,
			StartedAt:           row.StartedAt,
			CompletedAt:         row.CompletedAt,
			DurationMs:          durationMs,
			ErrorMessage:        row.ErrorMessage,
			ExecutiveSummary:    row.ExecutiveSummary,
			LLMInteractionCount: row.LLMCount,
			MCPInteractionCount: row.MCPCount,
			InputTokens:         row.LLMInputTokens,
			OutputTokens:        row.LLMOutputTokens,
			TotalTokens:         row.LLMTotalTokens,
			TotalStages:         row.TotalStages,
			CompletedStages:     row.CompletedStages,
			HasParallelStages:   row.HasParallel != 0,
			ChatMessageCount:    row.ChatMsgCount,
			CurrentStageIndex:   row.CurrentStageIndex,
			CurrentStageID:      row.CurrentStageID,
		})
	}

	totalPages := 0
	if totalCount > 0 {
		totalPages = (totalCount + pageSize - 1) / pageSize
	}

	return &models.DashboardListResponse{
		Sessions: items,
		Pagination: models.PaginationInfo{
			Page:       page,
			PageSize:   pageSize,
			TotalPages: totalPages,
			TotalItems: totalCount,
		},
	}, nil
}

// --- Aggregate helpers ---

// aggregateLLMStats returns LLM interaction count and token sums for a session.
func (s *SessionService) aggregateLLMStats(ctx context.Context, sessionID string) (count int, inputTokens, outputTokens, totalTokens int64, err error) {
	// SUM returns NULL when all values are NULL (nullable token columns).
	// Use sql.NullInt64 to avoid scan errors and default to 0.
	var results []struct {
		Count     int              `json:"count"`
		InputSum  stdsql.NullInt64 `json:"input_sum"`
		OutputSum stdsql.NullInt64 `json:"output_sum"`
		TotalSum  stdsql.NullInt64 `json:"total_sum"`
	}

	err = s.client.LLMInteraction.Query().
		Where(llminteraction.SessionIDEQ(sessionID)).
		Aggregate(
			ent.As(ent.Count(), "count"),
			ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
			ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
			ent.As(ent.Sum(llminteraction.FieldTotalTokens), "total_sum"),
		).
		Scan(ctx, &results)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to aggregate LLM stats: %w", err)
	}

	if len(results) > 0 {
		return results[0].Count, results[0].InputSum.Int64, results[0].OutputSum.Int64, results[0].TotalSum.Int64, nil
	}
	return 0, 0, 0, 0, nil
}

// executionTokenStats holds per-execution token sums.
type executionTokenStats struct {
	Input  int64
	Output int64
	Total  int64
}

// aggregateExecutionTokenStats returns per-execution token sums for a session.
func (s *SessionService) aggregateExecutionTokenStats(ctx context.Context, sessionID string) (map[string]executionTokenStats, error) {
	var rows []struct {
		ExecutionID stdsql.NullString `json:"execution_id"`
		InputSum    stdsql.NullInt64  `json:"input_sum"`
		OutputSum   stdsql.NullInt64  `json:"output_sum"`
		TotalSum    stdsql.NullInt64  `json:"total_sum"`
	}

	err := s.client.LLMInteraction.Query().
		Where(llminteraction.SessionIDEQ(sessionID)).
		GroupBy(llminteraction.FieldExecutionID).
		Aggregate(
			ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
			ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
			ent.As(ent.Sum(llminteraction.FieldTotalTokens), "total_sum"),
		).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate execution token stats: %w", err)
	}

	result := make(map[string]executionTokenStats, len(rows))
	for _, row := range rows {
		if !row.ExecutionID.Valid {
			continue
		}
		result[row.ExecutionID.String] = executionTokenStats{
			Input:  row.InputSum.Int64,
			Output: row.OutputSum.Int64,
			Total:  row.TotalSum.Int64,
		}
	}
	return result, nil
}

// countMCPInteractions returns the MCP interaction count for a session.
func (s *SessionService) countMCPInteractions(ctx context.Context, sessionID string) (int, error) {
	count, err := s.client.MCPInteraction.Query().
		Where(mcpinteraction.SessionIDEQ(sessionID)).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count MCP interactions: %w", err)
	}
	return count, nil
}

// GetDistinctAlertTypes returns distinct alert_type values from non-deleted sessions.
func (s *SessionService) GetDistinctAlertTypes(ctx context.Context) ([]string, error) {
	var results []string
	err := s.client.AlertSession.Query().
		Where(
			alertsession.DeletedAtIsNil(),
			alertsession.AlertTypeNotNil(),
		).
		Unique(true).
		Select(alertsession.FieldAlertType).
		Scan(ctx, &results)
	if err != nil {
		return nil, fmt.Errorf("failed to get distinct alert types: %w", err)
	}
	if results == nil {
		results = []string{}
	}
	return results, nil
}

// GetDistinctChainIDs returns distinct chain_id values from non-deleted sessions.
func (s *SessionService) GetDistinctChainIDs(ctx context.Context) ([]string, error) {
	var results []string
	err := s.client.AlertSession.Query().
		Where(alertsession.DeletedAtIsNil()).
		Unique(true).
		Select(alertsession.FieldChainID).
		Scan(ctx, &results)
	if err != nil {
		return nil, fmt.Errorf("failed to get distinct chain IDs: %w", err)
	}
	if results == nil {
		results = []string{}
	}
	return results, nil
}

// buildExecutionOverview creates an ExecutionOverview from an AgentExecution entity.
func buildExecutionOverview(exec *ent.AgentExecution, execTokens map[string]executionTokenStats) models.ExecutionOverview {
	tokens := execTokens[exec.ID]
	var durationMs *int64
	if exec.DurationMs != nil {
		v := int64(*exec.DurationMs)
		durationMs = &v
	}
	return models.ExecutionOverview{
		ExecutionID:       exec.ID,
		AgentName:         exec.AgentName,
		AgentIndex:        exec.AgentIndex,
		Status:            string(exec.Status),
		LLMBackend:        exec.LlmBackend,
		LLMProvider:       exec.LlmProvider,
		StartedAt:         exec.StartedAt,
		CompletedAt:       exec.CompletedAt,
		DurationMs:        durationMs,
		ErrorMessage:      exec.ErrorMessage,
		InputTokens:       tokens.Input,
		OutputTokens:      tokens.Output,
		TotalTokens:       tokens.Total,
		ParentExecutionID: exec.ParentExecutionID,
		Task:              exec.Task,
	}
}

// validateMCPOverride validates MCP server selection override
func (s *SessionService) validateMCPOverride(mcp *models.MCPSelectionConfig) error {
	// Validate all MCP server names exist in registry
	for _, server := range mcp.Servers {
		if _, err := s.mcpServerRegistry.Get(server.Name); err != nil {
			return fmt.Errorf("MCP server '%s' not found: %w", server.Name, err)
		}
	}
	return nil
}
