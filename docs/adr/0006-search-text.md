# ADR-0006: Search Text Feature

**Status:** Implemented  
**Date:** 2026-03-05

## Overview

TARSy sessions produce rich text content: LLM thinking, responses, tool call results, summaries, final analyses, executive summaries, and chat messages. The dashboard search only filters the *session list* using case-insensitive matching on a few session-level fields. There is no way to search *within* the detailed content of sessions: timeline event text, tool results, thinking content, or chat messages.

The search text feature adds:

1. **Dashboard search extension** (Phase 1): The existing session list search also searches `timeline_events.content` via PostgreSQL full-text search, finding sessions that mention specific resources, errors, or recommendations anywhere in their investigation content.
2. **In-session search** (Phase 2): A client-side search bar on the session detail view (terminated sessions only) that highlights and navigates to matching content within a session's timeline.

## Design Principles

1. **Progressive enhancement:** Extend the existing dashboard search rather than building a parallel search system.
2. **Server-side for cross-session, client-side for in-session:** FTS with a GIN index for dashboard queries (performance at scale). Exact substring matching client-side for in-session search (all data already loaded).
3. **Minimal schema changes:** One new GIN index on `timeline_events.content`. No new tables or columns aside from a `matched_in_content` boolean in the API response.
4. **Search everything:** All timeline event types are searchable. No artificial restrictions on which content is indexed.

## Key Decisions

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| Q1 | Search scope | Dashboard list search + in-session search (two phases) | Full search workflow: find the session, then find content within it. In-session search is client-side (no extra backend work). Rejected: dashboard-only (can't pinpoint where match is), in-session-only (can't find which session). |
| Q2 | Backend search approach | Hybrid — FTS for dashboard, client-side for in-session | Fast cross-session search via GIN index + exact substring matching within a session. Two behaviors serve different purposes. Rejected: ILIKE without a suitable index (sequential scan at scale), FTS everywhere for in-session (data model already loads events on the client). |
| Q3 | Index strategy | GIN full-text search index on `timeline_events.content` | Matches existing GIN index patterns elsewhere. Fast FTS queries. Rejected: no index (too slow at scale), GIN scoped by event type (unnecessarily restricts searchable content). |
| Q4 | In-session search | Client-side filter/highlight, terminated sessions only | No backend changes; instant results with debounce; works with collapse/expand. Rejected: defer (browser find doesn't work with collapsed sections), server-side in-session search (conflicts with load-all-events UX). |
| Q5 | Match context in session list | Match indicator only (`matched_in_content` boolean) | Simple backend and UI; avoids headline/snippet complexity. Users open the session for detail. Rejected: match snippet (too complex for Phase 1), no indicator (confusing results). |
| Q6 | Event type filtering | Search all event types | Comprehensive — won't miss matches. FTS handles noise via stemming/stop words. Type filtering can be layered on later. Rejected: high-value types only (misses tool output), optional type filter parameter (unnecessary API complexity). |

## Architecture

### Phase 1: Dashboard search extension

```
Dashboard Search Input ("memory leak pod-xyz")
    → GET /api/v1/sessions?search=memory+leak+pod-xyz
    → ListSessionsForDashboard()
    → FTS on alert_sessions.alert_data, alert_sessions.final_analysis (existing)
      OR
      EXISTS subquery: FTS on timeline_events.content (new)
    → Return matching sessions with matched_in_content flag
```

### Phase 2: In-session search

```
Session detail search bar ("pod-xyz")
    → Client-side filter on loaded timeline/flow item content
    → Highlight matches (shared highlight utility)
    → Auto-expand collapsed stages with matches
    → Scroll to first match
    (Only available for terminated sessions)
```

### Database

Add a GIN index on `timeline_events.content` using `to_tsvector('english', content)`, following the same approach as existing full-text indexes on session-level fields.

### Backend (dashboard)

Extend the session list search predicate so matches can come from existing session-field ILIKE conditions **or** an `EXISTS` subquery over `timeline_events` for the same session, comparing `plainto_tsquery` / `to_tsvector` on event `content`. Compute `matched_in_content` when the hit came from timeline content (alone or in addition to session fields).

### Frontend

**Phase 1:** Extend session list types with `matched_in_content` and show a small indicator when content matched inside the timeline.

**Phase 2:** Debounced search over in-memory flow items; substring match, highlight, expand stages containing hits, next/previous navigation and match count.
