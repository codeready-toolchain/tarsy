# ADR-0007: Automated Actions

**Status:** Implemented  
**Date:** 2026-03-06

## Overview

Add `action` as a new agent type and stage type to TARSy, enabling automated remediation actions based on investigation findings. This is an ergonomic and safety layer on top of existing capabilities — not a new engine. The action agent type provides auto-injected safety prompts; the action stage type provides DB auditability, distinct dashboard rendering, and context flow integration.

## Design Principles

1. **Minimal surface area** — reuse existing patterns (iterating controller, per-stage agent type override, prompt builder tiers).
2. **Safety by default** — auto-injected prompt layer can't be accidentally omitted.
3. **Backwards compatible** — no changes to existing agent types, stage types, or executor behavior for non-action configurations.
4. **Deterministic stage type** — derived from resolved agent types when the stage is created, no runtime ambiguity.
5. **Separation of concerns** — agent type owns prompt/controller selection; stage type owns persistence, executor context, and UI semantics.
6. **Maximum operator flexibility** — no ordering constraints; mixed stages allowed (with warning).

## Core Concepts

### Action Agent Type

**Controls:** controller selection and prompt injection.

- **Controller:** Same iterating, multi-turn MCP/function-calling controller as default investigation agents.
- **Prompt:** Standard tiered instructions plus an auto-injected safety preamble and action-specific task focus, analogous to how orchestrator agents receive orchestration behavioral instructions.

The safety preamble covers:

- Require hard evidence before acting.
- Focus on evaluating upstream analysis; avoid re-investigation.
- If evidence is ambiguous, report but do **not** act.
- Explain reasoning before executing action tools.
- Prefer inaction over incorrect action.
- Preserve the investigation report and amend with an actions section (this becomes the material fed into the executive summary).

### Action Stage Type

**Controls:** executor behavior, persisted `stage_type`, dashboard rendering, and queryability.

- **Derived from agent types:** When a stage is created, if every resolved agent on that stage is an action agent, the stage is stored as `action`; otherwise it remains a normal investigation stage.
- **DB queryability:** Filters like `stage_type = 'action'` identify action-evaluation stages.
- **Dashboard:** Distinct timeline treatment and a session-list signal when a session includes any action stage.
- **Context flow:** Action stages participate in stage context assembly and final-analysis extraction so the executive summary sees the full narrative including actions.
- **Schema:** Extending the `stage_type` enum is additive; existing rows are unaffected and the change remains backward compatible.

### Relationship Between Types

```
Agent type: action     →  safety prompt + iterating controller
                            (per-agent; each action agent gets this)

Stage type: action     →  audit, dashboard, context flow
                            (per-stage; only when ALL agents on the stage are action)
```

An action agent in a mixed stage still gets the safety prompt; the stage does not get action-type benefits, and configuration validation logs a warning.

## Key Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Stage type derivation location | Executor — derive when the DB stage is created | Single source of truth; correct `stage_type` is available from the first status event. Rejected: config-layer pre-compute (duplicates resolution), raw config-only check (ignores agent-level type definitions). |
| Q2 | Action stages in context flow | Include in stage context and final-analysis extraction | The action-amended report flows into the executive summary. Consistent and simple. Rejected: final-analysis only, or excluding action stages (summary would miss actions). |
| Q3 | Action stage ordering validation | None — action stages can appear anywhere | Action-only chains are unusual but valid; the safety prompt is the runtime guardrail. Rejected: hard validation that blocks valid chains. |
| Q4 | Mixed stage warning | Warn at config load | Cheap signal for accidental misconfiguration. Rejected: silent mixed stages. |
| Q5 | Frontend scope for v1 | Timeline distinction + session list badge | Operators see action stages in both list and detail. Rejected: constants-only or timeline-only. |
| Q6 | Controller for action agents | Iterating controller (tools, multi-turn) | Enough for evaluate → decide → act; orchestration remains available via existing patterns if needed. Rejected: a dedicated orchestrator just for actions. |
| Q7 | Context format for action stage | Same stage-context builder as other stages | No special-case assembly; upstream synthesis quality carries the analysis. Rejected: bespoke enriched or structured extraction (premature or brittle). |
| Q8 | Auto-injected safety prompt | Minimal high-level principles | Universal safety without over-prescriptive domain rules; mirrors orchestrator “behavioral layer + domain task” split. Rejected: huge rigid framework or no auto-injection. |
| Q9 | Dashboard and audit trail | Lightweight distinct rendering | Visibility without fragile tool-call parsing. Rejected: no UI treatment or premature “actions taken” parsing. |

## Data Flow

```
YAML: agents with type action
  ↓
Config validation accepts action; warn if a stage mixes action and non-action agents
  ↓
Executor resolves each agent’s effective type (stage override vs agent definition)
  ↓
If all agents on the stage resolve to action → persist stage_type action; else investigation
  ↓
Prompt path: standard tiers + action safety + action task focus
  ↓
Iterating controller runs the agent with MCP/tools
  ↓
API and realtime payloads expose stage_type for UI
  ↓
Dashboard: distinct timeline chrome and session-level badge when any action stage exists
```

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

- **Agent type** → safety prompt injection and iterating controller.
- **Stage type** (derived) → when every agent on the stage is action, the stored stage is `action`.

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

- **Human confirmation gate** — pause at the tool layer before forwarding destructive MCP calls; the decision model stays the same.
- **Enriched context** — if generic stage context proves insufficient, a dedicated action context builder pulling raw investigation evidence is the natural next step.
- **Tool call parsing for UI** — if reliable heuristics appear, a dedicated “actions taken” summary in the dashboard could follow.
- **Orchestrated action agents** — action agents inside orchestrated stages already receive the safety prompt; mixing action with orchestrator as first-class config may evolve later.
