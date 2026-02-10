# Phase 3.2: Iteration Controllers — Design Questions

This document contains questions and concerns about the proposed Phase 3.2 architecture that need discussion before finalizing the design.

**Status**: ✅ All Decided  
**Created**: 2026-02-08  
**Purpose**: Surface architectural decisions where the new TARSy significantly departs from old TARSy, or where non-obvious trade-offs need discussion

---

## How to Use This Document

For each question:
1. ✅ = Decided
2. 🔄 = In Discussion  
3. ⏸️ = Deferred
4. ❌ = Rejected

Add your answers inline under each question, then we'll update the main design doc.

---

## 🔥 Critical Priority (Architecture Decisions)

### Q1: Dropping Session Pause/Resume — Always Force Conclusion?

**Status**: ✅ Decided — **Option A: Always force conclusion or fail — drop pause/resume**

**Context:**

Old TARSy has a three-way decision at max iterations:
1. **If last interaction failed** → `MaxIterationsFailureError` (hard fail)
2. **If chat context OR `force_conclusion_at_max_iterations` enabled** → `ForceConclusion` (one more LLM call to wrap up)
3. **Otherwise** → `SessionPaused` (save conversation state, allow manual resume later)

`SessionPaused` stores the entire conversation in the database so a user can manually resume the session later. This requires:
- Conversation serialization/deserialization
- `_restore_paused_conversation()` logic at the start of every controller
- Paused session state management in the queue system
- Frontend support for resume UI

**Question:** Should new TARSy drop `SessionPaused` entirely and always force conclusion or fail?

**Options:**

#### Decision

Always force conclusion or fail — drop pause/resume entirely:
- If last interaction failed → fail the execution
- Otherwise → force conclusion (one more LLM call without tools)
- No `SessionPaused`, no conversation serialization, no resume logic

Session pause/resume adds substantial complexity for minimal value. If an investigation needs more iterations, the right solution is increasing `max_iterations` in the chain config, not manual resumption.

**Rejected alternatives:**
- Option B (keep pause/resume for parity) — significant complexity for a rarely-used feature
- Option C (configurable force conclusion flag) — unnecessary config complexity when the answer is always "force conclude"

---

### Q2: Final Analysis — New Strategy Enum or Configuration-Based Selection?

**Status**: ✅ Decided — **Drop final-analysis entirely — not a real strategy**

**Context:**

The project plan listed "Final analysis controller (tool-less comprehensive analysis)" as a Phase 3.2 item. The question was how to trigger it in new TARSy.

#### Decision

Drop the final-analysis concept entirely. It was a leftover from old TARSy that was never used in production. Final analysis is naturally produced by the existing flow:

- **Single agent stages**: The investigation agent (ReAct or Native Thinking) iterates until it stops calling tools and produces a final answer. That final answer is the stage result.
- **Multi-agent parallel stages**: Each agent produces a final answer → the synthesis agent combines them into the stage result.
- **Last stage in chain**: Its stage result becomes the session's final analysis.

There is no gap that requires a separate "final analysis" controller or strategy. Remove `FinalAnalysisController` from the Phase 3.2 design and the project plan.

**Rejected alternatives:**
- Option A (add `final-analysis` iteration strategy enum) — unnecessary, the concept itself is unneeded
- Option B (use synthesis + prompt differentiation) — synthesis serves a different purpose (combining parallel results), conflating it with final analysis would be confusing
- Option C (separate `controller_type` field) — unnecessary complexity for a non-existent need

---

### Q3: Chat Handling — Controllers Unaware or Chat-Aware?

**Status**: ✅ Decided — **Option A: Controllers chat-unaware — pure prompt concern**

**Context:**

Old TARSy has separate chat controllers (`ChatReActController`, `ChatNativeThinkingController`) that extend the base controllers and only override `build_initial_conversation()` to include investigation context and user questions.

#### Decision

Controllers are chat-unaware. The only difference between a chat agent and a regular investigation agent is the initial prompt — chat uses a system message like "You are here to answer user's question regarding the investigation..." vs the regular "You are an SRE agent investigating an alert...". Stage context wrapping may differ slightly too (to be verified against old TARSy in Phase 3.3).

Since this is purely a prompt composition concern, controllers don't need any chat awareness. The `ChatContext` struct will be added to `ExecutionContext` in Phase 3.2 so the data model is ready, but controllers won't inspect it. Full chat support lands in Phase 3.3 when the prompt builder handles chat-specific templates.

**Rejected alternatives:**
- Option B (minimal chat awareness in controllers) — leaks prompt logic into controllers, would need refactoring in Phase 3.3
- Option C (separate chat controllers, old TARSy pattern) — code duplication for no benefit when only the prompt differs

---

## ⚠️ Important (Design Clarification)

### Q4: Synthesis and Synthesis-Native-Thinking — One Controller or Two?

**Status**: ✅ Decided — **Option A: Single `SynthesisController`**

**Context:**

Old TARSy has two separate synthesis controllers. The question was whether the three key differences between react and native-thinking strategies require separate synthesis controllers.

#### Decision

A single `SynthesisController` handles all three differences between react and native-thinking:

1. **Tool calling format** (ReAct text vs native function calling) — N/A for synthesis, it doesn't call tools.
2. **Gemini native tools** (google_search, code_execution, url_context) — configured in `LLMProviderConfig.NativeTools` and passed through to Python via `GenerateInput.Config`. Python handles them transparently. The controller just passes the config as-is.
3. **Native thinking** — controlled by the `backend` field in config. When `google-native`, Python uses `GoogleNativeProvider` with thinking enabled. `ThinkingChunk`s are captured by `collectStream` regardless of controller.

All three differences are handled at the config/Python layer, not the controller layer. One controller is strictly better.

**Rejected alternatives:**
- Option B (two separate controllers) — code duplication with zero behavioral difference

---

### Q5: Per-Iteration Timeout — How Should It Work in Go?

**Status**: ✅ Decided — **Option A: Per-iteration `context.WithTimeout`**

**Context:**

Old TARSy wraps each iteration with `asyncio.wait_for(run_iteration(), timeout=iteration_timeout)`. Per-iteration timeouts prevent a single stuck iteration from consuming the entire session budget — especially important for parallel agents where one stuck agent shouldn't prevent the synthesis stage from running.

#### Decision

Each iteration creates a child context with its own deadline: `iterCtx, cancel := context.WithTimeout(ctx, iterationTimeout)`. LLM calls and tool executions use `iterCtx`. If the iteration times out, `iterCtx` is cancelled, the LLM stream is terminated, and the controller records a timeout failure and moves on.

The parent `ctx` (session-level) still carries the overall session timeout and user cancellation signal. The per-iteration `iterCtx` is a child — if the parent is cancelled (user cancellation), all iteration contexts are cancelled too. This gives us both:
- **Per-iteration timeout**: prevents wasting session budget on one stuck call
- **User cancellation**: propagates immediately through the context chain

`iterationTimeout` comes from `ResolvedAgentConfig` (configurable per chain/agent) with a sensible default (e.g., 120 seconds).

**Config addition needed:**
```go
type ResolvedAgentConfig struct {
    // ... existing fields ...
    IterationTimeout time.Duration  // Per-iteration timeout (default: 120s)
}
```

**Rejected alternatives:**
- Option B (session timeout only) — one stuck iteration wastes the entire session budget; especially bad for parallel agents
- Option C (channel-based timeout) — more complex, doesn't compose as well as context-based

---

### Q6: ReAct Parser — Port Exact Old TARSy Logic or Simplify?

**Status**: ✅ Decided — **Option A: Port complete parser logic**

**Context:**

Old TARSy's `react_parser.py` has extensive multi-tier detection logic evolved over time after facing multiple malformed ReAct responses from LLMs.

#### Decision

Port the complete parser logic from old TARSy. The multi-tier detection, multi-format action input parsing (JSON, YAML, key-value, raw), missing action recovery, and specific error feedback were all added to handle real LLM format deviations encountered in production. The current parser is proven to work well — it's very forgiving and capable of extracting needed parts even from badly formatted responses. Simplifying it would mean re-learning the same lessons.

**Rejected alternatives:**
- Option B (core logic only, skip exotic recovery) — would lose battle-tested recovery logic and require re-discovering the same edge cases
- Option C (minimal regex) — too brittle for real LLM output

---

### Q7: Thinking Content — Separate Timeline Events or LLMInteraction Only?

**Status**: ✅ Decided — **Option A: Create `llm_thinking` timeline events**

**Context:**

Old TARSy stores thinking content only in `LLMInteraction.metadata`. Phase 2 introduced timeline events as a first-class concept with `llm_thinking` as a defined event type.

#### Decision

Create `llm_thinking` timeline events when thinking content is present. Timeline events are the primary data model for real-time frontend updates — this was a key Phase 2 architectural decision and should be fully implemented. LLMInteraction records serve a different purpose (debugging/observability). The duplication is intentional: timeline for real-time display, LLMInteraction for detailed audit.

**Rejected alternatives:**
- Option B (LLMInteraction only) — underutilizes the timeline event system we deliberately designed in Phase 2
- Option C (configurable per agent) — premature flexibility

---

## 📋 Design Clarification

### Q8: ToolExecutor Integration with ExecutionContext — When to Wire?

**Status**: ✅ Decided — **Option A: Wire `StubToolExecutor` in Phase 3.2**

**Context:**

The design introduces a `ToolExecutor` interface in `ExecutionContext`. Phase 3.2 provides a `StubToolExecutor`. Phase 4 replaces it with a real MCP client.

#### Decision

Wire `StubToolExecutor` in Phase 3.2. Controllers are testable end-to-end with stub tool responses without waiting for Phase 4 MCP integration.

**Rejected alternatives:**
- Option B (leave nil until Phase 4) — can't test tool-using controllers until Phase 4

---

### Q9: Backend Selection for Phase 3.2 — Does LangChainProvider Need to Exist?

**Status**: ✅ Decided — **Option C: Minimal `LangChainProvider` stub delegating to `GoogleNativeProvider`**

**Context:**

Phase 3.1 Q1 decided on a dual-provider model. `LangChainProvider` hasn't been implemented yet. The question was whether Phase 3.2 needs it.

#### Decision

Implement a minimal `LangChainProvider` stub that internally delegates to `GoogleNativeProvider`. This gets all the wiring correct from day one:
- Go correctly routes `react`/`synthesis` → `langchain` backend
- Python's `LangChainProvider` receives the request and delegates to `GoogleNativeProvider`
- Backend routing, interface, and all Go-side code are production-correct

When Phase 6 arrives, we only need to replace `LangChainProvider` internals with real LangChain SDK calls — no refactoring of Go config resolution or Python routing needed.

**Rejected alternatives:**
- Option A (use GoogleNativeProvider for all, route everything to `google-native`) — requires changing Go's config resolution in Phase 6 on top of implementing LangChainProvider; unnecessary refactoring
- Option B (implement real LangChainProvider now) — more work for a feature (multi-provider) not needed until Phase 6

---

### Q10: Sequence Number Management — Global Counter or Per-Type?

**Status**: ✅ Decided — **Option B: Separate counters per type**

**Context:**

Timeline events and messages both have `sequence_number` fields. Phase 3.2 controllers create many more records than Phase 3.1.

#### Decision

Separate counters per type. Messages get their own sequence (1, 2, 3...), timeline events get their own (1, 2, 3...). The `sequence_number` fields already exist in the schema and are already used by `SingleCallController`.

Messages and timeline events are always queried independently (messages feed the LLM conversation, timeline events feed the frontend), so a shared counter across types adds no practical value. Each type being self-consistent is sufficient — `ORDER BY sequence_number` within each type gives correct ordering.

**Rejected alternatives:**
- Option A (single shared counter) — messages and events are never queried together; shared counter adds complexity for no benefit
- Option C (timestamps only) — timestamps can collide in fast loops; `sequence_number` fields already exist

---

## Summary

| Question | Topic | Priority | Recommendation |
|---|---|---|---|
| Q1 | Dropping session pause/resume | 🔥 Critical | Option A: Drop, always force conclusion or fail |
| Q2 | Final Analysis — drop entirely | 🔥 Critical | Drop: not a real strategy, investigation agents produce final answers naturally |
| Q3 | Chat handling approach | 🔥 Critical | Option A: Controllers chat-unaware, prompt concern (Phase 3.3) |
| Q4 | Synthesis — one or two controllers | ⚠️ Important | Option A: Single `SynthesisController` |
| Q5 | Per-iteration timeout mechanism | ⚠️ Important | Option A: Per-iteration `context.WithTimeout` |
| Q6 | ReAct parser completeness | ⚠️ Important | Option A: Port complete parser logic from old TARSy |
| Q7 | Thinking content in timeline | ⚠️ Important | Option A: Create `llm_thinking` timeline events |
| Q8 | ToolExecutor wiring in Phase 3.2 | 📋 Clarification | Option A: Wire `StubToolExecutor` |
| Q9 | Backend selection for Phase 3.2 | 📋 Clarification | Option C: Stub `LangChainProvider` delegating to `GoogleNativeProvider` |
| Q10 | Sequence number management | 📋 Clarification | Option B: Separate counters per type |
