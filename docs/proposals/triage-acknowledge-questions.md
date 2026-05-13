**Status:** Resolved — all decisions made
**Related:** [Design document](triage-acknowledge-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: UX approach for the acknowledge action

The acknowledge action needs a place in the UI. The current triage row has: an inline "Claim" button (needs_review group), "Unclaim" icon (in_progress group), "Reopen" icon (reviewed group), and a review-column click that opens the CompleteReviewModal. How should acknowledge be surfaced?

### Option A: Inline button on triage row (no modal)

Add an "Ack" button directly on the row in the `needs_review` and `in_progress` groups, next to Claim/Unclaim. One click and it's done.

- **Pro:** Fastest possible interaction — single click, no modal, no decisions required
- **Pro:** Clearly distinct from "Complete Review" which involves the modal + rating
- **Con:** Another button in the action area — could feel cluttered, especially next to "Claim"
- **Con:** Easy to mis-click with no confirmation (mitigated by undo snackbar)

**Decision:** Option A — inline button, one click, no modal. Speed is the point. Undo snackbar handles mis-clicks.

_Considered and rejected: Option B (modal adds clicks, conflates rating with non-rating), Option C (popover adds complexity for marginal benefit)_

---

## Q2: Visual indicator for acknowledged sessions in the "Reviewed" bucket

Currently, sessions in the "Reviewed" group show a colored chip (thumb up/down/up-down) via `ReviewCell` when they have a `quality_rating`. Acknowledged sessions have NULL `quality_rating`, so they'd show nothing. We need a way to distinguish "acknowledged but not rated" from a rendering gap.

### Option A: Subtle neutral chip (e.g., checkmark icon, grey/default color)

Show a small chip with a neutral color (grey/default) and a checkmark icon in the review column.

- **Pro:** Clear at a glance that someone dealt with this session
- **Pro:** Visually distinct from the colored quality chips
- **Con:** Adds a new visual element to learn

**Decision:** Option A — neutral grey chip with checkmark icon. Follows existing icon-only chip pattern, immediately conveys "seen but not judged."

_Considered and rejected: Option B (no indicator — can't distinguish ack'd from rendering bug), Option C (text takes more space than icon chip)_

---

## Q3: Should acknowledge support an optional note?

When acknowledging, should the user be able to attach a short note explaining why they're not rating (e.g., "alert was noise", "can't tell without more context", "duplicate of SESS-xyz")?

### Option A: No note — pure fire-and-forget

Acknowledge is just a status transition. No text fields.

- **Pro:** Maximally simple — both UX and backend
- **Pro:** Keeps the action truly one-click (no modal or popover needed)
- **Con:** Loses context about _why_ someone didn't rate — useful for retrospectives

**Decision:** Option A — no note. Start simple, keep it one-click. The `note` field already exists on activity rows so adding optional notes later is backward-compatible if the need arises.

_Considered and rejected: Option B (slows down interaction, defeats the speed goal), Option C (inconsistent single vs bulk behavior)_

---

## Q4: Bulk action bar placement

The triage view already has bulk actions (Claim, Complete, Unclaim, Reopen) that appear in a top bar when sessions are selected. Where does "Acknowledge" appear?

### Option A: In the needs_review and in_progress bulk bars

Add an "Acknowledge" button alongside the existing bulk actions for those groups. Same pattern: select rows → bar appears → click Acknowledge.

- **Pro:** Consistent with existing bulk patterns
- **Pro:** Enables efficient sweep of multiple noise sessions
- **Con:** More buttons in the bar (needs_review already has Claim + Complete)

**Decision:** Option A — bulk acknowledge available in both `needs_review` and `in_progress` groups. Covers the common batch-sweep case for both states.

_Considered and rejected: Option B (only needs_review — forces unclaim-first workaround for claimed sessions)_
