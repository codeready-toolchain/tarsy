# ADR-0015: Implicit Orchestrator

**Status:** Implemented
**Date:** 2026-04-02

## Overview

Previously, an agent required `type: orchestrator` to gain sub-agent dispatch capabilities. This coupled orchestration to agent identity rather than configuration, making it impossible for chat agents (or any other agent type) to dispatch sub-agents, and forcing separate agent definitions for orchestrator vs. sub-agent roles.

This ADR makes orchestration an **additive capability**: any agent that resolves a non-empty sub-agent catalog at runtime automatically receives orchestrator tools (`dispatch_agent`, `cancel_agent`, `list_agents`) and orchestrator prompt sections injected into its existing system prompt. The `AgentTypeOrchestrator` enum value and the built-in `Orchestrator` agent are removed entirely.

**Supersedes orchestrator-specific aspects of:** [ADR-0002: Orchestrator Agent](0002-orchestrator-impl.md) (the orchestrator runtime mechanics — `CompositeToolExecutor`, `SubAgentRunner`, push-based result collection, guardrails, DB schema, dashboard integration — remain as described in ADR-0002; only the trigger and prompt construction are changed)

## Design Principles

- **Orchestration is a capability, not an identity.** An investigation agent with sub-agents is still an investigation agent. A chat agent with sub-agents is still a chat agent. They gain orchestrator tools and instructions additively.
- **Single trigger.** Orchestrator wiring is gated on exactly one condition: the filtered sub-agent catalog is non-empty after resolving refs and intersecting with the registry. This applies uniformly across investigation and chat execution paths.
- **Additive injection.** Orchestrator prompt sections (behavioral strategy, agent catalog, result delivery rules) are appended to the agent's existing system prompt. No separate prompt path.
- **Convention over configuration.** Sub-agents present = orchestrator mode. One source of truth.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| D1 | Remove `AgentTypeOrchestrator`? | **Remove entirely** | Clean code — the type is fully redundant once orchestration is triggered by sub-agents. No dead code, no ambiguity. |
| D2 | Remove built-in `Orchestrator` agent? | **Remove** | A dedicated orchestrator agent contradicts "capability, not identity" — any agent with sub-agents gains orchestration. |
| D3 | How to integrate orchestrator prompts? | **Injection layer** — append orchestrator sections to the existing system prompt | The agent keeps its identity and instructions; orchestration is layered on top. Eliminates the separate `buildOrchestratorMessages` code path. |
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

The same trigger logic applies in both the investigation executor and the chat executor.

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

The user message is unaffected — it stays whatever the agent type produces (investigation context, chat question, etc.).

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
| `"action"` | IteratingController | Automated remediation with safety prompt |
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
      agent_timeout: 300s

agent_chains:
  orchestrator-investigation:
    stages:
      - name: investigation
        agents:
          - name: KubernetesAgent
            sub_agents: [WebResearcher, CodeExecutor, GeneralWorker]
```

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

- **Action agents with orchestration**: The action prompt path currently returns early before orchestrator prompt injection. If needed, action agents could gain orchestration support with additional prompt integration work.
- **Stage-level orchestrator guardrails**: Currently, `orchestrator:` guardrails are resolved from agent definitions and defaults only. Stage-level overrides could be added if needed.
