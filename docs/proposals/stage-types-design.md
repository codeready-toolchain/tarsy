# Stage Types — Design Document

**Status:** Final
**Related:** [Design questions](stage-types-questions.md) | [Session scoring design](session-scoring-design.md) (this doc is Phase 1)

## Overview

TARSy alert sessions are composed of stages, each representing a set of LLM interactions with some outcome. Today there are already multiple kinds of stages — investigation, synthesis, chat — but the schema has no explicit field to distinguish them. The kinds are identified through fragile heuristics: nullable FK checks (`chat_id IS NOT NULL`) and string suffix matching (`" - Synthesis"`).

This proposal adds a `stage_type` enum field to the Stage schema so every stage carries an explicit type. This removes heuristic-based classification from the codebase and enables direct filtering, simpler context building, and richer API/WS responses.

Additionally, executive summary generation is refactored from a special-cased direct LLM call into a typed stage, unifying all LLM-driven activities under the stage model.

**In scope:**

- PR 1: Data model, API, WS event changes, creation-path wiring, chat context builder simplification (additive, no behavior changes).
- PR 2: Refactor executive summary into a typed stage (`exec_summary`). Update context-building functions to filter by stage type.

**Out of scope:** UI changes, scoring pipeline (Phase 2 of the [session scoring design](session-scoring-design.md)).

**Follow-up (separate proposal):** Synthesis stages reference their "parent" stages by name (e.g. "my stage - Synthesis" is related to "my stage" stage). This is fragile and would benefit from a `referenced_stage_id` FK. See [referenced-stage-id proposal](referenced-stage-id-design.md). This proposal keeps name-based pairing.

## Design Principles

1. **Explicit over implicit.** Replace heuristic classification with a declarative field.
2. **Single source of truth.** The `stage_type` column is the canonical way to identify stage kind — no more suffix checks or FK inference.
3. **Consistent with existing patterns.** Follow the same ent enum pattern used by `stage.Status`, `stage.ParallelType`, and `stage.SuccessPolicy`.
4. **Composable context filtering.** Stage types enable query-level filtering for building different contexts (investigation, chat, scoring).

## Architecture

### Stage Type Enum

Defined as an ent schema enum field. No separate Go type — use ent-generated constants (`stage.StageTypeInvestigation`, `stage.StageTypeSynthesis`, etc.), consistent with how `stage.Status`, `stage.ParallelType`, and `stage.SuccessPolicy` work throughout the codebase.

Five values:

| Value | Description | Created by | Wired in |
|-------|-------------|------------|----------|
| `investigation` | From chain config stage | `executor.go` (default) | PR 1 |
| `synthesis` | Auto-generated after multi-agent investigation | `executor_synthesis.go` | PR 1 |
| `chat` | User follow-up chat message | `chat_executor.go` | PR 1 |
| `exec_summary` | Executive summary of the investigation | `executor.go` (refactored) | PR 2 |
| `scoring` | Quality evaluation | `ScoringExecutor` (Phase 2) | Reserved |

All five values are defined in the ent enum from PR 1, but `exec_summary` and `scoring` have no creation path until their respective phases. Stage creation is internal-only (`StageService.CreateStage` has no API endpoint) — only executor code calls it, so no service-level whitelist is needed; the ent enum provides schema-level validation, and the absence of a code path is sufficient protection.

Stage types enable composable context filtering:

| Need | Stage types included |
|------|---------------------|
| Build next-stage context | `investigation`, `synthesis` |
| Build chat context | `investigation`, `synthesis`, `chat` |
| Build scoring context | `investigation`, `synthesis`, `exec_summary` |
| Main timeline view | `investigation`, `synthesis` |
| Full session view | all |

### DB Schema Change

Add one field to `ent/schema/stage.go`:

```go
field.Enum("stage_type").
    Values("investigation", "synthesis", "chat", "exec_summary", "scoring").
    Default("investigation").
    Comment("Kind of stage: investigation (from chain), synthesis (auto-generated), chat (user message), exec_summary (executive summary), scoring (quality evaluation)"),
```

The field is **required with a default** (`investigation`), not optional/nillable. Every stage has exactly one type. The default covers the most common case (investigation) and means the investigation creation path needs no code change.

No index on `stage_type` — all current queries load stages per-session (typically 1-5 stages), filtering in-memory is trivial. An index can be added later if a cross-session filtering use case arises.

### CreateStageRequest Change

```go
type CreateStageRequest struct {
    // ... existing fields ...
    StageType string `json:"stage_type,omitempty"` // defaults to "investigation" if empty
}
```

### StageService.CreateStage Change

```go
stageType := stage.StageTypeInvestigation // default
if req.StageType != "" {
    stageType = stage.StageType(req.StageType)
    if err := stage.StageTypeValidator(stageType); err != nil {
        return nil, NewValidationError("stage_type", fmt.Sprintf("invalid: %q", req.StageType))
    }
}

builder := s.client.Stage.Create().
    // ... existing fields ...
    SetStageType(stageType)
```

Explicit service-level validation — consistent with the existing `ParallelType`/`SuccessPolicy` validation pattern in `CreateStage`. Ent generates `StageTypeValidator` (a switch over all enum constants, same shape as `ParallelTypeValidator`/`SuccessPolicyValidator`), so no hand-maintained map is needed.

### Creation Path Changes

**Investigation** (`executor.go:330`) — no change needed. Omitting `StageType` falls through to the `"investigation"` default.

**Synthesis** (`executor_synthesis.go:40`) — set `StageType: "synthesis"`.

**Chat** (`chat_executor.go:172`) — set `StageType: "chat"`.

**Exec summary** — see "Executive Summary Refactoring" section below (PR 2).

**Scoring** — not wired up in this proposal. Reserved for Phase 2 (`ScoringExecutor`).

### API Response Changes

**StageOverview** (`pkg/models/session.go`) — add `StageType string`:

```go
type StageOverview struct {
    // ... existing fields ...
    StageType string `json:"stage_type"`
}
```

Populated directly from `stg.StageType` in `GetSessionDetail`.

**TraceStageGroup** (`pkg/models/interaction.go`) — add `StageType string`:

```go
type TraceStageGroup struct {
    StageID   string `json:"stage_id"`
    StageName string `json:"stage_name"`
    StageType string `json:"stage_type"`
    // ...
}
```

Populated from the DB field in `handler_trace.go`.

### WS Event Payload Change

Add `stage_type` to `StageStatusPayload` (`pkg/events/payloads.go`):

```go
type StageStatusPayload struct {
    BasePayload
    StageID    string `json:"stage_id,omitempty"`
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"`
    StageType  string `json:"stage_type"`
    Status     string `json:"status"`
}
```

The `publishStageStatus` helper (`pkg/queue/executor_helpers.go`) gains a `stageType` parameter. All call sites are updated to pass the type:

- `executor.go` — `"investigation"` for investigation stages, `"synthesis"` for synthesis terminal status
- `executor_synthesis.go` — `"synthesis"`
- `chat_executor.go` — `"chat"`
- After PR 2: `"exec_summary"` for executive summary stage

### Chat Context Builder Simplification

With an explicit type field, the heuristic-based filtering in `chat_executor.go:452-485` becomes direct:

```go
for _, stg := range stages {
    switch stg.StageType {
    case stage.StageTypeSynthesis:
        // Already paired via synthResults map — skip
        continue
    case stage.StageTypeChat:
        // Previous Q&A
        isCurrentChat := stg.ChatUserMessageID != nil && *stg.ChatUserMessageID == input.Message.ID
        if !isCurrentChat {
            if qa := e.buildChatQA(ctx, stg); qa.Question != "" {
                previousChats = append(previousChats, qa)
            }
        }
        continue
    default:
        // Investigation stage — build per-agent timelines
    }
}
```

No more `strings.HasSuffix` or `chat_id != nil` checks for stage identification.

The synthesis stage _pairing_ logic (finding which investigation stage a synthesis belongs to) still uses name-based backward scanning. This is correct — the synthesis stage name is always derived from the parent in `executor_synthesis.go`. A structural `referenced_stage_id` FK could replace this in a future proposal but is orthogonal to stage types.

### Executive Summary Refactoring (PR 2)

The current executive summary implementation (`executor_synthesis.go:164-328`) is a special case:

- Direct LLM call — not through the agent/controller framework
- Creates its own timeline event with sentinel sequence number `999_999` (no `stage_id`, no `execution_id`)
- Creates its own LLM interaction with `interaction_type: "executive_summary"`
- Resolves LLM provider inline (chain `executive_summary_provider` → chain `llm_provider` → defaults)
- Publishes progress manually (not through stage infrastructure)
- Stores result on session fields (`executive_summary`, `executive_summary_error`)

Refactoring it into a typed stage:

1. **Create a Stage record** (type: `exec_summary`, name: "Executive Summary") and an AgentExecution record, using the standard stage infrastructure. The `dbStageIndex` continues incrementing naturally after the last investigation/synthesis stage.

2. **Run through the agent framework.** The exec summary is a single LLM call with no tools — reuse `SingleShotController` (see decision rationale below). Add a `NewExecSummaryController(pb)` factory function that wires a `SingleShotConfig` with a `BuildMessages` adapter, `ThinkingFallback: false`, and `InteractionLabel: "exec_summary"`. The controller receives `finalAnalysis` as `prevStageContext` and returns the summary as `FinalAnalysis` in the `ExecutionResult`.

   **Why `SingleShotController` is the right fit (not a new controller):**

   The current `generateExecutiveSummary()` does: (1) build a system + user message pair from static templates, (2) make a single LLM call, (3) return the text. This maps directly to `SingleShotController.Run()` which does the same thing plus standard message storage, timeline events, and interaction recording — exactly the infrastructure we want exec summary to gain.

   Key differences from synthesis that are handled by `SingleShotConfig`:
   - **Prompt building:** Exec summary uses `BuildExecutiveSummarySystemPrompt()` + `BuildExecutiveSummaryUserPrompt(prevStageContext)` — two simple static-template functions, unlike synthesis which uses `composeSynthesisInstructions(execCtx)` for config-aware instruction assembly. The `BuildMessages` function in the config adapts the existing prompt methods to the `func(*ExecutionContext, string) []ConversationMessage` signature with a trivial closure.
   - **No thinking fallback:** Exec summary doesn't use `ThinkingFallback` (set to `false`), unlike synthesis (set to `true`).
   - **Interaction label:** `"exec_summary"` instead of `"synthesis"`.

   A dedicated `ExecSummaryController` would only be warranted if future requirements add: multi-turn conversation (like `ScoringController`), custom retry/extraction logic, lifecycle hooks not supported by `SingleShotConfig`, or a fundamentally different context schema (e.g., needing `ExecutionContext` fields beyond `prevStageContext`). None of these apply today.

3. **Remove special-casing:**
   - Remove the `executiveSummarySeqNum = 999_999` sentinel. The stage infrastructure handles timeline ordering via `stage_index`.
   - Stop _creating_ new interactions with `interaction_type: "executive_summary"`. New exec summary interactions use the standard agent execution flow (type: `"llm"`). **Retain the `executive_summary` enum value** in the DB schema and Go constants for backward compatibility — existing rows in the database reference it.
   - Similarly, **retain the `timelineevent.EventTypeExecutiveSummary`** enum value for backward compatibility with existing timeline event records. New exec summary stages use standard stage-based timeline events instead.
   - Remove the manual progress publishing (`publishSessionProgress`, `publishExecutionProgressFromExecutor`). Stage creation and status publishing handle this.

4. **Keep the session-level `executive_summary` field** as a denormalized copy for quick access (session list, Slack notifications). After the exec summary stage completes, the executor extracts the summary from the stage result and sets it on `ExecutionResult.ExecutiveSummary`, which the worker persists to the session record — same as today.

5. **Keep `ExecutionResult` fields** — `ExecutiveSummary` and `ExecutiveSummaryError` stay for the worker to persist denormalized values.

6. **Update `countExpectedStages()`** — semantics are unchanged since exec summary was already counted as +1. The function still counts: config stages + synthesis stages + 1 (exec summary). The only difference is that the exec summary stage now actually exists as a Stage record.

7. **Preserve `executive_summary_provider` config** — the chain-level `executive_summary_provider` field continues to work. The exec summary stage's agent config resolves this provider through the existing hierarchy.

8. **Fail-open behavior preserved** — if the exec summary stage fails, the session still completes. The executor handles this the same way: log the error, populate `ExecutiveSummaryError`, continue to return `StatusCompleted`.

### Context-Building Function Updates (PR 2)

Update context-building functions to filter by stage type instead of treating all stages equally:

- **`buildStageContext()`** — filter to `investigation` + `synthesis` stages only. This is the existing behavior (it already only processes completed investigation/synthesis stages) but now explicit via stage type.
- **`extractFinalAnalysis()`** — filter to `investigation` + `synthesis` stages for the final analysis extraction. Same existing behavior, made explicit.
- **`buildChatContext()`** — already simplified in PR 1 using stage type switch.

## DB Schema Impact

One new column on the `stages` table:

| Column | Type | Nullable | Default | Index |
|--------|------|----------|---------|-------|
| `stage_type` | `enum('investigation','synthesis','chat','exec_summary','scoring')` | NOT NULL | `investigation` | None |

### Migration

The column is added with `DEFAULT 'investigation'`, so existing rows are backfilled automatically. Existing synthesis and chat stages need explicit backfill, embedded in the same ent migration:

```sql
-- Backfill synthesis stages (identified by name suffix)
UPDATE stages SET stage_type = 'synthesis' WHERE stage_name LIKE '% - Synthesis';

-- Backfill chat stages (identified by non-null chat_id)
UPDATE stages SET stage_type = 'chat' WHERE chat_id IS NOT NULL;

-- Investigation stages already have the correct default
-- No exec_summary stages exist yet (they only appear after PR 2)
-- No scoring stages exist yet (they only appear after Phase 2)
```

This migration is safe and idempotent. The heuristics match exactly the stages they need to update.

## Impact Analysis

### Files Affected (PR 1 — Additive)

| Area | Files | Change scope |
|------|-------|-------------|
| DB schema | `ent/schema/stage.go` | Add `stage_type` enum field |
| Generated code | `ent/` (codegen) | Regenerate ent code |
| Stage model | `pkg/models/stage.go` | Add `StageType` to `CreateStageRequest` |
| Stage service | `pkg/services/stage_service.go` | Wire `StageType` in `CreateStage` |
| Investigation executor | `pkg/queue/executor.go` | Update `publishStageStatus` calls to pass type |
| Synthesis executor | `pkg/queue/executor_synthesis.go` | Set `StageType: "synthesis"` in `CreateStageRequest`; update `publishStageStatus` call |
| Chat executor | `pkg/queue/chat_executor.go` | Set `StageType: "chat"` in `CreateStageRequest`; simplify `buildChatContext`; update `publishStageStatus` calls |
| Event helpers | `pkg/queue/executor_helpers.go` | Add `stageType` parameter to `publishStageStatus` |
| API DTOs | `pkg/models/session.go` | Add `StageType` to `StageOverview` |
| API DTOs | `pkg/models/interaction.go` | Add `StageType` to `TraceStageGroup` |
| API handler | `pkg/api/handler_trace.go` | Populate `StageType` from DB field |
| Session service | `pkg/services/session_service.go` | Populate `StageType` in `StageOverview` |
| WS payloads | `pkg/events/payloads.go` | Add `StageType` to `StageStatusPayload` |
| DB migration | Generated ent migration + backfill | Add column + backfill SQL |
| Tests | Various `_test.go` files | Update test cases to include `StageType` |

### Files Affected (PR 2 — Exec Summary Refactoring)

| Area | Files | Change scope |
|------|-------|-------------|
| Executor | `pkg/queue/executor.go` | Replace `generateExecutiveSummary()` call with exec summary stage creation + agent execution |
| Exec summary | `pkg/queue/executor_synthesis.go` | Remove `generateExecutiveSummary()`, `executiveSummarySeqNum` |
| Agent config | `pkg/agent/config_resolver.go` | Add `ResolveExecSummaryConfig()` or reuse existing provider resolution |
| Controller | `pkg/agent/controller/` | New `ExecSummaryController` or reuse `SingleShotController` |
| Agent factory | `pkg/agent/factory.go` | Wire exec summary agent type (if new) |
| Config enums | `pkg/config/enums.go` | Add `AgentTypeExecSummary` (if new agent type needed) |
| Context builders | `pkg/queue/executor.go` | Add explicit stage-type filter guards to `buildStageContext()`, `extractFinalAnalysis()` (safety — behavior unchanged, they already operate on the correct slice) |
| Event helpers | `pkg/queue/executor_helpers.go` | Update `publishStageStatus` call for exec summary |
| Trace handler | `pkg/api/handler_trace.go` | Exec summary interactions move from session-level `SessionInteractions` to a `TraceStageGroup` (they now have a `stage_id`) |
| Trace DTO | `pkg/models/interaction.go` | Update `SessionInteractions` doc — exec summary is no longer session-level |
| WS payloads | `pkg/events/payloads.go` | Update `InteractionCreatedPayload.StageID` comment — exec summary now has a stage |
| Tests | Various `_test.go` files | Update exec summary tests, add stage-type filtering tests |

### Risk

**PR 1:**
- **Low**: Additive field with a default value. No existing behavior changes.
- **Migration**: Backfill is a safe `UPDATE` on existing rows.
- **API compatibility**: New JSON field is additive — existing clients ignore unknown fields.

**PR 2:**
- **Medium**: Behavioral change — exec summary moves from direct LLM call to stage-based execution. Same outcome, different path.
- **Regression risk**: Exec summary must still produce the same output, still fail-open, still populate session-level field.
- **Mitigation**: Comprehensive tests comparing before/after behavior.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Where to define `StageType` | Ent-generated constants only | Consistent with `stage.Status`, `stage.ParallelType`, `stage.SuccessPolicy`. No duplication. |
| Q2 | DB index on `stage_type` | No index | Low cardinality, no cross-session queries. Can add later if needed. |
| Q3 | `stage_type` in `StageStatusPayload` | Yes | WS payload should be self-describing. Mechanical change to `publishStageStatus`. |
| Q4 | Synthesis pairing in `buildChatContext` | Replace identification only, keep name-based pairing | Name convention is reliable. `referenced_stage_id` FK is a [separate proposal](referenced-stage-id-design.md). |
| Q5 | Backfill migration | Embed in ent migration | Single step, standard pattern for schema + data changes. |
| Q6 | PR granularity | Two PRs | PR 1 is additive (~15 files), PR 2 is behavioral (exec summary refactoring). Clean separation of risk. |

## Implementation Plan

### PR 1: Stage Type Field (additive, no behavior change)

1. **Schema:** Add `stage_type` enum field to `ent/schema/stage.go` with 5 values (`investigation`, `synthesis`, `chat`, `exec_summary`, `scoring`). Regenerate ent code. Add backfill SQL to the migration.
2. **Service:** Add `StageType` to `CreateStageRequest`. Wire in `StageService.CreateStage` with `"investigation"` default.
3. **Creation paths:** Set `StageType: "synthesis"` in `executor_synthesis.go`. Set `StageType: "chat"` in `chat_executor.go`. Investigation path needs no change (default).
4. **API:** Add `StageType` to `StageOverview` and `TraceStageGroup`. Populate in `GetSessionDetail` and `handler_trace.go`.
5. **WS:** Add `StageType` to `StageStatusPayload`. Add `stageType` parameter to `publishStageStatus`. Update all call sites.
6. **Chat context:** Replace `strings.HasSuffix` and `chat_id != nil` identification checks with `stg.StageType` switch in `buildChatContext`.
7. **Tests:** Update existing tests, add targeted tests for type assignment and backfill.

### PR 2: Executive Summary as Typed Stage (behavioral change)

1. **Agent/controller:** Add `NewExecSummaryController(pb PromptBuilder) *SingleShotController` to `single_shot.go` (pattern: identical to `NewSynthesisController`). It returns `NewSingleShotController(SingleShotConfig{...})` with:
   - `BuildMessages`: closure that calls `pb.BuildExecutiveSummarySystemPrompt()` and `pb.BuildExecutiveSummaryUserPrompt(prevStageContext)`, returning a `[]ConversationMessage` with system + user roles. The `execCtx` parameter is unused (exec summary prompts are context-independent static templates).
   - `ThinkingFallback: false` — exec summary expects a direct text response, not thinking output.
   - `InteractionLabel: "exec_summary"`.
   - Add `config.AgentTypeExecSummary` constant and wire it in `controller/factory.go`'s switch: `case config.AgentTypeExecSummary: return NewExecSummaryController(execCtx.PromptBuilder), nil`.
2. **Config resolution:** Add `ResolveExecSummaryConfig()` in `config_resolver.go`. It resolves the LLM provider using the existing chain hierarchy: `chain.ExecutiveSummaryProvider` → `chain.LLMProvider` → `defaults.LLMProvider`. It returns the resolved `*config.LLMProviderConfig` and backend string, which the executor uses to populate the agent's `ExecutionContext.Config`. This mirrors the inline resolution in today's `generateExecutiveSummary()` (lines 188–210 of `executor_synthesis.go`) but extracted into a reusable function.
3. **Executor refactoring:** Replace `generateExecutiveSummary()` with:
   - Create Stage record (type: `exec_summary`, name: "Executive Summary")
   - Create AgentExecution record
   - Run agent through standard framework
   - Extract summary from agent result
   - Set `ExecutionResult.ExecutiveSummary` from stage result
4. **Remove special-casing:** Remove `executiveSummarySeqNum`. Stop creating new `executive_summary` interaction types and `EventTypeExecutiveSummary` timeline events (retain enum values for backward compatibility). Remove manual progress publishing.
5. **Context builders:** Add explicit stage-type guard checks to `buildStageContext()` and `extractFinalAnalysis()` — filter to `investigation` + `synthesis` only. This is a safety measure; these functions already operate on the correct slice, but explicit type checks prevent accidental inclusion of exec summary/scoring stages if the calling code changes.
6. **Trace handler:** Update `handler_trace.go` to handle exec summary interactions appearing inside a stage group instead of session-level `SessionInteractions`. Update `SessionInteractions` doc in `interaction.go`.
7. **Publish stage status:** Add `"exec_summary"` to the `publishStageStatus` call for the exec summary stage.
8. **Tests:** Verify exec summary still produces same output, fails-open, populates session field. Add stage-type filtering tests for context builders. Verify trace API returns exec summary within its stage group.
