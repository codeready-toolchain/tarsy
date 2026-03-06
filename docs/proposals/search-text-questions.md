# Search Text Feature — Design Questions

**Status:** Open — decisions pending
**Related:** [Design document](search-text-design.md)

Each question has options with trade-offs and a recommendation. Go through them one by one to form the design, then update the design document.

---

## Q1: Search scope

The search text feature could be scoped to just the dashboard session list (finding sessions by content), or it could also include an in-session search (finding specific text within a session's timeline). These are somewhat independent capabilities.

### Option B: Dashboard list search + in-session search

Option A plus a search bar on `SessionDetailPage` that filters/highlights timeline items matching a query. Client-side filtering on already-loaded timeline events.

- **Pro:** Full search workflow — find the session, then find the content within it.
- **Pro:** In-session search is client-side (no backend changes needed for this part).
- **Con:** More frontend work (search bar, highlight logic, scroll-to-match).
- **Con:** Client-side filtering may be slow for sessions with hundreds of events (unlikely to be a real problem with typical data volumes).

**Decision:** Option B — dashboard list search extended to timeline content (Phase 1) plus client-side in-session search on SessionDetailPage (Phase 2).

_Considered and rejected: Option A (no in-session search — can't pinpoint where in a session the match is), Option C (in-session only — doesn't solve finding which session contains the text)._

---

## Q2: Backend search approach

How to search `timeline_events.content` from the session list query.

### Option C: Hybrid — FTS for dashboard, client-side for in-session

Use PostgreSQL full-text search (`to_tsvector / plainto_tsquery`) with GIN index for the dashboard list search (where performance matters across all sessions). In-session search is client-side exact substring matching on already-loaded timeline events.

- **Pro:** Best of both: fast cross-session search + exact substring matching within a session.
- **Pro:** In-session search is client-side, no backend work needed.
- **Pro:** GIN index — fast even with millions of rows. Supports stemming.
- **Con:** Two different search behaviors (FTS stemming vs. exact match), but they serve different purposes.

**Decision:** Option C — FTS (`to_tsvector/plainto_tsquery`) with GIN index for dashboard cross-session search, client-side exact matching for in-session search.

_Considered and rejected: Option A (ILIKE — can't use GIN indexes, sequential scan at scale), Option B (FTS everywhere — in-session search is already client-side per Q1, no need for server-side FTS there)._

---

## Q3: Index strategy for timeline_events.content

### Option A: GIN full-text search index

```sql
CREATE INDEX IF NOT EXISTS idx_timeline_events_content_gin
ON timeline_events USING gin(to_tsvector('english', content));
```

- **Pro:** Fast FTS queries on timeline content.
- **Pro:** Follows existing pattern (`CreateGINIndexes()` already creates similar indexes).
- **Con:** Index size — timeline events can have large content. GIN index will be substantial.
- **Con:** Write overhead — every timeline event insert/update rebuilds index entries.

**Decision:** Option A — standard GIN FTS index on `timeline_events.content`, added via `CreateGINIndexes()` following the existing pattern.

_Considered and rejected: Option B (no index — sequential scan too slow at scale), Option C (GIN + event type filter — unnecessarily restricts searchable content, can be layered on later)._

---

## Q4: In-session search

### Option A: Client-side filter/highlight (terminated sessions only)

Add a search bar to `SessionDetailPage` for terminated sessions (completed, failed, cancelled, timed_out). When the user types, highlight `FlowItem`s whose content matches the query. Scroll to the first match. Uses existing `highlightSearchTermNodes()` utility.

- **Pro:** No backend changes. All data is already loaded.
- **Pro:** Instant results as user types (debounced).
- **Pro:** Can highlight exact matches within rendered content.
- **Con:** Frontend complexity — need to integrate with collapse/expand state (matches in collapsed stages should expand them).

**Decision:** Option A — client-side filter/highlight on `SessionDetailPage`, only available for terminated sessions (no streaming content to worry about).

_Considered and rejected: Option B (defer — Ctrl+F doesn't work with collapsed sections), Option C (server-side — breaks "load all events" model, too much work)._

---

## Q5: Match context in session list

### Option B: Match indicator only

Show a small badge or icon on session list items indicating the match came from timeline content (vs. session-level fields). No snippet extraction.

- **Pro:** Simpler backend and frontend.
- **Pro:** Avoids SQL complexity of `ts_headline()` or substring extraction.
- **Con:** Users can't see what matched — they have to open the session to find out.

**Decision:** Option B — show a "matched in content" indicator on session list items when the match is from timeline events rather than session-level fields.

_Considered and rejected: Option A (match snippet — too much SQL/backend complexity for Phase 1), Option C (no change — users would be confused about why sessions appear in results)._

---

## Q6: Event type filtering in search

### Option A: Search all event types

Search every `timeline_events.content` regardless of `event_type`.

- **Pro:** Comprehensive — won't miss matches.
- **Pro:** Simpler query (no event_type filter).
- **Con:** May return noisy matches from raw tool results, URL context, etc.

**Decision:** Option A — search all event types. FTS handles noise via stemming/stop words. Type filtering can be added later if needed.

_Considered and rejected: Option B (high-value types only — prevents finding text in tool output), Option C (optional type filter param — unnecessary API complexity)._
