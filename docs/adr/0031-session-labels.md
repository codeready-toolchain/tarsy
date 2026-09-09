# ADR-0031: Session Labels

**Status:** Implemented  
**Date:** 2026-09-09  
**Relates to:** [ADR-0004](0004-stage-types.md) (`exec_summary` stage), [ADR-0008](0008-session-scoring.md), [ADR-0019](0019-config-viewer.md), [ADR-0030](0030-named-fallback-lists.md)

## Overview

Downstream systems post TARSy’s executive summary to Slack and need a **machine-readable** signal they can match without NLP. A completed session previously exposed only prose (`final_analysis`, `executive_summary`).

This decision adds session-level **`labels`**: a closed list of tags chosen from an ordered map. The executive-summary LLM emits a last-line `LABELS:` trailer (same idea as action `YES`/`NO`). TARSy parses it and stores **canonical** names. Zero YAML uses a builtin map whose policy is exclusive (`watch`, `action`, or `noise`, or none). Deployments may select a named map with their own vocabulary and may opt into multiple labels on one session.

TARSy does **not** translate custom labels into the builtin three, emit Slack usergroups, or decide paging policy. Clients match on stored strings (for example `page` in `labels`, or `noise` to close with no follow-up).

## Design Principles

1. **Mechanism vs policy.** The schema is generic tags (`labels` + `label_maps`). Builtin policy is exclusive attention. Cardinality is a map field TARSy owns (`multi`), not a prose instruction the parser cannot enforce.
2. **Encode, do not re-judge.** The investigation (and compose, when present) already classified the incident. Exec summary copies that into zero or more labels from the active list.
3. **Builtin is a named catalog entry.** Reserved name `builtin`. Zero YAML injects the Go map under that key. YAML `label_maps.builtin` **fully replaces** it (no merge). Empty / omitted `label_map` inherits the preceding non-empty selector; all empty → `builtin`. `label_map: builtin` is an explicit selector, not the same as empty. Format (layer 4) stays a TARSy template keyed by `multi`.
4. **Preserve label order.** Map entries are a sequence (YAML list), never an unordered string map.
5. **Fail-open.** A `LABELS:` line that still does not parse after one reminder does not fail the session. `labels` stays null; the **first** summary is stored as-is (no strip). No `LABELS:` line after a completed summary is `[]`. A valid empty `LABELS:` is also `[]`, but the prompt does not ask for it.
6. **Reuse existing contracts.** Last-line parse + strip-on-success, session column, `GET /sessions/:id/status` as the poll surface. No dedicated labeling agent in v1.

## Decisions

| # | Topic | Decision | Rationale |
|---|-------|----------|-----------|
| Q1 | Where the LLM emits labels | Last line of the executive summary (same Generate). Strip only when the line parses against the active map. | Zero extra latency on the `/status` wait path. Matches action `YES`/`NO`. An extra extract shot or a dedicated labeling agent remains a follow-up if trailer parse is unreliable. |
| Q2 | Builtin map | `watch` / `action` / `noise`, `multi: false`. No `none` / `ignore`. `[]` is not closable. | Operational English without deployment-specific paging words. `noise` is the closable verdict; omit means “read the summary.” Custom maps may use other names (`false_positive`). |
| Q3 | Field shape | JSON `labels` `[]string` only. Always emit the key (no `omitempty`). Null vs `[]` is meaningful. | Membership checks (`"page" in labels`) scale to `multi: true`. A derived `needs_attention` boolean would encode paging policy TARSy must not own. |
| Q4 | Customization | Top-level `label_maps` catalog + `multi`. Reserved `builtin`. Last-non-empty `defaults.label_map` → `chain.label_map`. Not a field on the `executive_summary` job block. | Same catalog-and-selector shape as [ADR-0030](0030-named-fallback-lists.md). Labels classify the session; the job block only chooses which model writes the summary. YAML `builtin` is a full replace so there is no silent merge. |
| Q5 | Trailer always on? | Layers 1–4 always sit on the exec-summary prompt. Omit the line when none apply. No `LABELS:` line → `[]`. | `/status.labels` exists for every deployment. Prompting empty `LABELS:` invites `none` / `n/a`. |
| Q6 | Parse failure | Lenient parse, then **one** reminder on the same conversation. Still fail-open. Do not retry omit. | Recovers `LABELS: none` without a dedicated extract agent. Scoring’s five extraction retries are the wrong copy (score line is required; labels may be omitted). |
| Q7 | Strip | Session `executive_summary` plus exec-summary timeline `final_analysis` / `llm_response` when parse succeeds. Raw text stays on LLM interactions. | Slack, dashboard tooltip, and posted summary body must not show `LABELS:`. Same pattern as action-marker cleanup. Do not revive the leftover session-level `executive_summary` timeline event. |
| Q8 | Prompt placement | System prompt unchanged. Layers 1–4 on the **user** prompt after analysis + write cue; layer 4 last. | Hard constraints already live on the user message. Format last so generation starts after the trailer contract. |
| Q9 | v1 API / dashboard | Status + detail + list chips + `label=` contains + GIN + filter-options. No WebSocket field. No triage filter. | Downstream consumers poll HTTP `/status`. Dashboard already refetches on `session.status`. Filter options come from distinct stored names, not a two-value enum. |
| Q10 | Chat vs scoring | Chat: persisted summary footer, skip exec-summary stage, no labels. Scoring: stage + persisted summary + stored labels; no map; no sixth dimension. | Chat answers “what did we tell people?” Labels are paging metadata the operator already sees as chips. Scoring can catch “said FP, labeled `action`” without becoming config-aware. |

## Architecture

```
Alert → chain (investigation / synthesis / action / compose)
  → final_analysis
  → ExecSummaryAgent (typed exec_summary stage, fail-open)
       1–4 line facts-only summary
       + LABELS layers 1–4 (active map, format keyed by multi)
  → LLM text:
       <1–4 line summary>
       LABELS: page             # omitted entirely when none apply
  → Exec-summary controller (same execution, not a worker post-pass):
        last non-empty line starts with LABELS: (lenient)?
        yes + valid → strip, canonical []string (empty remainder → [])
        yes + invalid → one reminder Generate on the same conversation;
                        if still invalid or reminder LLM errors →
                        first summary unchanged, labels null
        no  → labels []  (do not retry)
  → Worker writes session.executive_summary + session.labels
       (empty labels slice is a real write; unset leaves NULL)
  → GET /api/v1/sessions/:id/status
       { id, status, executive_summary, labels }
  → Client: notify if labels contains a name it cares about (e.g. page)
```

Exec summary already runs **inline before** the session is marked complete (fail-open), so `GET /sessions/:id/status` can return `labels` with the terminal status. Scoring is async *after* complete and must not carry this signal.

```mermaid
sequenceDiagram
  participant Inv as Investigation/compose
  participant ES as ExecSummaryAgent
  participant LLM as LLM service
  participant W as Worker
  participant API as GET /sessions/:id/status
  participant C as Downstream client

  Inv->>ES: final_analysis
  ES->>LLM: summary prompt + LABELS layers (active map)
  LLM-->>ES: summary + LABELS: page
  Note over ES: parse; optional one reminder on same conversation
  ES->>W: cleaned summary + canonical labels
  W->>W: session complete (write executive_summary + labels)
  C->>API: poll until terminal
  API-->>C: executive_summary, labels
  Note over C: match stored names (e.g. page) for notify/close
```

### Active map

Resolve **last non-empty**: `defaults.label_map` → `chain.label_map`. Empty / omitted inherits the previous layer. Both empty → **`builtin`**. Named value must exist in the catalog **after** load-time inject.

`label_map: builtin` is a real selector. A chain can opt back to the (effective) builtin when `defaults.label_map` is a custom map.

Each map is `{multi, instructions?, labels []}` with `multi` defaulting to `false`. The label list is an ordered sequence of `{label, description}`. Catalog keys may be a mapping; only the list inside a map must stay ordered.

After YAML load: if `label_maps` has no `builtin` key, inject the Go map (attention essay in `instructions`, `watch` / `action` / `noise`, `multi: false`). If YAML defines `label_maps.builtin`, keep that entry as-is (**full replace**). Log a startup warning when YAML overrides `builtin`.

`multi` is enforced in **layer 4 and the parser**. Layer 2 (instructions) must not be the only place cardinality is stated.

`ExecSummaryAgent.custom_instructions` is **not** the labeling hook.

### Builtin map

SRE-generic. **`multi: false`.** This is **what to do with the alert**, not `actions_executed` and not `needs_review`. Used when all selectors are empty or the resolved name is `builtin`, unless YAML replaced `label_maps.builtin`.

There is no `none` / `ignore` label. **`[]` is not a close verdict.** Omit the `LABELS:` line when none of the three apply (unclassified, or remaining work that is not `watch` / `action` / `noise` — for example “file a backlog ticket” as the actual next step). Operators must still read the summary before closing when labels are empty or null.

A YAML override of `builtin` does **not** keep the Go essay unless it copies the text.

| Label | Use when | Do **not** use when |
|---|---|---|
| `watch` | The subject still looks off or evidence is thin. No intervention *now*; look again if it persists. | The analysis already concluded the alert can be closed with no follow-up (`noise`). Detector-tuning *as the reason to keep the alert open* is not `watch`. |
| `action` | A human must intervene **on the affected system** (fix the failing workload, approve an emergency change, execute a runbook step that was not automated). | Closing or acking the alert; filing a backlog ticket; tuning detection/rules; “no remediation because it was benign.” Not the same as `actions_executed`. Closable-with-no-work is `noise`, not `action`. |
| `noise` | This alert can be **closed with no remaining human work**. False positive, expected/maintenance, already recovered and done, duplicate of known-benign. Optional “tune later” in passing does not block `noise`. | A human should still look again (`watch`), intervene (`action`), or do a required follow-up (file a ticket, change a rule) before the alert is done. Unsure → `watch`, never `noise`. Omitting `LABELS:` is not `noise`. |

If the report disagrees with itself, prefer `watch` over `action`. Prefer `watch` over `noise` when unsure. Prefer `noise` over omit when the classification is clearly closable with no follow-up. Apply at most one of these three.

Other catalog entries **do not** have to use these names. To **extend** watch/action/noise with another tag, copy them into a new map name — there is no merge with `builtin`.

### Last-line contract

```text
LABELS: <label>
LABELS: <label>, <label>, ...
```

When none apply, **omit the line**. Parse the **last non-empty line** (trim trailing whitespace, then that last line). A `LABELS:` line that is not last does not count — no reminder.

Lenient match (must succeed without a retry):

1. Trim wrapping `*`, `_`, and backticks on that **line** (not per token). `LABELS: **page**` is leftover junk → parse failure → reminder.
2. Prefix is `LABELS` then optional whitespace then `:` then optional whitespace (case-insensitive). `LABELS:page` is valid. `LABEL:` without S is not.
3. Remainder: split on comma; trim each token; drop empty tokens (trailing comma).
4. Each token: trim a single trailing `.` or `;` if that makes it match. Case-insensitive match to the active list. **Store the canonical `label` from the slice**, unique, in **map order**.
5. Reject leftover junk in a token (`page please`) and names not in the list.

Cardinality (apply **after** uniquing into map order):

- `multi: false` — at most one unique name. Two or more distinct names → parse failure. `LABELS: watch, watch` is one name. Empty remainder (`LABELS:` with no names) is accepted as `[]` but is not prompted.
- `multi: true` — any unique subset of the list. Unknown token → parse failure. Empty remainder is accepted as `[]` but is not prompted.

| Last non-empty line | Result | Strip? | Retry? |
|---|---|---|---|
| No `LABELS:` prefix | `[]`, success | no | no |
| Valid names / valid empty remainder | canonical `[]string`, success | yes | no |
| Prefix matches but invalid | parse failure | no | one reminder |

Strip only when a `LABELS:` line parses successfully. New sessions do **not** create a session-level `event_type=executive_summary` timeline event. Chat and scoring read `alert_sessions.executive_summary`.

On reminder success, the stored summary and the `final_analysis` event come from the **retry** output (stripped). The first-turn `llm_response` may still show the bad trailer. If the reminder fails or the reminder LLM call **errors** (not cancel/timeout): `labels` null, **first** summary unstripped. Do not fail the exec-summary stage. Cancel and timeout still follow today’s exec-summary stage mapping.

Log parse-after-retry at warn. Increment `tarsy_session_labels_parse_failures_total`.

Empty first response is already retried by the single-shot controller. That is not the labels reminder. Labels reminder runs only after a non-empty completed generation whose last line is a bad `LABELS:` trailer.

### Prompt layers

Every resolved map is the same shape. Layer 2 is **`instructions` if non-empty, else** the generic “apply the label(s) whose descriptions best match; if none match, omit the LABELS line.” The Go-injected `builtin` ships with the attention essay already in `instructions`. A YAML `label_maps.builtin` that omits `instructions` gets the generic matcher, not the Go essay.

| # | Source |
|---|---|
| 1 | TARSy, fixed. Encode using **only** labels from the map below. Do not invent labels or a new verdict. The 1–4 line body stays facts-only. |
| 2 | Active map `instructions`, or generic matcher if empty |
| 3 | Active map `labels` **in sequence order** |
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

Cardinality in layer 2 may *reinforce* `multi` but does not replace it.

**Placement:** System prompt is unchanged (role + 1–4 lines). User prompt order:

1. Current CRITICAL RULES + analysis blob
2. Existing write cue: `Executive Summary (1-4 lines, facts only):`
3. Layers 1–4. Layer 4 is the last text in the user message.

Reminder user text is a separate builder, not a fifth always-on layer.

### Persistence and API

- `labels` on `alert_sessions`: optional JSON `[]string`. Not an enum.
- **NULL** while in progress, if exec summary was skipped (`final_analysis` empty → stage not run), if the exec-summary stage failed, or if a `LABELS:` line failed to parse after retry.
- **`[]`** when the summary completed with no `LABELS:` line, or with a valid empty trailer.
- Written in the worker’s terminal session update next to `executive_summary`. Three-state persist: unset = leave NULL; including empty slice = write `[]`.

JSON on the wire always emits the `labels` key. Nil marshals as `null`; empty as `[]`.

```json
{ "id": "…", "status": "completed", "executive_summary": "...", "labels": ["page"] }
{ "id": "…", "status": "completed", "executive_summary": "...", "labels": [] }
{ "id": "…", "status": "in_progress", "executive_summary": null, "labels": null }
```

| Surface | Labels |
|---------|--------|
| `GET /api/v1/sessions/:id/status` | yes (poll contract) |
| `GET /api/v1/sessions/:id` | yes |
| `GET /api/v1/sessions` list item | yes |
| `GET /api/v1/sessions?label=<token>` | contains (`labels @> '["page"]'::jsonb`); exact stored canonical name; token charset `[A-Za-z][A-Za-z0-9_-]*`; invalid token → 400. `NULL` and `[]` do not match |
| `GET /api/v1/sessions/filter-options` | distinct stored names from non-deleted, non-empty arrays |
| Dashboard | chips on historical list, triage rows, and session detail; FilterPanel select; no triage `label=` query param |
| WebSocket `SessionStatusPayload` | **no** — dashboard refetches on `session.status` |
| `GET /api/v1/sessions/:id/summary` | **no** (usage/cost payload) |

GIN index `idx_alert_sessions_labels_gin` (`jsonb_path_ops`) supports containment `@>`.

Clients that want a flag derive it from names **they** define. TARSy does not ship `needs_attention`. Do **not** treat `[]` as closable. Do **not** store this on `session_metadata` JSON.

Load-time validation: catalog key and `label` token ASCII `[A-Za-z][A-Za-z0-9_-]*` (must start with a letter; no comma). At least one label; `label` and `description` non-empty. Duplicate `label` values in one map fail (case-insensitive). After inject, every `label_map` selector must name a catalog entry (`builtin` always exists).

Config viewer shows the catalog **including `builtin`** (injected or YAML) and the raw `label_map` selectors (no per-session expansion), same as named fallback lists.

### Downstream consumers (chat vs scoring)

Source of truth for the **posted** 1–4 line summary is `alert_sessions.executive_summary`, not a leftover `event_type=executive_summary` timeline row.

| Consumer | Exec-summary **stage** | Persisted `executive_summary` | `labels` |
|---|---|---|---|
| Follow-up **chat** | Skip (investigation + synthesis + chat; [ADR-0008](0008-session-scoring.md)) | **Yes** — footer so chat can match what Slack posted | **No** |
| **Scoring** | **Yes** | **Yes** — same footer | **Yes** — stored names only (`[]` / `null` shown explicitly). No catalog, no `multi`, no descriptions |

Scoring without the map can still catch “investigation said benign/FP, labels are `action`” or “closable FP, labels empty instead of `noise`.” It cannot score “was `monitor` the right name on this catalog.” That is enough for v1. Do **not** add a sixth score dimension. The judge prompt includes a short sentence: the persisted summary must stay facts-only and labels must be consistent with the investigation conclusion.

## Configuration (user-facing contract)

Top-level `label_maps`, same idea as `fallback_lists`. Labels are a **sequence**. `multi` defaults to `false`.

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
  label_map: oncall   # optional; omit or builtin → catalog["builtin"]

agent_chains:
  kubernetes-investigation:
    label_map: oncall            # optional; last non-empty wins
  argocd-investigation:
    label_map: builtin           # opt back to builtin when defaults is custom
```

## What this is not

- Not a second classification LLM.
- Not a dedicated labeling / tagging agent (v1). Extra Generate on the `/status` wait path; scoring-style async would miss `/status`.
- Not Slack-specific. TARSy never emits Slack usergroup markup.
- Not a replacement for `review_status`.
- Not a TARSy paging/notification policy. Clients map stored labels (e.g. `page`) to whatever they already use.
- Not Go-side translation of custom labels into `watch` / `action` / `noise`.
- Not free-form tags. Unknown names are parse failures, not stored.
- Not user-applied or mid-investigation tags in v1.
- Not a `label_map` knob on `defaults.executive_summary` / `chain.executive_summary`.
- Not injecting the active `label_maps` catalog into chat or scoring.

## Out of scope

- Downstream notify/close policy: `if labels contains "page"` (or a configured list) → notify on-call
- Storing `label_map` name on the session (audit when catalogs change)
- Dedicated labeling agent; integer `max_labels`; `sort_by=label`; unlabeled (`[]` / null) filter; chip-click-to-filter; triage `label=` query param
- Putting labels or the label map into follow-up chat context
- Putting the label map (or a sixth score dimension) into the scoring judge
- `ExecSummaryAgent.custom_instructions` as a general overlay (orthogonal)
- Skills on exec summary

## References

- [ADR-0004: Stage Types](0004-stage-types.md)
- [ADR-0008: Session Scoring](0008-session-scoring.md)
- [ADR-0019: Read-Only Configuration Viewer](0019-config-viewer.md)
- [ADR-0030: Named Fallback Lists](0030-named-fallback-lists.md)
