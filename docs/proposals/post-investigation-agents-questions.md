# Post-Investigation Agents — Design Questions

**Status:** Resolved — all decisions made
**Related:** [Design document](post-investigation-agents-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Execution model — where do post-investigation agents run?

Post-investigation agents need MCP tool execution, their own timeouts and graceful shutdown. `ScoringExecutor` has no MCP factory and a 10-minute scoring timeout; `ChatMessageExecutor` already runs MCP-enabled agents after a session is terminal; `RealSessionExecutor.executeStage` runs chain stages but also updates session progress.

### Option A: Dedicated `PostInvestigationExecutor` (modeled on chat executor)
- **Pro:** Clean separation (same reasoning as ADR-0008 Q3 for scoring); own timeouts, cancel registry, shutdown; reuses shared helpers (`createToolExecutor`, `resolveMCPSelection`, `AgentFactory`, publish helpers).
- **Con:** Another executor to wire and shut down; some boilerplate similar to chat/scoring.

**Decision:** Option A — matches the existing pattern (scoring and chat each have their own executor) and keeps the session executor focused on the investigation.

_Considered and rejected: Option B — run inside `ScoringExecutor` (needs MCP + longer timeouts, keeps scoring spinner on, mixes concerns), Option C — reuse `RealSessionExecutor.executeStage` (built around an active session; post-terminal use needs fragile flags)_

---

## Q2: Config shape

### Option C: Independent single-agent entries, with `sub_agents` allowed on the entry
`post_investigation: [{name, agent, gate, pairing overrides, timeout, sub_agents}]`
- **Pro:** Simple mental model and validation; each agent independent (one failing doesn't affect others); fits the use cases (e.g. a detection-config proposer, an MCP tool-gap reporter and a runbook updater are unrelated). Lets an agent delegate (e.g. to WebResearcher, CodeExecutor) without full stage semantics.
- **Con:** No multi-agent/replica stages or chaining between agents.
- **Cost of sub-agents (verified):** ~40 lines of wiring mirroring `ChatMessageExecutor` (`orchestrator.NewSubAgentRunner` + `NewCompositeToolExecutor` + `NewResultCollector`, `pkg/queue/chat_executor.go:364-406`), ideally extracted into a shared helper used by both; validation via existing `validateSubAgentRefs`; the action prompt builder already injects orchestrator sections when the catalog is non-empty (`pkg/agent/prompt/action.go:37`); sub-agent timeouts are already clamped to remaining parent time; one extra e2e case.

**Decision:** Option C — sub-agent support is cheap because the orchestration machinery is already reusable from chat.

_Considered and rejected: Option A — single-agent entries without sub-agents (C costs little more and avoids a config change later), Option B — full `StageConfig` list with sequential context passing (synthesis/replicas/success policies need post-terminal semantics with no current use case)_

---

## Q3: Naming

Affects config key, stage type enum, dashboard labels, docs.

### Option D: "post-investigation" (`post_investigation:`, `stage_type: post_investigation`; entries called "post-investigation agents")
- **Pro:** Says when it runs; trigger-agnostic (scoring and human review both happen after the investigation); no collisions in code (only 3 loose mentions in docs).
- **Con:** Long; describes timing, not purpose.

**Decision:** Option D — descriptive and collision-free.

_Considered and rejected: Option A — "follow-up" ("follow-up" is the established term for chat across code, prompts, dashboard and memory), Option B — "post-scoring" (wrong once other triggers exist), Option C — "improvement" (too narrow), "handoff" (strong alternative, but fits less well for internal-only uses), "automation" (confusable with ADR-0007 automated actions), "epilogue" (whimsical), "downstream" (already used informally), "hooks" (implies scripts), "wrap-up" (already used in code)_

---

## Q4: Gate expressiveness

Which sessions a post-investigation agent runs on. Sessions closed as false positives (e.g. `noise` in the builtin label map) with a correct conclusion are a good source of suppression-type proposals, so the gate should not just mean "threat confirmed".

### Option B: optional `min_score` + optional `labels_any` / `labels_none`
- **Pro:** Covers outcome scoping using existing session labels (ADR-0031); still declarative and easy to validate against the chain's label map.
- **Con:** Slightly more config surface.

Semantics:
- All gate fields are optional. Unset field = no condition. With no gate fields the agent runs after every completed scoring.
- `min_score: N` — runs only if `total_score >= N` (0–100).
- `labels_any: [a, b]` — runs only if the session has at least one listed label (unlabeled sessions do not match).
- `labels_none: [c]` — skipped if the session has any listed label.
- All set conditions must pass (AND).
- Load-time validation: label names must exist in the chain's resolved label map; a label may not appear in both lists; empty lists are treated as unset.
- Example (builtin label map `watch` / `action` / `noise`): `min_score: 60`, `labels_any: [action, noise]` — confirmed problems and confirmed false positives; skips `watch` and unlabeled.

**Decision:** Option B, with `labels_none` included and every gate field optional.

_Considered and rejected: Option A — `min_score` only (can't scope by outcome), Option C — CEL/expression gate (new dependency and mini-language; overkill now)_

---

## Q5: Context given to the post-investigation agent

An agent proposing configuration changes (e.g. detection patterns) needs exact strings from the investigation (command lines, file names, image names). Final reports usually contain them, but not always verbatim.

### Option C: Compact by default, `include_investigation: true` opt-in per post-investigation agent
- Compact = session metadata + deep link, alert data, final report (compose if present), executive summary, labels, scoring verdict (score, analysis, failure tags, tool improvement report).
- Opt-in adds the full investigation timeline built by the existing `InvestigationContextBuilder` (same as scoring).
- **Pro:** Each agent chooses its cost/precision trade-off; reuses the existing builder.
- **Con:** One more knob.

**Decision:** Option C — start agents on compact context; enable `include_investigation` if proposals lack precise evidence.

_Considered and rejected: Option A — compact only (may miss exact evidence strings for precise patterns), Option B — always full timeline (expensive on every gated session; more raw attacker-controlled content)_

---

## Q6: Agent type / safety layer

Determines the auto-injected behavioral prompt layer and which agent definitions are valid as post-investigation agents.

### Option B: New `post_investigation` agent type with its own behavioral layer
- **Pro:** Wording fits the role (follow-through outside the investigation, not remediation; research allowed; no action is normal; report links).
- **Con:** New agent type touches a few switch points (see cost below).

Fit analysis of the existing action layer (`pkg/agent/prompt/action.go`, `templates.go`):
- **Carries over:** hard evidence before acting; ambiguous → report, don't act; explain before tools; prefer inaction; short memo; YES/NO marker → `actions_executed`.
- **Doesn't fit:** remediation framing ("action evaluation agent… automated remediation actions") — risks "no remediation needed" → skip; "avoid re-investigating" discourages needed research (current state of the target system, existing tickets); "## Previous Stage Data" user-message header. The built-in label map already treats "filing a backlog ticket; tuning detection/rules" as not-an-action (`pkg/config/labels.go:88`).

Cost (verified, small):
- `AgentTypePostInvestigation` in `pkg/config/enums.go` (+ `IsValid`).
- `controller/factory.go`: case → `NewIteratingController()`.
- New prompt builder (~60 lines): reuses the action safety bullets and `ActionOutputSchema` YES/NO marker; own role/task wording; own user-message builder (needed regardless because of the custom context, Q5).
- `iterating.go` prompt-cache check: off, same as action. Memory: tools available (`search_past_sessions`, `recall_past_investigations`) without briefing injection, same as chat (see design, "Memory tools, no briefing").
- Validator: type valid only in `post_investigation` entries; entries reject other explicit types; agents with no type are treated as `post_investigation` there.
- Golden test `testdata/function_calling_post_investigation.golden` + validator tests.

Draft behavioral layer (refine in implementation):

```text
## Post-Investigation Agent Guidelines

The investigation is complete and has been scored. Your role is to decide whether a
follow-through outside this investigation is warranted (as defined by your instructions)
and, if so, carry it out with your tools. You do not remediate the incident.

- Base decisions on evidence from the investigation, not on speculation
- You may use your tools to gather the extra context your task needs (e.g. current state
  of the target system, existing tickets); do not redo the alert investigation
- If evidence is ambiguous or conflicting, report your assessment but do NOT act
- Explain your reasoning BEFORE executing any tool with side effects
- No action is a normal, successful outcome; prefer inaction over incorrect action
- Write a short memo: decision, evidence, tools used, outcome, links to anything you created
```

**Decision:** Option B — the action layer's remediation framing and "don't re-investigate" rule are a poor fit, and B adds only an enum value, two switch cases, a validator rule and a prompt constant on top of what A needs anyway.

_Considered and rejected: Option A — reuse `action` type (remediation framing, discourages needed research), Option C — any type, no injected layer (every deployment reimplements safety guidance)_

---

## Q7: Dashboard presentation

### Option C: Timeline + "Post-investigation" section on session detail + session list indicator/filter
- Timeline: post-investigation stages after the Reflection (scoring) stage with their own separator.
- Session detail: "Post-investigation" section — per agent: name, status, acted / no-action chip (from `actions_executed`, set by the YES/NO marker, Q6), memo with clickable links.
- Session list: indicator when any post-investigation agent acted, and a filter (e.g. `post_investigation=acted|ran|none`).
- **Pro:** Outcomes visible at a glance; operators can find sessions that produced proposals; full trace still in timeline.
- **Con:** Most UI/API work of the options.
- **Cost (verified, small):** direct precedent — the session list already computes `has_action_stages` and `actions_executed` via per-session subqueries on `stages` (`pkg/services/session_service.go:1308-1315`); add the same for `stage_type = 'post_investigation'`, plus a query param in `pkg/api/handler_session.go` alongside existing filters (`scoring_status`, `label`, `review_status`, …).

**Decision:** Option C — cheap given the existing action-stage list precedent, and makes proposals discoverable inside TARSy.

_Considered and rejected: Option A — timeline only (outcome buried), Option B — no list indicator/filter (proposals not discoverable from the session list)_

---

## Q8: Triggers and re-runs

### Option A: Auto-scoring only
- **Pro:** Exactly-once per session in the normal path; no duplicate side effects from re-scoring (`POST /api/v1/sessions/:id/score` does not trigger post-investigation agents).
- **Con:** No way to retry a failed agent or run it on old sessions.

**Decision:** Option A — keep the executor entry point trigger-agnostic (`RunAsync(sessionID, scoreID)`-style) so a manual endpoint or a human-review trigger can be added later as a thin layer; neither is built in v1.

_Considered and rejected: Option B — every completed scoring incl. API re-score (re-scores silently repeat side effects), Option C — auto + explicit retry/backfill endpoint (extra endpoint/auth/button not needed for v1)_

