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
