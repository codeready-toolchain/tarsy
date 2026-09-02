# Sub-Agent Execution Limits

**Status:** Final
**Related:** [Questions document](sub-agent-execution-limits-questions.md)

## Overview

Sub-agents are full iterating agents (LLM loop + MCP tools + `max_iterations` → forced conclusion), but their lifetime is capped by `orchestrator.agent_timeout`, whose hardcoded default is **420s (7 minutes)**. That value is sized like one iteration (6m) plus one tool call (1m), not like an agent run.

When YAML omits `agent_timeout`, the resolver still fills in 420s. A typical investigation cannot finish in 7 minutes, so the kill lands mid-gRPC `Generate` and the orchestrator sees:

```text
execution interrupted: LLM error: rpc error: code = DeadlineExceeded
  desc = context deadline exceeded (code: , retryable: false)
```

`max_iterations` is resolved and honored by the iterating controller, but it never becomes the limiter: dozens of iterations cannot complete in 7 minutes. Forced conclusion only runs after the iteration loop ends; a mid-call deadline returns immediately with no wrap-up.

This design treats sub-agents as agents for budget purposes:

- Omitted `agent_timeout` means remaining **parent** (session/chat) time. No extra 7-minute hammer.
- Optional YAML `agent_timeout` is a dedicated lower cap, always clamped to remaining parent.
- Approaching a real deadline wraps up (same path as hitting `max_iterations`) instead of dying mid-thought.
- Wrap-up applies to **all** iterating agents, not only sub-agents.
- Built-in `DefaultMaxIterations` rises from 20 to 40. Unused `orchestrator.max_budget` is removed.

## Design Principles

1. **Sub-agents are agents, not fat tool calls.** They already run `IteratingController`. Outer time should not be “one iteration + one tool.”
2. **`max_iterations` is the primary stop.** Hitting the limit still forces a conclusion. Time is a safety net, not the normal completion path.
3. **Deadlines wrap up.** If remaining time is at or below the wrap-up reserve, stop iterating and force-conclude while a call can still finish. Hard-cancel mid-stream is last resort (operator cancel, deadline already expired, or wrap-up itself overruns).
4. **Parent context still wins.** Session timeout (`queue.session_timeout`, default 40m) and chat (same parent in production) remain the hard outer bound. A sub-agent must never outlive its parent.
5. **Surgical.** No new YAML keys. Per-ref `max_iterations` already exists. Per-ref wall-clock timeouts are out of scope. `max_budget` is removed, not implemented.
6. **Clear causes.** Extra `agent_timeout` caps use `WithTimeoutCause` so the orchestrator sees “sub-agent timed out after …” rather than a raw gRPC deadline. Session timeout may still be a plain `DeadlineExceeded` (unchanged).
7. **Product defaults, operator YAML.** Code defaults should be usable without a dedicated `orchestrator:` block. Deployments that want a tighter cap or fewer worker iterations set YAML themselves.

## Decisions

| Topic | Decision |
|-------|----------|
| Dedicated wall clock | Omitted `agent_timeout` → remaining parent. If set → `min(configured, remaining parent)`. Never fall back to 420s. |
| Built-in `agent_timeout` default | None. Q2 skipped. |
| Approaching deadline | Soft deadline: `wrapUpReserve = min(LLMCallTimeout, 3m)` (constant **3m**). Clamp each iteration so it cannot steal the reserve. Cancel stays fail-fast. |
| Who wraps up | All iterating agents (investigation, action, chat, sub-agent). Not single-shot or scoring. |
| Successful wrap-up status | `completed`, same as max-iterations. Metadata reason `max_iterations` vs `time_budget`. Label formatters and dashboard. Wrap-up LLM failure → `timed_out` + `context.Cause`. |
| `max_iterations` hierarchy | Unchanged: `defaults → agent def → chain → empty stage → ref`. |
| Sub-agent iteration default | No distinct default. Raise `DefaultMaxIterations` **20 → 40**. |
| `max_budget` | Remove from config, API, tests, and ADR-0002 table. |

## Architecture / How It Works

### Current timeout stack

```text
Session / chat context          default 40m  (queue.session_timeout)
  └─ agent_timeout              7m           ← binding constraint (hardcoded)
       └─ IterationTimeout      6m
            ├─ LLMCallTimeout   5m
            └─ ToolCallTimeout  1m           (MCP tools inside the agent)
```

`dispatch_agent` is already async: it returns immediately. The 1m `ToolCallTimeout` wraps only the accept call, not the worker. The 7m timer is a second deadline applied in `SubAgentRunner.Dispatch`:

```text
subCtx, cancel := context.WithTimeout(parentCtx, guardrails.AgentTimeout)
go runSubAgent(subCtx, ...)
```

`parentCtx` is the session/chat context. The extra cap is applied on top.

`orchestrator.max_budget` (default 900s in the resolver) is stored on `OrchestratorGuardrails` and **never applied**. The system-config API only emits YAML fields, so omitted `max_budget` does not appear there; the dead default is the resolver. ADR-0002 documents `agent_timeout` default 300s; the code default is 420s. Both are wrong after this work.

### Intended stack

```text
Session / chat context                         default 40m
  └─ remaining parent                          (default)
       or min(agent_timeout, remaining parent) (if YAML sets agent_timeout)
            └─ IterationTimeout                clamped so wrap-up reserve survives
                 ├─ LLMCallTimeout             5m
                 └─ ToolCallTimeout            1m
```

`OrchestratorGuardrails.AgentTimeout == 0` means no extra cap. Resolver must not fill in 420s when YAML omits the field. Validator: omitted is valid; if set, must be positive (unchanged).

When YAML sets a cap:

```text
timeout := min(configured, time.Until(parentDeadline))
subCtx, cancel := context.WithTimeoutCause(parentCtx, timeout,
    fmt.Errorf("sub-agent %s timed out after %s", name, timeout))
```

If the parent has no deadline, a configured `agent_timeout` still applies from dispatch start. If remaining parent is shorter than the configured cap, parent wins (never extend past the session/chat).

`/api/system/config` already omits nil YAML pointers. After this work it also drops `max_budget`. Unset `agent_timeout` stays omitted (not `"0s"`).

### Soft deadline (wrap-up)

Constant (not YAML):

```text
wrapUpReserve = min(LLMCallTimeout, 3m)   # 3m with current 5m LLMCallTimeout
```

`remaining` is `time.Until(deadline)` when `ctx` has a deadline. **No deadline → no time wrap-up** (unit tests that pass `context.Background()` keep today’s loop).

At the start of each loop iteration, in this order:

1. If `ctx` is `Canceled` → fail-fast, no wrap-up (`cancelled`).
2. If the parent deadline is already expired (`remaining <= 0`) → `timed_out`, no wrap-up (nothing left for a Generate).
3. If `remaining <= wrapUpReserve` → `forceConclusion` with reason `time_budget`.
4. If consecutive LLM/tool timeouts would otherwise abort (`ShouldAbortOnTimeouts`) **and** wrap-up is still possible (`remaining > 0`) → wrap up instead of returning `failed` from consecutive timeouts. Today’s abort remains only when there is still plenty of time (stuck provider, not a budget squeeze).
5. Otherwise start the iteration with:

```text
iterTimeout = min(IterationTimeout, remaining - wrapUpReserve)
```

Tools already run under `iterCtx`, so clamping the iteration also clamps MCP calls.

**`WaitForResult` must use the same clamp.** Today it waits on the parent `ctx`, so a pending-sub-agent wait can consume the wrap-up reserve. Bound that wait with `min(remaining - wrapUpReserve, …)` (or the iteration context). If the wait hits that bound and agents are still pending, treat it like “time to wrap up”: non-blocking drain, then `forceConclusion` with `time_budget`. Do **not** block wrap-up on pending workers — same as today’s max-iterations path.

Wrap-up’s Generate uses leftover parent time, nested under `LLMCallTimeout` (the earlier deadline wins). If wrap-up overruns, return `timed_out` using `context.Cause(ctx)` when present, not the raw gRPC `DeadlineExceeded` string.

A **child** `iterCtx`/`llmCtx` deadline while the parent still has reserve is not a session timeout. Do not return `execution interrupted` from `StatusFromContextErr(parent)`. Record the failure and continue the loop so the next start-of-iteration check can wrap up. Combined with step 4, two clamped timeouts near the reserve wrap up instead of `aborted after 2 consecutive timeouts`.

Examples:

- YAML `agent_timeout: 20m`, dispatched at session start → work **0–17m**, wrap-up **17–20m**.
- No YAML cap, 40m session → wrap-up when **3m of session** remain.
- 5m remaining parent, no YAML cap → wrap-up immediately (remaining ≤ reserve).
- Parent deadline ≤ reserve at first iteration (e.g. 2s test session) → wrap-up immediately. If wrap-up’s LLM also blocks until cancel, status is `timed_out` (overrun).

This lives in `IteratingController`, so investigation, action, chat, and sub-agent runs all wrap up. Single-shot stages (synthesis, executive summary, compose) and scoring are unchanged.

**Later stages share leftover parent time.** Wrap-up is sized for one text-only Generate of the agent that is running, not for the rest of the chain. If the last iterating stage wraps up in the final 3m of the **session**, synthesis / compose / executive summary run on whatever is left (often little). Synthesis is fail-fast; compose and executive summary are fail-open. Sub-agent wrap-up against a dedicated `agent_timeout` does not have this problem: the orchestrator’s session budget is separate.

### Max iterations

`SubAgentRunner.Dispatch` already calls `ResolveAgentConfig` and passes `ref.MaxIterations`. The iterating controller already loops to `MaxIterations` then `forceConclusion`. That path works when time remains.

Resolution stays:

```text
defaults → agent definition → chain → stage (always empty for sub-agents) → sub-agent ref
```

Chain-level `max_iterations` meant for the orchestrator still leaks to workers. Operators who want a different worker cap set it on the agent definition or the `sub_agents:` ref. No second resolution path.

`DefaultMaxIterations` changes from 20 to 40. YAML `defaults.max_iterations: 40` is redundant but valid.

### Forced-conclusion contract

Successful wrap-up (iteration limit **or** time budget):

| Surface | Behavior |
|---------|----------|
| Execution status | `completed` |
| `ExecutionResult` | Carry wrap-up reason (`max_iterations` \| `time_budget`) so callers do not read the DB. Empty when the agent finished normally. |
| `SubAgentResult` | Copy that reason. `FormatSubAgentResult` uses it. |
| Timeline metadata | `forced_conclusion: true`, `iterations_used`, `max_iterations`, **`reason`**: `max_iterations` \| `time_budget` |
| Forced-conclusion prompt | Iteration-limit wording today; time-budget wording when reason is `time_budget`. `PromptBuilder` gains a reason (interface + mocks). |
| Investigation formatter (synthesis / exec summary / scoring) | Read `final_analysis` event metadata; e.g. `**Final Analysis (forced conclusion — time budget):**` |
| Orchestrator injection (`FormatSubAgentResult`) | Mention wrap-up, e.g. `[Sub-agent completed — forced conclusion (time budget)]` |
| Dashboard | Generic “forced conclusion”, not hardcoded “Max Iterations” |

Wrap-up LLM **failure** (including overrun):

- Status `timed_out`
- Error from `context.Cause` when the context deadline fired
- Orchestrator injects `Error` (existing non-`completed` path)

Operator cancel stays fail-fast: `cancelled`, no wrap-up.

### Error path (today vs proposed)

Today, deadline during `Generate`:

1. gRPC `Recv` returns `DeadlineExceeded`
2. `ErrorChunk{Message: err.Error(), Code: ""}`  ← empty code, `retryable: false`
3. Iterating controller sees `ctx.Err()` and returns immediately: `execution interrupted: LLM error: rpc error: …`
4. Orchestrator injects `[Sub-agent timed_out] …: execution interrupted: LLM error: …`
5. No forced conclusion, no partial report

Proposed:

1. Optional extra cap uses `context.WithTimeoutCause`
2. Approaching deadline → force-conclude with reason `time_budget`
3. If the **parent** context still dies mid-call, `context.Cause(ctx)` is the timeout message; do not surface the raw gRPC string as the primary error
4. If only a **child** iteration/LLM context died, continue the loop (see Soft deadline)

### `max_budget`

Remove `MaxBudget` from `OrchestratorConfig`, `OrchestratorGuardrails`, the resolver, the validator, `/api/system/config`, tests, example YAML, and the ADR-0002 guardrail table. YAML that still sets `max_budget` is ignored by `yaml.Unmarshal` (unknown field; this repo does not use `KnownFields`). Do not implement a third nested clock.

### Out of scope

- Per-sub-agent-ref wall-clock timeout. One optional orchestrator-level `agent_timeout` is enough; per-ref `max_iterations` already exists.
- Changing hardcoded `ToolCallTimeout` (1m). Some MCP servers use a longer HTTP timeout; that mismatch is separate from the 7-minute agent kill.
- Skipping `ToolCallTimeout` on `dispatch_agent` (already returns in milliseconds).
- Nesting (depth remains 1).
- Adding `WithTimeoutCause` to the session/chat parent itself.

## Core Concepts

| Concept | Role |
|---------|------|
| Parent context | Session or chat timeout. Always the hard ceiling. |
| `agent_timeout` | Optional extra per-sub-agent wall clock from dispatch start. Omitted → remaining parent. If set → `min(configured, remaining parent)`. |
| `max_iterations` | Primary stop. Loop ends → `forceConclusion` (tools bound, calling disabled). Built-in default 40. |
| Per-call timeouts | Unchanged: 6m iteration / 5m LLM / 1m MCP tool, except the iteration (and pending-result wait) is clamped so the wrap-up reserve survives. |
| Wrap-up reserve | Constant `min(LLMCallTimeout, 3m)`. Remaining ≤ reserve → force-conclude with reason `time_budget`. No parent deadline → no time wrap-up. |
| Forced-conclusion reason | `max_iterations` or `time_budget`. Status stays `completed` when wrap-up succeeds. Carried on the result struct, timeline metadata, formatters, and dashboard. |

### Config surface

No new keys. `max_budget` is deleted. `agent_timeout` stays optional.

```yaml
defaults:
  max_iterations: 40          # also the new built-in default
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

Chat uses the same `SubAgentRunner`. In production, `cmd/tarsy` sets `ChatMessageExecutorConfig.SessionTimeout` to `queue.session_timeout` (default 40m). Tests may use a shorter chat timeout. Runner and controller changes apply to both paths. `min(agent_timeout, remaining parent)` still holds.

## Implementation Plan

Split so each PR leaves the product working.

### PR 1 — Outer budget, defaults, and config honesty - DONE

**Lands:**

- `SubAgentRunner.Dispatch`: if `AgentTimeout == 0`, use `parentCtx` as-is. If set, `min(configured, remaining parent)` and `context.WithTimeoutCause`. Never apply a hardcoded 420s.
- Resolver: omit `agent_timeout` → `AgentTimeout` stays 0. Do not fill a default duration.
- Remove `MaxBudget` from config structs, resolver, validator, system-config API, and tests.
- Raise `DefaultMaxIterations` from 20 to 40; update tests that assert the constant (controller test helpers that hardcode 20 may stay — they are local fixtures).
- `deploy/config/tarsy.yaml.example`: drop `max_budget`, stop advertising a 420s/600s `agent_timeout` default, note omit = parent remaining, `max_iterations` default 40.

**Where:** Orchestrator runner, orchestrator guardrails, config resolver/validator, system-config API, example YAML comments.

**Tests in this PR:**

- Dispatch with no `agent_timeout` does not add a shorter deadline than the parent.
- Dispatch with a short `agent_timeout` still times out; cause string is inspectable.
- When parent deadline is sooner than `agent_timeout`, parent wins.
- Existing `TestSubAgentRunner_Dispatch_Timeout` updated for cause and for “omit means parent.”
- Guardrail / system-config tests no longer expect `max_budget`.
- Resolver tests for `DefaultMaxIterations == 40`.

**Gap:** Wrap-up not yet implemented — a deadline mid-LLM still looks like a gRPC error until PR 2. Unconfigured deploys already stop dying at 7:00.

### PR 2 — Soft-deadline wrap-up and labeling - DONE

**Lands:**

- Iterating controller: wrap-up reserve, iteration clamp, `WaitForResult` clamp, wrap-up **before** consecutive-timeout abort, `forceConclusion` with reason `time_budget` vs `max_iterations`.
- `ExecutionResult` + `SubAgentResult` carry the reason; `FormatSubAgentResult` uses it.
- Cancel / expired deadline skip wrap-up.
- Forced-conclusion prompt distinguishes time budget from iteration limit (`PromptBuilder` interface + mocks).
- Timeline metadata includes `reason`.
- Investigation formatter labels forced conclusions from event metadata.
- Dashboard: generic “forced conclusion” (not hardcoded “Max Iterations”).
- Mid-call **parent** timeout errors prefer `context.Cause`.

**Where:** Iterating controller, prompt builder, investigation formatter, orchestrator result types/formatting, dashboard `finalAnalysisPresentation` (no test file today — add one).

**Tests in this PR:**

- Unit tests: no deadline → no time wrap-up; remaining ≤ reserve invokes wrap-up; remaining ≤ 0 does not call the LLM; iteration clamp leaves reserve; `WaitForResult` cannot eat the reserve; cancel does not wrap up; wrap-up overrun → `timed_out`; consecutive child timeouts near the reserve wrap up instead of `failed`.
- Existing max-iterations forced-conclusion tests still pass and gain `reason: max_iterations`.
- Formatter / `FormatSubAgentResult` tests for wrap-up labels.
- Dashboard presentation unit tests for generic copy.
- E2E: `test/e2e/timeout_test.go` uses a 2s session and `BlockUntilCancelled`. First iteration will take the wrap-up path (2s ≤ 3m). If the LLM still blocks until cancel, terminal status can remain `timed_out` (overrun); update assertions if they require an iteration-path event. Other short-timeout e2e/chat tests similarly. Prefer extending that scenario over a new harness. If a dedicated “wrap-up succeeded” e2e is awkward (needs parent > 3m plus a forced squeeze), say so in the PR and rely on unit tests.

**Gap:** None vs the decided contract if the unit tests above land. If a success-path e2e is deferred, unit tests still lock the controller behavior.

### PR 3 — Docs

**Lands:** Update ADR-0002 guardrail table (stale 300s `agent_timeout` and unused `max_budget`), ADR-0015 example YAML if it still lists a 300s default as if required, `docs/architecture-overview.md` (`max_iterations` default 20, `max_budget` mention). State the decided contract; do not copy this proposal verbatim. Example YAML comments already landed in PR 1.

**Tests:** None.

## References

- [ADR-0002: Orchestrator Agent](../adr/0002-orchestrator-impl.md) — guardrails, session-derived sub-agent context
- [ADR-0015: Implicit Orchestrator](../adr/0015-implicit-orchestrator.md) — orchestration as a capability; `orchestrator:` on any agent
