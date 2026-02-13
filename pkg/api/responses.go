package api

import (
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

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
	Status        string                       `json:"status"`
	Version       string                       `json:"version"`
	Database      *database.HealthStatus       `json:"database"`
	Phase         string                       `json:"phase"`
	Configuration ConfigurationStats           `json:"configuration"`
	WorkerPool    *queue.PoolHealth            `json:"worker_pool,omitempty"`
	MCPHealth     map[string]*mcp.HealthStatus `json:"mcp_health,omitempty"`
	Warnings      []*services.SystemWarning    `json:"warnings,omitempty"`
}

// ConfigurationStats contains counts of loaded configuration items.
type ConfigurationStats struct {
	Agents       int `json:"agents"`
	Chains       int `json:"chains"`
	MCPServers   int `json:"mcp_servers"`
	LLMProviders int `json:"llm_providers"`
}
