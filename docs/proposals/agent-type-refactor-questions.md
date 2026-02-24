# Agent Type System Refactor — Design Questions

**Status:** All 5 questions decided
**Related:** [Design document](agent-type-refactor-design.md)
**Context:** Identified during [orchestrator implementation design](orchestrator-impl-design.md)
**Last updated:** 2026-02-23

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How should controllers be restructured? — DECIDED

> **Decision:** Option D — Two structural controllers (iterating + single-shot) with parameterized config.

The current `FunctionCallingController` and `SynthesisController` represent two genuinely different control flow patterns: multi-turn iteration with tools vs. single LLM call without tools. The heavy lifting (streaming, message storage, timeline events, LLM call mechanics) is already extracted into shared package-level functions in `streaming.go`, `messages.go`, `timeline.go`, `helpers.go`. The controllers are thin orchestration layers composing those shared functions.

### Option D: Two structural controllers with parameterized config

Split controllers by **control flow pattern**, not by agent type. Parameterize the variable behaviors (prompt building, thinking fallback) via configuration rather than type-checks.

**Layer 1 — Shared functions (already exists):** Package-level functions (`callLLMWithStreaming`, `storeMessages`, `storeAssistantMessage`, `createTimelineEvent`, `recordLLMInteraction`, etc.) are the base layer. No new type needed.

**Layer 2 — Two control flow patterns:**

```go
// IteratingController — multi-turn loop with tools
// (current FunctionCallingController, renamed)
type IteratingController struct{}

func (c *IteratingController) Run(ctx, execCtx, prevStageContext) (*ExecutionResult, error) {
    // build messages → list tools → loop { call LLM → execute tool calls → repeat }
    // → forceConclusion if max iterations
}

// SingleShotController — one request, one response, no tools
type SingleShotController struct {
    cfg SingleShotConfig
}

type SingleShotConfig struct {
    BuildMessages    func(*ExecutionContext, string) []ConversationMessage
    ThinkingFallback bool  // use thinking text if response text is empty
}

func (c *SingleShotController) Run(ctx, execCtx, prevStageContext) (*ExecutionResult, error) {
    messages := c.cfg.BuildMessages(execCtx, prevStageContext)
    // single LLM call → store → return
}
```

**Layer 3 — Specializations via configuration:**

```go
// Synthesis = SingleShotController with synthesis prompt + thinking fallback
func NewSynthesisController(pb PromptBuilder) *SingleShotController {
    return &SingleShotController{cfg: SingleShotConfig{
        BuildMessages:    pb.BuildSynthesisMessages,
        ThinkingFallback: true,
    }}
}

// Scoring = SingleShotController with scoring prompt (no MCP tools)
func NewScoringController(pb PromptBuilder) *SingleShotController {
    return &SingleShotController{cfg: SingleShotConfig{
        BuildMessages:    pb.BuildScoringMessages,
        ThinkingFallback: false,
    }}
}

// Investigation = IteratingController as-is
// Orchestrator  = IteratingController as-is (tools from CompositeToolExecutor)
```

**Escape hatch:** If a future agent type needs behavior that doesn't fit `SingleShotConfig` or the iterating loop, it can implement the `Controller` interface directly and still compose the shared package-level functions. The interface is the contract; the pre-built controllers are conveniences, not constraints.

- **Pro:** Two controllers match the two real control flow patterns — no artificial unification.
- **Pro:** No type-awareness in controllers — behavior varies via injected config.
- **Pro:** Shared code already extracted into package-level functions — layer 1 is free.
- **Pro:** `SingleShotController` already has two users: synthesis (thinking fallback) and scoring (no fallback). Validates the parameterized approach.
- **Pro:** Orchestrator uses `IteratingController` unchanged — tools come from `CompositeToolExecutor`.
- **Pro:** Custom controllers remain possible via the `Controller` interface for future needs.
- **Con:** Slightly more indirection than a dedicated `SynthesisController` — config struct vs. hardcoded behavior.

**Rejected alternatives:** (A) Unify into single controller — controller becomes type-aware, single-responsibility violation; (B) Keep separate controllers per type — code duplication, new controller needed for each type; (C) Full strategy pattern via function injection — over-engineered, parameterizes everything including iteration behavior.

### Key points

- `FunctionCallingController` is renamed to `IteratingController`. Zero logic changes.
- `SynthesisController` is replaced by `SingleShotController` configured with synthesis behavior.
- Scoring uses `SingleShotController` with scoring-specific config (no MCP tools, no thinking fallback).
- The `Controller` interface remains the escape hatch for truly unique patterns.

---

## Q2: Should ScoringAgent be merged into BaseAgent? — DECIDED

> **Decision:** Option B — Keep `ScoringAgent` separate. It uses `SingleShotController` (from Q1).

`ScoringAgent` is identical to `BaseAgent` except it does NOT call `UpdateAgentExecutionStatus` (scoring lifecycle is managed externally by `ScoringService`). It's 52 lines of code, most of which is duplicated from BaseAgent.

The agent wrapper and controller are separate layers:
- **Agent wrapper** (this question): `ScoringAgent` stays separate — it skips execution status updates because `ScoringService` manages the lifecycle externally.
- **Controller** (Q1): Scoring uses `SingleShotController` configured with a scoring prompt builder. No MCP tools, single LLM call, no thinking fallback.

The scoring path becomes: `ScoringAgent` (wrapper, no status updates) → `SingleShotController` (scoring prompt config).

- **Pro:** No changes to BaseAgent.
- **Pro:** ScoringAgent's different lifecycle is explicit and encapsulated.
- **Pro:** Small, focused — 52 lines, manageable duplication.

**Rejected alternatives:** (A) Merge with `SkipExecutionStatusUpdates` flag — leaky abstraction, config shouldn't know about DB update internals; (C) Type-check in BaseAgent — makes BaseAgent aware of specific types.

---

## Q3: How should backend selection work after removing compound strategies? — DECIDED

> **Decision:** Option D — Remove `iteration_strategy` entirely. Introduce `llm_backend` for SDK path selection. Controller selection comes from `type`.

The old `iteration_strategy` conflated two concerns: (1) backend/SDK path selection (`native-thinking` vs `langchain`) and (2) agent behavior (synthesis, scoring as compound values). With Q1 deciding that `type` determines the controller pattern, `iteration_strategy` is fully redundant — its two jobs split cleanly into `type` (controller) and a new `llm_backend` field (SDK path).

### Option D: Replace `iteration_strategy` with `llm_backend`

```yaml
# Before (6 compound values encoding 3 concerns)
iteration_strategy: synthesis-native-thinking

# After (each field does one thing)
type: synthesis                 # → controller selection (SingleShotController)
llm_backend: native-gemini      # → SDK path (Google native SDK)
```

**New `llm_backend` field:**

```go
type LLMBackend string
const (
    LLMBackendNativeGemini LLMBackend = "native-gemini"
    LLMBackendLangChain    LLMBackend = "langchain"
)
```

- Inherits through the same hierarchy as `iteration_strategy` did: global defaults → chain → stage → agent
- `ResolveBackend` simplifies to a direct mapping from `llm_backend` (no compound parsing)
- Default: `langchain` (same as today when no strategy specified)

**Controller selection from `type` (no config field needed):**

| `type` | Controller |
|--------|-----------|
| default (investigation) | IteratingController |
| `synthesis` | SingleShotController (synthesis config) |
| `scoring` | SingleShotController (scoring config) |
| `orchestrator` | IteratingController |

- **Pro:** Each field does exactly one thing — `type` = behavior, `llm_backend` = SDK path.
- **Pro:** `iteration_strategy` removed entirely — no more misleading name.
- **Pro:** `llm_backend` is honest about what it controls (which SDK path the Python llm-service uses).
- **Pro:** No redundancy — controller selection is implicit from `type`, not a second config field.
- **Pro:** Explicit control preserved — users can force `langchain` for Google models when needed.
- **Con:** Rename from `iteration_strategy` to `llm_backend` touches all config inheritance points.

**Rejected alternatives:** (A) Auto-derive backend from provider type — loses ability to force LangChain for Google models; (B) Keep `iteration_strategy` with 2 values — misleading name, redundant with `type` for controller selection; (C) Auto-derive with manual override — more complex resolution logic than needed.

---

## Q4: How to handle backward compatibility for compound strategies? — DECIDED

> **Decision:** Option C — Break old configs immediately. Remove `iteration_strategy`, require `type` + `llm_backend`.

Existing configs use `iteration_strategy: synthesis`, `iteration_strategy: scoring-native-thinking`, `iteration_strategy: native-thinking`, etc. With Q3 removing `iteration_strategy` entirely in favor of `type` + `llm_backend`, the question is whether to provide a migration path.

Clean cut: remove `iteration_strategy` from config parsing. Config validation rejects unknown fields. Users update their configs to use `type` and `llm_backend` before upgrading.

- **Pro:** No migration code to write or maintain.
- **Pro:** No deprecated code paths lingering in the codebase.
- **Pro:** Forces configs to the clean state immediately.
- **Con:** Breaking change — all existing deployments need config updates simultaneously.

**Rejected alternatives:** (A) Silent migration — masks issues, user doesn't know they should update; (B) Migrate + deprecation warnings — extra code to maintain for a temporary transition period.

---

## Q5: Should this refactor happen before, with, or after the orchestrator? — DECIDED

> **Decision:** Option A — Refactor first, then orchestrator.

Do the full type system cleanup first:
1. Add `type` field, `description` field, `llm_backend` field
2. Migrate synthesis/scoring to type-based selection
3. Remove `iteration_strategy`, replace with `llm_backend`
4. Restructure controllers (IteratingController + SingleShotController)
5. Then build orchestrator on the clean foundation

- **Pro:** Orchestrator builds on a clean type system — no workarounds.
- **Pro:** Each change is focused and reviewable.
- **Pro:** Independently valuable — cleans up the existing system regardless of orchestrator.
- **Con:** Delays orchestrator work slightly.

**Rejected alternatives:** (B) Orchestrator first — temporary inconsistency with two config patterns coexisting; (C) Both together — largest scope, hardest to review.

---

## Summary

| # | Question | Decision / Recommendation |
|---|----------|--------------------------|
| Q1 | Controller restructuring? | **DECIDED:** Two structural controllers (IteratingController + SingleShotController) with parameterized config |
| Q2 | Merge ScoringAgent into BaseAgent? | **DECIDED:** Keep separate; uses SingleShotController from Q1 |
| Q3 | Backend selection? | **DECIDED:** Remove `iteration_strategy`, introduce `llm_backend` (`native-gemini`, `langchain`). Controller from `type`. |
| Q4 | Backward compatibility? | **DECIDED:** Break old configs — clean cut, no migration code |
| Q5 | Sequencing vs orchestrator? | **DECIDED:** Refactor first, then orchestrator |

---

## Decision Log

| Date | Question | Decision | Rationale |
|------|----------|----------|-----------|
| 2026-02-23 | Q1: Controller restructuring | Two structural controllers (IteratingController + SingleShotController) with parameterized config. `Controller` interface as escape hatch for custom patterns. | The two controllers match the two real control flow patterns (multi-turn loop vs single-shot). Shared code is already in package-level functions. Parameterized `SingleShotConfig` avoids type-awareness while serving both synthesis (thinking fallback) and scoring (no fallback). Orchestrator uses IteratingController unchanged. |
| 2026-02-23 | Q2: ScoringAgent merge | Keep `ScoringAgent` separate. Uses `SingleShotController` with scoring prompt config. | Small (52 lines), focused, explicitly encapsulates different lifecycle (no execution status updates). Agent wrapper and controller are separate layers — wrapper manages lifecycle, controller manages LLM call pattern. |
| 2026-02-23 | Q3: Backend selection | Remove `iteration_strategy` entirely. New `llm_backend` field (`native-gemini`, `langchain`) for SDK path. `type` determines controller (iterating vs single-shot). | `iteration_strategy` conflated backend selection and agent behavior. With `type` handling controller selection (Q1), the only remaining job is SDK path — which deserves an honest name. Each field does exactly one thing. |
| 2026-02-23 | Q4: Backward compatibility | Break old configs immediately. No migration code. | Clean cut — no deprecated code paths to maintain. Users update configs to `type` + `llm_backend` before upgrading. |
| 2026-02-23 | Q5: Sequencing | Refactor first, then orchestrator. | Independently valuable cleanup. Orchestrator builds on a clean foundation. Each step is a small, reviewable PR. |
