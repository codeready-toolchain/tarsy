package services

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/google/uuid"
)

// SubmitAlertInput contains the domain-level data needed to create a session.
// Transformed from the HTTP request + headers by the handler.
type SubmitAlertInput struct {
	AlertType string
	Runbook   string
	Data      string // Alert payload (opaque text, stored as-is)
	MCP       string // MCP selection config (optional, stored as-is)
	Author    string // From oauth2-proxy headers
}

// AlertService handles alert submission and session creation.
type AlertService struct {
	client        *ent.Client
	chainRegistry *config.ChainRegistry
	defaults      *config.Defaults
}

// NewAlertService creates a new AlertService.
func NewAlertService(client *ent.Client, chainRegistry *config.ChainRegistry, defaults *config.Defaults) *AlertService {
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

	// Create session in "pending" status
	builder := s.client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData(input.Data).
		SetAgentType(alertType). // Use alert type as agent type
		SetAlertType(alertType).
		SetChainID(chainID).
		SetStatus(alertsession.StatusPending).
		SetStartedAt(time.Now())

	if input.Author != "" {
		builder.SetAuthor(input.Author)
	}
	if input.Runbook != "" {
		builder.SetRunbookURL(input.Runbook)
	}

	session, err := builder.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return session, nil
}
