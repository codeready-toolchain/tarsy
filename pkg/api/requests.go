package api

// SubmitAlertRequest is the HTTP request body for POST /api/v1/alerts.
type SubmitAlertRequest struct {
	AlertType string `json:"alert_type"`
	Runbook   string `json:"runbook,omitempty"`
	Data      string `json:"data"`
	MCP       string `json:"mcp,omitempty"`
}
