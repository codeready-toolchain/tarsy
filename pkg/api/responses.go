package api

// AlertResponse is returned by POST /api/v1/alerts.
type AlertResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// CancelResponse is returned by POST /api/v1/sessions/:id/cancel.
type CancelResponse struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// HealthResponse is returned by GET /health.
type HealthResponse struct {
	Status  string                 `json:"status"`
	Version string                 `json:"version"`
	Checks  map[string]HealthCheck `json:"checks"`
}

// HealthCheck represents the status of a single health check component.
type HealthCheck struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
