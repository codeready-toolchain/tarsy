# ADR-0016: Triage Acknowledge Action

**Status:** Implemented
**Date:** 2026-05-13

## Overview

Sessions that complete investigation land in the "Needs Review" triage bucket. The only way to move them to "Reviewed" was via "Complete Review," which requires selecting a quality rating. When a reviewer has read the report but doesn't have enough information to judge quality, the session is stuck in the queue indefinitely.

This ADR introduces an **Acknowledge** action that moves a session from the review queue to "Reviewed" without assigning a quality rating — expressing "I've read this, but I can't or won't judge it."

## Design Principles

1. **No metric pollution** — Acknowledged sessions must not affect quality metrics (averages, distributions). `quality_rating` stays NULL.
2. **Lightweight UX** — Acknowledging requires fewer clicks than a full review. It's the "move along" escape hatch.
3. **Auditability** — The action is recorded in the review activity log (who acknowledged what and when).
4. **Reversible** — An acknowledged session can be reopened and fully reviewed later (existing `reopen` action works unchanged).
5. **Bulk-friendly** — Works with the existing bulk action infrastructure.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| 1 | UX approach | Inline "Ack" button on triage row, single click, no modal | Speed is the point. Undo snackbar handles mis-clicks. Modal adds clicks and conflates rating with non-rating. |
| 2 | Visual indicator in "Reviewed" bucket | Neutral grey chip with checkmark icon | Follows existing icon-only chip pattern. Visually distinct from colored quality chips. Clearly conveys "seen but not judged." |
| 3 | Optional note on acknowledge | No note — pure fire-and-forget | Keeps it maximally simple and truly one-click. The `note` field already exists on activity rows, so notes can be added later if needed. |
| 4 | Bulk action bar placement | Available in both `needs_review` and `in_progress` groups | Covers batch-sweep for both states. Only supporting `needs_review` would force an unclaim-first workaround for claimed sessions. |

## Architecture

### Data Flow

1. User clicks inline "Ack" button on a triage row (single click, no modal)
2. Dashboard sends `PATCH /api/v1/sessions/review` with `action: "acknowledge"`, `session_ids: [...]`
3. Handler validates (no `quality_rating` required for acknowledge)
4. Service transitions `review_status` → `reviewed`, sets `reviewed_at`, auto-claims if unclaimed
5. Activity row inserted with `action = "acknowledge"` (no note)
6. No memory confidence adjustment (NULL rating → skipped)
7. No feedback reflector triggered
8. WebSocket event published → UI updates in real-time
9. Undo snackbar shown (same pattern as claim/unclaim/reopen)

### State Transitions

```
needs_review → reviewed (auto-claim + acknowledge)
in_progress  → reviewed (acknowledge from claimed state)
```

The acknowledge action produces the same terminal state as `complete` (`review_status = reviewed`) but without a `quality_rating`. This matches the pattern already used for auto-reviewed cancelled sessions.

### Acknowledge vs Complete

| Aspect | Complete | Acknowledge |
|--------|----------|-------------|
| Quality rating | Required | Not set (NULL) |
| Feedback fields | Optional | None |
| Memory confidence | Adjusted | No adjustment |
| Feedback reflector | Triggered if feedback present | Not triggered |
| Terminal state | `reviewed` | `reviewed` |
| UX | Modal with rating selection | Single inline click |

### ReviewCell Display Logic

`ReviewCell` reads `review_status` from the session to distinguish "acknowledged (reviewed + no rating)" from "not yet reviewed." When `review_status === 'reviewed'` and `quality_rating` is null, it renders the neutral grey checkmark chip.

## Future Considerations

- **Optional notes**: The activity `note` field already exists, so an optional note on acknowledge can be added backward-compatibly if retrospective context becomes valuable.
- **Activity timeline rendering**: When the review activity timeline component is built, acknowledge entries should display with a neutral icon/color and no note or feedback fields.
