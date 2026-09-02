# ADR-0015: Implicit Orchestrator

**Status:** Implemented
**Date:** 2026-04-02
**Amended by:** [ADR-0029: Sub-Agent Execution Limits](0029-sub-agent-execution-limits.md) (2026-09-02) — `agent_timeout` is optional, not a 300s default

## Overview

Previously, an agent required `type: orchestrator` to gain sub-agent dispatch capabilities. This coupled orchestration to agent identity rather than configuration, making it impossible for chat agents (or any other agent type) to dispatch sub-agents, and forcing separate agent definitions for orchestrator vs. sub-agent roles.

This ADR makes orchestration an **additive capability**: any agent that resolves a non-empty sub-agent catalog at runtime automatically receives orchestrator tools (`dispatch_agent`, `cancel_agent`, `list_agents`) and orchestrator prompt sections injected into its existing system prompt. The `AgentTypeOrchestrator` enum value and the built-in `Orchestrator` agent are removed entirely.

**Supersedes orchestrator-specific aspects of:** [ADR-0002: Orchestrator Agent](0002-orchestrator-impl.md) (the orchestrator runtime mechanics — `CompositeToolExecutor`, `SubAgentRunner`, push-based result collection, guardrails, DB schema, dashboard integration — remain as described in ADR-0002; only the trigger and prompt construction are changed)

## Design Principles

- **Orchestration is a capability, not an identity.** An investigation agent with sub-agents is still an investigation agent. A chat agent with sub-agents is still a chat agent. An action agent with sub-agents is still an action agent. They gain orchestrator tools and instructions additively.
- **Single trigger.** Orchestrator wiring is gated on exactly one condition: the filtered sub-agent catalog is non-empty after resolving refs and intersecting with the registry. The session executor (and chat executor) attach dispatch tools from that catalog. Prompt injection follows the same catalog. This applies to investigation, chat, and action agents.
- **Additive injection.** Orchestrator prompt sections (behavioral strategy, agent catalog, result delivery rules) are appended to the agent's existing system prompt. No separate prompt path.
- **Convention over configuration.** Sub-agents present = orchestrator mode. One source of truth.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D1 | Remove `AgentTypeOrchestrator`? | **Remove entirely** | Clean code — the type is fully redundant once orchestration is triggered by sub-agents. No dead code, no ambiguity. |
| D2 | Remove built-in `Orchestrator` agent? | **Remove** | A dedicated orchestrator agent contradicts "capability, not identity" — any agent with sub-agents gains orchestration. |
| D3 | How to integrate orchestrator prompts? | **Injection layer** — append orchestrator sections to the existing system prompt | The agent keeps its identity and instructions; orchestration is layered on top. Eliminates the separate `buildOrchestratorMessages` code path. Action agents keep the safety preamble and memo task focus; catalog sections are additive. |
| D4 | Allow `orchestrator:` guardrails on any agent? | **Yes** | The block is inert if the agent never resolves sub-agents. Resolved from `defaults.orchestrator` + agent definition's `orchestrator:` block. |
| D5 | How do chat agents get sub-agents? | **`ChatConfig.SubAgents` overrides chain-level** | Mirrors the established `ChatConfig.MCPServers` precedence pattern: `chat.sub_agents` > `chain.sub_agents` > none. |
| D6 | How to prevent circular dispatch? | **Runtime prevention (no explicit check needed)** | `SubAgentRunner` creates a fresh `ExecutionContext` with `SubAgent` set — sub-agents get a task-only prompt and a plain MCP executor with no orchestrator tools. Depth is always 1. |
| D7 | Memory support for implicit orchestrators? | **No change needed** | Implicit orchestrators are `AgentTypeDefault`, which already supports memory. The dead `AgentTypeOrchestrator` case was simply removed. |
| D8 | Stage-level skill overrides? | **Additive merge on `StageAgentConfig`** | `RequiredSkills` and `Skills` fields on `StageAgentConfig` and `SubAgentRef` are merged with agent-definition skills (appended + deduplicated). This enables a single agent to serve as both orchestrator and sub-agent with different skill sets depending on chain context. |

## Architecture

### Orchestration Trigger

**Before:**
```
resolvedConfig.Type == AgentTypeOrchestrator?
  YES → resolve sub-agents, build SubAgentRunner, wrap tools, build orchestrator prompts
  NO  → plain agent
```

**After:**
```
refs := resolveSubAgents(chain, stage, agentConfig)
catalog := registry.Filter(refs.Names())
  catalog non-empty → build SubAgentRunner, wrap tools, inject orchestrator prompt sections
  catalog empty     → plain agent
```

The same trigger logic applies in the investigation executor, the chat executor, and action stages. The executor attaches `dispatch_agent` / `cancel_agent` / `list_agents` whenever the catalog is non-empty; prompt sections follow that same catalog (tools are the contract).

### Sub-Agent Resolution Precedence

For investigation stages:
```
stage-agent sub_agents > stage sub_agents > chain sub_agents
```

For chat:
```
chat.sub_agents > chain.sub_agents
```

First non-empty wins. If all are empty/nil, no orchestration is activated.

### Prompt Injection Model

The separate `buildOrchestratorMessages` dispatch path is eliminated. Instead, orchestrator sections are injected into whatever system prompt the agent already has:

```
[Normal system prompt — investigation / chat / custom instructions]
+ [Orchestrator Strategy]           ← injected when SubAgentCatalog non-empty
+ [Available Sub-Agents catalog]    ← injected when SubAgentCatalog non-empty
+ [Result Delivery rules]           ← injected when SubAgentCatalog non-empty
```

The user message is unaffected — it stays whatever the agent type produces (investigation context, chat question, or action task). Action agents keep the safety preamble and action memo task focus; orchestrator sections are appended when the catalog is non-empty. They do not switch to the investigation orchestrator task-focus string.

### Stage-Level Skill Overrides

`StageAgentConfig` and `SubAgentRef` both gain `RequiredSkills` and `Skills` fields. Unlike `mcp_servers` and other stage-agent overrides which use replacement semantics, skills are **additive** — stage-level skills are appended to the agent definition's skills and deduplicated. This matches the nature of skills as cumulative knowledge injections rather than exclusive resource grants.

**Example:**
```yaml
agents:
  IncidentInvestigator:
    required_skills: [domain-knowledge, triage-runbook]

agent_chains:
  incident-orchestrated:
    stages:
      - stage_agents:
          - name: IncidentInvestigator
            required_skills: [incident-report-format]  # additive: merged → [domain-knowledge, triage-runbook, incident-report-format]
          sub_agents:
            - name: IncidentInvestigator
            # inherits agent-def skills: [domain-knowledge, triage-runbook]
```

### AgentType Values (After)

| AgentType | Controller | Purpose |
|-----------|-----------|---------|
| `""` (default) | IteratingController | Investigation agents (+ implicit orchestration when sub-agents present) |
| `"action"` | IteratingController | Automated remediation with safety prompt (+ implicit orchestration when sub-agents present) |
| `"synthesis"` | SingleShotController | Synthesis of parallel results |
| `"exec_summary"` | SingleShotController | Executive summary generation |
| `"scoring"` | ScoringController | Session quality evaluation |

### Built-in Agents (After)

| Agent | Type | Purpose |
|-------|------|---------|
| KubernetesAgent | default | Kubernetes troubleshooting |
| ChatAgent | default | Follow-up conversations |
| SynthesisAgent | synthesis | Synthesizes parallel investigations |
| ExecSummaryAgent | exec_summary | Executive summary generation |
| ScoringAgent | scoring | Session quality evaluation |
| WebResearcher | default | Web research (google_search + url_context) |
| CodeExecutor | default | Python computation (code_execution) |
| GeneralWorker | default | Pure reasoning |

The `Orchestrator` built-in is removed — any agent with sub-agents gains orchestration.

### Configuration Examples

**Investigation orchestrator (any agent with sub-agents):**
```yaml
agents:
  KubernetesAgent:
    description: "Kubernetes troubleshooting agent"
    mcp_servers: [kubernetes-server]
    orchestrator:
      max_concurrent_agents: 3
      # agent_timeout omitted → remaining parent (session/chat) time
      # agent_timeout: 20m     # optional dedicated cap, still clamped to parent

agent_chains:
  orchestrator-investigation:
    stages:
      - name: investigation
        agents:
          - name: KubernetesAgent
            sub_agents: [WebResearcher, CodeExecutor, GeneralWorker]
```

**Amendment (ADR-0029):** `agent_timeout: 300s` was shown here as if required. It is optional; omit it to use remaining parent time.

**Chat orchestrator (opt-in via chat.sub_agents):**
```yaml
agent_chains:
  my-chain:
    sub_agents: [LogAnalyzer, MetricChecker]
    chat:
      enabled: true
      sub_agents: [LogAnalyzer, MetricChecker]  # or omit to inherit chain-level
```

## Future Considerations

- **Stage-level orchestrator guardrails**: Currently, `orchestrator:` guardrails are resolved from agent definitions and defaults only. Stage-level overrides could be added if needed.

## Amendments ([ADR-0029](0029-sub-agent-execution-limits.md), 2026-09-02)

D4 (`orchestrator:` guardrails on any agent) still stands. ADR-0029 changes the meaning of `agent_timeout` in those blocks:

- This example originally listed `agent_timeout: 300s` as if it were required. The field is optional and has **no built-in duration**. Omitted → remaining parent (session/chat) time. If set → `min(configured, remaining parent)`.
- Unused `max_budget` (documented on [ADR-0002](0002-orchestrator-impl.md)) is removed, not added here.
- Iterating wrap-up on deadline (investigation, action, chat, sub-agent) is specified in ADR-0029.

**Action agents and orchestration.** The executor already attaches dispatch tools when an action agent’s sub-agent catalog is non-empty. Prompt construction matches that contract: the action prompt injects orchestrator sections when the catalog is set, while keeping the action safety preamble and memo task focus. The former “action prompt returns early” future item is closed.
