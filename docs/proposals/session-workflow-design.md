# Session Workflow — Detailed Design

**Status:** Draft — pending decisions from [session-workflow-design-questions.md](session-workflow-design-questions.md)
**Sketch:** [session-workflow-sketch.md](session-workflow-sketch.md)

## Overview

TARSy automates incident investigation but has no human workflow after the AI finishes. This design adds a lightweight review lifecycle on top of the existing investigation lifecycle, giving SRE teams an action-oriented "Triage" view alongside the current session list.

Sketch decisions (from [session-workflow-questions.md](session-workflow-questions.md)):

- **Data model**: `review_status` + `assignee` fields on `alert_sessions` (fast queries) + `session_review_activity` table (history/feedback).
- **States**: `needs_review` → `in_progress` → `resolved`, with `resolution_reason` (`actioned` / `dismissed`) on resolve.
- **Entry**: All terminal sessions auto-enter as `needs_review`. Cancelled sessions auto-resolve as `dismissed`. Active investigations appear as a virtual "Investigating" column.
- **Assignment**: Self-claim only, using `X-Forwarded-User` header value.
- **Dashboard**: "Sessions" | "Triage" tabs. Triage has grouped list + Kanban sub-layouts.
- **Interactions**: Contextual action buttons (both views) + drag-and-drop (Kanban) + keyboard shortcuts.

## Design Principles

1. **Additive, not disruptive.** The existing session list, status model, API, and WebSocket events remain unchanged. Teams that don't use the workflow see no difference.
2. **Fast queries for the workflow view.** Denormalized `review_status` and `assignee` on the session avoid JOINs for list/filter/group operations.
3. **Auditable.** Every workflow transition is logged in the activity table with actor, timestamp, from/to state.
4. **Real-time.** Workflow state changes propagate via WebSocket so all users see the same board state.
5. **Consistent patterns.** Follow existing TARSy conventions: Ent schema for DB, service layer for business logic, Echo handlers for API, event publisher for WebSocket, MUI for frontend.

## Architecture

### Data Flow

```
Worker completes investigation
  → SessionService.UpdateSessionStatus(completed/failed/timed_out)
  → SessionService sets review_status = needs_review (or resolved+dismissed for cancelled)
  → EventPublisher.PublishReviewStatus (persistent, GlobalSessionsChannel)
  → Frontend Triage view updates via WebSocket

SRE clicks "Claim"
  → PATCH /api/v1/sessions/:id/review {action: "claim"}
  → SessionService.UpdateReviewStatus (sets assignee, review_status = in_progress)
  → Inserts session_review_activity row
  → EventPublisher.PublishReviewStatus
  → Frontend Triage view updates

SRE clicks "Resolve"
  → PATCH /api/v1/sessions/:id/review {action: "resolve", resolution_reason: "actioned", note: "..."}
  → SessionService.UpdateReviewStatus (sets resolved_at, resolution_reason, review_status = resolved)
  → Inserts session_review_activity row
  → EventPublisher.PublishReviewStatus
  → Frontend Triage view updates
```

### Component Overview

| Layer | Component | Changes |
|---|---|---|
| **Schema** | `ent/schema/alertsession.go` | Add `review_status`, `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` fields |
| **Schema** | `ent/schema/sessionreviewactivity.go` | New entity |
| **Migration** | `pkg/database/migrations/` | Add columns to `alert_sessions`, create `session_review_activity` table, backfill existing terminal sessions |
| **Service** | `pkg/services/session_service.go` | Add `UpdateReviewStatus`, `GetReviewActivity` methods; modify `UpdateSessionStatus` to set `review_status` on terminal transitions |
| **Models** | `pkg/models/session.go` | Add review fields to DTOs, add request/response types |
| **API** | `pkg/api/handler_review.go` | New handler for `PATCH /sessions/:id/review`, `GET /sessions/:id/review-activity` |
| **API** | `pkg/api/server.go` | Register new routes |
| **Events** | `pkg/events/types.go`, `payloads.go`, `publisher.go` | Add `review.status` event type and payload |
| **Frontend** | Dashboard components | Tab bar, Triage view, Kanban board, grouped list, action buttons, resolve modal |

## Database Schema

### AlertSession changes

New fields on the existing `alert_sessions` table:

```go
field.Enum("review_status").
    Values("needs_review", "in_progress", "resolved").
    Optional().
    Nillable().
    Comment("Human review workflow state — NULL while investigation is active"),

field.String("assignee").
    Optional().
    Nillable().
    Comment("User who claimed this session for review (X-Forwarded-User value)"),

field.Time("assigned_at").
    Optional().
    Nillable().
    Comment("When the session was claimed"),

field.Time("resolved_at").
    Optional().
    Nillable().
    Comment("When review_status transitioned to resolved"),

field.Enum("resolution_reason").
    Values("actioned", "dismissed").
    Optional().
    Nillable().
    Comment("Why the session was resolved"),

field.Text("resolution_note").
    Optional().
    Nillable().
    Comment("Free-text context on resolution"),
```

New indexes:

```go
index.Fields("review_status"),
index.Fields("review_status", "assignee"),
index.Fields("assignee"),
```

### SessionReviewActivity entity

New `ent/schema/sessionreviewactivity.go`:

```go
type SessionReviewActivity struct {
    ent.Schema
}

func (SessionReviewActivity) Fields() []ent.Field {
    return []ent.Field{
        field.String("id").
            StorageKey("activity_id").
            Unique().
            Immutable(),
        field.String("session_id").
            Immutable(),
        field.String("actor").
            Comment("User who performed the action (X-Forwarded-User)"),
        field.Enum("action").
            Values("claim", "unclaim", "resolve", "reopen").
            Comment("What happened"),
        field.Enum("from_status").
            Values("needs_review", "in_progress", "resolved").
            Optional().
            Nillable().
            Comment("Review status before transition"),
        field.Enum("to_status").
            Values("needs_review", "in_progress", "resolved").
            Comment("Review status after transition"),
        field.Enum("resolution_reason").
            Values("actioned", "dismissed").
            Optional().
            Nillable().
            Comment("Set when action is resolve"),
        field.Text("note").
            Optional().
            Nillable().
            Comment("Free-text context"),
        field.Time("created_at").
            Default(time.Now).
            Immutable(),
    }
}

func (SessionReviewActivity) Edges() []ent.Edge {
    return []ent.Edge{
        edge.From("session", AlertSession.Type).
            Ref("review_activities").
            Field("session_id").
            Unique().
            Required().
            Immutable(),
    }
}

func (SessionReviewActivity) Indexes() []ent.Index {
    return []ent.Index{
        index.Fields("session_id", "created_at"),
    }
}
```

### Migration

> **Open question:** How should existing terminal sessions be backfilled? See [questions document](session-workflow-design-questions.md), Q1.

## Service Layer

### UpdateSessionStatus — hook for automatic review_status

Modify the existing `UpdateSessionStatus` in `pkg/services/session_service.go` to set `review_status` when a session reaches a terminal state:

```go
func (s *SessionService) UpdateSessionStatus(_ context.Context, sessionID string, status alertsession.Status) error {
    // Background context for writes — intentional TARSy pattern.
    // DB writes must complete even if the caller's context is cancelled
    // (e.g., HTTP client disconnect, worker shutdown).
    writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Load current session to check existing review state.
    // Only initialize review fields on the FIRST transition into a terminal state.
    // Late/duplicate worker writes must not overwrite an existing claimed/resolved workflow.
    session, err := s.client.AlertSession.Get(writeCtx, sessionID)
    if err != nil {
        return fmt.Errorf("failed to load session: %w", err)
    }

    update := s.client.AlertSession.UpdateOneID(sessionID).
        SetStatus(status).
        SetLastInteractionAt(time.Now())

    isTerminal := status == alertsession.StatusCompleted ||
        status == alertsession.StatusFailed ||
        status == alertsession.StatusTimedOut
    isCancelled := status == alertsession.StatusCancelled
    alreadyHasReviewStatus := session.ReviewStatus != nil

    if isTerminal || isCancelled {
        update = update.SetCompletedAt(time.Now())
    }

    if isTerminal && !alreadyHasReviewStatus {
        update = update.SetReviewStatus(alertsession.ReviewStatusNeedsReview)
    }

    if isCancelled && !alreadyHasReviewStatus {
        now := time.Now()
        update = update.
            SetReviewStatus(alertsession.ReviewStatusResolved).
            SetResolvedAt(now).
            SetResolutionReason(alertsession.ResolutionReasonDismissed)
    }

    return update.Exec(writeCtx)
}
```

### UpdateReviewStatus — new method

```go
type UpdateReviewRequest struct {
    Action           string  // "claim", "unclaim", "resolve", "reopen"
    Actor            string  // from extractAuthor
    ResolutionReason *string // required for "resolve"
    Note             *string // optional
}

func (s *SessionService) UpdateReviewStatus(ctx context.Context, sessionID string, req UpdateReviewRequest) error
```

This method uses an atomic compare-and-transition pattern to prevent concurrent race conditions (e.g., two users claiming the same session simultaneously):

1. Begin a transaction (`s.client.Tx(writeCtx)`)
2. Perform a conditional UPDATE on the sessions row using the expected current `review_status` (and `assignee` where relevant) as WHERE predicates:
   ```sql
   UPDATE alert_sessions
   SET review_status = ?, assignee = ?, assigned_at = ?, ...
   WHERE session_id = ? AND review_status = ? [AND assignee = ?]
   ```
3. Check rows-affected count — if zero, the session's state has changed since the caller read it. Rollback and return `409 Conflict`.
4. Insert the `SessionReviewActivity` record within the same transaction (only after a successful conditional update).
5. Commit the transaction. All field updates (`review_status`, `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note`) and the activity log insert are atomic.
6. Return the updated session.

This ensures concurrent claims/resolves cannot both succeed — only the first writer wins, the second gets a conflict error.

> **Open question:** Should claiming an already-claimed session be allowed (reassignment)? See [questions document](session-workflow-design-questions.md), Q2.

### Transition validation

Valid state transitions:

| Action | From | To | Sets |
|---|---|---|---|
| `claim` | `needs_review` | `in_progress` | `assignee`, `assigned_at` |
| `unclaim` | `in_progress` | `needs_review` | clears `assignee`, `assigned_at` |
| `resolve` | `in_progress` | `resolved` | `resolved_at`, `resolution_reason`, `resolution_note` |
| `reopen` | `resolved` | `needs_review` | clears `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` |

> **Open question:** Should "resolve" be allowed directly from `needs_review` (skip claiming)? See [questions document](session-workflow-design-questions.md), Q3.

### GetReviewActivity

```go
func (s *SessionService) GetReviewActivity(ctx context.Context, sessionID string) ([]*ent.SessionReviewActivity, error)
```

Returns all activity records for a session, ordered by `created_at` ascending.

### ListSessions / ListSessionsForDashboard extensions

Add `review_status` and `assignee` to the existing filter parameters and response DTOs:

- `DashboardListParams`: add `ReviewStatus []string`, `Assignee string`
- `DashboardSessionItem`: add `ReviewStatus *string`, `Assignee *string`, `ResolutionReason *string`

> **Open question:** Does the Triage view need its own dedicated API endpoint, or can it reuse the existing list endpoint with added filters? See [questions document](session-workflow-design-questions.md), Q4.

## API

### PATCH /api/v1/sessions/:id/review

Single endpoint for all workflow transitions.

**Request body:**

```json
{
    "action": "claim" | "unclaim" | "resolve" | "reopen",
    "resolution_reason": "actioned" | "dismissed",
    "note": "optional free text"
}
```

**Responses:**

| Status | When |
|---|---|
| `200 OK` | Transition succeeded. Returns updated session review fields. |
| `400 Bad Request` | Invalid action, missing required fields (e.g., `resolution_reason` for resolve). |
| `404 Not Found` | Session doesn't exist. |
| `409 Conflict` | Invalid transition (e.g., resolve from `needs_review` if disallowed, claim already-claimed session). |

**Auth:** `extractAuthor` provides the actor identity from the `X-Forwarded-User` header (set by oauth2-proxy or kube-rbac-proxy running as colocated sidecars). **Deployment requirement:** the ingress must strip any client-supplied `X-Forwarded-User` header (e.g., `proxy_set_header X-Forwarded-User "";`) to prevent identity spoofing — the auth proxy is the sole source of truth. See [token-exchange-sketch.md](token-exchange-sketch.md) and [session-authorization-sketch.md](session-authorization-sketch.md) for the full trust model and deployment guarantees.

### GET /api/v1/sessions/:id/review-activity

Returns the review activity log for a session.

**Response:**

```json
{
    "activities": [
        {
            "id": "uuid",
            "actor": "jsmith@company.com",
            "action": "claim",
            "from_status": "needs_review",
            "to_status": "in_progress",
            "created_at": "2026-03-05T10:00:00Z"
        },
        {
            "id": "uuid",
            "actor": "jsmith@company.com",
            "action": "resolve",
            "from_status": "in_progress",
            "to_status": "resolved",
            "resolution_reason": "actioned",
            "note": "Applied fix from runbook, ticket INFRA-1234",
            "created_at": "2026-03-05T11:30:00Z"
        }
    ]
}
```

### Extended GET /api/v1/sessions query params

| Param | Type | Description |
|---|---|---|
| `review_status` | string (comma-separated) | Filter by review status: `needs_review`, `in_progress`, `resolved` |
| `assignee` | string | Filter by assignee (exact match) |
| `resolution_reason` | string | Filter resolved sessions by reason |

## WebSocket Events

### New event type: `review.status`

Published to `GlobalSessionsChannel` ("sessions") for Triage view updates, persistent (stored in DB for catchup).

```go
const EventTypeReviewStatus = "review.status"

type ReviewStatusPayload struct {
    BasePayload
    ReviewStatus     string  `json:"review_status"`               // needs_review, in_progress, resolved
    Assignee         *string `json:"assignee,omitempty"`           // null when unassigned
    ResolutionReason *string `json:"resolution_reason,omitempty"`  // actioned, dismissed
    Actor            string  `json:"actor"`                        // who triggered the change
}
```

The frontend Triage view subscribes to `sessions` channel (already subscribed for `session.status` and `session.progress`). On `review.status` events, it updates the card's position in the Kanban/grouped list.

> **Open question:** Should `review.status` be published to `GlobalSessionsChannel` only, or also to `SessionChannel(sessionID)`? See [questions document](session-workflow-design-questions.md), Q5.

## Frontend

### Component hierarchy

```
DashboardView (existing, modified)
├── TabBar: "Sessions" | "Triage"
│   ├── value from localStorage ('tarsy-dashboard-tab')
│   └── ToggleButtonGroup (matches existing Reasoning/Trace pattern)
├── [Sessions tab] — existing content unchanged
│   ├── FilterPanel
│   ├── ActiveAlertsPanel
│   └── HistoricalAlertsList
└── [Triage tab]
    ├── TriageFilterBar
    │   ├── Search
    │   ├── Alert type / chain filters (shared with Sessions)
    │   └── Assignee filter ("My sessions" / "Unassigned" / "All")
    ├── LayoutToggle: "List" | "Board"
    │   └── ToggleButtonGroup (persisted to localStorage 'tarsy-triage-layout')
    ├── [List layout] TriageGroupedList
    │   ├── TriageGroupSection ("Investigating", count, collapsible)
    │   │   └── TriageSessionRow[] (compact, read-only)
    │   ├── TriageGroupSection ("Needs Review", count)
    │   │   └── TriageSessionRow[] (with Claim button)
    │   ├── TriageGroupSection ("In Progress", count)
    │   │   └── TriageSessionRow[] (with Resolve button)
    │   └── TriageGroupSection ("Resolved", count, collapsed by default)
    │       └── TriageSessionRow[] (with resolution reason badge)
    └── [Board layout] TriageKanbanBoard
        ├── KanbanColumn ("Investigating", read-only)
        │   └── KanbanCard[] (compact, no actions)
        ├── KanbanColumn ("Needs Review")
        │   └── KanbanCard[] (Claim button, droppable)
        ├── KanbanColumn ("In Progress")
        │   └── KanbanCard[] (Resolve button, droppable)
        └── KanbanColumn ("Resolved", collapsed/scrollable)
            └── KanbanCard[] (resolution badge, droppable for reopen)
```

### Key components

**TriageSessionRow** — Table row for the grouped list view. Reuses data from `DashboardSessionItem` (extended with review fields). Shows: status badge, alert type, chain, author, assignee badge, time, action button.

**KanbanCard** — Card for the Kanban board. Compact: alert type, chain, author/assignee, time, executive summary snippet (truncated). Primary action button contextual to column. Three-dot menu for secondary actions.

**ResolveModal** — Compact dialog for resolving a session. Resolution reason radio group (`actioned` / `dismissed`) + optional note textarea + confirm button. Used by both the Resolve button and drag-to-Resolved.

> **Open question:** What information should Kanban cards show? See [questions document](session-workflow-design-questions.md), Q6.

### Data fetching

The Triage view needs two data sources:

1. **Active investigations** (for the virtual "Investigating" column): reuse `getActiveSessions()` — already fetched by the dashboard.
2. **Workflow sessions** (needs_review, in_progress, resolved): use `getSessions()` with `review_status` filter.

> **Open question:** Should the Triage view fetch all review-status groups in one call, or separate calls per group? See [questions document](session-workflow-design-questions.md), Q7.

### WebSocket handling

Extend the existing `sessions` channel handler in `DashboardView`:

- On `review.status` event: update the session's review state in the Triage view (move card between columns/groups).
- On `session.status` with terminal status: the backend already sets `review_status`, so the next data refresh picks it up. Alternatively, a `review.status` event fires simultaneously.

### Drag-and-drop

Kanban drag-and-drop using `@dnd-kit/core` and `@dnd-kit/sortable`:
- Each `KanbanColumn` is a droppable area.
- Each `KanbanCard` is a draggable item.
- On drop: determine source and target columns, validate transition, call API, optimistic UI update.
- If target is "Resolved": intercept drop, show `ResolveModal`, call API only after confirmation.
- Investigating column: not droppable as source (cards can't be dragged out).

> **Open question:** Which drag-and-drop library to use? See [questions document](session-workflow-design-questions.md), Q8.

### Keyboard shortcuts

Global keyboard handler (active when Triage tab is focused):
- `C` — claim the currently focused/selected card.
- `R` — resolve the currently focused card (opens ResolveModal).
- Arrow keys — navigate between cards.
- `Esc` — close ResolveModal.

Implemented via `useEffect` with `keydown` listener, scoped to the Triage tab.

### localStorage keys

| Key | Value | Default |
|---|---|---|
| `tarsy-dashboard-tab` | `"sessions"` \| `"triage"` | `"sessions"` |
| `tarsy-triage-layout` | `"list"` \| `"board"` | `"board"` |
| `tarsy-triage-filters` | `TriageFilter` JSON | `{}` |

## Implementation Plan

### Phase 1: Backend — Schema + Service + API

1. Add fields to `ent/schema/alertsession.go`
2. Create `ent/schema/sessionreviewactivity.go`
3. Run `make ent-generate`, `make migrate-create NAME=add_review_workflow`
4. Review and adjust migration (backfill existing terminal sessions)
5. Add `UpdateReviewRequest` and review fields to `pkg/models/session.go`
6. Modify `SessionService.UpdateSessionStatus` to set `review_status` on terminal transitions
7. Add `SessionService.UpdateReviewStatus` and `SessionService.GetReviewActivity`
8. Extend `ListSessionsForDashboard` with `review_status` and `assignee` filters
9. Add review fields to `DashboardSessionItem` response DTO

### Phase 2: Backend — Events + API handlers

1. Add `EventTypeReviewStatus` and `ReviewStatusPayload`
2. Add `PublishReviewStatus` to `EventPublisher`
3. Create `pkg/api/handler_review.go` with `PATCH /sessions/:id/review` and `GET /sessions/:id/review-activity`
4. Register routes in `setupRoutes()`
5. Publish `review.status` events from `UpdateReviewStatus`
6. Unit tests for service methods and handler

### Phase 3: Frontend — Tab bar + Triage grouped list

1. Add tab bar to `DashboardView` (Sessions | Triage)
2. Add `review_status`, `assignee`, `resolution_reason` to TypeScript types
3. Extend API service with `updateReview()` and `getReviewActivity()`
4. Build `TriageGroupedList` with collapsible sections
5. Build `TriageSessionRow` with action buttons
6. Build `ResolveModal`
7. Add `TriageFilterBar` with assignee filter
8. Wire WebSocket `review.status` events
9. localStorage persistence for tab and filters

### Phase 4: Frontend — Kanban board

1. Add drag-and-drop library dependency
2. Build `TriageKanbanBoard` with `KanbanColumn` and `KanbanCard`
3. Implement drag-and-drop transitions with optimistic UI
4. Intercept drag-to-Resolved with ResolveModal
5. Layout toggle (List | Board) in Triage tab
6. Keyboard shortcuts

### Phase 5: Polish

1. Review activity display on session detail page
2. Assignee badge on SessionListItem (Sessions tab, optional column)
3. Triage view empty states
4. Loading and error states
5. Responsive design for Kanban on smaller screens
