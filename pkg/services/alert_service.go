package services

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/google/uuid"
)

// SubmitAlertInput contains the domain-level data needed to create a session.
// Transformed from the HTTP request + headers by the handler.
type SubmitAlertInput struct {
	AlertType string
	Runbook   string
	Data      string                     // Alert payload (opaque text, stored as-is)
	MCP       *models.MCPSelectionConfig // MCP selection config (optional)
	Author    string                     // From oauth2-proxy headers
}

// AlertService handles alert submission and session creation.
type AlertService struct {
	client        *ent.Client
	chainRegistry *config.ChainRegistry
	defaults      *config.Defaults
}

// NewAlertService creates a new AlertService.
func NewAlertService(client *ent.Client, chainRegistry *config.ChainRegistry, defaults *config.Defaults) *AlertService {
	if client == nil {
		panic("NewAlertService: client must not be nil")
	}
	if chainRegistry == nil {
		panic("NewAlertService: chainRegistry must not be nil")
	}
	if defaults == nil {
		panic("NewAlertService: defaults must not be nil")
	}
	return &AlertService{
		client:        client,
		chainRegistry: chainRegistry,
		defaults:      defaults,
	}
}

// SubmitAlert creates a new session from an alert submission.
// The session starts in "pending" status and is picked up by the worker pool.
func (s *AlertService) SubmitAlert(ctx context.Context, input SubmitAlertInput) (*ent.AlertSession, error) {
	if input.Data == "" {
		return nil, NewValidationError("data", "alert data is required")
	}

	// Resolve alert type (use default if not provided)
	alertType := input.AlertType
	if alertType == "" {
		alertType = s.defaults.AlertType
	}

	// Resolve chain ID from alert type
	chainID, err := s.chainRegistry.GetIDByAlertType(alertType)
	if err != nil {
		return nil, NewValidationError("alert_type", fmt.Sprintf("no chain found for alert type '%s'", alertType))
	}

	// Generate session ID
	sessionID := uuid.New().String()

	// Convert MCP selection to JSON map for ent storage
	var mcpSelectionJSON map[string]any
	if input.MCP != nil {
		mcpBytes, err := json.Marshal(input.MCP)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal MCP selection: %w", err)
		}
		if err := json.Unmarshal(mcpBytes, &mcpSelectionJSON); err != nil {
			return nil, fmt.Errorf("failed to convert MCP selection: %w", err)
		}
	}

	// Create session in "pending" status
	// Note: created_at is set automatically by schema default
	// started_at will be set by the worker when it claims the session
	builder := s.client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData(input.Data).
		SetAgentType(alertType). // Use alert type as agent type
		SetAlertType(alertType).
		SetChainID(chainID).
		SetStatus(alertsession.StatusPending)

	if input.Author != "" {
		builder.SetAuthor(input.Author)
	}
	if input.Runbook != "" {
		builder.SetRunbookURL(input.Runbook)
	}
	if mcpSelectionJSON != nil {
		builder.SetMcpSelection(mcpSelectionJSON)
	}

	session, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}
