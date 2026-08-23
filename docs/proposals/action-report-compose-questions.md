# Action Report Compose Pass — Questions

**Status:** All decisions made
**Related:** [Design document](action-report-compose-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where does the compose pass live?

This is the main structural choice. It decides schema, executor flow, fail-open behavior, and how visible the step is in the dashboard.

### Option B: Automatic sibling stage after each action stage (like synthesis)
- **Pro:** Same pattern as synthesis and exec summary: work stage, then a single-shot document stage.
- **Pro:** Best timeline/stage-list visibility (named collapsible stage, e.g. `{actionStage} - Amended Report`).
- **Pro:** Fail-open is clean — action stage stays completed; compose stage can fail without rewriting action status.
- **Pro:** Independent provider, `referenced_stage_id` to the action stage, own LLM interaction type.
- **Con:** More surface area: `stage_type` enum, agent type, factory, progress count, dashboard chrome.
- **Con:** Slightly less "inside the remediator" — it is attached to action, not a YAML chain stage.

**Decision:** Option B — compose is a first-class document (session `final_analysis`), not a helper nested on the remediator. Same automatic-stage pattern as synthesis. Lightweight via no YAML and collapsed-by-default chrome, not by hiding the step.

_Considered and rejected: Option A (in-stage extra call — tool-summarization analogy does not hold; compose combines two stages and becomes the session report), Option C (chain YAML — product invariant, too easy to omit)._

---

## Q2: How strict is the compose prompt?

4.6 succeeded because it copied the synthesis and patched a few sections. 4.5/5 rewrote or stubbed. Mechanical concat is ugly because it does not patch stale "recommended actions." Compose has both documents in one prompt and must not re-author the investigation.

### Option A: Strict copy-edit (format-agnostic)
Instructions: emit the upstream report with its own wording and structure preserved (whatever format that agent or skill used); fold in the action result; patch only text the memo makes stale. Do not impose a TARSy report template, improve prose, drop sections, or invent facts.
- **Pro:** Closest to the 4.6 document operators liked, without assuming one investigation layout.
- **Pro:** Constrains even a "helpful" model; cheaper models are less dangerous.
- **Con:** A model that still ignores instructions (the original bug) could ignore this too — but the task is narrower and has both documents in one prompt.

**Decision:** Option A — the compose stage copy-edits the upstream document as it is; it does not rewrite it into a canonical template. The only upstream text it may change is whatever the action memo makes stale.

_Considered and rejected: Option B (light rewrite — that is how 5 already replaces the investigation)._

---

## Q3: Which model runs the compose stage?

Sibling stage means an independent provider — not “whatever the action agent used.” Token count is still one shot (two docs in, one doc out). Dollar cost and instruction-following differ.

**Constraint:** Synthesis is not always in the chain. TARSy inserts a synthesis stage only after parallel/replica investigation. A single-agent investigation has no synthesis agent and no synthesis provider block. The upstream report is synthesis output if that stage ran, otherwise the investigation agent’s final analysis.

### Option C: Dedicated builtin compose agent with optional chain override, defaulting to a mid-tier model (e.g. Sonnet / Flash)
- **Pro:** Cost control; copy-edit does not need tools or Opus.
- **Pro:** Same default on every chain, whether or not synthesis ran.
- **Pro:** Override if a cheap default starts dropping sections (same shape as `executive_summary_provider`).
- **Con:** One more config knob.

**Decision:** Option C — builtin compose agent, mid-tier default, optional chain override. Same default whether or not synthesis ran.

Mid-tier default is `defaults.compose_provider` (set in tarsy.yaml to a Flash/Sonnet provider ID that exists in that deployment). Chain `compose_provider` overrides it. Neither field inherits `chain.llm_provider` — that is often Opus and would undo the cost control. If both compose knobs are unset, fall through to `chain.llm_provider` → `defaults.llm_provider` so compose still runs.

_Considered and rejected: Option A (provider of whoever wrote the upstream report — chain-shape dependent; frontier-priced copy-edit), Option B (exec summary provider — that prompt is trained to compress)._

---

## Q4: What happens when the compose LLM fails?

Action tools may already have mutated the cluster. The session must still complete (**fail-open**, like exec summary — not fail-closed like synthesis).

### Option C: Compose stage **failed**; session `final_analysis` is mechanical concat (upstream report + fixed heading + raw memo)
- **Pro:** Timeline is honest — operators see that the compose LLM failed.
- **Pro:** The Final Analysis card is still complete (findings + actions). Ugly is acceptable on a rare error path.
- **Con:** Red compose stage next to a usable card. Acceptable if the card is clearly fallback concat, not a silent walk-back to the memo.

**Decision:** Option C — stage status `failed`; executor sets session `final_analysis` (and the failed stage’s analysis) to mechanical concat. Do **not** let `extractFinalAnalysis` skip the failed compose stage and land on the action memo.

_Considered and rejected: Option A (walk back to action memo — recreates today’s bug), Option B (completed stage + warning — hides LLM failure as a successful stage)._

---

## Q5: When do we skip creating a compose stage?

Compose runs after **each** successful action stage. Remaining policy is skip vs always insert.

### Option A: Skip only when there is no upstream investigation/synthesis (or prior compose) report
- **Pro:** Avoids a no-op stage on action-only chains.
- **Pro:** Still composes when the remediator took **no action** — "no action" is an amendment operators should see on the card, not a reason to leave the memo as the session report.
- **Con:** None significant.

**Decision:** Option A — skip only when there is no upstream report. Still compose when no tools ran.

_Considered and rejected: Option B (skip when no tools ran — recreates today’s bug on the common “NO ACTION” path), Option C (always insert — pointless copy-edit on action-only chains)._

---

## Q6: What is the compose stage called (schema + UI)?

`stage_type` / agent type in API and DB, plus the label in the stage list. Synthesis uses `stage_type: synthesis` and `{parent} - Synthesis`. Type string and display name do not have to match.

### Option C: schema `compose` / UI `{actionStage} - Amended Report`
- **Pro:** `compose` sits with `synthesis` / `exec_summary` as a document-producing stage; matches how we name the pass.
- **Pro:** List label is unambiguous (session-facing document), not a vague “Report” or legal “Amendment.”
- **Con:** Longer label than `{stage} - Synthesis`.

**Decision:** Schema `compose`; UI `{actionStage} - Amended Report`. Collapsed by default like synthesis / exec summary / action. `referenced_stage_id` → the action stage (trigger). Upstream report is prompt context, not a second FK.

_Considered and rejected: Option A (schema `amendment` — weaker type name, easy to confuse with action edits), Option B (UI `Report` — vague next to Final Analysis / Exec Summary)._

---

## Q7: How much do we strip from the action prompt?

Today (`action.go` / `templates.go`): "Your final report becomes finalAnalysis… Preserve the investigation report… produce an amended report that preserves the investigation findings." That contract moves to the compose stage.

### Option A: Remove all preserve/copy language; require a short structured memo only
- **Pro:** Stops wasting action-model output tokens on a reprint compose will not trust.
- **Pro:** Clear job split; 4.5/5 already write memos — we align the prompt with that.
- **Con:** 4.6-style action agents that currently emit a full report will stop (compose takes over — intended).

**Decision:** Option A — action agent emits a short memo only. Preserve/copy contract moves entirely to compose.

_Considered and rejected: Option B (keep “you may copy the report” — models oscillate; compose input becomes inconsistent)._

---

## Q8: What is fed into the compose stage?

### Option A: Two blobs — upstream report + action memo
Upstream report = last investigation/synthesis, or the **previous compose result** if this is a later action stage in the same chain.
- **Pro:** Smallest, cheapest, clearest prompt.
- **Pro:** Avoids dumping tool traces (expensive, noisy).
- **Con:** If the memo omits a tool outcome, compose cannot recover it from traces.

**Decision:** Option A — two `final_analysis` documents only. Compose does not re-evaluate the investigation or read tool traces.

_Considered and rejected: Option B (add compact tool-call summary — second interpretation of tools; add later only if memos are too thin), Option C (full action conversation — not a cheap pass)._

---

## Q9: Do scoring, memory, and chat see the compose stage?

Scoring today does **not** receive `alert_sessions.final_analysis` as its own blob. It gets alert + runbook + tools + a timeline of `investigation` / `exec_summary` / `action`. Stage `final_analysis` events appear only for those types. After compose, the card text is no longer the same as the last investigation/action event.

### Option A: Session field + chat + memory use compose output. Scoring timeline skips compose *internals*, but still includes compose **output** (the stage’s `final_analysis` / session card).
- **Pro:** Memory, Slack, exec summary, and chat see what operators read.
- **Pro:** Scoring still judges investigation + action *work*; it can also see if the delivered card is garbage relative to those inputs.
- **Pro:** Output is one extra document (~2–6k tokens), not the compose LLM trace.
- **Con:** Scoring is not asked to grade copy-edit craft as a process (we do not need it to).

**Decision:** Option A (refined) — exclude compose process from the scoring timeline; still feed compose **output** into the judge. Chat includes compose as the latest report. Memory uses session `final_analysis` (compose).

_Considered and rejected: Option B (full compose stage in scoring — grades a copy-edit pass as investigation quality), Option C (memory uses action memo — drops the investigation body)._
