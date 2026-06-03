# Move Acknowledge into Review Dialog

**Status:** Final  
**Related:** [Questions & decisions](ack-in-review-dialog-questions.md)

## Overview

Currently, "Acknowledge" (ack) and "Review" are two separate UX paths in the triage view:

- **Ack** — inline icon button on the triage row, single click, no modal, no quality rating
- **Review** — opens `CompleteReviewModal` with a 3-way quality rating plus optional feedback fields

This proposal consolidates both into the review dialog. Every review interaction opens the same modal, which now includes a fourth "Acknowledge" option alongside the three quality ratings. The standalone "Ack" button is removed from the triage row.

## Design Principles

1. **Single review entry point** — All review decisions happen in one place (the review dialog). Reduces cognitive load.
2. **Preserve ack semantics** — Acknowledge still means "I've seen this, no quality judgment." No metric pollution, no confidence adjustment.
3. **Minimal backend changes** — The existing `acknowledge` action on `PATCH /sessions/review` stays unchanged. One backend addition: memory confidence is properly reversed when a rating is changed or removed.
4. **Consistent across surfaces** — The same modal and options are available in triage, session list, session detail, and FinalAnalysisCard.

## Architecture

### Modal Layout

The `CompleteReviewModal` gains a fourth radio option in the existing `RadioGroup`:

| Value | Label | Icon | Description |
|-------|-------|------|-------------|
| `accurate` | Accurate | ThumbUp (green) | The investigation correctly identified the issue and root cause |
| `partially_accurate` | Partially Accurate | ThumbsUpDown (warning) | Some findings were correct but missed key aspects |
| `inaccurate` | Inaccurate | ThumbDown (red) | The investigation was wrong or misleading |
| *(new)* `acknowledge` | Acknowledge | DoneAll (neutral) | I've reviewed this but won't judge investigation quality |

**Conditional UI:** When "Acknowledge" is selected, feedback fields (action taken, investigation feedback) collapse/hide and the submit button relabels to "Acknowledge" (instead of "Complete Review").

**Note on the `acknowledge` value:** This is NOT added to the `QUALITY_RATING` constant (since it's semantically not a quality rating). Instead, it uses a separate sentinel (e.g., `REVIEW_SELECTION.ACKNOWLEDGE = 'acknowledge'`) that the handler detects to dispatch `action: "acknowledge"` instead of `action: "complete"`. The radio group mixes true quality ratings with this action sentinel in the UI only.

### ReviewCell for Acknowledged Sessions

Currently, `ReviewCell` renders acknowledged sessions (reviewed + null `quality_rating`) as a non-interactive grey checkmark chip (`tabIndex={-1}`, `cursor: 'default'`). This changes to be clickable — clicking opens `CompleteReviewModal` so the user can upgrade from acknowledged to a quality rating (or re-acknowledge).

The `getReviewModalMode` function in `types/api.ts` currently routes all `reviewed` sessions to EDIT mode. This must be updated to check `quality_rating`:
- `reviewed` + has `quality_rating` → EDIT mode (existing `EditFeedbackModal`)
- `reviewed` + null `quality_rating` (acknowledged) → COMPLETE mode (so user can rate or re-ack)

### Bulk Actions

The toolbar's separate "Ack All" and "Complete All" buttons merge into a single **"Review All"** button that opens the same modal (with all 4 options). The chosen action applies to all selected sessions.

### FinalAnalysisCard

A neutral acknowledge icon (e.g., `DoneAll`) is added alongside the existing inline thumb up/down shortcuts. Clicking it opens the review modal with "Acknowledge" pre-selected (`initialRating="acknowledge"`).

### Auto-Claim Behavior

No change. The backend already auto-claims on submit (not on modal open) for both `doComplete` and `doAcknowledge`. This remains unchanged.

### Snackbars (Triage View)

Existing differentiated snackbars remain, wired to the modal outcome:
- Acknowledge → "Acknowledged" + Undo
- Quality rating → rating chip + "Add note" + Undo

### Data Flow

1. User clicks ReviewCell chip (triage/session list) or inline icon (FinalAnalysisCard) → modal opens
2. User selects one of four options
3. If **Acknowledge** selected:
   - Feedback fields hidden
   - Submit button: "Acknowledge"
   - API call: `PATCH /sessions/review` with `action: "acknowledge"`
4. If **quality rating** selected:
   - Feedback fields visible (optional)
   - Submit button: "Complete Review"
   - API call: `PATCH /sessions/review` with `action: "complete"`, `quality_rating: "..."`
5. Backend handles auto-claim if session was in `needs_review`
6. WebSocket event published → UI updates

### Backend

The `acknowledge` and `complete` actions already exist on the same endpoint — no new API routes needed. However, one backend change is required:

**Memory confidence reset on rating change.** When a session's quality rating changes (re-rated or downgraded to acknowledge), the previous confidence adjustment must be reversed before applying the new one. New method in `pkg/memory/service.go`:

```
ReadjustConfidenceForRatingChange(ctx, project, sessionID, oldRating, newRating *QualityRating)
```

Logic:
1. **Reverse old rating:**
   - `accurate` → `confidence = confidence / 1.2` (approximate reversal; LEAST cap makes it imprecise for values that hit 1.0)
   - `partially_accurate` → `confidence = confidence / 0.6`
   - `inaccurate` → `deprecated = false` (confidence value was never changed)
2. **Apply new rating (if non-nil):** same existing multipliers
3. **If new rating is nil (acknowledge):** just reverse, done

The handler (`handler_review.go`) is updated to pass old + new rating to this method on `update_feedback` and `acknowledge` actions where the session previously had a rating.

## Affected Components

| Component | Change |
|-----------|--------|
| `CompleteReviewModal` | Add "Acknowledge" radio option with description; conditional field collapse; dynamic submit button label |
| `TriageSessionRow` | Remove ack `IconButton` from actions column |
| `TriageGroupedList` | Remove "Ack All" button; rename "Complete All" → "Review All" |
| `TriageView` | Remove `handleAcknowledge`; route modal ack through `handleReviewComplete`; update `handleBulkCompleteConfirm` to dispatch `acknowledge` action when selection is "acknowledge" |
| `DashboardView` | Remove `onAcknowledge` / `onBulkAcknowledge` prop threading; update `handleTriageComplete` / `handleBulkTriageComplete` to detect "acknowledge" selection and call `updateReview` with `action: "acknowledge"` instead of `action: "complete"` |
| `FinalAnalysisCard` | Add acknowledge icon (`DoneAll`) in the "Helpful?" pill alongside existing thumbs |
| `EditFeedbackModal` | Add "Acknowledge" option (same as `CompleteReviewModal`); allow downgrading from rated → acknowledged |
| `ReviewCell` | Make acknowledged sessions clickable (currently non-interactive: `tabIndex={-1}`, `cursor: 'default'`) so users can upgrade ack → rated |
| `types/api.ts` | Update `getReviewModalMode` to route acknowledged sessions (reviewed + no `quality_rating`) to COMPLETE mode instead of EDIT |

## Implementation Plan

Two PRs, each independently shippable.

### PR 1: Backend — Memory confidence reset on rating change - DONE

Fixes a pre-existing bug where re-rating a session applies the new adjustment without reversing the old one. Also enables the upcoming "downgrade to acknowledge" flow.

- Add `ReadjustConfidenceForRatingChange(ctx, project, sessionID, oldRating, newRating *QualityRating)` to `pkg/memory/service.go`
- Update `handler_review.go` to call it when a session's rating changes (on `update_feedback` where rating differs, and on `acknowledge` where session previously had a rating)
- The service layer already reads the current session state in `doUpdateFeedback`; extend `doAcknowledge` similarly to capture old rating
- Unit tests for all transitions: accurate→inaccurate, accurate→ack, inaccurate→accurate, partially→ack, etc.

### PR 2: Frontend — Consolidate acknowledge into review dialog

All frontend changes as a single cohesive UX refactor.

**Add acknowledge to CompleteReviewModal:**
- Add a `REVIEW_SELECTION` constant with an `ACKNOWLEDGE` value for the radio group
- Add fourth radio option with `DoneAll` icon and descriptive subtitle
- Conditionally hide/collapse feedback fields when "Acknowledge" is selected
- Dynamic submit button label: "Acknowledge" vs "Complete Review"
- Update `onComplete` callback to handle the acknowledge value (callers detect it and dispatch `action: "acknowledge"` instead of `action: "complete"`)

**Routing and ReviewCell:**
- Update `getReviewModalMode` to route acknowledged sessions (reviewed + no quality_rating) to COMPLETE mode
- Make `ReviewCell` clickable for acknowledged sessions

**Remove standalone ack:**
- Remove ack `IconButton` from `TriageSessionRow` (both `needs_review` and `in_progress` groups)
- Remove `onAcknowledge` / `onBulkAcknowledge` handler chain and props through `TriageView` → `DashboardView`

**Consolidate bulk actions:**
- Remove "Ack All" toolbar button from `TriageGroupedList`
- Rename "Complete All" → "Review All"

**FinalAnalysisCard:**
- Add `DoneAll` icon button in the "Helpful?" pill (rightmost, after thumb buttons)
- Pass `initialRating="acknowledge"` when opening the review modal from it

**EditFeedbackModal:**
- Add "Acknowledge" as a radio option (same UI as `CompleteReviewModal`)
- When switching from a quality rating to "Acknowledge", warn that this removes the rating
- Update `onSave` handler to dispatch `action: "acknowledge"` when selected

**Tests & cleanup:**
- Update E2E tests in `test/e2e/review_workflow_test.go`
- Update/remove frontend tests for the old ack button
- Add tests for: acknowledge option in modal, conditional field collapse, bulk "Review All", acknowledged → rated upgrade via ReviewCell
- Update ADR-0016 to note the UX consolidation
