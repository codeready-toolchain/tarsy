# Pipeline Golden Files Review — Findings

Systematic review of all golden files in `test/e2e/testdata/golden/pipeline/` and
cross-referencing with test setup, config, production code, and expected events.

**Date:** 2026-02-12
**Branch:** phase6-impl

---

## 1. ~~PRODUCTION BUG: AgentExecution stores wrong iteration strategy + synthesis prompt mislabels it~~ FIXED

**Severity:** Low-Medium | **Status:** Fixed
**Files:** `pkg/queue/executor.go` (lines 370-386, 569-584)

### Problem

Two related issues stemming from the same root cause:

**1a. DB record has wrong strategy.** The `AgentExecution.IterationStrategy`
field is populated from the raw `StageAgentConfig` before `ResolveAgentConfig`
is called. When the strategy is inherited from the agent registry (not overridden
at stage level), the stored value is empty/wrong.

```go
// executor.go:370-376 — execution created BEFORE resolution
exec, err := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
    // ...
    IterationStrategy: agentConfig.IterationStrategy,  // ← raw stage config (empty!)
})

// executor.go:386 — resolved AFTER creation
resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, input.chain, input.stageConfig, agentConfig)
```

**1b. Synthesis prompt shows wrong strategy.** `buildSynthesisContext` also reads
from the raw stage config and falls back to defaults, skipping the agent registry:

```go
// executor.go:576-584
strategy = string(configs[i].agentConfig.IterationStrategy) // empty — not set in stage YAML
if strategy == "" {
    strategy = string(e.cfg.Defaults.IterationStrategy)     // falls back to "native-thinking"
}
```

Visible in golden file `20_SynthesisAgent_llm_synthesis_1.golden`:
```
#### Agent 1: ConfigValidator (native-thinking, test-provider)
```
But ConfigValidator actually ran as `react` (confirmed by its conversation golden
files showing ReAct format prompts).

### Root Cause

The execution record is created before config resolution, so it captures the raw
stage-level value. The synthesis context builder also reads raw stage config
instead of using the stored (or resolved) value.

Note: in typical production configs, strategy is set at the stage/chain level
(not the global agent registry), so this bug only triggers when the strategy is
inherited from the agent definition. Still incorrect when it does trigger.

### Impact

- `AgentExecution.IterationStrategy` in the DB is wrong for agents inheriting
  strategy from the agent registry
- The synthesizer LLM receives incorrect strategy metadata for those agents
- Any dashboard showing per-execution strategy from DB would display it wrong

### Fix

1. Move `CreateAgentExecution` after `ResolveAgentConfig`, or update the
   execution record after resolution, so the DB stores the correctly resolved
   `resolvedConfig.IterationStrategy`.
2. `buildSynthesisContext` reads from the `AgentExecution` DB record (which now
   has the correct value) instead of recalculating from raw stage config.

This way the strategy is resolved once (correctly), stored once, and read from
DB everywhere — no recalculation needed.

---

## 2. ~~OBSERVABILITY GAP: Summarization conversations not stored in DB~~ — FIXED

**Severity:** Medium
**Files:** `pkg/agent/controller/summarize.go`, `pkg/api/handler_debug.go`

### Problem

Summarization `LLMInteraction` records were created with `messages_count: 2`
but no conversation data — the debug detail API returned an empty conversation.
Dashboard users could not inspect summarization prompts.

### Root Cause

`callSummarizationLLM` recorded the interaction via `recordLLMInteraction` but
never stored the conversation messages. Summarization conversations are
self-contained (system + user + assistant) and run mid-iteration, so they
cannot share the iteration's `Message` table sequence without corrupting
`ReconstructConversation` for both iteration and summarization interactions.

### Fix Applied

Stored the conversation **inline in `llm_request.conversation`** on the
`LLMInteraction` record. No schema change needed — `llm_request` is already
`map[string]any`. The handler extracts inline conversations as a fallback
when no `Message` records are linked.

**Changes:**
- `summarize.go`: Replaced `recordLLMInteraction` with `recordSummarizationInteraction`
  that embeds all 3 messages (system, user, assistant) in `llm_request.conversation`.
- `handler_debug.go`: Added `extractInlineConversation` fallback in
  `toLLMDetailResponse` — if `Message` records are empty, extracts from
  `llm_request["conversation"]`.
- Unit tests: `handler_debug_test.go` (inline conversation extraction, precedence),
  `summarize_test.go` (verifies inline conversation stored in DB).
- E2e golden files updated: both summarization golden files now show full
  human-readable conversation (system prompt, user context, assistant response).

---

## 3. ~~OBSERVABILITY GAP: Executive summary LLM call not tracked as interaction~~ — FIXED

**Severity:** Medium
**Files:** `ent/schema/llminteraction.go`, `pkg/queue/executor.go`, `pkg/services/interaction_service.go`, `pkg/api/handler_debug.go`, `pkg/models/interaction.go`

### Problem

The test asserted 14 LLM calls total, but `debug_list.golden` only contained 13
LLM interactions. The missing one was the executive summary.

### Root Cause

`generateExecutiveSummary` called `e.llmClient.Generate()` directly without
recording an `LLMInteraction` or storing `Message` records.

### Fix Applied

**Schema change:** Made `stage_id` and `execution_id` optional (`Nillable`) on the
`LLMInteraction` entity, since the executive summary is a session-level interaction
that doesn't belong to any stage or execution. Updated the corresponding edges to
no longer be `Required()`.

**Interaction recording:** `generateExecutiveSummary` now records an `LLMInteraction`
(type `"executive_summary"`) with inline conversation (system + user + assistant
messages stored in `llm_request.conversation`), consistent with how summarization
interactions are handled.

**Debug API:** `DebugListResponse` gained a new `session_interactions` field that
surfaces session-level interactions separately from the stage hierarchy. The
`buildDebugListResponse` function routes interactions with nil `execution_id` to
this new section.

**Test coverage:**
- Unit test: `TestBuildDebugListResponse_SessionLevelInteractions` — verifies
  grouping of nil-execution interactions into `session_interactions`
- Unit test: `TestInteractionService_CreateLLMInteraction_SessionLevel` — verifies
  DB persistence with nil stage_id/execution_id
- Integration test: `TestExecutor_ExecutiveSummaryGenerated` — extended to verify
  the LLM interaction is created with correct fields and inline conversation
- E2e: New golden file `21_Session_llm_executive_summary_1.golden` verifies the
  full prompt and response. `debug_list.golden` now shows the session_interactions section.

---

## 4. MINOR: Empty tool arguments omitted from MCP detail

**Severity:** Very Low
**Files:** `test/e2e/golden.go` (AssertGoldenMCPInteraction), `pkg/api/handler_debug.go`

### Problem

`02_DataCollector_mcp_get_nodes_1.golden` has no `=== TOOL_ARGUMENTS ===` section
because the arguments were `{}` (empty object, stored as nil). Other MCP
interactions with real arguments (e.g., `03_DataCollector_mcp_get_pods_1.golden`)
properly show their arguments.

### Impact

Cosmetic. In the dashboard, a user might wonder whether arguments were passed at
all vs. seeing an explicit empty `{}`.

### Fix (optional)

Render `=== TOOL_ARGUMENTS ===` with `{}` when arguments are nil/empty, so it's
explicit that the tool was called with no arguments. Alternatively, leave as-is —
this is a display preference.

---

## Verified — No Issues Found

These areas were checked and found correct:

- **Token counts** — match between script entries, debug_list, and interaction details
- **Conversation accumulation** — messages grow correctly (2 → 5 → 7 for DataCollector across 3 iterations)
- **Summarization in conversations** — summarized tool results appear with `[NOTE: ...]` prefix and summary text
- **Stage context chaining** — investigation → remediation → validation all carry forward correctly
- **MCP server scoping** — ConfigValidator sees only test-mcp tools (4), MetricsValidator only prometheus-mcp, Remediator sees all 7
- **ReAct vs Native-Thinking prompts** — ReAct lists tools in user prompt; native-thinking doesn't
- **Forced conclusion** — iteration limit message well-structured, metadata present on all relevant events
- **Synthesis parallel results** — both agents included with tool calls and responses
- **Deterministic ordering** — structural ordering (stage → agent index → chronological within agent)
- **Normalization** — timestamps, UUIDs, durations properly replaced with stable placeholders
- **Timeline golden** — all agents, all events, correct metadata including forced_conclusion markers
- **Session golden** — correct final_analysis, executive_summary, status, stage references
- **Stages golden** — 4 stages in correct order, all completed
- **Map iteration determinism** — confirmed in prior audit that all production paths sort correctly
