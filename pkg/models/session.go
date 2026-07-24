package models

import (
	"fmt"
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
	Page          int        `json:"page"`       // 1-based
	PageSize      int        `json:"page_size"`  // max 100
	SortBy        string     `json:"sort_by"`    // created_at, status, alert_type, author, duration, score
	SortOrder     string     `json:"sort_order"` // asc or desc
	Status        string     `json:"status"`     // comma-separated status filter
	AlertType     string     `json:"alert_type"`
	ChainID       string     `json:"chain_id"`
	Search        string     `json:"search"`         // ILIKE on alert_data, final_analysis
	StartDate     *time.Time `json:"start_date"`     // created_at >= start_date
	EndDate       *time.Time `json:"end_date"`       // created_at < end_date
	ScoringStatus string     `json:"scoring_status"` // scored, not_scored, scoring_in_progress, scoring_failed
	ReviewStatus  string     `json:"review_status"`  // comma-separated: needs_review, in_progress, reviewed
	Assignee      string     `json:"assignee"`       // exact match filter
	QualityRating string     `json:"quality_rating"` // accurate, partially_accurate, inaccurate
}

// DashboardSessionItem is a single session in the dashboard list with pre-computed stats.
type DashboardSessionItem struct {
	ID                    string           `json:"id"`
	AlertType             *string          `json:"alert_type"`
	ChainID               string           `json:"chain_id"`
	Status                string           `json:"status"`
	Author                *string          `json:"author"`
	CreatedAt             time.Time        `json:"created_at"`
	StartedAt             *time.Time       `json:"started_at"`
	CompletedAt           *time.Time       `json:"completed_at"`
	DurationMs            *int64           `json:"duration_ms"`
	ErrorMessage          *string          `json:"error_message"`
	ExecutiveSummary      *string          `json:"executive_summary"`
	LLMInteractionCount   int              `json:"llm_interaction_count"`
	MCPInteractionCount   int              `json:"mcp_interaction_count"`
	InputTokens           int64            `json:"input_tokens"`
	OutputTokens          int64            `json:"output_tokens"`
	TotalTokens           int64            `json:"total_tokens"`
	EstimatedCostUsd      *float64         `json:"estimated_cost_usd,omitempty"`
	CostCompleteness      CostCompleteness `json:"cost_completeness,omitempty"`
	TotalStages           int              `json:"total_stages"`
	CompletedStages       int              `json:"completed_stages"`
	HasParallelStages     bool             `json:"has_parallel_stages"`
	HasSubAgents          bool             `json:"has_sub_agents"`
	HasActionStages       bool             `json:"has_action_stages"`
	ActionsExecuted       *bool            `json:"actions_executed"`
	ChatMessageCount      int              `json:"chat_message_count"`
	ProviderFallbackCount int              `json:"provider_fallback_count"`
	CurrentStageIndex     *int             `json:"current_stage_index"`
	CurrentStageID        *string          `json:"current_stage_id"`
	MatchedInContent      bool             `json:"matched_in_content"`
	LatestScore           *int             `json:"latest_score"`
	ScoringStatus         *string          `json:"scoring_status"`
	ReviewStatus          *string          `json:"review_status"`
	Assignee              *string          `json:"assignee"`
	QualityRating         *string          `json:"quality_rating"`
	ActionTaken           *string          `json:"action_taken"`
	InvestigationFeedback *string          `json:"investigation_feedback"`
	FeedbackEdited        bool             `json:"feedback_edited"`
	FeedbackEditedBy      *string          `json:"feedback_edited_by"`
	FeedbackEditedAt      *time.Time       `json:"feedback_edited_at"`
}

// DashboardListResponse is the paginated session list response for the dashboard.
type DashboardListResponse struct {
	Sessions              []DashboardSessionItem `json:"sessions"`
	Pagination            PaginationInfo         `json:"pagination"`
	CostEstimationEnabled bool                   `json:"cost_estimation_enabled"`
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
	ID                      string         `json:"id"`
	AlertData               string         `json:"alert_data"`
	AlertType               *string        `json:"alert_type"`
	Status                  string         `json:"status"`
	ChainID                 string         `json:"chain_id"`
	Author                  *string        `json:"author"`
	ErrorMessage            *string        `json:"error_message"`
	FinalAnalysis           *string        `json:"final_analysis"`
	ExecutiveSummary        *string        `json:"executive_summary"`
	ExecutiveSummaryError   *string        `json:"executive_summary_error"`
	RunbookURL              *string        `json:"runbook_url"`
	SlackMessageFingerprint *string        `json:"slack_message_fingerprint,omitempty"`
	MCPSelection            map[string]any `json:"mcp_selection,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`

	// Computed fields
	DurationMs               *int64           `json:"duration_ms"`
	ChatEnabled              bool             `json:"chat_enabled"`
	ChatID                   *string          `json:"chat_id"`
	ChatMessageCount         int              `json:"chat_message_count"`
	TotalStages              int              `json:"total_stages"`
	CompletedStages          int              `json:"completed_stages"`
	FailedStages             int              `json:"failed_stages"`
	HasParallelStages        bool             `json:"has_parallel_stages"`
	HasActionStages          bool             `json:"has_action_stages"`
	ActionsExecuted          *bool            `json:"actions_executed"`
	InputTokens              int64            `json:"input_tokens"`
	OutputTokens             int64            `json:"output_tokens"`
	TotalTokens              int64            `json:"total_tokens"`
	CostEstimationEnabled    bool             `json:"cost_estimation_enabled"`
	EstimatedCostUsd         *float64         `json:"estimated_cost_usd,omitempty"`
	CostCompleteness         CostCompleteness `json:"cost_completeness,omitempty"`
	UnpricedInteractionCount *int             `json:"unpriced_interaction_count,omitempty"`
	LLMInteractionCount      int              `json:"llm_interaction_count"`
	MCPInteractionCount      int              `json:"mcp_interaction_count"`
	CurrentStageIndex        *int             `json:"current_stage_index"`
	CurrentStageID           *string          `json:"current_stage_id"`

	// Scoring fields
	LatestScore   *int    `json:"latest_score"`
	ScoringStatus *string `json:"scoring_status"`
	ScoreID       *string `json:"score_id"`

	// Review fields
	ReviewStatus          *string    `json:"review_status"`
	Assignee              *string    `json:"assignee"`
	QualityRating         *string    `json:"quality_rating"`
	ActionTaken           *string    `json:"action_taken"`
	InvestigationFeedback *string    `json:"investigation_feedback"`
	FeedbackEdited        bool       `json:"feedback_edited"`
	FeedbackEditedBy      *string    `json:"feedback_edited_by"`
	FeedbackEditedAt      *time.Time `json:"feedback_edited_at"`

	// Stage list
	Stages []StageOverview `json:"stages"`
}

// StageOverview is a summary of a stage within the session detail.
type StageOverview struct {
	ID                 string              `json:"id"`
	StageName          string              `json:"stage_name"`
	StageIndex         int                 `json:"stage_index"`
	StageType          string              `json:"stage_type"`
	Status             string              `json:"status"`
	ParallelType       *string             `json:"parallel_type"`
	ExpectedAgentCount int                 `json:"expected_agent_count"`
	ReferencedStageID  *string             `json:"referenced_stage_id,omitempty"`
	StartedAt          *time.Time          `json:"started_at"`
	CompletedAt        *time.Time          `json:"completed_at"`
	Executions         []ExecutionOverview `json:"executions,omitempty"`
}

// ExecutionOverview is a summary of an agent execution within a stage.
type ExecutionOverview struct {
	ExecutionID              string              `json:"execution_id"`
	AgentName                string              `json:"agent_name"`
	AgentIndex               int                 `json:"agent_index"`
	Status                   string              `json:"status"`
	LLMBackend               string              `json:"llm_backend"`
	LLMProvider              *string             `json:"llm_provider"`
	StartedAt                *time.Time          `json:"started_at"`
	CompletedAt              *time.Time          `json:"completed_at"`
	DurationMs               *int64              `json:"duration_ms"`
	ErrorMessage             *string             `json:"error_message"`
	InputTokens              int64               `json:"input_tokens"`
	OutputTokens             int64               `json:"output_tokens"`
	TotalTokens              int64               `json:"total_tokens"`
	EstimatedCostUsd         *float64            `json:"estimated_cost_usd,omitempty"`
	CostCompleteness         CostCompleteness    `json:"cost_completeness,omitempty"`
	UnpricedInteractionCount *int                `json:"unpriced_interaction_count,omitempty"`
	ParentExecutionID        *string             `json:"parent_execution_id,omitempty"`
	Task                     *string             `json:"task,omitempty"`
	OriginalLLMProvider      *string             `json:"original_llm_provider,omitempty"`
	OriginalLLMBackend       *string             `json:"original_llm_backend,omitempty"`
	FallbackReason           *string             `json:"fallback_reason,omitempty"`
	FallbackErrorCode        *string             `json:"fallback_error_code,omitempty"`
	FallbackAttempt          *int                `json:"fallback_attempt,omitempty"`
	SubAgents                []ExecutionOverview `json:"sub_agents,omitempty"`
}

// SessionSummaryResponse is returned by GET /api/v1/sessions/:id/summary.
type SessionSummaryResponse struct {
	SessionID                string           `json:"session_id"`
	TotalInteractions        int              `json:"total_interactions"`
	LLMInteractions          int              `json:"llm_interactions"`
	MCPInteractions          int              `json:"mcp_interactions"`
	InputTokens              int64            `json:"input_tokens"`
	OutputTokens             int64            `json:"output_tokens"`
	TotalTokens              int64            `json:"total_tokens"`
	CostEstimationEnabled    bool             `json:"cost_estimation_enabled"`
	EstimatedCostUsd         *float64         `json:"estimated_cost_usd,omitempty"`
	CostCompleteness         CostCompleteness `json:"cost_completeness,omitempty"`
	UnpricedInteractionCount *int             `json:"unpriced_interaction_count,omitempty"`
	TotalDurationMs          *int64           `json:"total_duration_ms"`
	ChainStatistics          ChainStatistics  `json:"chain_statistics"`

	// Score fields — present only when a completed score exists for the session.
	TotalScore    *int    `json:"total_score,omitempty"`
	ScoringStatus *string `json:"scoring_status,omitempty"`
}

// SessionStatusResponse is returned by GET /api/v1/sessions/:id/status.
type SessionStatusResponse struct {
	ID               string  `json:"id"`
	Status           string  `json:"status"`
	FinalAnalysis    *string `json:"final_analysis"`
	ExecutiveSummary *string `json:"executive_summary"`
	ErrorMessage     *string `json:"error_message"`
}

// ChainStatistics holds stage counts for the session summary.
type ChainStatistics struct {
	TotalStages       int  `json:"total_stages"`
	CompletedStages   int  `json:"completed_stages"`
	FailedStages      int  `json:"failed_stages"`
	CurrentStageIndex *int `json:"current_stage_index"`
}

// CostCompleteness describes whether all token-bearing LLM interactions have prices.
type CostCompleteness string

// Cost completeness values for session/execution cost aggregates.
const (
	CostCompletenessComplete CostCompleteness = "complete"
	CostCompletenessPartial  CostCompleteness = "partial"
	CostCompletenessNone     CostCompleteness = "none"
)

// DeriveCostCompleteness maps priced vs token-bearing interaction counts to a completeness enum.
// tokenBearing is the count of interactions with any of input/output/thinking tokens > 0.
// priced is the count of those with non-null estimated_cost_usd.
func DeriveCostCompleteness(tokenBearing, priced int) CostCompleteness {
	if priced == 0 {
		return CostCompletenessNone
	}
	if priced == tokenBearing {
		return CostCompletenessComplete
	}
	return CostCompletenessPartial
}

// --- Usage summary DTOs (GET /api/v1/usage/summary) ---

// UsageRankBy selects how top_sessions are ordered.
type UsageRankBy string

// Usage ranking modes for top_sessions.
const (
	UsageRankByCost   UsageRankBy = "cost"
	UsageRankByTokens UsageRankBy = "tokens"
)

// ValidUsageRankBy reports whether s is a known rank_by value.
func ValidUsageRankBy(s string) bool {
	switch UsageRankBy(s) {
	case UsageRankByCost, UsageRankByTokens:
		return true
	default:
		return false
	}
}

// UsageSummaryParams holds query parameters for the usage summary endpoint.
type UsageSummaryParams struct {
	StartDate time.Time   // created_at >= start (required)
	EndDate   time.Time   // created_at < end (required)
	AlertType string      // optional exact filter
	ChainID   string      // optional exact filter
	RankBy    UsageRankBy // cost or tokens; empty means default from costEstimationEnabled
}

// UsageSummaryResponse is returned by GET /api/v1/usage/summary.
type UsageSummaryResponse struct {
	CostEstimationEnabled bool                  `json:"cost_estimation_enabled"`
	Window                UsageWindow           `json:"window"`
	RankBy                UsageRankBy           `json:"rank_by"`
	Totals                UsageTotals           `json:"totals"`
	ByModel               []UsageModelBreakdown `json:"by_model"`
	ByAlertType           []UsageAlertBreakdown `json:"by_alert_type"`
	ByChain               []UsageChainBreakdown `json:"by_chain"`
	TopSessions           []UsageTopSession     `json:"top_sessions"`
}

// UsageWindow echoes the requested date range.
type UsageWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// UsageTotals is the fleet-wide token/cost rollup for the window.
type UsageTotals struct {
	InputTokens              int64            `json:"input_tokens"`
	OutputTokens             int64            `json:"output_tokens"`
	TotalTokens              int64            `json:"total_tokens"`
	EstimatedCostUsd         *float64         `json:"estimated_cost_usd,omitempty"`
	CostCompleteness         CostCompleteness `json:"cost_completeness,omitempty"`
	UnpricedInteractionCount *int             `json:"unpriced_interaction_count,omitempty"`
}

// UsageModelBreakdown is a per-model rollup within the window.
type UsageModelBreakdown struct {
	ModelName                string   `json:"model_name"`
	InputTokens              int64    `json:"input_tokens"`
	OutputTokens             int64    `json:"output_tokens"`
	TotalTokens              int64    `json:"total_tokens"`
	EstimatedCostUsd         *float64 `json:"estimated_cost_usd,omitempty"`
	Priced                   *bool    `json:"priced,omitempty"`                     // true when all token-bearing rows for the model are priced
	UnpricedInteractionCount *int     `json:"unpriced_interaction_count,omitempty"` // count of token-bearing interactions for this model with no resolved rate
}

// UsageAlertBreakdown is a per-alert-type rollup within the window.
type UsageAlertBreakdown struct {
	AlertType        string   `json:"alert_type"`
	TotalTokens      int64    `json:"total_tokens"`
	EstimatedCostUsd *float64 `json:"estimated_cost_usd,omitempty"`
}

// UsageChainBreakdown is a per-chain rollup within the window.
type UsageChainBreakdown struct {
	ChainID          string   `json:"chain_id"`
	TotalTokens      int64    `json:"total_tokens"`
	EstimatedCostUsd *float64 `json:"estimated_cost_usd,omitempty"`
}

// UsageTopSession is one of the capped top sessions in the window.
type UsageTopSession struct {
	SessionID        string           `json:"session_id"`
	AlertType        *string          `json:"alert_type"`
	ChainID          string           `json:"chain_id"`
	TotalTokens      int64            `json:"total_tokens"`
	EstimatedCostUsd *float64         `json:"estimated_cost_usd,omitempty"`
	CostCompleteness CostCompleteness `json:"cost_completeness,omitempty"`
	CreatedAt        time.Time        `json:"created_at"`
}

// --- Review workflow DTOs ---

// ReviewAction represents a workflow transition action.
type ReviewAction string

// Review workflow actions.
const (
	ReviewActionClaim          ReviewAction = "claim"
	ReviewActionUnclaim        ReviewAction = "unclaim"
	ReviewActionComplete       ReviewAction = "complete"
	ReviewActionReopen         ReviewAction = "reopen"
	ReviewActionUpdateFeedback ReviewAction = "update_feedback"
	ReviewActionAcknowledge    ReviewAction = "acknowledge"
)

// ValidReviewAction returns true if the action is a known value.
func ValidReviewAction(s string) bool {
	switch ReviewAction(s) {
	case ReviewActionClaim, ReviewActionUnclaim, ReviewActionComplete, ReviewActionReopen, ReviewActionUpdateFeedback, ReviewActionAcknowledge:
		return true
	default:
		return false
	}
}

// UpdateReviewRequest is the request body for PATCH /api/v1/sessions/review.
// SessionIDs contains one or more session IDs to apply the action to.
type UpdateReviewRequest struct {
	SessionIDs            []string `json:"session_ids"`
	Action                string   `json:"action"`
	Actor                 string   `json:"-"`
	QualityRating         *string  `json:"quality_rating,omitempty"`
	ActionTaken           *string  `json:"action_taken,omitempty"`
	InvestigationFeedback *string  `json:"investigation_feedback,omitempty"`
}

// UpdateReviewResponse reports per-session results from a review action.
type UpdateReviewResponse struct {
	Results []UpdateReviewResult `json:"results"`
}

// UpdateReviewResult is the outcome for a single session in a review action.
type UpdateReviewResult struct {
	SessionID string `json:"session_id"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// ReviewActivityItem is a single entry in the review activity log.
type ReviewActivityItem struct {
	ID                    string  `json:"id"`
	Actor                 string  `json:"actor"`
	Action                string  `json:"action"`
	FromStatus            *string `json:"from_status"`
	ToStatus              string  `json:"to_status"`
	QualityRating         *string `json:"quality_rating,omitempty"`
	Note                  *string `json:"note,omitempty"`
	InvestigationFeedback *string `json:"investigation_feedback,omitempty"`
	CreatedAt             string  `json:"created_at"`
}

// ReviewActivityResponse wraps the activity list for GET /sessions/:id/review-activity.
type ReviewActivityResponse struct {
	Activities []ReviewActivityItem `json:"activities"`
}

// --- Triage DTOs ---

// TriageGroupKey identifies a triage group in the URL path.
type TriageGroupKey string

// TriageGroupKey constants for each triage bucket.
const (
	TriageGroupInvestigating TriageGroupKey = "investigating"
	TriageGroupNeedsReview   TriageGroupKey = "needs_review"
	TriageGroupInProgress    TriageGroupKey = "in_progress"
	TriageGroupReviewed      TriageGroupKey = "reviewed"
)

// ParseTriageGroupKey validates a raw string from the URL path.
func ParseTriageGroupKey(s string) (TriageGroupKey, error) {
	switch TriageGroupKey(s) {
	case TriageGroupInvestigating, TriageGroupNeedsReview, TriageGroupInProgress, TriageGroupReviewed:
		return TriageGroupKey(s), nil
	default:
		return "", fmt.Errorf("unknown triage group %q", s)
	}
}

// TriageGroup is the paginated response for a single triage group.
type TriageGroup struct {
	Count      int                    `json:"count"`
	Page       int                    `json:"page"`
	PageSize   int                    `json:"page_size"`
	TotalPages int                    `json:"total_pages"`
	Sessions   []DashboardSessionItem `json:"sessions"`
}

// TriageGroupParams holds query parameters for the per-group triage endpoint.
type TriageGroupParams struct {
	Page     int
	PageSize int
	Assignee *string // nil=no filter, *""=unassigned, *"val"=specific assignee
}
