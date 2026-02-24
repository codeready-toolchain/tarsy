# Orchestrator Agent — Implementation Questions

**Status:** Open — decisions pending
**Related:** [Design document](orchestrator-impl-design.md)
**Vision:** [orchestrator-agent-design.md](orchestrator-agent-design.md)
**Last updated:** 2026-02-19

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How is an orchestrator agent identified in config?

Currently there is no `type` field on agents. Behavior is determined by `IterationStrategy` (→ controller selection) and `IsValidForScoring()` (→ agent wrapper). We need a way to mark an agent as an orchestrator so the session executor can wire up the `CompositeToolExecutor`.

### Option A: New `type` field on `AgentConfig`

Add `type: orchestrator` to agent config. The `type` determines *what* the agent does, while `iteration_strategy` determines *how* the LLM is called. Orchestrators still use `native-thinking` or `langchain` for their own LLM calls.

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    iteration_strategy: native-thinking
    description: "Dynamic investigation orchestrator"
```

- **Pro:** Clean separation of concerns — type vs strategy are orthogonal.
- **Pro:** Explicit, self-documenting config.
- **Pro:** Extensible to future agent types.
- **Con:** New concept in config. Currently TARSy has no `type` field.

### Option B: New `IterationStrategy` value

Add `orchestrator` or `orchestrator-native-thinking` as iteration strategies. The controller factory returns an `OrchestratorController`.

```yaml
agents:
  MyOrchestrator:
    iteration_strategy: orchestrator-native-thinking
    description: "Dynamic investigation orchestrator"
```

- **Pro:** Fits existing pattern — strategy determines behavior.
- **Pro:** No new field needed.
- **Con:** Conflates two concerns — "orchestrate sub-agents" is not an iteration strategy, it's a capability.
- **Con:** Would need `orchestrator-native-thinking` AND `orchestrator-langchain` variants (combinatorial explosion).

### Option C: Convention-based — detect from config presence

If an agent has `max_concurrent_agents` or similar orchestrator-only config, treat it as an orchestrator. No explicit type field.

- **Pro:** No new field.
- **Con:** Implicit, easy to misconfigure.
- **Con:** Harder to validate.

**Recommendation:** Option A. `type` is a clean, explicit concept that separates *what* (orchestrator) from *how* (native-thinking/langchain). Avoids combinatorial explosion of iteration strategies.

---

## Q2: How are orchestration tools combined with MCP tools?

The orchestrator needs both MCP tools (for its own direct queries, if configured) and orchestration tools (`dispatch_agent`, `get_result`, `cancel_agent`). These must appear as a unified tool set to the LLM.

### Option A: CompositeToolExecutor (wrapper pattern)

Create a `CompositeToolExecutor` that wraps the existing MCP `ToolExecutor` and adds orchestration tools. Implements the same `ToolExecutor` interface. Routing: if tool name matches an orchestration tool, handle internally; otherwise delegate to MCP.

```go
type CompositeToolExecutor struct {
    mcpExecutor agent.ToolExecutor
    runner      *SubAgentRunner
}
```

- **Pro:** No changes to `ToolExecutor` interface or `FunctionCallingController`.
- **Pro:** Clean composition — MCP executor is unaware of orchestration.
- **Pro:** Session executor wires it up; the controller just sees tools.
- **Con:** Orchestration tool execution has side effects (spawning goroutines) — more complex than a simple tool call.

### Option B: Extend MCP ToolExecutor to support virtual tools

Add a "virtual tool" registration mechanism to the existing MCP `ToolExecutor` so it can handle non-MCP tools.

- **Pro:** Single executor, no wrapper.
- **Con:** Pollutes the MCP executor with non-MCP concerns.
- **Con:** Tight coupling between MCP and orchestration.

### Option C: Controller intercepts orchestration calls

The controller checks each tool call before delegating to `ToolExecutor`. If it's an orchestration tool, handle it directly in the controller.

- **Pro:** Explicit — controller knows about orchestration.
- **Con:** Requires a new controller or significant changes to `FunctionCallingController`.
- **Con:** Breaks the "controller is strategy-agnostic" principle.

**Recommendation:** Option A. The wrapper pattern keeps everything composable. The `FunctionCallingController` doesn't need to know it's running an orchestrator — it just calls tools and feeds results back to the LLM.

---

## Q3: How are orchestration tools named to avoid MCP collisions?

MCP tools use `server.tool` naming convention (e.g., `kubernetes-server.get_pod`). Orchestration tools need names that can't collide with MCP tool names.

### Option A: Plain names without prefix

`dispatch_agent`, `get_result`, `cancel_agent`. Simple and clean.

- **Pro:** Most natural names for the LLM.
- **Pro:** No prefix noise in prompts.
- **Con:** Could theoretically collide if an MCP server was named "dispatch_agent" — extremely unlikely.

### Option B: Prefixed names

`orchestrator.dispatch_agent`, `orchestrator.get_result`, `orchestrator.cancel_agent`. Uses the same `server.tool` convention as MCP.

- **Pro:** Consistent with MCP naming pattern.
- **Pro:** Zero collision risk.
- **Con:** Longer names, more verbose in LLM tool calls.

### Option C: Underscore-prefixed

`_dispatch_agent`, `_get_result`, `_cancel_agent`. Special prefix signals "internal" tools.

- **Pro:** Short names.
- **Pro:** Convention signals these aren't MCP tools.
- **Con:** Unconventional — LLMs might not handle leading underscores well.

**Recommendation:** Option A. The names are unique enough in practice (`dispatch_agent` is not a plausible MCP server name since MCP servers use hyphenated names like `kubernetes-server`). The `CompositeToolExecutor` routes by exact name match, so even if a collision existed, it would be caught at config validation time.

---

## Q4: Does the orchestrator need a new controller?

The `FunctionCallingController` runs the iteration loop: build messages → call LLM → process tool calls → repeat. The orchestrator follows the same loop, just with additional tools.

### Option A: Reuse FunctionCallingController as-is

The orchestrator uses the exact same `FunctionCallingController`. All orchestration behavior comes from the `CompositeToolExecutor`. The controller doesn't know it's orchestrating.

The only gap: when the orchestrator finishes (no more tool calls, or forced conclusion), it needs to wait for all sub-agents to complete and cancel any stragglers. This cleanup happens in the agent's `Execute()` method (post-controller), not in the controller itself.

- **Pro:** Zero controller changes. Maximum reuse.
- **Pro:** The `CompositeToolExecutor` is the single point of orchestration logic.
- **Con:** Post-execution cleanup (WaitAll) needs to happen somewhere — adds logic to agent wrapper or executor.

### Option B: New OrchestratorController wrapping FunctionCallingController

An `OrchestratorController` that embeds or delegates to `FunctionCallingController`, adding pre/post hooks for sub-agent lifecycle management.

```go
type OrchestratorController struct {
    inner  *FunctionCallingController
    runner *SubAgentRunner
}

func (c *OrchestratorController) Run(ctx, execCtx, prevStageContext) (*ExecutionResult, error) {
    defer c.runner.WaitAll(ctx)  // Cleanup
    return c.inner.Run(ctx, execCtx, prevStageContext)
}
```

- **Pro:** Clean lifecycle hooks.
- **Pro:** Sub-agent cleanup is explicit.
- **Con:** New type, more wiring in the factory.
- **Con:** The `SubAgentRunner` would be referenced both by the controller (for WaitAll) and by the `CompositeToolExecutor` (for Dispatch/GetResult/Cancel).

### Option C: New standalone OrchestratorController

Fork `FunctionCallingController` into a separate `OrchestratorController` with orchestration logic built into the iteration loop.

- **Pro:** Full control, can add orchestration-specific iteration logic.
- **Con:** Code duplication with `FunctionCallingController`.
- **Con:** Maintenance burden — any fix to the FC iteration loop must be replicated.

**Recommendation:** Option A if the post-cleanup can be handled cleanly (e.g., by the `OrchestratorAgent` wrapper), otherwise Option B. The iteration loop itself doesn't need changes — orchestration tools are just tools.

---

## Q5: Where do orchestrator guardrails live in config?

The orchestrator needs configurable limits: max concurrent sub-agents, per-sub-agent timeout, total budget. These are orchestrator-specific and don't apply to regular agents.

### Option A: Flat fields on `AgentConfig`

Add `max_concurrent_agents`, `agent_timeout`, `max_budget` directly to `AgentConfig`. Ignored for non-orchestrator agents.

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    max_concurrent_agents: 5
    agent_timeout: 300s
```

- **Pro:** Simple, flat config.
- **Pro:** Follows existing pattern — all agent config is flat.
- **Con:** Adds orchestrator-only fields to a shared struct. Validation needs to reject these on non-orchestrator agents.

### Option B: Nested `orchestrator` section

Group orchestrator-specific config under a sub-section:

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    orchestrator:
      max_concurrent_agents: 5
      agent_timeout: 300s
      max_budget: 600s
```

- **Pro:** Clean grouping — orchestrator config is self-contained.
- **Pro:** Easier to validate: if `type != orchestrator`, `orchestrator:` section is forbidden.
- **Pro:** Extensible — new orchestrator-specific config goes here.
- **Con:** Slightly deeper nesting.

### Option C: Global defaults + per-agent override

Global `orchestrator_defaults` section in config, overridable per agent.

```yaml
orchestrator_defaults:
  max_concurrent_agents: 5
  agent_timeout: 300s

agents:
  MyOrchestrator:
    type: orchestrator
```

- **Pro:** Centralized defaults.
- **Con:** New top-level config section.
- **Con:** Harder to reason about which value applies.

**Recommendation:** Option B. Nesting keeps orchestrator config cleanly separated and makes validation straightforward. Global defaults (Option C) can be layered on later if multiple orchestrators share the same settings.

---

## Q6: Where does `sub_agents` override live in the config hierarchy?

The vision doc says `sub_agents` can be overridden at chain/stage/agent level. We need to decide where it fits in the existing config hierarchy.

### Option A: Chain-level only

```yaml
agent_chains:
  focused:
    sub_agents: [LogAnalyzer, MetricChecker]
```

- **Pro:** Simple — one override level.
- **Con:** Can't scope differently per stage or orchestrator within a chain.

### Option B: Chain + stage + stage-agent level (full hierarchy)

Follow the existing override pattern (like `mcp_servers`, `llm_provider`, `max_iterations`):

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

- **Pro:** Consistent with how TARSy handles other overrides.
- **Pro:** Maximum flexibility.
- **Con:** Complex — hard to reason about which level applies.
- **Con:** `sub_agents` on `StageAgentConfig` adds fields to a widely-used struct.

### Option C: Agent-level in `AgentConfig`

Put `sub_agents` on the agent definition itself:

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    sub_agents: [LogAnalyzer, MetricChecker]
```

- **Pro:** Self-contained — orchestrator definition includes its scope.
- **Con:** Can't vary by chain (same orchestrator, different sub-agent sets).

**Recommendation:** Option A for v1 (chain-level only). It covers the main use case (different chains with different sub-agent scopes) without adding complexity to stage/agent config. Option B can be added later by extending `StageConfig` and `StageAgentConfig` if needed.

---

## Q7: How are sub-agent executions modeled in the DB?

Sub-agents spawned by the orchestrator need DB representation for timeline tracking and observability. The current hierarchy is: Session → Stage → AgentExecution.

### Option A: AgentExecution with `parent_execution_id`

Sub-agent runs are `AgentExecution` records under the same Stage as the orchestrator, with a `parent_execution_id` linking to the orchestrator's execution.

```
Session → Stage → [OrchestratorExecution, SubExec1(parent=Orch), SubExec2(parent=Orch)]
```

- **Pro:** Minimal schema change — one new nullable column.
- **Pro:** Reuses existing `AgentExecution` infrastructure (status tracking, timeline events).
- **Pro:** Query is simple: `WHERE parent_execution_id = ?`.
- **Con:** `agent_index` semantics change — sub-agents get dynamic indices.
- **Con:** `expected_agent_count` on Stage won't account for sub-agents (it's set at stage creation).
- **Con:** Stage status aggregation (`UpdateStageStatus`) would see sub-agents and try to aggregate — needs adjustment.

### Option B: Separate Stage for sub-agents

Create a "sub-stage" for each orchestrator's sub-agents, separate from the orchestrator's own stage.

```
Session → Stage(orchestrator) → [OrchestratorExecution]
       → SubStage(dynamic)   → [SubExec1, SubExec2, ...]
```

- **Pro:** Clean separation — sub-agents don't pollute the orchestrator's stage.
- **Pro:** Stage status aggregation works naturally on the sub-stage.
- **Con:** New stage concept (not in chain config) — requires changes to stage creation logic.
- **Con:** Stage index / ordering becomes complex.
- **Con:** Dashboard stage rendering affected.

### Option C: New `SubAgentExecution` entity

A new DB entity specifically for orchestrator sub-agents, separate from `AgentExecution`.

- **Pro:** Complete separation — no impact on existing entities.
- **Con:** Duplicates much of `AgentExecution` (status, timing, error handling).
- **Con:** Timeline events need to support a new parent entity.
- **Con:** Most infrastructure would need to be duplicated.

**Recommendation:** Option A. The `parent_execution_id` approach is the simplest schema change and reuses all existing infrastructure. The `UpdateStageStatus` function needs to filter out sub-agents (those with non-null `parent_execution_id`), but that's a targeted fix. `expected_agent_count` can exclude sub-agents.

---

## Q8: How is the orchestrator's task injected into the sub-agent prompt?

When the orchestrator dispatches a sub-agent with `dispatch_agent(name="LogAnalyzer", task="Find 5xx errors...")`, the task needs to reach the sub-agent's LLM as the primary input. Currently, the user message in `BuildFunctionCallingMessages` contains alert data + previous stage context.

### Option A: Task replaces alert data in the user message

When building the sub-agent's prompt, use the `task` text as the alert data. The sub-agent sees:
- System: custom_instructions + MCP instructions
- User: task from orchestrator (replacing alert data)

The `ExecutionContext.AlertData` field is set to the task text.

- **Pro:** Simplest — reuses existing prompt building exactly.
- **Pro:** Sub-agent doesn't know it was dispatched by an orchestrator.
- **Con:** Previous stage context is empty (sub-agents have no "previous stage").
- **Con:** Original alert data is not visible to the sub-agent (unless orchestrator includes it in the task).

### Option B: Task as separate field, alert data still included

Add a `Task` field to `ExecutionContext`. The prompt builder includes both: task as the primary request, alert data as background context.

- **Pro:** Sub-agent sees both the specific task and the original alert context.
- **Pro:** Richer context for the sub-agent's investigation.
- **Con:** More complex prompt construction.
- **Con:** Risk of conflicting instructions (task says one thing, alert implies another).

### Option C: Task in AlertData, original alert in prevStageContext

Use `AlertData` for the task, and pass the original alert data as `prevStageContext` so it appears as background context.

- **Pro:** Sub-agent gets both task and alert context.
- **Pro:** Reuses existing fields, no new concept.
- **Con:** `prevStageContext` semantics are stretched — it's not actually previous stage context.

**Recommendation:** Option A for v1. The orchestrator is responsible for including relevant alert context in the task description — this is part of the "natural language is the protocol" principle. If the orchestrator writes good tasks, the sub-agent has everything it needs. The original alert data is available to the orchestrator and it can include relevant parts in the task.

---

## Q9: What dashboard changes are needed and how to phase them?

The dashboard currently shows a flat timeline per execution. The orchestrator introduces a parent-child execution tree.

### Option A: MVP — flat view with sub-agent markers

Show sub-agent executions as timeline events within the orchestrator's timeline (like tool calls). Click to expand into the sub-agent's full timeline.

- **Pro:** Minimal dashboard change — sub-agent dispatches appear as tool call events.
- **Pro:** Clickable expansion to sub-agent timelines.
- **Con:** Orchestrator timeline gets cluttered with sub-agent events.

### Option B: Tree view from the start

Build a proper execution tree view: orchestrator at the root, sub-agents as expandable child nodes, each with their own timeline.

- **Pro:** Correct visualization from day one.
- **Pro:** Matches the mental model of the trace tree.
- **Con:** Significant frontend work.
- **Con:** Delays MVP.

### Option C: No dashboard changes for v1

Sub-agent executions appear in the existing stage view as additional executions (alongside the orchestrator). Timeline events are visible but not hierarchically organized.

- **Pro:** Zero frontend work.
- **Pro:** Sub-agent data is still visible and debuggable via the existing views.
- **Con:** No visual hierarchy — harder to understand the orchestrator's flow.
- **Con:** Sub-agents may look like parallel agents in a regular stage.

**Recommendation:** Option C for initial implementation, then Option A as a fast follow. The backend records everything (timeline events, execution IDs, parent links). The existing dashboard shows all executions in a stage — sub-agents will appear there. Not ideal UX, but functional. Dashboard improvements can come after the backend is proven.

---

## Q10: How are SubAgentRunner dependencies injected?

The `SubAgentRunner` needs access to heavy infrastructure: `AgentFactory`, `ClientFactory` (MCP), `Config`, `PromptBuilder`, `LLMClient`, `EventPublisher`, and DB services. Currently these live in `RealSessionExecutor`.

### Option A: Pass a dependency bundle

Create a struct that bundles the dependencies the runner needs, extracted from the session executor:

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

The session executor creates this bundle and passes it to the `SubAgentRunner`.

- **Pro:** Explicit dependencies, testable (can mock the bundle).
- **Pro:** No circular references.
- **Con:** Another struct to maintain.

### Option B: Interface for sub-agent execution

Define an interface that the session executor implements:

```go
type SubAgentExecutor interface {
    ExecuteSubAgent(ctx context.Context, parentExecCtx *ExecutionContext, agentName, task string) (executionID string, resultCh <-chan *ExecutionResult, err error)
}
```

The `SubAgentRunner` gets this interface and delegates back to the session executor.

- **Pro:** Clean separation — runner doesn't know about internals.
- **Pro:** Session executor reuses its own `executeAgent` logic.
- **Con:** Potential for circular dependency if not careful with interfaces.
- **Con:** More indirection.

### Option C: Extract shared execution logic

Pull the "resolve config + create tools + create agent + execute" path into a standalone function that both `RealSessionExecutor.executeAgent` and `SubAgentRunner` call.

- **Pro:** Maximum code reuse, no duplication.
- **Pro:** Single source of truth for agent execution.
- **Con:** May require refactoring `executeAgent` which has many parameters.

**Recommendation:** Option A for simplicity. The dependency bundle is explicit and straightforward. Option C (extract shared logic) is a nice-to-have refactor that can happen once the MVP works. The session executor creates the bundle from its own fields and passes it to the runner.

---

## Q11: What is the implementation phasing?

The feature involves config changes, new Go types, DB schema changes, and eventually dashboard updates. How should we break this into shippable increments?

### Option A: Vertical slice — minimal end-to-end

Ship a minimal working orchestrator as a single PR (or small series):
1. Config: `description`, `type`, sub_agents override (chain-level)
2. Backend: `CompositeToolExecutor`, `SubAgentRunner`, orchestrator prompt
3. DB: `parent_execution_id` on `AgentExecution`
4. No dashboard changes (sub-agents visible as extra executions in stage)

Then iterate: guardrails, dashboard, polish.

- **Pro:** Working feature fastest.
- **Pro:** Real usage feedback early.
- **Con:** Large initial PR.

### Option B: Horizontal layers

1. PR1: Config foundation — `Description`, `Type`, `sub_agents`, validation
2. PR2: DB schema — `parent_execution_id`, `task` on AgentExecution
3. PR3: SubAgentRunner + CompositeToolExecutor
4. PR4: Orchestrator prompt building
5. PR5: Session executor wiring
6. PR6: Dashboard updates

- **Pro:** Small, reviewable PRs.
- **Pro:** Each PR is self-contained.
- **Con:** Feature isn't usable until PR5.
- **Con:** More PRs to coordinate.

### Option C: Feature-flagged single merge

Build everything behind a feature flag, merge in stages, enable when ready.

- **Pro:** Can merge incrementally without affecting production.
- **Pro:** Feature is tested end-to-end before enabling.
- **Con:** Feature flag infrastructure may not exist.
- **Con:** Dead code in production until enabled.

**Recommendation:** Option B. Small PRs are easier to review and test. The config changes (PR1) and DB schema (PR2) are independently useful (e.g., `Description` on agents is valuable even without the orchestrator). The feature becomes usable after PR5 and can be tested manually. Dashboard improvements follow as a separate stream.

---

## Summary

| # | Question | Recommendation |
|---|----------|----------------|
| Q1 | Orchestrator identification | New `type` field on `AgentConfig` |
| Q2 | Tool combination approach | CompositeToolExecutor (wrapper pattern) |
| Q3 | Orchestration tool naming | Plain names (`dispatch_agent`, etc.) |
| Q4 | Controller approach | Reuse FunctionCallingController |
| Q5 | Guardrail config location | Nested `orchestrator` section |
| Q6 | `sub_agents` override hierarchy | Chain-level only for v1 |
| Q7 | Sub-agent DB model | `parent_execution_id` on AgentExecution |
| Q8 | Task injection into sub-agent | Task replaces alert data |
| Q9 | Dashboard phasing | No changes for v1, sub-agents visible in existing views |
| Q10 | Dependency injection | Pass a dependency bundle struct |
| Q11 | Implementation phasing | Horizontal layers (small PRs) |
