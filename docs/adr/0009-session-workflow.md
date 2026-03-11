# ADR-0009: Session Workflow

**Status:** Implemented (Phases 1–3); Phases 4–5 deferred
**Date:** 2026-03-11

## Overview

TARSy automates incident investigation but has no human workflow after the AI finishes. This design adds a lightweight review lifecycle on top of the existing investigation lifecycle, giving SRE teams an action-oriented "Triage" view alongside the current session list.

## Design Principles

1. **Additive, not disruptive.** The existing session list, status model, API, and WebSocket events remain unchanged. Teams that don't use the workflow see no difference.
2. **Fast queries for the workflow view.** Denormalized `review_status` and `assignee` on the session avoid JOINs for list/filter/group operations.
3. **Auditable.** Every workflow transition is logged in the activity table with actor, timestamp, from/to state.
4. **Real-time.** Workflow state changes propagate via WebSocket so all users see the same board state.
5. **Consistent patterns.** Follow existing TARSy conventions: Ent schema for DB, service layer for business logic, Echo handlers for API, event publisher for WebSocket, MUI for frontend.

## Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| S-Q1 | Where does the review lifecycle live? | Hybrid: fields on session + activity table | Fast list queries (no JOIN), history in activity table, follows scoring pattern (denormalized current state + related table) |
| S-Q2 | View paradigm for workflow dashboard? | Additive hybrid: keep current list + new Triage tab | Purely additive — existing list unchanged; clean separation of "what has the system done" vs. "what do I need to do" |
| S-Q3 | How does assignment work? | Self-claim only (X-Forwarded-User) | No user registry exists; extend to assign-to-others when OIDC groups land |
| S-Q4 | Review workflow states? | 3 states (`needs_review` → `in_progress` → `resolved`) + `resolution_reason` | Simple state machine; resolution reason (`actioned`/`dismissed`) captures outcome without extra states |
| S-Q5 | Which sessions enter the workflow? | All terminal sessions auto-enter + virtual "Investigating" column | Nothing falls through cracks; SREs see full picture; `dismissed` is escape valve for noise |
| S-Q6 | Where does the workflow view live? | Tabs on existing dashboard (Sessions \| Triage) | Everything in one place; default stays as current view; last-used tab persisted |
| S-Q7 | How to identify users? | Raw `X-Forwarded-User` header value | Zero infrastructure; consistent with existing `author` pattern |
| D-Q1 | How to backfill existing terminal sessions? | Backfill all to `resolved`/`dismissed` | Clean start; only new investigations enter the workflow queue |
| D-Q2 | Claiming already-claimed session? | Allow with frontend confirmation | Prevents accidental overrides while allowing handoff when someone is off-shift |
| D-Q3 | Direct resolve from `needs_review`? | Allow, auto-set assignee to resolver | Fast noise dismissal; clean data (no NULL assignees on resolved sessions) |
| D-Q4 | Dedicated triage API endpoint? | Yes — `GET /api/v1/sessions/triage` with grouped response | Single call for entire view; server-side counts; bounded resolved group |
| D-Q5 | `review.status` event channels? | Both SessionChannel and GlobalSessionsChannel | Session detail page gets real-time updates; consistent with `session.status` pattern |
| D-Q6 | Kanban card content? | Alert type, chain, author, time, exec summary snippet, assignee/score badges | Enough to triage without opening detail page; no investigation internals |
| D-Q7 | Triage view fetch strategy? | Single call with `resolved_limit` | Atomic + bounded response; active groups return in full (small); resolved is capped |
| D-Q8 | Drag-and-drop library? | `@dnd-kit/core` + `@dnd-kit/sortable` | React 19 compatible, accessible, ~27kB, largest community |

## Architecture

### Data Flow

```
Worker completes investigation
  → Worker.updateSessionTerminalStatus (atomic tx: terminal status + review init)
    → Sets review_status = needs_review (or resolved+dismissed for cancelled)
  → Worker.publishSessionStatus (session.status to SessionChannel + GlobalSessionsChannel)
  → Worker.publishReviewStatus (review.status to SessionChannel + GlobalSessionsChannel)
  → Frontend Triage view updates via WebSocket

SRE clicks "Claim"
  → PATCH /api/v1/sessions/:id/review {action: "claim"}
  → SessionService.UpdateReviewStatus (sets assignee, review_status = in_progress)
  → Inserts session_review_activity row
  → EventPublisher.PublishReviewStatus (to SessionChannel + GlobalSessionsChannel)
  → Frontend Triage view updates

SRE clicks "Resolve"
  → PATCH /api/v1/sessions/:id/review {action: "resolve", resolution_reason: "actioned", note: "..."}
  → SessionService.UpdateReviewStatus (sets resolved_at, resolution_reason, review_status = resolved)
  → Inserts session_review_activity row
  → EventPublisher.PublishReviewStatus (to SessionChannel + GlobalSessionsChannel)
  → Frontend Triage view updates
```

### Component Overview

| Layer | Component | Changes |
|---|---|---|
| **Schema** | `ent/schema/alertsession.go` | Add `review_status`, `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` fields + `review_activities` edge |
| **Schema** | `ent/schema/sessionreviewactivity.go` | New entity |
| **Migration** | `pkg/database/migrations/` | Add columns to `alert_sessions`, create `session_review_activity` table, backfill existing terminal sessions |
| **Worker** | `pkg/queue/worker.go` | Modify `updateSessionTerminalStatus` to initialize `review_status` atomically; add `publishReviewStatus` helper |
| **Service** | `pkg/services/session_service.go` | Add `UpdateReviewStatus`, `GetReviewActivity` methods (no changes to `UpdateSessionStatus`) |
| **Models** | `pkg/models/session.go` | Add review fields to DTOs, add request/response types |
| **API** | `pkg/api/handler_review.go` | New handler for `PATCH /sessions/:id/review`, `GET /sessions/:id/review-activity`, `GET /sessions/triage` |
| **API** | `pkg/api/server.go` | Register new routes |
| **Events** | `pkg/events/types.go`, `payloads.go`, `publisher.go` | Add `review.status` event type, payload, and `PublishReviewStatus` method |
| **Frontend** | Dashboard components | Tab bar, Triage view, grouped list, action buttons, resolve modal |

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

New edge (add to existing `Edges()` method):

```go
edge.To("review_activities", SessionReviewActivity.Type).
    Annotations(entsql.OnDelete(entsql.Cascade)),
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

**Backfill strategy:** The migration backfills all existing terminal sessions (`completed`, `failed`, `timed_out`, `cancelled`) to `review_status = resolved`, `resolution_reason = dismissed`, `resolved_at = completed_at`. Deriving `resolved_at` from the existing `completed_at` ensures the resolved group's "most recent first" ordering and `resolved_limit` semantics work correctly from day one. All three fields are set in a single UPDATE for atomicity. This gives a clean start — only new investigations enter the "Needs Review" queue.

## Service Layer

### Worker terminal path — automatic review_status initialization

The Worker owns terminal status writes via `Worker.updateSessionTerminalStatus()` in `pkg/queue/worker.go` — it does NOT go through `SessionService.UpdateSessionStatus`. The review_status initialization must happen in this method. `SessionService.UpdateSessionStatus` is unchanged.

Modify `Worker.updateSessionTerminalStatus` to include review_status initialization in the same transaction. The terminal status write and review init MUST be atomic — if the status commits but review init fails, the session is terminal with no `review_status`, breaking the auto-enter invariant.

```go
// updateSessionTerminalStatus writes the final session status and initializes
// the review workflow. Returns whether review_status was initialized (the caller
// should publish a review.status event when true).
func (w *Worker) updateSessionTerminalStatus(ctx context.Context, session *ent.AlertSession, result *ExecutionResult) (bool, error) {
    tx, err := w.client.Tx(ctx)
    if err != nil {
        return false, fmt.Errorf("failed to begin transaction: %w", err)
    }
    defer func() { _ = tx.Rollback() }()

    // 1. Write terminal status + results as a compare-and-set: only succeed from
    // an active state. If a racing worker already wrote the terminal status, this
    // returns zero affected rows and we treat it as a no-op.
    now := time.Now()
    update := tx.AlertSession.Update().
        Where(
            alertsession.IDEQ(session.ID),
            alertsession.StatusIn(
                alertsession.StatusInProgress,
                alertsession.StatusCancelling,
            ),
        ).
        SetStatus(result.Status).
        SetCompletedAt(now)

    if result.FinalAnalysis != "" {
        update = update.SetFinalAnalysis(result.FinalAnalysis)
    }
    if result.ExecutiveSummary != "" {
        update = update.SetExecutiveSummary(result.ExecutiveSummary)
    }
    if result.ExecutiveSummaryError != "" {
        update = update.SetExecutiveSummaryError(result.ExecutiveSummaryError)
    }
    if result.Error != nil {
        update = update.SetErrorMessage(result.Error.Error())
    }

    statusAffected, err := update.Save(ctx)
    if err != nil {
        return false, fmt.Errorf("failed to update session terminal status: %w", err)
    }
    if statusAffected == 0 {
        // Another worker already wrote the terminal status — nothing to do.
        return false, nil
    }

    // 2. Initialize review_status only on the FIRST terminal transition.
    // Uses a conditional UPDATE with WHERE review_status IS NULL to avoid TOCTOU:
    // if two workers race to set the terminal status, only the first one initializes
    // the review fields — the second sees zero rows affected and is a no-op.
    var affected int

    if result.Status == alertsession.StatusCancelled {
        affected, err = tx.AlertSession.Update().
            Where(
                alertsession.IDEQ(session.ID),
                alertsession.ReviewStatusIsNil(),
            ).
            SetReviewStatus(alertsession.ReviewStatusResolved).
            SetResolvedAt(time.Now()).
            SetResolutionReason(alertsession.ResolutionReasonDismissed).
            Save(ctx)
    } else {
        // completed, failed, timed_out → needs_review
        affected, err = tx.AlertSession.Update().
            Where(
                alertsession.IDEQ(session.ID),
                alertsession.ReviewStatusIsNil(),
            ).
            SetReviewStatus(alertsession.ReviewStatusNeedsReview).
            Save(ctx)
    }
    if err != nil {
        return false, fmt.Errorf("failed to initialize review status: %w", err)
    }

    if err := tx.Commit(); err != nil {
        return false, fmt.Errorf("failed to commit terminal status: %w", err)
    }

    return affected > 0, nil
}
```

The Worker's `processSession` method calls this and then publishes events:

```go
// In Worker.processSession, replacing current steps 11 and 11a:

// 11. Update terminal status + initialize review (atomic, background context)
reviewInitialized, err := w.updateSessionTerminalStatus(finalizeCtx, session, result)
if err != nil {
    log.Error("Failed to update session terminal status", "error", err)
    return err
}

// 11a. Publish terminal session status event
w.publishSessionStatus(finalizeCtx, session.ID, result.Status)

// 11b. Publish review.status event (only when review was actually initialized)
if reviewInitialized {
    w.publishReviewStatus(finalizeCtx, session.ID, result.Status)
}
```

New `publishReviewStatus` helper on the Worker (mirrors existing `publishSessionStatus`):

```go
// publishReviewStatus publishes a review.status event to both the session-specific
// and global channels. Non-blocking: errors are logged.
func (w *Worker) publishReviewStatus(ctx context.Context, sessionID string, terminalStatus alertsession.Status) {
    if w.eventPublisher == nil {
        return
    }

    payload := events.ReviewStatusPayload{
        BasePayload: events.BasePayload{
            Type:      events.EventTypeReviewStatus,
            SessionID: sessionID,
            Timestamp: time.Now().Format(time.RFC3339Nano),
        },
        Actor: "system",
    }

    if terminalStatus == alertsession.StatusCancelled {
        payload.ReviewStatus = "resolved"
        reason := "dismissed"
        payload.ResolutionReason = &reason
    } else {
        payload.ReviewStatus = "needs_review"
    }

    if err := w.eventPublisher.PublishReviewStatus(ctx, sessionID, payload); err != nil {
        slog.Warn("Failed to publish review status",
            "session_id", sessionID, "review_status", payload.ReviewStatus, "error", err)
    }
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

func (s *SessionService) UpdateReviewStatus(ctx context.Context, sessionID string, req UpdateReviewRequest) (*ent.AlertSession, error)
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
4. Insert `SessionReviewActivity` record(s) within the same transaction (only after a successful conditional update). For a **direct resolve** (`needs_review → resolved`), insert **two** activity rows: one for the implicit claim (`needs_review → in_progress`) and one for the resolution (`in_progress → resolved`). For all other actions, insert one row.
5. Read the updated session within the transaction (SELECT after UPDATE) to return it to the caller without a separate read.
6. Commit the transaction. All field updates (`review_status`, `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note`), the activity log insert(s), and the session read are atomic.
7. Return the updated session to the caller.

The API handler publishes the `review.status` event via `EventPublisher.PublishReviewStatus` after a successful `UpdateReviewStatus` call. Event publishing is NOT inside the service method — consistent with the Worker pattern where the caller (Worker/handler) owns event publishing.

This ensures concurrent claims/resolves cannot both succeed — only the first writer wins, the second gets a conflict error.

**Reassignment:** The backend allows reclaiming unconditionally (no 409 for already-claimed sessions). The frontend shows a confirmation dialog when the session is already claimed: "This session is currently claimed by A. Take over?" Both transitions are logged in the activity table.

### Transition validation

Valid state transitions:

| Action | From | To | Sets |
|---|---|---|---|
| `claim` | `needs_review` | `in_progress` | `assignee`, `assigned_at` |
| `claim` | `in_progress` | `in_progress` | `assignee`, `assigned_at` (reassignment — frontend confirmation, backend unconditional) |
| `unclaim` | `in_progress` | `needs_review` | clears `assignee`, `assigned_at` |
| `resolve` | `in_progress` | `resolved` | `resolved_at`, `resolution_reason`, `resolution_note` |
| `resolve` | `needs_review` | `resolved` | `assignee` (auto-set to actor), `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` |
| `reopen` | `resolved` | `needs_review` | clears `assignee`, `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` |

**Direct resolve:** Resolving directly from `needs_review` is allowed. The backend auto-sets `assignee` to the resolver — you can't resolve a session without becoming its owner. The activity log records both the implicit claim and the resolution as separate entries.

### GetReviewActivity

```go
func (s *SessionService) GetReviewActivity(ctx context.Context, sessionID string) ([]*ent.SessionReviewActivity, error)
```

Returns all activity records for a session, ordered by `created_at` ascending.

### ListSessions / ListSessionsForDashboard extensions

Add `review_status` and `assignee` to the existing filter parameters and response DTOs:

- `DashboardListParams`: add `ReviewStatus []string`, `Assignee string`
- `DashboardSessionItem`: add `ReviewStatus *string`, `Assignee *string`, `ResolutionReason *string`

**Triage endpoint:** A new `GET /api/v1/sessions/triage` returns a grouped response (`investigating`, `needs_review`, `in_progress`, `resolved`) with counts. Each group is bounded (e.g., `?resolved_limit=20`), avoiding expensive full scans. See API section below.

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
| `409 Conflict` | State changed since last read — the atomic compare-and-transition found zero affected rows (e.g., unclaiming a session that was already resolved by another user, reopening a session that is currently `in_progress`). |

**Auth:** `extractAuthor` provides the actor identity from the `X-Forwarded-User` header (set by oauth2-proxy or kube-rbac-proxy running as colocated sidecars). **Deployment requirement:** the ingress must strip any client-supplied `X-Forwarded-User` header (e.g., `proxy_set_header X-Forwarded-User "";`) to prevent identity spoofing — the auth proxy is the sole source of truth. See [token-exchange-sketch.md](../proposals/token-exchange-sketch.md) and [session-authorization-sketch.md](../proposals/session-authorization-sketch.md) for the full trust model and deployment guarantees.

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

### GET /api/v1/sessions/triage

Returns sessions grouped by review status for the Triage view. Single call for the entire view.

**Query parameters:**

| Param | Type | Default | Description |
|---|---|---|---|
| `resolved_limit` | int | `20` | Max resolved sessions to return (most recent first) |
| `assignee` | string | | Filter by assignee (exact match). Empty = all. |

**Response:**

```json
{
    "investigating": {
        "count": 2,
        "sessions": [...]
    },
    "needs_review": {
        "count": 5,
        "sessions": [...]
    },
    "in_progress": {
        "count": 1,
        "sessions": [...]
    },
    "resolved": {
        "count": 142,
        "sessions": [...],
        "has_more": true
    }
}
```

Each group's `sessions` array contains `DashboardSessionItem` objects (extended with review fields). The `investigating` group is populated from active sessions (`status IN (pending, in_progress)`, `review_status IS NULL`) using the same query as `GetActiveSessions`. The `needs_review`, `in_progress`, and `resolved` groups query by `review_status`. All groups except `resolved` return in full; `resolved` is bounded by `resolved_limit` and includes `has_more` for pagination.

| Status | When |
|---|---|
| `200 OK` | Returns grouped response. Empty groups have `count: 0` and `sessions: []`. |
| `500 Internal Server Error` | Database error. |

### Extended GET /api/v1/sessions query params

| Param | Type | Description |
|---|---|---|
| `review_status` | string (comma-separated) | Filter by review status: `needs_review`, `in_progress`, `resolved` |
| `assignee` | string | Filter by assignee (exact match) |
| `resolution_reason` | string | Filter resolved sessions by reason |

## WebSocket Events

### New event type: `review.status`

Published to both `SessionChannel(sessionID)` and `GlobalSessionsChannel` — matching the existing `PublishSessionStatus` pattern:

1. **Persist** to `SessionChannel(sessionID)` via `persistAndNotify` (stored in DB for catchup on reconnect).
2. **Broadcast** to `GlobalSessionsChannel` via `notifyOnly` (transient — for the Triage dashboard's live updates).

```go
const EventTypeReviewStatus = "review.status"

type ReviewStatusPayload struct {
    BasePayload
    ReviewStatus     string  `json:"review_status"`               // needs_review, in_progress, resolved
    Assignee         *string `json:"assignee,omitempty"`           // null when unassigned
    ResolutionReason *string `json:"resolution_reason,omitempty"`  // actioned, dismissed
    Actor            string  `json:"actor"`                        // who triggered the change ("system" for automated worker transitions)
}
```

`PublishReviewStatus` in `EventPublisher` follows the same dual-channel pattern as `PublishSessionStatus`:

```go
func (p *EventPublisher) PublishReviewStatus(ctx context.Context, sessionID string, payload ReviewStatusPayload) error {
    payloadJSON, err := json.Marshal(payload)
    if err != nil {
        return fmt.Errorf("failed to marshal ReviewStatusPayload: %w", err)
    }

    var firstErr error
    if err := p.persistAndNotify(ctx, sessionID, SessionChannel(sessionID), payloadJSON); err != nil {
        slog.Warn("Failed to publish review status to session channel",
            "session_id", sessionID, "error", err)
        firstErr = err
    }
    if err := p.notifyOnly(ctx, GlobalSessionsChannel, payloadJSON); err != nil {
        slog.Warn("Failed to publish review status to global channel",
            "session_id", sessionID, "error", err)
        if firstErr == nil {
            firstErr = err
        }
    }
    return firstErr
}
```

The frontend Triage view subscribes to the `sessions` channel (already subscribed for `session.status` and `session.progress`). On `review.status` events, it updates the card's position in the grouped list. The session detail page subscribes to `session:{id}` and can show the current review state with real-time updates.

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
    └── TriageGroupedList
        ├── TriageGroupSection ("Investigating", count, collapsible)
        │   └── TriageSessionRow[] (compact, read-only)
        ├── TriageGroupSection ("Needs Review", count)
        │   └── TriageSessionRow[] (with Claim button)
        ├── TriageGroupSection ("In Progress", count)
        │   └── TriageSessionRow[] (with Resolve button)
        └── TriageGroupSection ("Resolved", count, collapsed by default)
            └── TriageSessionRow[] (with resolution reason badge)
```

### Key components

**TriageSessionRow** — Table row for the grouped list view. Reuses data from `DashboardSessionItem` (extended with review fields). Shows: status badge, alert type, chain, author, assignee badge, time, action button.

**ResolveModal** — Compact dialog for resolving a session. Resolution reason radio group (`actioned` / `dismissed`) + optional note textarea + confirm button.

### Data fetching

Single API call to `GET /api/v1/sessions/triage` returns all four groups — including the `investigating` group (active sessions) — in one response. The backend assembles the `investigating` group using the same query as `GetActiveSessions` and the workflow groups by `review_status`. The `resolved` group is bounded by `?resolved_limit=20` (default). Pagination for older resolved sessions is available via the standard sessions list endpoint with `review_status=resolved` filter.

### WebSocket handling

Extend the existing `sessions` channel handler in `DashboardView`:

- On `review.status` event: update the session's review state in the Triage view (move between groups).
- On `session.status` with terminal status: the backend already sets `review_status`, so the next data refresh picks it up. Alternatively, a `review.status` event fires simultaneously.

### localStorage keys

| Key | Value | Default |
|---|---|---|
| `tarsy-dashboard-tab` | `"sessions"` \| `"triage"` | `"sessions"` |
| `tarsy-triage-filters` | `TriageFilter` JSON | `{}` |

## Implementation Plan

### Phase 1: Backend — Schema + Service — DONE

1. Add fields and `review_activities` edge to `ent/schema/alertsession.go`
2. Create `ent/schema/sessionreviewactivity.go`
3. Run `make ent-generate`, `make migrate-create NAME=add_review_workflow`
4. Review and adjust migration (backfill existing terminal sessions to `resolved`/`dismissed`)
5. Add `UpdateReviewRequest` and review fields to `pkg/models/session.go`
6. Modify `Worker.updateSessionTerminalStatus` to initialize `review_status` atomically (transactional: terminal status + review init in one tx)
7. Add `SessionService.UpdateReviewStatus` and `SessionService.GetReviewActivity`
8. Extend `ListSessionsForDashboard` with `review_status` and `assignee` filters
9. Add review fields to `DashboardSessionItem` response DTO

### Phase 2: Backend — Events + API handlers — DONE

1. Add `EventTypeReviewStatus` and `ReviewStatusPayload` to `pkg/events/types.go` and `payloads.go`
2. Add `PublishReviewStatus` to `EventPublisher` (dual-channel: persist to session channel, transient to global)
3. Add `Worker.publishReviewStatus` helper; call from `processSession` after terminal status write
4. Create `pkg/api/handler_review.go` with `PATCH /sessions/:id/review`, `GET /sessions/:id/review-activity`, `GET /sessions/triage/:group` (per-group paginated endpoint; `:group` is one of `investigating`, `needs_review`, `in_progress`, `resolved`; accepts `page`, `page_size`, `assignee` query params; returns `{ count, page, page_size, total_pages, sessions[] }`)
5. Register routes in `setupRoutes()`
6. Publish `review.status` events from API handler after `UpdateReviewStatus` calls (manual transitions)
7. Unit tests for service methods, Worker review init, and handlers

### Phase 3: Frontend — Tab bar + Triage grouped list — DONE

1. Add tab bar to `DashboardView` (Sessions | Triage)
2. Add `review_status`, `assignee`, `resolution_reason` to TypeScript types
3. Extend API service with `updateReview()`, `getReviewActivity()`, and `getTriageSessions()`
4. Build `TriageGroupedList` with collapsible sections
5. Build `TriageSessionRow` with action buttons
6. Build `ResolveModal`
7. Add `TriageFilterBar` with assignee filter
8. Wire WebSocket `review.status` events
9. localStorage persistence for tab and filters

### Phase 4: Frontend — Kanban board — DEFERRED

1. Add drag-and-drop library dependency (`@dnd-kit/core` + `@dnd-kit/sortable`)
2. Build `TriageKanbanBoard` with `KanbanColumn` and `KanbanCard`
3. Implement drag-and-drop transitions with optimistic UI
4. Intercept drag-to-Resolved with ResolveModal
5. Layout toggle (List | Board) in Triage tab
6. Keyboard shortcuts

### Phase 5: Polish — DEFERRED

1. Review activity display on session detail page
2. Assignee badge on SessionListItem (Sessions tab, optional column)
3. Triage view empty states
4. Loading and error states
5. Responsive design for Kanban on smaller screens
