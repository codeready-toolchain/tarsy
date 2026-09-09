# Session Labels

**Status:** Draft — questions resolved in [session-labels-questions.md](session-labels-questions.md)

## Overview

Downstream systems post TARSy’s executive summary to Slack and need a **machine-readable** signal they can match without NLP. Today a completed session only exposes prose (`final_analysis`, `executive_summary`).

This design adds session-level **`labels`**: a closed list of tags chosen from an ordered map. The executive-summary LLM emits a last-line `LABELS:` trailer (same idea as action `YES`/`NO`). Go parses it and stores **canonical** names. Zero YAML uses a builtin map whose *policy* is exclusive (`watch`, `action`, or `noise`, or none). Deployments may select a named map with their own vocabulary (`monitor`, `page`, `false_positive`, …) and may opt into multiple labels on one session.

TARSy does **not** translate custom labels into the builtin three, emit Slack usergroups, or decide paging policy. Clients match on stored strings (for example `page` in `labels`, or `noise` to close with no follow-up).

## Design Principles

1. **Mechanism vs policy.** The schema is generic tags (`labels` + `label_maps`). Builtin policy is exclusive attention. Cardinality is a map field TARSy owns (`multi`), not a prose instruction the parser cannot enforce.
2. **Encode, do not re-judge.** The investigation (and compose, when present) already classified the incident. Exec summary copies that into zero or more labels from the active list.
3. **Builtin is a named catalog entry.** Reserved name `builtin`. Zero YAML injects the Go map under that key. `label_maps.builtin` in tarsy.yaml **fully replaces** it (no merge). Empty `label_map` and `label_map: builtin` are the same selector. Layer 4 (format) stays a TARSy template keyed by `multi`.
4. **Preserve label order.** Map entries are a Go **slice** (YAML sequence), never `map[string]string`.
5. **Fail-open.** A `LABELS:` line that still does not parse after one reminder does not fail the session. `labels` stays null; the **first** summary is stored **as-is** (no strip). No `LABELS:` line after a completed summary is `[]` (nothing applies). A valid empty `LABELS:` is also `[]`, but the prompt does not ask for it.
6. **Reuse existing contracts.** Last-line parse + strip-on-success, session column, `GET /sessions/:id/status` as the poll surface Guardian Cockpit already uses. No dedicated labeling agent in v1.

## Architecture / How It Works

```
Alert → chain (investigation / synthesis / action / compose)
  → final_analysis
  → ExecSummaryAgent (typed exec_summary stage, fail-open)
       1–4 line facts-only summary
       + LABELS layers 1–4 (active map, format keyed by multi)
  → LLM text:
       <1–4 line summary>
       LABELS: page             # omitted entirely when none apply
  → Same execution, in the exec-summary controller (not the worker):
        last non-empty line starts with LABELS: (lenient)?
        yes + valid → strip, canonical []string (empty remainder → [])
        yes + invalid → one reminder Generate on the same conversation;
                        if still invalid or reminder LLM errors →
                        first summary unchanged, labels null
        no  → labels []  (do not retry)
  → queue.ExecutionResult carries cleaned executive_summary + labels
  → Worker writes session.executive_summary + session.labels
       (empty labels slice is a real write; nil pointer leaves NULL)
  → GET /api/v1/sessions/:id/status
       { id, status, executive_summary, labels }
  → Client: notify if labels contains a name it cares about (e.g. page)
```

Exec summary already runs **inline before** the session is marked complete (fail-open). Guardian Cockpit already waits for a terminal status then reads `/status`. Scoring is async *after* complete and must not carry this signal.

```mermaid
sequenceDiagram
  participant Inv as Investigation/compose
  participant ES as ExecSummaryAgent
  participant LLM as LLM service
  participant W as Worker
  participant API as GET /sessions/:id/status
  participant GC as Guardian Cockpit

  Inv->>ES: final_analysis
  ES->>LLM: summary prompt + LABELS layers (active map)
  LLM-->>ES: summary + LABELS: page
  Note over ES: parse; optional one reminder on same conversation
  ES->>W: cleaned summary + canonical labels
  W->>W: session complete (write executive_summary + labels)
  GC->>API: poll until terminal
  API-->>GC: executive_summary, labels
  Note over GC: deterministic: "page" in labels → notify on-call
```

## Core Concepts

### Active map

Resolve **last non-empty**: `defaults.label_map` → `chain.label_map`. Empty / omitted inherits the previous layer. Both empty → **`builtin`**. Named value must exist in the catalog **after** load-time inject.

This selector is **session-level**, next to `fallback_list` on `defaults` / `agent_chains`. It is **not** a field on the `executive_summary` job-pairing block (`llm_provider` / `llm_backend` / `fallback_list`). Labels classify the session; the job block only chooses which model writes the summary.

`label_map: builtin` is a real selector. A chain can opt back to the (effective) builtin when `defaults.label_map` is a custom map. That was impossible when builtin was only the empty-selector fallback.

Go shape (order-preserving):

```go
const LabelMapBuiltin = "builtin"

type LabelMap struct {
	Multi        bool        // omit/false → at most one label; true → zero or more
	Instructions string      // optional; empty → generic layer 2 (not the Go essay)
	Labels       []LabelSpec // ordered; at least one
}

type LabelSpec struct {
	Label       string
	Description string
}
```

Do **not** store the catalog as `map[string]string`. Catalog keys (`label_maps.<name>`) may be a Go `map`; only the **label list** inside a map must stay a slice.

After YAML load: if `label_maps` has no `builtin` key, insert the Go map (essay in `Instructions`, `watch` / `action` / `noise`, `multi: false`). If YAML defines `label_maps.builtin`, keep that entry as-is (**full replace**, no merge of labels or instructions). Log a startup warning when YAML overrides `builtin`.

`multi` is enforced in **layer 4 and the parser**. Layer 2 (instructions) must not be the only place cardinality is stated.

The resolved map is an input to the exec-summary prompt builder (`PromptBuilder.BuildExecutiveSummaryUserPrompt` gains the active `LabelMap`). It is not an `ExecSummaryAgent` YAML field. `executeExecSummaryStage` already has `e.cfg` and `input.chain` (same as `ResolveExecSummaryConfig`).

### Builtin map

The Go default lives under catalog key **`builtin`**. SRE-generic. **`multi: false`.** This is **what to do with the alert**, not `actions_executed` and not `needs_review`.

Used when the selector is empty or `builtin`, unless YAML replaced `label_maps.builtin`.

There is no `none` / `ignore` label. **`[]` is not a close verdict.** Omit the `LABELS:` line when none of the three apply (unclassified, or remaining work that is not `watch` / `action` / `noise` — for example “file a backlog ticket” as the actual next step). Operators must still read the summary before closing when labels are empty or null.

The Go builtin’s **`Instructions` + `Labels`** must use these definitions, including negatives. Short slogans (“a human must do something”) are too wide: false-positive reports routinely recommend closing the alert and optionally tuning detection. A YAML override of `builtin` does **not** keep this essay unless it copies the text.

| Label | Use when | Do **not** use when |
|---|---|---|
| `watch` | The subject still looks off or evidence is thin. No intervention *now*; look again if it persists. | The analysis already concluded the alert can be closed with no follow-up (`noise`). Detector-tuning *as the reason to keep the alert open* is not `watch`. |
| `action` | A human must intervene **on the affected system** (fix the failing workload, approve an emergency change, execute a runbook step that was not automated). | Closing or acking the alert; filing a backlog ticket; tuning detection/rules; “no remediation because it was benign.” Not the same as `actions_executed`. Closable-with-no-work is `noise`, not `action`. |
| `noise` | This alert can be **closed with no remaining human work**. False positive, expected/maintenance, already recovered and done, duplicate of known-benign. Optional “tune later” in passing does not block `noise`. | A human should still look again (`watch`), intervene (`action`), or do a required follow-up (file a ticket, change a rule) before the alert is done. Unsure → `watch`, never `noise`. Omitting `LABELS:` is not `noise`. |

If the report disagrees with itself, prefer `watch` over `action`. Prefer `watch` over `noise` when unsure. Prefer `noise` over omit when the classification is clearly closable with no follow-up. Apply at most one of these three.

Other catalog entries **do not** have to use these names (a custom map can use `false_positive` instead of `noise`). To **extend** watch/action/noise with another tag, copy them into a new map name — there is no merge with `builtin`.

### Catalog YAML

Top-level `label_maps`, same idea as `fallback_lists` (loaded onto `config.Config` next to `FallbackLists`). Labels are a **sequence**. `multi` defaults to `false`.

```yaml
label_maps:
  oncall:
    # multi omitted → false (at most one label, or none)
    instructions: |   # optional — empty → generic matcher, not the Go builtin essay
      Prefer Classification and Recommended Action from the analysis.
    labels:
      - label: monitor
        description: |
          MONITOR. No intervention now; look again if it persists.
      - label: page
        description: |
          PAGE. A human must intervene on the affected system now.
      - label: false_positive
        description: |
          Close with no action. Detector was wrong or the event is expected.

  # Optional full replace of the Go default (no merge with watch/action/noise):
  # builtin:
  #   multi: false
  #   instructions: |
  #     ...
  #   labels:
  #     - label: watch
  #       description: ...

  ops-tags:
    multi: true
    labels:
      - label: watch
        description: Subject still looks off; look again if it persists.
      - label: page
        description: Page the on-call; human intervention needed now.
      - label: modify_detection_rules
        description: Detection should be tuned; does not imply paging.

defaults:
  label_map: oncall   # optional; omit or `builtin` → catalog["builtin"]

agent_chains:
  kubernetes-investigation:
    label_map: oncall            # optional; last non-empty wins
  argocd-investigation:
    label_map: builtin           # opt back to builtin when defaults is custom
```

Load-time validation:

- Catalog key and `label` token: ASCII `[A-Za-z][A-Za-z0-9_-]*` (must start with a letter; no comma — that is the trailer delimiter). Reserved catalog key: `builtin`.
- At least one label; `label` and `description` non-empty.
- Duplicate `label` values in one map fail (compare case-insensitively).
- After inject, every `label_map` on defaults/chain must name a catalog entry (`builtin` always exists).

Config viewer shows the catalog **including `builtin`** (injected or YAML) and the raw `label_map` selectors (no per-session expansion), same as named fallback lists. Empty selector is equivalent to `builtin`.

`ExecSummaryAgent.custom_instructions` is **not** the labeling hook. Builtin `ExecSummaryAgent` has no custom instructions today. A later general exec-summary overlay is out of scope for this feature.

### Last-line contract

```text
LABELS: <label>
LABELS: <label>, <label>, ...
```

When none apply, **omit the line** (prompted path). Parse the **last non-empty line** (same as action `YES`/`NO`: trim trailing whitespace, then that last line). A `LABELS:` line that is not last does not count — no reminder (Q5/Q6).

Lenient match (must succeed without a retry):

1. Trim wrapping `*`, `_`, and backticks on that **line** (not per token). `LABELS: **page**` is leftover junk → parse failure → reminder.
2. Prefix is `LABELS` then optional whitespace then `:` then optional whitespace (case-insensitive). `LABELS:page` and `LABELS:  page` are both valid. `LABEL:` without S is not.
3. Remainder: split on comma; trim each token; drop empty tokens (trailing comma).
4. Each token: trim a single trailing `.` or `;` if that makes it match. Case-insensitive match to the active list. **Store the canonical `Label` from the slice**, unique, in **map order**.
5. Reject leftover junk in a token (`page please`) and names not in the list.

Cardinality (apply **after** uniquing into map order):

- `multi: false` — at most one unique name. Two or more distinct names → parse failure. `LABELS: watch, watch` is one name. Empty remainder (`LABELS:` with no names) is accepted as `[]` but is not prompted.
- `multi: true` — any unique subset of the list. Unknown token → parse failure. Empty remainder is accepted as `[]` but is not prompted.

`ExtractLabels(text, map LabelMap)` lives next to `ExtractActionsTaken`. Three outcomes:

| Last non-empty line | Result | Strip? | Retry? |
|---|---|---|---|
| No `LABELS:` prefix | `[]`, success | no | no |
| Valid names / valid empty remainder | canonical `[]string`, success | yes | no |
| Prefix matches but invalid | parse failure | no | one reminder |

Strip the line from session `executive_summary` **and** that execution’s `final_analysis` / `llm_response` timeline events **only** when a `LABELS:` line parses successfully. Same helper pattern as `stripActionMarkerFromTimeline`: try extract on each `final_analysis` / `llm_response` event for that execution; skip events that do not parse. Raw text stays on `llm_interactions`.

New sessions do **not** create a session-level `event_type=executive_summary` timeline event (current behavior: `SingleShotController` records `final_analysis` + streaming `llm_response`). Do not start writing that legacy event. Chat and scoring must read `alert_sessions.executive_summary` (PR 0), not that event type.

On reminder success, the stored summary and the `final_analysis` event come from the **retry** output (stripped). The first-turn `llm_response` may still show the bad trailer; that is acceptable (same honesty as scoring retries). Strip still runs on events whose last line is a valid `LABELS:` trailer.

No `LABELS:` line after a completed summary → `labels: []`, nothing to strip. **Do not retry.**

A `LABELS:` line that still fails after lenient parse → **one reminder** Generate on the **same conversation** (append assistant + user reminder, like scoring extraction): allowed names + `multi` rule; keep the 1–4 line summary; omit the line if none apply. If the reminder parses, use that output (strip retry). If it fails or the reminder LLM call **errors** (not cancel/timeout): `labels` null, **first** summary unstripped. Do not fail the exec-summary stage. Cancel and timeout still follow today’s exec-summary stage mapping.

Log parse-after-retry at warn. Increment a prometheus counter (`tarsy_session_labels_parse_failures_total`) in the same PR — `pkg/metrics` already uses `promauto` counters.

Empty first response is already retried by `SingleShotController` (`maxEmptyResponseRetries`). That is not the labels reminder. Labels reminder runs only after a non-empty completed generation whose last line is a bad `LABELS:` trailer.

### Prompt layers

Every resolved `LabelMap` is the same shape. Layer 2 is **`Instructions` if non-empty, else** the generic “apply the label(s) whose descriptions best match; if none match, omit the LABELS line.” The Go-injected `builtin` ships with the attention essay already in `Instructions`. A YAML `label_maps.builtin` that omits `instructions` gets the generic matcher, not the Go essay.

| # | Source |
|---|---|
| 1 | TARSy, fixed. Encode using **only** labels from the map below. Do not invent labels or a new verdict. The 1–4 line body stays facts-only. |
| 2 | Active map `instructions`, or generic matcher if empty |
| 3 | Active map `labels` **in slice order** |
| 4 | TARSy template, **keyed by `multi`**. Not free-form. |

**Layer 4 when `multi` is false:**

```text
If one label applies, last line exactly:
LABELS: <one exact label from the list>
Copy the label as written. Nothing after that line.
If none apply, do not add a LABELS line.
```

**Layer 4 when `multi` is true:**

```text
If any labels apply, last line exactly:
LABELS: <comma-separated unique labels from the list>
Only labels from the list. Nothing after that line.
If none apply, do not add a LABELS line.
```

Implementers must keep Go-builtin layer 2/3 wording (including negatives) in the injected `builtin` map, not a shorter paraphrase. Cardinality in layer 2 may *reinforce* `multi` but does not replace it.

**Placement:** System prompt is unchanged (role + 1–4 lines). User prompt order:

1. Current CRITICAL RULES + analysis blob
2. Existing write cue: `Executive Summary (1-4 lines, facts only):`
3. Layers 1–4. Layer 4 is the last text in the user message.

The write cue stays so the 1–4 line body is still requested; format last so generation starts after the trailer contract (Q8).

Reminder user text is a separate builder (same idea as `BuildScoringOutputSchemaReminderPrompt`), not a fifth always-on layer.

### Persistence and API

- `labels` on `alert_sessions`: **optional JSON `[]string`** (same Ent pattern as `session_scores.failure_tags`: `field.JSON("labels", []string{}).Optional()`). Not an enum.
- **NULL** while in progress, if exec summary was skipped (`final_analysis` empty → stage not run), if the exec-summary stage failed (existing `executive_summary_error` path), or if a `LABELS:` line failed to parse after retry.
- **`[]`** when the summary completed with no `LABELS:` line, or with a valid empty trailer.
- Written in the worker’s existing terminal update (`updateSessionTerminalStatus`) next to `executive_summary`. Today empty strings are skipped (`if result.ExecutiveSummary != ""`). Labels need a three-state on **`queue.ExecutionResult`** (not `agent.ExecutionResult`): `Labels *[]string` — nil = leave NULL, non-nil including `&[]string{}` = `SetLabels`.

JSON on the wire: always emit the `labels` key. Do **not** use `omitempty` (nil and empty would collapse). `[]string` without a pointer already marshals nil as `null` and empty as `[]`.

```json
{ "id": "…", "status": "completed", "executive_summary": "...", "labels": ["page"] }
{ "id": "…", "status": "completed", "executive_summary": "...", "labels": [] }
{ "id": "…", "status": "in_progress", "executive_summary": null, "labels": null }
```

**Minimum API:** `GET /api/v1/sessions/:id/status` — `SessionStatusResponse.labels`.

**Detail:** `SessionDetailResponse.labels` (same JSON rules). Mapped in `GetSessionDetail` like `executive_summary`.

**List:** `DashboardSessionItem.labels`. `dashboardRow` must scan the jsonb column (entity fields are selected today; add an explicit `[]string`/`json` field on the scan struct). `GET /api/v1/sessions?label=<token>` filters to sessions whose `labels` JSON **contains** that name (`labels @> '["page"]'::jsonb`). Exact match to **stored canonical** names (case-sensitive). Token charset matches config labels; invalid token → 400 like `scoring_status`. `NULL` and `[]` do not match a contains filter.

**Filter options:** `GET /api/v1/sessions/filter-options` includes distinct stored labels (`jsonb_array_elements_text` on non-deleted sessions where `labels` is a non-empty array). FilterPanel: one select; `hasActiveFilters` / localStorage persistence / API query-string wiring (same path as `scoring_status`).

**Index:** `CREATE INDEX IF NOT EXISTS idx_alert_sessions_labels_gin ON alert_sessions USING gin (labels jsonb_path_ops)` in `CreateGINIndexes` (containment `@>` only). Broaden that function’s comment (today it only mentions FTS). Comment on the Ent field and the existing `AlertSession.Annotations()` GIN note. Add the name to the [db-migration-review](../../.claude/skills/db-migration-review/SKILL.md) known-index table (that table today lists only partial unique indexes; extend it so Atlas `DROP INDEX` is stripped). Include the existing FTS GIN names in the same table edit so they are not dropped either.

Clients that want a flag derive it from names **they** define (`action` / `noise` on builtin, `page` / `false_positive` on a custom map). TARSy does not ship `needs_attention` or a parallel `attention` string. Do **not** treat `[]` as closable.

Do **not** put this on `session_metadata` JSON. Do not add `labels` to `SessionStatusPayload` WebSocket. Dashboard already refetches the list on `session.status` (`DashboardView`); detail refetches the session DTO the same way.

`GET /api/v1/sessions/:id/summary` is the usage/cost payload (`SessionSummaryResponse`). It does not grow a `labels` field.

## What this is not

- Not a second classification LLM.
- Not a dedicated labeling / tagging agent (v1). Extra Generate on the GC wait path; scoring-style async would miss `/status`. Revisit only if trailer parse is unreliable (extra extract shot) or a later feature needs re-label / labels without exec summary / mid-chain tags.
- Not Slack-specific. TARSy never emits Slack usergroup markup (`<!subteam^…>`).
- Not a replacement for `review_status`.
- Not Guardian Cockpit work. GC maps stored labels (e.g. `page`) to whatever notification it already uses.
- Not Go-side translation of custom labels into `watch` / `action` / `noise`.
- Not free-form tags. Unknown names are parse failures, not stored.
- Not user-applied or mid-investigation tags in v1.
- Not a `label_map` knob on `defaults.executive_summary` / `chain.executive_summary`.
- Not injecting the active `label_maps` catalog into chat or scoring (Q10). Scoring judges labels against the investigation prose only.

## Downstream consumers (chat vs scoring)

Source of truth for the **posted** 1–4 line summary is `alert_sessions.executive_summary`, not `event_type=executive_summary`. That timeline type is leftover; new sessions never write it (`TestExecutor_ExecutiveSummaryNoLegacyTimelineEvent`).

| Consumer | Exec-summary **stage** (thinking / `llm_response` / `final_analysis`) | Persisted `executive_summary` | `labels` |
|---|---|---|---|
| Follow-up **chat** | Skip (already tested; [ADR-0008](../adr/0008-session-scoring.md) chat filter is investigation + synthesis + chat) | **Yes** — `## Executive Summary` footer so chat can match what Slack/GC posted | **No** — operators see chips; chat is Q&A on the investigation, not paging policy |
| **Scoring** | **Yes** — ADR-0008 already includes `exec_summary` in scoring context | **Yes** — same footer; persisted product vs stage process | **Yes** — stored names only (`[]` / `null` included). No catalog, no `multi`, no descriptions |

Scoring today already sees the exec-summary **stage**. The bug is only the extra `## Executive Summary` footer, which `getExecutiveSummary()` leaves empty because it still queries the dead event. Chat has both: it skips the stage (intentional) *and* misses the footer (unintentional).

Scoring without the map can still catch “investigation said benign/FP, labels are `action`” or “closable FP, labels empty instead of `noise`.” It cannot score “was `monitor` the right name on this catalog.” That is enough for v1; do **not** add a sixth score dimension (keeps 0–100 calibration). Add a short judge-prompt sentence: the persisted summary must stay facts-only and labels must be consistent with the investigation conclusion.

## Implementation Plan

Each step leaves TARSy working. `labels` is null until persist lands. Unset `label_map` uses the builtin map (`watch` / `action` / `noise`, `multi: false`).

Do **not** attach LABELS layers to the live exec-summary prompt until parse + strip + persist exist. Shipping prompt-only would store `LABELS:` in `executive_summary` and Guardian Cockpit would post it to Slack.

### PR 0 — Read stored executive summary (bugfix, no labels) - DONE

Independent of labels. Shared helper: the `## Executive Summary` footer comes from `session.ExecutiveSummary` (empty string if null). Delete/stop using the two `getExecutiveSummary` lookups on `EventTypeExecutiveSummary` (`pkg/queue/chat_executor.go`, `pkg/queue/investigation_context.go`). Do **not** resume writing that event.

- **Chat:** keep skipping `StageTypeExecSummary` (existing `chat_executor` test: stage `final_analysis` must not appear). Pass the session column into `FormatStructuredInvestigation`.
- **Scoring / reflector:** keep including the exec-summary stage. Same column → footer. Mild duplication with the stage’s stripped `final_analysis` is acceptable (process vs posted product).
- Update scoring integration tests that seed a legacy `executive_summary` timeline event to set the session column instead.
- Keep `TestExecutor_ExecutiveSummaryNoLegacyTimelineEvent`.

**Not in this PR:** labels, judge-prompt text, catalog.

### PR 1 — Catalog, resolve, prompt helpers (no live prompt, no DB) - DONE

**What lands:**

- Config types: `label_maps` on `TarsyYAMLConfig` / `config.Config` (clone like `FallbackLists`), `defaults.label_map`, `chain.label_map`.
- Builtin `LabelMap` injected as catalog key `builtin` when YAML omits it (`multi: false`, essay + `watch` / `action` / `noise`). YAML `label_maps.builtin` is a full replace (warn at load).
- Resolve helper: last non-empty selector → catalog lookup; empty ≡ `builtin`. Chain `label_map: builtin` wins over `defaults.label_map: oncall`.
- Prompt **helpers** that take the active `LabelMap` and render layers 1–4; layer 4 from the `multi` flag. Goldens: builtin; one custom exclusive map; one `multi: true` map. Custom goldens must not contain builtin attention-essay names unless the map listed them. Existing live `BuildExecutiveSummaryUserPrompt(finalAnalysis string)` and `NewExecSummaryController` stay unchanged.
- Validation tests (dup labels, empty, unknown selector, bad tokens, order preserved, `multi` default, inherit defaults → chain, inject `builtin`, YAML override of `builtin`, chain `label_map: builtin` vs defaults custom).

**Not in this PR:** Session column, `/status` field, live controller wiring.

### PR 2 — Parse, persist, status/detail API, live prompt

**What lands:**

- Wire layers into `BuildExecutiveSummaryUserPrompt` + `NewExecSummaryController` (signature takes active map). Update the existing `executive_summary` prompt golden.
- Parse + one reminder **inside the exec-summary controller path** (same `agent_executions` row, conversation append, like scoring). Synthesis/compose SingleShot paths stay unchanged.
- Ent: optional JSON `labels` `[]string`; Atlas migration (`make migrate-create NAME=add_session_labels`, then [db-migration-review](../../.claude/skills/db-migration-review/SKILL.md)).
- GIN `idx_alert_sessions_labels_gin` (`jsonb_path_ops`) in `CreateGINIndexes`. Comment + skill known-index list (see Persistence).
- `ExtractLabels` next to `ExtractActionsTaken`. Timeline strip helper next to `stripActionMarkerFromTimeline`.
- `queue.ExecutionResult.Labels *[]string`. Worker `SetLabels` when non-nil (including empty). Skipped/failed summary leaves NULL.
- `SessionStatusResponse` + `SessionDetailResponse` include `labels`.
- Fail-open after retry: warn + counter + first summary untouched + null labels. No `LABELS:` line → `[]`, no retry.
- Scoring context: append persisted labels under the executive-summary footer (`Labels: page` / `Labels: (none)` for `[]` / `Labels: (unknown)` for null). Chat does **not** get this line.
- Judge prompt: one short instruction that persisted executive summary and labels are session output to check for faithfulness vs the investigation. No sixth dimension. No label-map dump.

**Tests this PR:** Parser table (no trailer → `[]`; empty `LABELS:` → `[]` + strip; `LABELS:page`; wrapping markdown on the line; trailing comma; one label; `watch, watch` with `multi: false`; multi subset; map-order; case; unknown; `multi: false` with two names). Reminder: first line bad, second valid → stored labels + stripped retry summary; retry still bad → first summary, null labels; reminder LLM error → same. Worker: custom map stores `page` not `action`; empty slice written vs NULL. API status/detail JSON: `[]` vs `null`. One e2e with a scripted last line matching builtin; one with a custom map if e2e config is cheap. Scripted LLM is sequential and ignores extra prompt text; scripts that omit `LABELS:` stay one Generate (`[]`). An invalid trailer test must add a **second** sequential exec-summary entry for the reminder.

**Deferred to PR 3:** Dashboard chips + `label=` filter (Q9). GIN can land here unused until PR 3.

### PR 3 — Dashboard list chips and filter

- `DashboardSessionItem.labels`; chips on the historical list (`SessionListItem`) and session detail. Triage page does **not** get a label filter (Q9). If a shared row component shows chips, that is fine; no triage query param.
- `GET /api/v1/sessions?label=<token>` — contains (`@>`); validate token charset. Handler + `ListSessionsForDashboard` tests.
- FilterPanel select; `GET /api/v1/sessions/filter-options` distinct stored labels; `hasActiveFilters` / persistence / API query-string wiring (same path as `scoring_status`).
- Docs: architecture-overview, functional-areas, `deploy/config/README.md` (`label_maps` example).

Not in this PR: triage filter, `sort_by=label`, unlabeled (`[]` / null) filter, WebSocket payload field, chip-click-to-filter.

### Out of scope (follow-up, not TARSy PRs)

- Guardian Cockpit: `if labels contains "page"` (or a configured list) → notify on-call.
- A downstream deployment: its own `label_maps` entry + chain selector (`multi: false` until a second independent tag is actually consumed).
- Skills on exec summary.
- `ExecSummaryAgent.custom_instructions` as a general overlay (orthogonal).
- Storing `label_map` name on the session (audit when catalogs change) — not required for v1.
- Dedicated labeling agent; `max_labels` integer; sort by label; unlabeled filter.
- Putting labels or the label map into follow-up chat context.
- Putting the label map (or a sixth score dimension) into the scoring judge.

## Resolved questions

| # | Topic | Status |
|---|---|---|
| Q1 | Exec-summary trailer vs extra shot vs labeling agent | **A** — last line of exec summary; strip only on a valid `LABELS:` line |
| Q2 | Builtin map | **A** — `watch` / `action` / `noise`, `multi: false`; `[]` is not closable; no `none`/`ignore` |
| Q3 | Field name | **A** — JSON `labels` `[]string`; null vs `[]`; no `attention` / `needs_attention` |
| Q4 | Customization | **`label_maps` + `multi`**; reserved `builtin`; YAML `builtin` full replace; no merge; empty selector ≡ `builtin` |
| Q5 | Always-on trailer vs opt-in | **A** — always in the prompt (from PR 2); omit the line when none apply; no `LABELS:` line → `[]` |
| Q6 | Parse failure and retries | **B** — lenient parse, then one reminder on the same conversation; still fail-open; do not retry omit |
| Q7 | Strip from timeline | **A** — session column + exec-summary `final_analysis` / `llm_response` if present; raw on `llm_interactions` |
| Q8 | System vs user prompt for layers 1–4 | **B** — user prompt after analysis + write cue; layer 4 last; system unchanged |
| Q9 | v1 API / dashboard / WebSocket | **C** — status + detail + list chips + `label=` contains + GIN; no WebSocket field |
| Q10 | Chat vs scoring context | Chat: persisted summary, no labels, skip exec-summary stage. Scoring: stage + persisted summary + stored labels; no map |
