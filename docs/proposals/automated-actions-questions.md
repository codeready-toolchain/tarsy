# Automated Actions — Sketch Questions

**Status:** All questions decided
**Related:** [Sketch document](automated-actions-sketch.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the sketch, then update the sketch document.

---

## Q1: Where in the pipeline should actions execute?

The action capability needs a home in TARSy's processing pipeline. This determines how it interacts with investigation results, when it runs, and how it's configured.

### Option A: New "action" stage type in the chain

Add a new stage type (`action`) that appears as a regular stage in the chain definition, running after investigation/synthesis stages. The executor handles it like any other stage — creates Stage and AgentExecution records, runs through the agent framework, captures timeline events.

- **Pro:** Clean separation of investigation from action — easy to reason about, audit, and disable
- **Pro:** Fits naturally into the existing chain model (sequential stages with context flow)
- **Pro:** Action stage sees full investigation context via `BuildStageContext()`
- **Pro:** Can be added/removed from chains without touching investigation stages
- **Pro:** Enables auto-injected safety prompt layer (like orchestrator agents get orchestration strategy) — consistent baseline that can't be accidentally omitted
- **Pro:** Enables a specialized context builder tuned for action evaluation (more structured, less noise, or richer evidence — see Q7)
- **Pro:** Queryable in DB — "show all automated actions across sessions" is a trivial `stage_type = 'action'` query
- **Pro:** Dashboard can render action stages with distinct visual treatment (action badges, "VM SHUTDOWN" indicators)
- **Pro:** Executor can enforce pre-conditions (e.g., require at least one investigation/synthesis stage completed before action)
- **Con:** Adds latency — action happens only after all investigation stages complete
- **Con:** New stage type requires schema/enum changes (but precedent exists with `exec_summary`, `scoring`)

**Decision:** Option A — a dedicated action stage type (and agent type).

Automated actions are already possible with regular investigation agents + appropriate MCP tools + custom instructions. The new types are an **ergonomic and safety layer** — not a new capability. They provide: auto-injected safety prompt (agent type), specialized context building (stage type), DB-level auditability (stage type), dashboard treatment (stage type), and executor pre-condition enforcement (stage type). Less custom prompt required, more works OOTB.

This parallels `exec_summary` — not a different engine, but a different role with different guardrails.

_Considered and rejected: Option B — plain investigation stage (no safety guardrails or auditability beyond what you manually configure), Option C — post-session async (delay unacceptable for time-sensitive security actions), Option D — inline in synthesis (overloads synthesis, requires controller change)_

---

## Q2: How should the action decision be made?

The action stage needs to decide whether to act and what actions to take. This could range from full LLM autonomy to a structured extraction + rules engine approach.

### Option C: LLM judgment with policy guardrails

The action agent uses LLM judgment (like Option A), but with configurable guardrails that constrain what it can do. For example: the action stage only has access to specific MCP tools (not all tools), and its prompt includes explicit policy rules. The LLM decides *whether* to act, but the *available actions* are constrained by configuration.

- **Pro:** Balances flexibility with safety — LLM handles nuance, config constrains scope
- **Pro:** Simple implementation — regular agent with restricted tool access
- **Pro:** Policy changes via YAML (add/remove tools, update instructions) — no code changes
- **Pro:** The agent's reasoning is captured in the timeline for audit
- **Pro:** Compatible with future human confirmation — a confirmation gate could be added at the MCP tool-calling layer without changing the decision model
- **Con:** Still depends on LLM judgment for the "should I act?" decision
- **Con:** An aggressive LLM might use all available tools even when not warranted

**Decision:** Option C — LLM judgment with policy guardrails.

The action agent's MCP tool access is constrained by configuration (you only give it the tools you want it to use), and explicit prompt instructions define when action is appropriate. This matches TARSy's existing pattern where agents are guided by instructions + tool availability. For the security use case: give the action agent only `shutdown-vm`, instruct it "only shut down VMs classified as MALICIOUS with HIGH confidence" — the reasoning is captured in the timeline for audit.

**Future extension (out of scope):** Optional human confirmation before action execution. This would most naturally live at the tool-calling layer — the MCP tool executor could pause and wait for approval before forwarding the call to the MCP server. The LLM decision model (Option C) doesn't need to change; the gate is downstream of the decision.

_Considered and rejected: Option A — full LLM judgment (no guardrails on capability — too risky for production actions), Option B — structured extraction + rule engine (significant complexity, rigid, requires structured output from investigation stages)_

---

## Q3: How should action policies be defined and enforced?

The system needs a way to express "under what conditions can TARSy take automated actions?" This affects configuration, safety, and auditability.

### Option C: Prompt-based policy with tool-level constraints

Policies are expressed in the agent's instructions, but with an additional layer: the action stage's MCP server configuration explicitly controls which tools are available. The "enforcement" is that the agent simply can't call tools it doesn't have.

- **Pro:** Two layers of control — prompt guides behavior, tool access constrains capability
- **Pro:** Tool-level control is already supported by TARSy's MCP configuration
- **Pro:** Simple to implement — no new config types
- **Pro:** Easy to reason about: "this agent can only do X, Y, Z"
- **Con:** An agent with `shutdown-vm` will always be *able* to shut down VMs — the prompt is the only thing deciding *when*
- **Con:** Policy reasoning happens in LLM space, not in deterministic code

**Decision:** Option C — prompt-based policy with tool-level constraints.

Two layers of control: `custom_instructions` describe decision criteria (when to act), `mcp_servers` configuration limits capability (what actions are possible). TARSy's existing config model already supports both. If operational experience shows LLM-based policy decisions are insufficient, structured policies can be layered on later without changing the pipeline architecture.

_Considered and rejected: Option A — prompt-only (no hard enforcement — agent could do anything), Option B — structured YAML policy (requires structured output from investigation, significant complexity, rigid)_

---

## Q4: Separation of concerns — investigation vs action

The action agent's role is to evaluate findings and decide whether to act, not to repeat the investigation. This question is about how to enforce that separation through prompts and best practices.

**Decision:** Tool availability is a configuration concern — operators choose which MCP servers the action agent gets. No architectural decision needed.

The auto-injected action prompt (Q8) should guide the agent to **focus on evaluating findings and acting**, encouraging it to avoid re-investigating while leaving room for brief re-verification when the agent judges it necessary (e.g., confirming a workload is still running before shutting it down). The prompt should not hard-prohibit investigation — the LLM should have room for best judgment.

**Upstream quality matters.** The action agent is only as good as the analysis it receives. Investigation agents produce final analyses; synthesis agents unify parallel results. For single-agent stages, the agent's final analysis IS the unified assessment. In all cases, the action agent relies on these concluded assessments to make decisions.

**Implementation note:** As part of this feature, re-evaluate the built-in synthesis prompt to ensure it emphasizes including evidence references, classification, and confidence in the report. Comprehensive upstream reporting makes the action agent's job more reliable. This is not a new feature — it's a prompt quality improvement that benefits the action pipeline.

---

## Q5: How should the action stage be configured in the chain YAML?

The action stage needs a configuration model that fits TARSy's existing chain YAML structure.

### Option A: Agent type `action` drives stage type

Use the existing `StageAgentConfig.Type` field to set `type: action` on agents. The stage type is **derived at config load time**: if all agents in a stage have `type: action`, the executor creates the Stage with `stage_type: action`. Otherwise the stage stays `investigation`.

```yaml
stages:
  - name: "take-action"
    agents:
      - name: "RemediationAgent"
        type: "action"
        mcp_servers: ["remediation-server"]
```

- **Pro:** Minimal config schema changes — reuses existing `type` override mechanism
- **Pro:** No new stage-level config fields
- **Pro:** Deterministic rule resolved at config load time — no runtime ambiguity
- **Pro:** Users can mix agent types in a stage if they want, but they lose action stage benefits (dashboard, audit, context) — a clear and reasonable trade-off
- **Pro:** Each `type: action` agent still gets the auto-injected safety prompt regardless of whether the stage is pure action or mixed
- **Con:** Stage type is implicit (derived from agents), not explicitly declared

**Decision:** Option A — agent type `action` drives stage type.

Adding `action` to the `AgentType` enum. The **agent type** provides: safety prompt auto-injection, IteratingController selection. The **stage type** is derived at config load: all agents in the stage are `type: action` → `stage_type: action`, otherwise → `investigation`. Stage type provides: specialized context, dashboard rendering, DB auditability, executor pre-conditions.

Users can mix action and non-action agents in a stage — nothing prevents it. The action agents still get their safety prompt. But the stage loses action-type benefits (it becomes a regular investigation stage). This is a reasonable trade-off: if you want the full action stage experience, keep the stage pure.

_Considered and rejected: Option B — stage-level config field (new field, mapping concern between config and DB stage types), Option C — global agent type (couples action to agent definition, can't reuse agent as investigation elsewhere)_

---

## Q6: Should the action capability reuse the orchestrator pattern?

The orchestrator agent can dynamically dispatch sub-agents and synthesize results. Could the action stage use an orchestrator to dispatch specialized action sub-agents?

### Option A: Simple IteratingController (regular agent with tools)

The action stage uses a standard IteratingController agent — same as investigation agents. It has access to action tools and uses them directly. No orchestration needed.

- **Pro:** Simple — no new patterns or machinery
- **Pro:** Sufficient for the initial use case (evaluate findings → decide → act)
- **Pro:** Fewer iterations needed — action decisions are typically straightforward

**Decision:** Option A — simple IteratingController.

Purely action stages don't need orchestration for the foreseeable use cases. If complex multi-action scenarios arise, there are two escape hatches without architectural changes: (1) promote the action agent to `type: orchestrator` at the stage level (existing mechanism — though this would require multi-type support for agents, a future consideration), or (2) add action-capable agents into an existing orchestrated investigation stage (works today — the agents get the safety prompt via their `type: action`, though the stage itself won't be `stage_type: action`).

_Considered and rejected: Option B — orchestrator agent for action dispatch (over-engineering for initial use case, adds latency and configuration complexity)_

---

## Q7: What context should the action stage receive?

The standard `BuildStageContext()` provides previous stages' `final_analysis` text — a narrative summary. The action stage has different needs than a follow-up investigation stage: it needs to evaluate evidence and make a binary act/don't-act decision. The context format could help or hinder that.

### Option A: Standard stage context (BuildStageContext)

Use the existing `BuildStageContext()` — the action agent receives the same `final_analysis` text from previous stages that any downstream investigation stage would get.

- **Pro:** Zero implementation — works today
- **Pro:** Consistent with how all other stages receive context

**Decision:** Option A — standard stage context.

This was effectively decided during the Q4 discussion. The action agent relies on the upstream final analysis (synthesis result or single-agent result). To make this work well, upstream investigation and synthesis agents should be prompted to provide comprehensive, evidence-backed analysis with clear classifications and confidence indicators. The action agent's auto-injected safety prompt (Q8) reinforces that it should evaluate the evidence presented in the context rather than re-investigating.

If operational experience shows the agent frequently misinterprets findings, enriched context (Option B) is the natural next step.

_Considered and rejected: Option B — enriched context with raw evidence (implementation complexity, token overhead, premature), Option C — synthesis-only context (loses cross-reference ability), Option D — structured context extraction (coupling, unreliable structured LLM output, premature)_

---

## Q8: What safety-focused system prompt should be auto-injected for action agents?

This is an **agent type concern**: the prompt builder auto-injects behavioral layers based on `AgentType`. The `type: orchestrator` agents get orchestration strategy, sub-agent catalog, and result delivery mechanics. An `action` agent type should similarly get a safety-focused layer. The question is what belongs in this auto-injected layer vs. in the agent's `custom_instructions`.

### Option A: Minimal safety preamble

Auto-inject a short, focused safety preamble — high-level principles only. Domain-specific decision criteria stay in `custom_instructions`.

Auto-injected layer would cover:
- "Require hard evidence before acting — never act on speculation or low-confidence findings"
- "Your role is to evaluate the analysis provided by previous stages and decide whether to act — avoid re-investigating what has already been thoroughly analyzed"
- "If evidence is ambiguous or conflicting, report your assessment but do NOT act"
- "Explain your reasoning BEFORE executing any action tool"
- "Prefer inaction over incorrect action"
- "Your final report becomes the finalAnalysis that the exec summary stage will summarize. Preserve the investigation report from previous stages and amend it with an actions section covering: what actions were taken (or why none were taken), the reasoning behind each decision, and the outcome of each action. Do not replace the investigation report with a purely action-oriented summary"

**Decision:** Option A — minimal safety preamble.

A short, universal safety preamble provides the "can't accidentally forget it" guarantee without being prescriptive about domain-specific decision criteria. The auto-injected layer says "be careful, require evidence, explain yourself" — the `custom_instructions` say "for security investigations, shut down VMs only when classification is MALICIOUS with HIGH confidence." This mirrors the orchestrator pattern: auto-injected behavioral strategy + custom domain instructions.

Note: the preamble deliberately avoids language like "verify current state" which could encourage the agent to re-run investigation tools. The action agent should trust the upstream analysis and focus on the act/don't-act decision. Light re-verification is allowed if the agent judges it truly necessary, but the prompt should not invite it.

_Considered and rejected: Option B — comprehensive action framework (rigid, may conflict with domain-specific patterns, long prompt), Option C — no auto-injection (loses the key safety benefit, easy to forget safety instructions)_

---

## Q9: How should action stages appear in the dashboard and audit trail?

Action stages have a unique characteristic: they represent TARSy _doing_ something to production systems, not just analyzing them. This may warrant distinct visual treatment in the dashboard and specific auditability features.

**Decision:** Option B — lightweight distinct rendering.

The action stage runs 100% of the time for every successful session when defined in the chain. Whether it actually takes action or decides to skip is embedded in the agent's reasoning and final report — there's no reliable external signal. So the UI treatment is limited to: (1) render the action stage distinctly in the timeline (different icon/color/label, driven by `stage_type: action`), and (2) optionally show a badge on session list items indicating action agents were involved. The actual "what happened" is in the agent's final report, which the user reads like any other stage result.

Option C (parsing tool calls) is a potential future enhancement if we find a reliable heuristic, but premature for v1.

_Considered and rejected: Option A — no special treatment (loses the at-a-glance visibility), Option C — dedicated summary section (fragile tool call parsing, premature)_
