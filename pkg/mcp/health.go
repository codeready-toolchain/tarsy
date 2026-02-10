package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// HealthStatus captures the health check result for a single MCP server.
type HealthStatus struct {
	ServerID  string    `json:"server_id"`
	Healthy   bool      `json:"healthy"`
	LastCheck time.Time `json:"last_check"`
	Error     string    `json:"error,omitempty"`
	ToolCount int       `json:"tool_count"`
}

// HealthMonitor periodically checks MCP server health.
// Runs a background goroutine that probes each server with ListTools.
type HealthMonitor struct {
	factory        *ClientFactory
	registry       *config.MCPServerRegistry
	warningService *services.SystemWarningsService

	checkInterval time.Duration
	pingTimeout   time.Duration

	// Dedicated health-check client (long-lived, recreated on failure)
	client   *Client
	clientMu sync.Mutex

	// Cached tools from last successful health check per server
	toolCache   map[string][]*mcpsdk.Tool
	toolCacheMu sync.RWMutex

	// Current status per server
	statuses   map[string]*HealthStatus
	statusesMu sync.RWMutex

	cancel context.CancelFunc
	done   chan struct{}
	logger *slog.Logger
}

// NewHealthMonitor creates a new health monitor.
func NewHealthMonitor(
	factory *ClientFactory,
	registry *config.MCPServerRegistry,
	warningService *services.SystemWarningsService,
) *HealthMonitor {
	return &HealthMonitor{
		factory:        factory,
		registry:       registry,
		warningService: warningService,
		checkInterval:  MCPHealthInterval,
		pingTimeout:    MCPHealthPingTimeout,
		toolCache:      make(map[string][]*mcpsdk.Tool),
		statuses:       make(map[string]*HealthStatus),
		logger:         slog.Default(),
	}
}

// Start launches the background health check loop.
// Calling Start on an already-running monitor is a no-op.
func (m *HealthMonitor) Start(ctx context.Context) {
	if m.cancel != nil {
		return // already started
	}
	ctx, m.cancel = context.WithCancel(ctx)
	m.done = make(chan struct{})

	// Initialize dedicated health client
	m.clientMu.Lock()
	serverIDs := m.registry.ServerIDs()
	client, err := m.factory.CreateClient(ctx, serverIDs)
	if err != nil {
		m.logger.Warn("Health monitor: failed to create initial client", "error", err)
	}
	m.client = client
	m.clientMu.Unlock()

	go m.loop(ctx)
}

// Stop gracefully shuts down the health monitor.
// After Stop returns, Start may be called again.
func (m *HealthMonitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		<-m.done
	}
	m.clientMu.Lock()
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	m.clientMu.Unlock()

	// Clear stale health data so a subsequent Start begins with a clean slate
	// and IsHealthy() doesn't return results for removed/changed servers.
	m.statusesMu.Lock()
	m.statuses = make(map[string]*HealthStatus)
	m.statusesMu.Unlock()

	m.toolCacheMu.Lock()
	m.toolCache = make(map[string][]*mcpsdk.Tool)
	m.toolCacheMu.Unlock()

	// Reset so Start can be called again.
	m.cancel = nil
	m.done = nil
}

func (m *HealthMonitor) loop(ctx context.Context) {
	defer close(m.done)

	// Attempt client recovery if it wasn't created during Start
	m.ensureClient(ctx)

	// Run first check immediately
	m.checkAll(ctx)

	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.ensureClient(ctx)
			m.checkAll(ctx)
		}
	}
}

// ensureClient attempts to create the health client if it is nil.
// Handles recovery from transient factory failures without requiring restart.
func (m *HealthMonitor) ensureClient(ctx context.Context) {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()

	if m.client != nil {
		return
	}

	serverIDs := m.registry.ServerIDs()
	client, err := m.factory.CreateClient(ctx, serverIDs)
	if err != nil {
		m.logger.Warn("Health monitor: failed to recreate client", "error", err)
		return
	}
	m.client = client
	m.logger.Info("Health monitor: client recovered successfully")
}

func (m *HealthMonitor) checkAll(ctx context.Context) {
	serverIDs := m.registry.ServerIDs()

	for _, serverID := range serverIDs {
		m.checkServer(ctx, serverID)
	}
}

func (m *HealthMonitor) checkServer(ctx context.Context, serverID string) {
	m.clientMu.Lock()
	client := m.client
	m.clientMu.Unlock()

	if client == nil {
		m.setStatus(serverID, false, "health client not initialized", 0)
		return
	}

	// Clear the tool cache for this server so the health check actually
	// probes the connection rather than returning stale cached data.
	client.InvalidateToolCache(serverID)

	checkCtx, checkCancel := context.WithTimeout(ctx, m.pingTimeout)
	defer checkCancel()

	tools, err := client.ListTools(checkCtx, serverID)
	if err != nil {
		m.logger.Debug("Health check failed, attempting reinitialize",
			"server", serverID, "error", err)

		// Try to reinitialize the session with a bounded context
		reconCtx, reconCancel := context.WithTimeout(ctx, m.pingTimeout)
		defer reconCancel()

		if reinitErr := client.recreateSession(reconCtx, serverID); reinitErr != nil {
			m.setStatus(serverID, false, fmt.Sprintf("health check failed: %s", err.Error()), 0)
			m.warningService.AddWarning(
				services.WarningCategoryMCPHealth,
				fmt.Sprintf("MCP server %q is unhealthy", serverID),
				err.Error(), serverID)
			return
		}

		// Retry after reinit with a fresh timeout context
		retryCtx, retryCancel := context.WithTimeout(ctx, m.pingTimeout)
		defer retryCancel()

		tools, err = client.ListTools(retryCtx, serverID)
		if err != nil {
			m.setStatus(serverID, false, fmt.Sprintf("health check failed after reinit: %s", err.Error()), 0)
			m.warningService.AddWarning(
				services.WarningCategoryMCPHealth,
				fmt.Sprintf("MCP server %q is unhealthy", serverID),
				err.Error(), serverID)
			return
		}
	}

	// Healthy
	m.setStatus(serverID, true, "", len(tools))

	// Update tool cache
	m.toolCacheMu.Lock()
	m.toolCache[serverID] = tools
	m.toolCacheMu.Unlock()

	// Clear any existing warning
	m.warningService.ClearByServerID(services.WarningCategoryMCPHealth, serverID)
}

func (m *HealthMonitor) setStatus(serverID string, healthy bool, errMsg string, toolCount int) {
	m.statusesMu.Lock()
	defer m.statusesMu.Unlock()
	m.statuses[serverID] = &HealthStatus{
		ServerID:  serverID,
		Healthy:   healthy,
		LastCheck: time.Now(),
		Error:     errMsg,
		ToolCount: toolCount,
	}
}

// GetStatuses returns the current health status of all monitored servers.
func (m *HealthMonitor) GetStatuses() map[string]*HealthStatus {
	m.statusesMu.RLock()
	defer m.statusesMu.RUnlock()
	result := make(map[string]*HealthStatus, len(m.statuses))
	for k, v := range m.statuses {
		cp := *v
		result[k] = &cp
	}
	return result
}

// GetCachedTools returns the cached tools from the last successful health check.
// The returned map is a shallow copy: slices share the same underlying Tool
// pointers with the monitor's cache. Callers must not mutate the slices.
func (m *HealthMonitor) GetCachedTools() map[string][]*mcpsdk.Tool {
	m.toolCacheMu.RLock()
	defer m.toolCacheMu.RUnlock()
	result := make(map[string][]*mcpsdk.Tool, len(m.toolCache))
	for k, v := range m.toolCache {
		result[k] = v
	}
	return result
}

// IsHealthy returns true if all monitored servers are healthy.
// Returns false when no statuses exist yet (before first check completes).
func (m *HealthMonitor) IsHealthy() bool {
	m.statusesMu.RLock()
	defer m.statusesMu.RUnlock()
	if len(m.statuses) == 0 {
		return false
	}
	for _, s := range m.statuses {
		if !s.Healthy {
			return false
		}
	}
	return true
}
