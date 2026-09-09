# Session Labels — Design Questions

**Status:** All decisions made
**Related:** [Design document](session-labels-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Where does the LLM emit the labels trailer?

The investigation already decided (classification / recommended action in prose). Something must emit a contracted token Go can parse **before** the session is marked complete, or Guardian Cockpit’s existing `/status` poll will miss it.

### Option A: Last line of the executive summary (same LLM call)

- **Pro:** Zero extra Generate, no extra latency before Slack. Same fail-open stage GC already waits on.
- **Pro:** Matches action `YES`/`NO` last-line parse.
- **Pro:** Strip the line so Slack still gets 1–4 lines of prose.
- **Con:** Summarizer does two jobs (prose + encode). Overlay mapping and CRITICAL RULES compete in one prompt.
- **Con:** Changes the exec-summary golden and every deployment’s prompt, not only one site.

If parse fails later, Option B (extra extract shot) remains a follow-up without changing this contract’s client API.

**Decision:** Option A — last line of the executive summary. Strip that line from the stored summary **only when it parses as a `LABELS:` trailer against the active map** (builtin or custom). A successful empty `LABELS:` is accepted as `[]` but is not what the prompt asks for (Q5). If a `LABELS:` line does not parse, leave the executive summary untouched and leave `labels` null. Live prompt wiring ships with parse + strip (PR 2), not in a prompt-only PR that would leak `LABELS:` into Slack.

_Considered and rejected: Option B (extra single-shot — extra cost/latency on the GC wait path; revisit if trailer parse is unreliable), Option C (investigation/compose last line — too much blast radius), Option D (dedicated labeling agent — second Generate if inline; if async like scoring, GC’s `/status` poll misses it). A labeling agent is a later feature if we need re-label, labels without exec summary, or mid-chain tags._

---

## Q2: What is the builtin default map?

A boolean collapses “needs a human now” vs “keep an eye on it” vs “nothing to do.” The builtin list must stay meaningful for every deployment. “A human must do something” is too wide: false-positive reports often recommend closing the alert and tuning detection — that is housekeeping, not paging.

These three labels are the **zero-config map**, with `multi: false`. Custom maps (Q4) may use different names, counts, and `multi: true`. The session column stores whatever names were chosen, as strings. There is no `none` token. **`[]` is not “close as noise.”**

Operators close alerts after reading the executive summary. A positive closable bit (`noise`) is what they can match; empty labels still mean “read the summary, do not auto-close.” A custom map can use `false_positive` instead of `noise`; TARSy does not translate.

### Option A: `watch` / `action` / `noise` (no `none`)

- **Pro:** Operational English without deployment-specific words in TARSy.
- **Pro:** No `LABELS:` line (or empty `LABELS:`) is `[]`; `null` stays “we could not parse a `LABELS:` line.” `noise` is the closable verdict.
- **Pro:** Three buckets match page / watch / close-with-no-work, without overloading omit.
- **Con:** `action` might be read as “automated action already ran” (`actions_executed`). Mitigate in the prompt: this is **human intervention on the incident subject**, not tool execution and not alert-hygiene.
- **Con:** `noise` is broader than detector-FP (includes already-recovered / expected). That matches “close with no remaining work,” not a forensics finding.

Canonical definitions (including negatives) are in the [design document](session-labels-design.md#builtin-map). Catalog keys and `label` tokens are ASCII `[A-Za-z][A-Za-z0-9_-]*`.

**Decision:** Option A — builtin map is `watch` / `action` / `noise`, `multi: false`. `action` = intervene on the affected system. `noise` = close this alert with no remaining human work. Filing a backlog item or tuning a rule as the *required* next step is **not** `noise` (omit, or `watch` if they should look again). Prefer `watch` over `action` if the report disagrees; prefer `watch` over `noise` when unsure; prefer `noise` over omit when clearly closable. Clients must not treat `[]` as closable.

_Considered and rejected: Option B (`escalate` as the intervene label), Option C (boolean only), a builtin `none`/`ignore` label (omit already means unlabeled; `ignore` reads like “skip this”), `false_positive` as the builtin name (too detector-specific for recovered-true-positives; a custom map can use that word), extra builtin labels for “tune rules / close ticket” as *required* work (still not a builtin; that stays unlabeled). Treating `[]` as noise (confused omit would look closable)._

---

## Q3: What is the API/DB field, and do we also expose a boolean?

Clients (GC, dashboard, status poll) need one stable JSON key. The value is zero or more names from the **active** map (builtin or custom). The mechanism is a list even when builtin policy is exclusive (`multi: false`).

### Option A: `labels` `[]string` only (no `attention` string, no derived boolean)

- **Pro:** Single source of truth. Clients test membership (`"action" in labels` or `"page" in labels`).
- **Pro:** `null` vs `[]` distinguishes a bad `LABELS:` line from “none apply.”
- **Pro:** `multi: true` does not require a later breaking change from a single string.
- **Con:** Clients write a membership check instead of `if (needs_attention)`.

**Decision:** Option A — optional JSON column / JSON `labels` (`[]string`). Last-line token `LABELS:`. Store canonical names from the active list, unique, in map order (uniqueness before cardinality). Null while in progress, if exec summary was skipped, if the exec-summary stage failed, or if a `LABELS:` line failed to parse. Empty array if the summary completed with no `LABELS:` line, or with a valid empty trailer. Worker persist uses `queue.ExecutionResult.Labels *[]string` (nil = leave NULL, including skipped summary; non-nil empty = write `[]`). No `attention` field and no derived boolean in TARSy. Always emit the JSON key (no `omitempty`).

_Considered and rejected: Option B (derived `needs_attention` — duplicates paging policy TARSy should not own), Option C (nullable string `attention` — exclusive-only schema; a later tags feature cannot share the exec-summary trailer), Option D (both `attention` and `labels` — two trailers / two encode jobs on one prompt)._

---

## Q4: How do deployments customize labels, and how is “one vs many” chosen?

Builtin `watch` / `action` / `noise` must work with **no YAML**. Some deployments want their own vocabulary (`monitor` / `page` / `false_positive`) and do not want TARSy to translate that back into the builtin three. Some maps will want several independent tags on one session; builtin attention must stay exclusive.

Cardinality belongs on the map as a field TARSy uses for **layer 4 and the parser**. It must not live only in replaceable layer-2 prose (format and parse would drift).

### Named catalog of ordered label lists (`label_maps`) plus `multi` bool

- **Pro:** Zero config uses the builtin list (`multi: false`). Optional catalog is structured (like `fallback_lists`), not free-form prompt dump.
- **Pro:** Arbitrary labels and count; TARSy does not map custom → builtin. Clients page on stored strings.
- **Pro:** A Go **slice** preserves YAML order in the prompt and in tests.
- **Pro:** `defaults.label_map` and `chain.label_map` select by name (last non-empty wins).
- **Pro:** `multi` default `false` keeps exclusive maps exclusive until someone opts in. Parser rejects two names when `multi` is false.
- **Con:** `labels` is an open string list; dashboard/GC must treat unknown names as opaque chips / configured page-on lists.
- **Con:** New config section and validation.

Prompt assembly:

1. Fixed builtin — overall goal (encode from the closed list; do not invent labels or a new verdict).
2. Map `instructions` if set; otherwise a short generic “apply the label(s) whose descriptions best match; if none match, omit the LABELS line.” The Go-injected `builtin` entry already has the exclusive-attention essay in `Instructions`.
3. The ordered label list from the active map — `label` + `description`.
4. TARSy template keyed by `multi` — last line `LABELS: <names>` when any apply; omit the line when none apply.

These four layers sit on the **user** prompt after CRITICAL RULES, the analysis blob, and the existing write cue (Q8).

**Decision:** Optional `label_maps` catalog; each map is `{multi, instructions?, labels []}` with `multi` defaulting to `false`. Reserved catalog key **`builtin`**. After load, if YAML omitted it, inject the Go `watch` / `action` / `noise` map (essay in `Instructions`). If YAML defines `label_maps.builtin`, that entry **fully replaces** the Go default (no merge). Empty `label_map` and `label_map: builtin` resolve the same. A chain can set `label_map: builtin` when defaults points at a custom map. Stored values are canonical names from the active list, not a translation to `watch` / `action` / `noise`. Selector is `defaults.label_map` then `chain.label_map` (last non-empty). It is not a field on the `executive_summary` job-pairing block.

_Considered and rejected: ExecSummaryAgent `custom_instructions` as the mapping hook; per-chain free-form only; token→enum map; cardinality only in layer 2 (no `multi`); integer `max_labels` in v1; putting `label_map` on `defaults.executive_summary` / `chain.executive_summary`; merging YAML maps with the Go builtin (copy into a new name to extend)._

---

## Q5: Is the LABELS last line always required, or only when a custom map is configured?

The last line is how `labels` is encoded. Builtin map and custom maps both need it in the prompt.

When **no** labels apply, two encodings are possible: `LABELS:` with no names, or no `LABELS:` line. Empty `LABELS:` looks unfinished; models fill it with `none` / `n/a` / invented tokens. Omitting the line is the usual “optional metadata” shape. Parser still accepts a valid empty `LABELS:` if the model emits one.

### Option A: Always on in the prompt — omit the line when none apply

- **Pro:** `/status.labels` exists for every deployment; GC contract is stable.
- **Pro:** No “forgot to enable” footgun. Zero YAML still gets builtin `watch` / `action` / `noise`.
- **Pro:** Prompt never asks for `LABELS:` with zero names.
- **Con:** Every exec-summary prompt changes. A missing line after a completed summary is `[]`, not “unknown” (malformed `LABELS:` stays null, Q6).

**Decision:** Option A — layers 1–4 always sit on the exec-summary prompt. If any labels apply, last line is `LABELS: …`. If none apply, do not emit a `LABELS:` line. Completed summary with no `LABELS:` line → `labels: []`. A `LABELS:` line that fails to parse → `labels` null, summary unstripped (Q6). For the builtin map, clearly closable-with-no-work is `noise`, not omit (Q2).

_Considered and rejected: Option B (opt-in only when `label_map` is set — two code paths, contradicts zero-config), prompting `LABELS:` with no names as the empty encoding (easy for the model to botch)._

---

## Q6: What happens when the last line does not parse?

Parse failure is a `LABELS:` line that does not match the active map after **lenient** parse (unknown name, leftover junk, cardinality). A completed summary with **no** `LABELS:` line is success `[]` (Q5), not this question.

Lenient parse (first shot, no retry) must accept obvious trailer formatting noise: `LABELS:page`, extra spaces around `:` and commas, last non-empty line, wrapping `*` / backticks on that **line**, trailing comma. Unique names in map order, then apply `multi`. It must **not** invent aliases (`LABEL:` without S, `none`, extra words in a token) or strip per-token markdown (`LABELS: **page**` is junk → reminder). Exact rules live in the [design document](session-labels-design.md#last-line-contract).

Exec summary is one short Generate. One reminder on the same conversation is cheap relative to investigation. Scoring’s five `maxExtractionRetries` are the wrong copy: the score line is required; labels may be omitted.

### Option B: One reminder retry, then fail-open

- **Pro:** Recovers `LABELS: none` / `LABELS: n/a` / unknown name without a dedicated extract agent.
- **Pro:** Still fail-open if the reminder also fails — session completes, original summary kept.
- **Con:** Extra Generate on the GC wait path. Bounded to **one**.

**Decision:** Option B — after a lenient parse still fails on a `LABELS:` line, one reminder Generate **on the same conversation** (append assistant + user reminder, like scoring extraction; allowed names + cardinality; same 1–4 line summary; omit the line if none apply). Implement this in the exec-summary controller path, not as a worker post-parse with no conversation. If the reminder parses, use that output (strip on success). If it fails or the reminder LLM call errors: `labels` null, **first** summary stored as-is (do not replace prose with a worse retry). Cancel/timeout still follow today’s exec-summary stage mapping. Do not retry when there is no `LABELS:` line. Do not fail the exec-summary stage for a bad trailer. Log at warn; add `tarsy_session_labels_parse_failures_total` in the same persist PR (`pkg/metrics` already uses `promauto`).

_Considered and rejected: Option A (no retry — silent miss on a bad trailer is worse than one short Generate), Option C (set `executive_summary_error` — couples “could not summarize” with “could not parse trailer”), copying scoring’s five extraction retries._

---

## Q7: Should the marker be stripped from stored executive summary and timeline?

Action stages strip `YES`/`NO` from `final_analysis` and from `final_analysis` / `llm_response` timeline events; raw text stays on the LLM interaction. New sessions do not create a legacy `executive_summary` timeline event.

### Option A: Strip from session `executive_summary` and exec-summary timeline `final_analysis` / `llm_response` if present

- **Pro:** Slack, dashboard tooltip, and GC body never show `LABELS: …`.
- **Pro:** Consistent with action marker cleanup.
- **Con:** Trace “what the model said” for the stage event is cleaned; full raw remains on `llm_interactions`.

**Decision:** Option A — same helper pattern as `stripActionMarkerFromTimeline`. Strip only on successful parse (Q1). If Q6’s reminder succeeds, strip the **retry** output (session column and that execution’s `final_analysis` / `llm_response` events). The first-turn `llm_response` may still show a bad trailer. Do not strip when parse fails or when there is no `LABELS:` line. Do not revive the legacy session-level `executive_summary` timeline event.

_Considered and rejected: Option B (session column only — exec-summary stage card would still show the trailer), Option C (do not strip — Slack/GC would post metadata)._

---

## Q8: Where do the four LABELS prompt layers sit (system vs user)?

Today the system prompt is only role + “1–4 lines.” CRITICAL RULES, the analysis blob, and the write cue are already on the **user** message. Layers 1–4 (goal, map instructions, ordered labels, format) must produce a parseable `LABELS:` line **when any labels apply**, or no trailer when none apply (Q5). Customization is the named map (Q4), not `ExecSummaryAgent.custom_instructions`.

### Option B: Layers 1–4 on the **user** prompt after the analysis (format last)

- **Pro:** Format + label list sit next to the line the model must emit.
- **Pro:** Matches where hard constraints already live.
- **Con:** User golden includes the full map; tests must pin builtin vs a custom-map golden.

**Decision:** Option B — system prompt stays the current role/length line. User prompt stays CRITICAL RULES + analysis, then layers 1–4 with layer 4 the last text in the message. The existing “Executive Summary (1-4 lines, facts only):” cue stays; labels format follows it so generation starts after the trailer contract.

_Considered and rejected: Option A (all four on system — CRITICAL RULES on user can outweigh system), Option C (map on system, format on user — split across messages)._

---

## Q9: What ships in v1 besides `GET /sessions/:id/status`?

Guardian Cockpit polls status. List chips and a contains-filter are the same shape as `scoring_status` (query param → `DashboardListParams` → FilterPanel). JSONB GIN is one `CREATE INDEX` next to the existing FTS GIN helpers (`CreateGINIndexes`). Not a separate epic.

### Option C: Status + detail + list DTO + chips + `label=` filter + GIN

- **Pro:** Operators see and query the same strings GC uses.
- **Con:** Open vocabulary: filter options come from distinct stored labels, not a two-value enum.

**Decision:** Option C, tightly scoped:

- `GET /api/v1/sessions/:id/status` (`SessionStatusResponse`) and `SessionDetailResponse` include `labels`.
- `DashboardSessionItem.labels` + chips on the historical list and session detail (arbitrary strings).
- `GET /api/v1/sessions?label=<token>` — contains one name (`labels @> '["page"]'`). Exact match to stored canonical names. Token charset matches config labels. No comma-AND, no “unlabeled”, no `sort_by=label`.
- GIN on `alert_sessions.labels` via `CreateGINIndexes` (`jsonb_path_ops`; document so Atlas drift review does not drop it).
- `GET /api/v1/sessions/filter-options` adds distinct stored labels (same idea as `alert_types`). FilterPanel: one select.
- Do **not** add `labels` to `SessionStatusPayload` WebSocket — GC polls HTTP; dashboard already refetches on `session.status`.
- Not in v1: triage-page filter, chip-click-to-filter, sort by label.

_Considered and rejected: Option A (status/detail only — dashboard blind), Option B (chips without filter — index and query param are the cheap part)._

---

## Q10: Do follow-up chat and scoring need the executive summary and labels?

Chat and scoring both call `getExecutiveSummary()`, which looks for a session-level `event_type=executive_summary` timeline row. New sessions do not write that event; the text lives on `alert_sessions.executive_summary`. Chat also **skips** `StageTypeExecSummary` (intentional, covered by a unit test). Scoring already **includes** that stage (ADR-0008).

Chat already has investigation / compose / action timelines. The 1–4 line summary is what Slack/GC posted — useful so chat can answer “what did we tell people?” without inventing a different blurb. The exec-summary stage (thinking, streaming `llm_response`) is noise in a follow-up Q&A. Labels are paging/triage metadata; the operator sees chips on the session. Dumping them into chat without the map does not help much.

Scoring’s job is “how good was this investigation?” The persisted summary and labels are part of the **product**. The judge can check faithfulness against investigation prose without the catalog (“said FP, labeled `action`” or “closable, labels empty instead of `noise`”). Injecting the map would make scoring config-aware and would churn goldens whenever a deployment’s map changes. A sixth score dimension would recalibrate 0–100; a short “also check summary + labels” sentence does not.

**Decision:**

- **Chat:** persisted `executive_summary` footer only. Keep skipping the exec-summary stage. No labels, no map.
- **Scoring:** keep the exec-summary stage. Same persisted summary footer. Add stored labels (`[]` / `null` shown explicitly). No map. No sixth dimension; one judge-prompt sentence in the persist PR.
- **PR 0** (before labels): switch both readers from the dead timeline event to the session column.

_Considered and rejected: reviving the legacy timeline event; including the exec-summary stage in chat; putting labels in chat; injecting `label_maps` into the judge; a sixth scoring dimension._
