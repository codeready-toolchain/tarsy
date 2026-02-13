# Phase 6: Additional E2E Test Scenarios — Proposal

## Timeline API — Fixed

The `GET /api/v1/sessions/:id/timeline` endpoint has been implemented. It returns timeline
events ordered by sequence number via `TimelineService.GetSessionTimeline()`. The endpoint
is wired into the e2e test harness, and the pipeline test already verifies it comprehensively:

- `GetTimeline()` helper on `TestApp` calls the API
- Cross-references API response against direct DB query (same count, IDs, event types, statuses, content)
- Projects the API response through `ProjectAPITimelineForGolden()` and asserts against the
  same golden file as the DB query — proving API and DB return identical data

All new tests below verify timeline events through this API endpoint.

---

## Current Coverage Analysis

### What `TestE2E_Pipeline` Already Covers

The existing pipeline test is comprehensive. It exercises:

| Feature | How It's Covered |
|---------|-----------------|
| Multi-stage sequential execution | 4 stages + 2 synthesis stages |
| NativeThinking strategy | investigation, MetricsValidator, ScalingReviewer, chat |
| ReAct strategy | remediation, ConfigValidator |
| Parallel agents (policy=all, both succeed) | validation stage |
| Replicas | scaling-review (x2) |
| Synthesis: synthesis-native-thinking | validation synthesis |
| Synthesis: plain synthesis | scaling-review synthesis |
| Forced conclusion | MetricsValidator (max_iterations=1) |
| Executive summary (success) | after all stages |
| Chat with tool calls | 2 chat messages |
| Tool call summarization | get_pods, query_alerts |
| Google Search grounding | GroundingChunk in synthesis |
| 2 MCP servers | test-mcp, prometheus-mcp |
| Golden file assertions (4 layers) | session, stages, timeline, debug list, 26 interaction details |
| WS event structural assertions | AssertEventsInOrder |

### Design Doc Scenarios Already Covered

| Scenario | Covered By |
|----------|-----------|
| 1 — Full Investigation Flow | Pipeline test (multi-stage + parallel + synthesis + chat) |
| 2 — Simple Single-Stage | Pipeline test's investigation stage IS a single-agent stage |
| 7 — Replica Execution | Pipeline test stage 4 (ScalingReviewer x2) |
| 9 — Chat Context Accumulation | Pipeline test (2 chat messages; chat 2 gets context from chat 1, verified in golden files) |
| 11 — Comprehensive Observability | Pipeline test (all 26 interaction detail golden files) |
| 14 — Forced Conclusion | Pipeline test (MetricsValidator max_iterations=1 → forced conclusion) |

### Scenarios NOT Yet Covered (Requested: 3, 4, 5, 6, 8, 10, 12, 13, 15, 16)

| Scenario | What's Unique (not in Pipeline test) |
|----------|-------------------------------------|
| 3 — Stage Failure / Fail Fast | LLM error → stage `failed` → session `failed` → subsequent stages never start |
| 4 — Investigation Cancellation | Cancel API → context propagated → `cancelled` status, in-flight events cleaned up |
| 5 — Parallel Policy Any | One agent fails, stage succeeds (Pipeline test only has policy=all with both succeeding) |
| 6 — Parallel Policy All (with failure) | One agent fails → stage fails (Pipeline test's policy=all has both succeeding) |
| 8 — Executive Summary Fail-Open | Exec summary LLM error → session still `completed`, `executive_summary_error` populated |
| 10 — Chat Cancellation | Cancel during chat → chat stage `cancelled`, investigation state unaffected |
| 12 — Concurrent Sessions | Multiple sessions processed in parallel (WorkerCount > 1), no cross-session leakage |
| 13 — Queue Capacity Limit | `MaxConcurrentSessions` enforcement, overflow sessions queued then processed |
| 15 — Session Timeout | Investigation hits `SessionTimeout` deadline → `timed_out` status |
| 16 — Chat Timeout | Chat hits timeout deadline → chat stage `timed_out`, investigation unaffected |

---

## Proposed Test Organization

### Recommendation: 5 Combined Tests (10 Scenarios → 5 Functions)

Following the pipeline test's approach of packing multiple scenarios into one flow, the 10 scenarios are combined into **5 test functions** based on thematic affinity and flow compatibility.

---

### Test 1: `TestE2E_FailureResilience` — Scenarios 5 + 8

**Concept:** A multi-stage pipeline where things go wrong, but the system recovers gracefully.

**Pipeline design:**
```
Stage 1: "analysis" — parallel agents, policy=any
  Agent A (NativeThinking): returns LLM error
  Agent B (NativeThinking): succeeds with tool call + final answer
  → Synthesis: runs with Agent B's result only
Stage 2: "summary" — single agent, succeeds
Executive summary: LLM returns error → fail-open
```

**What this verifies (unique to this test):**
- **Policy=any resilience**: stage succeeds despite one agent failure (Scenario 5)
- **Synthesis with partial results**: synthesis receives only the successful agent's output
- **Executive summary fail-open**: session `status=completed` with empty `executive_summary` and populated `executive_summary_error` (Scenario 8)
- Error from failed agent is captured in execution record

**What we DON'T re-verify (already in Pipeline test):**
- Detailed interaction-level golden files
- Detailed timeline golden
- Full WS event golden sequence

**Verification approach:**
- DB assertions: session status, stage statuses, execution statuses (one failed, one succeeded)
- Session API: `executive_summary` empty, `executive_summary_error` populated
- **Timeline API** (`GET /sessions/:id/timeline`): failed agent's last timeline event has
  `failed` status with error content; successful agent's timeline events have `completed` status
- WS event order assertions (structural, not golden)
- Stage count, execution count

---

### Test 2: `TestE2E_FailurePropagation` — Scenarios 6 + 3

**Concept:** A multi-stage pipeline where a failure in a parallel stage cascades to stop the entire session.

**Pipeline design:**
```
Stage 1: "preparation" — single agent, succeeds
Stage 2: "parallel-check" — parallel agents, policy=all
  Agent A (NativeThinking): succeeds
  Agent B (NativeThinking): returns LLM error
  → Stage fails (policy=all requires both to succeed)
Stage 3: "final" — single agent (SHOULD NEVER START — fail-fast)
```

**What this verifies (unique to this test):**
- **Policy=all failure**: stage fails when one agent errors (Scenario 6)
- **Fail-fast propagation**: stage 3 is never created in DB (Scenario 3)
- Session `status=failed` with error details
- No synthesis runs after failed stage

**Verification approach:**
- DB assertions: only 2 stages in DB (stage 3 never created)
- Stage statuses: stage 1 `completed`, stage 2 `failed`
- Session status: `failed`, `error_message` populated
- Execution statuses for stage 2: one `completed`, one `failed`
- **Timeline API** (`GET /sessions/:id/timeline`): stage 1 events all `completed`;
  stage 2 failed agent's timeline event has `failed` status with error content;
  no timeline events exist for stage 3
- WS event order: no stage 3 events

---

### Test 3: `TestE2E_Cancellation` — Scenarios 4 + 10

**Concept:** Two sessions on the same TestApp testing cancellation of both investigation and chat.

**Flow:**

**Session 1 — Investigation cancellation with parallel agents (Scenario 4):**
1. Submit alert → session starts a stage with 2 parallel agents
2. Both agents' LLM entries use `BlockUntilCancelled: true` (routed dispatch)
3. Wait for session `in_progress` and stage `started`
4. POST `/sessions/:id/cancel`
5. Context cancellation fans out to both agent goroutines → both channels close
6. Verify: session `cancelled`, stage `cancelled`, **both** agents' in-flight timeline
   events transitioned from `streaming` to `cancelled` (not orphaned)

**Session 2 — Chat cancellation (Scenario 10):**
1. Submit alert → investigation runs to completion (simple single-stage, no blocking)
2. Send chat message → chat LLM blocks with `BlockUntilCancelled: true`
3. Wait for chat stage `started`
4. POST `/sessions/:id/cancel`
5. Verify: chat stage `cancelled`, session overall status remains `completed`
6. Send another chat message → completes normally (verifies subsequent chat works after cancellation)

**What this verifies (unique to this test):**
- Cancel API → context propagation → `cancelled` status (Scenario 4)
- **Context fan-out to parallel agents**: cancellation propagates to all running goroutines,
  not just one — the pipeline test only covers parallel agents that succeed
- In-flight streaming timeline events from **multiple concurrent agents** cleaned up
  (not stuck as `streaming`)
- Chat cancellation doesn't affect completed investigation (Scenario 10)
- Subsequent chat works after a cancelled one
- Two different cancellation scopes in one test

**Verification approach:**
- Session 1: session & stage status assertions (`cancelled`)
- Session 1 **Timeline API**: **no timeline events stuck as `streaming`** — all in-flight
  events transitioned to `cancelled`; this is the critical assertion for this test
- Session 2: session status `completed`, chat stage `cancelled`, follow-up chat `completed`
- Session 2 **Timeline API**: investigation timeline events `completed`; chat timeline events
  from cancelled chat are `cancelled` (not `streaming`); follow-up chat events `completed`
- WS event order for both sessions

---

### Test 4: `TestE2E_Timeout` — Scenarios 15 + 16

**Concept:** Two sessions on the same TestApp testing timeout of both investigation and chat.

**Config:** `WithSessionTimeout(2s)`, `WithChatTimeout(2s)`

**Flow:**

**Session 1 — Session timeout (Scenario 15):**
1. Submit alert → investigation starts
2. LLM blocks with `BlockUntilCancelled: true` (simulates stuck LLM)
3. Session timeout fires after 2s → context cancelled
4. Verify: session `timed_out`, stage `timed_out`, error message mentions timeout

**Session 2 — Chat timeout (Scenario 16):**
1. Submit alert → investigation completes quickly (LLM responds immediately, non-blocking)
2. Send chat message → chat LLM blocks with `BlockUntilCancelled: true`
3. Chat timeout fires after 2s → context cancelled
4. Verify: chat stage `timed_out`, session status remains `completed`
5. Send another chat → completes normally (verifies chat still works after timeout)

**What this verifies (unique to this test):**
- `timed_out` status distinct from `cancelled` (Scenario 15)
- Timeout triggered by deadline, not API call
- In-flight timeline events marked `timed_out`
- Chat timeout doesn't affect completed investigation (Scenario 16)
- Session continues accepting chat after a timed-out one

**Verification approach:**
- Session 1: session & stage status = `timed_out`, error message assertions
- Session 1 **Timeline API**: **no timeline events stuck as `streaming`** — all in-flight
  events transitioned to `timed_out`; mirrors the cancellation test but with `timed_out` status
- Session 2: session `completed`, chat stage `timed_out`, follow-up chat `completed`
- Session 2 **Timeline API**: investigation events `completed`; chat events from timed-out
  chat are `timed_out`; follow-up chat events `completed`

**Note:** Session 2 needs a different chain config or alert type so the investigation's LLM doesn't block (session timeout is 2s). The investigation LLM entries must be fast (non-blocking). The blocking only happens on the chat LLM entry.

---

### Test 5: `TestE2E_Concurrency` — Scenarios 12 + 13

**Concept:** Single TestApp with `WithWorkerCount(3)`, `WithMaxConcurrentSessions(2)`. Verifies both queue capacity limits and concurrent execution correctness.

**Config:** `WithWorkerCount(3)`, `WithMaxConcurrentSessions(2)`

**Flow:**
1. Submit 4 alerts simultaneously → 4 pending sessions
2. Wait until exactly 2 sessions are `in_progress` (the max concurrent limit)
3. Verify: remaining 2 sessions still `pending` (Scenario 13 — queue capacity)
4. The 2 active sessions' LLM entries resolve → sessions complete
5. Wait: the remaining 2 pending sessions get picked up and complete
6. All 4 sessions `completed`

**What this verifies (unique to this test):**
- `MaxConcurrentSessions` enforcement: only 2 of 3 workers claim sessions (Scenario 13)
- Overflow sessions are queued, not dropped
- Concurrent execution: multiple sessions run in parallel without interference (Scenario 12)
- No cross-session data leakage: each session has its own correct stages/executions
- Worker pool correctly drains pending queue after sessions complete

**Verification approach:**
- Polling: wait for exactly 2 `in_progress` + 2 `pending` (or similar assertion)
- After completion: verify each session has correct stage count and agent names
- Verify total execution count across all sessions matches expected
- **Timeline API** per session: all events `completed`, correct event count per session
  (no cross-session leakage — session A's timeline doesn't contain session B's events)
- No golden files — API + DB state assertions only

**Pipeline per session:** Simple single-stage, single-agent to minimize LLM script complexity. Each session just needs 1 LLM entry (final answer). Use routed dispatch or sequential entries carefully ordered.

---

## Verification Depth Summary

| Test | Golden Files | Session/Stages/Execs | Timeline API | WS Events | Debug API |
|------|-------------|----------------------|-------------|-----------|-----------|
| Pipeline (existing) | Full (session, stages, timeline, debug list, 26 interactions) | Stages, execs golden | API + DB cross-referenced, same golden | Full structural | Full detail |
| FailureResilience | None | Status, count, error fields | API: failed events `failed` + error; success events `completed` | Structural order | No |
| FailurePropagation | None | Status, count (stage 3 absent) | API: failed events `failed`; no stage 3 events | Structural order | No |
| Cancellation | None | Status: `cancelled` | API: **no `streaming` orphans** — all → `cancelled` | Structural order | No |
| Timeout | None | Status: `timed_out` + error msg | API: **no `streaming` orphans** — all → `timed_out` | Structural order | No |
| Concurrency | None | All 4 sessions: status, stages, execs | API: all `completed`, no cross-session leakage | No (non-deterministic) | No |

**Why Timeline API, not just DB?** The dashboard consumes the Timeline API — if the API is broken or returns wrong data, users see nothing regardless of what's in the DB. Verifying through the API tests the full path: DB → service → HTTP handler → JSON response. DB-level checks are redundant when the API is exercised.

Timeline event status checks are a critical verification for the cancellation and timeout tests — they catch the real bug class where in-flight `streaming` events get orphaned when execution is interrupted. For failure tests, they confirm errors propagate to the observable timeline layer, not just internal execution records.

This keeps the golden file maintenance burden concentrated in the pipeline test while the new tests focus on verifying specific behaviors through targeted assertions.

**Note on Pipeline test:** The pipeline test already verifies the Timeline API — it cross-references
the API response against the DB query and asserts both produce identical golden output.

---

## Infrastructure Additions Required

Before implementing the test scenarios, these harness additions are needed:

### 1. New `TestApp` Construction Options

```go
func WithWorkerCount(n int) TestAppOption          // For concurrency tests
func WithMaxConcurrentSessions(n int) TestAppOption // For queue capacity tests
func WithSessionTimeout(d time.Duration) TestAppOption  // For session timeout tests
func WithChatTimeout(d time.Duration) TestAppOption     // For chat timeout tests
```

These fields already exist in `testAppConfig` — just need public `With*` functions.

### 2. New TestApp Helper Methods

`GetTimeline()` already exists. The following are still needed:

```go
// CancelSession sends POST /api/v1/sessions/:id/cancel
func (app *TestApp) CancelSession(t *testing.T, sessionID string) map[string]interface{}

// QuerySessionsByStatus returns sessions matching the given status (for concurrency tests)
func (app *TestApp) QuerySessionsByStatus(t *testing.T, status string) []string

// WaitForNSessionsInStatus waits until exactly N sessions have the given status
func (app *TestApp) WaitForNSessionsInStatus(t *testing.T, n int, status string)
```

### 4. New Config Files

Each combined test needs its own YAML config (or could build configs in-code). Recommendation: YAML files in `test/e2e/testdata/configs/` like the pipeline test.

---

## Implementation Order

1. **Infrastructure additions** — `With*` options, `CancelSession` helper, concurrency helpers
3. **`TestE2E_FailurePropagation`** (3 + 6) — Simplest new test, exercises error + LLM Error entry
4. **`TestE2E_FailureResilience`** (5 + 8) — Exercises policy=any + exec summary failure
5. **`TestE2E_Cancellation`** (4 + 10) — Exercises `BlockUntilCancelled` + cancel API
6. **`TestE2E_Timeout`** (15 + 16) — Similar to cancellation but deadline-triggered
7. **`TestE2E_Concurrency`** (12 + 13) — Most complex, needs multi-session coordination

