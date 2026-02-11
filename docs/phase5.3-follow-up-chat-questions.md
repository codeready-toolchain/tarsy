# Phase 5.3: Follow-up Chat — Open Questions (ALL RESOLVED)

All questions have been resolved. Decisions are reflected in the design document (`docs/phase5.3-follow-up-chat-design.md`).

---

## Q1: ~~Chat Execution Model~~ — RESOLVED

**Decision**: No pool, no queue, no concurrency limits. Each chat message spawns a goroutine directly. Chats are user-initiated and rare — concurrency control solves a problem that doesn't exist. One-at-a-time per chat provides natural limiting. Old TARSy had no limits and worked fine. If this ever becomes a concern, a semaphore is a one-line addition.

---

## Q2: ~~Investigation Context Capture Timing~~ — RESOLVED

**Decision**: Build lazily per message. `TimelineService.GetSessionTimeline()` + `FormatInvestigationContext()` already exist. Investigation data is immutable after session completion, so the same query always returns the same result. One indexed DB query per rare, user-initiated chat message is irrelevant. This avoids a schema migration, model changes, and service layer modifications that pre-capture would require. Departing from old TARSy's pre-capture approach is justified because the new TARSy code makes lazy building trivially easy.

---

## Q3: ~~Investigation Context Content~~ — RESOLVED

**Decision**: Include everything — no filtering at all. All timeline events pass through to `FormatInvestigationContext()`, including `llm_thinking`. This matches the synthesis context builder (which already includes thinking via the shared `formatTimelineEvents()`). Thinking content shows *why* the agent made decisions, which is valuable for both chat follow-up and future reuse (e.g., investigation quality evaluation). `formatTimelineEvents()` already handles all event types including tool call/summary deduplication. Departing from old TARSy's `include_thinking=False` is justified: the universal unfiltered context is simpler (zero filter code) and reusable across multiple consumers.

---

## Q4: ~~Chat History Detail~~ — RESOLVED

**Decision**: Unified timeline context. No separate "chat history" — previous chat exchanges are part of the session timeline. Each chat message creates a `user_question` timeline event, so `GetSessionTimeline()` returns the full chronological record: investigation events → user question → chat response events (with full tool calls and results) → next user question → etc. This gives the agent complete continuity (can reference its own previous tool results) with zero extra code. The separate `buildChatHistory()` / `FormatChatHistory()` / `ChatExchange` correlation logic is not needed.

---

## Q5: ~~One-at-a-Time Enforcement~~ — RESOLVED

**Decision**: Server-side enforcement (Option A). Return 409 Conflict if a message is sent while one is processing. User must cancel or wait. Simple, prevents interleaved responses, and the UI naturally supports it (disable send button while processing).

---

## Q6: ~~Stage Event Cleanup for Chat~~ — RESOLVED

**Decision**: Same pattern as sessions — cleanup after each chat stage completes with 60s grace period. Consistent with existing code.

---

## Q7: ~~Heartbeat for Chat Executions~~ — RESOLVED

**Decision**: Heartbeat on `Chat.last_interaction_at` (Option A). Schema already has the fields. Needed to prevent stale active stages from permanently blocking the one-at-a-time check after a crash.

---

## Q8: ~~Chat Timeout Configuration~~ — RESOLVED

**Decision**: Reuse `SessionTimeout` (Option B). No new config. Simpler. If chat-specific tuning is needed later, a dedicated config is easy to add.
