# Phase 4.1: MCP Client Infrastructure — Detailed Design

**Status**: ✅ Design Complete (all questions resolved)
**Last Updated**: 2026-02-09

## Overview

This document details the MCP Client Infrastructure for the new TARSy implementation. Phase 4.1 replaces the `StubToolExecutor` with a production MCP-backed `ToolExecutor`, enabling real tool execution against external MCP servers.

**Phase 4.1 Scope**: MCP client (Go SDK wrapper), transport layer, tool registry & discovery, error handling & recovery, per-session isolation, health monitoring, system warnings service, ActionInput parameter parsing, tool name validation.

**What This Phase Delivers:**
- `MCPClient` — manages MCP SDK sessions for multiple servers, per-session lifecycle
- `MCPClientFactory` — creates per-session MCP client instances
- `MCPToolExecutor` — implements `ToolExecutor` interface, backed by MCP
- Transport support — stdio (subprocess), Streamable HTTP, SSE
- ActionInput parameter parsing — multi-format cascade (JSON → YAML → key-value → raw)
- Tool name `server.tool` routing and validation
- Error classification, retry, and session recreation
- `MCPHealthMonitor` — background health checks with tool cache
- `SystemWarningsService` — in-memory warning store for MCP health and other system issues
- Integration with existing controllers and session executor

**What This Phase Does NOT Deliver:**
- Data masking service (Phase 4.2 — required before production use)
- Tool result summarization (Phase 4.3 — requires LLM call integration)
- Tool output streaming via `stream.chunk` (Phase 4.3)
- MCP selection per-alert override (Phase 4.3)

**Dependencies:**
- Phase 3 complete (ToolExecutor interface, controllers, streaming)
- Official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.3.0)
- Existing `config.MCPServerRegistry` with transport configs

---

## Architecture

### Layered Design

```
┌─────────────────────────────────────────────────────────────────┐
│  Controllers (ReAct, NativeThinking, Synthesis)                   │
│                                                                    │
│  Call ToolExecutor.Execute(ToolCall) and ListTools()               │
│  - ReAct: Name="server.tool", Arguments=raw string                │
│  - NativeThinking: Name="server__tool", Arguments=JSON string     │
└──────────────────────────┬────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  MCPToolExecutor  (implements agent.ToolExecutor)                  │
│                                                                    │
│  Responsibilities:                                                 │
│  1. Tool name normalization (server__tool → server.tool)           │
│  2. Tool name splitting and routing (server.tool → server + tool)  │
│  3. ActionInput parameter parsing (raw string → map[string]any)    │
│  4. Delegates to MCPClient for actual MCP calls                    │
│  5. Applies data masking to results (Phase 4.2)                    │
│  6. Triggers summarization for large results (Phase 4.3)           │
│  7. Returns ToolResult to controller                               │
└──────────────────────────┬────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  MCPClient  (manages MCP SDK sessions)                             │
│                                                                    │
│  Responsibilities:                                                 │
│  1. Initialize sessions for configured MCP servers                 │
│  2. Transport creation (stdio/HTTP/SSE) via MCP SDK                │
│  3. list_tools per server (with caching)                           │
│  4. call_tool with error recovery                                  │
│  5. Session recreation on transport failures                       │
│  6. Graceful shutdown (close transports)                           │
└──────────────────────────┬────────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  MCP Go SDK  (github.com/modelcontextprotocol/go-sdk)              │
│                                                                    │
│  mcp.NewClient() → client.Connect(transport) → session             │
│  session.ListTools() / session.CallTool()                          │
│  CommandTransport / StreamableClientTransport / SSEClientTransport │
└─────────────────────────────────────────────────────────────────┘
```

### Package Layout

```
pkg/
├── mcp/                        # MCP client infrastructure
│   ├── client.go               # MCPClient — MCP SDK session manager
│   ├── client_factory.go       # MCPClientFactory — per-session creation
│   ├── executor.go             # MCPToolExecutor — implements ToolExecutor
│   ├── params.go               # ActionInput parameter parsing
│   ├── params_test.go
│   ├── router.go               # Tool name splitting, validation, routing
│   ├── router_test.go
│   ├── recovery.go             # Error classification and recovery logic
│   ├── recovery_test.go
│   ├── health.go               # MCPHealthMonitor — background health checks
│   ├── health_test.go
│   ├── transport.go            # Transport creation from config
│   └── transport_test.go
```

### Key Design Principle: Separation of Concerns

Old TARSy mixed MCP session management, parameter parsing, data masking, summarization, and health monitoring all into a single `MCPClient` class. New TARSy separates these:

| Concern | Old TARSy | New TARSy |
|---------|-----------|-----------|
| MCP SDK sessions | `MCPClient` | `MCPClient` |
| Parameter parsing | `react_parser.py` | `MCPToolExecutor` (via `params.go`) |
| Tool name routing | `MCPClient._validate_tool_call` | `MCPToolExecutor` (via `router.go`) |
| Data masking | `MCPClient.call_tool` | `MCPToolExecutor` (delegates to masking service, Phase 4.2) |
| Summarization | `MCPClient._maybe_summarize` | `MCPToolExecutor` (delegates to summarizer, Phase 4.3) |
| Health monitoring | `MCPHealthMonitor` | `MCPHealthMonitor` |
| ToolExecutor interface | N/A (agents called MCP directly) | `MCPToolExecutor` |

---

## Detailed Component Design

### 1. MCPClient

The core MCP session manager. Wraps the official Go SDK to manage connections to multiple MCP servers.

```go
package mcp

import (
    "context"
    "sync"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/codeready-toolchain/tarsy/pkg/config"
)

// MCPClient manages MCP SDK sessions for multiple servers.
// Each MCPClient instance is scoped to a single session (alert processing or health check).
// Thread-safe: sessions may be accessed from multiple goroutines during parallel stages.
type MCPClient struct {
    registry *config.MCPServerRegistry

    mu          sync.RWMutex
    sessions    map[string]*mcpsdk.ClientSession // serverID → session
    clients     map[string]*mcpsdk.Client        // serverID → client (for reconnection)
    failedServers map[string]string              // serverID → error message

    // Tool cache (populated on first ListTools, never invalidated — each MCPClient
    // instance is short-lived per session, so the cache is naturally fresh)
    toolCache   map[string][]mcpsdk.Tool         // serverID → tools
    toolCacheMu sync.RWMutex

    logger *slog.Logger
}

// Initialize connects to all configured MCP servers.
// Servers that fail to connect are recorded in failedServers.
// At startup (readiness probe context), the caller should treat failures as fatal.
// Per-session callers use fail-open: partial initialization is acceptable
// (failed servers are reported to the LLM via the prompt builder).
func (c *MCPClient) Initialize(ctx context.Context, serverIDs []string) error

// InitializeServer connects to a single MCP server.
// Returns nil if already connected. Used for lazy initialization and recovery.
func (c *MCPClient) InitializeServer(ctx context.Context, serverID string) error

// ListTools returns tools from a specific server. Uses cache if available.
func (c *MCPClient) ListTools(ctx context.Context, serverID string) ([]mcpsdk.Tool, error)

// ListAllTools returns tools from all connected servers. Returns partial
// results if some servers fail (logs errors, does not abort).
func (c *MCPClient) ListAllTools(ctx context.Context) (map[string][]mcpsdk.Tool, error)

// CallTool executes a tool call on the specified server.
// Handles recovery (retry with session recreation) on transport failures.
func (c *MCPClient) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error)

// Close shuts down all sessions and transports gracefully.
func (c *MCPClient) Close() error

// HasSession checks if a server has an active session.
func (c *MCPClient) HasSession(serverID string) bool

// FailedServers returns the map of servers that failed to initialize.
func (c *MCPClient) FailedServers() map[string]string
```

**Initialization flow:**

```
MCPClient.Initialize(ctx, ["kubernetes-server", "argocd-server"])
  │
  ├─ for each serverID:
  │    ├─ registry.Get(serverID) → MCPServerConfig
  │    ├─ createTransport(config.Transport) → mcp.Transport
  │    ├─ mcp.NewClient(implementation, nil) → client
  │    ├─ client.Connect(ctx, transport, nil) → session
  │    ├─ on success: store in sessions map
  │    └─ on failure: record in failedServers, log warning, continue
  │
  └─ return nil (caller decides whether failures are fatal — see below)
```

**Two initialization contexts:**
- **Startup (readiness probe)**: All servers must initialize. If `FailedServers()` is non-empty after `Initialize()`, TARSy does not become ready. This catches broken configs and bugs before taking traffic.
- **Per-session**: Partial initialization is acceptable. Failed servers are communicated to the LLM in the system prompt so it can adjust its investigation strategy (see "Prompt Builder Integration" below).

**Session map is keyed by server ID**, not transport. One session per server per MCPClient instance.

### 2. Transport Creation

Maps `config.TransportConfig` to MCP SDK transport types.

```go
package mcp

import (
    "fmt"
    "os/exec"
    "os"
    "strings"

    mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
    "github.com/codeready-toolchain/tarsy/pkg/config"
)

// createTransport creates an MCP SDK transport from config.
func createTransport(cfg config.TransportConfig) (mcpsdk.Transport, error) {
    switch cfg.Type {
    case config.TransportTypeStdio:
        return createStdioTransport(cfg)
    case config.TransportTypeHTTP:
        return createHTTPTransport(cfg)
    case config.TransportTypeSSE:
        return createSSETransport(cfg)
    default:
        return nil, fmt.Errorf("unsupported transport type: %s", cfg.Type)
    }
}

func createStdioTransport(cfg config.TransportConfig) (*mcpsdk.CommandTransport, error) {
    if cfg.Command == "" {
        return nil, fmt.Errorf("stdio transport requires command")
    }

    cmd := exec.Command(cfg.Command, cfg.Args...)

    // Resolve environment variables with template substitution
    // {{.VAR}} patterns were already resolved by config loader
    env := os.Environ()
    if len(cfg.Env) > 0 {
        for k, v := range cfg.Env {
            env = append(env, fmt.Sprintf("%s=%s", k, v))
        }
    }
    cmd.Env = env

    return &mcpsdk.CommandTransport{Command: cmd}, nil
}

func createHTTPTransport(cfg config.TransportConfig) (*mcpsdk.StreamableClientTransport, error) {
    if cfg.URL == "" {
        return nil, fmt.Errorf("HTTP transport requires url")
    }
    transport := &mcpsdk.StreamableClientTransport{
        Endpoint: cfg.URL,
    }
    // Custom HTTP client for auth, TLS, timeouts
    if cfg.BearerToken != "" || cfg.VerifySSL != nil || cfg.Timeout > 0 {
        transport.HTTPClient = buildHTTPClient(cfg)
    }
    return transport, nil
}

func createSSETransport(cfg config.TransportConfig) (*mcpsdk.SSEClientTransport, error) {
    if cfg.URL == "" {
        return nil, fmt.Errorf("SSE transport requires url")
    }
    transport := &mcpsdk.SSEClientTransport{
        Endpoint: cfg.URL,
    }
    if cfg.BearerToken != "" || cfg.VerifySSL != nil || cfg.Timeout > 0 {
        transport.HTTPClient = buildHTTPClient(cfg)
    }
    return transport, nil
}
```

**Environment handling for stdio (decided in Q8: inherit + override):**
- `cmd.Env` = parent process env (`os.Environ()`) + config overrides from `TransportConfig.Env` map
- Subprocess inherits `PATH`, `HOME`, etc. automatically
- Template vars (e.g., `{{.KUBECONFIG}}`) are already resolved by the config loader

**HTTP client customization:**
- Bearer token: via `Authorization` header in custom `http.RoundTripper`.
- TLS verification: via `tls.Config{InsecureSkipVerify: !*cfg.VerifySSL}` (default: verify).
- Timeout: via `http.Client.Timeout`.

### 3. MCPToolExecutor

The bridge between controllers and MCP. Implements the existing `agent.ToolExecutor` interface.

```go
package mcp

import (
    "context"

    "github.com/codeready-toolchain/tarsy/pkg/agent"
    "github.com/codeready-toolchain/tarsy/pkg/config"
)

// MCPToolExecutor implements agent.ToolExecutor backed by real MCP servers.
// Created per-session by MCPClientFactory.
type MCPToolExecutor struct {
    client   *MCPClient
    registry *config.MCPServerRegistry

    // Resolved list of server IDs this executor can access.
    // Determined by agent config hierarchy + per-alert MCP selection override.
    serverIDs []string

    // Optional tool filter per server (from MCP selection override).
    // nil means all tools for that server are available.
    toolFilter map[string][]string // serverID → allowed tool names (nil = all)
}

// NewMCPToolExecutor creates a new executor for the given servers.
func NewMCPToolExecutor(
    client *MCPClient,
    registry *config.MCPServerRegistry,
    serverIDs []string,
    toolFilter map[string][]string,
) *MCPToolExecutor

// Execute runs a tool call via MCP.
//
// Flow:
//   1. Normalize tool name (server__tool → server.tool for NativeThinking)
//   2. Split and validate server.tool name
//   3. Check server is in allowed serverIDs
//   4. Check tool is in allowed tools (if filter set)
//   5. Parse Arguments string into map[string]any
//   6. Call MCPClient.CallTool(ctx, serverID, toolName, params)
//   7. Convert MCP result to ToolResult
//   8. Apply data masking (Phase 4.2 — stub in 4.1)
//   9. Check if summarization needed (Phase 4.3 — stub in 4.1)
//  10. Return ToolResult
func (e *MCPToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
    // Step 1: Normalize name
    name := NormalizeToolName(call.Name)

    // Step 2-4: Route and validate
    serverID, toolName, err := e.resolveToolCall(name)
    if err != nil {
        return &agent.ToolResult{
            CallID:  call.ID,
            Name:    call.Name,
            Content: err.Error(),
            IsError: true,
        }, nil // Return error as content, not as Go error (matches MCP convention)
    }

    // Step 5: Parse arguments
    params, err := ParseActionInput(call.Arguments)
    if err != nil {
        return &agent.ToolResult{
            CallID:  call.ID,
            Name:    call.Name,
            Content: fmt.Sprintf("Failed to parse tool arguments: %s", err),
            IsError: true,
        }, nil
    }

    // Step 6: Execute via MCP
    result, err := e.client.CallTool(ctx, serverID, toolName, params)
    if err != nil {
        return &agent.ToolResult{
            CallID:  call.ID,
            Name:    call.Name,
            Content: fmt.Sprintf("MCP tool execution failed: %s", err),
            IsError: true,
        }, nil
    }

    // Step 7: Convert to ToolResult
    content := extractTextContent(result)

    // Steps 8-9: masking and summarization (stubs for Phase 4.1)
    // TODO (Phase 4.2): content = e.maskingService.MaskResult(content, serverID)
    // TODO (Phase 4.3): content = e.maybeSummarize(ctx, content, serverID, toolName)

    return &agent.ToolResult{
        CallID:  call.ID,
        Name:    call.Name,
        Content: content,
        IsError: result.IsError,
    }, nil
}

// ListTools returns all available tools from configured MCP servers.
// Tools are returned with server-prefixed names (e.g., "kubernetes-server.get_pods").
func (e *MCPToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error) {
    var allTools []agent.ToolDefinition

    for _, serverID := range e.serverIDs {
        tools, err := e.client.ListTools(ctx, serverID)
        if err != nil {
            // Log error but continue — partial tools are better than none
            slog.Warn("Failed to list tools from MCP server",
                "server", serverID, "error", err)
            continue
        }

        for _, tool := range tools {
            // Apply tool filter if set
            if filter, ok := e.toolFilter[serverID]; ok && len(filter) > 0 {
                if !slices.Contains(filter, tool.Name) {
                    continue
                }
            }

            allTools = append(allTools, agent.ToolDefinition{
                Name:             fmt.Sprintf("%s.%s", serverID, tool.Name),
                Description:      tool.Description,
                ParametersSchema: marshalSchema(tool.InputSchema),
            })
        }
    }

    if len(allTools) == 0 {
        return nil, nil // Consistent with StubToolExecutor contract
    }
    return allTools, nil
}
```

**Key design decisions in MCPToolExecutor:**

1. **Error returns as content, not Go errors**: When a tool call fails (invalid name, parse error, MCP error), the error is returned in `ToolResult.Content` with `IsError: true`. Go-level errors are reserved for infrastructure failures (context cancelled, nil client). This matches the MCP SDK convention where `CallToolResult.IsError` carries tool-level errors.

2. **Tool name normalization**: NativeThinking uses `server__tool` (double underscore, as Gemini function names can't contain dots), while ReAct uses `server.tool`. The executor normalizes both formats.

3. **Partial tool listing**: If one server fails to list tools, the executor continues with other servers. This preserves availability when one MCP server is unhealthy.

### 4. ActionInput Parameter Parsing (`params.go`)

Ported from old TARSy's `_parse_action_parameters()` with the same multi-format cascade.

```go
package mcp

// ParseActionInput parses a raw ActionInput string into structured parameters.
//
// Parsing cascade (first successful parse wins):
//   1. JSON object → map[string]any
//   2. JSON non-object (string, number, array) → {"input": value}
//   3. YAML with complex structures (arrays, nested maps) → map[string]any
//   4. Key-value pairs (key: value or key=value, comma/newline separated)
//   5. Single raw string → {"input": string}
//
// Empty input returns empty map (for no-parameter tools).
func ParseActionInput(input string) (map[string]any, error) {
    input = strings.TrimSpace(input)
    if input == "" {
        return map[string]any{}, nil
    }

    // 1. Try JSON
    if result, ok := tryParseJSON(input); ok {
        return result, nil
    }

    // 2. Try YAML (only if it has structure — arrays or nested maps)
    if result, ok := tryParseYAML(input); ok {
        return result, nil
    }

    // 3. Try key-value pairs
    if result, ok := tryParseKeyValue(input); ok {
        return result, nil
    }

    // 4. Fallback: raw string
    return map[string]any{"input": input}, nil
}

// tryParseJSON attempts to parse input as JSON.
// Returns (result, true) on success.
func tryParseJSON(input string) (map[string]any, bool) {
    // Quick-reject: must start with JSON-like character
    if len(input) == 0 || (input[0] != '{' && input[0] != '[' && input[0] != '"') {
        return nil, false
    }

    var raw any
    if err := json.Unmarshal([]byte(input), &raw); err != nil {
        return nil, false
    }

    // If it's already a map, use directly
    if m, ok := raw.(map[string]any); ok {
        return m, true
    }

    // Non-object JSON: wrap in {"input": value}
    return map[string]any{"input": raw}, true
}

// tryParseYAML attempts to parse input as YAML.
// Only accepts if result is a map with complex values (arrays, nested maps).
// Simple key: value pairs are handled by tryParseKeyValue instead, to avoid
// false positives on plain text that happens to look like YAML.
func tryParseYAML(input string) (map[string]any, bool) {
    var result map[string]any
    if err := yaml.Unmarshal([]byte(input), &result); err != nil {
        return nil, false
    }
    if result == nil || len(result) == 0 {
        return nil, false
    }

    // Only accept YAML if it contains complex structures.
    // Plain "key: value" lines are handled by key-value parser.
    if hasComplexValues(result) {
        return result, true
    }
    return nil, false
}

// tryParseKeyValue attempts to parse "key: value" or "key=value" pairs
// separated by commas or newlines.
func tryParseKeyValue(input string) (map[string]any, bool) {
    // Split on commas and newlines
    parts := splitKeyValueParts(input)
    if len(parts) == 0 {
        return nil, false
    }

    result := make(map[string]any)
    for _, part := range parts {
        key, value, ok := parseKeyValuePair(part)
        if !ok {
            return nil, false // If any part fails, reject the whole thing
        }
        result[key] = coerceValue(value)
    }

    if len(result) == 0 {
        return nil, false
    }
    return result, true
}

// coerceValue converts string values to appropriate Go types.
// Matches old TARSy's _convert_parameter_value().
func coerceValue(s string) any {
    s = strings.TrimSpace(s)
    lower := strings.ToLower(s)

    // Booleans
    if lower == "true" {
        return true
    }
    if lower == "false" {
        return false
    }

    // Null
    if lower == "null" || lower == "none" {
        return nil
    }

    // Integer
    if i, err := strconv.ParseInt(s, 10, 64); err == nil {
        return i
    }

    // Float
    if f, err := strconv.ParseFloat(s, 64); err == nil {
        return f
    }

    return s
}
```

**Why not parse in the parser?**

The ReAct parser intentionally keeps `ActionInput` as raw text because:
1. The parser doesn't know the target tool's parameter schema
2. Parsing failures should result in MCP errors (retryable), not malformed responses
3. NativeThinking already provides structured JSON — parsing there is trivial
4. Clean separation: parser parses LLM text format, executor parses tool parameters

### 5. Tool Name Router (`router.go`)

Handles tool name normalization, splitting, and validation.

```go
package mcp

import (
    "fmt"
    "regexp"
    "strings"
)

var toolNameRegex = regexp.MustCompile(`^([\w][\w-]*)\.([\w][\w-]*)$`)

// NormalizeToolName converts tool names between controller formats.
// NativeThinking uses "server__tool" (Gemini function name restriction).
// ReAct uses "server.tool" (text-based).
// Normalizes both to "server.tool" for routing.
func NormalizeToolName(name string) string {
    // Convert double-underscore to dot (NativeThinking → canonical)
    if strings.Contains(name, "__") && !strings.Contains(name, ".") {
        return strings.Replace(name, "__", ".", 1)
    }
    return name
}

// SplitToolName splits "server.tool" into (serverID, toolName, error).
// Validates format with strict regex: server and tool parts must be
// word characters and hyphens, non-empty.
func SplitToolName(name string) (serverID, toolName string, err error) {
    matches := toolNameRegex.FindStringSubmatch(name)
    if matches == nil {
        return "", "", fmt.Errorf(
            "invalid tool name %q: must be in 'server.tool' format "+
                "(e.g., 'kubernetes-server.get_pods')", name)
    }
    return matches[1], matches[2], nil
}

// resolveToolCall validates a tool call against the executor's configuration.
// Returns the serverID and bare toolName, or an error.
func (e *MCPToolExecutor) resolveToolCall(name string) (serverID, toolName string, err error) {
    serverID, toolName, err = SplitToolName(name)
    if err != nil {
        return "", "", err
    }

    // Check server is in allowed list
    if !slices.Contains(e.serverIDs, serverID) {
        return "", "", fmt.Errorf(
            "MCP server %q is not available for this execution. "+
                "Available servers: %s", serverID, strings.Join(e.serverIDs, ", "))
    }

    // Check tool filter (per-alert MCP selection)
    if filter, ok := e.toolFilter[serverID]; ok && len(filter) > 0 {
        if !slices.Contains(filter, toolName) {
            return "", "", fmt.Errorf(
                "tool %q is not available on server %q. "+
                    "Available tools: %s", toolName, serverID, strings.Join(filter, ", "))
        }
    }

    return serverID, toolName, nil
}
```

**NativeThinking tool name mapping:**

When tools are listed for NativeThinking (Gemini structured function calling), tool names must be valid function identifiers. The convention from old TARSy: replace `.` with `__` for Gemini, reverse on execution.

The **NativeThinking controller** handles the `.` → `__` conversion when passing tools to the LLM (decided in Q3). The controller already knows it's Gemini-specific, so it's the right place for this. The `MCPToolExecutor`'s `NormalizeToolName()` handles the reverse (`__` → `.`) transparently when the LLM calls a tool back.

### 6. Error Handling & Recovery (`recovery.go`)

Ported from old TARSy's classification logic, adapted for Go SDK error types.

```go
package mcp

import (
    "errors"
    "net"
)

// RecoveryAction determines how to handle an MCP operation failure.
type RecoveryAction int

const (
    // NoRetry — the error is not recoverable (bad request, auth failure, timeout).
    NoRetry RecoveryAction = iota
    // RetrySameSession — transient error, retry with existing session (rate limit).
    RetrySameSession
    // RetryNewSession — transport failure, recreate session and retry.
    RetryNewSession
)

// Recovery configuration constants.
const (
    // MaxRetries is the number of retry attempts after the initial failure.
    // One retry balances recovery from transient failures without hammering a sick server.
    MaxRetries = 1

    // ReinitTimeout is the deadline for recreating an MCP session during recovery.
    // Covers transport setup + MCP Initialize handshake. Stdio subprocesses (npx)
    // can be slow to start, so 10s gives enough room.
    ReinitTimeout = 10 * time.Second

    // OperationTimeout is the per-call deadline for CallTool and ListTools.
    // The MCP Go SDK has no built-in timeout — it relies on context.Context.
    // This wraps each MCP call with context.WithTimeout.
    // Set conservatively: some tools are legitimately slow (large K8s queries,
    // log retrieval, monitoring data). The iteration timeout (120s) is the hard
    // ceiling above this — OperationTimeout should be comfortably below it to
    // leave room for LLM processing in the same iteration.
    OperationTimeout = 90 * time.Second

    // RetryBackoffMin/Max define the jittered backoff range between retries.
    // Randomized within [min, max] to avoid thundering herd on shared servers.
    RetryBackoffMin = 250 * time.Millisecond
    RetryBackoffMax = 750 * time.Millisecond
)

// ClassifyError determines the recovery action for an MCP operation error.
func ClassifyError(err error) RecoveryAction {
    if err == nil {
        return NoRetry
    }

    // Context errors — no retry
    if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
        return NoRetry
    }

    // Network errors — retry with new session
    var netErr net.Error
    if errors.As(err, &netErr) {
        if netErr.Timeout() {
            return NoRetry // Timeout: don't retry (could be slow server)
        }
        return RetryNewSession
    }

    // MCP JSON-RPC errors — generally not retryable (bad request, method not found)
    // The Go SDK returns these as structured errors
    if isMCPProtocolError(err) {
        return NoRetry
    }

    // Connection-level errors — retry with new session
    if isConnectionError(err) {
        return RetryNewSession
    }

    // Default: no retry (unknown errors are not safe to retry)
    return NoRetry
}
```

**Recovery flow in MCPClient.CallTool:**

```
CallTool(ctx, serverID, toolName, args)
  │
  ├─ Attempt 1: session.CallTool(ctx, params)
  │    ├─ Success → return result
  │    └─ Error → ClassifyError(err)
  │         ├─ NoRetry → return error
  │         ├─ RetrySameSession → sleep(backoff) → Attempt 2
  │         └─ RetryNewSession → recreateSession(serverID) → Attempt 2
  │
  └─ Attempt 2: session.CallTool(ctx, params)
       ├─ Success → return result
       └─ Error → return error (max retries exceeded)
```

**Session recreation** uses a per-server `sync.Mutex` to prevent concurrent recreation of the same session (same pattern as old TARSy's `_reinit_locks`).

### 7. MCPClientFactory

Creates per-session MCP client instances. Called by the session executor.

```go
package mcp

import (
    "context"

    "github.com/codeready-toolchain/tarsy/pkg/config"
)

// MCPClientFactory creates MCPClient instances for sessions.
type MCPClientFactory struct {
    registry *config.MCPServerRegistry
}

// NewMCPClientFactory creates a new factory.
func NewMCPClientFactory(registry *config.MCPServerRegistry) *MCPClientFactory {
    return &MCPClientFactory{registry: registry}
}

// CreateClient creates a new MCPClient connected to the specified servers.
// The caller is responsible for calling Close() when done.
func (f *MCPClientFactory) CreateClient(ctx context.Context, serverIDs []string) (*MCPClient, error) {
    client := newMCPClient(f.registry)
    if err := client.Initialize(ctx, serverIDs); err != nil {
        client.Close() // Clean up partial initialization
        return nil, err
    }
    return client, nil
}

// CreateToolExecutor creates a fully-wired MCPToolExecutor for a session.
// This is the primary entry point used by the session executor.
func (f *MCPClientFactory) CreateToolExecutor(
    ctx context.Context,
    serverIDs []string,
    toolFilter map[string][]string,
) (*MCPToolExecutor, error) {
    client, err := f.CreateClient(ctx, serverIDs)
    if err != nil {
        return nil, err
    }
    return NewMCPToolExecutor(client, f.registry, serverIDs, toolFilter), nil
}
```

### 8. MCPHealthMonitor

Background service that monitors MCP server health at runtime (post-startup). Startup validation is handled separately by the eager initialization check (Q7). The health monitor detects degradation after TARSy is running — servers going down, becoming unresponsive, etc.

```go
package mcp

import (
    "context"
    "sync"
    "time"
)

// HealthStatus represents the health state of an MCP server.
type HealthStatus struct {
    ServerID  string
    Healthy   bool
    Error     string       // Last error message (empty if healthy)
    LastCheck time.Time
    ToolCount int          // Number of tools available (from last successful check)
}

// MCPHealthMonitor periodically checks MCP server health.
// Uses a dedicated MCPClient (not shared with session processing).
type MCPHealthMonitor struct {
    factory       *MCPClientFactory
    registry      *config.MCPServerRegistry
    checkInterval time.Duration
    pingTimeout   time.Duration

    warningsService *services.SystemWarningsService

    mu       sync.RWMutex
    statuses map[string]*HealthStatus // serverID → status
    client   *MCPClient              // Dedicated health-check client

    // Tool cache for system endpoint (serves GET /api/v1/mcp/tools)
    toolCache   map[string][]mcpsdk.Tool
    toolCacheMu sync.RWMutex

    cancel context.CancelFunc
}

// NewMCPHealthMonitor creates a new health monitor.
func NewMCPHealthMonitor(
    factory *MCPClientFactory,
    registry *config.MCPServerRegistry,
    warnings *services.SystemWarningsService,
) *MCPHealthMonitor {
    return &MCPHealthMonitor{
        factory:         factory,
        registry:        registry,
        warningsService: warnings,
        checkInterval:   15 * time.Second,
        pingTimeout:     5 * time.Second,
        statuses:        make(map[string]*HealthStatus),
        toolCache:       make(map[string][]mcpsdk.Tool),
    }
}

// Start begins the background health check loop.
func (m *MCPHealthMonitor) Start(ctx context.Context) error

// Stop gracefully shuts down the health monitor.
func (m *MCPHealthMonitor) Stop()

// GetStatuses returns current health statuses for all servers.
func (m *MCPHealthMonitor) GetStatuses() map[string]*HealthStatus

// GetCachedTools returns the cached tool list (for system endpoints).
func (m *MCPHealthMonitor) GetCachedTools() map[string][]mcpsdk.Tool

// IsHealthy returns true if all configured servers are healthy.
func (m *MCPHealthMonitor) IsHealthy() bool
```

**Health check flow (per server, every 15s):**

```
checkServer(ctx, serverID)
  │
  ├─ Has session?
  │    ├─ Yes → listTools(ctx, serverID) with 5s timeout
  │    │    ├─ Success → update cache, mark healthy, warningsService.ClearByServerID()
  │    │    └─ Failure → try reinitialize
  │    └─ No → try reinitialize
  │
  └─ reinitialize:
       ├─ client.InitializeServer(ctx, serverID)
       │    ├─ Success → listTools → update cache, mark healthy, warningsService.ClearByServerID()
       │    └─ Failure → mark unhealthy, warningsService.AddWarning()
```

**Key differences from old TARSy**:
- Startup failures are **fatal** (readiness probe fails) — old TARSy logged warnings but continued
- The health monitor is for **runtime degradation only** — it doesn't need to handle first-time initialization failures

### 9. SystemWarningsService

In-memory warning store for non-fatal system issues visible in the dashboard. Ported from old TARSy's `SystemWarningsService` (identical concept, ~40 lines of Go).

```go
package services

import (
    "sync"
    "time"

    "github.com/google/uuid"
)

// WarningCategory constants for categorizing system warnings.
const (
    WarningCategoryMCPHealth = "mcp_health"       // MCP server became unhealthy at runtime
)

// SystemWarning represents a non-fatal system issue.
type SystemWarning struct {
    ID        string    `json:"id"`
    Category  string    `json:"category"`
    Message   string    `json:"message"`
    Details   string    `json:"details,omitempty"`
    ServerID  string    `json:"server_id,omitempty"` // For MCP-related warnings
    CreatedAt time.Time `json:"created_at"`
}

// SystemWarningsService manages in-memory system warnings.
// Thread-safe. Not persisted — warnings are transient and reset on restart.
type SystemWarningsService struct {
    mu       sync.RWMutex
    warnings map[string]*SystemWarning // warningID → warning
}

func NewSystemWarningsService() *SystemWarningsService

// AddWarning adds a warning and returns its ID.
func (s *SystemWarningsService) AddWarning(category, message, details, serverID string) string

// GetWarnings returns all active warnings.
func (s *SystemWarningsService) GetWarnings() []*SystemWarning

// ClearByServerID removes a warning matching category + serverID.
// Used by MCPHealthMonitor to clear warnings when servers recover.
func (s *SystemWarningsService) ClearByServerID(category, serverID string) bool
```

**Integration points:**

1. **MCPHealthMonitor** — calls `AddWarning` when a server becomes unhealthy, `ClearByServerID` when it recovers:

```go
// In health monitor check loop:
if healthy {
    s.warningsService.ClearByServerID(WarningCategoryMCPHealth, serverID)
} else {
    s.warningsService.AddWarning(WarningCategoryMCPHealth,
        fmt.Sprintf("MCP server %q is unreachable", serverID),
        err.Error(), serverID)
}
```

2. **`/health` endpoint** — includes warnings in the response:

```go
// In health handler:
warnings := warningsService.GetWarnings()
// Include in health response JSON (warnings don't cause 503 — they're informational)
```

3. **Dashboard** (Phase 6) — polls health endpoint and displays warnings.

**Location**: `pkg/services/system_warnings.go` — alongside other services, not in `pkg/mcp/` (it's a general-purpose service that future phases will also use for non-MCP warnings).

---

## Integration Points

### Session Executor Changes (`pkg/queue/executor.go`)

The session executor needs to create a real `MCPToolExecutor` instead of `StubToolExecutor`.

```go
// Before (Phase 3.2):
execCtx.ToolExecutor = agent.NewStubToolExecutor(nil)

// After (Phase 4.1):
toolExecutor, err := e.mcpFactory.CreateToolExecutor(ctx, resolvedConfig.MCPServers, nil)
if err != nil {
    // Non-fatal: executor can run without tools (synthesis, no-tool agents)
    logger.Warn("Failed to create MCP tool executor, using stub", "error", err)
    toolExecutor = agent.NewStubToolExecutor(nil)
}
execCtx.ToolExecutor = toolExecutor

// ... after agent execution:
defer execCtx.ToolExecutor.Close() // Close() is on the ToolExecutor interface (Q11)
```

### ToolExecutor Interface Change

`Close() error` is added to the `agent.ToolExecutor` interface (decided in Q11):

```go
type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
    ListTools(ctx context.Context) ([]ToolDefinition, error)
    Close() error // NEW: cleanup transports and subprocesses
}
```

`StubToolExecutor` gets a no-op: `func (s *StubToolExecutor) Close() error { return nil }`

**RealSessionExecutor changes:**

```go
type RealSessionExecutor struct {
    cfg            *config.Config
    dbClient       *ent.Client
    llmClient      agent.LLMClient
    eventPublisher agent.EventPublisher
    agentFactory   *agent.AgentFactory
    promptBuilder  *prompt.PromptBuilder
    mcpFactory     *mcp.MCPClientFactory  // NEW
}

func NewRealSessionExecutor(
    cfg *config.Config, dbClient *ent.Client,
    llmClient agent.LLMClient, eventPublisher agent.EventPublisher,
    mcpFactory *mcp.MCPClientFactory,  // NEW parameter
) *RealSessionExecutor
```

### NativeThinking Controller Changes

The NativeThinking controller normalizes tool names to Gemini-compatible format (decided in Q3):

```go
// In NativeThinking controller, when building LLM input:
tools, _ := execCtx.ToolExecutor.ListTools(ctx)
geminiTools := make([]agent.ToolDefinition, len(tools))
for i, t := range tools {
    geminiTools[i] = t
    geminiTools[i].Name = strings.Replace(t.Name, ".", "__", 1)
}
// Pass geminiTools to LLM — MCPToolExecutor.NormalizeToolName() reverses on execute
```

### Server Startup Changes (`cmd/tarsy/main.go`)

Startup performs eager MCP initialization. If any configured server fails, TARSy does not become ready — the readiness probe fails. This catches broken configs before taking traffic (decided in Q7). Rolling updates in OpenShift/K8s ensure no downtime.

```go
// Create system warnings service (in-memory, for dashboard / health endpoint)
warningsService := services.NewSystemWarningsService()

// Create MCP client factory
mcpFactory := mcp.NewMCPClientFactory(cfg.MCPServerRegistry)

// Eager MCP initialization — fatal on failure (Q7)
// Validates all configured servers can connect before accepting traffic.
startupClient, err := mcpFactory.CreateClient(ctx, cfg.AllMCPServerIDs())
if err != nil {
    logger.Error("MCP server initialization failed — not ready", "error", err)
    os.Exit(1)
}
if failed := startupClient.FailedServers(); len(failed) > 0 {
    logger.Error("MCP servers failed to initialize — not ready", "failed", failed)
    os.Exit(1)
}
startupClient.Close() // Startup client is just for validation

// Create health monitor (runtime degradation detection — Q7)
healthMonitor := mcp.NewMCPHealthMonitor(mcpFactory, cfg.MCPServerRegistry, warningsService)
if err := healthMonitor.Start(ctx); err != nil {
    logger.Error("MCP health monitor failed to start", "error", err)
    os.Exit(1)
}
defer healthMonitor.Stop()

// Pass factory to session executor
executor := queue.NewRealSessionExecutor(cfg, dbClient, llmClient, publisher, mcpFactory)

// Pass warningsService to health handler
healthHandler := api.NewHealthHandler(healthMonitor, warningsService)
```

### Prompt Builder Integration (Q5: Failed Server Warnings)

When per-session MCP initialization has partial failures, the prompt builder warns the LLM:

```go
// In appendMCPInstructions or a new method:
func (b *PromptBuilder) appendUnavailableServerWarnings(sections []string, failedServers map[string]string) []string {
    if len(failedServers) == 0 {
        return sections
    }
    var sb strings.Builder
    sb.WriteString("## Unavailable MCP Servers\n\n")
    sb.WriteString("The following servers failed to initialize and their tools are NOT available:\n")
    for serverID, errMsg := range failedServers {
        sb.WriteString(fmt.Sprintf("- **%s**: %s\n", serverID, errMsg))
    }
    sb.WriteString("\nDo not attempt to use tools from these servers.")
    return append(sections, sb.String())
}
```

This requires passing `failedServers` through `ExecutionContext` (new field on `ResolvedAgentConfig` or `ExecutionContext` itself).

---

## Tool Lifecycle During Execution

Complete flow from controller tool call to result:

```
ReAct Controller                                NativeThinking Controller
  │                                               │
  │ parsed.Action = "kubernetes-server.get_pods"   │ tc.Name = "kubernetes-server__get_pods"
  │ parsed.ActionInput = "namespace: default"      │ tc.Arguments = '{"namespace":"default"}'
  │                                               │
  ▼                                               ▼
ToolExecutor.Execute(ToolCall{                  ToolExecutor.Execute(ToolCall{
  Name: "kubernetes-server.get_pods",             Name: "kubernetes-server__get_pods",
  Arguments: "namespace: default"                 Arguments: '{"namespace":"default"}'
})                                              })
  │                                               │
  ▼                                               ▼
MCPToolExecutor.Execute()
  │
  ├─ NormalizeToolName: "kubernetes-server__get_pods" → "kubernetes-server.get_pods"
  │                     "kubernetes-server.get_pods"  → "kubernetes-server.get_pods" (no-op)
  │
  ├─ SplitToolName: "kubernetes-server" + "get_pods"
  │
  ├─ resolveToolCall: validate server in allowed list
  │
  ├─ ParseActionInput:
  │    "namespace: default" → tryJSON(fail) → tryYAML(fail) → tryKV(success)
  │    → {"namespace": "default"}
  │
  │    '{"namespace":"default"}' → tryJSON(success) → {"namespace": "default"}
  │
  ├─ MCPClient.CallTool(ctx, "kubernetes-server", "get_pods", {"namespace": "default"})
  │    │
  │    ├─ session.CallTool(ctx, &mcp.CallToolParams{
  │    │      Name: "get_pods",
  │    │      Arguments: map[string]any{"namespace": "default"},
  │    │  })
  │    │
  │    └─ On error: ClassifyError → retry/recreate/fail
  │
  ├─ extractTextContent(result) → "pod1\npod2\npod3..."
  │
  ├─ [Phase 4.2] maskingService.MaskResult(content, "kubernetes-server")
  │
  ├─ [Phase 4.3] maybeSummarize(ctx, content, "kubernetes-server", "get_pods")
  │
  └─ Return ToolResult{Content: "pod1\npod2\npod3...", IsError: false}
```

---

## MCP Result Content Extraction

MCP SDK returns `CallToolResult` with `Content` as a slice of content items (text, image, resource). TARSy only processes text content:

```go
// extractTextContent extracts text from MCP CallToolResult.
// Concatenates all TextContent items, ignoring non-text content.
func extractTextContent(result *mcpsdk.CallToolResult) string {
    var parts []string
    for _, c := range result.Content {
        if tc, ok := c.(*mcpsdk.TextContent); ok {
            parts = append(parts, tc.Text)
        }
    }
    return strings.Join(parts, "\n")
}
```

---

## Testing Strategy

The MCP Go SDK provides `mcp.NewInMemoryTransports()` — a pair of connected in-memory transports that allow spinning up real MCP servers in-process with zero external dependencies. This enables comprehensive integration testing without real clusters, subprocesses, or network.

### Unit Tests

| Component | Test Focus |
|-----------|------------|
| `params_test.go` | JSON, YAML, key-value, raw string parsing; type coercion; edge cases |
| `router_test.go` | Tool name normalization, splitting, validation, error messages |
| `recovery_test.go` | Error classification for all error types |
| `transport_test.go` | Transport creation from config (no actual connections) |
| `executor_test.go` | MCPToolExecutor with mock MCPClient interface; isolated Execute/ListTools logic |
| `health_test.go` | Health monitor state transitions with mock client |
| `system_warnings_test.go` | Warning add/get/clear, thread safety, category filtering |

### Integration Tests (In-Memory MCP Servers)

All integration tests use `mcp.NewInMemoryTransports()` to create real MCP servers in-process. No build tags, no external dependencies — these run in CI alongside unit tests.

#### Test Helper: In-Memory MCP Test Server

```go
// pkg/mcp/testutil_test.go (test-only, not exported)

// testServer creates an in-memory MCP server with the given tools.
// Returns a connected MCPClient session ready for use.
type testTool struct {
    name        string
    description string
    handler     any // MCP SDK tool handler function
}

// startTestServer spins up an in-memory MCP server and returns a connected client session.
func startTestServer(t *testing.T, serverName string, tools []testTool) *mcpsdk.ClientSession {
    t.Helper()
    ctx := context.Background()

    server := mcpsdk.NewServer(&mcpsdk.Implementation{
        Name: serverName, Version: "test",
    }, nil)

    for _, tool := range tools {
        mcpsdk.AddTool(server, &mcpsdk.Tool{
            Name:        tool.name,
            Description: tool.description,
        }, tool.handler)
    }

    clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()

    // Start server in background — stops when test context ends
    go server.Connect(ctx, serverTransport, nil)

    client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tarsy-test", Version: "test"}, nil)
    session, err := client.Connect(ctx, clientTransport, nil)
    require.NoError(t, err)
    t.Cleanup(func() { session.Close() })

    return session
}
```

#### Integration Test Cases

| Test | What It Covers |
|------|----------------|
| **Tool discovery** | ListTools across multiple in-memory servers, verify tool names and schemas |
| **Tool execution E2E** | `MCPToolExecutor.Execute("server.tool", rawInput)` → ActionInput parsing → routing → MCP CallTool → ToolResult |
| **Multi-server routing** | Two in-memory servers with overlapping tool names, verify correct routing by `server.tool` prefix |
| **Error tool** | Server tool returns `IsError: true` — verify `ToolResult.IsError` is set, Go `error` is nil |
| **Tool execution failure** | Server tool returns Go error — verify retry classification and error propagation |
| **Slow tool + timeout** | Server tool sleeps beyond context deadline — verify context cancellation propagates |
| **Unknown tool** | Execute a tool that doesn't exist on the server — verify error message |
| **Invalid server** | Execute with unknown server prefix — verify validation error before MCP call |
| **ActionInput formats** | Execute with JSON, YAML, key-value, and raw string ActionInput — verify each parses correctly through the full pipeline |
| **NativeThinking tool names** | `server__tool` normalization → `server.tool` → correct MCP routing |
| **Health monitor lifecycle** | Start monitor with in-memory servers, verify healthy status, stop a server, verify unhealthy + warning added, restart, verify recovery + warning cleared |
| **Session close** | Create MCPToolExecutor, execute tool, Close() — verify all sessions cleaned up |
| **Per-session isolation** | Two concurrent MCPToolExecutors from same factory — verify independent sessions |

#### Example: Full E2E Tool Execution Test

```go
func TestMCPToolExecutor_Execute_E2E(t *testing.T) {
    ctx := context.Background()

    // Create in-memory MCP server with a "get_pods" tool
    server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "kubernetes", Version: "test"}, nil)
    mcpsdk.AddTool(server, &mcpsdk.Tool{
        Name:        "get_pods",
        Description: "List pods in a namespace",
    }, func(ctx context.Context, req *mcpsdk.CallToolRequest, input struct {
        Namespace string `json:"namespace"`
    }) (*mcpsdk.CallToolResult, struct {
        Pods string `json:"pods"`
    }, error) {
        return nil, struct {
            Pods string `json:"pods"`
        }{Pods: fmt.Sprintf("pod-1, pod-2 (ns: %s)", input.Namespace)}, nil
    })

    clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
    go server.Connect(ctx, serverTransport, nil)

    // Wire up MCPToolExecutor with the in-memory transport
    // (details depend on factory/client wiring — simplified here)
    executor := createTestExecutor(t, map[string]mcpsdk.Transport{
        "kubernetes": clientTransport,
    })
    defer executor.Close()

    // Execute via the same interface the agent uses
    result, err := executor.Execute(ctx, "kubernetes.get_pods", `{"namespace": "production"}`)
    require.NoError(t, err)
    assert.False(t, result.IsError)
    assert.Contains(t, result.Content, "pod-1, pod-2")
    assert.Contains(t, result.Content, "production")
}
```

#### Example: Health Monitor with SystemWarningsService

```go
func TestMCPHealthMonitor_DetectsFailureAndRecovery(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    warnings := services.NewSystemWarningsService()

    // Start with a healthy in-memory server
    server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test-server", Version: "test"}, nil)
    mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "ping", Description: "health check tool"}, pingHandler)

    // Create factory that uses in-memory transports
    factory := createTestFactory(t, server)
    monitor := mcp.NewMCPHealthMonitor(factory, registry, warnings)
    monitor.Start(ctx)
    defer monitor.Stop()

    // Initially healthy — no warnings
    require.Eventually(t, func() bool { return monitor.IsHealthy() }, 5*time.Second, 100*time.Millisecond)
    assert.Empty(t, warnings.GetWarnings())

    // Simulate server failure (cancel the server's context)
    killTestServer(t, "test-server")

    // Monitor detects failure — warning added
    require.Eventually(t, func() bool { return !monitor.IsHealthy() }, 20*time.Second, 500*time.Millisecond)
    assert.Len(t, warnings.GetWarnings(), 1)
    assert.Equal(t, services.WarningCategoryMCPHealth, warnings.GetWarnings()[0].Category)

    // Restart server — monitor detects recovery, warning cleared
    restartTestServer(t, "test-server")
    require.Eventually(t, func() bool { return monitor.IsHealthy() }, 20*time.Second, 500*time.Millisecond)
    assert.Empty(t, warnings.GetWarnings())
}
```

#### Example: Multi-Server Routing

```go
func TestMCPToolExecutor_MultiServerRouting(t *testing.T) {
    ctx := context.Background()

    // Two servers, each with a tool named "list"
    k8sServer := createToolServer(t, "kubernetes", "list", k8sListHandler)
    gitServer := createToolServer(t, "github", "list", gitListHandler)

    executor := createTestExecutor(t, map[string]mcpsdk.Transport{
        "kubernetes": k8sServer.transport,
        "github":     gitServer.transport,
    })
    defer executor.Close()

    // Same tool name, different server prefix → different results
    k8sResult, err := executor.Execute(ctx, "kubernetes.list", `{}`)
    require.NoError(t, err)
    assert.Contains(t, k8sResult.Content, "pods")

    gitResult, err := executor.Execute(ctx, "github.list", `{}`)
    require.NoError(t, err)
    assert.Contains(t, gitResult.Content, "repos")
}
```

### No Real MCP Server Tests

We deliberately avoid tests that require real external MCP servers (e.g., `kubernetes-mcp-server` with a live cluster). The in-memory transport approach tests the identical SDK code paths (protocol, serialization, tool dispatch) without external dependencies. If manual validation against a real server is needed, it can be done locally — not in CI.

---

## Configuration

No new configuration types needed — Phase 2 already defined `MCPServerConfig`, `TransportConfig`, `MaskingConfig`, `SummarizationConfig`. The `MCPServerRegistry` is already populated at startup.

### Hardcoded Constants (`pkg/mcp/`)

Not configurable via YAML — these are operational defaults that match old TARSy. Can be promoted to config if real-world usage reveals the need.

```go
// MCP operation timeouts (MCP Go SDK has no built-in timeouts — all via context.Context)
const (
    MCPInitTimeout       = 30 * time.Second // Per-server initialization timeout (transport + handshake)
    MCPOperationTimeout  = 90 * time.Second // CallTool / ListTools deadline (must be < iteration timeout of 120s)
    MCPHealthPingTimeout = 5 * time.Second  // Health check ping timeout (fast fail for monitoring)
    MCPHealthInterval    = 15 * time.Second // Health check loop interval
)
```

### TransportConfig Extension

The `Env` field needs to be added for stdio transport environment overrides:

```go
type TransportConfig struct {
    Type    TransportType `yaml:"type" validate:"required"`
    Command string        `yaml:"command,omitempty"`
    Args    []string      `yaml:"args,omitempty"`
    Env     map[string]string `yaml:"env,omitempty"` // NEW: environment overrides for stdio
    URL     string        `yaml:"url,omitempty"`
    BearerToken string   `yaml:"bearer_token,omitempty"`
    VerifySSL   *bool    `yaml:"verify_ssl,omitempty"`
    Timeout     int      `yaml:"timeout,omitempty"`
}
```

---

## Deferred to Phase 4.2 (Data Masking)

The `MCPToolExecutor` includes stub hooks for data masking. Phase 4.2 will implement:
- `MaskingService` with compiled regex patterns
- Code-based maskers (Kubernetes Secret masker)
- Integration point in `MCPToolExecutor.Execute()` after MCP call
- Alert payload sanitization

## Deferred to Phase 4.3 (MCP Features)

- Tool result summarization (LLM-based, using `PromptBuilder.BuildMCPSummarizationSystemPrompt`)
- Per-alert MCP selection override (`MCPSelectionConfig`)
- Tool output streaming (`stream.chunk` events during MCP execution)
- MCP server health tracking integration with system warnings

---

## Implementation Order

| Step | Component | Effort | Dependencies |
|------|-----------|--------|--------------|
| 1 | `params.go` + tests | Small | None (pure functions) |
| 2 | `router.go` + tests | Small | None (pure functions) |
| 3 | `transport.go` + tests | Small | config types |
| 4 | `recovery.go` + tests | Small | None (pure functions) |
| 5 | `client.go` + tests | Medium | transport, recovery, MCP SDK |
| 6 | `executor.go` + tests | Medium | client, params, router |
| 7 | `client_factory.go` | Small | client |
| 8 | `system_warnings.go` + tests | Small | None (standalone service) |
| 9 | Executor integration | Medium | executor, factory, queue/executor.go changes |
| 10 | `health.go` + tests | Medium | client, factory, system_warnings |
| 11 | Server startup wiring | Small | factory, health, system_warnings |
| 12 | Integration tests | Medium | All above |

Steps 1-4 are parallelizable (pure logic, no dependencies).
Steps 5-8 build on 1-4 (step 8 is also independent).
Steps 9-11 are the integration layer.

---

## Appendix: Go SDK Type Reference

Key types from `github.com/modelcontextprotocol/go-sdk/mcp`:

```go
// Client creation
func NewClient(impl *Implementation, opts *ClientOptions) *Client
func (c *Client) Connect(ctx context.Context, t Transport, opts *SessionOptions) (*ClientSession, error)

// Transports
type CommandTransport struct { Command *exec.Cmd }
type StreamableClientTransport struct { Endpoint string; HTTPClient *http.Client }
type SSEClientTransport struct { Endpoint string; HTTPClient *http.Client }

// Session operations
func (s *ClientSession) ListTools(ctx context.Context, params *ListToolsParams) (*ListToolsResult, error)
func (s *ClientSession) CallTool(ctx context.Context, params *CallToolParams) (*CallToolResult, error)
func (s *ClientSession) Close() error

// Tool types
type Tool struct { Name string; Description string; InputSchema *jsonschema.Schema }
type CallToolParams struct { Name string; Arguments map[string]any }
type CallToolResult struct { Content []Content; IsError bool }
```
