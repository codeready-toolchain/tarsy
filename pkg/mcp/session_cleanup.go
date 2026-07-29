package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const sessionCleanupTimeout = 15 * time.Second

// CleanupSessions best-effort deletes per-execution sandbox sessions on the
// given MCP servers that declare session_cleanup_url (e.g. cli-mcp-server).
// mcpSessionID is the agent execution ID used as X-Session-ID.
// Only serverIDs requested by the closing client are considered — other
// registry servers are left alone.
// Failures are logged and never returned — idle TTL remains the safety net.
func CleanupSessions(
	ctx context.Context,
	registry *config.MCPServerRegistry,
	mcpSessionID string,
	serverIDs []string,
	logger *slog.Logger,
) {
	if registry == nil || mcpSessionID == "" || len(serverIDs) == 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Cap total cleanup time so a hung endpoint cannot stall workers for
	// longer than one cleanup timeout across all servers.
	cleanupCtx, cancel := context.WithTimeout(ctx, sessionCleanupTimeout)
	defer cancel()

	for _, serverID := range serverIDs {
		serverCfg, err := registry.Get(serverID)
		if err != nil || serverCfg == nil || !needsSessionCleanup(serverCfg.Transport) {
			continue
		}
		if err := deleteSession(cleanupCtx, serverCfg.Transport, mcpSessionID); err != nil {
			logger.Warn("MCP session cleanup failed",
				"server", serverID,
				"mcp_session_id", mcpSessionID,
				"error", err)
			continue
		}
		logger.Info("MCP session cleaned up",
			"server", serverID,
			"mcp_session_id", mcpSessionID)
	}
}

// needsSessionCleanup reports whether the transport opts into explicit session
// cleanup via session_cleanup_url.
func needsSessionCleanup(cfg config.TransportConfig) bool {
	return cfg.SessionCleanupURL != ""
}

func deleteSession(ctx context.Context, cfg config.TransportConfig, mcpSessionID string) error {
	endpoint, err := resolveSessionCleanupURL(cfg.SessionCleanupURL, mcpSessionID)
	if err != nil {
		return err
	}

	// Use the cleanup endpoint as the client URL so bearer/custom headers are
	// host-scoped to the DELETE target (which may differ from the MCP /mcp path).
	cleanupCfg := cfg
	cleanupCfg.URL = endpoint

	client, err := buildHTTPClient(cleanupCfg, map[string]string{"SESSION_ID": mcpSessionID})
	if err != nil {
		return err
	}
	// Prefer a short dedicated timeout for cleanup.
	client.Timeout = sessionCleanupTimeout

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// 404 means already gone — treat as success.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent ||
		resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("unexpected status %d", resp.StatusCode)
}

// resolveSessionCleanupURL expands {{.SESSION_ID}} in the configured cleanup URL.
func resolveSessionCleanupURL(rawURL, mcpSessionID string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("session_cleanup_url is empty")
	}
	resolved, err := resolveHeaderTemplates(map[string]string{"url": rawURL}, map[string]string{
		"SESSION_ID": mcpSessionID,
	})
	if err != nil {
		return "", err
	}
	endpoint := resolved["url"]
	if endpoint == "" {
		return "", fmt.Errorf("session_cleanup_url resolved to empty")
	}
	return endpoint, nil
}
