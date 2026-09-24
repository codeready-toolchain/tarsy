# Session Cancel Reason — Design Questions

**Status:** Decided
**Related:** [Design document](session-cancel-reason-design.md)

Each question has options with trade-offs. Decisions below are the source of truth for the final design.

---

## Q1: Where should session cancel metadata live?

Cancel actor and reason need to survive the `cancelling` → `cancelled` hop, show up on the dashboard list tooltip, and not be wiped when the worker finalizes the session. Several existing buckets look tempting (`error_message`, `session_metadata`, review activity).

### Option A: Dedicated nullable columns on `alert_sessions`

- **Pro:** Queryable, typed, easy to add to the dashboard list SQL and detail DTOs (same pattern as `author`, `assignee`).
- **Pro:** Worker can leave them alone when writing terminal status; no JSON merge races.
- **Con:** Migration (nullable, no backfill).

**Decision:** Option A — dedicated nullable columns on `alert_sessions`.

_Considered and rejected: Option B (reuse `error_message` — semantically an error and the worker overwrites it), Option C (`session_metadata` JSON — not in the list query, unstructured), Option D (audit table — overkill for a single terminal transition)._

---

## Q2: What fields represent who / what / why?

The must-have is **who cancelled** (user) and **optional why**. Nice-to-have is **what initiated** a system cancel.

### Option A: `cancelled_by` + `cancel_reason` only

- **Pro:** Smallest schema. User cancel: `cancelled_by = extractAuthor`, `cancel_reason` optional. System cancel (Q5): `cancelled_by = "system"`, short reason.
- **Pro:** Tooltip is a single format: `Cancelled by {cancelled_by}` plus optional `: {reason}`.
- **Con:** `"system"` and `"api-client"` are both synthetic identities; UI cannot style them differently without string matching.

**Decision:** Option A — `cancelled_by` + `cancel_reason` only. `"system"` is a safety net for the rare `context.Canceled` path with no prior API write (production session cancel is dashboard/API). Detail for that path goes in `cancel_reason` (e.g. `worker context cancelled`), not a source enum.

_Considered and rejected: Option B (`cancel_source` enum — no real production values beyond user/api-client), Option C (reason-only — does not record who cancelled)._

---

## Q3: Should pending (queued) sessions be cancellable with the same reason flow?

The dashboard Cancel button is shown for `pending` (`canCancelSession`). `CancelSession` only CAS-updates `in_progress` → `cancelling`, so queued cancel fails with “not in a cancellable state”.

### Option A: Support pending → `cancelled` immediately, with actor + reason

- **Pro:** Matches the UI the user already has; cancelling a queued duplicate is a real case.
- **Pro:** No worker: one update to terminal `cancelled`, `completed_at`, `review_status = reviewed` (same auto-review as other cancelled sessions).
- **Con:** Slightly wider CAS predicate (`pending` or `in_progress`).

**Decision:** Option A — pending → `cancelled` immediately with actor + reason. CAS still serializes against a worker claim (`in_progress` takes the cancelling path). Handler publishes `session.status=cancelled` and `review.status=reviewed` (worker is not involved, and the list/detail pages only live-update on those events).

_Considered and rejected: Option B (leave broken — UI already shows Cancel), Option C (pending → `cancelling` — nothing is running to observe it)._

---

## Q4: Should chat Stop on a terminal session collect a reason?

`POST /sessions/:id/cancel` also stops an in-flight follow-up chat. Chat is only offered after the investigation is terminal. The chat Stop control is an icon with no confirmation dialog.

### Option A: Out of scope for session fields and reason dialog; record chat Stop actor on the chat stage

- **Pro:** Chat cancel does not change session status; session-level “who cancelled the investigation” would be misleading on a completed session.
- **Pro:** No extra UI on the Stop button (must-have is the session Cancel dialog).
- **Con:** No optional *why* for chat Stop (Stop stays one click).

**Decision:** Option A — no session cancel fields, no Stop dialog. Persist `extractAuthor` on the in-progress chat stage (and its active execution) at request time as `error_message` (`Cancelled by {actor}`). Chat terminal writes today clobber that: `UpdateAgentExecutionStatus` with `context canceled`, then `UpdateStageStatus` with `all agents cancelled`. Cancelled terminal writes must not overwrite a non-empty `error_message`. Same **non-error display contract as Q6**: status stays `cancelled`; the dashboard must not treat that string as a failure (`ErrorCard`, `severity="error"`, red accent). Today `FAILED_EXECUTION_STATUSES` includes `cancelled`, so a cancelled chat stage can show **both** “Stage Failed” and “Stage Cancelled” — implementation must stop using the failed-family set for timeline/trace cancelled rows (ScoreBadge may keep a local set for scoring shutdown). Copy is grey/info **Cancelled** plus the actor.

_Considered and rejected: Option B (reason dialog on chat Stop — extra UX on an immediate control; not asked for)._

---

## Q5: How much system auto-cancel attribution do we want?

User request: nice-to-have, not must. Real **session** `cancelled` without a user request is uncommon: timeouts and orphans are `timed_out`; graceful shutdown waits, then orphan-recovers as `timed_out`; scoring/chat `Stop()` cancel those jobs, not the session. The leftover is parent context cancelled while a worker is executing.

### Option B: If terminal is `cancelled` and `cancelled_by` is still null, set `cancelled_by = "system"` and a short reason

- **Pro:** Cheap in `updateSessionTerminalStatus` / `mapCancellation`. Covers parent-ctx cancel.
- **Con:** Reason will be generic (`worker context cancelled`). Must not apply to `timed_out`.

**Decision:** Option B — safety net only, as already agreed in Q2. `cancelled_by = "system"` and a short `cancel_reason` when the session is `cancelled` with no prior API write. Do not reclassify `timed_out` / orphans; do not treat scoring or chat `Stop()` as session cancel.

_Considered and rejected: Option A (leave those rows unattributed), Option C (scoring/chat shutdown — wrong entity)._

---

## Q6: Should `cancel_agent` grow an optional `reason`?

Orchestrator tool is currently `cancel_agent({ execution_id })`. Sub-agent cancelled banner and the result injected into the orchestrator conversation usually say `context canceled`. `interruptedResult` already uses `context.Cause`.

### Option A: Add optional `reason`; pass it with `WithCancelCause`; store in existing `error_message`

- **Pro:** Optional, so old tool calls keep working. Cause already flows to execution status and `FormatSubAgentResult`. `SubAgentCard` already shows `error_message`. No new columns.
- **Pro:** Timeline `cancel_agent` tool event already stores arguments.
- **Con:** `error_message` is still a slightly overloaded name (same as today for timeouts, which already use `WithTimeoutCause`).

**Decision:** Option A, required, as **PR3 after dashboard chrome**. Persist a friendly attribution string (e.g. `Cancelled by {parent agent name}` plus `: {reason}` if present), not `context canceled`. Status stays `cancelled`. Dashboard must key off status: grey/info **Cancelled** plus details — never `ErrorCard`, error alerts, or red accents (timeline **and** trace). Same contract as Q4 chat stages. `FAILED_EXECUTION_STATUSES` must not include `cancelled` for timeline/trace. Over-length `reason` is a tool `IsError`, not HTTP 400. Do not merge into PR1 (orchestrator vs session persist) or swap with PR2 (that would delay the session cancel dialog and put attribution on error chrome).

_Considered and rejected: Option B (dedicated execution column — extra migration for a nice-to-have), Option C (skip — leaves `context canceled` and error chrome)._

---

## Q7: What max length for the reason?

Free text needs a bound at the API (and matching UI counter). Chat messages allow 100k; a cancel reason should not.

### Option A: 500 characters

- **Pro:** Fits a confirmation dialog; enough for “duplicate of X” / “false positive”. Cheap to store and tooltip.
- **Con:** Unusual long notes get truncated.

**Decision:** Option A — 500 characters. Trim whitespace; empty after trim → `NULL`; reject above 500 with HTTP 400 (count Unicode runes). Same cap for `cancel_agent` `reason`. Dialog shows a remaining-character counter.

_Considered and rejected: Option B (2000 — too much for a tooltip/dialog), Option C (unbounded)._

---

## Q8: Which UI (and Slack) surfaces show the metadata?

Must-have: tooltip on cancelled status in the main view. “Etc.” needs a boundary.

### Option B: Dashboard tooltip / header / detail copy + Slack attribution (not Error)

- **Pro:** Covers list, triage (same `StatusBadge`), session page. Detail refetch already runs on terminal status.
- **Pro:** `FinalAnalysisCard` and the empty-timeline cancelled alert can include actor/reason when present.
- **Pro:** Slack is the other place operators see cancelled sessions; add actor/reason on the existing `SessionCompletedInput` instead of stuffing `ErrorMessage`.
- **Con:** Slightly more copy to get right.

**Decision:** Option B — tooltip everywhere `StatusBadge` is used for sessions; richer cancelled copy on the detail page; Slack uses actor/reason as cancel attribution, never `*Error:*` / `context canceled`. No new timeline event type. Pending cancel has no Slack start thread today — do not invent a new Slack message for queued-only cancels.

_Considered and rejected: Option A (dashboard only — Slack would still prefix `*Error:*` if `error_message` is set), Option C (new `session_cancelled` timeline event — extra WS/renderer; tooltip already covers the list)._
