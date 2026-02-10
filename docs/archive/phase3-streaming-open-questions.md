# Phase 3.4: Real-time Streaming — Open Questions

**Status**: ✅ All Questions Decided  
**Last Updated**: 2026-02-09

---

## Q1: Stream Chunk Content — Accumulated vs Delta ✅ DECIDED (revised)

**Decision**: **Incremental deltas**. Each `stream.chunk` carries only the new tokens (the delta), not the full accumulated content. Clients concatenate deltas locally.

**Original decision** was accumulated content for simpler client logic. **Revised** during implementation because:
- PostgreSQL NOTIFY has a hard 8 KB payload limit. Accumulated content exceeds this for any LLM response longer than ~7.9 KB, causing silent truncation of transient `stream.chunk` events with no DB fallback. This is a data-loss bug, not a theoretical concern.
- Events within a single session are delivered in strict order (see Q10), so out-of-order delivery — the main argument for accumulated content — does not apply in practice.
- `timeline_event.completed` carries the full authoritative content from DB, correcting any deltas missed during a disconnect.
- Frontend concatenation is trivial (`content += delta`).
- Delta payloads are ~50-200 bytes each, well under the 8 KB limit.

---

## Q2: NOTIFY Channel Granularity ✅ DECIDED

**Decision**: **Per-session** channels. One channel per session: `session:{session_id}`.

**Rationale**: Simple — one subscription per session page. Total event volume per session (~100-500 events) is easily handled by a single channel. Finer-grained channels add complexity without clear benefit at expected scale.

---

## Q3: Client Catchup Window Size ✅ DECIDED

**Decision**: Cap catchup at **200 events**. If more events were missed, the server includes a `"has_more": true` flag in the catchup response. The frontend client automatically detects this flag and falls back to a full REST API reload of the session state (`GET /api/v1/sessions/{id}`) instead of requesting additional catchup pages.

**Rationale**: 200 events covers typical disconnects (seconds to minutes). Automatic REST fallback keeps the client logic simple — no pagination loop for catchup, just a single conditional: if `has_more`, reload from REST.

---

## Q4: Session List Page — REST Polling vs WebSocket ✅ DECIDED

**Decision**: **Global channel** (`"sessions"`). Include a global `"sessions"` channel that broadcasts session-level status events (created, started, completed, failed) so the session list page updates in real-time.

**Rationale**: Real-time session list updates provide a noticeably better UX — users see new sessions appear and status changes happen instantly without polling delay. The global channel only carries lightweight session status events (not timeline events or streaming chunks), so bandwidth is minimal. Clients subscribed to `"sessions"` receive only `session.status` and `session.created` events, not the full timeline event stream.

---

## Q5: Event Cleanup Timing ✅ DECIDED

**Decision**: **60-second grace period**. After session completion, wait 60 seconds before cleaning up the session's events from the notification table. Implementation: `time.AfterFunc(60 * time.Second, ...)` in the worker.

**Rationale**: Gives connected clients time to receive the final events and complete rendering before the catchup data disappears. The `timeline_events` table retains data regardless — this only affects the `events` (notification) table used for catchup.

---

## Q6: WebSocket Backpressure ✅ DECIDED

**Decision**: **Timeout + disconnect**. If a WebSocket write takes >10s, close the connection. The client reconnects and catches up via the catchup mechanism.

**Rationale**: Simplest approach — leverages the catchup system we're already building. The coder/websocket library supports write timeouts natively. 10s is generous for any reasonable connection.

---

## Q7: NotifyListener Connection Pooling ✅ DECIDED

**Decision**: **Single dedicated connection** for all LISTEN channels. No pooling.

**Rationale**: A single connection handles thousands of notifications/sec. At expected scale (50 concurrent sessions, ~500 notifications/sec) this is well within capacity. Revisit only if monitoring shows it becoming a bottleneck.

---

## Q8: Streaming Events for Tool Calls ✅ DECIDED

**Decision**: Phase 3.4 publishes `timeline_event.created` and `timeline_event.completed` for tool calls (already created by controllers), but does **not** implement `stream.chunk` for tool output. Tool output streaming (live MCP output) is deferred to **Phase 4** as a dedicated item.

**Rationale**: In Phase 3.4, tool events appear as instant created→completed pairs — sufficient since there's no real MCP execution yet. Phase 4 adds the real MCP client and will extend the streaming protocol with live tool output chunks.

---

## Q9: Handling NOTIFY During Transactions ✅ DECIDED

**Decision**: **INSERT + `pg_notify()` in the same transaction** (option a). `pg_notify()` is naturally transactional in PostgreSQL — notifications are held until `COMMIT`. The `EventPublisher.Publish()` method must execute the event INSERT and `pg_notify()` call within a single database transaction, not on separate connections.

**Implementation note**: The current design sketch uses separate calls (`eventService.CreateEvent()` on the ent pool, then `db.ExecContext()` for `pg_notify()`). During implementation, refactor `Publish()` to use a single ent transaction that includes both the INSERT and the `pg_notify()` call.

---

## Q10: Event Ordering Guarantees ✅ DECIDED

**Decision**: **Ordered within a session**, no ordering guarantee across sessions. The frontend uses `sequence_number` for display ordering and `db_event_id` for catchup tracking.

**Rationale**: Within a single session, the full pipeline is sequential: controller creates events one at a time → NOTIFY delivers in commit order → `receiveLoop` processes sequentially → `Broadcast` sends sequentially. No cross-session ordering is needed.

---

## Summary of Recommendations

| # | Question | Recommendation |
|---|---|---|
| Q1 | Accumulated vs delta content | ✅ Incremental deltas — decided (revised from accumulated; avoids 8 KB NOTIFY limit) |
| Q2 | Channel granularity | ✅ Per-session — decided |
| Q3 | Catchup window size | ✅ 200 events cap; auto REST fallback — decided |
| Q4 | Session list real-time | ✅ Global "sessions" channel — decided |
| Q5 | Event cleanup timing | ✅ 60-second grace period — decided |
| Q6 | WebSocket backpressure | ✅ Timeout + disconnect (10s) — decided |
| Q7 | Listener connection pooling | ✅ Single connection — decided |
| Q8 | Tool call streaming | ✅ Persist now; stream tool output in Phase 4 — decided |
| Q9 | NOTIFY in transactions | ✅ Same transaction for INSERT + pg_notify — decided |
| Q10 | Event ordering | ✅ Ordered within session — decided |
