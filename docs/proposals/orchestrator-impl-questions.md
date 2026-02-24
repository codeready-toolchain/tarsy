# Orchestrator Agent — Implementation Questions

**Status:** All 11 questions decided
**Related:** [Design document](orchestrator-impl-design.md)
**Vision:** [orchestrator-agent-design.md](orchestrator-agent-design.md)
**Last updated:** 2026-02-19

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How is an orchestrator agent identified in config? — DECIDED

> **Decision:** Use the existing `type` field — `AgentTypeOrchestrator = "orchestrator"`.

Already resolved by [ADR-0001](../adr/0001-agent-type-refactor.md). The refactor replaced `IterationStrategy` with orthogonal `type` + `llm_backend` fields. The ADR explicitly plans for the orchestrator:

```go
// Future: AgentTypeOrchestrator AgentType = "orchestrator"
```

The orchestrator maps to `IteratingController` (same multi-turn tool-calling loop as default agents), with orchestration behavior coming from the `CompositeToolExecutor`. Config:

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    llm_backend: native-gemini
    description: "Dynamic SRE investigation orchestrator"
```

No new concept needed — just add the constant and the controller factory case.

*Resolved by: ADR-0001 agent type refactor.*

---

## Q2: How are orchestration tools combined with MCP tools? — DECIDED

> **Decision:** Option A — CompositeToolExecutor (wrapper pattern).

A `CompositeToolExecutor` wraps the existing MCP `ToolExecutor` and adds orchestration tools. Implements the same `ToolExecutor` interface. On `Execute`, routes by name: orchestration tools go to the `SubAgentRunner`, everything else delegates to MCP. The `IteratingController` doesn't need changes — it just sees tools.

```go
type CompositeToolExecutor struct {
    mcpExecutor agent.ToolExecutor
    runner      *SubAgentRunner
}
```

*Rejected: (B) extend MCP executor — pollutes MCP with orchestration concerns; (C) controller intercepts — breaks the clean controller/tool separation from ADR-0001.*

---

## Q3: How are orchestration tools named to avoid MCP collisions? — DECIDED

> **Decision:** Option A — plain names (`dispatch_agent`, `cancel_agent`, `list_agents`).

MCP tools always contain a dot (`server.tool` format), orchestration tools don't — natural namespace separation. The `CompositeToolExecutor` routes by matching the known orchestration tool names; everything else goes to MCP.

Note: `get_result` was removed from the tool surface — results are pushed automatically (see Q4 decision).

*Rejected: (B) prefixed `orchestrator.*` — unnecessary verbosity; (C) underscore-prefixed — unconventional, may confuse LLMs.*

---

## Q4: Does the orchestrator need a new controller? — DECIDED

> **Decision:** Reuse `IteratingController` with a targeted modification for push-based result collection.

Per [ADR-0001](../adr/0001-agent-type-refactor.md), the `IteratingController` (renamed from `FunctionCallingController`) runs the multi-turn tool-calling loop. The orchestrator reuses this loop with one targeted change to support push-based sub-agent results.

### Why push-based?

**Polling-based `get_result` is problematic**: the LLM wastes iterations checking on running sub-agents, and there's no way to wait efficiently within the iteration loop. A push-based model avoids this entirely — sub-agent results are injected into the orchestrator's conversation as they complete.

TARSy implements the push-based principle within the controller loop (the `IteratingController` already manages the iteration lifecycle, making it a natural integration point).

### Controller modification

Two additions to the iteration loop (zero impact on non-orchestrator agents):

```go
for iteration := 0; iteration < maxIter; iteration++ {
    // 1. Non-blocking drain: inject any completed sub-agent results
    if runner := execCtx.SubAgentRunner; runner != nil {
        for {
            result, ok := runner.TryGetNext()
            if !ok { break }
            messages = append(messages, formatSubAgentResult(result))
        }
    }

    resp := callLLMWithStreaming(ctx, messages, tools)
    // ... execute tool calls ...

    if len(resp.ToolCalls) == 0 {
        // 2. Wait when idle: if sub-agents are pending, wait for at least one
        if runner := execCtx.SubAgentRunner; runner != nil && runner.HasPending() {
            result, err := runner.WaitForNext(ctx)
            if err != nil { break }
            messages = append(messages, formatSubAgentResult(result))
            continue
        }
        break
    }
}
```

This enables multi-phase orchestration:
- LLM can dispatch agents across multiple iterations
- Results appear automatically as sub-agents finish
- Before each LLM call, any available results are injected
- When the LLM is idle (no tool calls), it waits for pending sub-agents instead of exiting
- The LLM can react to partial results (cancel, dispatch follow-ups, synthesize)

### Cleanup

Sub-agent cleanup (cancel + wait) is handled by `CompositeToolExecutor.Close()`, which is already deferred in `executeAgent()`. No controller-level cleanup needed.

### Why not the alternatives?

- **Option A (reuse as-is, no changes):** Would require a polling `get_result` tool — wastes iterations, LLMs are unreliable at polling. Push-based is strictly better.
- **Option B (wrapper controller):** The wrapper approach was designed for cleanup hooks, but cleanup is handled by `CompositeToolExecutor.Close()`. The only remaining concern (result injection) requires modifying the loop itself, not wrapping it.
- **Option C (fork controller):** Code duplication. The push-based modification is small enough to add directly.

*The controller change is ~15 lines — two `if runner != nil` blocks. The iteration loop structure, tool execution, streaming, forced conclusion — all unchanged.*

---

## Q5: Where do orchestrator guardrails live in config? — DECIDED

> **Decision:** B + C — nested `orchestrator` section on agents, with global defaults under the existing `defaults` section.

The orchestrator needs configurable limits: max concurrent sub-agents, per-sub-agent timeout, total budget. These live in two places:

1. **Global defaults** under the existing `defaults:` section (same pattern as `llm_provider`, `max_iterations`, etc.)
2. **Per-agent overrides** under a nested `orchestrator:` section on each orchestrator agent

```yaml
defaults:
  llm_provider: "google-default"
  max_iterations: 20
  orchestrator:                          # Global orchestrator defaults
    max_concurrent_agents: 5
    agent_timeout: 300s
    max_budget: 600s

agents:
  MyOrchestrator:
    type: orchestrator
    orchestrator:                         # Per-agent override (optional)
      max_concurrent_agents: 3           # Override: fewer concurrent agents
      agent_timeout: 600s                # Override: longer timeout for this one

  AnotherOrchestrator:
    type: orchestrator
    # No orchestrator section — uses global defaults
```

**Merge semantics:** Per-agent `orchestrator:` fields override global `defaults.orchestrator:` fields. Missing per-agent fields fall back to global defaults. Missing global defaults use hardcoded sensible values.

- **Pro:** Clean grouping — orchestrator config is self-contained under a nested section.
- **Pro:** Follows TARSy's existing `defaults:` pattern for global settings.
- **Pro:** Easy to validate: if `type != orchestrator`, `orchestrator:` section is forbidden.
- **Pro:** Multiple orchestrators can share defaults while allowing per-agent tuning.
- **Pro:** Extensible — new orchestrator-specific config goes in the same section.

*Rejected: (A) flat fields on AgentConfig — pollutes shared struct with orchestrator-only fields.*

---

## Q6: Where does `sub_agents` override live in the config hierarchy? — DECIDED

> **Decision:** Option B — full hierarchy (chain + stage + stage-agent), consistent with other TARSy overrides.

Follow the existing override pattern (like `mcp_servers`, `llm_provider`, `max_iterations`). All levels are optional — use only what you need.

```yaml
agent_chains:
  investigation:
    sub_agents: [LogAnalyzer, MetricChecker, K8sInspector]  # Chain default
    stages:
      - name: triage
        sub_agents: [LogAnalyzer]  # Stage override (only log analysis)
        agents:
          - name: MyOrchestrator
            sub_agents: [LogAnalyzer, MetricChecker]  # Stage-agent override
```

In practice, most configs will only use one level — typically chain-level. The stage and stage-agent levels exist for flexibility but aren't required:

```yaml
# Typical usage — chain-level only
agent_chains:
  log-only-investigation:
    sub_agents: [LogAnalyzer]
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
```

If no `sub_agents` is specified at any level, the orchestrator sees all agents with a `description` (the global registry).

- **Pro:** Consistent with how TARSy handles other overrides — same mental model.
- **Pro:** Maximum flexibility when needed, simple when not.
- **Pro:** Optional at every level — no noise unless you want fine-grained control.

*Rejected: (A) chain-level only — inconsistent with other overrides, limits flexibility unnecessarily; (C) agent-level only — can't vary by chain.*

---

## Q7: How are sub-agent executions modeled in the DB? — DECIDED

> **Decision:** Option A — `AgentExecution` with `parent_execution_id`.

Sub-agent runs are `AgentExecution` records under the same Stage as the orchestrator, with a `parent_execution_id` linking to the orchestrator's execution.

```
Session → Stage → [OrchestratorExecution, SubExec1(parent=Orch), SubExec2(parent=Orch)]
```

Minimal schema change — one new nullable column. Reuses all existing `AgentExecution` infrastructure (status tracking, timeline events). Query is simple: `WHERE parent_execution_id = ?`.

The `UpdateStageStatus` function needs to filter out sub-agents (those with non-null `parent_execution_id`), and `expected_agent_count` excludes sub-agents. Both are targeted fixes.

*Rejected: (B) separate stage — new stage concept, complex ordering, dashboard impact; (C) new entity — duplicates AgentExecution infrastructure.*

---

## Q8: How is the orchestrator's task injected into the sub-agent prompt? — DECIDED

> **Decision:** Option A — task replaces alert data as the user message, but with a **custom sub-agent prompt template** (not the investigation template).

The task from `dispatch_agent` becomes the sub-agent's user message. But it does NOT use the existing investigation template (`FormatAlertSection` with `<!-- ALERT_DATA_START -->` markers, runbook, chain context). Sub-agents get a clean, task-focused template.

**Current investigation user message** (NOT used for sub-agents):
```
## Alert Details
### Alert Metadata
**Alert Type:** kubernetes
### Alert Data
<!-- ALERT_DATA_START -->
{"description": "...", "namespace": "..."}
<!-- ALERT_DATA_END -->

## Runbook Content
...

## Previous Stage Data
...
```

**New sub-agent user message** (custom template):
```
## Task

Find all 5xx errors for service-X in the last 30 min. Report: error count,
top error messages, time pattern.
```

Implementation — a new `buildSubAgentUserMessage` in `PromptBuilder`:

```go
func (b *PromptBuilder) buildSubAgentUserMessage(task string) string {
    var sb strings.Builder
    sb.WriteString("## Task\n\n")
    sb.WriteString(task)
    sb.WriteString("\n")
    return sb.String()
}
```

The prompt builder selects the template based on agent type or an `ExecutionContext.Task` field:

```go
// In BuildFunctionCallingMessages:
if execCtx.Task != "" {
    userContent = b.buildSubAgentUserMessage(execCtx.Task)
} else if isChat {
    userContent = b.buildChatUserMessage(execCtx)
} else {
    userContent = b.buildInvestigationUserMessage(execCtx, prevStageContext)
}
```

The system message still includes the sub-agent's `custom_instructions` + MCP server instructions (Tier 1-3), same as any agent.

- **Pro:** Clean separation — sub-agents see a task, not an alert investigation.
- **Pro:** No confusing `<!-- ALERT_DATA_START -->` markers for a non-alert context.
- **Pro:** Orchestrator is responsible for including relevant context in the task text ("natural language is the protocol").
- **Pro:** Minimal change — new `Task` field on `ExecutionContext`, one new template function, small branch in `BuildFunctionCallingMessages`.

*Rejected: (B) task + alert data both — risk of conflicting instructions, over-complex prompt; (C) task in AlertData, alert in prevStageContext — stretches field semantics.*

---

## Q9: What dashboard changes are needed and how to phase them? — DECIDED

> **Decision:** Option B — tree view from the start. Sub-agent executions must be visually distinguishable from the orchestrator and show the parent-child relationship.

The dashboard needs to show:
1. Sub-agent executions with their own timelines (not hidden behind the orchestrator)
2. Clear parent-child relationship — which execution is the orchestrator, which are sub-agents
3. Sub-agent status, results, and timing

The initial implementation doesn't need to be fancy — a simple tree/nested view where sub-agents are indented or grouped under their parent orchestrator is sufficient. But the hierarchy must be visible from day one. Operators debugging an investigation need to see which sub-agent produced bad data and trace it back to the orchestrator's dispatch decision.

The backend already records everything needed (`parent_execution_id`, timeline events). The frontend queries `parent_execution_id` to build the tree.

- **Pro:** Correct mental model from day one — operators see the investigation flow.
- **Pro:** Essential for debugging — "which sub-agent gave bad data?"
- **Pro:** Can start simple (nested list) and refine later (full tree visualization).

*Rejected: (A) flat view with markers — clutters the orchestrator timeline; (C) no changes — sub-agents look like parallel stage agents, confusing.*

---

## Q10: How are SubAgentRunner dependencies injected? — DECIDED

> **Decision:** Option A — pass a dependency bundle struct.

The session executor creates a `SubAgentDeps` bundle from its own fields and passes it to the `SubAgentRunner`. Explicit, testable, no circular references.

```go
type SubAgentDeps struct {
    Config         *config.Config
    AgentFactory   *agent.AgentFactory
    MCPFactory     *mcp.ClientFactory
    LLMClient      agent.LLMClient
    EventPublisher agent.EventPublisher
    PromptBuilder  agent.PromptBuilder
    DBClient       *ent.Client
}
```

Option C (extract shared execution logic) is a nice-to-have refactor that can happen once the MVP works.

*Rejected: (B) interface — more indirection, potential circular dependency; (C) extract shared logic — good refactor but not needed for MVP.*

---

## Q11: What is the implementation phasing? — DECIDED

> **Decision:** Option B — horizontal layers. Small, reviewable PRs.

1. **PR1: Config foundation** — `sub_agents` override at chain/stage/agent level, `orchestrator` nested config section, `defaults.orchestrator` globals, validation
2. **PR2: DB schema** — `parent_execution_id`, `task` on `AgentExecution`, `UpdateStageStatus` filter
3. **PR3: SubAgentRunner + CompositeToolExecutor** — core orchestration types, dispatch/cancel/list, results channel
4. **PR4: Controller modification + orchestrator prompt** — push-based drain/wait logic in `IteratingController`, `buildSubAgentUserMessage`, agent catalog in system prompt
5. **PR5: Session executor wiring** — detect orchestrator → create runner + composite executor, `SubAgentDeps` bundle
6. **PR6: Dashboard** — tree view with parent-child hierarchy

Each PR is self-contained and reviewable. Config (PR1) and DB (PR2) are independently useful. Feature becomes testable after PR5. Dashboard follows as a separate stream.

*Rejected: (A) vertical slice — large initial PR, harder to review; (C) feature-flagged — no flag infrastructure, dead code.*

---

## Summary

| # | Question | Recommendation |
|---|----------|----------------|
| Q1 | Orchestrator identification | Existing `type` field — `AgentTypeOrchestrator` (ADR-0001) |
| Q2 | Tool combination approach | CompositeToolExecutor (wrapper pattern) |
| Q3 | Orchestration tool naming | Plain names (`dispatch_agent`, `cancel_agent`, `list_agents`) |
| Q4 | Controller approach | Reuse IteratingController + push-based result injection |
| Q5 | Guardrail config location | Nested `orchestrator` section + global defaults under `defaults:` |
| Q6 | `sub_agents` override hierarchy | Full hierarchy (chain + stage + stage-agent), all optional |
| Q7 | Sub-agent DB model | `parent_execution_id` on `AgentExecution` |
| Q8 | Task injection into sub-agent | Custom sub-agent template (`## Task` + task text) |
| Q9 | Dashboard phasing | Tree view from the start — simple but hierarchical |
| Q10 | Dependency injection | Dependency bundle struct (`SubAgentDeps`) |
| Q11 | Implementation phasing | Horizontal layers (6 PRs: config → DB → runner → controller → wiring → dashboard) |
