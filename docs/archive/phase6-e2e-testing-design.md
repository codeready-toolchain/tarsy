# Phase 6: End-to-End Testing — Summary

## Goal

Build a comprehensive e2e test suite exercising the full TARSy pipeline — from HTTP API through chain execution to WebSocket event delivery — with mocked LLM and in-memory MCP servers. Real PostgreSQL, real event streaming (NOTIFY/LISTEN), real WebSocket connections.

---

## Architecture

### In-Process Test Server

Each e2e test boots a full TARSy application **in-process** via `TestApp` (`test/e2e/harness.go`). Same wiring as `cmd/tarsy/main.go` but with mock LLM and in-memory MCP servers on a random-port HTTP server. PostgreSQL via testcontainers with per-test schema isolation.

### What's Real vs. Mocked

| Component | Real/Mock | Rationale |
|-----------|-----------|-----------|
| PostgreSQL | **Real** (testcontainers) | Core state store |
| HTTP server, WebSocket | **Real** | Real routing, real upgrade, real NOTIFY delivery |
| EventPublisher, NotifyListener, ConnectionManager | **Real** | Full event pipeline |
| WorkerPool, SessionExecutor, ChatMessageExecutor | **Real** | Full orchestration |
| Agent framework (controllers, prompt builder) | **Real** | Full iteration loop |
| Config system | **Real** (YAML, loaded via `config.Initialize`) | Agent resolution, merge, validation exercised |
| MCP Client + ToolExecutor | **Real** | Tool routing, name mangling, masking exercised |
| MCP Servers | **In-memory** (`mcpsdk.InMemoryTransport`) | Scripted `ToolHandler` functions |
| LLMClient | **Mock** (`ScriptedLLMClient`) | No real LLM API calls |

### Key Design Decisions

1. **In-process over subprocess**: Fast, debuggable, trivial mock injection via Go interfaces
2. **Real MCP stack**: Uses `mcp.Client` → `mcp.ToolExecutor` backed by in-memory SDK servers instead of a custom `ToolExecutor` mock — exercises the full tool pipeline
3. **Dual-dispatch LLM mock**: Sequential fallback for single-agent + agent-routed dispatch for parallel stages (non-deterministic call order)
4. **YAML configs per scenario**: Loaded via production `config.Initialize()` for realistic behavior
5. **Golden files for deep verification**: Pipeline test verifies all 4 data layers; other tests use targeted assertions
6. **Event-driven waits + DB polling**: WebSocket events for real-time flow; DB polling for reliable state assertions

---

## Test Infrastructure

### TestApp (`test/e2e/harness.go`)

Functional options: `WithConfig`, `WithLLMClient`, `WithMCPServers`, `WithWorkerCount`, `WithMaxConcurrentSessions`, `WithSessionTimeout`, `WithChatTimeout`, `WithDBClient` (shared DB for multi-replica), `WithPodID`.

### ScriptedLLMClient (`test/e2e/mock_llm.go`)

- `AddSequential(entry)` — consumed in order (single-agent, synthesis, exec summary, chat, summarization)
- `AddRouted(agentName, entry)` — matched from system prompt agent name (parallel stages)
- Supports: `Text` (shorthand), `Chunks` (NativeThinking tool calls), `Error`, `BlockUntilCancelled`, `WaitCh` + `OnBlock` (concurrency coordination)
- `CapturedInputs()` for assertion on LLM inputs

### In-Memory MCP (`test/e2e/mcp_helpers.go`)

`SetupInMemoryMCP()` creates in-memory MCP servers with scripted `ToolHandler` functions and returns a real `mcp.ClientFactory`. Backed by `pkg/mcp/testing.go`: `Client.InjectSession()` and `NewTestClientFactory()`.

### Golden Files (`test/e2e/golden.go`, `test/e2e/normalize.go`)

Golden files in `test/e2e/testdata/golden/{scenario}/`. `-update` flag regenerates. `Normalizer` replaces UUIDs, timestamps, durations with indexed placeholders (`{STAGE_ID_1}`, `{EXEC_ID_1}`) preserving referential integrity. Human-readable formats for LLM and MCP interaction details.

### WebSocket Client (`test/e2e/ws_client.go`)

`WSClient` with background read loop. `AssertEventsInOrder` for structural WS event verification with support for unordered groups (parallel agents). Events deduplicated by `db_event_id`.

### Helpers (`test/e2e/helpers.go`)

HTTP: `SubmitAlert`, `GetSession`, `GetTimeline`, `GetDebugList`, `GetLLMInteractionDetail`, `GetMCPInteractionDetail`, `SendChatMessage`, `CancelSession`. Polling: `WaitForSessionStatus`, `WaitForStageStatus`, `WaitForNSessionsInStatus`. DB: `QueryTimeline`, `QueryStages`, `QueryExecutions`. Golden projections: `ProjectStageForGolden`, `ProjectTimelineForGolden`, `BuildAgentNameIndex`.

---

## Test Scenarios

| Test | Config | Scenarios |
|------|--------|-----------|
| `TestE2E_Pipeline` | pipeline | Full pipeline: 4 stages, 2 synthesis (native-thinking + plain), NativeThinking + ReAct strategies, 2 MCP servers, tool summarization, forced conclusion, replicas, executive summary, 2 chat messages with tool calls. Golden file verification of session, stages, timeline, debug list, 31 individual interaction details |
| `TestE2E_FailureResilience` | failure-resilience | Policy=any (stage succeeds despite agent failure), executive summary fail-open, synthesis with partial results |
| `TestE2E_FailurePropagation` | failure-propagation | Policy=all (stage fails when any agent fails), fail-fast (subsequent stages never created) |
| `TestE2E_Cancellation` | cancellation | Investigation cancellation (parallel agents), chat cancellation on completed session, follow-up chat works after cancellation |
| `TestE2E_Timeout` | timeout | Session timeout (2s deadline), chat timeout (2s deadline), follow-up chat works after timeout |
| `TestE2E_Concurrency` | concurrency | MaxConcurrentSessions enforcement (capacity=2, submit 4), concurrent execution, no cross-session data leakage |
| `TestE2E_MultiReplica` | multi-replica | Cross-replica WebSocket delivery via PostgreSQL NOTIFY/LISTEN, shared DB with 2 TestApp instances (worker replica + API-only replica) |

---

## Bug Fixes Discovered During E2E Testing

1. **Cancel handler**: Now succeeds if either session or chat cancellation works (previously failed when session already completed)
2. **Post-cancellation DB updates**: Use `context.Background()` for status updates and event publishing after cancellation/timeout (previously used cancelled context → failed writes)
3. **Agent execution status mapping**: Override agent's reported status based on `ctx.Err()`: `DeadlineExceeded` → `timed_out`, cancellation → `cancelled` (previously could report wrong terminal status)
4. **Chat executor status mapping**: Same fix as above for chat executions
5. **API startup validation**: Added `ValidateWiring()` to catch missing service dependencies at startup (previously caused cryptic 503s at request time)

## New APIs Added During Phase 6

- `GET /api/v1/sessions/:id/timeline` — timeline events ordered by sequence
- `GET /api/v1/sessions/:id/debug` — interaction list grouped by stage → execution
- `GET /api/v1/sessions/:id/debug/llm/:interaction_id` — full LLM interaction with conversation
- `GET /api/v1/sessions/:id/debug/mcp/:interaction_id` — full MCP interaction details

## Other Changes During Phase 6

- **Auto-catchup on WebSocket subscribe**: New subscribers receive prior events immediately
- **`event_type` in `timeline_event.completed`**: Added for observability
- **`AgentExecution.llm_provider`** field: Stores resolved provider name
- **`LLMInteraction`** schema: `stage_id`/`execution_id` made optional (session-level interactions), new types `synthesis`/`forced_conclusion`, new index on `(session_id, created_at)`
- **`aggregateChainMCPServers`**: Now resolves agent definitions from registry (fixes chat MCP inheritance)
- **`pkg/mcp/testing.go`**: `InjectSession()`, `NewTestClientFactory()` for test support
- **`test/database/`**: `SharedTestDB`, `NewTestClient` for test DB management
- **Makefile targets**: `test-unit`, `test-e2e`, `test-go`, `test-go-coverage`
