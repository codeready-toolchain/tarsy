# Feedback Edited-By: Design Questions

**Status:** Resolved
**Related:** [Design document](feedback-edited-by-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: How to source the "edited by" data

The `session_review_activities` table already records who performed each `update_feedback` action. We need to decide how to make this data available in the session DTOs.

### Option A: Computed subquery (like `feedback_edited` today)

Extend the existing `feedback_edited` EXISTS subquery into correlated subqueries that fetch the actor and timestamp of the most recent `update_feedback` activity row. No schema migration needed.

- **Pro:** Zero migration overhead — follows the exact pattern already used for `feedback_edited`
- **Pro:** Always consistent — derived from the source-of-truth activity log
- **Pro:** List/triage queries already run 9-10 correlated subqueries per row; two more indexed LIMIT 1 lookups are negligible with pagination capped at 100-200 rows
- **Con:** Two additional correlated subqueries per row (but hitting existing `(session_id, created_at)` index)

**Decision:** Option A — no migration, always consistent, and the pagination bounds make the performance cost negligible given the existing subquery load.

_Considered and rejected: Option B — denormalized columns (unnecessary migration + backfill for negligible perf gain), Option C — scalar subselect (same as A, just framed differently)._

---

## Q2: What information to surface

Decide which pieces of edit attribution to include in the API response.

### Option B: Actor + timestamp (`feedback_edited_by` + `feedback_edited_at`)

Return both the editor's identity and when the edit happened.

- **Pro:** Full context — "Edited by alice, 2 hours ago"
- **Pro:** Helps with audit/debugging without needing to open the activity log
- **Pro:** Timestamp is essentially free — same subquery row, just a different column select
- **Con:** One more field to plumb through

**Decision:** Option B — actor + timestamp gives enough context for the common case. Full history is already available via the activity API if needed.

_Considered and rejected: Option A — actor only (no temporal context), Option C — full edit history inline (overkill, activity endpoint already serves this)._

---

## Q3: Include in list endpoints or detail-only

The dashboard list and triage views already show `feedback_edited` as a boolean. Should the new fields also appear there?

### Option B: Both list and detail

Add the fields to `DashboardSessionItem` too, so the triage and list views have the data available when opening review modals.

- **Pro:** Review modals are opened from list/triage context — the data must already be loaded to display "edited by" in the modal without an extra API call
- **Pro:** Subquery cost is negligible (per Q1 analysis)
- **Con:** Slightly larger JSON payloads for list responses

**Decision:** Option B — the review modals can be opened from any context (session detail, triage view, dashboard list), so the data needs to be present in all response types. An extra fetch just to show "edited by" in the modal would be unnecessary latency.

_Considered and rejected: Option A — detail only (would require an extra API call when opening modals from list/triage views)._

---

## Q4: How to display the edit attribution in the UI

The current `ReviewModalHeader` shows `assignee` with a person icon and an "Edited" chip. We need to decide how to incorporate the editor identity. Display is in the review modals only (`CompleteReviewModal`, `EditFeedbackModal`).

### Option B: Separate "Edited by" line below assignee

Keep the "Edited" chip as-is but add a second line below the assignee line with an edit icon: "Edited by {actor}, {relative time}".

- **Pro:** Clear visual separation between reviewer (assignee) and editor
- **Pro:** Enough room for full detail without truncation
- **Con:** Takes slightly more vertical space in the modal header

**Decision:** Option B — separate line below the assignee gives clear distinction between who reviewed and who last edited. The "Edited" chip stays as a quick signal, and the line below provides the detail.

_Considered and rejected: Option A — enhanced chip (cramped with long usernames), Option C — replace assignee (loses original reviewer context)._
