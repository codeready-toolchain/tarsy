# Show Who Edited Session Feedback

**Status:** Final

## Overview

When a reviewer edits feedback on a session (via the `update_feedback` action), the UI currently shows an "Edited" chip but still displays the original `assignee` as the only person associated with the feedback. There is no indication of *who* made the edit or *when*.

This design adds `feedback_edited_by` and `feedback_edited_at` fields so the review modals show who last modified the feedback and when.

## Design Principles

- **No migrations** — compute from the existing `session_review_activities` audit log
- **Consistency** — follow the existing `feedback_edited` computed-subquery pattern
- **Minimal UI disruption** — add a line to the existing modal header, don't restructure

## Architecture

### Data source

The `session_review_activities` table already records the `actor` and `created_at` for every `update_feedback` action. Two new correlated subqueries fetch the most recent edit's actor and timestamp:

```sql
(SELECT actor FROM session_review_activities
 WHERE session_id = ... AND action = 'update_feedback'
 ORDER BY created_at DESC LIMIT 1)

(SELECT created_at FROM session_review_activities
 WHERE session_id = ... AND action = 'update_feedback'
 ORDER BY created_at DESC LIMIT 1)
```

The existing `(session_id, created_at)` index covers the `session_id` filter and `created_at` sort; the `action = 'update_feedback'` predicate is applied as a filter after the index scan. With typically 0–3 activity rows per session, this is negligible. Pagination capped at 100–200 rows and 9–10 correlated subqueries already running per row make two more `LIMIT 1` lookups immaterial.

### Backend changes

**Go DTOs** (`pkg/models/session.go`):

Add two fields to both `DashboardSessionItem` and `SessionDetailResponse`:

```go
FeedbackEditedBy *string    `json:"feedback_edited_by"`
FeedbackEditedAt *time.Time `json:"feedback_edited_at"`
```

**List query** (`pkg/services/session_service.go`):

Add two `AppendSelectAs` subqueries alongside the existing `feedback_edited` one. Add corresponding fields to the `dashboardRow` scan struct.

**Triage query** (`pkg/services/session_service_review.go`):

Same pattern — add two subqueries and extend the `triageRow` scan struct.

**Detail query** (`pkg/services/session_service.go` — `GetSession`):

The detail endpoint currently uses an Ent `Exist` query for `feedback_edited`. Replace this with a `.Select("actor", "created_at").Where(...).Order(Desc(created_at)).First(ctx)` query that returns the latest `update_feedback` activity row. If found, populate all three fields (`feedback_edited = true`, `feedback_edited_by`, `feedback_edited_at`); if `ent.IsNotFound`, set `feedback_edited = false` with nil for the other two. This is a different pattern from the list/triage `AppendSelectAs` subqueries but is the idiomatic Ent approach for the single-session detail path.

### Frontend changes

**TypeScript types** (`web/dashboard/src/types/session.ts`):

Add to both `DashboardSessionItem` and `SessionDetailResponse`:

```typescript
feedback_edited_by?: string | null;
feedback_edited_at?: string | null;
```

**`ReviewModalHeader`** (`web/dashboard/src/components/dashboard/ReviewModalHeader.tsx`):

Add new props `feedbackEditedBy` and `feedbackEditedAt`. When present, render a second line below the assignee line:

```
[EditOutlined icon] Edited by {feedbackEditedBy}, {relative time}
```

Using the same styling pattern as the existing assignee line (small icon + `body2` + `text.secondary`). Use the existing `timeAgo()` utility from `web/dashboard/src/utils/format.ts` (wraps `date-fns` `formatDistanceToNow`) for the relative timestamp.

**`EditFeedbackModal`** and **`CompleteReviewModal`**:

Pass `feedbackEditedBy` and `feedbackEditedAt` through to `ReviewModalHeader`.

**Callers** (`SessionDetailPage`, `DashboardView`, `TriageView`):

Pass the new session fields into the modal props.

### Event streaming

No changes needed. The `ReviewStatusPayload` already includes `Actor`. The dashboard refetches session data on `review.status` events, so the new computed fields are picked up automatically after an edit.

## Implementation Plan

1. **Go models** — add `FeedbackEditedBy` / `FeedbackEditedAt` to DTOs and scan structs
2. **Go queries** — add two correlated subqueries to list, triage, and detail queries
3. **TypeScript types** — add fields to session interfaces
4. **ReviewModalHeader** — add "Edited by" line with relative timestamp
5. **Modal + caller plumbing** — pass new props through `EditFeedbackModal`, `CompleteReviewModal`, `SessionDetailPage`, `DashboardView`, `TriageView`
6. **Tests** — update Go service tests to assert new fields; update E2E golden files (new fields will be `null` in goldens since no E2E test exercises `update_feedback`)
