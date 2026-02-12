# E2E Tests

End-to-end tests for TARSy. These tests boot a complete in-process TARSy instance with:

- **Real PostgreSQL** (via `testcontainers-go`)
- **Real event streaming** (PostgreSQL NOTIFY/LISTEN + WebSocket)
- **Real queue/worker execution**
- **Mocked LLM** (`ScriptedLLMClient` with dual dispatch)
- **In-memory MCP servers** (real MCP SDK with in-memory transports)

## Running

```bash
# Run all e2e tests
make test-e2e

# Run a specific test
go test -v -run TestE2E_SingleStage -timeout 60s ./test/e2e/

# Update golden files after intentional changes
go test -v -run TestE2E_SingleStage -timeout 60s -update ./test/e2e/
```

## Prerequisites

- Docker (for PostgreSQL testcontainer)
- Go 1.23+

## Architecture

### Test Harness (`harness.go`)

`NewTestApp(t, opts...)` creates a full TARSy instance with:
- Per-test PostgreSQL schema (isolated)
- Random-port HTTP server
- Configurable worker count, timeouts, MCP servers, LLM scripts

### ScriptedLLMClient (`mock_llm.go`)

Dual-dispatch mock implementing `agent.LLMClient`:
- **Sequential**: entries consumed in order (single-agent stages, synthesis, exec summary)
- **Routed**: entries matched by agent name from system prompt's `CustomInstructions`
- `CapturedInputs()` for observability assertions
- `BlockUntilCancelled` for cancellation/timeout tests

### In-Memory MCP (`mcp_helpers.go`)

`SetupInMemoryMCP(t, servers)` creates real MCP SDK servers with scripted tool handlers,
returning a `*mcp.ClientFactory` that the real executor pipeline uses.

### Golden Files (`golden.go`, `normalize.go`)

- `AssertGoldenJSON` compares normalized JSON against golden files
- `-update` flag regenerates golden files
- `Normalizer` replaces UUIDs, timestamps, and IDs with stable placeholders

### Chain Configs (`testdata/configs/chains.go`)

Programmatic, type-safe chain configurations. One function per scenario.

## Adding a New Test

1. Add a chain config function in `testdata/configs/chains.go` if needed
2. Create a test function in `scenarios_test.go`
3. Script LLM responses (remember: investigation + executive summary)
4. Use `app.WaitForSessionStatus()` for reliable terminal status polling
5. Assert via API (`app.GetSession`), DB (`app.QueryStages`), and optionally golden files
6. Run with `-update` to generate golden files, then verify without `-update`

## Test Scenarios

| Test | What it verifies |
|------|-----------------|
| `SingleStage` | Happy path: 1 stage, 1 agent, tool call, golden files |
| `FailFast` | Stage failure stops chain, stage 2 never starts |
| `Cancellation` | Session cancel via API during LLM execution |
| `ParallelPolicyAny` | Parallel agents, one fails, stage succeeds |
| `ParallelPolicyAll` | Parallel agents, one fails, stage/session fails |
| `Replicas` | 3 replicas with tool calls + synthesis |
| `ExecutiveSummaryFailOpen` | Exec summary LLM error, session still completes |
| `ChatContextAccumulation` | Chat with context growth across messages |
| `ChatCancellation` | Chat cancellation while LLM blocked |
| `ConcurrentSessions` | Multiple sessions with multiple workers |
| `ForcedConclusion` | MaxIterations hit, forced conclusion without tools |
| `SessionTimeout` | Short timeout with blocked LLM |
| `ChatTimeout` | Chat timeout with blocked LLM |
