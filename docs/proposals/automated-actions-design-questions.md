# Automated Actions — Design Questions

**Status:** All questions decided
**Related:** [Design document](automated-actions-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where should the stage type derivation logic live?

The sketch decided that stage type is derived from agent types: all agents `type: action` → `stage_type: action`, otherwise `investigation`. The question is where this derivation runs.

Today, `executeStage()` always hardcodes `StageTypeInvestigation` when creating a stage. The effective agent type is determined by `ResolveAgentConfig()` which merges the agent definition's `Type` with the stage-level `StageAgentConfig.Type` override. This resolution currently runs inside `executeStage()` for each agent.

### Option A: Derive in the executor (at stage creation time)

Before creating the DB stage in `executeStage()`, resolve all agent configs for the stage and check if every resolved type is `AgentTypeAction`. Pass the derived stage type to `CreateStage()`.

```go
// In executeStage(), before CreateStage:
stageType := stage.StageTypeInvestigation
if allAgentsAreAction(input.stageConfig.Agents, agentRegistry) {
    stageType = stage.StageTypeAction
}
```

The helper `allAgentsAreAction` would apply the same type-resolution logic that `ResolveAgentConfig` uses (stage override > agent definition).

- **Pro:** Single source of truth — derivation happens where the stage is created
- **Pro:** Uses the same resolution logic path as the rest of the executor
- **Pro:** No config-layer changes beyond the enum
- **Pro:** The stage type is set before the first `stage.status: started` event is published — dashboard sees `action` from the very first streaming event

**Decision:** Option A — derive in the executor at stage creation time.

The derivation happens in `executeStage()` before `CreateStage()`. A small `allAgentsAreAction` helper applies the established resolution order (stage override > agent definition). The derived type is passed to `CreateStage()` and immediately available in the `stage.status: started` WebSocket event — no streaming gap for the dashboard.

_Considered and rejected: Option B — pre-compute at config load (duplicates resolution logic, new config field for marginal benefit), Option C — minimal check on raw config fields (ignores agent-level type definitions, inconsistent with type resolution)_

---

## Q2: Should action stages contribute to `buildStageContext()` and `extractFinalAnalysis()`?

Currently, `buildStageContext()` and `extractFinalAnalysis()` in `executor_helpers.go` filter to only `investigation` and `synthesis` stages. The action stage will typically be the last chain-defined stage. Its final report is the investigation report amended with an actions section. The exec summary stage (which runs after all chain stages) calls `extractFinalAnalysis()` to get the text to summarize.

**Decision:** Option A — include action stages in both `buildStageContext()` and `extractFinalAnalysis()`.

Add `StageTypeAction` to the filter in both functions. The action agent's amended report (investigation + actions) becomes the `finalAnalysis` that the exec summary summarizes. Consistent, simple, and future-proof.

_Considered and rejected: Option B — include only in extractFinalAnalysis (special-casing for no practical benefit), Option C — don't include (exec summary would never mention actions taken)_

---

## Q3: Should action stage ordering be validated at config load?

The sketch mentions "executor pre-condition enforcement — require investigation to complete before action." The question is whether to enforce this as a config validation rule.

If someone configures an action stage as the first (or only) stage in a chain, the action agent would receive no investigation context. The safety prompt says "require hard evidence" — so it would likely take no action. But it's still a misconfiguration.

**Decision:** Option C — no validation, rely on sequential execution and safety prompt.

An action-only chain (no preceding investigation stages) is unusual but valid — the action agent handles both investigation and action. The safety prompt provides the runtime guardrail ("require hard evidence"). No ordering constraints imposed. Maximum operator flexibility.

_Considered and rejected: Option A — hard validation (blocks valid action-only chains), Option B — warning (marginal value, easy to miss)_

---

## Q4: Should mixed action/non-action stages produce a config warning?

A stage with some `type: action` agents and some non-action agents is valid — the action agents still get the safety prompt. But the stage type falls back to `investigation`, losing action-stage benefits (dashboard, audit, DB queryability).

**Decision:** Option A — warn at config load.

Log a warning during config validation when a stage has mixed action and non-action agents: "Stage 'X' has mixed action and non-action agents — stage type will be 'investigation', action-stage benefits (dashboard, audit) will not apply." A single log line at startup is cheap and catches accidental misconfiguration (e.g., forgot to set `type: action` on one agent).

_Considered and rejected: Option B — no warning (easy to misconfigure without realizing)_

---

## Q5: What frontend changes are needed for v1?

The sketch decided on "lightweight distinct rendering" (Option B in sketch Q9). The question is what specifically ships in v1.

**Decision:** Option C — full treatment (timeline + session list badge).

Add `ACTION: 'action'` to `STAGE_TYPE` constants, distinct icon/color/label for action stages in the timeline, and an "action evaluation" badge on session list items for sessions containing at least one action stage. The badge signals action evaluation occurred (not that actions were necessarily taken). May require checking stage types client-side or a minor API addition.

_Considered and rejected: Option A — constant only (no visual benefit), Option B — timeline only (incomplete, session list scanning is important for operators)_
