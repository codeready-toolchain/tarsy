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

// CleanupSessions best-effort deletes per-execution sandbox sessions on
// MCP servers that declare session_cleanup_url (e.g. cli-mcp-server).
// sessionID is the agent execution ID used as X-Session-ID.
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

// needsSessionCleanup reports whether the transport opts into explicit session
// cleanup via session_cleanup_url.
func needsSessionCleanup(cfg config.TransportConfig) bool {
	return cfg.SessionCleanupURL != ""
}

func deleteSession(ctx context.Context, cfg config.TransportConfig, sessionID string) error {
	endpoint, err := resolveSessionCleanupURL(cfg.SessionCleanupURL, sessionID)
	if err != nil {
		return err
	}

	// Use the cleanup endpoint as the client URL so bearer/custom headers are
	// host-scoped to the DELETE target (which may differ from the MCP /mcp path).
	cleanupCfg := cfg
	cleanupCfg.URL = endpoint

	client, err := buildHTTPClient(cleanupCfg, map[string]string{"SESSION_ID": sessionID})
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
func resolveSessionCleanupURL(rawURL, sessionID string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("session_cleanup_url is empty")
	}
	resolved, err := resolveHeaderTemplates(map[string]string{"url": rawURL}, map[string]string{
		"SESSION_ID": sessionID,
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
