// Package mcp provides MCP (Model Context Protocol) client infrastructure
// for connecting to and executing tools on MCP servers.
package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/version"
)

// Client manages MCP SDK sessions for multiple servers.
// Each Client instance is scoped to a single session (alert processing or health check).
// Thread-safe: sessions may be accessed from multiple goroutines during parallel stages.
type Client struct {
	registry *config.MCPServerRegistry

	mu            sync.RWMutex
	sessions      map[string]*mcpsdk.ClientSession // serverID → session
	clients       map[string]*mcpsdk.Client        // serverID → client (for reconnection)
	failedServers map[string]string                // serverID → error message

	// Tool cache (populated on first ListTools, never invalidated — each Client
	// instance is short-lived per session, so the cache is naturally fresh)
	toolCache   map[string][]*mcpsdk.Tool
	toolCacheMu sync.RWMutex

	// Per-server mutex for session recreation to prevent thundering herd
	reinitMu sync.Map // serverID → *sync.Mutex

	logger *slog.Logger
}

// newClient creates a new Client.
func newClient(registry *config.MCPServerRegistry) *Client {
	return &Client{
		registry:      registry,
		sessions:      make(map[string]*mcpsdk.ClientSession),
		clients:       make(map[string]*mcpsdk.Client),
		failedServers: make(map[string]string),
		toolCache:     make(map[string][]*mcpsdk.Tool),
		logger:        slog.Default(),
	}
}

// Initialize connects to all configured MCP servers.
// Servers that fail to connect are recorded in failedServers.
// The caller decides whether failures are fatal:
//   - Startup (readiness probe): check FailedServers() and fail if non-empty
//   - Per-session: partial initialization is acceptable
//
// Always returns nil today; the error return is retained so the signature can
// evolve (e.g., returning an error when *all* servers fail) without breaking
// callers.
func (c *Client) Initialize(ctx context.Context, serverIDs []string) error {
	for _, serverID := range serverIDs {
		if err := c.InitializeServer(ctx, serverID); err != nil {
			c.mu.Lock()
			c.failedServers[serverID] = err.Error()
			c.mu.Unlock()
			c.logger.Warn("MCP server failed to initialize",
				"server", serverID, "error", err)
		}
	}
	return nil
}

// InitializeServer connects to a single MCP server.
// Returns nil if already connected. Used for lazy initialization and recovery.
// Uses per-server mutex to prevent concurrent initialization of the same server.
func (c *Client) InitializeServer(ctx context.Context, serverID string) error {
	// Acquire per-server mutex to serialize initialization attempts
	muI, _ := c.reinitMu.LoadOrStore(serverID, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	return c.initializeServerLocked(ctx, serverID)
}

// initializeServerLocked performs the actual server initialization.
// Caller must hold the per-server reinitMu lock.
func (c *Client) initializeServerLocked(ctx context.Context, serverID string) error {
	// Check if already connected (under per-server lock, no TOCTOU race)
	c.mu.RLock()
	if _, exists := c.sessions[serverID]; exists {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	// Get server config
	serverCfg, err := c.registry.Get(serverID)
	if err != nil {
		return fmt.Errorf("server %q not found in registry: %w", serverID, err)
	}

	// Create transport
	transport, err := createTransport(serverCfg.Transport)
	if err != nil {
		return fmt.Errorf("failed to create transport for %q: %w", serverID, err)
	}

	// Create MCP client and connect with timeout
	initCtx, cancel := context.WithTimeout(ctx, MCPInitTimeout)
	defer cancel()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    version.AppName,
		Version: version.GitCommit,
	}, nil)

	session, err := client.Connect(initCtx, transport, nil)
	if err != nil {
		// Defensive: close the transport if it implements io.Closer to avoid
		// leaking resources (e.g., stdio child processes). The SDK closes the
		// underlying connection on most failure paths, but this guards against
		// edge cases and future transport types.
		if closer, ok := transport.(io.Closer); ok {
			_ = closer.Close()
		}
		return fmt.Errorf("failed to connect to %q: %w", serverID, err)
	}

	// Store session and clear failure record
	c.mu.Lock()
	c.sessions[serverID] = session
	c.clients[serverID] = client
	delete(c.failedServers, serverID)
	c.mu.Unlock()

	c.logger.Info("MCP server connected", "server", serverID)
	return nil
}

// ListTools returns tools from a specific server. Uses cache if available.
func (c *Client) ListTools(ctx context.Context, serverID string) ([]*mcpsdk.Tool, error) {
	// Check cache first
	// Lock ordering: never acquire c.mu while holding toolCacheMu.
	c.toolCacheMu.RLock()
	if cached, ok := c.toolCache[serverID]; ok {
		c.toolCacheMu.RUnlock()
		return cached, nil
	}
	c.toolCacheMu.RUnlock()

	// Get session
	c.mu.RLock()
	session, exists := c.sessions[serverID]
	c.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("no session for server %q", serverID)
	}

	// Call with timeout
	opCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()

	result, err := session.ListTools(opCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools from %q: %w", serverID, err)
	}

	// Cache results (nil-guard: ensure we always cache a non-nil slice so
	// cache hits don't return nil to callers).
	tools := result.Tools
	if tools == nil {
		tools = []*mcpsdk.Tool{}
	}
	c.toolCacheMu.Lock()
	c.toolCache[serverID] = tools
	c.toolCacheMu.Unlock()

	return tools, nil
}

// ListAllTools returns tools from all connected servers.
// Returns partial results if some servers fail (logs errors, does not abort).
// Returns an error only when every server fails (no tools available at all).
func (c *Client) ListAllTools(ctx context.Context) (map[string][]*mcpsdk.Tool, error) {
	c.mu.RLock()
	serverIDs := make([]string, 0, len(c.sessions))
	for id := range c.sessions {
		serverIDs = append(serverIDs, id)
	}
	c.mu.RUnlock()

	result := make(map[string][]*mcpsdk.Tool)
	var lastErr error
	for _, id := range serverIDs {
		tools, err := c.ListTools(ctx, id)
		if err != nil {
			lastErr = err
			c.logger.Warn("Failed to list tools from MCP server",
				"server", id, "error", err)
			continue
		}
		result[id] = tools
	}

	if len(result) == 0 && lastErr != nil {
		return nil, fmt.Errorf("all servers failed to list tools: %w", lastErr)
	}
	return result, nil
}

// CallTool executes a tool call on the specified server.
// Handles recovery (retry with session recreation) on transport failures.
// At most one retry is attempted after a jittered backoff; if the retry also
// fails the error is returned to the caller.
func (c *Client) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error) {
	params := &mcpsdk.CallToolParams{
		Name:      toolName,
		Arguments: args,
	}

	// First attempt
	result, err := c.callToolOnce(ctx, serverID, params)
	if err == nil {
		return result, nil
	}

	// Classify error for recovery
	action := ClassifyError(err)
	if action == NoRetry {
		return nil, err
	}

	// Retry logic
	c.logger.Info("MCP call failed, retrying",
		"server", serverID, "tool", toolName,
		"action", action, "error", err)

	// Jittered backoff
	backoff := RetryBackoffMin + time.Duration(rand.Int64N(int64(RetryBackoffMax-RetryBackoffMin)))
	select {
	case <-time.After(backoff):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Recreate session if needed
	if action == RetryNewSession {
		if err := c.recreateSession(ctx, serverID); err != nil {
			return nil, fmt.Errorf("session recreation failed for %q: %w", serverID, err)
		}
	}

	// Second attempt
	result, err = c.callToolOnce(ctx, serverID, params)
	if err != nil {
		return nil, fmt.Errorf("retry failed for %q.%s: %w", serverID, toolName, err)
	}
	return result, nil
}

// callToolOnce performs a single CallTool attempt.
func (c *Client) callToolOnce(ctx context.Context, serverID string, params *mcpsdk.CallToolParams) (*mcpsdk.CallToolResult, error) {
	c.mu.RLock()
	session, exists := c.sessions[serverID]
	c.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("no session for server %q", serverID)
	}

	opCtx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()

	return session.CallTool(opCtx, params)
}

// recreateSession tears down and recreates the session for a server.
// Uses per-server mutex to prevent concurrent recreation.
//
// Note: if two goroutines race into recreateSession, the second will
// unnecessarily tear down the freshly recreated session and create another.
// A staleness guard (checking if session exists after lock) doesn't work here
// because the first caller also sees the broken session in the map.
// The cost is an extra recreation, which is acceptable for simplicity.
// Future optimisation: a per-server generation counter (incremented on each
// recreation) would let the second goroutine detect the session was already
// refreshed and skip re-creation. Worth adding if this becomes a hot path.
func (c *Client) recreateSession(ctx context.Context, serverID string) error {
	// Get or create per-server mutex
	muI, _ := c.reinitMu.LoadOrStore(serverID, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Close existing session
	c.mu.Lock()
	if session, exists := c.sessions[serverID]; exists {
		_ = session.Close()
		delete(c.sessions, serverID)
		delete(c.clients, serverID)
	}
	c.mu.Unlock()

	// Clear tool cache for this server
	c.InvalidateToolCache(serverID)

	// Reinitialize with timeout (use locked variant — we already hold reinitMu)
	reinitCtx, cancel := context.WithTimeout(ctx, ReinitTimeout)
	defer cancel()

	return c.initializeServerLocked(reinitCtx, serverID)
}

// Close shuts down all sessions and transports gracefully.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var firstErr error
	for id, session := range c.sessions {
		if err := session.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("close session %q: %w", id, err)
		}
	}

	// Clear all state
	c.sessions = make(map[string]*mcpsdk.ClientSession)
	c.clients = make(map[string]*mcpsdk.Client)
	c.failedServers = make(map[string]string)

	// Lock ordering note: mu → toolCacheMu is safe here because no other
	// code path holds toolCacheMu while acquiring mu.
	c.toolCacheMu.Lock()
	c.toolCache = make(map[string][]*mcpsdk.Tool)
	c.toolCacheMu.Unlock()

	return firstErr
}

// InvalidateToolCache removes the cached tool list for a server,
// forcing the next ListTools call to re-probe the server.
// Lock ordering: never acquire c.mu while holding toolCacheMu.
func (c *Client) InvalidateToolCache(serverID string) {
	c.toolCacheMu.Lock()
	delete(c.toolCache, serverID)
	c.toolCacheMu.Unlock()
}

// HasSession checks if a server has an active session.
func (c *Client) HasSession(serverID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, exists := c.sessions[serverID]
	return exists
}

// FailedServers returns the map of servers that failed to initialize.
func (c *Client) FailedServers() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]string, len(c.failedServers))
	for k, v := range c.failedServers {
		result[k] = v
	}
	return result
}
