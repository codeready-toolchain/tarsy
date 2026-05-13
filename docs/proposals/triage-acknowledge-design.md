# Triage Acknowledge Action

**Status:** Final
**Decisions:** [triage-acknowledge-questions.md](triage-acknowledge-questions.md)

## Overview

Sessions that complete investigation land in the "Needs Review" triage bucket. Currently, the only way to move them to "Reviewed" is via the "Complete Review" action, which requires selecting a quality rating (accurate / partially accurate / inaccurate). When a reviewer has read the report but doesn't have enough information to judge quality, the session is stuck in the queue indefinitely.

This proposal introduces an **Acknowledge** action that moves a session from the review queue to "Reviewed" without assigning a quality rating — expressing "I've read this, but I can't or won't judge it."

## Design Principles

1. **No metric pollution** — Acknowledged sessions must not affect quality metrics (averages, distributions). `quality_rating` stays NULL.
2. **Lightweight UX** — Acknowledging should require fewer clicks than a full review. It's the "move along" escape hatch.
3. **Auditability** — The action must be recorded in the review activity log so you can see who acknowledged what and when.
4. **Reversible** — An acknowledged session can be reopened and fully reviewed later (existing `reopen` action works unchanged).
5. **Bulk-friendly** — Must work with the existing bulk action infrastructure.

## Architecture / How It Works

### Data flow

1. User clicks inline "Ack" button on a triage row (single click, no modal)
2. Dashboard sends `PATCH /api/v1/sessions/review` with `action: "acknowledge"`, `session_ids: [...]`
3. Handler validates (no `quality_rating` required for acknowledge)
4. Service transitions `review_status` → `reviewed`, sets `reviewed_at`, auto-claims if unclaimed
5. Activity row inserted with `action = "acknowledge"` (no note — fire-and-forget)
6. No memory confidence adjustment (NULL rating → handler skips)
7. No feedback reflector triggered
8. WebSocket event published (existing pattern) → UI updates in real-time
9. Undo snackbar shown (same pattern as claim/unclaim/reopen)

### State transitions

```
needs_review → reviewed (auto-claim + acknowledge)
in_progress  → reviewed (acknowledge from claimed state)
```

The acknowledge action produces the same terminal state as `complete` (`review_status = reviewed`) but without a `quality_rating`. This matches the pattern already used for auto-reviewed cancelled sessions.

## Core Concepts

### Acknowledge vs Complete

| Aspect | Complete | Acknowledge |
|--------|----------|-------------|
| Quality rating | Required | Not set (NULL) |
| Feedback fields | Optional | None |
| Memory confidence | Adjusted | No adjustment |
| Feedback reflector | Triggered if feedback present | Not triggered |
| Terminal state | `reviewed` | `reviewed` |
| UX | Modal with rating selection | Single inline click |

### Visual indicator in "Reviewed" bucket

Acknowledged sessions show a neutral grey chip with a checkmark icon (`CheckCircle` or `DoneAll`) in the review column. This follows the existing icon-only chip pattern but is visually distinct from the colored quality rating chips (green/yellow/red).

## Implementation Plan

### Phase 1: Backend

1. Add `ReviewActionAcknowledge = "acknowledge"` to `pkg/models/session.go` + update `ValidReviewAction`
2. Add `"acknowledge"` to `sessionreviewactivity.action` enum in `ent/schema/sessionreviewactivity.go`
3. Run `make ent-generate` (generates Go constants for the new enum value)
4. Create DB migration (`make migrate-create NAME=add_acknowledge_action`) — Atlas derives the ALTER TYPE from the schema diff
5. Add `doAcknowledge` method in `pkg/services/session_service_review.go`:
   - Same transition logic as `doComplete` but without `SetQualityRating`
   - Handles both `needs_review` (auto-claim) and `in_progress` states
6. Wire into `updateSingleReview` switch + add validation (no quality_rating required)
7. Add unit tests + E2E test (`test/e2e/`) covering the acknowledge action

### Phase 2: Dashboard

1. Add `ACKNOWLEDGE: 'acknowledge'` to `REVIEW_ACTION` in `web/dashboard/src/types/api.ts`
2. Add inline "Ack" button (tooltip: "Acknowledge") to `TriageSessionRow` in `needs_review` and `in_progress` groups (next to Claim/Unclaim)
3. Thread `onAcknowledge` prop through the chain: `DashboardView` → `TriageView` → `TriageGroupedList` → `TriageSessionRow`
4. Wire `handleTriageAcknowledge` / `handleBulkTriageAcknowledge` in `DashboardView` → calls `updateReview` with `action: "acknowledge"`
5. Add undo snackbar support — the existing snackbar already handles NULL `completedRating` gracefully (`getRatingConfig(undefined)` returns no icon), so no special case needed
6. Update `ReviewCell` to show neutral grey checkmark chip for acknowledged sessions:
   - `ReviewCell` currently only reads `quality_rating`. It needs `review_status` from the session to distinguish "acknowledged (reviewed + no rating)" from "not yet reviewed." The `DashboardSessionItem` type already includes `review_status`, so no new data is needed — just a conditional branch when `review_status === 'reviewed'` and `quality_rating` is null.
7. Add bulk "Ack" button to `TriageGroupedList` bulk action bar for both `needs_review` and `in_progress` groups (same prop pattern as `onBulkClaim` / `onBulkComplete`)

### Phase 3: Activity log

No dashboard component currently renders the review activity timeline (the `getReviewActivity` API and types exist but are unconsumed). This phase is deferred until that component is built. When it is:

1. Display "Acknowledged" with neutral icon/color
2. No note or feedback fields shown for acknowledge activity rows
