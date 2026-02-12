# Phase 6 E2E Tests — Gap Analysis

This document compares the current state of the e2e tests against the design in `docs/phase6-e2e-testing-design.md`, identifying all gaps, problems, and areas of weakness.

---

## Executive Summary

The test **infrastructure** (harness, mock LLM, WS client, MCP helpers, golden file system, normalizer) is largely in place and appears functional. The **test scenarios** all exist (16 out of 16), but they are **hollow** — most perform only basic status checks and barely use the infrastructure that was built. The core design goals (golden file comparisons for WS events, timeline, stages, LLM conversations, MCP interactions) are almost entirely unimplemented.

**Bottom line**: We have the skeleton but not the muscle. The infrastructure works, but tests assert almost nothing meaningful.

---

## 1. Golden Files — Severely Underutilized

### What the design calls for

Each scenario should have a golden directory with multiple files:
- `ws_events.golden` — WebSocket event sequence (filtered, projected, normalized)
- `session.golden` — GET /sessions/:id API response
- `timeline.golden` — Timeline events from DB
- `stages.golden` — Stage records from DB
- (For observability test): `llm_conversations.golden`, `llm_interactions.golden`, `mcp_interactions.golden`

### Current state

Only **5** golden directories exist, each containing **only** `session.golden`:

| Scenario | session | ws_events | timeline | stages | llm_conv | llm_int | mcp_int |
|----------|---------|-----------|----------|--------|----------|---------|---------|
| single_stage | YES | no | no | no | — | — | — |
| exec_summary_fail_open | YES | no | no | no | — | — | — |
| forced_conclusion | YES | no | no | no | — | — | — |
| full_flow | YES | no | no | no | — | — | — |
| observability | YES | no | no | no | no | no | no |
| fail_fast | no | no | no | no | — | — | — |
| parallel_any | no | no | no | no | — | — | — |
| parallel_all | no | no | no | no | — | — | — |
| replicas | no | no | no | no | — | — | — |
| cancellation | no | no | no | no | — | — | — |
| chat_context | no | no | no | no | — | — | — |
| chat_cancellation | no | no | no | no | — | — | — |
| concurrent_sessions | no | no | no | no | — | — | — |
| session_timeout | no | no | no | no | — | — | — |
| chat_timeout | no | no | no | no | — | — | — |
| queue_capacity | no | no | no | no | — | — | — |

Per git status, `ws_events.golden` files that previously existed were **deleted** (for fail_fast, parallel_all, parallel_any, replicas, single_stage).

### Gap

- **No ws_events.golden files exist anywhere** — the primary design goal (verifying WS event sequences via golden comparison) is completely unimplemented
- **11 scenarios have zero golden files**
- **No timeline/stages/interaction golden files exist** for any scenario
- The observability test has none of the extra golden files the design specifies

---

## 2. WebSocket Event Verification — Not Implemented

### What the design calls for

The `FilterEventsForGolden` + `AssertGoldenEvents` pipeline to:
1. Filter WS events (include session.status, stage.status, timeline_event.*, etc.)
2. Collapse stream.chunk events to a single marker
3. Project to key fields only (via `ProjectForGolden`)
4. Normalize IDs/timestamps
5. Compare against `ws_events.golden`

### Current state

- `FilterEventsForGolden` and `ProjectForGolden` **exist in helpers.go** but are **never called** from any test
- `AssertGoldenEvents` **exists in golden.go** but is **never called** from any test
- Tests connect to WebSocket and use `WaitFor*` methods purely for **synchronization** (waiting for completion), not for **assertion**
- The collected events are never compared against anything

### Gap

The entire WS event verification layer is dead code. This was the design's main advantage over old TARSy ("first-class WS event testing"). It's built but unused.

---

## 3. Assertion Depth — Shallow Across the Board

### What the design calls for

Rich, multi-layer assertions per test:
1. WS events → golden comparison
2. API response → golden comparison
3. DB state (timeline, stages) → golden comparison
4. (For observability) LLM conversations, LLM/MCP interactions → golden comparison

### Current state

Most tests follow this pattern:
```go
// Wait for completion
app.WaitForSessionStatus(t, sessionID, "completed")

// Check status
session := app.GetSession(t, sessionID)
assert.Equal(t, "completed", session["status"])

// Maybe check a few fields
assert.NotEmpty(t, session["final_analysis"])

// Maybe count stages/executions
stages := app.QueryStages(t, sessionID)
assert.Len(t, stages, 1)
```

This is essentially just smoke-testing: "did it finish without crashing?"

### Specific weak assertions

| Test | Assertion quality | Problem |
|------|------------------|---------|
| SingleStage | `assert.NotEmpty(session["final_analysis"])` | Doesn't verify content matches LLM mock |
| FailFast | `assert.Equal("failed", ...)`, `assert.Len(stages, 1)` | No golden, no WS events, no error message check |
| Cancellation | `status == "cancelled" \|\| status == "failed"` | Accepts either — masks real issues |
| ParallelAny | `assert.GreaterOrEqual(len(stages), 2)` | Very loose — doesn't verify synthesis happened |
| ParallelAll | `assert.Equal("failed", ...)` | No check on error message, no synthesis verification |
| Replicas | `assert.GreaterOrEqual(len(execs), 3)` | No check on replica naming (AgentName-1, -2, -3) |
| ExecSummaryFailOpen | Uses golden for session — best of the bunch | But no WS/timeline/stages golden |
| ChatContext | Counts `userQuestions == 2` | Doesn't verify 2nd chat's LLM input includes 1st exchange context |
| ChatCancellation | `status == "failed" \|\| status == "cancelled"` | Accepts either |
| Observability | Field-level checks only | No golden files for LLM conversations or DB interactions |
| ConcurrentSessions | `assert.Equal("completed", ...)` per session | No cross-session leakage check |
| QueueCapacity | Conditional health check that silently passes | `if wp, ok := ...; ok { if active, ok := ...; ok { assert } }` — outer layers may fail silently |
| ForcedConclusion | Checks `inputs[2].Tools == nil` | Good assertion! But no timeline golden |
| SessionTimeout | `status == "timed_out" \|\| status == "failed"` | Accepts either |
| ChatTimeout | `stage.status == "timed_out" \|\| status == "failed"` | Accepts either |

### The "either/or" pattern

Four tests accept multiple status values: Cancellation, SessionTimeout, ChatCancellation, ChatTimeout. This strongly suggests the underlying behavior is non-deterministic or broken. Proper e2e tests should assert a single expected outcome.

---

## 4. Comprehensive Observability Test — Drastically Reduced

### What the design calls for (Scenario 11)

The "deep verification test" with:
- 2 parallel agents (NativeThinking + ReAct) with different LLM providers
- Full golden comparison of all LLM conversation messages (system, user, assistant) per call
- Golden comparison of LLM interaction DB records (model name, tokens, timing, provider)
- Golden comparison of MCP interaction DB records (tool name, args, result, timing)
- Tool summarization verification (large tool result → summarization call)
- Auto-synthesis verification

### Current state

Split into two tests (not in the design):
- `TestE2E_ComprehensiveObservability` — Uses a **single agent** (not parallel), does field-level assertions only
- `TestE2E_ComprehensiveObservabilityParallel` — Uses parallel agents, but still only field-level assertions

Neither test does:
- Golden comparison of LLM conversations
- Golden comparison of LLM/MCP interaction records
- Tool summarization verification
- Full conversation message verification (system prompts, tool results in messages, etc.)

### Gap

The observability test is the most reduced from its design. It's meant to be the anchor test that proves the full data pipeline, but currently it's just a slightly more detailed version of SingleStage.

---

## 5. Full Flow Test — Missing Key Verifications

### What the design calls for (Scenario 1)

The "flagship test" with:
- 3 stages: data-collection → parallel-investigation (3 agents, any policy) → final-diagnosis
- 12+ LLM calls including tool call rounds + summarization
- WS event golden comparison
- Timeline golden comparison
- Chat phase (2 messages)
- Verification of LLM call count, MCP tool call recording

### Current state

The test exists and has an elaborate LLM script (good), but:
- Uses `assert.GreaterOrEqual` for stage/execution/LLM counts (very loose)
- No WS event golden comparison
- No timeline golden comparison
- Chat waiting uses fragile `require.Eventually` with stage name == "Chat Response" (brittle)
- Only golden file is `session.golden`

### Config note

The full-flow config references `ChatAgent` which is not defined in the agents section of `tarsy.yaml`. This likely works because it's a built-in agent, but the design called for explicit definition.

---

## 6. Missing/Incomplete Test Aspects

### a) No stream.chunk collapse marker

Design calls for collapsing multiple `stream.chunk` events into a single `{"type":"stream.chunk"}` marker. Current implementation just excludes them entirely with a comment: "stream.chunk events are excluded entirely because their presence is timing-sensitive." While pragmatic, this means we never verify that streaming chunks were actually sent.

### b) No DB timeline/stages golden assertions

The `QueryTimeline`, `QueryStages`, `QueryExecutions` helpers exist and are called, but results are only checked with `assert.Len` or `assert.NotEmpty`. No golden file comparison.

### c) Chat context accumulation not verified

`TestE2E_ChatContextAccumulation` checks `llm.CapturedInputs()` count but never inspects the actual messages to verify the second chat call includes the first exchange. The design specifically calls for this.

### d) No WS event ordering assertions for sequential scenarios

Even for non-parallel tests where WS event ordering is deterministic (SingleStage, FailFast, ForcedConclusion, etc.), no event sequence is verified.

### e) Queue capacity health check is silently conditional

```go
if wp, ok := health["worker_pool"].(map[string]interface{}); ok {
    if activeSessions, ok := wp["active_sessions"].(float64); ok {
        assert.LessOrEqual(t, activeSessions, float64(2))
    }
}
```

If the health endpoint doesn't return `worker_pool` or `active_sessions`, the assertion is silently skipped.

---

## 7. What's Working Well

Despite the gaps, some things are solid:

1. **TestApp harness** — Full in-process wiring with proper cleanup. YAML configs loaded through production `config.Initialize` path (arguably better than the design's Go config builders).

2. **ScriptedLLMClient** — Dual dispatch (sequential + routed) works correctly for the scenarios that use it. `CapturedInputs()` is available for conversation verification (just not used yet).

3. **MCP in-memory setup** — Real `mcp.ClientFactory` backed by in-memory MCP SDK servers. `StaticToolHandler` / `ErrorToolHandler` helpers are clean.

4. **WSClient** — Connection, subscription, `WaitFor*` methods all work for synchronization.

5. **Golden file infrastructure** — `AssertGolden`, `AssertGoldenJSON`, `AssertGoldenEvents`, `Normalizer` with ID registration — all built and ready. Just not used.

6. **Normalizer** — Proper indexed placeholders (`{STAGE_ID_1}`, `{EXEC_ID_1}`) with registration API. Ready for use.

7. **HTTP helpers** — `SubmitAlert`, `GetSession`, `CancelSession`, `SendChatMessage`, `GetHealth` — clean and functional.

8. **DB query helpers** — `QueryTimeline`, `QueryStages`, `QueryExecutions`, `QueryLLMInteractions`, `QueryMCPInteractions` — all implemented and used (for basic checks at least).

9. **Config loader** — YAML configs go through production `config.Initialize`, exercising real config merge/validation.

---

## 8. Prioritized Gap List

From most impactful to least:

| # | Gap | Impact | Difficulty |
|---|-----|--------|------------|
| 1 | No WS event golden comparison in any test | **Critical** — core design goal, 0% done | Medium — infrastructure exists, just needs wiring |
| 2 | Observability test is hollow | **Critical** — anchor test proves data pipeline | High — needs LLM conversation + interaction golden files |
| 3 | "Either/or" status assertions | **High** — masks real bugs in cancel/timeout flows | Medium — need to fix underlying behavior first |
| 4 | No timeline/stages golden files | **High** — DB state not verified beyond counts | Medium |
| 5 | Chat context accumulation not verified | **High** — key feature untested | Low — just inspect `CapturedInputs()` messages |
| 6 | Session golden files for 11 scenarios | **Medium** — session API response not compared | Low — just run with `-update` |
| 7 | Loose count assertions (GreaterOrEqual) | **Medium** — hides unexpected extra/missing records | Low — replace with exact counts |
| 8 | Queue capacity health check silent skip | **Medium** — assertion may never run | Low — use `require` instead of `if/ok` |
| 9 | No tool summarization test | **Medium** — summarization flow unverified | Medium — need large tool result + extra LLM call |
| 10 | Stream chunk collapse not done | **Low** — pragmatic exclusion is acceptable | Low |
| 11 | Missing golden dirs for 11 scenarios | **Low** — cosmetic but affects consistency | Low — auto-generated with `-update` |

---

## 9. Infrastructure Gaps

| Item | Status | Notes |
|------|--------|-------|
| `harness.go` (TestApp) | DONE | Functional |
| `mock_llm.go` (ScriptedLLMClient) | DONE | Dual dispatch works |
| `mcp_helpers.go` (SetupInMemoryMCP) | DONE | Real MCP stack |
| `ws_client.go` (WSClient) | DONE | Event collection works |
| `golden.go` (AssertGolden*) | DONE | Built but unused |
| `normalize.go` (Normalizer) | DONE | Indexed placeholders ready |
| `helpers.go` (HTTP/DB/WS projection) | DONE | FilterEventsForGolden unused |
| Makefile `test-e2e` target | NOT CHECKED | Design calls for it |
| Race detector testing | NOT CHECKED | Design calls for it |
| `test/e2e/README.md` | EXISTS | Not verified for completeness |

---

## 10. Recommended Approach

Instead of trying to fix everything at once (which led to the current state), address gaps incrementally:

**Phase A**: Fix the simplest deterministic scenarios first
- SingleStage, FailFast, ExecSummaryFailOpen, ForcedConclusion
- Add WS event golden files
- Add timeline/stages golden files
- Replace loose assertions with exact checks

**Phase B**: Fix cancel/timeout scenarios
- Investigate the "either/or" status patterns
- Fix underlying behavior so tests can assert a single expected status
- Add golden files

**Phase C**: Chat scenarios
- ChatContextAccumulation: verify LLM input messages
- ChatCancellation / ChatTimeout: fix status ambiguity
- Add golden files

**Phase D**: Parallel scenarios
- ParallelAny, ParallelAll, Replicas
- Decide on deterministic WS assertion strategy for parallel execution
- Add golden files where ordering is deterministic

**Phase E**: Full flow + Observability
- Restore FullFlow to design spec
- Build comprehensive observability with all 4 data layers
- Tool summarization

**Phase F**: Concurrency + queue
- ConcurrentSessions, QueueCapacity
- Fix silent health check assertions
