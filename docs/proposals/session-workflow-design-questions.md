# Session Workflow — Design Questions

**Status:** Open — decisions pending
**Related:** [Design document](session-workflow-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How should existing terminal sessions be backfilled?

When the migration runs, there are already `completed`, `failed`, `timed_out`, and `cancelled` sessions in the database with `review_status = NULL`. The Triage view needs to handle them.

### Option A: Backfill all terminal sessions to `needs_review`

The migration sets `review_status = needs_review` for all existing `completed`/`failed`/`timed_out` sessions, and `review_status = resolved` + `resolution_reason = dismissed` for `cancelled` sessions.

- **Pro:** Consistent. Every terminal session has a review status. The Triage view works immediately without special-casing NULL.
- **Con:** Floods the "Needs Review" column with potentially hundreds of old sessions that nobody intends to review.

### Option B: Backfill all terminal sessions to `resolved`

The migration sets all existing terminal sessions to `resolved` with `resolution_reason = dismissed`. Only new sessions going forward enter as `needs_review`.

- **Pro:** Clean start. The Triage view isn't overwhelmed with historical data.
- **Pro:** Teams start fresh — only new investigations need attention.
- **Con:** Historical sessions appear in the "Resolved" column, which might confuse users ("I didn't resolve these").

### Option C: Leave existing sessions as NULL, only apply to new sessions

Don't backfill. Existing sessions keep `review_status = NULL`. The Triage view only shows sessions created after the migration.

- **Pro:** Zero risk. No changes to existing data.
- **Pro:** Clean start without false "resolved" records.
- **Con:** The Triage view needs to handle the NULL case — sessions with NULL `review_status` that are in a terminal state don't appear in any workflow column. They're only visible in the Sessions tab.
- **Con:** If someone looks at an old session's detail page, the review section would show "No review status" which could be confusing.

**Recommendation:** Option B. Backfill all existing terminal sessions to `resolved` (reason: `dismissed`). This gives a clean start — the "Needs Review" column is empty on day one, only new investigations enter the workflow. Old sessions appear in "Resolved" but that's accurate — they were never formally reviewed. The alternative (Option C) requires special-casing NULL in every query and UI component, which adds complexity for little benefit.

---

## Q2: Should claiming an already-claimed session be allowed?

When session X is claimed by user A (`in_progress`, `assignee = A`), can user B click "Claim" to take it over?

### Option A: Block — only the current assignee or unclaimed sessions can be claimed

Return `409 Conflict` if someone tries to claim a session that's already assigned to someone else.

- **Pro:** Prevents accidental overrides. Clear ownership.
- **Pro:** Forces explicit handoff — A must unclaim first, then B claims.
- **Con:** If A is unavailable (off-shift, sick), someone needs to unclaim on their behalf, which isn't currently possible (only the assignee can unclaim their own session, unless we allow anyone to unclaim).

### Option B: Allow — claiming an already-claimed session reassigns it

Any user can claim any `in_progress` session, replacing the current assignee. Both transitions are logged in the activity table.

- **Pro:** No blockers. If A is unavailable, B just grabs it.
- **Pro:** Simpler — one action instead of unclaim-then-claim.
- **Con:** Accidental overrides — user B might not realize A was working on it.
- **Con:** Could cause confusion if two people claim the same session quickly.

### Option C: Allow with confirmation

If the session is already claimed by someone else, show a confirmation dialog: "This session is currently claimed by A. Take over?"

- **Pro:** Prevents accidental overrides while still allowing handoff.
- **Pro:** User is informed of the existing assignee.
- **Con:** Extra UI complexity (confirmation only in certain conditions).

**Recommendation:** Option C. Allow reassignment but with a frontend confirmation when the session is already claimed. Backend allows the transition unconditionally (no 409) — the confirmation is a UX guardrail, not a hard constraint. This handles the "A is off-shift" case gracefully while preventing accidents.

---

## Q3: Should resolving directly from `needs_review` be allowed?

Can an SRE resolve a session without claiming it first? E.g., they see a false positive in the "Needs Review" column and want to quickly dismiss it.

### Option A: Require claim first

Resolve is only valid from `in_progress`. To dismiss a false positive, the SRE must claim → resolve (two clicks).

- **Pro:** Every resolved session has an assignee — clear audit trail of who reviewed it.
- **Pro:** Simpler state machine — strictly linear transitions.
- **Con:** Two clicks for a common operation (bulk-dismissing noise).

### Option B: Allow direct resolve from `needs_review`

The state machine allows `needs_review` → `resolved` directly. The actor is recorded as the resolver in the activity table. The `assignee` field can optionally be set to the resolver.

- **Pro:** Fast dismissal of noise. One click instead of two.
- **Pro:** Reduces friction for high-volume triage.
- **Con:** Assignee might be NULL for directly-resolved sessions (unless we auto-set it).
- **Con:** Less strict audit — no explicit "I'm taking ownership" step for dismissed items.

### Option C: Allow direct resolve, auto-set assignee

Same as Option B, but automatically set `assignee` to the resolver when resolving directly from `needs_review`. This ensures every resolved session has an assignee.

- **Pro:** Fast dismissal + complete audit trail.
- **Pro:** Clean data — no NULL assignees on resolved sessions.
- **Con:** Slightly misleading — the "assignee" didn't really "work on" the session, they just dismissed it.

**Recommendation:** Option C. Allow direct resolve from `needs_review` with auto-set assignee. The primary use case is bulk-dismissing noise, and requiring two clicks per session is unnecessary friction. Auto-setting the assignee ensures every resolved session has an owner for audit purposes. The activity log distinguishes between "claimed then resolved" and "directly resolved" via the action sequence.

---

## Q4: Does the Triage view need a dedicated API endpoint?

The current session list uses `GET /api/v1/sessions` with filters. The Triage view needs sessions grouped by `review_status` with counts. Should it reuse the existing endpoint or get a new one?

### Option A: Reuse existing endpoint with new filters

Add `review_status` and `assignee` query params to `GET /api/v1/sessions`. The frontend makes multiple calls (one per group) or fetches all and groups client-side.

- **Pro:** No new endpoint. Minimal backend change.
- **Con:** Multiple API calls for one view (4 calls for 4 groups), or over-fetching all sessions.
- **Con:** No server-side counts per group without extra calls.

### Option B: New Triage endpoint with grouped response

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
- **Con:** New endpoint to implement and maintain.
- **Con:** Slightly different data shape from the existing list endpoint.

### Option C: Reuse existing endpoint + separate counts endpoint

Use `GET /api/v1/sessions` with `review_status` filter for each group. Add `GET /api/v1/sessions/review-counts` for the column counts.

- **Pro:** Reuses existing endpoint for data.
- **Pro:** Counts are efficient (single COUNT query, no data transfer).
- **Con:** Multiple API calls (counts + 3-4 data calls).
- **Con:** Counts and data can be slightly out of sync.

**Recommendation:** Option B. A dedicated triage endpoint is cleaner. The Triage view's data needs are different from the session list — it needs all groups at once with counts, and the "Resolved" group should be limited to recent items (the full list is in the Sessions tab). A single endpoint handles this efficiently with one DB query per group. The existing `GET /api/v1/sessions` gets the review filters too (for the Sessions tab optional columns), but the Triage view gets its own optimized endpoint.

---

## Q5: Should `review.status` events go to GlobalSessionsChannel only or also SessionChannel?

WebSocket events can be published to the global "sessions" channel (for the dashboard/Triage view) and/or the per-session channel (for the session detail page).

### Option A: GlobalSessionsChannel only

Publish `review.status` to the global channel. The session detail page doesn't get real-time review updates.

- **Pro:** Simpler — one channel.
- **Con:** If someone is viewing a session detail page while another user claims/resolves it, they won't see the update until they refresh.

### Option B: Both channels

Publish `review.status` to both `GlobalSessionsChannel` and `SessionChannel(sessionID)`.

- **Pro:** Session detail page gets real-time review status updates.
- **Pro:** Consistent with how `session.status` is published (both channels).
- **Con:** Slightly more work (two publish calls), but trivial.

**Recommendation:** Option B. Publish to both channels. The session detail page should show real-time review status (who claimed it, when it was resolved) without requiring a page refresh. This is consistent with how `session.status` events work — they go to both channels. The overhead is negligible.

---

## Q6: What information should Kanban cards show?

Kanban cards need to be compact but informative enough to identify and triage a session without opening the detail page.

### Option A: Minimal — ID, alert type, time

Just enough to identify the session. Click to see more.

- **Pro:** Maximum density — more cards visible per column.
- **Con:** Hard to triage without opening each session. Defeats the purpose of the board view.

### Option B: Standard — alert type, chain, author, time, executive summary snippet

A one-line summary of the investigation plus metadata. Similar to the current `SessionListItem` but in card form.

- **Pro:** Enough context to decide: claim, dismiss, or open for details.
- **Pro:** Executive summary snippet gives the TL;DR of what the AI found.
- **Con:** Taller cards mean fewer visible per column.

### Option C: Rich — everything from Option B + score badge, indicators, assignee

Include the quality score, parallel/sub-agent indicators, and assignee badge.

- **Pro:** Maximum information for triage decisions.
- **Pro:** Score helps prioritize (low-scoring investigations might be less trustworthy).
- **Con:** Visually busy. The card competes with the detail page for information density.

**Recommendation:** Option B. Standard information: alert type, chain ID, author, relative time, and a truncated executive summary (first ~100 chars). Add an assignee badge when claimed. Add a score badge if scoring is available (this is a lightweight addition). Keep indicators (parallel, sub-agents) out — they're investigation-internal detail that doesn't help with triage.

---

## Q7: Should the Triage view fetch all groups in one call or separate calls?

This depends on Q4 (dedicated endpoint vs. reuse). If Q4 is Option B (dedicated endpoint), the answer is likely one call. But there are nuances.

### Option A: Single call for all groups

One `GET /api/v1/sessions/triage` returns all four groups with data.

- **Pro:** Atomic snapshot — all groups are consistent.
- **Pro:** Single loading state.
- **Con:** Slower if the "Resolved" group is large (hundreds of sessions).
- **Con:** Can't independently refresh a single group.

### Option B: Single call with limits per group

One `GET /api/v1/sessions/triage?resolved_limit=20` returns all groups but caps the resolved group.

- **Pro:** Atomic + bounded response size.
- **Pro:** "Investigating" and "Needs Review" return all (typically small), "Resolved" returns the most recent N.
- **Con:** Need a "Load more" UX for the resolved column.

### Option C: Separate calls per group

Four independent API calls, one per review status group. Each can have its own pagination.

- **Pro:** Independent loading — "Needs Review" appears fast even if "Resolved" is slow.
- **Pro:** Easy to paginate each group independently.
- **Con:** 4 HTTP requests per view load.
- **Con:** Groups can be briefly out of sync during loading.

**Recommendation:** Option B. Single call with a `resolved_limit` parameter. The active groups (investigating, needs_review, in_progress) are typically small (single digits to low tens) and should always be returned in full. The resolved group can be large and benefits from a limit with "Load more" pagination. This keeps it to one API call while keeping response size bounded.

---

## Q8: Which drag-and-drop library?

The Kanban board needs drag-and-drop for moving cards between columns.

### Option A: @dnd-kit/core + @dnd-kit/sortable

Modern, lightweight (~15kB), accessible (ARIA, keyboard), React 18+ compatible. Active maintenance. Used by many modern Kanban implementations.

- **Pro:** Small bundle size, good performance.
- **Pro:** Built-in accessibility (keyboard drag, screen reader announcements).
- **Pro:** Flexible API — supports multiple drop targets, drag overlays, collision detection.
- **Con:** Requires some setup for the sortable + droppable combination.

### Option B: react-beautiful-dnd

Atlassian's library, well-known. But deprecated/unmaintained since 2024.

- **Pro:** Very polished drag animations.
- **Con:** Unmaintained. No React 19 support. 187kB bundle.
- **Con:** React 19 strict mode issues.

### Option C: Native HTML5 drag-and-drop

No library. Use the browser's native drag-and-drop API.

- **Pro:** Zero bundle size increase.
- **Con:** Poor mobile support.
- **Con:** No built-in accessibility.
- **Con:** Significantly more code for a good UX (drag previews, drop indicators, animation).

**Recommendation:** Option A. `@dnd-kit` is the clear choice — modern, maintained, lightweight, accessible, and React 19 compatible. `react-beautiful-dnd` is dead. Native DnD is too much work for a polished result.
