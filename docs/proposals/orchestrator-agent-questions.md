# Orchestrator Agent — Design Questions

**Status:** All 15 questions decided
**Related:** [Design document](orchestrator-agent-design.md)
**Last updated:** 2026-02-19

Each question has multiple options with trade-offs and a recommendation. We'll go through these one by one to form the vision, then update the design document.

---

## Q1: What is a "sub-agent"? — DECIDED

> **Decision:** Sub-agents are regular TARSy agents — global registry with override.

Sub-agents are not a new concept. They are the existing TARSy agents — both config agents (`agents:` in tarsy.yaml) and built-in agents (KubernetesAgent, etc.). Agents with a `description` field form the global sub-agent registry; agents without `description` are excluded from orchestrator visibility. The registry can be further restricted to a subset via `sub_agents` override at chain → stage → agent-name level.

```yaml
agents:
  LogAnalyzer:
    description: "Analyzes logs from Loki to find error patterns"
    mcp_servers: [loki]
    custom_instructions: "You analyze logs to find error patterns..."

agent_chains:
  # Default: orchestrator sees ALL agents (config + built-in)
  full-investigation:
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
            type: orchestrator

  # Override: restrict to a subset
  focused-investigation:
    sub_agents: [LogAnalyzer, MetricChecker]
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
            type: orchestrator
```

**Key points:**

- `description` is the opt-in: agents with it are orchestrator-visible, agents without it are excluded.
- Same agent can appear in static chains AND as an orchestrator sub-agent.
- Built-in agents (KubernetesAgent, etc.) already have descriptions — they're first-class sub-agents by default.
- `sub_agents` override follows TARSy's existing chain → stage → agent-name inheritance.

*Rejected alternatives: (A) reuse-only — too rigid for ad-hoc invocation; (B) new separate concept — unnecessary duplication; (C) hybrid wrappers — over-engineered.*

---

## Q2: How does the orchestrator know what sub-agents are available? — DECIDED

> **Decision:** Global registry from config + built-ins. Follows directly from Q1.

The orchestrator automatically sees all agents defined in `agents:` plus all built-in agents. No separate discovery mechanism needed. The `sub_agents` override (from Q1) handles scoping.

The orchestrator's system prompt is built at runtime from the resolved agent list.

*Rejected alternatives: (A) static per-orchestrator list — unnecessary given global registry; (B) dynamic plugin discovery — premature complexity; (C) runtime enrichment — two sources of truth.*

---

## Q3: How are sub-agents described to the orchestrator? — DECIDED

> **Decision:** Name + `description` field + MCP servers. `description` is required for orchestrator visibility.

Each agent's entry in the orchestrator's system prompt includes:
- **Name** (already exists)
- **Description** (required — agents without it are excluded from the orchestrator's registry)
- **MCP servers** (already exists in agent config)

The LLM is smart enough to infer when to use "LogAnalyzer with Loki MCP server" vs "MetricChecker with Prometheus" without explicit `when_to_use` or `capabilities` fields. These can be added later if dispatch quality proves insufficient. Built-in agents already have descriptions and are visible by default.

*Rejected alternatives: (A) name-only — too sparse; (B) structured capabilities + domains — over-engineered for v1; (C) when_to_use examples — verbose, can add later if needed.*

---

## Q4: How does the orchestrator invoke sub-agents? — DECIDED

> **Decision:** Async dispatch + get_result (Option B).

The orchestrator gets three tools for sub-agent management:

- **`dispatch_agent`** — fire-and-forget. Spawns a sub-agent with a task, returns an execution ID immediately.
- **`get_result`** — retrieve status and result of a dispatched sub-agent by execution ID.
- **`cancel_agent`** — cancel a running sub-agent by execution ID. Matches TARSy's existing agent/execution cancellation infrastructure (currently human-only, this extends it to LLM-driven cancellation).

This enables parallel execution with lifecycle control — the orchestrator can dispatch multiple agents, collect results, and cancel unnecessary work:

```
dispatch_agent(name="log_analyzer", task="Find 5xx errors for service-X")  → { execution_id: "abc" }
dispatch_agent(name="metric_checker", task="Check latency for service-X")  → { execution_id: "def" }
get_result(execution_id="abc")  → { status: "completed", result: "Found spike at 14:23..." }
cancel_agent(execution_id="def")  → { status: "cancelled" }
```

**Key points:**

- Uses TARSy's existing `execution_id` concept for tracking sub-agent runs.
- The orchestrator LLM decides sequencing — dispatch one at a time or fan out in parallel.
- `get_result` returns status (`running`, `completed`, `failed`, `cancelled`) so the LLM knows when to poll vs proceed.
- `cancel_agent` reuses TARSy's existing cancellation mechanism, extending it from human-only to LLM-driven.
- Requires a frontier model for the orchestrator (already supported — TARSy has all major providers).
- Sub-agent runs are tracked in a registry for lifecycle management.
- Guardrails: max concurrent sub-agents, per-agent timeout, total orchestrator budget.

*Rejected alternatives: (A) sync-only `run_agent` — blocks on each call, no parallelism; (C) sync + batch parallel — complex schema, LLM struggles with batch input/output.*

---

## Q5: How are MCP servers attached to sub-agents? — DECIDED

> **Decision:** Config-driven only — use TARSy's existing MCP server assignment. The orchestrator LLM does not control MCP attachment.

Sub-agents get their MCP servers from agent config (`agents.X.mcp_servers`), with the existing TARSy override mechanism at chain/stage level. The orchestrator LLM has no parameter to pass or override MCP servers in `dispatch_agent` — this is purely an infrastructure/config concern.

```yaml
agents:
  LogAnalyzer:
    mcp_servers: [loki]                        # agent-level default
  MetricChecker:
    mcp_servers: [prometheus]

agent_chains:
  multi-cloud-investigation:
    stages:
      - name: investigate
        agents:
          - name: MyOrchestrator
            type: orchestrator
        # MCP overrides at chain/stage level if needed — not LLM-decided
```

**Key points:**

- No new mechanism needed — reuses TARSy's existing `mcp_servers` config on agents.
- Overrides at chain/stage level already work in TARSy for context-specific adjustments.
- The orchestrator LLM focuses on *what task* to give each agent, not *what infrastructure* it needs.
- LLM-driven MCP selection can be added later (Option B/C) if real usage shows config-only is too rigid.

*Rejected alternatives: (A) hardcoded static — more restrictive than TARSy's current model; (B) LLM decides — too much infrastructure responsibility for the LLM; (C) defaults + LLM override — premature flexibility; (D) global/chain-level only — sub-agents may see irrelevant servers.*

---

## Q6: What format do sub-agent results take? — DECIDED

> **Decision:** Free text (Option A).

Sub-agents return plain text — their raw LLM response. The orchestrator interprets and synthesizes it using its own reasoning. No structured schema, no JSON envelope.

*Rejected alternatives: (B) structured JSON — requires enforcing output schemas on LLMs, schema maintenance overhead; (C) text + structured metadata — post-processing complexity for uncertain gain.*

---

## Q7: Should TARSy have a "skills" system? — DECIDED

> **Decision:** No — defer (Option D). Existing `custom_instructions` already covers this.

TARSy's per-agent `custom_instructions` already serves the same purpose as a skills system: injecting domain knowledge into agent prompts. The difference is modularity — a dedicated skills system would offer reusable named blocks with discovery and gating, while TARSy's instructions are inline text per agent config. This means some instruction text may be duplicated across agents, but functionally it works.

A proper skills system (reusable named blocks, compose-by-reference, deduplication) would be a DX improvement worth revisiting once we see what instruction patterns emerge in practice.

*Rejected alternatives: (A) skills for orchestrator only — premature without usage data; (B) skills for all agents — large scope, same coverage already exists via custom_instructions; (C) prompt templating — adds complexity without solving the reuse problem.*

---

## Q8: If we build skills, what format should they use? — DEFERRED

> Deferred — depends on Q7. When a skills system is introduced, revisit format options (markdown with frontmatter, YAML, or database-stored).

---

## Q9: What LLM should the orchestrator use vs sub-agents? — DECIDED

> **Decision:** Configurable per agent (Option C), config-driven only. LLM does not select models.

Each sub-agent uses its own configured LLM (already supported in TARSy's agent config). The orchestrator uses whatever model its chain/stage configures. No model selection is exposed to the orchestrator LLM — it dispatches tasks, not infrastructure decisions. Same philosophy as Q5 (MCP servers = config-driven).

We skip LLM-driven model selection for v1 — keeps the orchestrator focused on *what* to investigate, not *how* to run it. Can add an optional `model` override to `dispatch_agent` later if needed.

*Rejected alternatives: (A) same model everywhere — too rigid, can't match capability to task; (B) strong orchestrator + cheap sub-agents — artificial constraint; (D) LLM picks models — unpredictable costs, LLMs aren't good at meta-decisions about model selection.*

---

## Q10: How deep can orchestration go? — DECIDED

> **Decision:** Depth 1 only (Option A). Orchestrator calls leaf agents, no nesting.

Sub-agents cannot spawn their own sub-agents. Simple, predictable, debuggable, cost-controlled. Can revisit if real usage shows depth-2 adds clear value.

*Rejected alternatives: (B) configurable max depth — exponential complexity, hard to debug; (C) unlimited with budget — unpredictable behavior.*

---

## Q11: How does the orchestrator handle sub-agent failures? — DECIDED

> **Decision:** LLM decides. No auto-retry at orchestration level.

Sub-agent failures (error, timeout, unknown) are reported to the orchestrator LLM with full context: status, error message, and any partial output. The orchestrator then decides what to do — retry, try a different agent, proceed with partial data, or report the failure. No auto-retry, no automatic recovery. The LLM has the context to reason about whether retrying makes sense.

Note: TARSy's existing LLM-level retries (in `llm-service`) still apply to individual LLM calls within a sub-agent. This decision is about orchestration-level failure handling — when an entire sub-agent run fails.

*Rejected alternatives: (A) was mislabeled in original — it described the same approach; (B) auto-retry at orchestration level — adds complexity, masks issues, LLM can retry itself if needed; (C) silent skip — dangerous in SRE context.*

---

## Q12: Observability — how do we trace orchestrator runs? — DECIDED

> **Decision:** Full trace tree (Option B). Each sub-agent run gets its own timeline, linked to the orchestrator via parent execution ID.

Essential for an SRE tool: when the orchestrator produces a wrong conclusion, operators need to trace which sub-agent gave bad data and why. Reuses TARSy's existing timeline infrastructure with a parent-child linking concept.

*Rejected alternatives: (A) orchestrator timeline only — sub-agent internals opaque, insufficient for debugging; (C) summaries only — can't debug bad sub-agent results.*

---

## Q13: Should the orchestrator be able to use MCP servers directly? — DECIDED

> **Decision:** Config-driven (Option B, but not mandatory). The orchestrator is a regular TARSy agent — if `mcp_servers` are assigned to it, it gets direct access. If not, it's a pure coordinator.

The orchestrator is just another TARSy agent. Whether it has direct MCP access depends entirely on its config — same as any agent. No special logic needed.

In practice, operators choose the orchestrator's role via config: assign MCP servers for a hybrid investigator/coordinator, or leave them empty for a pure dispatcher. The `custom_instructions` can guide the LLM's behavior ("prefer delegating to sub-agents for heavy analysis, use your own tools only for quick checks").

*Rejected alternatives: (A) pure coordinator only — forces overhead for trivial checks, artificial constraint; (C) read-only MCP — no clean read/write separation in MCP servers.*

---

## Q14: How does the orchestrator's output integrate with the rest of the chain? — DECIDED

> **Decision:** Plain text, same as any agent (Option A).

The orchestrator produces text that flows to the next chain agent. Zero integration work — transparent to the chain. Context flows between orchestrator and sub-agents via natural language in both directions:

- **Down (task):** the orchestrator crafts the `task` parameter to tell sub-agents what to investigate and what to report back.
- **Up (result):** sub-agents return free text, the orchestrator reasons over it and synthesizes.

No structured contracts. The orchestrator's `custom_instructions` + the task descriptions it generates are the full "protocol."

*Rejected alternatives: (B) structured output schema — rigid, requires schema maintenance; (C) text + structured metadata — more work for uncertain gain in v1.*

---

## Q15: Should the orchestrator have memory across runs? — DECIDED

> **Decision:** Defer (Option D), but design with memory in mind.

No memory in v1. Each run starts fresh. But memory is a likely future feature, so the current design should not block it.

**How memory would likely work in TARSy (future):**

- Investigation outputs/findings stored as indexable records (keyed by execution ID)
- On new runs, orchestrator searches memory for relevant past incidents before dispatching
- Orchestrator includes relevant past context in sub-agent task descriptions ("Last time this service had similar symptoms, root cause was...")
- Sub-agents don't access memory directly — orchestrator mediates
- Possible implementations: vector-indexed DB, embeddings on past investigation summaries

**Why the current design already supports this:**

1. Orchestrator output is text (Q14) — easily capturable and indexable
2. Full trace tree (Q12) — stores enough context to be indexed later
3. `custom_instructions` — memory search results can be injected as additional prompt context
4. `execution_id` — natural key for linking memory entries to investigations
5. Sub-agents don't need memory (Q11 passes context via task)

**Design note:** Don't discard investigation results after chain completion. The text output + timeline data is the raw material for future memory.

*Rejected alternatives: (A) stateless only — misses learning opportunity; (B) memory via skills — depends on skills system; (C) full session memory now — significant complexity, premature.*

---

## Summary of Recommendations

| # | Question | Decision |
|---|----------|----------|
| Q1 | What is a sub-agent? | Regular TARSy agents (config + built-in) with `description`. Global registry with override. |
| Q2 | How does orchestrator discover sub-agents? | Global registry from config + built-ins (follows from Q1) |
| Q3 | How are sub-agents described? | Name + `description` (required) + MCP servers. LLM infers the rest. |
| Q4 | How does orchestrator invoke sub-agents? | Async `dispatch_agent` + `get_result` + `cancel_agent` |
| Q5 | How are MCP servers attached? | Config-driven only, reuse TARSy's existing `mcp_servers` |
| Q6 | What format do results take? | Free text (raw LLM response) |
| Q7 | Should TARSy have skills? | Defer — `custom_instructions` covers this |
| Q8 | Skill format? | Deferred (depends on Q7) |
| Q9 | LLM model selection? | Config per agent, LLM does not select models |
| Q10 | Orchestration depth? | Depth 1 only, no nesting |
| Q11 | Failure handling? | LLM decides, no auto-retry at orchestration level |
| Q12 | Observability? | Full trace tree with parent-child linking |
| Q13 | Orchestrator direct MCP access? | Config-driven — assign MCP servers or leave empty |
| Q14 | Output format for chain? | Plain text (same as any agent) |
| Q15 | Memory across runs? | Defer, but design with memory in mind |

---

## Decision Log

| Date | Question | Decision | Rationale |
|------|----------|----------|-----------|
| 2026-02-19 | Q1: What is a sub-agent? | Regular TARSy agents (config + built-in) with `description` field form the registry. Agents without `description` are excluded. `sub_agents` override at chain/stage/agent level for further scoping. | No new concept needed. `description` is the opt-in. Built-ins already have descriptions. Follows existing TARSy override patterns. |
| 2026-02-19 | Q2: How does orchestrator discover sub-agents? | Global registry from config + built-ins. Follows directly from Q1. | No separate discovery needed — agents are already known from config and builtins. |
| 2026-02-19 | Q3: How are sub-agents described? | Name + `description` (required for visibility) + MCP servers list. LLM infers when to use each agent. | `description` doubles as opt-in gate. Minimal config. LLM is capable of dispatch decisions from name + description + tools. Richer metadata can be added later if needed. |
| 2026-02-19 | Q4: How does orchestrator invoke sub-agents? | Async `dispatch_agent` + `get_result`. Orchestrator manages execution IDs, can fan out in parallel. | Enables parallel investigation. Proven pattern. Frontier models handle async tool management well. |
| 2026-02-19 | Q5: How are MCP servers attached? | Config-driven only. Reuse TARSy's existing `mcp_servers` on agents + chain/stage overrides. LLM does not control MCP attachment. | Keep LLM focused on tasks, not infrastructure. Existing TARSy mechanism already supports overrides. |
| 2026-02-19 | Q6: What format do results take? | Free text — raw LLM response from sub-agent. | Simplest. No schema to maintain. Orchestrator LLM reasons over natural language. |
| 2026-02-19 | Q7: Skills system? | Defer. Existing `custom_instructions` covers this. Q8 (skills format) also deferred. | TARSy's per-agent custom_instructions already serves the same purpose. Reusable blocks can be added later as a DX improvement. |
| 2026-02-19 | Q9: LLM model selection? | Configurable per agent in config. LLM does not select models. | Each agent already has LLM config in TARSy. Orchestrator focuses on tasks, not infrastructure. LLM model override can be added later if needed. |
| 2026-02-19 | Q10: Orchestration depth? | Depth 1 only. No nesting. | Simple, predictable, debuggable. Revisit later if needed. |
| 2026-02-19 | Q11: Failure handling? | LLM decides. No auto-retry at orchestration level. Full failure context (status + partial output) sent to orchestrator. | LLM has context to reason about retries. Existing LLM-level retries still apply within sub-agents. |
| 2026-02-19 | Q12: Observability? | Full trace tree. Sub-agent timelines linked to orchestrator via parent execution ID. | Essential for SRE debugging. Reuses existing TARSy timeline infra. |
| 2026-02-19 | Q13: Direct MCP access? | Config-driven. Orchestrator is a regular agent — assign `mcp_servers` for hybrid mode, leave empty for pure coordinator. | No special logic needed. Operators choose the role via config. |
| 2026-02-19 | Q14: Output format for chain? | Plain text, same as any agent. Context flows via natural language (task down, result up). | Zero integration work. Orchestrator is transparent to the chain. |
| 2026-02-19 | Q15: Memory across runs? | Defer, but design with memory in mind. Don't discard investigation outputs. | Current design already supports future memory: text output (Q14), trace tree (Q12), execution_id tracking, custom_instructions injection point. |

*All 15 questions decided.*
