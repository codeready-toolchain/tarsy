# Orchestrator Agent — Design Document

**Status:** Vision complete — ready for implementation planning
**Related:** [Design questions & decision log](orchestrator-agent-questions.md)
**Last updated:** 2026-02-19

## Overview

The Orchestrator Agent introduces **dynamic, LLM-driven workflow orchestration** to TARSy. Instead of following a predefined chain of agents, the orchestrator uses LLM reasoning to decide which agents to invoke, what tasks to give them, and how to synthesize their results — all at runtime.

From TARSy's perspective, the orchestrator is just another agent in a chain. It receives input, produces text output, and follows the existing execution model. But internally, it opens the door to flexible, multi-agent investigation flows that adapt to each situation.

**This is an additive feature, not a rewrite.** Existing chains, agents, and execution remain unchanged.

## Design Principles

1. **Additive, not rewrite.** The orchestrator is a new capability layered on top of existing TARSy architecture. A single agent in an existing chain becomes the entry point to dynamic orchestration.

2. **Regular agent, special tools.** The orchestrator is a standard TARSy agent with three additional tools (`dispatch_agent`, `get_result`, `cancel_agent`). Everything else — config, MCP servers, custom_instructions, LLM selection — works identically to any other agent.

3. **LLM as the planner.** No hardcoded DAG or workflow engine. The orchestrator LLM reasons about input, available agents, and results to decide what to do, in what order, and when to stop.

4. **Config-driven infrastructure, LLM-driven tasks.** The LLM decides *what* to investigate and *what tasks* to give sub-agents. Config determines *how* things run — which MCP servers, which models, which agents are available. Clean separation.

5. **Natural language is the protocol.** Context flows between orchestrator and sub-agents as plain text. The orchestrator crafts task descriptions; sub-agents return findings as free text. No structured schemas.

6. **Controlled autonomy.** The orchestrator has freedom to choose agents and sequencing, but operates within configured guardrails (max concurrent agents, timeouts, allowed agent list, depth limit).

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Existing TARSy Chain                                       │
│                                                             │
│  ┌──────────┐   ┌──────────┐   ┌──────────┐                 │
│  │ Agent A  │──▶│ Agent B  │──▶│ Agent C  │──▶ ...          │
│  │ (static) │   │ (static) │   │ (static) │                 │
│  └──────────┘   └──────────┘   └──────────┘                 │
│                                                             │
│  OR: chain with orchestrator                                │
│                                                             │
│  ┌──────────┐   ┌──────────────────────────────┐            │
│  │ Agent A  │──▶│  Orchestrator Agent          │──▶ ...     │
│  │ (static) │   │                              │            │
│  └──────────┘   │  LLM reasons + tool calls:   │            │
│                 │  ┌─────────────────────────┐ │            │
│                 │  │ dispatch_agent(LogA, ..)│ │            │
│                 │  │ dispatch_agent(MetC, ..)│ │            │
│                 │  │ get_result(exec_abc)    │ │            │
│                 │  │ cancel_agent(exec_def)  │ │            │
│                 │  │ get_result(exec_abc)    │ │            │
│                 │  │ → synthesized output    │ │            │
│                 │  └─────────────────────────┘ │            │
│                 └──────────────────────────────┘            │
│                        │              │                     │
│                  ┌─────┘              └──────┐              │
│                  ▼                           ▼              │
│            ┌────────────┐              ┌────────────┐       │
│            │ LogAnalyzer│              │MetricCheck │       │
│            │ (sub-agent)│              │(sub-agent) │       │
│            │ MCP: loki  │              │MCP: prom   │       │
│            └────────────┘              └────────────┘       │
└─────────────────────────────────────────────────────────────┘
```

## Sub-Agents

Sub-agents are **regular TARSy agents** — both config agents (`agents:` in tarsy.yaml) and built-in agents (KubernetesAgent, ChatAgent, etc.). No new concept needed.

**Discovery:** All agents form a global registry that the orchestrator sees by default. The view can be restricted via `sub_agents` override at chain/stage/agent level, following TARSy's existing override patterns.

**Description:** Each agent is presented to the orchestrator LLM with its name, `description` (new optional field for config agents; built-ins already have it), and MCP servers list. The LLM infers dispatch decisions from this.

```yaml
agents:
  LogAnalyzer:
    description: "Analyzes logs from Loki to find error patterns and anomalies"
    mcp_servers: [loki]
    custom_instructions: "Focus on error patterns, correlate timestamps..."

  MetricChecker:
    description: "Queries Prometheus for metric anomalies and threshold breaches"
    mcp_servers: [prometheus]
    custom_instructions: "Check p99 latency, error rates, resource usage..."

  K8sInspector:
    description: "Inspects Kubernetes resources, pod status, and events"
    mcp_servers: [kubernetes]

agent_chains:
  dynamic-investigation:
    stages:
      - name: investigate
        agents:
          - name: Orchestrator
            type: orchestrator
            custom_instructions: |
              You are an SRE investigation orchestrator. Analyze the alert,
              dispatch relevant sub-agents to gather data, synthesize findings
              into a root cause analysis.

  # Override: restrict available sub-agents for a focused chain
  log-only-investigation:
    sub_agents: [LogAnalyzer]
    stages:
      - name: investigate
        agents:
          - name: Orchestrator
            type: orchestrator
```

## Orchestrator Tools

The orchestrator gets three tools in addition to any MCP server tools from its own config:

### `dispatch_agent`

Fire-and-forget. Spawns a sub-agent with a task, returns an execution ID immediately.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `name` | string | yes | Agent name from the registry |
| `task` | string | yes | Natural language task description |

Returns: `{ execution_id: string }`

### `get_result`

Retrieve status and result of a dispatched sub-agent.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `execution_id` | string | yes | ID from `dispatch_agent` |

Returns: `{ status: "running" | "completed" | "failed" | "cancelled", result?: string, error?: string }`

### `cancel_agent`

Cancel a running sub-agent. Extends TARSy's existing cancellation infrastructure (currently human-only) to LLM-driven cancellation.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `execution_id` | string | yes | ID from `dispatch_agent` |

Returns: `{ status: "cancelled" | "already_completed" | "not_found" }`

### Example Flow

```
Orchestrator receives: "Alert: service-X 5xx rate at 15%"

1. dispatch_agent(name="LogAnalyzer", task="Find all 5xx errors for service-X
   in the last 30 min. Report: error count, top error messages, time pattern.")
   → { execution_id: "exec-abc" }

2. dispatch_agent(name="MetricChecker", task="Check service-X latency, error rate,
   and CPU/memory for the last hour. Flag any anomalies.")
   → { execution_id: "exec-def" }

3. get_result(execution_id="exec-abc")
   → { status: "completed", result: "Found 2,847 5xx errors. 92% are
      'connection refused' to payments-db. Spike started at 14:23 UTC..." }

4. get_result(execution_id="exec-def")
   → { status: "running" }

5. dispatch_agent(name="K8sInspector", task="Check payments-db pod status,
   recent events, and restarts in the last hour.")
   → { execution_id: "exec-ghi" }

6. get_result(execution_id="exec-def")
   → { status: "completed", result: "p99 latency jumped from 120ms to 8.2s
      at 14:22. CPU nominal. Memory at 94% on payments-db pod." }

7. cancel_agent(execution_id="exec-def")  // already done, no-op
   → { status: "already_completed" }

8. get_result(execution_id="exec-ghi")
   → { status: "completed", result: "payments-db-0 OOMKilled at 14:22,
      restarted 3 times. Current memory limit: 512Mi." }

9. Orchestrator synthesizes: "Root cause: payments-db OOMKilled due to 512Mi
   memory limit. This caused connection refused errors from service-X,
   resulting in the 5xx spike starting 14:23 UTC."
```

## Configuration

The orchestrator is configured like any TARSy agent, with the `type: orchestrator` marker enabling the dispatch tools:

```yaml
agents:
  MyOrchestrator:
    type: orchestrator
    description: "Dynamic SRE investigation orchestrator"
    custom_instructions: |
      You investigate alerts by dispatching specialized sub-agents.
      Prefer parallel dispatch when sub-agents are independent.
      Synthesize all findings into a clear root cause analysis.
    # Optional: give orchestrator direct MCP access for quick checks
    # mcp_servers: [prometheus]

agent_chains:
  investigation:
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
```

**Everything is config-driven:**

| Aspect | Mechanism | Default |
|--------|-----------|---------|
| Available sub-agents | Global registry (all agents) | All config + built-in agents |
| Sub-agent scoping | `sub_agents` override at chain/stage/agent | All agents visible |
| MCP servers on sub-agents | `mcp_servers` on agent config | Per-agent config |
| Orchestrator's own MCP servers | `mcp_servers` on orchestrator config | None (pure coordinator) |
| LLM per agent | Agent-level LLM config | Chain/global default |
| Orchestrator instructions | `custom_instructions` | None |

## Execution Model

- **Async dispatch:** `dispatch_agent` returns immediately with an execution ID. The orchestrator LLM manages sequencing — serial, parallel, or mixed.
- **Depth 1 only:** Sub-agents cannot spawn their own sub-agents. Simple, predictable, debuggable.
- **Failure handling:** Sub-agent failures (error, timeout) are reported to the orchestrator with full context. The LLM decides what to do — retry, try a different agent, proceed with partial data, or report the failure. No auto-retry at orchestration level.

## Observability

**Full trace tree** with parent-child linking:

```
Execution: exec-001 (Orchestrator)
├── Sub-execution: exec-abc (LogAnalyzer)
│   ├── LLM call: analyze prompt
│   ├── MCP tool call: loki.query_range(...)
│   ├── LLM call: synthesize findings
│   └── Result: "Found 2,847 5xx errors..."
├── Sub-execution: exec-def (MetricChecker)
│   ├── LLM call: analyze prompt
│   ├── MCP tool call: prometheus.query(...)
│   └── Result: "p99 latency jumped..."
├── Sub-execution: exec-ghi (K8sInspector)
│   ├── LLM call: analyze prompt
│   ├── MCP tool call: kubernetes.get_pod(...)
│   └── Result: "payments-db-0 OOMKilled..."
└── Orchestrator synthesis: "Root cause: payments-db OOMKilled..."
```

Each sub-agent run gets its own timeline, linked to the orchestrator via parent execution ID. Reuses TARSy's existing timeline infrastructure.

## Guardrails

| Guardrail | Config | Default |
|-----------|--------|---------|
| Max concurrent sub-agents | `orchestrator.max_concurrent` | TBD |
| Per sub-agent timeout | `orchestrator.agent_timeout` | TBD |
| Total orchestrator budget | `orchestrator.max_budget` | TBD |
| Allowed sub-agents | `sub_agents` override | All agents |
| Max depth | Hardcoded | 1 (no nesting) |

## Context Flow

Natural language is the protocol at every level:

```
Alert data + prior agent results
        │
        ▼
┌─────────────────────┐
│  Orchestrator LLM   │
│                     │
│  Sees: agent list   │  System prompt includes:
│  with descriptions  │  - custom_instructions
│  + MCP servers      │  - available agents (name + description + MCP servers)
│                     │  - chain context (alert, prior results)
└────────┬────────────┘
         │
    dispatch_agent(task="...")   ◄── task is the orchestrator's "contract"
         │                          with the sub-agent: what to do, what to report
         ▼
┌─────────────────────┐
│  Sub-agent LLM      │
│                     │
│  Sees: task text    │  System prompt includes:
│  + MCP tools        │  - agent's custom_instructions
│  + alert context    │  - MCP server tools
│                     │  - task from orchestrator
└────────┬────────────┘
         │
    Free text result    ◄── sub-agent's findings, in natural language
         │
         ▼
┌─────────────────────┐
│  Orchestrator LLM   │
│                     │
│  Reasons over       │  Decides: dispatch more agents? synthesize? retry?
│  all results        │
│                     │
└────────┬────────────┘
         │
    Text output to chain  ◄── final synthesis, same as any agent
```

## Future Considerations

These features are explicitly deferred but the current design accommodates them:

### Skills System (Q7-Q8)
TARSy's `custom_instructions` already covers knowledge injection. A future skills system (reusable named blocks, compose-by-reference) would be a DX improvement. No architectural change needed — skills would inject into the same system prompt path.

### Memory (Q15)
Investigation outputs + trace data should be preserved (not discarded after chain completion). Future memory would:
- Index past investigation results (keyed by execution ID)
- Orchestrator searches memory before dispatching
- Relevant past context included in sub-agent task descriptions
- Injection point: `custom_instructions` or additional system prompt section

### Deeper Orchestration (Q10)
Depth 1 is the starting point. Depth 2+ could be added with a configurable max depth. The async dispatch model already supports it conceptually.

### LLM Model Override (Q9)
The orchestrator could gain an optional `model` parameter on `dispatch_agent` to override sub-agent models at runtime. Deferred because config-driven selection is sufficient for v1.

### Steer (Q4)
A `steer_agent` tool could inject new instructions into a running sub-agent, redirecting its work mid-execution. Deferred for v1.
