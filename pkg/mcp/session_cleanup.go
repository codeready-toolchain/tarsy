package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const sessionCleanupTimeout = 15 * time.Second

// CleanupSessions best-effort deletes per-investigation sandbox sessions on
// MCP servers that use session-scoped custom_headers (e.g. cli-mcp-server).
// Failures are logged and never returned — idle TTL remains the safety net.
func CleanupSessions(ctx context.Context, registry *config.MCPServerRegistry, sessionID string, logger *slog.Logger) {
	if registry == nil || sessionID == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	for serverID, serverCfg := range registry.GetAll() {
		if serverCfg == nil || !needsSessionCleanup(serverCfg.Transport) {
			continue
		}
		if err := deleteSession(ctx, serverCfg.Transport, sessionID); err != nil {
			logger.Warn("MCP session cleanup failed",
				"server", serverID,
				"session_id", sessionID,
				"error", err)
			continue
		}
		logger.Info("MCP session cleaned up",
			"server", serverID,
			"session_id", sessionID)
	}
}

// needsSessionCleanup reports whether the transport is an HTTP(S) MCP server
// that injects a session-scoped header (cli-mcp-server pattern).
func needsSessionCleanup(cfg config.TransportConfig) bool {
	if cfg.Type != config.TransportTypeHTTP && cfg.Type != config.TransportTypeSSE {
		return false
	}
	if cfg.URL == "" {
		return false
	}
	for _, value := range cfg.CustomHeaders {
		if strings.Contains(value, "{{.SESSION_ID}}") {
			return true
		}
	}
	return false
}

func deleteSession(ctx context.Context, cfg config.TransportConfig, sessionID string) error {
	endpoint, err := sessionCleanupURL(cfg.URL, sessionID)
	if err != nil {
		return err
	}

	client, err := buildHTTPClient(cfg, map[string]string{"SESSION_ID": sessionID})
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

// sessionCleanupURL derives DELETE /sessions/{id} from the MCP /mcp endpoint URL.
func sessionCleanupURL(mcpURL, sessionID string) (string, error) {
	u, err := url.Parse(mcpURL)
	if err != nil {
		return "", fmt.Errorf("parse mcp url: %w", err)
	}
	// Strip trailing /mcp (with or without trailing slash) to get the service base.
	path := strings.TrimSuffix(u.Path, "/")
	path = strings.TrimSuffix(path, "/mcp")
	u.Path = strings.TrimSuffix(path, "/") + "/sessions/" + url.PathEscape(sessionID)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
