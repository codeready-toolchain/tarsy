package api

import "github.com/codeready-toolchain/tarsy/pkg/models"

// SubmitAlertRequest is the HTTP request body for POST /api/v1/alerts.
type SubmitAlertRequest struct {
	AlertType               string                     `json:"alert_type"`
	Runbook                 string                     `json:"runbook,omitempty"`
	Data                    string                     `json:"data"`
	MCP                     *models.MCPSelectionConfig `json:"mcp,omitempty"`
	SlackMessageFingerprint string                     `json:"slack_message_fingerprint,omitempty"`
}
