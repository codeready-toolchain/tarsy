# ADR-0009: Session Workflow

**Status:** Implemented (list-based Triage); Kanban and follow-up polish deferred  
**Date:** 2026-03-11

## Overview

TARSy automates incident investigation but has no human workflow after the AI finishes. This design adds a lightweight review lifecycle on top of the existing investigation lifecycle, giving SRE teams an action-oriented "Triage" view alongside the current session list.

## Design Principles

1. **Additive, not disruptive.** The existing session list, status model, API, and WebSocket events remain unchanged. Teams that don't use the workflow see no difference.
2. **Fast queries for the workflow view.** Denormalized `review_status` and `assignee` on the session avoid JOINs for list/filter/group operations.
3. **Auditable.** Every workflow transition is logged in the activity table with actor, timestamp, from/to state.
4. **Real-time.** Workflow state changes propagate via WebSocket so all users see the same board state.
5. **Consistent patterns.** Follow existing TARSy conventions: schema/migrations for DB, service layer for business logic, HTTP handlers for API, event publisher for WebSocket, MUI for frontend.

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
  → Atomic DB update: terminal session status + review workflow initialization
  → Publish session.status (session-specific + global channels)
  → Publish review.status (session-specific + global channels)
  → Frontend Triage view updates via WebSocket

SRE clicks "Claim"
  → PATCH /api/v1/sessions/:id/review { action: "claim" }
  → Session service applies transition, inserts review activity
  → Publish review.status
  → Frontend Triage view updates

SRE clicks "Resolve"
  → PATCH /api/v1/sessions/:id/review { action: "resolve", resolution_reason, note }
  → Session service applies transition, inserts review activity
  → Publish review.status
  → Frontend Triage view updates
```

## Database Schema

### AlertSession changes

New nullable fields on `alert_sessions` (NULL while investigation is active):

| Field | Purpose |
|-------|---------|
| `review_status` | `needs_review`, `in_progress`, or `resolved` |
| `assignee` | User who claimed the session (X-Forwarded-User value) |
| `assigned_at` | When the session was claimed |
| `resolved_at` | When `review_status` became `resolved` |
| `resolution_reason` | `actioned` or `dismissed` |
| `resolution_note` | Free-text context on resolution |

**Relationship:** one-to-many **review activities** per session (cascade delete with session).

**Indexes:** `review_status`; composite `(review_status, assignee)`; `assignee`.

### SessionReviewActivity entity

Append-only audit log of workflow transitions.

| Field | Purpose |
|-------|---------|
| `activity_id` | Primary key |
| `session_id` | FK to session (required) |
| `actor` | Who performed the action (X-Forwarded-User) |
| `action` | `claim`, `unclaim`, `resolve`, `reopen` |
| `from_status` / `to_status` | Review state before / after (nullable `from` where N/A) |
| `resolution_reason` | Set when action is resolve |
| `note` | Optional free text |
| `created_at` | Immutable timestamp |

**Index:** `(session_id, created_at)` for ordered history.

### Migration

**Backfill:** All existing terminal sessions (`completed`, `failed`, `timed_out`, `cancelled`) → `review_status = resolved`, `resolution_reason = dismissed`, `resolved_at = completed_at` (single UPDATE for atomicity). Only new investigations enter the "Needs Review" queue.

## Service Layer

### Worker terminal path — review initialization

The worker owns the atomic transition that sets terminal investigation status **and** initializes review fields in one transaction. Terminal status uses compare-and-set from active states so only one worker wins; review initialization uses a conditional update when `review_status` is still NULL so double-writes do not corrupt state. Cancelled sessions are initialized as `resolved` with reason dismissed; other terminal outcomes become `needs_review`.

After commit, the worker publishes session status and, when review was initialized, `review.status`.

### UpdateReviewStatus

API-driven transitions use an **atomic compare-and-transition** pattern inside a transaction:

1. Conditional `UPDATE` on the session row matching the expected current `review_status` (and assignee where relevant).
2. If zero rows updated → rollback and return `409 Conflict`.
3. Insert one or more `SessionReviewActivity` rows in the same transaction. **Direct resolve** from `needs_review` logs two rows: implicit claim then resolve.
4. Re-read the session in the transaction and return it after commit.

Event publishing for `review.status` happens in the API handler (or worker), not inside the service method — consistent with other callers owning side effects.

**Reassignment:** Backend allows reclaiming an in-progress session without conflict; the frontend confirms before taking over from another assignee. Both transitions are logged.

### Transition validation

| Action | From | To | Sets |
|--------|------|-----|------|
| `claim` | `needs_review` | `in_progress` | `assignee`, `assigned_at` |
| `claim` | `in_progress` | `in_progress` | `assignee`, `assigned_at` (reassignment) |
| `unclaim` | `in_progress` | `needs_review` | clears `assignee`, `assigned_at` |
| `resolve` | `in_progress` | `resolved` | `resolved_at`, `resolution_reason`, `resolution_note` |
| `resolve` | `needs_review` | `resolved` | `assignee` (actor), `assigned_at`, `resolved_at`, `resolution_reason`, `resolution_note` |
| `reopen` | `resolved` | `needs_review` | clears assignee/timestamps/resolution fields |

**Direct resolve:** Allowed from `needs_review`; assignee is set to the resolver. Activity log records implicit claim and resolution separately.

### Review activity read path

A dedicated read returns all activity rows for a session, ascending by `created_at`.

### List / dashboard extensions

Session list parameters and dashboard DTOs gain `review_status`, `assignee`, and (where needed) `resolution_reason` for filtering and display. The triage endpoint aggregates groups server-side (see API).

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
|--------|------|
| `200 OK` | Transition succeeded; body includes updated review fields. |
| `400 Bad Request` | Invalid action or missing required fields (e.g. `resolution_reason` for resolve). |
| `404 Not Found` | Session does not exist. |
| `409 Conflict` | Expected state no longer holds (compare-and-transition saw zero rows). |

**Auth:** Actor identity from `X-Forwarded-User` (oauth2-proxy / kube-rbac-proxy). **Deployment requirement:** ingress must strip client-supplied `X-Forwarded-User` so only the auth proxy sets it. See [token-exchange-sketch.md](../proposals/token-exchange-sketch.md) and [session-authorization-sketch.md](../proposals/session-authorization-sketch.md).

### GET /api/v1/sessions/:id/review-activity

Returns the review activity log for a session.

**Response shape:**

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

Grouped payload for the Triage view (one round trip).

**Query parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `resolved_limit` | int | `20` | Max resolved sessions (most recent first) |
| `assignee` | string | — | Filter by assignee (exact); empty = all |

**Response structure:**

```json
{
    "investigating": { "count": 2, "sessions": [] },
    "needs_review": { "count": 5, "sessions": [] },
    "in_progress": { "count": 1, "sessions": [] },
    "resolved": { "count": 142, "sessions": [], "has_more": true }
}
```

Each group's `sessions` array uses the same dashboard session shape as the main list, extended with review fields. **Investigating:** active investigation sessions (`review_status` NULL). **Needs review / in progress / resolved:** filtered by `review_status`. Resolved is capped by `resolved_limit` with `has_more` for pagination; other groups return in full.

| Status | When |
|--------|------|
| `200 OK` | Success; empty groups use `count: 0`, `sessions: []`. |
| `500 Internal Server Error` | Database error. |

### Extended GET /api/v1/sessions query params

| Param | Type | Description |
|-------|------|-------------|
| `review_status` | comma-separated | `needs_review`, `in_progress`, `resolved` |
| `assignee` | string | Exact match |
| `resolution_reason` | string | Filter resolved sessions |

## WebSocket Events

### `review.status`

Published to **both** the session-scoped channel and the global sessions channel (same pattern as `session.status`):

1. **Persist** on the session channel (reconnect / catch-up).
2. **Broadcast** on the global channel (live Triage updates).

**Payload fields (conceptual):**

- Event type `review.status`
- `session_id`, timestamp (base envelope)
- `review_status`: `needs_review` \| `in_progress` \| `resolved`
- `assignee`: optional (null when unassigned)
- `resolution_reason`: optional (`actioned` \| `dismissed`)
- `actor`: who caused the change (`system` for worker-driven initialization)

The Triage view listens on the global sessions subscription; session detail listens on the per-session channel for live review state.

## Frontend

### Component hierarchy

```
DashboardView (existing, modified)
├── TabBar: "Sessions" | "Triage"
│   └── Toggle pattern consistent with existing dashboard controls
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
        │   └── TriageSessionRow[] (with Claim)
        ├── TriageGroupSection ("In Progress", count)
        │   └── TriageSessionRow[] (with Resolve)
        └── TriageGroupSection ("Resolved", count, collapsed by default)
            └── TriageSessionRow[] (resolution reason badge)
```

### Key components

- **TriageSessionRow** — Row for grouped lists; shows status, alert type, chain, author, assignee, time, primary action.
- **ResolveModal** — Resolution reason (`actioned` / `dismissed`), optional note, confirm.

**Deferred (per decisions):** Kanban board (`@dnd-kit`), layout toggle, drag-and-drop–driven transitions, and additional polish (activity on detail page, optional assignee column on main list, empty/loading states, responsive Kanban).
