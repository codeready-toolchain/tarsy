package models

import (
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
)

// CreateSessionRequest contains fields for creating a new alert session
type CreateSessionRequest struct {
	SessionID       string              `json:"session_id"`
	AlertData       string              `json:"alert_data"`
	AgentType       string              `json:"agent_type"`
	AlertType       string              `json:"alert_type,omitempty"`
	ChainID         string              `json:"chain_id"`
	Author          string              `json:"author,omitempty"`
	RunbookURL      string              `json:"runbook_url,omitempty"`
	MCPSelection    *MCPSelectionConfig `json:"mcp_selection,omitempty"`
	SessionMetadata map[string]any      `json:"session_metadata,omitempty"`
}

// SessionFilters contains filtering options for listing sessions
type SessionFilters struct {
	Status         string     `json:"status,omitempty"`
	AgentType      string     `json:"agent_type,omitempty"`
	AlertType      string     `json:"alert_type,omitempty"`
	ChainID        string     `json:"chain_id,omitempty"`
	Author         string     `json:"author,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	StartedBefore  *time.Time `json:"started_before,omitempty"`
	Limit          int        `json:"limit,omitempty"`
	Offset         int        `json:"offset,omitempty"`
	IncludeDeleted bool       `json:"include_deleted,omitempty"`
}

// SessionResponse wraps an AlertSession with optional loaded edges
type SessionResponse struct {
	*ent.AlertSession
	// Edges can be accessed via AlertSession.Edges when loaded
}

// SessionListResponse contains paginated session list
type SessionListResponse struct {
	Sessions   []*ent.AlertSession `json:"sessions"`
	TotalCount int                 `json:"total_count"`
	Limit      int                 `json:"limit"`
	Offset     int                 `json:"offset"`
}

// --- Dashboard DTOs ---

// DashboardListParams holds query parameters for the dashboard session list.
type DashboardListParams struct {
	Page      int        `json:"page"`       // 1-based
	PageSize  int        `json:"page_size"`  // max 100
	SortBy    string     `json:"sort_by"`    // created_at, status, alert_type, author, duration
	SortOrder string     `json:"sort_order"` // asc or desc
	Status    string     `json:"status"`     // comma-separated status filter
	AlertType string     `json:"alert_type"`
	ChainID   string     `json:"chain_id"`
	Search    string     `json:"search"`     // ILIKE on alert_data, final_analysis
	StartDate *time.Time `json:"start_date"` // created_at >= start_date
	EndDate   *time.Time `json:"end_date"`   // created_at < end_date
}

// DashboardSessionItem is a single session in the dashboard list with pre-computed stats.
type DashboardSessionItem struct {
	ID                  string     `json:"id"`
	AlertType           *string    `json:"alert_type"`
	ChainID             string     `json:"chain_id"`
	Status              string     `json:"status"`
	Author              *string    `json:"author"`
	CreatedAt           time.Time  `json:"created_at"`
	StartedAt           *time.Time `json:"started_at"`
	CompletedAt         *time.Time `json:"completed_at"`
	DurationMs          *int64     `json:"duration_ms"`
	ErrorMessage        *string    `json:"error_message"`
	ExecutiveSummary    *string    `json:"executive_summary"`
	LLMInteractionCount int        `json:"llm_interaction_count"`
	MCPInteractionCount int        `json:"mcp_interaction_count"`
	InputTokens         int64      `json:"input_tokens"`
	OutputTokens        int64      `json:"output_tokens"`
	TotalTokens         int64      `json:"total_tokens"`
	TotalStages         int        `json:"total_stages"`
	CompletedStages     int        `json:"completed_stages"`
	HasParallelStages   bool       `json:"has_parallel_stages"`
	ChatMessageCount    int        `json:"chat_message_count"`
	CurrentStageIndex   *int       `json:"current_stage_index"`
	CurrentStageID      *string    `json:"current_stage_id"`
}

// DashboardListResponse is the paginated session list response for the dashboard.
type DashboardListResponse struct {
	Sessions   []DashboardSessionItem `json:"sessions"`
	Pagination PaginationInfo         `json:"pagination"`
}

// PaginationInfo describes pagination state.
type PaginationInfo struct {
	Page       int `json:"page"`
	PageSize   int `json:"page_size"`
	TotalPages int `json:"total_pages"`
	TotalItems int `json:"total_items"`
}

// ActiveSessionsResponse is returned by GET /api/v1/sessions/active.
type ActiveSessionsResponse struct {
	Active []ActiveSessionItem `json:"active"`
	Queued []QueuedSessionItem `json:"queued"`
}

// ActiveSessionItem is an in-progress or cancelling session.
// TotalStages is intentionally omitted — clients get it from real-time
// progress events (SessionProgressPayload) or the dashboard list endpoint.
type ActiveSessionItem struct {
	ID                string     `json:"id"`
	AlertType         *string    `json:"alert_type"`
	ChainID           string     `json:"chain_id"`
	Status            string     `json:"status"`
	Author            *string    `json:"author"`
	CreatedAt         time.Time  `json:"created_at"`
	StartedAt         *time.Time `json:"started_at"`
	CurrentStageIndex *int       `json:"current_stage_index"`
	CurrentStageID    *string    `json:"current_stage_id"`
}

// QueuedSessionItem is a pending session waiting for a worker.
type QueuedSessionItem struct {
	ID            string    `json:"id"`
	AlertType     *string   `json:"alert_type"`
	ChainID       string    `json:"chain_id"`
	Status        string    `json:"status"`
	Author        *string   `json:"author"`
	CreatedAt     time.Time `json:"created_at"`
	QueuePosition int       `json:"queue_position"` // 1-based
}

// SessionDetailResponse is the enriched session detail DTO.
type SessionDetailResponse struct {
	// Core fields (from AlertSession)
	ID                    string         `json:"id"`
	AlertData             string         `json:"alert_data"`
	AlertType             *string        `json:"alert_type"`
	Status                string         `json:"status"`
	ChainID               string         `json:"chain_id"`
	Author                *string        `json:"author"`
	ErrorMessage          *string        `json:"error_message"`
	FinalAnalysis         *string        `json:"final_analysis"`
	ExecutiveSummary      *string        `json:"executive_summary"`
	ExecutiveSummaryError *string        `json:"executive_summary_error"`
	RunbookURL            *string        `json:"runbook_url"`
	MCPSelection          map[string]any `json:"mcp_selection,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`

	// Computed fields
	DurationMs          *int64  `json:"duration_ms"`
	ChatEnabled         bool    `json:"chat_enabled"`
	ChatID              *string `json:"chat_id"`
	ChatMessageCount    int     `json:"chat_message_count"`
	TotalStages         int     `json:"total_stages"`
	CompletedStages     int     `json:"completed_stages"`
	FailedStages        int     `json:"failed_stages"`
	HasParallelStages   bool    `json:"has_parallel_stages"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	TotalTokens         int64   `json:"total_tokens"`
	LLMInteractionCount int     `json:"llm_interaction_count"`
	MCPInteractionCount int     `json:"mcp_interaction_count"`
	CurrentStageIndex   *int    `json:"current_stage_index"`
	CurrentStageID      *string `json:"current_stage_id"`

	// Stage list
	Stages []StageOverview `json:"stages"`
}

// StageOverview is a summary of a stage within the session detail.
type StageOverview struct {
	ID                 string              `json:"id"`
	StageName          string              `json:"stage_name"`
	StageIndex         int                 `json:"stage_index"`
	Status             string              `json:"status"`
	ParallelType       *string             `json:"parallel_type"`
	ExpectedAgentCount int                 `json:"expected_agent_count"`
	StartedAt          *time.Time          `json:"started_at"`
	CompletedAt        *time.Time          `json:"completed_at"`
	Executions         []ExecutionOverview `json:"executions,omitempty"`
}

// ExecutionOverview is a summary of an agent execution within a stage.
type ExecutionOverview struct {
	ExecutionID       string     `json:"execution_id"`
	AgentName         string     `json:"agent_name"`
	AgentIndex        int        `json:"agent_index"`
	Status            string     `json:"status"`
	IterationStrategy string     `json:"iteration_strategy"`
	LLMProvider       *string    `json:"llm_provider"`
	StartedAt         *time.Time `json:"started_at"`
	CompletedAt       *time.Time `json:"completed_at"`
	DurationMs        *int64     `json:"duration_ms"`
	ErrorMessage      *string    `json:"error_message"`
	InputTokens       int64      `json:"input_tokens"`
	OutputTokens      int64      `json:"output_tokens"`
	TotalTokens       int64      `json:"total_tokens"`
}

// SessionSummaryResponse is returned by GET /api/v1/sessions/:id/summary.
type SessionSummaryResponse struct {
	SessionID         string          `json:"session_id"`
	TotalInteractions int             `json:"total_interactions"`
	LLMInteractions   int             `json:"llm_interactions"`
	MCPInteractions   int             `json:"mcp_interactions"`
	InputTokens       int64           `json:"input_tokens"`
	OutputTokens      int64           `json:"output_tokens"`
	TotalTokens       int64           `json:"total_tokens"`
	TotalDurationMs   *int64          `json:"total_duration_ms"`
	ChainStatistics   ChainStatistics `json:"chain_statistics"`
}

// ChainStatistics holds stage counts for the session summary.
type ChainStatistics struct {
	TotalStages       int  `json:"total_stages"`
	CompletedStages   int  `json:"completed_stages"`
	FailedStages      int  `json:"failed_stages"`
	CurrentStageIndex *int `json:"current_stage_index"`
}
