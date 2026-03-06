# Automated Actions — Design Questions

**Status:** Open — decisions pending
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
- **Con:** Stage type only known at runtime — can't be displayed/logged at config load

### Option B: Pre-compute at config load, use in executor

Add a computed `StageType` field to `StageConfig` (or a helper method) that resolves agent types during config loading/validation. The executor reads this pre-computed value instead of deriving it.

```go
// In config loader or validator:
for _, stage := range chain.Stages {
    stage.DerivedStageType = deriveStageType(stage.Agents, agentRegistry)
}
```

- **Pro:** Stage type known at config load — can be logged, validated, used in warnings
- **Pro:** Config is self-describing — you can inspect it without running the executor
- **Con:** Must replicate agent type resolution logic in the config layer (agent def Type + stage override Type)
- **Con:** New computed field on `StageConfig` — slightly enlarges the config model
- **Con:** If resolution logic changes, two places to update

### Option C: Minimal executor check, no pre-computation

Don't pre-compute or store a derived stage type. In `executeStage()`, check the raw `StageAgentConfig.Type` fields directly (before full resolution). If all stage agent configs have `Type: action` explicitly set, use action stage type.

```go
stageType := stage.StageTypeInvestigation
if allStageAgentConfigsHaveType(input.stageConfig.Agents, config.AgentTypeAction) {
    stageType = stage.StageTypeAction
}
```

- **Pro:** Simplest — no resolution needed, just check config fields
- **Pro:** Only explicit `type: action` in YAML triggers action stage — no surprises from agent definition defaults
- **Con:** Ignores the agent definition's `Type` — an agent defined as `type: action` at the top level but referenced without `type: action` in the stage config wouldn't contribute
- **Con:** Inconsistent with how other type resolution works (agent def + stage override merge)

**Recommendation:** Option A. The derivation belongs in the executor, close to where `ResolveAgentConfig` already runs. The helper is small and uses the established resolution order (stage override > agent definition). Pre-computation (Option B) adds a new config concept for marginal benefit. Option C is too simplistic — it ignores agent-level type definitions.

---

## Q2: Should action stages contribute to `buildStageContext()` and `extractFinalAnalysis()`?

Currently, `buildStageContext()` and `extractFinalAnalysis()` in `executor_helpers.go` filter to only `investigation` and `synthesis` stages. The action stage will typically be the last chain-defined stage. Its final report is the investigation report amended with an actions section. The exec summary stage (which runs after all chain stages) calls `extractFinalAnalysis()` to get the text to summarize.

### Option A: Include action stages in both

Add `StageTypeAction` to the filter in both functions:

```go
if s.stageType != stage.StageTypeInvestigation &&
    s.stageType != stage.StageTypeSynthesis &&
    s.stageType != stage.StageTypeAction {
    continue
}
```

- **Pro:** The action agent's amended report (investigation + actions) becomes the `finalAnalysis` — the exec summary sees the complete picture
- **Pro:** If a hypothetical future stage follows the action stage, it receives the action context
- **Pro:** Consistent — the action stage's report IS the final substantive output of the chain
- **Con:** None significant — the action stage is last, so `buildStageContext` for subsequent stages is theoretical

### Option B: Include action only in `extractFinalAnalysis`, not `buildStageContext`

Action stages contribute to the final analysis (for exec summary) but not to the chain context passed to subsequent stages.

- **Pro:** Prevents action context from bleeding into hypothetical subsequent investigation stages
- **Con:** Special-casing with no practical benefit since action is typically last
- **Con:** Two functions with different filter logic — harder to reason about

### Option C: Don't include action stages

Leave the current filter as-is. The final analysis comes from the last investigation/synthesis stage. The action stage's report is only visible in its own stage record.

- **Pro:** No changes to existing helper functions
- **Con:** The exec summary doesn't see action results — it summarizes only the investigation
- **Con:** The session's executive summary would never mention what actions were taken
- **Con:** Defeats the purpose of the action agent amending the report

**Recommendation:** Option A. The action agent's entire purpose is to produce the final substantive report (investigation + actions). Both context functions should include it. Since the action stage is typically last, `buildStageContext` inclusion is theoretical but harmless and future-proof.

---

## Q3: Should action stage ordering be validated at config load?

The sketch mentions "executor pre-condition enforcement — require investigation to complete before action." The question is whether to enforce this as a config validation rule.

If someone configures an action stage as the first (or only) stage in a chain, the action agent would receive no investigation context. The safety prompt says "require hard evidence" — so it would likely take no action. But it's still a misconfiguration.

### Option A: Validate — action stages must not be the first stage

Add a validation rule in `validateChains()`: if a stage's derived type is `action`, at least one non-action stage must precede it.

- **Pro:** Catches obvious misconfiguration at startup, not at runtime
- **Pro:** Clear error message: "action stage 'take-action' must be preceded by at least one investigation stage"
- **Con:** Requires knowing the derived stage type at validation time (depends on Q1)
- **Con:** Minor code addition to the validator

### Option B: Warn but don't reject

Log a warning if an action stage appears first, but allow it.

- **Pro:** Non-breaking — operators can experiment with unusual configurations
- **Pro:** The safety prompt provides a runtime guardrail anyway
- **Con:** Warnings are easy to miss in logs

### Option C: No validation — rely on sequential execution and safety prompt

The chain stages are sequential by definition. The safety prompt says "require hard evidence." If there's no evidence, the agent won't act. No validation needed.

- **Pro:** Zero code — the system self-corrects
- **Pro:** Maximum operator flexibility
- **Con:** Confusing failure mode — the action agent runs, does nothing, and the operator has to figure out why

**Recommendation:** Option A. A validation error at startup is vastly better than a confusing runtime non-result. The check is trivial. If Q1 is decided as Option A (executor derivation), the validation can use the same `allAgentsAreAction` helper or a simpler check on the raw config.

---

## Q4: Should mixed action/non-action stages produce a config warning?

A stage with some `type: action` agents and some non-action agents is valid — the action agents still get the safety prompt. But the stage type falls back to `investigation`, losing action-stage benefits (dashboard, audit, DB queryability).

### Option A: Warn at config load

Log a warning: "Stage 'X' has mixed action and non-action agents — stage type will be 'investigation', action-stage benefits (dashboard, audit) will not apply."

- **Pro:** Operators know the trade-off they're making
- **Pro:** Catches accidental misconfiguration (e.g., forgot to set `type: action` on one agent)
- **Con:** Minor noise if the mixing is intentional

### Option B: No warning — document the behavior

Just document the behavior in operator docs. The config is valid, the system works correctly.

- **Pro:** Less noise, simpler code
- **Pro:** Intentional mixing doesn't trigger unnecessary warnings
- **Con:** Easy to misconfigure without realizing

**Recommendation:** Option A. A single log line at startup is cheap and catches common mistakes. Operators who intentionally mix can safely ignore it.

---

## Q5: What frontend changes are needed for v1?

The sketch decided on "lightweight distinct rendering" (Option B in sketch Q9). The question is what specifically ships in v1.

### Option A: Minimal — constant only, no visual changes

Add `ACTION: 'action'` to `STAGE_TYPE` constants. The timeline renders action stages like investigation stages. The frontend is ready for future visual work but doesn't change appearance yet.

- **Pro:** Zero frontend risk — purely additive
- **Pro:** Backend can ship independently
- **Con:** No visual differentiation — operators can't tell action stages apart at a glance

### Option B: Timeline distinct rendering

Add the constant, plus a distinct icon/color/label for action stages in the timeline component. When `stage_type === 'action'`, show a different visual treatment (e.g., different icon, accent color, "Action" label instead of "Investigation").

- **Pro:** Delivers the sketch's intended UX benefit
- **Pro:** Low effort — conditional styling in one component
- **Pro:** Uses existing `stage_type` field already in the API
- **Con:** Requires frontend PR, design decision on icon/color

### Option C: Full treatment — timeline + session list badge

Option B plus an "action evaluation" badge on session list items for sessions that contain at least one action stage.

- **Pro:** Complete v1 experience per the sketch
- **Con:** Session list badge requires checking if any stage in the session is action-typed — may need an API addition or client-side filtering
- **Con:** More frontend scope

**Recommendation:** Option B. Timeline distinct rendering delivers the core UX benefit with minimal effort. The session list badge (Option C) can follow once the backend is proven. Option A delays the UX value that motivated the distinct stage type.
