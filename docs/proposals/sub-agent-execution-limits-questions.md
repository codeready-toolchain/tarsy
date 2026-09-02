# Sub-Agent Execution Limits — Design Questions

**Status:** Closed — all questions decided
**Related:** [Design document](sub-agent-execution-limits-design.md)

---

## Q1: Should sub-agents have a dedicated wall-clock shorter than the parent session/chat context?

Today every sub-agent gets `context.WithTimeout(parentCtx, 420s)` on top of the session (or chat) context. That extra cap is what kills long-running workers at exactly 7:00. Dispatch is already async; this timer is not the MCP tool timeout.

Dropping the extra cap means a sub-agent can run until the parent expires, bounded by `max_iterations` and per-call timeouts (6m / 5m / 1m). Keeping a cap preserves a runaway-worker guard independent of session length.

### Option A: Parent context only (no extra cap unless configured)

- **Pro:** Sub-agents are timed like stage agents. `max_iterations` becomes the real stop. Matches “treat them as agents, not tool calls.”
- **Pro:** Simplest: `subCtx = parentCtx` (or `WithTimeout` only when YAML sets `agent_timeout`).
- **Pro:** No new magic-number default. Unconfigured deploys stop dying at 7:00.
- **Con:** A stuck sub-agent can consume the rest of the session and block the orchestrator’s wait-for-pending path.
- **Con:** Concurrent workers (`max_concurrent_agents`) can run that long in parallel — LLM cost, not extra wall clock.

**Semantics:**
- Omitted `agent_timeout` → no extra timer; use remaining parent (session/chat) time.
- Set in YAML (`defaults.orchestrator.agent_timeout`, optional per-agent `orchestrator:` override) → `min(configured, remaining parent)`. Never outlive the session.
- Unset must **not** fall back to hardcoded 420s (today’s bug).

**Decision:** Option A — remaining parent timeout by default; admins may set a dedicated lower `agent_timeout` globally (and per-agent if they want). Always clamp to remaining parent when set.

_Considered and rejected: Option B (always-on dedicated cap with a new built-in default — extra magic number, still two clocks to reason about), Option C (keep 7m — does not fix the incident)._

---

## Q2: If a dedicated cap remains, what should the default `agent_timeout` be?

**Skipped** — Q1 Option A has no extra built-in default. Omitted `agent_timeout` means parent context. There is nothing to pick (15m / 20m / 7m). Deployments that want a lower cap set it in YAML.

---

## Q3: What should happen when the sub-agent deadline approaches?

Today the iterating loop returns immediately on `ctx` deadline: no forced conclusion, error wrapped as an LLM gRPC failure. `max_iterations` wrap-up never runs.

A remaining-time check **only between iterations** is not enough: an iteration can start with just over the reserve left, run up to `IterationTimeout` (6m), and eat the wrap-up window.

### Option B: Soft deadline — reserved slice + iteration clamp

`wrapUpReserve = min(LLMCallTimeout, 3m)` (default: **3m**). Wrap-up is one text-only Generate; it does not need the full 5m iterating LLM budget.

- Before a normal iteration: if remaining ≤ reserve → force-conclude now.
- Clamp that iteration to `min(IterationTimeout, remaining - reserve)` so in-flight work cannot steal the reserve.
- Wrap-up’s Generate uses whatever is left (capped by `LLMCallTimeout`).
- If wrap-up overruns → hard-cancel with `context.Cause`, not a raw gRPC string.
- Operator **cancel** (`Canceled`) stays fail-fast — no wrap-up.

Example: 20m `agent_timeout`, dispatched at session start → work **0–17m**, wrap-up **17–20m**. No YAML cap → wrap-up when **3m of parent** remain.

- **Pro:** Same wrap-up path as hitting `max_iterations`. Orchestrator (or stage) gets a report.
- **Pro:** Predictable last-N-minutes window; in-flight iteration cannot consume it.
- **Con:** 3m may be tight for a huge-context “write the full report” call.
- **Con:** If wrap-up still overruns, hard-cancel.

**Decision:** Option B — reserved slice of `min(LLMCallTimeout, 3m)`, iteration clamp, cancel stays fail-fast. 3m is a constant, not YAML.

_Considered and rejected: Option A (hard-cancel — discards findings, today’s incident), Option C (return last assistant text — last turn is often a tool call, not a report)._

---

## Q4: Should soft-deadline wrap-up apply only to sub-agents, or to every iterating agent?

The natural implementation is in `IteratingController`. That fires for stage agents near session timeout too.

### Option B: All iterating agents (deadline only, not cancel)

- **Pro:** One helper, no `SubAgent != nil` branch. Session timeout and optional `agent_timeout` wrap up the same way.
- **Pro:** Regular agents hitting session timeout get a conclusion instead of a gRPC error.
- **Con:** Stage agents start wrapping up ~3m before session timeout (slightly earlier stop than today).

Not applied to: single-shot (synthesis, exec summary, compose — already one LLM call), scoring (separate timeout after the session).

**Decision:** Option B — every iterating agent (investigation, action, chat, sub-agent). Cancel remains fail-fast. Single-shot and scoring unchanged.

_Considered and rejected: Option A (sub-agents only — special-cases the shared loop; stage agents still die mid-LLM at session timeout)._

---

## Q5: If wrap-up succeeds, what execution status should the sub-agent report?

`FormatSubAgentResult` injects `Result` only when status is `completed`; otherwise it injects `Error`. Max-iterations wrap-up today returns `completed`.

Timeline events already carry `forced_conclusion: true` (plus `iterations_used` / `max_iterations`). The dashboard shows `CONCLUSION (⚠️Max Iterations)`. Downstream LLMs (synthesis, exec summary, scoring, orchestrator injection) currently get **analysis text only** — they do not read that metadata.

### Option A: `completed` + surface wrap-up in downstream context

- Status stays `completed` (same as max-iterations wrap-up). Orchestrator and parallel-stage success policy treat it as a finished analysis.
- Metadata keeps `forced_conclusion: true` and adds a **reason** (`max_iterations` vs `time_budget`).
- Formatters that feed later LLMs label it, e.g. `**Final Analysis (forced conclusion — time budget):**`, and orchestrator injection mentions wrap-up. Scoring/synthesis/exec summary can discount a rushed report.
- Dashboard label becomes generic “forced conclusion”, not hardcoded “Max Iterations”.
- If wrap-up’s LLM call **fails**, status is `timed_out` with `context.Cause`.

- **Pro:** Reuses the existing forced-conclusion contract; later LLMs actually see the signal (they do not today).
- **Pro:** Does not mark a useful report as a failed sub-agent.
- **Con:** Formatters and dashboard copy need a small update.

**Decision:** Option A — `completed` plus explicit wrap-up labeling in timeline context, orchestrator injection, and dashboard. Reason in metadata (`max_iterations` vs `time_budget`). Wrap-up LLM failure stays `timed_out`.

_Considered and rejected: Option B (`timed_out` with Result — fights success policy; orchestrator treats a real report as failure), Option C (prefix only on sub-agent injection — misses synthesis/scoring/exec summary)._

---

## Q6: How should `max_iterations` resolve for sub-agents?

Today: `defaults → agent def → chain → empty stage → ref`. If a chain later sets `max_iterations` for the orchestrator, workers inherit it because `Dispatch` passes the parent chain and an empty stage.

### Option A: Keep the current hierarchy

- **Pro:** No code change. Per-ref override already works (`max_iterations` on `sub_agents:` entries).
- **Con:** Chain-level `max_iterations` meant for the orchestrator leaks to workers.

**Decision:** Option A — keep `defaults → agent def → chain → stage (empty) → ref`. Per-ref/per-agent YAML remains the way to give workers a different cap.

_Considered and rejected: Option B (skip chain/stage — extra resolution path for a leak operators can already override per ref), Option C (apply orchestrator stage max_iterations to workers)._

---

## Q7: Should sub-agents have a distinct built-in `max_iterations` default (lower than 20/40)?

Code `DefaultMaxIterations` is 20. Focused workers (a single lookup, a narrow cluster check) rarely need 40 turns. A new default would affect every deployment.

### Option A: No new sub-agent default — raise the global code default to 40

Same hierarchy as today (Q6). No second implicit number for workers. Raise TARSy `DefaultMaxIterations` from **20 → 40** so unconfigured deploys can finish a realistic iterating investigation. Per-ref/per-agent YAML still lowers a worker if needed. Time wrap-up (Q3) still stops a runaway loop.

- **Pro:** One default everywhere; YAML `defaults.max_iterations: 40` becomes redundant but harmless.
- **Con:** Deployments that relied on the code default of 20 get more iterations (capped by time wrap-up / session timeout).

**Decision:** Option A — no distinct sub-agent default. Change TARSy `DefaultMaxIterations` from 20 to 40.

_Considered and rejected: Option B (built-in sub-agent default of ~10 — extra magic number; a focused lookup and a full investigation would share it unless every ref overrides)._

---

## Q8: What should we do with unused `orchestrator.max_budget`?

It is validated, merged, shown in system config, default 900s, and never read by the runner. ADR-0002 says it is “total orchestrator budget.”

### Option B: Remove it (config, API view, tests, ADR)

Drop `MaxBudget` from `OrchestratorConfig`, guardrails, `/api/system/config`, tests, example YAML, and the ADR-0002 guardrail table. Existing YAML that still sets `max_budget` is ignored by normal YAML unmarshal (unknown field). No third nested clock.

- **Pro:** Honest config surface; no dead knob next to the real `agent_timeout`.
- **Con:** Docs/examples that mentioned `max_budget` need a pass. Anyone who set it in YAML keeps a no-op key until they delete it.

**Decision:** Option B — remove `max_budget`. Do not implement it in this work.

_Considered and rejected: Option A (leave unused — keeps lying in API/docs), Option C (implement as orchestrator wall clock — three nested deadlines, easy to recreate a short hammer)._
