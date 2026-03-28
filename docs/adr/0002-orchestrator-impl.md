# ADR-0002: Orchestrator Agent

**Status:** Implemented  
**Date:** 2026-02-26

## Overview

The Orchestrator Agent introduces **dynamic, LLM-driven workflow orchestration** to TARSy. Instead of following a predefined chain of agents, the orchestrator uses LLM reasoning to decide which agents to invoke, what tasks to give them, and how to combine their results — all at runtime.

From TARSy's perspective, the orchestrator is just another agent in a chain. It receives input, produces text output, and follows the existing execution model. But internally, it opens the door to flexible, multi-agent investigation flows that adapt to each situation. **This is an additive feature, not a rewrite.** Existing chains, agents, and execution remain unchanged.

The orchestrator is a standard TARSy agent with three additional tools (`dispatch_agent`, `cancel_agent`, `list_agents`) and a push-based result collection model. Sub-agent results are automatically injected into the orchestrator's conversation as they complete — the LLM never polls for results.

## Design Principles

### Vision

1. **Additive, not rewrite.** The orchestrator is a new capability layered on top of existing TARSy architecture. A single agent in an existing chain becomes the entry point to dynamic orchestration.

2. **LLM as the planner.** No hardcoded DAG or workflow engine. The orchestrator LLM reasons about input, available agents, and results to decide what to do, in what order, and when to stop.

3. **Config-driven infrastructure, LLM-driven tasks.** The LLM decides *what* to investigate and *what tasks* to give sub-agents. Config determines *how* things run — which MCP servers, which models, which agents are available. Clean separation.

4. **Natural language is the protocol.** Context flows between orchestrator and sub-agents as plain text. The orchestrator crafts task descriptions; sub-agents return findings as free text. No structured schemas.

5. **Controlled autonomy.** The orchestrator has freedom to choose agents and sequencing, but operates within configured guardrails (max concurrent agents, timeouts, allowed agent list, depth limit).

### Implementation

1. **Push-based result collection.** `dispatch_agent` returns immediately; sub-agent results are automatically injected into the orchestrator's conversation as they complete. The LLM never polls — it dispatches, continues working, and sees results appear between iterations.

2. **Minimal controller modification.** The orchestrator reuses `IteratingController` with one targeted change: before the loop exits (when the LLM has no tool calls), it checks for pending sub-agents and waits for them. Available results are also drained non-blockingly before each LLM call.

3. **ToolExecutor is the seam.** The existing `ToolExecutor` interface is the integration point. A `CompositeToolExecutor` wraps MCP tools + orchestration tools into a single tool set.

4. **Sub-agents are regular executions.** Sub-agents run through the same resolve-config → tool executor → agent factory → execute path as any agent.

5. **DB records follow the existing model.** Sub-agent runs create real `AgentExecution` records with timeline events, linked to the orchestrator via a `parent_execution_id` column.

## Architecture

### High-Level

```text
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
│                 │  │ ← LogA result pushed    │ │            │
│                 │  │ dispatch_agent(K8s, ..) │ │            │
│                 │  │ ← MetC result pushed    │ │            │
│                 │  │ ← K8s result pushed     │ │            │
│                 │  │ → final response        │ │            │
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

### Components

```text
┌───────────────────────────────────────────────────────────────────────┐
│  Session Executor                                                     │
│                                                                       │
│  executeStage → executeAgent → AgentFactory.CreateAgent               │
│                                      │                                │
│                             ┌────────┴────────────────────┐           │
│                             │  Agent (type=orchestrator)  │           │
│                             └────────┬────────────────────┘           │
│                                      │                                │
│                             ┌────────┴───────────────────┐            │
│                             │  IteratingController       │            │
│                             │  (+ push-based drain/wait) │            │
│                             └────────┬───────────────────┘            │
│                                      │                                │
│                             ┌────────┴───────────────────┐            │
│                             │  CompositeToolExecutor     │            │
│                             │  ├─ MCP tools (Loki, etc.) │            │
│                             │  └─ Orchestration tools    │            │
│                             │     ├─ dispatch_agent      │            │
│                             │     ├─ cancel_agent        │            │
│                             │     └─ list_agents         │            │
│                             └────────┬───────────────────┘            │
│                                      │                                │
│                             ┌────────┴───────────────────┐            │
│                             │  SubAgentRunner            │            │
│                             │  (spawns/tracks sub-agents)│            │
│                             │  goroutine per sub-agent   │            │
│                             └────────────────────────────┘            │
└───────────────────────────────────────────────────────────────────────┘
```

**CompositeToolExecutor** wraps the existing MCP `ToolExecutor` and adds orchestration tools. Implements the same `ToolExecutor` interface. On execute, routes by name: orchestration tools go to the `SubAgentRunner`, everything else delegates to MCP. On close, cancels all running sub-agents and waits for them to finish (with a timeout using a background context when the parent context may already be cancelled).

**SubAgentRunner** manages the lifecycle of sub-agent goroutines. Provides push-based result delivery via a buffered results channel and lifecycle management (dispatch, cancel, list, cancel-all). Sub-agent contexts derive from the session-level context (not the per-iteration context) so they survive across orchestrator iterations. A close channel ensures individual sub-agent cancellations still deliver results while bulk shutdown drops them cleanly.

**ExecutionContext** gains optional orchestration-related fields: sub-agent context (for sub-agents), a result collector interface for drain/wait (avoids import cycles), and a catalog of available agents for the system prompt. All are unset for non-orchestrator agents — zero impact on existing agents.

### Data Flow

1. Orchestrator's LLM call returns `dispatch_agent` tool calls
2. `CompositeToolExecutor` routes each to `SubAgentRunner.Dispatch`
3. SubAgentRunner creates an `AgentExecution` DB record (with `parent_execution_id`), resolves agent config, spawns a goroutine that runs the full agent execution path
4. Returns immediately: `{ execution_id: "sub-exec-123", status: "accepted" }`
5. When sub-agent finishes, result is sent to a shared results channel
6. Controller drains results before each LLM call (non-blocking) and waits when idle (blocking)
7. Results are injected as user-role messages (format is internal and not disclosed to the LLM to prevent hallucination)

### Push-Based Result Collection

The orchestrator LLM never polls for results. Instead:

- **`dispatch_agent`** returns immediately with an execution ID
- **Sub-agent results** are automatically injected into the conversation as they complete
- **Before each LLM call**: non-blocking drain of any available results
- **When LLM is idle** (no tool calls but sub-agents pending): blocking wait

This means the LLM can dispatch across multiple iterations, see results as soon as they're ready, react to early results (cancel unnecessary agents, dispatch follow-ups), and never waste iterations polling.

### Example Flow

```text
Orchestrator receives: "Alert: service-X 5xx rate at 15%"

Iteration 1 — LLM dispatches parallel investigations:
  dispatch_agent(name="LogAnalyzer", task="Find all 5xx errors for service-X
    in the last 30 min. Report: error count, top error messages, time pattern.")
    → { execution_id: "exec-abc", status: "accepted" }
  dispatch_agent(name="MetricChecker", task="Check service-X latency, error rate,
    and CPU/memory for the last hour. Flag any anomalies.")
    → { execution_id: "exec-def", status: "accepted" }

Iteration 2 — No tool calls → pending sub-agents → controller waits...
  LogAnalyzer finishes → result injected:
  "[Sub-agent completed] LogAnalyzer (exec-abc): Found 2,847 5xx errors.
   92% are 'connection refused' to payments-db. Spike started at 14:23 UTC..."

Iteration 3 — LLM sees result, dispatches follow-up:
  dispatch_agent(name="K8sInspector", task="Check payments-db pod status,
    recent events, and restarts in the last hour.")
  Before iteration 4: MetricChecker finishes → result drained

Iteration 4 — Waits for K8sInspector...
  "[Sub-agent completed] K8sInspector (exec-ghi): payments-db-0 OOMKilled
   at 14:22, restarted 3 times. Current memory limit: 512Mi."

Iteration 5 — Final response:
  "Root cause: payments-db OOMKilled due to 512Mi memory limit..."
```

### Context Flow

```text
Alert data + prior agent results
        │
        ▼
┌─────────────────────┐
│  Orchestrator LLM   │  System prompt: custom_instructions +
│  Sees: agent list   │  available agents (name + description + tools) +
│  with descriptions  │  chain context + "Results delivered automatically"
└────────┬────────────┘
         │
    dispatch_agent(task="...")   ◄── returns immediately with execution_id
         │
         ▼
┌─────────────────────┐
│  Sub-agent LLM      │  System prompt: agent's custom_instructions +
│  (runs in goroutine)│  MCP server tools + task from orchestrator
└────────┬────────────┘
         │
    Result pushed to orchestrator's conversation automatically
         │
         ▼
┌─────────────────────┐
│  Orchestrator LLM   │  Decides: dispatch more? cancel? final answer?
└────────┬────────────┘
         │
    Text output to chain  ◄── final response, same as any agent
```

## Sub-Agents

Sub-agents are **regular TARSy agents** — both config agents (`agents:` in tarsy.yaml) and built-in agents (KubernetesAgent, ChatAgent, etc.). No new concept needed.

**Discovery:** Agents with a `description` field form the global sub-agent registry (built at config load time). Agents without `description` are excluded. Orchestrator agents themselves are also excluded. The registry can be further restricted via `sub_agents` override at chain/stage/agent level.

**Execution model:**
- **Dispatch and forget:** `dispatch_agent` returns immediately. Results are pushed back automatically.
- **Multi-phase dispatch:** The LLM can dispatch agents across multiple iterations, see early results, and dispatch follow-ups.
- **Idle wait:** When the LLM has no more tool calls but sub-agents are running, the controller pauses until at least one result arrives.
- **Depth 1 only:** Sub-agents cannot spawn their own sub-agents.
- **Failure handling:** Sub-agent failures are reported to the orchestrator with full context. The LLM decides what to do. No auto-retry at orchestration level.
- **Final response (not synthesis):** The orchestrator is typically a single agent in a stage. Its final response is just the LLM's last output — no separate synthesis step. If `max_iterations` is hit, the existing forced-conclusion path produces the final analysis. Remaining sub-agents are cancelled when the composite executor closes.

## Configuration

### Orchestrator Config

Per [ADR-0001](0001-agent-type-refactor.md), `AgentConfig` already has `Type`, `Description`, and `LLMBackend` fields. The orchestrator adds `orchestrator` as an agent type and a nested `orchestrator` section for guardrails:

```yaml
defaults:
  orchestrator:
    max_concurrent_agents: 5
    agent_timeout: 300s
    max_budget: 600s

agents:
  MyOrchestrator:
    type: orchestrator
    description: "Dynamic SRE investigation orchestrator"
    custom_instructions: |
      You investigate alerts by dispatching specialized sub-agents...
    orchestrator:
      max_concurrent_agents: 3
```

### `sub_agents` Override

Follows the same override pattern as `mcp_servers`, `llm_provider`, `max_iterations` — full hierarchy (chain + stage + stage-agent), all levels optional:

```yaml
agent_chains:
  focused-investigation:
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
            sub_agents: [LogAnalyzer, MetricChecker]
```

Supports both short-form (list of strings) and long-form (list of objects with per-reference overrides like `llm_provider`, `max_iterations`, `mcp_servers`), and can be mixed. If omitted at all levels, the orchestrator sees the full global registry.

### New Built-In Agents

Four new built-in agents ship with the orchestrator feature:

| Agent | Type | Native Tools | MCP | Purpose |
|-------|------|-------------|-----|---------|
| Orchestrator | orchestrator | none | none | Dispatches and coordinates sub-agents |
| WebResearcher | default | google_search, url_context | none | Web research and URL analysis |
| CodeExecutor | default | code_execution | none | Python computation and analysis |
| GeneralWorker | default | none | none | Reasoning, summarization, drafting |

These complement existing built-in agents (KubernetesAgent, etc.) which already have descriptions and are orchestrator-visible. Built-in agent definitions were extended with backend and native-tools fields where needed for WebResearcher and CodeExecutor.

A `native_tools` field on `AgentConfig` enables per-agent override of provider-level native tools (three-level resolution: LLM provider → agent-level → per-alert override).

### Usage Examples

**Minimal** — reference the built-in Orchestrator by name:

```yaml
defaults:
  llm_provider: "google-prod"
  llm_backend: "google-native"

agents:
  LogAnalyzer:
    description: "Analyzes logs for error patterns"
    mcp_servers: [loki]

agent_chains:
  alert-investigation:
    alert_types: [high-error-rate]
    stages:
      - name: orchestrate
        agents:
          - name: Orchestrator
            sub_agents: [LogAnalyzer, GeneralWorker]
```

**Comprehensive** — override LLM provider/backend, add MCP servers to GeneralWorker, configure sub-agent overrides:

```yaml
agent_chains:
  deep-investigation:
    stages:
      - name: orchestrate
        agents:
          - name: Orchestrator
            llm_provider: "openai-prod"
            llm_backend: "langchain"
            sub_agents:
              - name: LogAnalyzer
              - name: MetricChecker
              - name: WebResearcher
              - name: GeneralWorker
                mcp_servers: [kubernetes-server]
                max_iterations: 5
              - name: CodeExecutor
                llm_provider: "google-prod"
```

The override hierarchy (`defaults → agentDef → chain → stage → stage-agent`) means built-in agents can be fully customized at the point of use — no redefinition required.

## Orchestration Tools

Three tools registered via the composite executor. There is no `get_result` tool — results are pushed automatically.

- **`dispatch_agent(name, task)`** — fire-and-forget. Spawns a sub-agent with a task, returns an execution ID immediately. Results are delivered automatically when the sub-agent finishes.
- **`cancel_agent(execution_id)`** — cancel a running sub-agent. Returns `cancelled`, `already_completed`, or `not_found`.
- **`list_agents()`** — list all dispatched sub-agents and their current status.

MCP tools use `server.tool` naming (e.g., `kubernetes-server.get_pod`). Orchestration tools use plain names without dots — natural namespace separation. When recorded as MCP interaction records, they use a dedicated server name so dashboards can distinguish them from real MCP calls.

## Controller Approach

The orchestrator reuses `IteratingController` with two additions to the iteration loop (zero impact on non-orchestrator agents when the sub-agent collector is unset):

1. **Before each LLM call**: non-blocking drain of available sub-agent results
2. **At loop exit** (no tool calls): if sub-agents are still pending, blocking wait for a result instead of terminating — then continue the loop with the new result

This enables multi-phase orchestration: dispatch → wait → react → dispatch more → wait → final response.

### Orchestrator Final Response vs. Stage Synthesis

```text
Current parallel pattern:              Orchestrator pattern:

Stage:                                 Stage:
├─ Agent A (parallel) ──┐              └─ Orchestrator (single) ──────────┐
├─ Agent B (parallel) ──┼─ SynthesisAgent    ├─ dispatch LogAnalyzer      │
└─ Agent C (parallel) ──┘  (separate exec)   ├─ dispatch MetricChecker    │ same
                                             ├─ collect results           │ execution
                                             └─ final response → output ──┘
```

The orchestrator's final response requires no special code — it uses the existing `IteratingController` completion path. When the LLM produces text with no tool calls and no pending sub-agents, the loop exits and the text becomes `FinalAnalysis`.

## Database Schema

Two new nullable columns on `AgentExecution`:

- **`parent_execution_id`** — links sub-agents to their parent orchestrator execution (`NULL` for regular agents and orchestrators themselves)
- **`task`** — task description from orchestrator dispatch (dashboard tree view and timeline)

Sub-agent executions live under the **same stage** as the orchestrator. The uniqueness rule for stage placement was adjusted with partial indexes: one for top-level agents and one for sub-agents. Stage status updates consider top-level executions only — sub-agent failures don't incorrectly mark the stage as failed.

## Prompt Construction

### Orchestrator System Prompt

When the execution is an orchestrator agent, the system prompt is assembled in tiers:

1. General SRE instructions (Tier 1)
2. MCP server instructions for the orchestrator's own MCP servers (Tier 2)
3. Custom instructions (Tier 3)
4. **Agent catalog** — available sub-agents with name, description, and tools
5. **Result delivery instructions** — explains that results appear automatically, no polling needed

Example agent catalog:

```text
## Available Sub-Agents

You can dispatch these agents using the dispatch_agent tool.
Results are delivered automatically when each sub-agent finishes — do not poll.

- **LogAnalyzer**: Analyzes logs from Loki to find error patterns and anomalies
  MCP tools: loki
- **WebResearcher**: Searches the web and analyzes URLs for real-time information
  Native tools: google_search, url_context
- **GeneralWorker**: General-purpose agent for analysis, summarization, reasoning
  Tools: none (pure reasoning)
```

### Sub-Agent Prompt

Sub-agents receive a clean task-focused user message (`## Task\n\n{task text}`) instead of the investigation template. The system message includes the agent's own `custom_instructions`, MCP instructions, and an auto-injected focus block:

```text
You are a sub-agent dispatched by an orchestrator for a specific task.

Rules:
- Focus exclusively on your assigned task — do not investigate unrelated areas.
- Your final response is automatically reported back to the orchestrator.
- Be concise: state what you found, key evidence, and relevant details.
- If you have tools available, use them. If not, use reasoning alone.
```

Sub-agent results are injected into the orchestrator's conversation as user-role messages. The injection format is internal to `FormatSubAgentResult` and is intentionally not disclosed in the orchestrator's system prompt to prevent the LLM from hallucinating results using the same format.

## Cancellation Cascade

When the orchestrator is cancelled (session cancel via API):

1. Session context cancelled → orchestrator's LLM call fails
2. `SubAgentRunner` cancels all sub-agent contexts
3. Sub-agents receive cancelled context → status set to `cancelled`
4. Sub-agent runner waits for all goroutines to exit
5. Orchestrator returns `ExecutionStatusCancelled`

Sub-agent goroutines derive their contexts from the parent session context (with `agent_timeout` deadline), **not** from the per-iteration context — this is critical so sub-agents survive across orchestrator iterations and are only cancelled when the session itself is cancelled.

## Guardrails

| Guardrail | Config | Default |
|-----------|--------|---------|
| Max concurrent sub-agents | `orchestrator.max_concurrent_agents` | 5 |
| Per sub-agent timeout | `orchestrator.agent_timeout` | 300s |
| Total orchestrator budget | `orchestrator.max_budget` | 600s |
| Allowed sub-agents | `sub_agents` override | All agents |
| Max depth | Hardcoded | 1 (no nesting) |

## Dashboard

The dashboard surfaces orchestrator sub-agents in both the Reasoning view and Trace view, with real-time streaming. The backend API nests sub-agents in session and trace payloads — no new API endpoints.

A nullable `parent_execution_id` on timeline events and the same field on WebSocket payloads makes both REST and WS responses self-describing — the dashboard partitions events without cross-referencing.

```text
SessionDetailPage
  ├─ WS handler ────────── Filters sub-agent events into separate maps
  └─ ConversationTimeline
      └─ StageContent ───── Partitions groups into orchestrator + sub-agents
          └─ SubAgentCard ─ Collapsible inline card per sub-agent
                             Collapsed: name, task, status, duration, tokens
                             Expanded: sub-agent's own timeline items + streaming

TracePage
  └─ StageAccordion ── Detects orchestrator (sub_agents present)
      └─ SubAgentTabs ─ Reused parallel-execution tabs pattern
```

**Reasoning view:** Sub-agent cards appear inline in the orchestrator's flow, anchored to `dispatch_agent` tool call results. Collapsed by default, expandable for full sub-agent timeline.

**Trace view:** Orchestrator metadata and interactions first, then "Sub-Agents" section with tabs. Interaction counts recursively include sub-agents plus a "N sub-agents" chip.

**Edge cases:** Orchestrator as sole agent (common — no tabs), parallel stage (orchestrator tab has sub-agent cards), sub-agent failure (shown without implying orchestrator failed), no sub-agents dispatched (renders as normal agent).

## Observability

Sub-agent executions publish the same events as regular executions (`execution.status`, `execution.progress`, `timeline_event.created/completed`, `stream.chunk`). The `parent_execution_id` in each event enables filtering and routing.

Full trace tree with parent-child linking:

```text
Execution: exec-001 (Orchestrator)
├── Sub-execution: exec-abc (LogAnalyzer)
│   ├── MCP tool call: loki.query_range(...)
│   └── Result: "Found 2,847 5xx errors..."
├── Sub-execution: exec-def (MetricChecker)
│   ├── MCP tool call: prometheus.query(...)
│   └── Result: "p99 latency jumped..."
└── Final response: "Root cause: payments-db OOMKilled..."
```

## Future Considerations

- **Skills system:** `custom_instructions` covers this today. A future skills system (reusable named blocks, compose-by-reference) would be a DX improvement.
- **Memory across runs:** Index past investigation results, let the orchestrator search before dispatching, include past context in sub-agent tasks.
- **Deeper orchestration:** Depth 2+ with configurable max depth. The async dispatch model supports it conceptually.
- **LLM model override:** Optional `model` parameter on `dispatch_agent` for runtime model selection.
- **Steer:** A `steer_agent` tool to inject new instructions into a running sub-agent mid-execution.

## Decided Questions

### Vision

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| V1 | What is a sub-agent? | Regular TARSy agents with `description`. Global registry with override. | No new concept. `description` is the opt-in. |
| V2 | How does orchestrator discover sub-agents? | Global registry from config + built-ins | Agents are already known from config and builtins. |
| V3 | How are sub-agents described? | Name + `description` (required) + MCP servers | LLM infers dispatch decisions from name + description + tools. |
| V4 | How does orchestrator invoke sub-agents? | Async `dispatch_agent` + push-based results | Polling wastes iterations; push-based delivers automatically. |
| V5 | How are MCP servers attached? | Config-driven only, reuse existing `mcp_servers` | LLM focuses on tasks, not infrastructure. |
| V6 | What format do results take? | Free text (raw LLM response) | No schema to maintain. |
| V7 | Skills system? | Defer — `custom_instructions` covers this | Reusable blocks can be added later. |
| V8 | LLM model selection? | Config per agent, LLM does not select models | Each agent already has LLM config. |
| V9 | Orchestration depth? | Depth 1 only, no nesting | Simple, predictable, debuggable. |
| V10 | Failure handling? | LLM decides, no auto-retry | LLM has context to reason about retries. |
| V11 | Observability? | Full trace tree with parent-child linking | Essential for SRE debugging. |
| V12 | Orchestrator direct MCP access? | Config-driven — assign MCP servers or leave empty | Operators choose the role via config. |
| V13 | Output format for chain? | Plain text (same as any agent) | Zero integration work. |
| V14 | Memory across runs? | Defer, but design with memory in mind | Current design already supports future memory. |

### Implementation

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| I1 | Orchestrator identification in config? | Existing `type` field — orchestrator type ([ADR-0001](0001-agent-type-refactor.md)) | Resolved by ADR-0001 |
| I2 | Tool combination approach? | CompositeToolExecutor (wrapper pattern) | Clean separation; controller/tool boundary preserved |
| I3 | Orchestration tool naming? | Plain names (`dispatch_agent`, etc.) | MCP uses dots — natural namespace separation |
| I4 | Controller approach? | Reuse IteratingController + push-based injection | Small, localized change; loop structure intact |
| I5 | Guardrail config? | Nested `orchestrator` section + `defaults:` | Follows existing patterns |
| I6 | `sub_agents` hierarchy? | Full hierarchy (chain + stage + stage-agent) | Consistent with `mcp_servers`/`llm_provider` |
| I7 | Sub-agent DB model? | `parent_execution_id` on `AgentExecution` | Minimal schema change |
| I8 | Task injection? | Custom sub-agent template (`## Task`) | Clean separation from investigation template |
| I9 | Dashboard? | Tree view from the start | Essential for debugging |
| I10 | Dependency injection? | Explicit dependency bundle for sub-agent wiring | Explicit, testable |
| I11 | Phasing? | Layered delivery | Keeps changes reviewable without prescribing a fixed PR count |

### Dashboard

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| D1 | Sub-agent event association? | DB column + WS payload `parent_execution_id` | No lookup needed, deterministic |
| D2 | Reasoning view display? | Collapsible inline cards | Natural reading order, progressive disclosure |
| D3 | Trace view display? | Nested tabs within orchestrator's panel | Reuses parallel execution tabs pattern |
| D4 | Interaction counting? | Recursive total + "N sub-agents" chip | Operators see scope AND know sub-agents exist |
| D5 | One PR or split? | Backend and dashboard shipped together for this feature | Backend changes exist to serve the operator experience |
