package api

import (
	"net/http"
	"sort"
	"time"

	echo "github.com/labstack/echo/v5"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// --- Response types ---

// SystemWarningsResponse is returned by GET /api/v1/system/warnings.
type SystemWarningsResponse struct {
	Warnings []SystemWarningItem `json:"warnings"`
}

// SystemWarningItem is a single system warning.
type SystemWarningItem struct {
	ID        string `json:"id"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Details   string `json:"details"`
	ServerID  string `json:"server_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// MCPServersResponse is returned by GET /api/v1/system/mcp-servers.
type MCPServersResponse struct {
	Servers []MCPServerStatus `json:"servers"`
}

// MCPToolInfo describes a single tool from an MCP server.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPServerStatus describes the health and tools of a single MCP server.
type MCPServerStatus struct {
	ID        string        `json:"id"`
	Healthy   bool          `json:"healthy"`
	LastCheck string        `json:"last_check"`
	ToolCount int           `json:"tool_count"`
	Tools     []MCPToolInfo `json:"tools"`
	Error     *string       `json:"error"`
}

// DefaultToolsResponse is returned by GET /api/v1/system/default-tools.
type DefaultToolsResponse struct {
	AlertType   string          `json:"alert_type,omitempty"`
	MCPServers  []string        `json:"mcp_servers"`
	NativeTools map[string]bool `json:"native_tools"`
}

// --- Handlers ---

// systemWarningsHandler handles GET /api/v1/system/warnings.
func (s *Server) systemWarningsHandler(c *echo.Context) error {
	response := SystemWarningsResponse{
		Warnings: []SystemWarningItem{},
	}

	if s.warningService != nil {
		for _, w := range s.warningService.GetWarnings() {
			response.Warnings = append(response.Warnings, SystemWarningItem{
				ID:        w.ID,
				Category:  w.Category,
				Message:   w.Message,
				Details:   w.Details,
				ServerID:  w.ServerID,
				CreatedAt: w.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	return c.JSON(http.StatusOK, response)
}

// mcpServersHandler handles GET /api/v1/system/mcp-servers.
func (s *Server) mcpServersHandler(c *echo.Context) error {
	response := MCPServersResponse{
		Servers: []MCPServerStatus{},
	}

	if s.healthMonitor == nil {
		return c.JSON(http.StatusOK, response)
	}

	statuses := s.healthMonitor.GetStatuses()
	cachedTools := s.healthMonitor.GetCachedTools()

	for serverID, status := range statuses {
		server := MCPServerStatus{
			ID:        serverID,
			Healthy:   status.Healthy,
			LastCheck: status.LastCheck.Format(time.RFC3339),
			Tools:     []MCPToolInfo{},
		}

		if status.Error != "" {
			server.Error = &status.Error
		}

		if tools, ok := cachedTools[serverID]; ok {
			server.ToolCount = len(tools)
			for _, t := range tools {
				server.Tools = append(server.Tools, MCPToolInfo{
					Name:        t.Name,
					Description: t.Description,
				})
			}
		}

		response.Servers = append(response.Servers, server)
	}

	// Sort for deterministic output.
	sort.Slice(response.Servers, func(i, j int) bool {
		return response.Servers[i].ID < response.Servers[j].ID
	})

	return c.JSON(http.StatusOK, response)
}

// defaultToolsHandler handles GET /api/v1/system/default-tools.
//
// Optional query parameter: ?alert_type=X
// - When provided: returns MCP servers configured for that alert type's chain.
// - When absent: returns all configured MCP server IDs.
func (s *Server) defaultToolsHandler(c *echo.Context) error {
	nativeTools := map[string]bool{
		"google_search":  false,
		"code_execution": false,
		"url_context":    false,
	}

	// Resolve default LLM provider's native tools configuration.
	if s.cfg.Defaults != nil && s.cfg.Defaults.LLMProvider != "" {
		if provider, err := s.cfg.LLMProviderRegistry.Get(s.cfg.Defaults.LLMProvider); err == nil {
			for tool, enabled := range provider.NativeTools {
				nativeTools[string(tool)] = enabled
			}
		}
	}

	// Fall back: check all providers for google type (first match).
	if s.cfg.Defaults == nil || s.cfg.Defaults.LLMProvider == "" {
		for _, providerCfg := range s.cfg.LLMProviderRegistry.GetAll() {
			if providerCfg.Type == config.LLMProviderTypeGoogle {
				for tool, enabled := range providerCfg.NativeTools {
					nativeTools[string(tool)] = enabled
				}
				break
			}
		}
	}

	// Resolve MCP servers based on optional alert_type parameter.
	alertType := c.QueryParam("alert_type")
	mcpServers := []string{}

	if alertType != "" {
		// Look up chain for this alert type and aggregate its MCP servers.
		chain, err := s.cfg.ChainRegistry.GetByAlertType(alertType)
		if err == nil {
			// Use chain-level MCP servers if set, otherwise aggregate from stages.
			if len(chain.MCPServers) > 0 {
				mcpServers = chain.MCPServers
			} else {
				mcpServers = agent.AggregateChainMCPServers(s.cfg, chain)
			}
		}
		// If alert type not found, fall through to all servers below.
	}

	if len(mcpServers) == 0 {
		// No alert type provided or chain lookup failed: return all server IDs.
		if s.cfg.MCPServerRegistry != nil {
			for id := range s.cfg.MCPServerRegistry.GetAll() {
				mcpServers = append(mcpServers, id)
			}
		}
	}

	sort.Strings(mcpServers)

	return c.JSON(http.StatusOK, DefaultToolsResponse{
		AlertType:   alertType,
		MCPServers:  mcpServers,
		NativeTools: nativeTools,
	})
}
