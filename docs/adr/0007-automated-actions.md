# ADR-0007: Automated Actions

**Status:** Implemented
**Date:** 2026-03-06

## Overview

Add `action` as a new agent type and stage type to TARSy, enabling automated remediation actions based on investigation findings. This is an ergonomic and safety layer on top of existing capabilities — not a new engine. The action agent type provides auto-injected safety prompts; the action stage type provides DB auditability, distinct dashboard rendering, and context flow integration.

## Design Principles

1. **Minimal surface area** — reuse existing patterns (IteratingController, `StageAgentConfig.Type` override, prompt builder tiers)
2. **Safety by default** — auto-injected prompt layer can't be accidentally omitted
3. **Backwards compatible** — no changes to existing agent types, stage types, or executor behavior
4. **Deterministic stage type** — derived from agent types in the executor, no runtime ambiguity
5. **Separation of concerns** — agent type owns prompt/controller, stage type owns executor/DB/UI
6. **Maximum operator flexibility** — no ordering constraints, mixed stages allowed (with warning)

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
- `actionBehavioralInstructions` constant — the safety preamble
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

This branch goes after the orchestrator check, before the sub-agent check, matching the existing dispatch pattern.

#### 5. Executor — `pkg/queue/executor.go`

**Stage type derivation** in `executeStage()`:

Before creating the DB stage, derive the stage type from resolved agent configs using an `allAgentsAreAction` helper method on `RealSessionExecutor`. This helper iterates the stage's agent configs and resolves each agent's effective type using the same logic as `ResolveAgentConfig` (stage override > agent definition, via `e.cfg.GetAgent(name)`). The derived type is passed to `CreateStage()` and immediately available in the first `stage.status: started` WebSocket event.

```go
// Method on RealSessionExecutor (has access to e.cfg for agent definitions)
stageType := stage.StageTypeInvestigation
if e.allAgentsAreAction(input.stageConfig) {
    stageType = stage.StageTypeAction
}
```

The helper checks each `StageAgentConfig`: if `agentConfig.Type` is set, use it; otherwise look up `agentDef.Type` via `e.cfg.GetAgent(agentConfig.Name)`. If all resolve to `AgentTypeAction`, return true. On error paths (e.g., agent not found), the stage type defaults to `investigation` — agent resolution errors are caught later by `ResolveAgentConfig` in `executeAgent`.

**Context flow** in `executor_helpers.go`:

Include `StageTypeAction` in both `buildStageContext()` and `extractFinalAnalysis()`. The action agent's amended report (investigation + actions) becomes the `finalAnalysis` that feeds the exec summary.

```go
if s.stageType != stage.StageTypeInvestigation &&
    s.stageType != stage.StageTypeSynthesis &&
    s.stageType != stage.StageTypeAction {
    continue
}
```

#### 6. Config Validation — `pkg/config/validator.go`

`AgentType.IsValid()` already covers validation in both `validateAgents()` and `validateStage()`. Once `AgentTypeAction` is added to the enum, it passes validation automatically.

**No ordering validation** — action stages can appear anywhere in the chain, including as the first/only stage. The safety prompt provides the runtime guardrail.

**Mixed stage warning** — log a warning during config validation when a stage has mixed action and non-action agents: "Stage 'X' has mixed action and non-action agents — stage type will be 'investigation', action-stage benefits (dashboard, audit) will not apply."

#### 7. Frontend — `web/dashboard/`

Full treatment for v1:
- `src/constants/eventTypes.ts` — add `ACTION: 'action'` to `STAGE_TYPE`
- `src/types/session.ts` — add `has_action_stages: boolean` to `DashboardSessionItem` and `ActiveSessionItem`
- Timeline components — distinct icon/color/label for action stages (see `StageSeparator.tsx` `getStageTypeIcon`, `StageAccordion.tsx` stage type badge)
- Session list — "action evaluation" badge on sessions containing at least one action stage, driven by the new `has_action_stages` field

**Backend support for session list badge:** The `DashboardSessionItem` type doesn't currently include stage-type detail. Add a `has_action_stages` boolean field to the Go model (`pkg/models/session.go`) and compute it in `session_service.go` when building the session list response (check if any stage has `stage_type = 'action'`). This mirrors the existing `has_parallel_stages` and `has_sub_agents` fields.

### Data Flow

```
YAML config: agents[].type: "action"
  ↓
Config validation: AgentTypeAction.IsValid() → true
  ↓ (warning if mixed action/non-action agents in a stage)
Executor: ResolveAgentConfig() → resolvedConfig.Type = AgentTypeAction
  ↓
Executor: e.allAgentsAreAction(stageConfig) → StageType = action (else investigation)
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
Frontend: distinct icon/color/label in timeline + session list badge
```

### Executor Flow (Updated)

```
1. Resolve chain config
2. For each config stage:
   a. Resolve agent configs
   b. Derive stage type (e.allAgentsAreAction → "action", else "investigation")
   c. Create DB stage with derived type
   d. Publish stage.status: started (with correct stage_type from first event)
   e. Run agents (IteratingController for action, same as default)
   f. If multiple agents → run synthesis (synthesis stage)
   g. Update chain context: prevContext = buildStageContext(completedStages)
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
- Preserve the investigation report, amend with actions section (this becomes the `finalAnalysis` for the exec summary)

### Action Stage Type (`stage.StageTypeAction`)

**Controls:** executor behavior, DB schema, dashboard rendering, queryability.

- **Derived from agent types:** in `executeStage()`, if all resolved agents are `type: action`, the stage gets `stage_type: action`. Otherwise it stays `investigation`.
- **DB queryability:** `WHERE stage_type = 'action'` finds all action evaluation stages.
- **Dashboard:** distinct rendering in timeline + session list badge.
- **Context flow:** action stages contribute to `buildStageContext()` and `extractFinalAnalysis()` so the exec summary sees the complete picture.

### Relationship Between Types

```
Agent type: action     →  prompt safety layer + IteratingController
                            (per-agent concern, each action agent gets this)

Stage type: action     →  DB audit + dashboard + context flow
                            (per-stage concern, only when ALL agents are action)
```

An action agent in a mixed stage still gets the safety prompt. The stage just doesn't get action-type benefits (a config warning is logged).

## Key Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Stage type derivation location | Executor — derive at stage creation time via `allAgentsAreAction` method | Single source of truth at stage creation; stage type set before first WS event. Rejected: config-layer pre-compute (duplicates resolution logic), raw config check (ignores agent-level type definitions). |
| Q2 | Action stages in context flow | Include in both `buildStageContext()` and `extractFinalAnalysis()` | Action agent's amended report (investigation + actions) becomes the finalAnalysis for exec summary. Consistent, simple. Rejected: extractFinalAnalysis only (special-casing for no benefit), exclude (exec summary would miss actions). |
| Q3 | Action stage ordering validation | No validation — action stages can appear anywhere | Action-only chains are unusual but valid. Safety prompt provides runtime guardrail. Maximum operator flexibility. Rejected: hard validation (blocks valid action-only chains), warning (marginal value). |
| Q4 | Mixed stage warning | Warn at config load | Log warning for mixed action/non-action stages. Cheap, catches accidental misconfiguration. Rejected: no warning (easy to misconfigure without realizing). |
| Q5 | Frontend scope for v1 | Full treatment — timeline distinct rendering + session list badge | Timeline icon/color/label + "action evaluation" badge on session list. Rejected: constant only (no visual benefit), timeline only (incomplete for operators scanning session list). |
| Q6 | Controller for action agents | Simple IteratingController (regular agent with tools) | Sufficient for evaluate→decide→act pattern. Orchestration available via existing escape hatches if needed. Rejected: orchestrator for action dispatch (over-engineering). |
| Q7 | Context format for action stage | Standard `BuildStageContext()` | Zero implementation, consistent with all other stages. Upstream quality (synthesis prompt improvements) makes this work well. Rejected: enriched context (premature complexity), structured extraction (unreliable). |
| Q8 | Auto-injected safety prompt | Minimal safety preamble (high-level principles only) | Universal safety guarantee without domain-specific prescriptiveness. Mirrors orchestrator pattern: auto-injected behavioral strategy + custom domain instructions. Rejected: comprehensive framework (rigid, conflicts with domain patterns), no auto-injection (loses key safety benefit). |
| Q9 | Dashboard and audit trail | Lightweight distinct rendering | Action stage rendered distinctly in timeline + session list badge. Actual details in agent's final report. Rejected: no treatment (loses visibility), tool call parsing (fragile, premature). |

## Implementation Notes

### DB Migration

Adding `"action"` to the PostgreSQL `stage_type` enum is an additive change (`ALTER TYPE ... ADD VALUE`). Ent's migration system handles this automatically. No data migration needed — existing rows are unaffected. Fully backwards compatible.

### Error Paths in `executeStage()`

The `executeStage()` function returns `stageResult{stageType: stage.StageTypeInvestigation}` in several early error paths (e.g., no agents, stage creation failure). These error paths fire before the stage type derivation runs, so they default to `investigation`. This is correct — the derived type is only meaningful on the happy path after `CreateStage`.

### Action Stage Auto-Collapse in Timeline

`ConversationTimeline.tsx` has `shouldAutoCollapseStage` which auto-collapses synthesis and exec_summary stages when the session is complete. Action stages should auto-collapse too — the exec summary and final answer already cover the action details. Add `STAGE_TYPE.ACTION` to the collapsible list in `shouldAutoCollapseStage`.

### Upstream Analysis Quality

The action agent is only as good as the analysis it receives. As part of this feature, the built-in synthesis prompt was re-evaluated to ensure it emphasizes including evidence references, classification, and confidence in the report.

## Configuration Example

```yaml
agent_chains:
  my-investigation:
    alert_types: ["MyAlertType"]
    stages:
      - name: "investigation"
        agents: [...]
        synthesis: {...}
      - name: "take-action"
        agents:
          - name: "RemediationAgent"
            type: "action"
            mcp_servers: ["remediation-server"]
```

The `type: action` on the agent config drives both:
- **Agent type** → safety prompt auto-injection, IteratingController selection
- **Stage type** (derived) → all agents are `type: action` → `stage_type: action` in DB

## Pipeline Flow

```
Alert arrives
  → [Investigation stages] — agents gather evidence
  → [Synthesis stage] — findings unified (if parallel agents)
  → [Action stage] — evaluates findings, executes justified actions
  → [Exec Summary stage] — auto-generated summary (hardcoded, always runs last)
  → Session completed
```

## Future Considerations

- **Human confirmation gate** — at the MCP tool-calling layer, pause and wait for approval before forwarding action tool calls. The decision model doesn't change; the gate is downstream.
- **Enriched context** — if standard `BuildStageContext()` proves insufficient, a specialized `BuildActionContext()` with raw evidence from investigation stages is the natural next step.
- **Tool call parsing for UI** — if a reliable heuristic emerges to detect which tool calls were "real actions" vs. verification, a dedicated "Actions Taken" summary section could be added to the dashboard.
- **Orchestrated action agents** — if complex multi-action scenarios arise, action agents could be added into existing orchestrated investigation stages (works today, agents still get safety prompt). Multi-type support for agents (action + orchestrator) is a future consideration.

## Implementation Plan

### PR 1: Backend — action agent type and stage type (DONE)

Config layer, DB schema, controller factory, prompt builder, executor (stage type derivation + context flow), synthesis prompt review, tests.

### PR 2: Frontend — action stage rendering + session list badge

Backend session list API (`has_action_stages`), frontend constants, types, timeline distinct rendering, session list badge.
