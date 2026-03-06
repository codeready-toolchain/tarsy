# Automated Actions for TARSy

**Status:** Sketch complete — all design questions decided, see [automated-actions-questions.md](automated-actions-questions.md) for full decision rationale.

## Problem

TARSy currently operates as an **observer**: it investigates alerts, produces analysis and recommendations, then hands off to engineers for action. For high-confidence, well-defined situations — such as clear evidence of a security policy violation — the "hand off to a human" step adds unnecessary delay. The investigation already identified the threat, the recommended action is clear, and the tools to execute it could be available.

This sketch defines **automated action capabilities** for TARSy, enabling it to act on its own findings when conditions warrant it.

## Scope

**In scope:**
- Mechanism for TARSy to execute remediation actions based on investigation findings
- Safety guardrails (confidence thresholds, action policies, audit trail)
- Configuration model for enabling/disabling actions per chain
- Integration with existing MCP tool infrastructure

**Out of scope:**
- Approval workflows requiring human-in-the-loop (future enhancement — would live at the MCP tool-calling layer)
- Undo/rollback mechanisms (actions are assumed to be safe and idempotent)

## Motivating Example

A security investigation chain runs multiple parallel agents, synthesizes findings, and produces a report with a classification (LEGITIMATE / SUSPICIOUS / MALICIOUS) and recommended action (MONITOR / INVESTIGATE_FURTHER / SUSPEND).

When the synthesis concludes with **MALICIOUS + HIGH confidence + SUSPEND**, TARSy could automatically shut down the offending workload using a remediation MCP tool — without waiting for an engineer to read the report and act manually.

## How It Relates to the Existing System

**Automated actions are already possible today.** A regular investigation agent with the right MCP servers (including tools that mutate state), appropriate custom instructions (or runbooks), and proper chain positioning can already take actions. Nothing in the current architecture prevents this.

What's missing is **ergonomic and safety support** for doing this correctly. The `action` agent type and stage type proposed here provide:

- **Auto-injected safety prompt** — consistent baseline that can't be accidentally omitted (agent type concern)
- **DB-level auditability** — "show all automated actions" is a trivial query (stage type concern)
- **Distinct dashboard treatment** — operators see at a glance which sessions had action evaluation (stage type concern)
- **Executor pre-condition enforcement** — require investigation to complete before action (stage type concern)

These built-in types make it easier to configure actionable chains correctly. Less custom prompt required. More works out of the box.

### Agent Type vs Stage Type

TARSy has two orthogonal type systems that serve different purposes:

- **Agent type** (`config.AgentType`) — determines the **controller** and **auto-injected prompt layers**. An `action` agent type uses IteratingController (same as default agents) and gets safety-focused behavioral instructions auto-injected by the prompt builder.
- **Stage type** (`stage.StageType` in DB) — determines **executor behavior**, **context building**, **dashboard rendering**, and **queryability**. An `action` stage type gets distinct visual treatment and is filterable in the DB.

The stage type is **derived from the agent types at config load time**: if all agents in a stage have `type: action`, the stage gets `stage_type: action`. Otherwise it stays `investigation`. This is a deterministic rule — no runtime ambiguity.

Users can mix action and non-action agents in a stage. Each `type: action` agent still gets the safety prompt. But the stage loses action-type benefits (dashboard, audit, DB queryability). If you want the full action stage experience, keep the stage pure.

## Pipeline Flow

```
Alert arrives
  → [Investigation stages] — agents gather evidence
  → [Synthesis stage] — findings unified (if parallel agents)
  → [Action stage] — evaluates findings, executes justified actions
  → [Exec Summary stage] — auto-generated summary (hardcoded, always runs last)
  → Session completed
```

The action stage is typically the **last chain-defined stage**. The exec summary stage runs automatically after all chain stages complete — it's hardcoded in the executor, not defined in YAML. The action stage's final report becomes the `finalAnalysis` that feeds into the exec summary. The action agent preserves the upstream investigation report and amends it with an actions section — what was done (or why nothing was done), the reasoning, and the outcome — so the exec summary has the complete picture to work with.

## Action Agent Behavior

The action agent uses the **IteratingController** (same as investigation agents) with access to action-specific MCP tools. It receives the standard stage context (`BuildStageContext()`) containing the upstream final analysis.

The agent:
1. Receives the investigation/synthesis report via standard stage context
2. Evaluates whether any actions are justified based on the evidence presented
3. Executes approved actions via MCP tools (if warranted)
4. Produces a final report that preserves the investigation report and appends an actions section

### Decision Model

**LLM judgment with policy guardrails.** The action agent uses LLM judgment to decide whether to act, constrained by two layers:
- **`custom_instructions`** — describe decision criteria (when to act)
- **`mcp_servers` configuration** — limit capability (what actions are possible)

The LLM decides *whether* to act; configuration constrains *what* it can do.

### Separation of Concerns

The action agent **evaluates and acts** — it does not re-investigate. Its auto-injected prompt encourages this separation while leaving room for brief re-verification when the agent judges it necessary. Investigation agents investigate and find evidence. Synthesis agents analyze and provide the report. The action agent decides and acts based on that report.

### Auto-Injected Safety Prompt

The `action` agent type gets a **minimal safety preamble** auto-injected by the prompt builder (similar to how `orchestrator` agents get orchestration strategy). This covers:

- Require hard evidence before acting — never act on speculation or low-confidence findings
- Focus on evaluating the upstream analysis, not re-investigating
- If evidence is ambiguous or conflicting, report assessment but do NOT act
- Explain reasoning BEFORE executing any action tool
- Prefer inaction over incorrect action
- Preserve the investigation report and amend it with an actions section (this becomes the `finalAnalysis` that the exec summary stage summarizes)

Domain-specific decision criteria (e.g., "shut down VMs only when classified MALICIOUS with HIGH confidence") stay in `custom_instructions`.

## Upstream Analysis Quality

The action agent is only as good as the analysis it receives. As part of this feature, the built-in synthesis prompt should be **re-evaluated** to ensure it emphasizes including evidence references, classification, and confidence in the report. For single-agent stages, the investigation agent's final analysis serves the same role — best-practice guidance should encourage structured, evidence-based conclusions.

## Configuration Model

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
    # ...
```

The `type: action` on the agent config drives both:
- **Agent type** → safety prompt auto-injection, IteratingController selection
- **Stage type** (derived) → all agents are `type: action` → `stage_type: action` in DB

No new stage-level config fields needed. This reuses the existing `StageAgentConfig.Type` override mechanism.

## Dashboard and Audit Trail

The action stage runs for every successful session when defined in the chain. Whether it actually takes action or decides to skip is embedded in the agent's reasoning and final report — there's no reliable external signal to detect this.

UI treatment:
- **Timeline:** Action stages rendered with distinct icon/color/label (driven by `stage_type: action` already in the API)
- **Session list:** Optional badge indicating action agents were involved (signals action evaluation occurred, not that actions were necessarily taken)
- **Audit:** `stage_type = 'action'` enables trivial DB queries to find all sessions with action evaluation

The actual details of what happened are in the agent's final report, read like any other stage result.

## Future Considerations

- **Human confirmation gate** — at the MCP tool-calling layer, pause and wait for approval before forwarding action tool calls. The decision model doesn't change; the gate is downstream.
- **Enriched context** — if standard `BuildStageContext()` proves insufficient, a specialized `BuildActionContext()` with raw evidence from investigation stages is the natural next step.
- **Tool call parsing for UI** — if a reliable heuristic emerges to detect which tool calls were "real actions" vs. verification, a dedicated "Actions Taken" summary section could be added to the dashboard.
- **Orchestrated action agents** — if complex multi-action scenarios arise, action agents could be added into existing orchestrated investigation stages (works today, agents still get safety prompt). Multi-type support for agents (action + orchestrator) is a future consideration.
