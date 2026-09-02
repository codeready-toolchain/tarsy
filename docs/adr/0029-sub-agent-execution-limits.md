# ADR-0029: Sub-Agent Execution Limits

**Status:** Implemented  
**Date:** 2026-09-02  
**Amends:** [ADR-0002](0002-orchestrator-impl.md), [ADR-0015](0015-implicit-orchestrator.md)

## Overview

Sub-agents are full iterating agents (LLM loop, MCP tools, `max_iterations` → forced conclusion), but they used to share a second wall clock: `orchestrator.agent_timeout`. When YAML omitted that field, the resolver still filled a hardcoded **420s**. That duration is sized like one iteration plus one tool call, not like an agent run.

A typical investigation cannot finish in seven minutes. The kill landed mid-gRPC `Generate`, the orchestrator saw a raw `DeadlineExceeded`, and forced conclusion never ran. `max_iterations` was resolved and honored by the iterating controller, but it was never the limiter.

This decision treats sub-agents as agents for budget purposes:

- Omitted `agent_timeout` means remaining **parent** (session/chat) time. No extra 7-minute hammer.
- Optional YAML `agent_timeout` is a dedicated lower cap, always clamped to remaining parent.
- Approaching a real deadline wraps up (same path as hitting `max_iterations`) instead of dying mid-thought.
- Wrap-up applies to **all** iterating agents, not only sub-agents.
- Built-in `DefaultMaxIterations` rises from 20 to 40. Unused `orchestrator.max_budget` is removed.

## Design Principles

1. **Sub-agents are agents, not fat tool calls.** They already run the iterating controller. Outer time should not be “one iteration + one tool.”
2. **`max_iterations` is the primary stop.** Hitting the limit still forces a conclusion. Time is a safety net, not the normal completion path.
3. **Deadlines wrap up.** If remaining time is at or below the wrap-up reserve, stop iterating and force-conclude while a call can still finish. Hard-cancel mid-stream is last resort (operator cancel, deadline already expired, or wrap-up itself overruns).
4. **Parent context still wins.** Session timeout (`queue.session_timeout`, default 40m) and chat (same parent in production) remain the hard outer bound. A sub-agent must never outlive its parent.
5. **Surgical.** No new YAML keys. Per-ref `max_iterations` already exists. Per-ref wall-clock timeouts are out of scope. `max_budget` is removed, not implemented.
6. **Clear causes.** Extra `agent_timeout` caps use `WithTimeoutCause` so the orchestrator sees “sub-agent timed out after …” rather than a raw gRPC deadline. Session timeout may still be a plain `DeadlineExceeded` (unchanged).
7. **Product defaults, operator YAML.** Code defaults should be usable without a dedicated `orchestrator:` block. Deployments that want a tighter cap or fewer worker iterations set YAML themselves.

## Decisions

| # | Topic | Decision | Rationale |
|---|-------|----------|-----------|
| Q1 | Dedicated wall clock | Omitted `agent_timeout` → remaining parent. If set → `min(configured, remaining parent)`. Never fall back to 420s. | Sub-agents should be timed like stage agents so `max_iterations` can actually run. A stuck worker can consume the rest of the session; operators who want a lower cap set YAML. Rejected: always-on dedicated cap (extra magic number, two clocks) and keeping 7m (does not fix the incident). |
| Q2 | Built-in `agent_timeout` default | None. Skipped. | Q1 has no extra built-in duration. There is nothing to pick. |
| Q3 | Approaching deadline | Soft deadline: wrap-up reserve = `min(LLMCallTimeout, 3m)` (constant **3m**). Clamp each iteration so it cannot steal the reserve. Cancel stays fail-fast. | Same wrap-up path as hitting `max_iterations`. A remaining-time check only between iterations is not enough: an iteration can start with just over the reserve and eat the window. Rejected: hard-cancel (today’s incident) and returning last assistant text (last turn is often a tool call). |
| Q4 | Who wraps up | All iterating agents (investigation, action, chat, sub-agent). Not single-shot or scoring. | One loop, no sub-agent special case. Stage agents near session timeout get a conclusion too. Cancel remains fail-fast. |
| Q5 | Successful wrap-up status | `completed`, same as max-iterations. Metadata reason `max_iterations` vs `time_budget`. Label formatters and dashboard. Wrap-up LLM failure → `timed_out` + `context.Cause`. | Reuses the existing forced-conclusion contract so later LLMs and the orchestrator treat a real report as finished work. Rejected: `timed_out` with a result (fights success policy) and prefixing only sub-agent injection (misses synthesis / scoring / exec summary). |
| Q6 | `max_iterations` hierarchy | Unchanged: `defaults → agent def → chain → empty stage → ref`. | Per-ref override already works. Chain-level `max_iterations` meant for the orchestrator still leaks to workers; operators who want a different worker cap set it on the agent definition or the `sub_agents:` ref. |
| Q7 | Sub-agent iteration default | No distinct default. Raise `DefaultMaxIterations` **20 → 40**. | One default everywhere. Time wrap-up still stops a runaway loop. Rejected: a second built-in (~10) that would need every focused worker to override. |
| Q8 | `max_budget` | Remove from config, API, tests, and the ADR-0002 guardrail table. | It was stored, validated, and never applied. Do not add a third nested clock. Existing YAML that still sets it is ignored by unmarshal. |

## Architecture

### Timeout stack

```text
Session / chat context                         default 40m
  └─ remaining parent                          (default)
       or min(agent_timeout, remaining parent) (if YAML sets agent_timeout)
            └─ IterationTimeout                clamped so wrap-up reserve survives
                 ├─ LLMCallTimeout             5m
                 └─ ToolCallTimeout            1m
```

`dispatch_agent` remains async: it returns immediately. The 1m tool timeout wraps only the accept call, not the worker.

`OrchestratorGuardrails.AgentTimeout == 0` means no extra cap. The resolver must not fill a duration when YAML omits the field. Validator: omitted is valid; if set, must be positive.

When YAML sets a cap:

```text
timeout := min(configured, time.Until(parentDeadline))
```

If remaining parent is shorter than or equal to the configured cap, parent wins (session timeout stays a plain `DeadlineExceeded`). If the parent has no deadline, the configured cap still applies from dispatch start, with cause `sub-agent {name} timed out after {timeout}`.

`/api/system/config` omits unset `agent_timeout` (not `"0s"`) and no longer emits `max_budget`.

### Soft deadline (wrap-up)

Constant (not YAML):

```text
wrapUpReserve = min(LLMCallTimeout, 3m)   # 3m with current 5m LLMCallTimeout
```

`remaining` is time until the parent deadline. **No deadline → no time wrap-up** (unit tests that pass an undeadlined context keep today’s loop).

At the start of each iterating loop iteration, in this order:

1. If the context is `Canceled` → fail-fast, no wrap-up (`cancelled`).
2. If the parent deadline is already expired (`remaining <= 0`) → `timed_out`, no wrap-up.
3. If `remaining <= wrapUpReserve` → force-conclude with reason `time_budget`.
4. If consecutive LLM/tool timeouts would otherwise abort **and** wrap-up is still possible → wrap up instead of `failed`. Today’s abort remains only when there is still plenty of time (stuck provider, not a budget squeeze).
5. Otherwise start the iteration with `iterTimeout = min(IterationTimeout, remaining - wrapUpReserve)`.

Tools already run under the iteration context, so clamping the iteration also clamps MCP calls.

Pending-sub-agent wait uses the same clamp. If that wait hits the bound and agents are still pending: non-blocking drain, then force-conclude with `time_budget`. Do **not** block wrap-up on pending workers — same as today’s max-iterations path.

Wrap-up’s Generate uses leftover parent time, nested under `LLMCallTimeout` (the earlier deadline wins). If wrap-up overruns, return `timed_out` using `context.Cause` when present, not the raw gRPC `DeadlineExceeded` string.

A **child** iteration/LLM deadline while the parent still has reserve is not a session timeout. Do not return `execution interrupted` from the parent. Record the failure and continue the loop so the next start-of-iteration check can wrap up.

Examples:

- YAML `agent_timeout: 20m`, dispatched at session start → work **0–17m**, wrap-up **17–20m**.
- No YAML cap, 40m session → wrap-up when **3m of session** remain.
- 5m remaining parent, no YAML cap → wrap-up immediately (remaining ≤ reserve).
- Parent deadline ≤ reserve at first iteration → wrap-up immediately. If wrap-up’s LLM also blocks until cancel, status is `timed_out` (overrun).

This lives in the iterating controller, so investigation, action, chat, and sub-agent runs all wrap up. Single-shot stages (synthesis, executive summary, compose) and scoring are unchanged.

**Later stages share leftover parent time.** Wrap-up is sized for one text-only Generate of the agent that is running, not for the rest of the chain. If the last iterating stage wraps up in the final 3m of the **session**, synthesis / compose / executive summary run on whatever is left (often little). Synthesis is fail-fast; compose and executive summary are fail-open. Sub-agent wrap-up against a dedicated `agent_timeout` does not have this problem: the orchestrator’s session budget is separate.

### Max iterations

`SubAgentRunner.Dispatch` already resolves agent config and passes `ref.MaxIterations`. The iterating controller already loops to that cap then force-concludes. That path works when time remains.

Resolution stays:

```text
defaults → agent definition → chain → stage (always empty for sub-agents) → sub-agent ref
```

`DefaultMaxIterations` is **40**. YAML `defaults.max_iterations: 40` is redundant but valid.

### Forced-conclusion contract

Successful wrap-up (iteration limit **or** time budget):

| Surface | Behavior |
|---------|----------|
| Execution status | `completed` |
| Execution / sub-agent result | Carry wrap-up reason (`max_iterations` \| `time_budget`). Empty when the agent finished normally. |
| Timeline metadata | `forced_conclusion: true`, `iterations_used`, `max_iterations`, **`reason`**: `max_iterations` \| `time_budget` |
| Forced-conclusion prompt | Iteration-limit wording vs time-budget wording. |
| Investigation formatter (synthesis / exec summary / scoring) | Read `final_analysis` event metadata; e.g. `**Final Analysis (forced conclusion — time budget):**` |
| Orchestrator injection | Mention wrap-up, e.g. `[Sub-agent completed — forced conclusion (time budget)]` |
| Dashboard | Generic “forced conclusion” plus wrap-up reason when metadata has one (not hardcoded “Max Iterations”) |

Wrap-up LLM **failure** (including overrun): status `timed_out`; error from `context.Cause` when the context deadline fired; orchestrator injects `Error` (existing non-`completed` path).

Operator cancel stays fail-fast: `cancelled`, no wrap-up.

### Error path

Optional extra cap uses `context.WithTimeoutCause`. Approaching deadline → force-conclude with reason `time_budget`. If the **parent** context still dies mid-call, `context.Cause` is the timeout message. If only a **child** iteration/LLM context died, continue the loop (see Soft deadline).

## Configuration

No new keys. `max_budget` is deleted. `agent_timeout` stays optional.

```yaml
defaults:
  max_iterations: 40          # also the built-in default
  orchestrator:
    max_concurrent_agents: 5
    # agent_timeout omitted → remaining parent (session/chat) time
    # agent_timeout: 20m     # optional dedicated cap, still clamped to parent

agents:
  KubernetesAgent:
    description: "Kubernetes troubleshooting agent"
    mcp_servers: [kubernetes-server]

agent_chains:
  kubernetes-investigation-orchestrated:
    stages:
      - name: investigation
        agents:
          - name: KubernetesAgent
            sub_agents:
              - name: KubernetesAgent
                max_iterations: 12    # already supported; optional per-ref override
              - name: GeneralWorker
                max_iterations: 5
```

### Chat

Chat uses the same sub-agent runner. In production, chat session timeout is `queue.session_timeout` (default 40m). Tests may use a shorter chat timeout. Runner and controller changes apply to both paths. `min(agent_timeout, remaining parent)` still holds.

## Out of scope

- Per-sub-agent-ref wall-clock timeout. One optional orchestrator-level `agent_timeout` is enough; per-ref `max_iterations` already exists.
- Changing hardcoded `ToolCallTimeout` (1m). Some MCP servers use a longer HTTP timeout; that mismatch is separate from the former 7-minute agent kill.
- Skipping `ToolCallTimeout` on `dispatch_agent` (already returns in milliseconds).
- Nesting (depth remains 1).
- Adding `WithTimeoutCause` to the session/chat parent itself.

## Future Considerations

- A dedicated “wrap-up succeeded” e2e needs parent time greater than 3m plus a forced squeeze; unit tests cover that path.
- Later-stage leftover budget after session wrap-up (synthesis / compose / exec summary in the final minutes) is accepted, not redesigned here.

## Related

- [ADR-0002: Orchestrator Agent](0002-orchestrator-impl.md) — guardrails, session-derived sub-agent context
- [ADR-0015: Implicit Orchestrator](0015-implicit-orchestrator.md) — orchestration as a capability; `orchestrator:` on any agent
