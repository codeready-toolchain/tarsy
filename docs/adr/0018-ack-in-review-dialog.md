# ADR-0018: Move Acknowledge into Review Dialog

**Status:** Implemented
**Date:** 2026-06-02

## Overview

Prior to this change, "Acknowledge" and "Review" were two separate UX paths in the triage view:

- **Ack** — inline icon button on the triage row, single click, no modal, no quality rating
- **Review** — opens a modal with a 3-way quality rating plus optional feedback fields

This consolidates both into the review dialog. Every review interaction opens the same modal, which now includes a fourth "Acknowledge" option alongside the three quality ratings. The standalone "Ack" button is removed from the triage row.

Extends [ADR-0016: Triage Acknowledge Action](0016-triage-acknowledge.md) which introduced the acknowledge action itself.

## Design Principles

1. **Single review entry point** — All review decisions happen in one place (the review dialog). Reduces cognitive load.
2. **Preserve ack semantics** — Acknowledge still means "I've seen this, no quality judgment." No metric pollution, no confidence adjustment.
3. **Minimal backend changes** — The existing `acknowledge` action on `PATCH /sessions/review` stays unchanged. Backend additions: memory confidence reversal on rating change, and `reviewed → reviewed` transitions for upgrading/downgrading.
4. **Consistent across surfaces** — The same modal and options are available in triage, session list, session detail, and FinalAnalysisCard.

## Decisions

| # | Decision | Choice | Rationale |
|---|----------|--------|-----------|
| 1 | How to present "Acknowledge" in the review dialog | Fourth radio option with conditional UI (collapse fields, relabel button) | Single selection mechanism avoids dual-submit confusion; adaptive UI makes the lightweight path obvious. Rejected: flat fourth radio (conflates rating with non-rating), separate section with own button (dual-submit paths confuse users). |
| 2 | Bulk acknowledge UX | Merge "Ack All" and "Complete All" into single "Review All" button opening the modal | Reduces toolbar buttons; consistent with per-session flow. Only net cost is one extra click for bulk-ack. |
| 3 | Auto-claim on modal submit | No change (backend already auto-claims on submit, not on modal open) | Existing behavior for both `doComplete` and `doAcknowledge` is correct as-is. |
| 4 | Snackbar differentiation | Keep existing differentiated snackbars, wired to modal outcome | "Acknowledged" + Undo for ack; rating chip + "Add note" + Undo for complete. No behavioral change needed. |
| 5 | FinalAnalysisCard shortcut | Add acknowledge icon (`DoneAll`) alongside existing thumbs | Consistency — acknowledge should be available everywhere review is. Quick path for detail-page reviewers. |

## Architecture

### Modal Layout

The review modal uses a `RadioGroup` with four options:

| Value | Label | Icon | Description |
|-------|-------|------|-------------|
| `accurate` | Accurate | ThumbUp (green) | The investigation correctly identified the issue and root cause |
| `partially_accurate` | Partially Accurate | ThumbsUpDown (warning) | Some findings were correct but missed key aspects |
| `inaccurate` | Inaccurate | ThumbDown (red) | The investigation was wrong or misleading |
| `acknowledge` | Acknowledge | DoneAll (neutral) | I've reviewed this but won't judge investigation quality |

When "Acknowledge" is selected:
- Feedback fields (action taken, investigation feedback) collapse/hide
- Submit button relabels to "Acknowledge" (instead of "Complete Review")
- Hidden field values are NOT submitted (cleared before dispatch)

The `acknowledge` value is NOT part of the `QUALITY_RATING` constant (semantically not a quality rating). A separate `REVIEW_SELECTION` constant combines the three ratings with the acknowledge sentinel. The handler detects it and dispatches `action: "acknowledge"` instead of `action: "complete"`.

### ReviewCell Clickability

Acknowledged sessions (reviewed + null `quality_rating`) become clickable in `ReviewCell`. Clicking opens the review modal in COMPLETE mode so the user can upgrade to a quality rating (or re-acknowledge).

Modal mode routing logic:
- `reviewed` + has `quality_rating` → EDIT mode (existing edit-feedback modal)
- `reviewed` + null `quality_rating` (acknowledged) → COMPLETE mode

### Backend Transitions

Two new state transitions support upgrading and downgrading:

| From State | Action | To State | Effect |
|------------|--------|----------|--------|
| reviewed (acknowledged, no rating) | `complete` | reviewed (rated) | Sets quality_rating, action_taken, feedback |
| reviewed (rated) | `acknowledge` | reviewed (acknowledged) | Clears quality_rating, action_taken, feedback |

### Memory Confidence Reset

When a session's quality rating changes (re-rated or downgraded to acknowledge), the previous confidence adjustment is reversed before applying the new one:

1. **Reverse old rating** — undo the multiplier applied by the previous rating
2. **Apply new rating** (if non-nil) — apply the new multiplier
3. **If new rating is nil** (acknowledge) — just reverse, done

### Data Flow

1. User clicks ReviewCell chip or inline icon → modal opens
2. User selects one of four options
3. If **Acknowledge**: feedback fields hidden; API dispatches `action: "acknowledge"`
4. If **quality rating**: feedback fields visible; API dispatches `action: "complete"` with `quality_rating`
5. Backend handles auto-claim if session was in `needs_review`
6. WebSocket event published → UI updates

## Future Considerations

- The "one extra click" cost for bulk-ack could be mitigated with keyboard shortcuts in the modal
- If acknowledge usage grows significantly, a dedicated keyboard shortcut (e.g., `A` key) in triage view could bypass the modal entirely
