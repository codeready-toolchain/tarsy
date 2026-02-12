# Phase 6: End-to-End Testing — Design

## Goal

Build a comprehensive e2e test suite that exercises the full TARSy pipeline — from HTTP API request through chain execution to WebSocket event delivery — with minimal mocking. Only external services (LLM, MCP servers) are mocked. Real PostgreSQL, real event streaming (NOTIFY/LISTEN), real WebSocket connections.

Tests verify both **live WebSocket updates** (event sequence, content) and **API responses** (session details, timeline) using golden files for readable, maintainable assertions.

---

## Architecture Overview

### In-Process Test Server (`test/e2e/harness.go`)

Each e2e test boots a full TARSy application **in-process** — the same wiring as `cmd/tarsy/main.go` but with mock externals and a random-port HTTP server. No subprocess, no Docker, no network hops for the Go orchestrator itself.

```
┌─────────────────────────────────────────────────────────┐
│                    TestApp (in-process)                 │
│                                                         │
│  ┌─────────┐  ┌────────────┐  ┌──────────────────────┐  │
│  │ Echo    │  │ WorkerPool │  │ ChatMessageExecutor  │  │
│  │ HTTP    │  │ (1 worker) │  │                      │  │
│  │ Server  │  └─────┬──────┘  └──────────┬───────────┘  │
│  └────┬────┘        │                    │              │
│       │       ┌─────┴──────┐      ┌──────┴───────┐      │
│       │       │ Session    │      │ Chat         │      │
│       │       │ Executor   │      │ Executor     │      │
│       │       └─────┬──────┘      └──────┬───────┘      │
│       │             │                    │              │
│  ┌────┴─────────────┴────────────────────┴───────────┐  │
│  │              Shared Dependencies                  │  │
│  │  EventPublisher ← real (DB + pg_notify)           │  │
│  │  NotifyListener ← real (dedicated pgx conn)       │  │
│  │  ConnectionManager ← real                         │  │
│  │  PostgreSQL ← testcontainers (per-test schema)    │  │
│  │  LLMClient ← ScriptedLLMClient (mock)             │  │
│  │  MCPFactory ← real (InMemory MCP SDK servers)     │  │
│  │  Config ← test-specific YAML or in-code config    │  │
│  └───────────────────────────────────────────────────┘  │
│                                                         │
└─────────────────────────────────────────────────────────┘

Test code:
  ┌─────────────────┐   ┌──────────────────────┐
  │ HTTP Client     │   │ WebSocket Client     │
  │ (net/http)      │   │ (coder/websocket)    │
  │ POST /alerts    │   │ subscribe, collect   │
  │ GET /sessions   │   │ events               │
  │ POST /cancel    │   │                      │
  │ POST /chat      │   │                      │
  └─────────────────┘   └──────────────────────┘
```

### Why In-Process?

| Approach | Pros | Cons |
|----------|------|------|
| In-process | Fast, debuggable, no ports/containers for Go code | Couples test to internal types |
| Subprocess | Isolated | Slow startup, hard to inject mocks, log capture hard |

The `LLMClient` interface makes LLM mocking trivial in-process. MCP uses real `mcp.ClientFactory` + `mcp.ToolExecutor` backed by in-memory MCP SDK servers (`mcpsdk.InMemoryTransport`) with scripted `ToolHandler` functions — the same pattern already proven in `pkg/mcp/executor_test.go`. The only external dependency is PostgreSQL (already solved via testcontainers + per-test schemas).

---

## Test Harness

### `TestApp` (`test/e2e/harness.go`)

```go
// TestApp boots a complete TARSy instance for e2e testing.
type TestApp struct {
    // Core
    Config         *config.Config
    DBClient       *database.Client
    EntClient      *ent.Client
    
    // Mocks / test wiring
    LLMClient      *ScriptedLLMClient
    MCPFactory     *mcp.ClientFactory    // real factory backed by in-memory MCP SDK servers
    
    // Real infrastructure
    EventPublisher *events.EventPublisher
    ConnManager    *events.ConnectionManager
    NotifyListener *events.NotifyListener
    WorkerPool     *queue.WorkerPool
    ChatExecutor   *queue.ChatMessageExecutor
    Server         *api.Server
    
    // Runtime
    BaseURL        string // e.g. "http://127.0.0.1:54321"
    WSURL          string // e.g. "ws://127.0.0.1:54321/ws"
    t              *testing.T
}

// NewTestApp creates and starts a full TARSy test instance.
// Call t.Cleanup-registered shutdown automatically.
func NewTestApp(t *testing.T, opts ...TestAppOption) *TestApp

// TestAppOption configures the test app.
type TestAppOption func(*testAppConfig)

func WithConfig(cfg *config.Config) TestAppOption                                          // Custom config
func WithLLMClient(client *ScriptedLLMClient) TestAppOption                                // Pre-scripted LLM (dual dispatch)
func WithMCPServers(servers map[string]map[string]mcpsdk.ToolHandler) TestAppOption         // In-memory MCP SDK servers
func WithWorkerCount(n int) TestAppOption                                                  // Default: 1
func WithSessionTimeout(d time.Duration) TestAppOption                                     // Default: 30s
```

### Startup Sequence

1. Create per-test PostgreSQL schema (reusing `test/util.SetupTestDatabase`)
2. Create `database.Client` with GIN indexes (reusing `test/database.NewTestClient`)
3. Create `ScriptedLLMClient` with pre-programmed responses
4. Create `mcp.ClientFactory` backed by in-memory MCP SDK servers (via `SetupInMemoryMCP`)
5. Create `EventPublisher` (real — backed by test DB)
6. Create `NotifyListener` (real — dedicated pgx connection to test schema)
7. Create `ConnectionManager` + wire to `NotifyListener`
8. Create `SessionExecutor`, `WorkerPool` (1 worker by default)
9. Create `ChatMessageExecutor`
10. Create `api.Server` with all setters
11. Start HTTP server on `127.0.0.1:0` (OS-assigned random port)
12. Register `t.Cleanup` for orderly shutdown (chat executor → worker pool → HTTP → notify listener → MCP clients → DB)

### Shutdown (via `t.Cleanup`)

```
ChatExecutor.Stop()          // drain active chat goroutines
WorkerPool.Stop()            // drain active sessions
HTTP server Shutdown(5s)     // stop accepting connections
NotifyListener.Stop()        // close LISTEN connection
DB schema DROP CASCADE       // cleanup (handled by SetupTestDatabase)
```

---

## Mock Strategy

### ScriptedLLMClient (`test/e2e/mock_llm.go`)

The `ScriptedLLMClient` implements `agent.LLMClient` with a unified dual-dispatch mock: **sequential fallback** for single-agent stages plus **agent-aware routing** for parallel stages.

```go
type LLMScriptEntry struct {
    // Response content (one of these must be set)
    Chunks  []agent.Chunk   // Pre-built chunks to return (supports TextChunk, ToolCallChunk, etc.)
    Text    string          // Shorthand: auto-wrapped as TextChunk
    Error   error           // Return error from Generate()
}

type ScriptedLLMClient struct {
    mu             sync.Mutex
    sequential     []LLMScriptEntry              // consumed in order for non-routed calls
    seqIndex       int
    routes         map[string][]LLMScriptEntry   // agentName → per-agent script
    routeIndex     map[string]int                // agentName → current index
    capturedInputs []*agent.GenerateInput
}

// AddSequential adds an entry consumed in order (for single-agent stages, synthesis,
// executive summary, chat, summarization calls, etc.)
func (c *ScriptedLLMClient) AddSequential(entry LLMScriptEntry)

// AddRouted adds an entry for a specific agent name (matched from system prompt).
// Used for parallel stages where agents need differentiated responses.
func (c *ScriptedLLMClient) AddRouted(agentName string, entry LLMScriptEntry)

func (c *ScriptedLLMClient) Generate(ctx context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error)
func (c *ScriptedLLMClient) Close() error
func (c *ScriptedLLMClient) CapturedInputs() []*agent.GenerateInput
func (c *ScriptedLLMClient) CallCount() int
```

**Dispatch logic**: On each `Generate()` call, extract agent name from system prompt. If a route exists for that agent name and has remaining entries, use it. Otherwise, consume the next sequential entry.

**Why dual dispatch?** Parallel agents call `Generate()` concurrently — call order is non-deterministic. Agent-aware routing ensures each parallel agent gets its intended response regardless of goroutine scheduling. Sequential entries handle everything else (synthesis, executive summary, chat, summarization) with minimal ceremony.

**ToolCallChunk support**: For NativeThinking tests, entries use `Chunks` with `ToolCallChunk` objects. A typical NativeThinking agent flow requires at least 2 entries: first returns tool calls, second returns text (final answer). ReAct agents use text-based tool calls parsed from `TextChunk` content.

### MCP: In-Memory SDK Servers (`test/e2e/mcp_helpers.go`)

MCP tool calls are **core infrastructure** — most e2e tests include them. NativeThinking with MCP tool calls is the primary production path. Both NativeThinking and ReAct strategies are exercised.

Rather than replacing `mcp.ClientFactory` with a custom mock, we use the **real** `mcp.Client` → `mcp.ToolExecutor` pipeline backed by **in-memory MCP SDK servers**. This is the same approach already proven in `pkg/mcp/executor_test.go` and `pkg/mcp/integration_test.go`.

**Why real MCP stack instead of a custom ScriptedToolExecutor?**
- Exercises the full `mcp.ToolExecutor` code: tool name splitting (`kubernetes.get_pods` → server `kubernetes`, tool `get_pods`), NativeThinking name mangling (`kubernetes__get_pods` → `kubernetes.get_pods`), argument parsing (JSON and key-value), masking, error handling
- No custom `agent.ToolExecutor` mock to maintain — just `mcpsdk.ToolHandler` functions
- Already proven in existing integration tests

**Setup pattern** (adapted from `pkg/mcp/executor_test.go:newTestExecutor`):

```go
// SetupInMemoryMCP creates in-memory MCP servers with scripted tool handlers
// and wires them into a real mcp.ClientFactory for use in TestApp.
func SetupInMemoryMCP(t *testing.T, servers map[string]map[string]mcpsdk.ToolHandler) *mcp.ClientFactory {
    registry := config.NewMCPServerRegistry(nil)
    client := mcp.NewClientForTest(registry)  // unexported newClient exposed via test helper

    for serverID, tools := range servers {
        // Create in-memory MCP server with registered tools
        server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: serverID, Version: "test"})
        for toolName, handler := range tools {
            server.AddTool(&mcpsdk.Tool{Name: toolName}, handler)
        }

        // Wire client ↔ server via in-memory transport (no network)
        clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
        go func() { _ = server.ServeTransport(context.Background(), serverTransport) }()

        sdkClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "tarsy-test", Version: "test"}, nil)
        session, err := sdkClient.Connect(context.Background(), clientTransport, nil)
        require.NoError(t, err)

        client.InjectSession(serverID, sdkClient, session)  // test-only method
    }

    return mcp.NewClientFactoryFromClient(client, nil)  // nil masking for most tests
}
```

**Scripted tool handlers**: Each `ToolHandler` is a simple function returning predetermined results:

```go
// Example: setting up MCP servers for a test
mcpFactory := SetupInMemoryMCP(t, map[string]map[string]mcpsdk.ToolHandler{
    "kubernetes": {
        "get_pods": func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
            return &mcpsdk.CallToolResult{
                Content: []mcpsdk.Content{&mcpsdk.TextContent{
                    Text: `[{"name":"app-pod-1","status":"OOMKilled","restarts":5}]`,
                }},
            }, nil
        },
        "get_events": func(_ context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
            return &mcpsdk.CallToolResult{
                Content: []mcpsdk.Content{&mcpsdk.TextContent{
                    Text: `OOMKilled event at 14:32 UTC`,
                }},
            }, nil
        },
    },
})
```

**Dynamic handlers**: For tests needing argument-dependent responses or call tracking:

```go
"get_pod_logs": func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
    podName := req.Arguments["pod_name"].(string)
    mu.Lock()
    recordedCalls = append(recordedCalls, podName)
    mu.Unlock()
    return &mcpsdk.CallToolResult{
        Content: []mcpsdk.Content{&mcpsdk.TextContent{
            Text: fmt.Sprintf("Logs for %s: memory limit exceeded", podName),
        }},
    }, nil
},
```

**Typical test pattern**: The `ScriptedLLMClient` first returns a `ToolCallChunk` (NativeThinking) or text with tool call syntax (ReAct). The real `mcp.ToolExecutor` routes the call to the in-memory MCP server, which returns the scripted result. The LLM client's next entry returns the final answer incorporating tool results.

**Tool summarization**: Tests that exercise large tool results return large text from the `ToolHandler`. The `ScriptedLLMClient` handles summarization calls as regular sequential entries — they appear in order between the tool result return and the next agent iteration. At least one test scenario (the comprehensive observability test) explicitly verifies that summarization calls are made and their output is stored correctly.

**Test helper exposure**: The `mcp` package will need to expose a few test-only helpers (via `_test.go` files or an `export_test.go` pattern) to allow injecting pre-wired sessions into the `Client`. This follows the same internal wiring already used in `pkg/mcp/executor_test.go:newTestExecutor`.

### What We Mock vs. What's Real

| Component | Real/Mock | Rationale |
|-----------|-----------|-----------|
| PostgreSQL | **Real** | Core state store; testcontainers |
| HTTP server | **Real** | Echo, real routing, real handlers |
| WebSocket | **Real** | Real upgrade, real NOTIFY delivery |
| EventPublisher | **Real** | DB + pg_notify in same transaction |
| NotifyListener | **Real** | pgx LISTEN on test schema |
| ConnectionManager | **Real** | Channel subscriptions, broadcast |
| WorkerPool | **Real** | Claim, execute, status update |
| SessionExecutor | **Real** | Full chain orchestration |
| ChatMessageExecutor | **Real** | Full chat flow |
| Agent framework | **Real** | Controllers, prompt builder |
| Config system | **Real** | In-memory config (not file-based) |
| LLMClient | **Mock** | No real LLM API calls |
| MCP Client + ToolExecutor | **Real** | Real `mcp.Client` → `mcp.ToolExecutor` pipeline (tool routing, name mangling, masking) |
| MCP Servers | **In-memory** | `mcpsdk.InMemoryTransport` with scripted `ToolHandler` functions (no real server processes) |
| Masking | **Real** | Lightweight, no external deps |

---

## WebSocket Test Client (`test/e2e/ws_client.go`)

A test helper that connects to the WebSocket endpoint and collects events.

```go
// WSClient connects to the TARSy WebSocket endpoint and collects events.
type WSClient struct {
    conn     *websocket.Conn
    events   []WSEvent
    mu       sync.Mutex
    ctx      context.Context
    cancel   context.CancelFunc
    doneCh   chan struct{}
}

type WSEvent struct {
    Type      string                 `json:"type"`
    Raw       json.RawMessage        // Original JSON
    Parsed    map[string]interface{} // Parsed for assertions
    Received  time.Time              // When we received it
}

// Connect establishes a WebSocket connection to the test server.
func Connect(ctx context.Context, wsURL string) (*WSClient, error)

// Subscribe sends a subscribe action for the given channel.
func (c *WSClient) Subscribe(channel string) error

// WaitForEvent waits until an event matching the predicate is received, or timeout.
func (c *WSClient) WaitForEvent(predicate func(WSEvent) bool, timeout time.Duration) (*WSEvent, error)

// WaitForEventType waits for an event with the given type.
func (c *WSClient) WaitForEventType(eventType string, timeout time.Duration) (*WSEvent, error)

// WaitForSessionStatus waits for a session.status event with the given status.
func (c *WSClient) WaitForSessionStatus(status string, timeout time.Duration) (*WSEvent, error)

// CollectUntil collects events until predicate returns true or timeout.
func (c *WSClient) CollectUntil(predicate func(events []WSEvent) bool, timeout time.Duration) ([]WSEvent, error)

// Events returns a snapshot of all collected events.
func (c *WSClient) Events() []WSEvent

// EventsByType returns events filtered by type.
func (c *WSClient) EventsByType(eventType string) []WSEvent

// Close closes the WebSocket connection.
func (c *WSClient) Close() error
```

### Event Collection Pattern

The `WSClient` runs a background goroutine that reads events and appends them to a thread-safe slice. Test code uses `WaitFor*` methods to block until specific events arrive, then asserts on the full collected sequence.

```go
// Example test flow:
ws := e2e.Connect(ctx, app.WSURL)
defer ws.Close()

// Subscribe before submitting alert (so we catch all events)
ws.Subscribe("session:" + sessionID)

// ... submit alert, wait for completion ...

// Collect all events
events := ws.Events()
```

---

## Golden File System (`test/e2e/golden.go`)

### Philosophy

Golden files store the **expected output** as readable text/JSON. Tests compare actual output against golden files. A `-update` flag regenerates goldens from actual output.

This replaces Python's approach of embedding expected data as dicts in `expected_conversations.py`. Benefits:
- Expected data is in plain files, easy to review in PRs
- No code changes needed to update expectations
- Large expected payloads don't clutter test code

### Directory Structure

```
test/e2e/
├── testdata/
│   ├── configs/                    # Test-specific chain/agent configs
│   │   ├── simple_chain.go         # Config builders (not YAML — in-code for type safety)
│   │   └── complex_chain.go
│   ├── golden/
│   │   ├── full_flow/              # One directory per test scenario
│   │   │   ├── ws_events.golden    # WebSocket event sequence (projected)
│   │   │   ├── session.golden      # GET /sessions/:id response
│   │   │   ├── timeline.golden     # Timeline events from DB
│   │   │   └── stages.golden       # Stage records from DB
│   │   ├── single_stage/
│   │   │   └── ...
│   │   ├── parallel_any/
│   │   │   └── ...
│   │   ├── parallel_all/
│   │   │   └── ...
│   │   ├── cancel_session/
│   │   │   └── ...
│   │   ├── comprehensive_observability/   # Extra golden files for this scenario
│   │   │   ├── ws_events.golden
│   │   │   ├── session.golden
│   │   │   ├── timeline.golden
│   │   │   ├── llm_conversations.golden   # Full LLM messages (system, user, assistant)
│   │   │   ├── llm_interactions.golden    # LLM interaction DB records
│   │   │   └── mcp_interactions.golden    # MCP interaction DB records (incl. summarization)
│   │   ├── concurrent_sessions/
│   │   │   └── ...
│   │   ├── react_flow/
│   │   │   └── ...
│   │   └── ...                     # Other scenario directories
│   └── alert_payloads/             # Sample alert data
│       └── kubernetes_oom.txt
├── harness.go                      # TestApp
├── mock_llm.go                     # ScriptedLLMClient (dual dispatch)
├── mcp_helpers.go                   # SetupInMemoryMCP + common ToolHandler builders
├── ws_client.go                    # WSClient
├── golden.go                       # Golden file helpers
├── normalize.go                    # Content normalization
├── helpers.go                      # Shared test utilities, ProjectForGolden
└── scenarios_test.go               # Test cases (or split into multiple files)
```

### Golden File Format

Golden files use **normalized JSON** (one field per line, sorted keys) for structured data:

```json
{
  "executive_summary": "Executive summary: Pod-1 OOM killed.",
  "final_analysis": "Root cause is memory leak in app container.",
  "session_id": "{SESSION_ID}",
  "status": "completed"
}
```

For WebSocket event sequences, a **line-delimited JSON** format (one event per line):

```json
{"type":"session.status","status":"in_progress"}
{"type":"stage.status","stage_name":"data-collection","stage_index":1,"status":"started"}
{"type":"timeline_event.created","event_type":"llm_response","status":"streaming"}
{"type":"stream.chunk"}
{"type":"timeline_event.completed","event_type":"llm_response","status":"completed"}
{"type":"timeline_event.created","event_type":"final_analysis","status":"completed"}
{"type":"stage.status","stage_name":"data-collection","stage_index":1,"status":"completed"}
{"type":"stage.status","stage_name":"diagnosis","stage_index":2,"status":"started"}
...
{"type":"session.status","status":"completed"}
```

Stream chunks are collapsed to a single `{"type":"stream.chunk"}` marker (we don't assert on every delta — just that chunks were sent).

### Normalization (`test/e2e/normalize.go`)

Dynamic data is replaced with stable placeholders before comparison:

```go
// NormalizeForGolden replaces dynamic values with stable placeholders.
func NormalizeForGolden(data []byte) []byte

// Normalizations applied:
// 1. UUIDs → {UUID}
// 2. RFC3339 timestamps → {TIMESTAMP}
// 3. Unix timestamps → {UNIX_TS}
// 4. Session IDs → {SESSION_ID} (from context)
// 5. Stage IDs → {STAGE_ID_1}, {STAGE_ID_2}, ... (ordered by first appearance)
// 6. Execution IDs → {EXEC_ID_1}, ... (ordered by first appearance)
// 7. db_event_id (integers) → {DB_EVENT_ID}
// 8. connection_id → {CONN_ID}
```

**ID Stabilization**: UUIDs that appear multiple times (e.g., a stage_id referenced in both stage.status and timeline_event.created) are replaced with the same placeholder (`{STAGE_ID_1}`) based on their first appearance order. This preserves referential integrity in golden files.

```go
type Normalizer struct {
    sessionID  string
    stageIDs   []string  // ordered by first appearance
    execIDs    []string  // ordered by first appearance
}

func NewNormalizer(sessionID string) *Normalizer
func (n *Normalizer) Normalize(data []byte) []byte
```

### Golden File Helpers

```go
var updateGolden = flag.Bool("update", false, "update golden files")

// AssertGolden compares actual output against a golden file.
// If -update flag is set, writes actual to the golden file instead.
func AssertGolden(t *testing.T, goldenPath string, actual []byte)

// AssertGoldenJSON normalizes and compares JSON output.
func AssertGoldenJSON(t *testing.T, goldenPath string, actual interface{})

// AssertGoldenEvents normalizes and compares a WebSocket event sequence.
func AssertGoldenEvents(t *testing.T, goldenPath string, events []WSEvent, normalizer *Normalizer)
```

---

## Test Scenarios

### Scenario 1: Full Investigation Flow (`TestE2E_FullFlow`)

**The flagship test.** Multi-stage chain with parallel agents, NativeThinking with MCP tool calls, synthesis, executive summary, and follow-up chat. Exercises the broadest possible surface area.

**Chain config:**
```
Chain: "kubernetes-oom"
  Stage 1: "data-collection" (1 agent, native-thinking + MCP tools)
  Stage 2: "parallel-investigation" (2 agents, policy: any, native-thinking + MCP tools)
    → auto-synthesis
  Stage 3: "final-diagnosis" (1 agent, native-thinking)
  → executive summary
  → chat follow-up (2 messages)
```

**LLM script (10+ calls, includes tool call rounds):**
1. Stage 1 agent: `ToolCallChunk("get_pod_logs", ...)` → tool result → `TextChunk: Collected metrics showing OOM.`
2. Stage 2 agent 1 (routed): `ToolCallChunk("get_metrics", ...)` → tool result → `TextChunk: Agent 1 analysis.`
3. Stage 2 agent 2 (routed): `ToolCallChunk("get_events", ...)` → tool result → `TextChunk: Agent 2 analysis.`
4. Synthesis: `Synthesized: Both agents agree on memory leak.`
5. Stage 3 agent: `TextChunk: Root cause is memory leak in app.`
6. Executive summary: `Pod experienced OOM due to memory leak.`
7. Chat message 1 response: `ToolCallChunk("get_pod_status", ...)` → tool result → `TextChunk: The OOM was caused by...`
8. Chat message 2 response: `TextChunk: You can restart with...`

**Test flow:**
1. Create `TestApp` with the above config + LLM script + in-memory MCP servers with scripted tool handlers
2. Connect WebSocket, subscribe to `sessions` channel
3. POST `/api/v1/alerts` with kubernetes OOM alert → get `session_id`
4. Subscribe WebSocket to `session:{session_id}`
5. Collect WS events until `session.status: completed`
6. Assert WS event sequence against `golden/full_flow/ws_events.golden`
7. GET `/api/v1/sessions/:id` → assert response against `golden/full_flow/session.golden`
8. Query timeline events from DB → assert against `golden/full_flow/timeline.golden`
9. **Chat phase:**
   - POST `/api/v1/sessions/:id/chat/messages` with "What caused the OOM?"
   - Collect WS events until `stage.status: completed` (chat stage)
   - POST second chat message: "How do I restart it?"
   - Collect WS events until second chat stage completes
10. Assert full WS event sequence (investigation + chat) against golden
11. GET session again → verify final state with chat stages
12. Verify LLM call count matches expected
13. Verify MCP tool calls were recorded correctly

### Scenario 2: Simple Single-Stage with MCP (`TestE2E_SingleStage`)

Minimal happy path: one stage, one agent, NativeThinking with one tool call, final answer.

**Verifies:**
- Alert submission → session creation
- Worker pool picks up and executes
- Session transitions: pending → in_progress → completed
- Stage events: started → completed
- NativeThinking tool call → tool result → final answer flow
- Timeline event: final_analysis
- Session response includes final_analysis

### Scenario 3: Stage Failure — Fail Fast (`TestE2E_FailFast`)

First stage agent fails (LLM error). Second stage should never start.

**Verifies:**
- Session status → failed
- Only 1 stage created in DB
- Stage events: started → failed (for stage 1 only)
- No stage 2 events

### Scenario 4: Cancellation (`TestE2E_Cancellation`)

Submit alert, wait for first stage to start, then POST cancel.

**Verifies:**
- Session status transitions through cancelling → cancelled
- Agent execution stops (context cancelled)
- Stage gets terminal status

### Scenario 5: Parallel Agents — Policy Any (`TestE2E_ParallelPolicyAny`)

Two agents (routed responses), one fails, policy=any → stage succeeds. Both agents make MCP tool calls.

**Verifies:**
- Stage completes despite one agent failure
- Synthesis runs (with results from successful agent)
- Session completes
- Tool calls recorded for both agents

### Scenario 6: Parallel Agents — Policy All (`TestE2E_ParallelPolicyAll`)

Two agents, one fails, policy=all → stage fails.

**Verifies:**
- Stage fails because not all agents succeeded
- No synthesis (stage failed)
- Session fails (fail-fast)

### Scenario 7: Replica Execution (`TestE2E_Replicas`)

Stage with replicas=3, all succeed, each with tool calls.

**Verifies:**
- 3 agent executions with names `AgentName-1`, `-2`, `-3`
- Synthesis after replicas
- Correct stage/execution records in DB

### Scenario 8: Executive Summary Fail-Open (`TestE2E_ExecutiveSummaryFailOpen`)

Executive summary LLM call fails. Session should still complete.

**Verifies:**
- Session status = completed
- `executive_summary` is empty
- `executive_summary_error` is populated

### Scenario 9: Chat Context Accumulation (`TestE2E_ChatContextAccumulation`)

Two sequential chat messages. Second message's LLM call should receive context from the first. Chat agent makes tool calls.

**Verifies:**
- First chat creates Chat + Stage records
- Second chat's LLM input includes first exchange context
- Timeline shows both user_question + final_analysis pairs

### Scenario 10: Chat Cancellation (`TestE2E_ChatCancellation`)

Submit chat message, then cancel the session. Chat execution should be cancelled.

**Verifies:**
- Chat stage gets cancelled/failed status
- Subsequent chat messages can be submitted after cancellation

### Scenario 11: Comprehensive Observability (`TestE2E_ComprehensiveObservability`)

**The deep verification test.** A stage with two parallel agents (NativeThinking + MCP tool calls, including tool summarization) followed by a chat. This test asserts on all 4 data layers:

**Chain config:**
```
Chain: "observability-test"
  Stage 1: "investigation" (2 parallel agents, native-thinking + MCP tools)
    → auto-synthesis
  → executive summary
  → chat follow-up (1 message with tool call)
```

**Asserts (beyond standard WS events + API golden files):**
1. **LLM conversation messages** — Full golden file comparison of all messages (system, user, assistant) passed to `Generate()` for each call, captured via `ScriptedLLMClient.CapturedInputs()`. Verifies prompt construction, context accumulation, tool results in conversation.
2. **LLM interaction DB records** — Queries `llm_interaction` table: verifies model, token counts, timing, request/response content for each LLM call.
3. **MCP interaction DB records** — Queries `mcp_interaction` table: verifies tool name, arguments, result, timing, associated execution for each tool call.
4. **Tool summarization** — At least one agent's tool result is large enough to trigger summarization. Verifies the summarization LLM call is made, and the summarized content is stored alongside the original in the MCP interaction record.

This test uses golden files for LLM conversations (normalized) in addition to the standard WS event and API golden files.

### Scenario 12: Concurrent Session Execution (`TestE2E_ConcurrentSessions`)

Submit multiple alerts concurrently within a single test. Uses `WithWorkerCount(3)` to enable parallel session processing.

**Verifies:**
- Multiple sessions created and processed in parallel
- Each session completes independently with correct state
- No cross-session data leakage
- Worker pool handles concurrent claim + execute correctly
- WebSocket events for each session delivered to correct subscribers

### Scenario 13: ReAct Strategy (`TestE2E_ReActFlow`)

Single stage with ReAct iteration strategy: tool call via text parsing → observation → final answer.

**Verifies:**
- ReAct text-based tool call parsing works end-to-end
- Tool result is injected as observation
- Final answer extracted correctly
- Contrasts with NativeThinking flow used in most other tests

---

## WebSocket Event Verification Strategy

### Event Filtering and Projection for Golden Files

Not all WS events are useful for golden comparison, and not all fields within an event matter. The golden file includes a **filtered, projected, normalized** sequence.

**Step 1 — Filter**:
1. **Include**: `session.status`, `stage.status`, `timeline_event.created`, `timeline_event.completed`, `chat.created`, `chat.user_message`
2. **Collapse**: Multiple `stream.chunk` events into a single `{"type":"stream.chunk"}` marker
3. **Exclude**: `connection.established`, `subscription.confirmed`, `pong`, `catchup.overflow`

**Step 2 — Project** (`ProjectForGolden`):
Each included event is reduced to **type + key fields only**, dropping noisy payload details. This keeps golden files stable across minor field additions:

```go
// ProjectForGolden extracts only the key fields from a WS event for golden comparison.
func ProjectForGolden(event WSEvent) map[string]interface{} {
    projected := map[string]interface{}{"type": event.Type}
    switch event.Type {
    case "session.status":
        projected["status"] = event.Parsed["status"]
    case "stage.status":
        projected["stage_name"] = event.Parsed["stage_name"]
        projected["stage_index"] = event.Parsed["stage_index"]
        projected["status"] = event.Parsed["status"]
    case "timeline_event.created", "timeline_event.completed":
        projected["event_type"] = event.Parsed["event_type"]
        projected["status"] = event.Parsed["status"]
    case "chat.created":
        projected["chat_id"] = event.Parsed["chat_id"]
    case "chat.user_message":
        projected["content"] = event.Parsed["content"]
    }
    return projected
}
```

**Step 3 — Normalize**: Replace IDs, timestamps with placeholders (see Normalization section).

### Event Assertion Helper

```go
// FilterEventsForGolden filters, collapses, and projects WS events for golden comparison.
func FilterEventsForGolden(events []WSEvent) []map[string]interface{} {
    var filtered []map[string]interface{}
    lastWasChunk := false
    for _, e := range events {
        switch e.Type {
        case "stream.chunk":
            if !lastWasChunk {
                filtered = append(filtered, map[string]interface{}{"type": "stream.chunk"})
                lastWasChunk = true
            }
        case "connection.established", "subscription.confirmed", "pong":
            continue
        default:
            filtered = append(filtered, ProjectForGolden(e))
            lastWasChunk = false
        }
    }
    return filtered
}
```

---

## DB State Verification

In addition to API responses and WS events, tests query the database directly to verify persistent state:

### Timeline Assertions

```go
// QueryTimeline returns all timeline events for a session, ordered by sequence.
func (app *TestApp) QueryTimeline(sessionID string) []*ent.TimelineEvent

// QueryStages returns all stages for a session, ordered by index.
func (app *TestApp) QueryStages(sessionID string) []*ent.Stage

// QueryExecutions returns all agent executions for a session.
func (app *TestApp) QueryExecutions(sessionID string) []*ent.AgentExecution
```

### LLM/MCP Interaction Assertions (for comprehensive observability test)

```go
// QueryLLMInteractions returns all LLM interaction records for a session.
func (app *TestApp) QueryLLMInteractions(sessionID string) []*ent.LLMInteraction

// QueryMCPInteractions returns all MCP interaction records for a session.
func (app *TestApp) QueryMCPInteractions(sessionID string) []*ent.MCPInteraction
```

These are serialized to normalized JSON and compared against golden files, providing a complete 4-layer picture:
- **WS events** → "what the client saw in real-time"
- **API response** → "what GET /sessions/:id returns"
- **DB timeline** → "what's persisted for history"
- **LLM/MCP interactions** → "the debug/observability layer" (verified in comprehensive observability test)

---

## Normalization Details

### UUID Replacement

UUIDs are replaced with indexed placeholders to preserve referential integrity:

```go
// Input:  stage_id: "a1b2c3d4-e5f6-..."  (appears in stage.status and timeline_event)
// Output: stage_id: "{STAGE_ID_1}"        (same placeholder everywhere)
```

The `Normalizer` tracks first-appearance order:
- First unique stage UUID seen → `{STAGE_ID_1}`
- Second unique stage UUID seen → `{STAGE_ID_2}`
- etc.

### Timestamp Replacement

All RFC3339 timestamps → `{TIMESTAMP}`. We don't need to verify exact times — only ordering (which is implicit in event sequence).

### Content Normalization

LLM response content is **not** normalized (it comes from our mock, so it's deterministic). Only infrastructure-generated dynamic values (IDs, timestamps) are replaced.

---

## Test Configuration

### Chain Configs as Go Code

Test chain configurations are built programmatically (not loaded from YAML). This ensures type safety and avoids file path resolution issues:

```go
// test/e2e/testdata/configs/chains.go

func FullFlowConfig() *config.Config {
    maxIter := 2  // allow 1 tool-call round + final answer
    return &config.Config{
        Defaults: &config.Defaults{
            LLMProvider:       "test-provider",
            IterationStrategy: config.IterationStrategyNativeThinking,
            MaxIterations:     &maxIter,
        },
        AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
            "DataCollector":   {
                IterationStrategy: config.IterationStrategyNativeThinking,
                MaxIterations:     &maxIter,
                MCPServers:        []string{"test-mcp"},
            },
            "Investigator":    {
                IterationStrategy: config.IterationStrategyNativeThinking,
                MaxIterations:     &maxIter,
                MCPServers:        []string{"test-mcp"},
            },
            "Diagnostician":   {
                IterationStrategy: config.IterationStrategyNativeThinking,
                MaxIterations:     &maxIter,
            },
            "SynthesisAgent":  {IterationStrategy: config.IterationStrategySynthesis, MaxIterations: &maxIter},
            "ChatAgent":       {
                IterationStrategy: config.IterationStrategyNativeThinking,
                MaxIterations:     &maxIter,
                MCPServers:        []string{"test-mcp"},
            },
        }),
        LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
            "test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model"},
        }),
        ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
            "kubernetes-oom": {
                AlertTypes: []string{"kubernetes-oom"},
                Stages: []config.StageConfig{
                    {Name: "data-collection", Agents: []config.StageAgentConfig{{Name: "DataCollector"}}},
                    {Name: "parallel-investigation", Agents: []config.StageAgentConfig{{Name: "Investigator"}, {Name: "Investigator"}}, SuccessPolicy: config.SuccessPolicyAny},
                    {Name: "final-diagnosis", Agents: []config.StageAgentConfig{{Name: "Diagnostician"}}},
                },
                Chat: &config.ChatConfig{Enabled: true},
            },
        }),
        MCPServerRegistry: config.NewMCPServerRegistry(map[string]*config.MCPServerConfig{
            "test-mcp": {Type: "stdio", Command: "mock"},  // transport overridden by InMemoryTransport
        }),
    }
}
```

### Queue Configuration for Tests

- **WorkerCount**: 1 by default (sufficient — most tests are sequential within a scenario). Use `WithWorkerCount(n)` for concurrent session tests (e.g., Scenario 12).
- **PollInterval**: 100ms (fast pickup, but not too aggressive)
- **SessionTimeout**: 30s (generous for test stability, but fails fast if stuck)
- **HeartbeatInterval**: 5s

---

## Test Execution

### Running Tests

```bash
# Run all e2e tests
go test ./test/e2e/ -v -timeout 120s

# Run a specific scenario
go test ./test/e2e/ -v -run TestE2E_FullFlow -timeout 60s

# Update golden files
go test ./test/e2e/ -v -update

# Run with race detector (slower but catches concurrency bugs)
go test ./test/e2e/ -v -race -timeout 180s
```

### Test Isolation

Each test gets:
- Its own PostgreSQL schema (via `test/util.SetupTestDatabase`)
- Its own `TestApp` instance (own HTTP server port, own worker pool)
- Its own WebSocket connections

No shared state between tests. Tests can run in parallel (different schemas, different ports). However, **start with sequential execution** (`-parallel 1`) until stability is proven, then enable parallelism.

### CI Integration

- Uses `CI_DATABASE_URL` environment variable (external PostgreSQL service in CI)
- Or `testcontainers-go` locally (automatic PostgreSQL container startup)
- Separated by package path (`./test/e2e/` vs `./pkg/...`), no build tags needed
- Makefile targets:
  - `make test-unit` — runs `./pkg/...` (unit + integration tests)
  - `make test-e2e` — runs `./test/e2e/...` (e2e tests)
  - `make test-go` — runs both
  - `make test-coverage` — runs both with coverage report
  - `make test` — runs all tests (Go + Python + dashboard, future-proof)
- Timeout: 120s per test, 300s total

---

## Wait/Poll Strategy

E2e tests deal with async operations. The strategy is **event-driven where possible, polling where necessary**:

### WebSocket Events (Event-Driven)

For session completion: `ws.WaitForSessionStatus("completed", 30*time.Second)`. This blocks until the specific event arrives on the WebSocket — no polling loop needed.

### DB State (Polling with `require.Eventually`)

For cases where we need to verify DB state without a corresponding WS event:

```go
require.Eventually(t, func() bool {
    session, _ := app.EntClient.AlertSession.Get(ctx, sessionID)
    return session.Status == alertsession.StatusCompleted
}, 30*time.Second, 100*time.Millisecond, "session should complete")
```

### Combined Pattern

Most tests follow this pattern:
1. Submit via HTTP
2. Wait for WS event (completion/failure)
3. Assert WS events against golden
4. Assert HTTP response against golden
5. Assert DB state against golden

---

## Implementation Plan

### Phase 6.1: Test Infrastructure (harness, mocks, helpers)

1. **`test/e2e/mock_llm.go`** — `ScriptedLLMClient` with dual dispatch (sequential + agent-routed)
2. **`test/e2e/mcp_helpers.go`** — `SetupInMemoryMCP` + common `ToolHandler` builders (reusing `mcpsdk.InMemoryTransport` pattern from `pkg/mcp/executor_test.go`)
3. **`pkg/mcp/export_test.go`** — Test-only helpers to expose `Client` internals for in-memory wiring
4. **`test/e2e/harness.go`** — `TestApp` with full wiring (real `mcp.ClientFactory` backed by in-memory servers)
4. **`test/e2e/ws_client.go`** — `WSClient` for WebSocket testing
5. **`test/e2e/golden.go`** — Golden file comparison helpers
6. **`test/e2e/normalize.go`** — Content normalization
7. **`test/e2e/helpers.go`** — HTTP client helpers, DB query helpers, wait utilities, `ProjectForGolden`

### Phase 6.2: Core Scenarios

1. **`TestE2E_SingleStage`** — Simplest happy path with NativeThinking + tool call (validates harness works)
2. **`TestE2E_FullFlow`** — The flagship multi-stage + parallel (routed) + MCP tool calls + chat test
3. **`TestE2E_FailFast`** — Stage failure stops the chain
4. **`TestE2E_Cancellation`** — Session cancellation

### Phase 6.3: Parallel, Edge Cases & Observability

5. **`TestE2E_ParallelPolicyAny`** — Parallel agents with policy=any + MCP
6. **`TestE2E_ParallelPolicyAll`** — Parallel agents with policy=all
7. **`TestE2E_Replicas`** — Replica execution with tool calls
8. **`TestE2E_ExecutiveSummaryFailOpen`** — Exec summary failure doesn't fail session
9. **`TestE2E_ChatContextAccumulation`** — Chat context grows across messages + tool calls
10. **`TestE2E_ChatCancellation`** — Chat execution cancellation
11. **`TestE2E_ComprehensiveObservability`** — Full 4-layer assertion (WS events, API, LLM conversations, LLM/MCP interaction DB records, tool summarization)
12. **`TestE2E_ConcurrentSessions`** — Multiple sessions processed in parallel (`WorkerCount > 1`)
13. **`TestE2E_ReActFlow`** — ReAct strategy with text-based tool call parsing

### Phase 6.4: Polish & CI

- Makefile targets: `test-e2e`, `test-go`, `test-coverage`, `test`
- Race detector testing
- CI pipeline integration
- Golden file review and cleanup
- Documentation update

---

## Comparison with Old TARSy E2E

| Aspect | Old TARSy (Python) | New TARSy (Go) |
|--------|--------------------|--------------------|
| **DB isolation** | Temp SQLite per test file, singleton resets, Makefile hack for per-file isolation | Per-test PostgreSQL schema, no singletons, native Go test isolation |
| **Test runner** | 27 separate `pytest` invocations via Makefile | Single `go test` command, parallel-safe |
| **WebSocket testing** | None (HTTP polling only) | Real WebSocket client, event collection + golden comparison |
| **Golden data** | Python dicts in `expected_conversations.py` | `.golden` files in `testdata/golden/` |
| **Normalization** | `E2ETestUtils.normalize_content()` (timestamps, UUIDs, test keys) | `Normalizer` with indexed placeholders (preserves referential integrity) |
| **LLM mock** | Patching LangChain/Gemini SDK classes | `ScriptedLLMClient` implementing `agent.LLMClient` interface |
| **MCP mock** | Patching `MCPClient.list_tools`/`call_tool` | Real `mcp.Client` + `mcp.ToolExecutor` backed by in-memory MCP SDK servers (`mcpsdk.InMemoryTransport`) |
| **Test server** | FastAPI `TestClient` (in-process ASGI) | `net/http` server on random port (real TCP) |
| **Assertions** | `assert_conversation_messages()` for first N messages | Golden file comparison of full event sequences |
| **Long scenarios** | Separate test files for each scenario | Multiple scenarios in one package, each with own `TestApp` |

### Key Improvements

1. **No Makefile hack**: Go's per-test schema isolation eliminates the need for separate process invocations
2. **WebSocket coverage**: First-class WS event testing (old TARSy had none)
3. **Golden files**: Readable `.golden` files instead of Python dict constants
4. **Referential integrity in normalization**: Indexed placeholders (`{STAGE_ID_1}`) instead of generic `{UUID}` — you can verify that the same stage ID appears in both `stage.status` and `timeline_event`
5. **Full event sequence**: Assert entire WS event stream, not just final API response
6. **No singleton issues**: Go's explicit dependency injection vs. Python's global singletons
7. **Parallel test safety**: Each test gets its own schema + port + app instance
8. **Unified LLM mock**: Single `ScriptedLLMClient` with dual dispatch (sequential + agent-routed) — handles both single-agent and parallel-agent stages cleanly
9. **MCP as core with real stack**: Tool calls use the real `mcp.Client` → `mcp.ToolExecutor` pipeline (not a custom mock) backed by in-memory MCP SDK servers — exercises tool routing, name mangling, masking in every e2e test
10. **4-layer observability test**: Dedicated comprehensive test verifies all data layers including LLM conversations and LLM/MCP interaction records
11. **Concurrent session testing**: Validates worker pool under parallel load
