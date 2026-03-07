# Session Workflow — Design Questions

**Status:** Final — all decisions recorded
**Related:** [Design document](session-workflow-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How should existing terminal sessions be backfilled?

When the migration runs, there are already `completed`, `failed`, `timed_out`, and `cancelled` sessions in the database with `review_status = NULL`. The Triage view needs to handle them.

### Option B: Backfill all terminal sessions to `resolved` (chosen)

The migration sets all existing terminal sessions to `resolved` with `resolution_reason = dismissed`. Only new sessions going forward enter as `needs_review`.

- **Pro:** Clean start. The Triage view isn't overwhelmed with historical data.
- **Pro:** Teams start fresh — only new investigations need attention.
- **Con:** Historical sessions appear in the "Resolved" column, which might confuse users ("I didn't resolve these").

**Decision:** Option B — backfill all existing terminal sessions to `resolved` (dismissed). Clean start; only new investigations enter the workflow.

_Considered and rejected: Option A — backfill to `needs_review` (floods the queue with old sessions). Option C — leave as NULL (requires NULL handling in every query and UI component)._

---

## Q2: Should claiming an already-claimed session be allowed?

When session X is claimed by user A (`in_progress`, `assignee = A`), can user B click "Claim" to take it over?

### Option C: Allow with confirmation (chosen)

If the session is already claimed by someone else, the frontend shows a confirmation dialog: "This session is currently claimed by A. Take over?" Backend allows the transition unconditionally (no 409) — the confirmation is a UX guardrail, not a hard constraint.

- **Pro:** Prevents accidental overrides while still allowing handoff.
- **Pro:** User is informed of the existing assignee.
- **Pro:** Handles "A is off-shift" gracefully — B just confirms and takes over.

**Decision:** Option C — allow reclaim with frontend confirmation. Backend is unconditional; the UX prevents accidents.

_Considered and rejected: Option A — block reclaim (requires explicit unclaim by A, who may be unavailable). Option B — allow unconditionally (accidental overrides)._

---

## Q3: Should resolving directly from `needs_review` be allowed?

Can an SRE resolve a session without claiming it first? E.g., they see a false positive in the "Needs Review" column and want to quickly dismiss it.

### Option C: Allow direct resolve, auto-set assignee (chosen)

The state machine allows `needs_review` → `resolved` directly. Automatically sets `assignee` to the resolver, ensuring every resolved session has an owner.

- **Pro:** Fast dismissal of noise — one click instead of two (claim → resolve).
- **Pro:** Clean data — no NULL assignees on resolved sessions.
- **Pro:** Activity log distinguishes "claimed then resolved" from "directly resolved" via action sequence.

**Decision:** Option C — allow direct resolve from `needs_review` with auto-set assignee. Fast noise dismissal with complete audit trail.

_Considered and rejected: Option A — require claim first (two clicks for bulk dismissal is unnecessary friction). Option B — direct resolve without auto-set assignee (NULL assignees on resolved sessions)._

---

## Q4: Does the Triage view need a dedicated API endpoint?

The current session list uses `GET /api/v1/sessions` with filters. The Triage view needs sessions grouped by `review_status` with counts.

### Option B: New Triage endpoint with grouped response (chosen)

`GET /api/v1/sessions/triage` returns sessions pre-grouped by review status with counts:

```json
{
    "investigating": { "count": 2, "sessions": [...] },
    "needs_review": { "count": 5, "sessions": [...] },
    "in_progress": { "count": 1, "sessions": [...] },
    "resolved": { "count": 12, "sessions": [...] }
}
```

- **Pro:** Single call for the entire Triage view.
- **Pro:** Server-side counts are accurate.
- **Pro:** Can limit `resolved` to recent (e.g., last 24h or last 50) while showing all active groups fully.

The existing `GET /api/v1/sessions` also gets `review_status` and `assignee` filter params (for the Sessions tab optional columns).

**Decision:** Option B — dedicated `GET /api/v1/sessions/triage` endpoint with grouped response. Single call, server-side grouping, bounded resolved group.

_Considered and rejected: Option A — reuse existing endpoint (4 API calls per view, no server-side counts). Option C — existing endpoint + counts endpoint (multiple calls, sync issues)._

---

## Q5: Should `review.status` events go to GlobalSessionsChannel only or also SessionChannel?

WebSocket events can be published to the global "sessions" channel (for the dashboard/Triage view) and/or the per-session channel (for the session detail page).

### Option B: Both channels (chosen)

Publish `review.status` to both `GlobalSessionsChannel` and `SessionChannel(sessionID)`.

- **Pro:** Session detail page gets real-time review status updates.
- **Pro:** Consistent with how `session.status` is published (both channels).
- **Pro:** Negligible overhead (two publish calls).

**Decision:** Option B — publish to both channels. Consistent with existing `session.status` pattern.

_Considered and rejected: Option A — global only (session detail page wouldn't see real-time review changes)._

---

## Q6: What information should Kanban cards show?

Kanban cards need to be compact but informative enough to identify and triage a session without opening the detail page.

### Option B: Standard — alert type, chain, author, time, executive summary snippet (chosen)

Card contents: alert type, chain ID, author, relative time, truncated executive summary (~100 chars), assignee badge when claimed, score badge if scoring is available.

- **Pro:** Enough context to decide: claim, dismiss, or open for details.
- **Pro:** Executive summary snippet gives the TL;DR of what the AI found.
- **Pro:** Score badge is a lightweight addition that helps prioritize.

**Decision:** Option B — standard information. Investigation-internal indicators (parallel, sub-agents) are excluded — they don't help with triage.

_Considered and rejected: Option A — minimal (forces opening every session to triage). Option C — rich (visually busy, competes with detail page)._

---

## Q7: Should the Triage view fetch all groups in one call or separate calls?

Given Q4 (dedicated triage endpoint), this is about the response shape and how to handle the potentially large Resolved group.

### Option B: Single call with limits per group (chosen)

One `GET /api/v1/sessions/triage?resolved_limit=20` returns all groups but caps the resolved group.

- **Pro:** Atomic + bounded response size.
- **Pro:** Active groups (investigating, needs_review, in_progress) return in full — typically small (single digits to low tens).
- **Pro:** Resolved returns the most recent N with "Load more" pagination.

**Decision:** Option B — single call with `resolved_limit` parameter. Bounded response, atomic snapshot for actionable columns.

_Considered and rejected: Option A — all data unbounded (resolved group can be huge). Option C — separate calls per group (4 HTTP requests, sync issues)._

---

## Q8: Which drag-and-drop library?

The Kanban board needs drag-and-drop for moving cards between columns. TARSy's dashboard runs React 19.

### Option A: @dnd-kit/core + @dnd-kit/sortable (chosen)

Modern, ~27kB, built-in accessibility (ARIA, keyboard), React 19 supported (issue #1511 closed Feb 2026). 6.9M weekly npm downloads. Extensive community Kanban examples.

- **Pro:** React 19 compatible — confirmed.
- **Pro:** Built-in accessibility (keyboard drag, screen reader announcements).
- **Pro:** Flexible API — multiple drop targets, drag overlays, collision detection.
- **Pro:** Excellent cross-platform support including mobile.
- **Pro:** Largest community, most examples and resources.

**Decision:** Option A — `@dnd-kit`. React 19 compatible, accessible, well-maintained, strong community.

_Considered and rejected: @atlaskit/pragmatic-drag-and-drop — smaller bundle (~4.7kB), powers Jira/Confluence, but React 19 support is still an open issue (#181) — blocker for TARSy. react-beautiful-dnd — deprecated/unmaintained since 2024, 187kB, no React 19 support. Native HTML5 DnD — poor mobile support, no accessibility, too much code for a polished result._
