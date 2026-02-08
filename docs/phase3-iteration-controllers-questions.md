# Phase 3.2: Iteration Controllers — Design Questions

This document contains questions and concerns about the proposed Phase 3.2 architecture that need discussion before finalizing the design.

**Status**: 🔄 In Discussion  
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

**Status**: 🔄

**Context:**

The project plan lists "Final analysis controller (tool-less comprehensive analysis)" as a Phase 3.2 item. Final Analysis produces a comprehensive final analysis from all accumulated investigation data — typically used as the last stage of a chain.

In old TARSy, `ReactFinalAnalysisController` was a separate controller (now dropped). The question is how to trigger `FinalAnalysisController` in new TARSy.

Currently, the controller factory maps `IterationStrategy` enum values to controllers. `FinalAnalysisController` needs a way to be selected.

**Options:**

**Option A: Add `final-analysis` iteration strategy enum**
- Add `IterationStrategyFinalAnalysis = "final-analysis"` to `pkg/config/enums.go`
- Controller factory maps it to `FinalAnalysisController`
- Chain config uses `iteration_strategy: final-analysis` for the final stage
- Pros: Explicit, discoverable, consistent with other strategies
- Cons: Another enum value; `FinalAnalysisController` is really just a specialized single-call

**Option B (Recommended): Use `synthesis` strategy + differentiate via prompt (Phase 3.3)**
- `FinalAnalysisController` is not needed as a separate controller
- Both synthesis and final analysis are tool-less single LLM calls
- The difference is entirely in the prompt: synthesis merges parallel results, final analysis provides comprehensive investigation summary
- Remove `FinalAnalysisController` from Phase 3.2; handle as a prompt template in Phase 3.3
- Chain config marks the last stage as `iteration_strategy: synthesis` and the prompt builder uses the stage position/name to select the appropriate template
- Pros: Fewer controllers, DRY, prompt concern handled in prompt phase
- Cons: Less explicit in config; relies on prompt builder to distinguish synthesis vs final analysis

**Option C: Add a `controller_type` field separate from `iteration_strategy`**
- Keep `iteration_strategy` for the core strategies (react, native-thinking, synthesis)
- Add an optional `controller_type: final-analysis` field to agent config
- Controller factory checks `controller_type` first, then falls back to `iteration_strategy`
- Pros: Separation of concerns between iteration pattern and controller purpose
- Cons: Two fields to configure; more complexity

**Recommendation:** Option B. Final analysis and synthesis are structurally identical (single tool-less LLM call). The difference is the prompt content, which belongs in Phase 3.3. This avoids creating a controller that duplicates `SynthesisController` logic. If we later find that final analysis needs different controller logic (not just different prompts), we can easily add a dedicated controller at that point.

---

### Q3: Chat Handling — Controllers Unaware or Chat-Aware?

**Status**: 🔄

**Context:**

Old TARSy has separate chat controllers (`ChatReActController`, `ChatNativeThinkingController`) that extend the base controllers and only override `build_initial_conversation()` to include investigation context and user questions.

The design doc proposes making controllers chat-unaware — chat is handled entirely through `ExecutionContext.ChatContext` and prompt building (Phase 3.3).

**Question:** Should Phase 3.2 controllers have any chat awareness, or should it be purely a prompt/context concern?

**Options:**

**Option A (Recommended): Controllers chat-unaware — pure prompt concern**
- Controllers don't check `ChatContext` at all
- `ChatContext` data flows into prompt building (Phase 3.3)
- Controllers build messages using a `MessageBuilder` interface that handles chat/non-chat internally
- Pros: Clean separation, no controller duplication, chat is purely a composition concern
- Cons: Requires careful Phase 3.3 prompt builder design; Phase 3.2 chat testing is limited until Phase 3.3

**Option B: Minimal chat awareness — controllers check ChatContext during message building**
- Controllers check `execCtx.ChatContext != nil` in their `buildMessages()` methods
- If chat, include investigation context + user question in messages
- No separate chat controllers, but controllers have a branch for chat
- Pros: Chat works in Phase 3.2 without waiting for Phase 3.3
- Cons: Prompt logic leaks into controllers; will need refactoring in Phase 3.3

**Option C: Separate chat controllers (old TARSy pattern)**
- `ChatReActController` extends `ReActController` with different message building
- `ChatNativeThinkingController` extends `NativeThinkingController`
- Pros: Explicit, similar to old TARSy
- Cons: Code duplication (only message building differs), more controllers to maintain

**Recommendation:** Option A. The only difference between chat and non-chat is the prompt content. Making controllers chat-aware couples prompt logic to iteration logic. Phase 3.2 should focus on iteration mechanics; Phase 3.3 will handle prompt composition including chat-specific templates. The `ChatContext` struct should still be added to `ExecutionContext` in Phase 3.2 so the data model is ready, but controllers should not inspect it.

---

## ⚠️ Important (Design Clarification)

### Q4: Synthesis and Synthesis-Native-Thinking — One Controller or Two?

**Status**: 🔄

**Context:**

Old TARSy has two separate synthesis controllers:
- `SynthesisController` — uses LangChain for multi-provider synthesis
- `SynthesisNativeThinkingController` — uses Gemini native thinking for synthesis (no tools)

The design doc proposes using a single `SynthesisController` for both `synthesis` and `synthesis-native-thinking` strategies, since the only difference is the LLM backend (determined by config).

**Question:** Is a single controller sufficient, or are there Gemini-specific behaviors that require a separate controller?

**Analysis:**

Both synthesis controllers in old TARSy:
1. Build a conversation with synthesis prompt + previous stage context
2. Make a single LLM call (no tools)
3. Return the result

The `SynthesisNativeThinkingController` additionally:
- Gets thinking content from the response (but new TARSy's `LLMResponse` already captures `ThinkingText` from `ThinkingChunk`s regardless of controller)
- Uses Google Native SDK instead of LangChain (but this is handled by the backend field in config, transparent to the controller)

**Options:**

**Option A (Recommended): Single `SynthesisController`**
- Both strategies use the same controller
- Thinking content is already captured by `collectStream` for any backend
- Backend selection is transparent (config-driven)
- Pros: DRY, simpler
- Cons: None identified — no behavioral difference exists

**Option B: Two separate controllers**
- `SynthesisController` and `SynthesisNativeThinkingController`
- Pros: Mirrors old TARSy
- Cons: Code duplication with zero behavioral difference

**Recommendation:** Option A. There is no behavioral difference between the two — the thinking content collection and backend selection are already handled at layers below the controller. One controller is strictly better.

---

### Q5: Per-Iteration Timeout — How Should It Work in Go?

**Status**: 🔄

**Context:**

Old TARSy wraps each iteration with `asyncio.wait_for(run_iteration(), timeout=iteration_timeout)`. The `iteration_timeout` is a global setting (`settings.llm_iteration_timeout`).

In Go, timeouts are handled via `context.Context`. The session-level timeout is already handled by the queue system (context deadline). But individual iterations within a controller also need timeout protection to prevent a single stuck LLM call or tool execution from consuming the entire session timeout.

**Question:** How should per-iteration timeouts be implemented?

**Options:**

**Option A (Recommended): Per-iteration context deadline**
- Each iteration creates a child context with its own deadline: `iterCtx, cancel := context.WithTimeout(ctx, iterationTimeout)`
- LLM calls and tool executions use `iterCtx`
- If the iteration times out, `iterCtx` is cancelled, the LLM stream is terminated, and the controller moves to the next iteration (or records a timeout failure)
- `iterationTimeout` comes from `ResolvedAgentConfig` (configurable per chain/agent)
- Pros: Go-native, clean cancellation propagation, configurable
- Cons: Need to be careful that `iterCtx` cancellation doesn't interfere with parent `ctx`

**Option B: No per-iteration timeout — rely on session timeout only**
- The session context already has a deadline
- If a single iteration takes too long, eventually the session times out
- Pros: Simple
- Cons: One stuck iteration wastes the entire session timeout; no feedback about which iteration was slow

**Option C: Channel-based timeout (select with timer)**
- Use `select` with `time.After()` to race LLM calls against a timer
- Pros: Explicit control
- Cons: More complex than context-based, doesn't compose as well

**Recommendation:** Option A. Go's `context.WithTimeout` is the idiomatic approach. The iteration timeout should be added to `ResolvedAgentConfig` (from chain/agent config) with a sensible default (e.g., 120 seconds). This gives each iteration its own deadline while the parent session context controls the overall timeout.

**Config addition needed:**
```go
type ResolvedAgentConfig struct {
    // ... existing fields ...
    IterationTimeout time.Duration  // Per-iteration timeout (default: 120s)
}
```

---

### Q6: ReAct Parser — Port Exact Old TARSy Logic or Simplify?

**Status**: 🔄

**Context:**

Old TARSy's `react_parser.py` has extensive multi-tier detection logic:
- Section-based extraction (Thought/Action/Action Input/Final Answer)
- Multi-format action input parsing (JSON, YAML, key-value, raw)
- Missing action recovery (look for tool-like patterns without explicit `Action:` marker)
- Specific error feedback generation for different malformed response types
- Tool name validation against available tools

Some of this complexity was added over time to handle specific LLM format deviations. The question is whether to port all of this or start simpler.

**Options:**

**Option A: Port complete parser logic**
- Implement all detection tiers, multi-format parsing, and recovery logic
- Pros: Full parity; handles all edge cases old TARSy encountered
- Cons: Complex; some recovery logic may be for models/prompts we no longer use

**Option B (Recommended): Port core logic, simplify recovery**
- Implement section-based detection (Thought/Action/Action Input/Final Answer)
- Implement JSON action input parsing (primary format)
- Implement specific error feedback for common cases
- Skip: YAML parsing, key-value parsing, missing action recovery
- Add recovery logic later if/when we encounter specific failures
- Pros: Simpler, covers 95% of cases, easy to extend
- Cons: May need to add recovery logic later

**Option C: Minimal parser — just regex extraction**
- Simple regex patterns for `Action:`, `Action Input:`, `Final Answer:`
- Pros: Very simple
- Cons: Too brittle; real LLM output is messy

**Recommendation:** Option B. Start with robust core parsing (section detection + JSON action input + error feedback) and skip the exotic recovery logic. If we encounter format deviations in production, we can add specific recovery handlers. The parser should be designed for extensibility (add new detection strategies without changing existing ones).

---

### Q7: Thinking Content — Separate Timeline Events or LLMInteraction Only?

**Status**: 🔄

**Context:**

Old TARSy stores thinking content (from Gemini native thinking) in `LLMInteraction.metadata['thinking_content']`. It does NOT create separate timeline events for thinking content.

The Phase 2 database design defines a `llm_thinking` event type in the `TimelineEvent` enum. The design doc proposes creating `llm_thinking` timeline events for thinking content.

**Question:** Should thinking content create timeline events, or stay in LLMInteraction only?

**Options:**

**Option A (Recommended): Create `llm_thinking` timeline events**
- For native thinking and synthesis-native-thinking, when `ThinkingText` is present, create a `llm_thinking` timeline event
- Also store in `LLMInteraction.thinking_content` (already planned)
- Pros: Thinking appears in the timeline (frontend can show it), consistent with event type enum, enriches the real-time update stream
- Cons: More timeline events (potentially verbose), thinking content stored in two places

**Option B: LLMInteraction only (old TARSy pattern)**
- Store thinking content only in `LLMInteraction.thinking_content`
- No timeline events for thinking
- Pros: Less verbose timeline, matches old TARSy
- Cons: Thinking content not visible in timeline, event type enum value unused

**Option C: Configurable per agent**
- Add `show_thinking_in_timeline: true/false` to agent config
- Pros: Flexible
- Cons: More configuration; premature flexibility

**Recommendation:** Option A. The `llm_thinking` event type already exists in the schema, and thinking content is valuable for debugging and transparency. The frontend can choose whether to display thinking events. The duplication (timeline + LLMInteraction) is intentional — timeline is for real-time updates, LLMInteraction is for detailed audit.

---

## 📋 Design Clarification

### Q8: ToolExecutor Integration with ExecutionContext — When to Wire?

**Status**: 🔄

**Context:**

The design introduces a `ToolExecutor` interface in `ExecutionContext`. Phase 3.2 provides a `StubToolExecutor`. Phase 4 replaces it with a real MCP client.

**Question:** Should the `StubToolExecutor` be wired into `ExecutionContext` in Phase 3.2, or should `ToolExecutor` remain nil until Phase 4?

**Options:**

**Option A (Recommended): Wire `StubToolExecutor` in Phase 3.2**
- `SessionExecutor` creates and injects `StubToolExecutor` for all tool-using strategies
- Controllers can be tested end-to-end with stub tool responses
- Pros: Controllers are testable in Phase 3.2 without Phase 4
- Cons: Need to update `SessionExecutor` to create stubs

**Option B: Leave nil until Phase 4**
- Controllers check `execCtx.ToolExecutor != nil` before calling
- ReAct/NativeThinking return an error if no tool executor and tools are needed
- Pros: Clean separation; no stub code
- Cons: Can't test tool-using controllers until Phase 4

**Recommendation:** Option A. The whole point of the `ToolExecutor` interface is to enable Phase 3.2 testing. The stub should be wired in `SessionExecutor` with the tool definitions from the agent config (which references MCP server tools — for Phase 3.2 these will be empty or mock).

---

### Q9: Backend Selection for Phase 3.2 — Does LangChainProvider Need to Exist?

**Status**: 🔄

**Context:**

Phase 3.1 Q1 decided on a dual-provider model: `LangChainProvider` for multi-provider and `GoogleNativeProvider` for Gemini-specific features. The backend mapping is:
- `react`, `synthesis` → `langchain` backend
- `native-thinking`, `synthesis-native-thinking` → `google-native` backend

But `LangChainProvider` has not been implemented yet — Phase 3.1 only delivered `GoogleNativeProvider`. The question is whether Phase 3.2 needs `LangChainProvider` to test ReAct and Synthesis controllers.

**Key insight:** ReAct doesn't use function calling at all — it's pure text generation. `GoogleNativeProvider` can do text generation without using any Gemini-specific features. The multi-provider benefit of LangChain only matters in Phase 6 (multi-LLM support).

**Options:**

**Option A (Recommended): Use `GoogleNativeProvider` for all strategies in Phase 3.2**
- Temporarily route all strategies through `google-native` backend
- ReAct works because it doesn't use function calling (just text generation)
- Synthesis works because it's also just text generation
- Implement `LangChainProvider` in Phase 6 when we actually need multi-provider
- Pros: No LangChain dependency in Phase 3.2, simpler, ReAct/Synthesis work fine with any text-generating backend
- Cons: Delays LangChain integration; if there are provider-specific text generation quirks, they won't surface until Phase 6

**Option B: Implement `LangChainProvider` in Phase 3.2**
- Build the LangChain provider now to establish the dual-provider architecture
- Pros: Architecture ready for Phase 6, tests exercise both backends
- Cons: More work in Phase 3.2 for a feature (multi-provider) that isn't needed yet

**Option C: Implement a minimal `LangChainProvider` stub**
- `LangChainProvider` exists but internally delegates to `GoogleNativeProvider`
- Backend routing works, but actual LangChain SDK isn't used
- Pros: Architecture tested, no LangChain dependency yet
- Cons: Misleading name; stub that pretends to be something it isn't

**Recommendation:** Option A. Since we're only supporting Gemini in Phases 3-5, all strategies can use `GoogleNativeProvider` for text generation. ReAct just needs text output (no function calling). Implementing LangChain now adds complexity for a feature that won't be used until Phase 6. When Phase 6 arrives, we can implement `LangChainProvider` and update the backend routing.

**Impact on design doc:** If accepted, the "Backend Selection" table changes — all strategies use `google-native` in Phase 3.2. The `backend` field in proto/config still exists but is set to `google-native` for all strategies until Phase 6.

---

### Q10: Sequence Number Management — Global Counter or Per-Type?

**Status**: 🔄

**Context:**

Timeline events and messages both have `sequence_number` fields. The `SingleCallController` (Phase 3.1) uses a simple counter. But Phase 3.2 controllers create many more records (multiple iterations, tool calls, tool results, thinking events), and the sequence numbering needs to be consistent and correct.

**Question:** How should sequence numbers be managed across the iteration loop?

**Options:**

**Option A (Recommended): Single atomic counter per execution**
- `ExecutionContext` gets a `NextSequenceNumber() int` method
- All timeline events, messages, and interactions use this counter
- Monotonically increasing across the entire execution
- Pros: Simple, correct ordering, no confusion about relative order of events vs messages
- Cons: Messages and timeline events share numbering space (not separated)

**Option B: Separate counters for messages and timeline events**
- Messages have their own sequence: 1, 2, 3...
- Timeline events have their own sequence: 1, 2, 3...
- Pros: Cleaner per-type ordering
- Cons: Can't determine relative order between a message and a timeline event

**Option C: Timestamp-based ordering only**
- No sequence numbers — use `created_at` timestamps
- Pros: No counter management needed
- Cons: Timestamps can collide in fast loops; less reliable ordering

**Recommendation:** Option A. A single counter ensures a total order across all records created during an execution. This makes reconstructing the exact execution timeline straightforward. The counter should be a simple `int` on `ExecutionContext` (or a helper struct), incremented atomically.

---

## Summary

| Question | Topic | Priority | Recommendation |
|---|---|---|---|
| Q1 | Dropping session pause/resume | 🔥 Critical | Option A: Drop, always force conclusion or fail |
| Q2 | Final Analysis as strategy or prompt | 🔥 Critical | Option B: Handle via synthesis + prompt (Phase 3.3) |
| Q3 | Chat handling approach | 🔥 Critical | Option A: Controllers chat-unaware, prompt concern |
| Q4 | Synthesis — one or two controllers | ⚠️ Important | Option A: Single `SynthesisController` |
| Q5 | Per-iteration timeout mechanism | ⚠️ Important | Option A: Per-iteration `context.WithTimeout` |
| Q6 | ReAct parser completeness | ⚠️ Important | Option B: Core logic, skip exotic recovery |
| Q7 | Thinking content in timeline | ⚠️ Important | Option A: Create `llm_thinking` timeline events |
| Q8 | ToolExecutor wiring in Phase 3.2 | 📋 Clarification | Option A: Wire `StubToolExecutor` |
| Q9 | Backend selection for Phase 3.2 | 📋 Clarification | Option A: Use `GoogleNativeProvider` for all |
| Q10 | Sequence number management | 📋 Clarification | Option A: Single atomic counter per execution |
