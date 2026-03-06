# Automated Actions — Design Document

**Status:** Draft — pending decisions from [automated-actions-design-questions.md](automated-actions-design-questions.md)
**Based on:** [automated-actions-sketch.md](automated-actions-sketch.md), [automated-actions-questions.md](automated-actions-questions.md) (all sketch-level decisions resolved)

## Overview

Add `action` as a new agent type and stage type to TARSy, enabling automated remediation actions based on investigation findings. This is an ergonomic and safety layer on top of existing capabilities — not a new engine. The action agent type provides auto-injected safety prompts; the action stage type provides DB auditability, distinct dashboard rendering, and executor pre-conditions.

See the [sketch document](automated-actions-sketch.md) for full rationale and design decisions.

## Design Principles

1. **Minimal surface area** — reuse existing patterns (IteratingController, `StageAgentConfig.Type` override, prompt builder tiers)
2. **Safety by default** — auto-injected prompt layer can't be accidentally omitted
3. **Backwards compatible** — no changes to existing agent types, stage types, or executor behavior
4. **Deterministic stage type** — derived from agent types, no runtime ambiguity
5. **Separation of concerns** — agent type owns prompt/controller, stage type owns executor/DB/UI

## Architecture

### Component Changes

#### 1. Config Enums — `pkg/config/enums.go`

Add `AgentTypeAction`:

```go
AgentTypeAction AgentType = "action"
```

Update `IsValid()` to include it. No new config struct fields — `StageAgentConfig.Type` already supports arbitrary `AgentType` values.

#### 2. DB Schema — `ent/schema/stage.go`

Add `"action"` to the `stage_type` enum:

```go
field.Enum("stage_type").
    Values("investigation", "synthesis", "chat", "exec_summary", "scoring", "action").
```

Run `go generate ./ent/...` to regenerate ent code, validator, and migration.

#### 3. Controller Factory — `pkg/agent/controller/factory.go`

Map `AgentTypeAction` to `IteratingController` (same as default and orchestrator):

```go
case config.AgentTypeAction:
    return NewIteratingController(), nil
```

#### 4. Prompt Builder — `pkg/agent/prompt/`

New file `action.go` with:
- `actionBehavioralInstructions` constant — the safety preamble from Q8
- `actionTaskFocus` constant — task-level focus for action agents
- `buildActionMessages()` method — mirrors `buildOrchestratorMessages` pattern:
  1. `ComposeInstructions(execCtx)` — standard Tier 1–3 (general SRE, MCP, custom)
  2. Append `actionBehavioralInstructions`
  3. Append `actionTaskFocus`
  4. Standard `buildInvestigationUserMessage` for user content

Update `builder.go` — add branch in `BuildFunctionCallingMessages`:

```go
if execCtx.Config.Type == config.AgentTypeAction {
    return b.buildActionMessages(execCtx, prevStageContext)
}
```

This branch goes before the chat/investigation dispatch (after orchestrator, before sub-agent check), matching the existing pattern.

#### 5. Executor — `pkg/queue/executor.go`

**Stage type derivation** in `executeStage()`:

Currently, `executeStage` always creates stages with `StageTypeInvestigation`. Change this to derive the stage type from the resolved agent configs.

> **Open question:** Where exactly should this derivation happen? — see [questions document](automated-actions-design-questions.md), Q1.

**Context flow** in `executor_helpers.go`:

`buildStageContext()` and `extractFinalAnalysis()` currently filter to only investigation and synthesis stages. Action stages need to be included so the action agent's amended report flows to the exec summary.

> **Open question:** Should action stages contribute to the context flow? — see [questions document](automated-actions-design-questions.md), Q2.

#### 6. Config Validation — `pkg/config/validator.go`

`AgentType.IsValid()` already covers validation in both `validateAgents()` and `validateStage()`. Once `AgentTypeAction` is added to the enum, it passes validation automatically.

> **Open question:** Should we validate action stage ordering or warn about mixed stages? — see [questions document](automated-actions-design-questions.md), Q3 and Q4.

#### 7. Frontend — `web/dashboard/`

- `src/constants/eventTypes.ts` — add `ACTION: 'action'` to `STAGE_TYPE`
- `src/types/session.ts` — no changes (StageOverview already has `stage_type: string`)
- Timeline components — distinct rendering for action stages

> **Open question:** What's the frontend scope for v1? — see [questions document](automated-actions-design-questions.md), Q5.

### Data Flow

```
YAML config: agents[].type: "action"
  ↓
Config validation: AgentTypeAction.IsValid() → true
  ↓
Executor: ResolveAgentConfig() → resolvedConfig.Type = AgentTypeAction
  ↓
Executor: all resolved agents are action? → StageType = action (else investigation)
  ↓
Prompt builder: BuildFunctionCallingMessages → buildActionMessages
  → ComposeInstructions (Tier 1–3) + actionBehavioralInstructions + actionTaskFocus
  ↓
Controller: IteratingController (multi-turn with MCP tools)
  ↓
DB: stage.stage_type = "action"
  ↓
API: StageOverview.stage_type = "action", StageStatusPayload.stage_type = "action"
  ↓
Frontend: distinct icon/color/label in timeline
```

### Executor Flow (Updated)

```
1. Resolve chain config
2. For each config stage:
   a. Resolve agent configs
   b. Derive stage type (all action → "action", else "investigation")
   c. Create DB stage with derived type
   d. Run agents (IteratingController for action, same as default)
   e. If multiple agents → run synthesis (synthesis stage)
   f. Update chain context: prevContext = buildStageContext(completedStages)
3. Extract final analysis (includes action stages)
4. Run exec summary (fail-open) — summarizes the action-amended report
5. Return result
```

## Core Concepts

### Action Agent Type (`config.AgentTypeAction`)

**Controls:** controller selection and prompt injection.

- **Controller:** `IteratingController` — same as default investigation agents. Multi-turn, MCP tools, function calling.
- **Prompt:** `buildActionMessages` — standard Tier 1–3 instructions + auto-injected safety preamble + action task focus. This mirrors how `buildOrchestratorMessages` appends orchestration behavioral instructions.

The safety preamble covers:
- Require hard evidence before acting
- Focus on evaluating upstream analysis, avoid re-investigation
- If evidence is ambiguous, report but do NOT act
- Explain reasoning before executing action tools
- Prefer inaction over incorrect action
- Preserve the investigation report, amend with actions section

### Action Stage Type (`stage.StageTypeAction`)

**Controls:** executor behavior, DB schema, dashboard rendering, queryability.

- **Derived from agent types:** if all agents in a stage have `type: action` (after resolution), the stage gets `stage_type: action`. Otherwise it stays `investigation`.
- **DB queryability:** `WHERE stage_type = 'action'` finds all action evaluation stages.
- **Dashboard:** distinct rendering in timeline, optional badge on session list.
- **Context flow:** action stages contribute to `buildStageContext()` and `extractFinalAnalysis()` so the exec summary sees the complete picture.

### Relationship Between Types

```
Agent type: action     →  prompt safety layer + IteratingController
                            (per-agent concern, each action agent gets this)

Stage type: action     →  DB audit + dashboard + context flow
                            (per-stage concern, only when ALL agents are action)
```

An action agent in a mixed stage still gets the safety prompt. The stage just doesn't get action-type benefits.

## Implementation Plan

### Phase 1: Core Types

**Goal:** Add the new types without changing any behavior.

**Files:**
- `pkg/config/enums.go` — add `AgentTypeAction`, update `IsValid()`
- `ent/schema/stage.go` — add `"action"` to stage_type enum
- `pkg/agent/controller/factory.go` — add `AgentTypeAction` case → `IteratingController`
- Run `go generate ./ent/...`

**Tests:**
- `pkg/config/enums_test.go` (if exists) — test `IsValid()` with new type
- `pkg/agent/controller/factory_test.go` — test `AgentTypeAction` → IteratingController

### Phase 2: Prompt Builder

**Goal:** Auto-inject safety prompt for action agents.

**Files:**
- `pkg/agent/prompt/action.go` (new) — `actionBehavioralInstructions`, `actionTaskFocus`, `buildActionMessages`
- `pkg/agent/prompt/builder.go` — add `AgentTypeAction` branch in `BuildFunctionCallingMessages`

**Tests:**
- `pkg/agent/prompt/action_test.go` (new) — verify message structure, safety preamble present, Tier 1–3 composed
- `pkg/agent/prompt/builder_test.go` — verify dispatch to `buildActionMessages`

### Phase 3: Executor Logic

**Goal:** Derive stage type from agent types, include action stages in context flow.

**Files:**
- `pkg/queue/executor.go` — stage type derivation in `executeStage()`
- `pkg/queue/executor_helpers.go` — include `StageTypeAction` in `buildStageContext()` and `extractFinalAnalysis()`

**Tests:**
- `pkg/queue/executor_test.go` — test `buildStageContext` and `extractFinalAnalysis` with action stages
- `pkg/queue/executor_integration_test.go` — end-to-end test: chain with investigation + action stage

### Phase 4: Config Validation

**Goal:** Warn on edge cases, validate ordering if decided.

**Files:**
- `pkg/config/validator.go` — warnings/validation per decisions

**Tests:**
- `pkg/config/validator_test.go` — test validation with action stages

### Phase 5: Frontend

**Goal:** Distinct rendering for action stages.

**Files:**
- `web/dashboard/src/constants/eventTypes.ts` — add `ACTION` to `STAGE_TYPE`
- Timeline components — conditional styling for action stage type

### Phase 6: Synthesis Prompt Review

**Goal:** Improve upstream analysis quality for action agents.

**Files:**
- `pkg/config/builtin.go` — review and update `SynthesisAgent.CustomInstructions`
- Ensure evidence references, classification, and confidence are emphasized

## Open Questions

Summary of implementation decisions needed:

| # | Question | Section |
|---|----------|---------|
| Q1 | Where should stage type derivation happen? | Executor |
| Q2 | Should action stages contribute to context flow? | Executor Helpers |
| Q3 | Should action stage ordering be validated? | Config Validation |
| Q4 | Should mixed action/non-action stages produce a warning? | Config Validation |
| Q5 | What frontend changes are needed for v1? | Frontend |
