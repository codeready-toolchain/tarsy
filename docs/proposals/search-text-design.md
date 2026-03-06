# Search Text Feature

**Status:** Final
**Decisions:** [search-text-questions.md](search-text-questions.md)

## Overview

TARSy sessions produce rich text content: LLM thinking, responses, tool call results, summaries, final analyses, executive summaries, and chat messages. Currently, the dashboard search only filters the *session list* using ILIKE on `alert_data` and `final_analysis` — two fields on the `alert_sessions` table. There is no way to search *within* the detailed content of sessions: the timeline event text, tool results, thinking content, or chat messages.

The search text feature adds:

1. **Dashboard search extension** (Phase 1): The existing session list search also searches `timeline_events.content` via PostgreSQL full-text search, finding sessions that mention specific resources, errors, or recommendations anywhere in their investigation content.
2. **In-session search** (Phase 2): A client-side search bar on `SessionDetailPage` (terminated sessions only) that highlights and navigates to matching content within a session's timeline.

## Design Principles

1. **Progressive enhancement**: Extend the existing dashboard search rather than building a parallel search system.
2. **Server-side for cross-session, client-side for in-session**: FTS with GIN index for dashboard queries (performance at scale). Exact substring matching client-side for in-session search (all data already loaded).
3. **Minimal schema changes**: One new GIN index on `timeline_events.content`. No new tables or columns (aside from a `matched_in_content` boolean in the API response).
4. **Search everything**: All timeline event types are searchable. No artificial restrictions on which content is indexed.

## Architecture

### Phase 1: Dashboard Search Extension

```
Dashboard Search Input ("memory leak pod-xyz")
    → GET /api/v1/sessions?search=memory+leak+pod-xyz
    → ListSessionsForDashboard()
    → FTS on alert_sessions.alert_data, alert_sessions.final_analysis (existing)
      OR
      EXISTS subquery: FTS on timeline_events.content (new)
    → Return matching sessions with matched_in_content flag
```

### Phase 2: In-Session Search

```
SessionDetailPage search bar ("pod-xyz")
    → Client-side filter on loaded FlowItem[] content
    → Highlight matches using highlightSearchTermNodes()
    → Auto-expand collapsed stages with matches
    → Scroll to first match
    (Only available for terminated sessions)
```

### Database Changes

New GIN index on `timeline_events.content`, added via `CreateGINIndexes()` in `pkg/database/migrations.go`:

```sql
CREATE INDEX IF NOT EXISTS idx_timeline_events_content_gin
ON timeline_events USING gin(to_tsvector('english', content));
```

This follows the existing pattern for `alert_sessions` GIN indexes.

### Backend Changes

**`pkg/database/migrations.go`** — Add GIN index creation:

```go
// In CreateGINIndexes():
_, err = db.ExecContext(ctx,
    `CREATE INDEX IF NOT EXISTS idx_timeline_events_content_gin
    ON timeline_events USING gin(to_tsvector('english', content))`)
```

**`pkg/services/session_service.go`** — Modify `ListSessionsForDashboard()`:

Current search filter (ILIKE on session fields):
```go
sql.Or(
    sql.ContainsFold(alertsession.FieldAlertData, params.Search),
    sql.ContainsFold(alertsession.FieldFinalAnalysis, params.Search),
)
```

Extended to include timeline event FTS:
```go
sql.Or(
    sql.ContainsFold(alertsession.FieldAlertData, params.Search),
    sql.ContainsFold(alertsession.FieldFinalAnalysis, params.Search),
    sql.ExprP(
        `EXISTS (SELECT 1 FROM timeline_events te
         WHERE te.session_id = "alert_sessions"."session_id"
         AND to_tsvector('english', te.content) @@ plainto_tsquery('english', $1))`,
        params.Search,
    ),
)
```

**`pkg/models/session.go`** — Add `MatchedInContent` to `DashboardSessionItem`:

```go
MatchedInContent bool `json:"matched_in_content"`
```

This flag is `true` when the session matched via timeline event content rather than (or in addition to) session-level fields. Computed in the query or in post-processing.

**`pkg/api/handler_session.go`** — No changes needed (search param already parsed).

### Frontend Changes

**Phase 1 (dashboard):**

- `types/session.ts` — Add `matched_in_content: boolean` to `DashboardSessionItem`
- `SessionListItem` — Show a small indicator (icon/chip) when `matched_in_content` is true, e.g., "Matched in content" chip
- No changes to `FilterPanel` or search behavior — the same input now finds more results

**Phase 2 (in-session search):**

- `SessionDetailPage` — Add a search bar (only visible for terminated sessions: completed, failed, cancelled, timed_out)
- Search bar with debounced input filters `FlowItem[]` by substring match on `content`
- Matching items are highlighted using `highlightSearchTermNodes()` (existing utility)
- Collapsed stages containing matches are auto-expanded
- Navigation controls: next/previous match, match count indicator
- Scroll to first match on search

## Implementation Plan

### Phase 1: Dashboard search extension

1. Add GIN index on `timeline_events.content` in `CreateGINIndexes()`
   → Verify: index exists after startup, `EXPLAIN` shows index scan for FTS queries
2. Modify `ListSessionsForDashboard()` to include `EXISTS` subquery on timeline events
   → Verify: search for text only in timeline events returns the correct session
3. Add `matched_in_content` field to `DashboardSessionItem` and compute it
   → Verify: field is true when match is from timeline, false when from session fields
4. Add `matched_in_content` to frontend types and show indicator in `SessionListItem`
   → Verify: indicator visible for content-matched sessions, hidden for direct matches
5. Add backend tests for extended search
   → Verify: existing search tests pass, new tests cover timeline content search

### Phase 2: In-session search

6. Add search bar to `SessionDetailPage` (terminated sessions only)
   → Verify: bar visible on completed/failed sessions, hidden on active sessions
7. Implement client-side `FlowItem` filtering and highlight
   → Verify: typing a term highlights matching content, clears on empty input
8. Implement auto-expand for collapsed stages with matches
   → Verify: collapsed stage expands when it contains a match
9. Add match navigation (next/previous, count)
   → Verify: navigation scrolls to each match in order
