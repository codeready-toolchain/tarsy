# Implicit Orchestrator — Design Questions

**Status:** All decisions resolved
**Related:** [Design document](implicit-orchestrator-design.md)

---

## Q1: How do chat agents get their sub-agents?

Chat agents currently have no sub-agent configuration path. We need to decide where chat sub-agents come from.

### Option C: Both — `ChatConfig.SubAgents` overrides chain-level (chosen)

Add a `SubAgents` field to `ChatConfig` but fall back to `chain.SubAgents` when the chat field is empty. Mirrors how `ChatConfig.MCPServers` already works (explicit override > chain-level > aggregated).

```yaml
# Chat inherits chain sub_agents automatically:
agent_chains:
  my-chain:
    sub_agents: [LogAnalyzer, MetricChecker]
    chat:
      enabled: true

# Or chat overrides with a subset:
agent_chains:
  my-chain:
    sub_agents: [LogAnalyzer, MetricChecker, DangerousRemediator]
    chat:
      enabled: true
      sub_agents: [LogAnalyzer, MetricChecker]  # no DangerousRemediator
```

- **Pro:** Works for both "just inherit" and "I need different agents for chat."
- **Pro:** Follows established precedent (`ChatConfig.MCPServers` already uses this pattern).
- **Con:** Slightly more complex resolution logic (but the pattern is already established).

**Decision:** Option C — matches the existing `MCPServers` precedence pattern in `ResolveChatAgentConfig`. Zero-config defaults with explicit override support.

_Considered and rejected: Option A (explicit-only `ChatConfig.SubAgents` — forces duplication for common case), Option B (chain inheritance only — no way to restrict chat sub-agents)_

---

## Q2: Should `AgentTypeOrchestrator` be removed or kept?

Once orchestration is triggered by sub-agents, the `orchestrator` type value in `AgentType` becomes redundant for runtime behavior. The question is whether to keep it.

### Option A: Remove entirely (chosen)

Delete `AgentTypeOrchestrator` from `enums.go`, the controller factory, all switch cases, built-in agents, and tests. The `Orchestrator` built-in agent becomes `AgentTypeDefault` with `CustomInstructions`.

- **Pro:** Clean — no dead code, no ambiguity about what the type means.
- **Pro:** Users can't misconfigure (setting `type: orchestrator` without sub-agents).
- **Con:** Breaking change for existing YAML configs that use `type: orchestrator`.
- **Con:** Large diff touching many test files, golden files, and the E2E orchestrator config.

**Decision:** Option A — clean code, no leftovers. The type is fully redundant once orchestration is triggered by sub-agents. Remove everywhere.

_Considered and rejected: Option B (keep as cosmetic hint — dead code confuses contributors), Option C (deprecation path — unnecessary complexity at this stage)_

---

## Q3: How should orchestrator prompts be integrated?

Orchestration is not a separate prompt path — it's an additive capability. Any agent with tools (investigation, chat, action, or future types) that resolves sub-agents should get orchestrator prompt sections injected into its existing prompt. The current design with a dedicated `buildOrchestratorMessages` that replaces the entire prompt is wrong for the implicit model.

### Option A: Injection layer — append orchestrator sections to existing system prompt (chosen)

Remove `buildOrchestratorMessages` as a separate dispatch path. Instead, after building the normal system prompt (investigation, chat, action — whatever the agent is), append the orchestrator sections (behavioral strategy, agent catalog, result delivery rules) when `SubAgentCatalog` is non-empty.

The prompt builder flow becomes:
1. Build system + user messages via the existing path (investigation/chat/action/sub-agent)
2. If `len(execCtx.SubAgentCatalog) > 0`, append orchestrator behavioral instructions + catalog + result delivery to the system message

```
[Normal system prompt for agent type]
+ [Orchestrator Strategy section]        ← injected
+ [Available Sub-Agents catalog]         ← injected
+ [Result Delivery rules]                ← injected
```

- **Pro:** Any agent type with tools automatically becomes an orchestrator. No special dispatch needed.
- **Pro:** Eliminates the separate `buildOrchestratorMessages` code path entirely.
- **Pro:** Chat, investigation, action agents all get orchestrator capability the same way.
- **Pro:** The agent keeps its own identity/instructions — orchestration is layered on top.
- **Con:** Need to ensure the injected sections don't conflict with agent-specific instructions.

**Decision:** Option A — orchestration is a capability, not an identity. The agent keeps its normal prompt and gains orchestrator tools + instructions as an additive layer. `buildOrchestratorMessages` is eliminated as a separate dispatch path; orchestrator sections are injected into whatever prompt the agent already has.

_Considered and rejected: Option B (keep separate dispatch, extend to all agent types — loses agent identity, requires N variants)_

---

## Q4: Should the `Orchestrator` config block be allowed on any agent?

Currently, `validator.go` rejects `orchestrator:` config on non-`type: orchestrator` agents. With implicit orchestration, a `default`-type agent may need guardrail overrides.

### Option A: Allow `orchestrator:` on any agent (chosen)

Remove the validator restriction (which was tied to `type: orchestrator`, now deleted per Q2). Any agent definition can include `orchestrator:` to customize max_concurrent_agents, agent_timeout, max_budget. If the agent never resolves sub-agents at runtime, the block is ignored.

- **Pro:** Flexible — users can define guardrails for agents that might be used as orchestrators in some chains but not others.
- **Pro:** Simple rule: "if present, it's validated; if agent gets sub-agents at runtime, it's applied."
- **Con:** Slightly confusing: an agent config has `orchestrator:` but no `sub_agents` anywhere.

Note: `deploy/config/tarsy.yaml.example` is missing the `orchestrator:` block — add it as part of this work.

**Decision:** Option A — the validator restriction is deleted as part of Q2 cleanup. The `orchestrator:` block is allowed on any agent and applied when sub-agents are resolved.

_Considered and rejected: Option B (cross-reference chains — complex, fragile), Option C (expand restriction — same complexity as B)_

---

## Q5: How do we prevent circular dispatch without the type marker?

Currently, `BuildSubAgentRegistry` excludes `type: orchestrator` agents, and `validateSubAgentRefs` rejects orchestrator agents as sub-agents. Without the type as a reliable marker, we need an alternative.

### Moot — runtime already prevents sub-agent recursion

Sub-agents cannot become orchestrators by design:

1. `SubAgentRunner` creates a fresh `ExecutionContext` with `SubAgent` set (task-only mode)
2. The prompt builder sees `execCtx.SubAgent != nil` → builds task-only conversation, no orchestrator instructions
3. The sub-agent's `ToolExecutor` is a plain MCP executor — no `CompositeToolExecutor`, no `dispatch_agent` tool

Even if an orchestrator-capable agent is listed as a sub-agent, it runs in task-only mode without orchestrator tools. No circularity is possible at runtime.

**Decision:** Remove the `type == AgentTypeOrchestrator` exclusion from `BuildSubAgentRegistry` and `validateSubAgentRefs` without adding a replacement. The runtime sub-agent execution path is the prevention. Budget/timeout guardrails remain as a safety net.

---

## Q6: Should memory support detection be type-based or capability-based?

`agentTypeSupportsMemory` returns `true` for `AgentTypeDefault`, `AgentTypeAction`, and `AgentTypeOrchestrator`. With implicit orchestrators that may be `AgentTypeDefault`, the check is already correct (default supports memory). The question is whether to clean this up.

### Moot — subsumed by Q2

Q2 decided to remove `AgentTypeOrchestrator` entirely. Implicit orchestrators are `AgentTypeDefault`, which already returns `true` in `agentTypeSupportsMemory`. The dead `AgentTypeOrchestrator` case is simply deleted as part of Q2 cleanup. No design decision needed.

---

## Q7: Should chat orchestrator be in the same PR as the core change?

The core change (sub-agent-driven trigger) and chat orchestrator support are logically related but different in scope.

**Hard constraint:** After every PR, TARSy must be fully functional. Config changes are acceptable, but no PR may leave orchestration (or any feature) broken regardless of configuration. The final code must be clean — no dead or legacy code.

### Decision: Two PRs

**PR1: Core refactor** — Sub-agent-driven orchestration trigger + `AgentTypeOrchestrator` removal + prompt injection model.

- Existing orchestrator chains already have `sub_agents` configured → orchestration keeps working, just triggered by sub-agents instead of type.
- Configs need updating: remove `type: orchestrator` from agent definitions (it becomes an invalid value). All other YAML syntax unchanged.
- Investigation orchestration: fully functional.
- Chat orchestration: not yet available (same as before — no regression).

**PR2: Chat orchestrator** — `ChatConfig.SubAgents`, chat executor wiring, prompt injection for chat path.

- Purely additive. Only configs that opt into `chat.sub_agents` are affected.
- No regressions — existing configs without `chat.sub_agents` behave identically.
