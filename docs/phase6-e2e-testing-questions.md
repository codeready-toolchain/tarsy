# Phase 6: End-to-End Testing — Open Questions

Questions where the design significantly departs from old TARSy or involves non-obvious trade-offs.

---

## Q1: ~~Test Package Location~~ — RESOLVED

**Decision**: `test/e2e/`. Consistent with existing `test/util/` and `test/database/` structure. Clearly separated from production code, Go convention for integration/e2e tests.

---

## Q2: ~~Build Tag Separation~~ — RESOLVED

**Decision**: Package path separation (no build tags, no `Short()`). Go supports listing multiple package paths in one command (`go test ./pkg/... ./test/e2e/`), so combining them is trivial. Makefile targets:

| Target | Scope | Use Case |
|--------|-------|----------|
| `test-unit` | Go `./pkg/...` (unit + integration) | Fast dev feedback |
| `test-e2e` | Go `./test/e2e/` | Run just e2e tests |
| `test-go` | Go all (`./pkg/... ./test/e2e/`) | All Go tests |
| `test-coverage` | Go all + `-coverprofile` | CI with coverage report |
| `test` | `test-go` + Python + Dashboard (future) | Full repo validation |

`make test` is the umbrella "test everything in the repo" target. Developers who want fast feedback use `make test-unit` explicitly. Note: `test-unit` includes integration tests from `./pkg/...` (testcontainers-backed) — the Makefile target should have a comment clarifying this.

---

## Q3: ~~Golden File Format~~ — RESOLVED

**Decision**: JSON golden files (`.golden` containing formatted JSON). Line-delimited JSON for WS event sequences (one event per line). `-update` flag to regenerate goldens from actual output. Standard Go `encoding/json` for comparison, diff-friendly when pretty-printed.

---

## Q4: ~~WebSocket Event Assertion Granularity~~ — RESOLVED

**Decision**: Type + key fields only. Golden files contain a projected subset of each event — `type` plus significant fields, ignoring infrastructure fields (`timestamp`, `db_event_id`, `connection_id`). A `ProjectForGolden(event)` function extracts the meaningful subset per event type:
- `session.status` → `{type, status}`
- `stage.status` → `{type, stage_name, stage_index, status}`
- `timeline_event.created` → `{type, event_type, status}`
- `timeline_event.completed` → `{type, event_type, status, content}` (content for final_analysis/user_question)
- `stream.chunk` → collapsed to single `{type: "stream.chunk"}`

---

## Q5: ~~Parallel Agent LLM Call Non-Determinism~~ — RESOLVED

**Decision**: Unified mock with dual dispatch — sequential fallback + agent-aware routing in a single `ScriptedLLMClient`. `AddSequential()` for single-agent stages, exec summary, chat, etc. `AddRouted(agentName, ...)` for parallel stages where agents need differentiated responses. Resolution: if agent name matches a route, use that; otherwise consume the next sequential entry. One mock type, zero overhead for simple tests, full differentiation for parallel-focused tests. Parallel-specific tests should use routed entries to produce meaningful synthesis verification.

---

## Q6: ~~What to Assert in Golden Files~~ — RESOLVED

**Decision**: Events + API golden files for most tests. One dedicated comprehensive observability test (parallel agents + tool calls + chat) that additionally asserts on all 4 data layers via golden files:
1. WS events, API responses, DB timeline/stages (same as all tests)
2. Full LLM conversation messages — role + normalized content per `Generate()` call, golden file comparison (like old TARSy's `assert_conversation_messages`)
3. LLMInteraction / MCPInteraction DB records — counts, types, tokens, conversation indices

This comprehensive test intentionally breaks on prompt changes — concentrated in one place for easy review and update. Other tests use selective `Contains` assertions on captured LLM inputs for critical behaviors (stage context passing, chat context accumulation) when needed.

---

## Q7: ~~`TestApp` Lifecycle~~ — RESOLVED

**Decision**: Per-test `TestApp`. Each `Test_*` function creates its own instance — maximum isolation, no state leakage. Overhead is minimal (shared testcontainer, only per-test schema creation at ~10ms). Additionally, include tests for concurrent session execution: submit multiple alerts within the same test using `WorkerCount > 1`, verifying the worker pool, DB claim isolation (`FOR UPDATE SKIP LOCKED`), and WS event routing under concurrent load.

---

## Q8: ~~Stream Chunk Verification~~ — RESOLVED

**Decision**: Collapse to marker. Golden files show a single `{"type":"stream.chunk"}` marker per streaming sequence. This proves the full streaming pipeline works end-to-end (LLM → controller → publisher → pg_notify → NotifyListener → WS client). Content correctness is already verified by `timeline_event.completed` assertions. No dedicated streaming test needed — the combination of "chunks arrived" + "final content is correct" provides sufficient coverage without brittleness.

---

## Q9: ~~Exposing `api.Server` for Testing~~ — RESOLVED

**Decision**: `httptest.NewServer` wrapping Echo (standard Go pattern, no production code changes). Echo v5's `Echo` type implements `http.Handler`, so `httptest.NewServer(e)` should work directly. If Echo v5 doesn't work cleanly with `httptest`, fall back to adding a `StartForTest()` method on `api.Server`.

---

## Q10: ~~NotifyListener Connection for Tests~~ — RESOLVED

**Decision**: Base connection without search_path. NOTIFY/LISTEN is database-level in PostgreSQL — not schema-scoped. Channel names include session UUIDs (e.g., `session:abc-123`) which are unique per test, so events are naturally isolated. The NotifyListener only does `LISTEN` + `WaitForNotification` (never queries tables), so it doesn't need `search_path`. Use `test/util.GetBaseConnectionString()` directly.

---

## Q11: ~~Test Timeout Strategy~~ — RESOLVED

**Decision**: Layered timeouts. Test-level (`go test -timeout 60s`) as safety net, session timeout (30s) to prevent orchestrator hangs, wait helpers (15s default, customizable) for fast failure diagnosis, worker poll interval (100ms) for fast pickup. If a wait times out, the error message tells you exactly which event/state was expected.

---

## Q12: ~~MCP Tool Call Testing in E2E~~ — RESOLVED

**Decision**: MCP tool calls are the primary production path (NativeThinking + Gemini structured tool calls). `MockMCPFactory` + `ScriptedToolExecutor` are core test infrastructure, not optional add-ons. Most tests include tool calls — the standard agent flow is: LLM returns tool call(s) → execute tool via `ScriptedToolExecutor` → LLM receives results → returns final answer (at least 2 LLM calls per agent). Both NativeThinking (structured `ToolCallChunk`) and ReAct (text-parsed tool calls) paths are exercised across tests. Only the simplest edge-case tests (fail-fast, cancellation) may skip tool calls for brevity.

---

## Decision Summary

| Question | Recommendation | Key Reason |
|----------|---------------|------------|
| Q1: Package location | `test/e2e/` **✓** | Matches existing structure |
| Q2: Build tag | Path separation (no tag) **✓** | Simplest, Makefile convention |
| Q3: Golden format | JSON files **✓** | Standard, diff-friendly |
| Q4: WS event granularity | Type + key fields **✓** | Strong yet stable |
| Q5: Parallel LLM ordering | Unified mock (sequential + routed) **✓** | Deterministic, differentiated when needed |
| Q6: What to assert | Events + API; one comprehensive test with full LLM/MCP assertions **✓** | Full 4-layer coverage, maintainable |
| Q7: TestApp lifecycle | Per-test + concurrent session tests **✓** | Maximum isolation |
| Q8: Stream chunks | Collapse to marker **✓** | Confirms pipeline without brittleness |
| Q9: Server for tests | `httptest.Server` (fallback: `StartForTest()`) **✓** | Standard Go, no prod changes |
| Q10: NotifyListener conn | Base connection (no search_path) **✓** | NOTIFY is DB-level |
| Q11: Timeouts | Layered (test + session + wait) **✓** | Fast failure diagnosis |
| Q12: MCP testing | Core infrastructure, most tests include tool calls **✓** | Primary production path |
