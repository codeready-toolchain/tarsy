# Phase 5.3: Follow-up Chat — Open Questions

Questions where the new design significantly departs from old TARSy or involves non-obvious trade-offs. Each question includes options and a recommendation.

---

## Q1: Chat Execution Model — Separate Executor vs Shared Worker Pool

**Context**: Old TARSy processes chat messages via `asyncio.create_task()` — direct in-process execution with no concurrency control. The project plan says "queue-based — chat messages routed through worker pool to limit concurrent LLM usage."

**The question**: How should chat messages be processed relative to the existing session WorkerPool?

### Option A: Separate ChatMessageExecutor (recommended in design doc)

A dedicated `ChatMessageExecutor` with its own bounded goroutine pool. Independent concurrency limit (`MaxConcurrentChats`). Triggered by API handler, not by polling.

**Pros**:
- Clean separation of concerns (sessions vs chat)
- Independent concurrency tuning (chat limit != session limit)
- Lower latency for chat (no polling interval delay)
- Simpler implementation (no changes to existing WorkerPool)

**Cons**:
- Two concurrency limits instead of one shared LLM budget
- Under peak load, sessions + chats could exceed desired total LLM concurrency
- Not DB-queue-based (in-memory queue, lost on crash)

### Option B: Shared WorkerPool with Dual Polling

Extend the existing Worker to poll both `AlertSession` (pending) and `ChatUserMessage` (new status field) tables. Workers process whichever they claim first. Single concurrency limit.

**Pros**:
- Single concurrency limit for all LLM work
- True queue-based (DB persistence, crash recovery)
- Reuses existing worker infrastructure

**Cons**:
- Chat latency depends on poll interval (1–5s delay)
- Workers processing long sessions block chat messages
- Requires adding `status` field to ChatUserMessage
- Mixing concerns in the worker loop
- Chat priority vs session priority becomes an issue

### Option C: Shared Semaphore, Separate Trigger

ChatMessageExecutor triggered by API (like Option A), but acquires a slot from a **shared semaphore** that the WorkerPool also uses. Total LLM concurrency is bounded regardless of workload mix.

**Pros**:
- Single LLM concurrency budget
- Low latency for chat (API-triggered)
- Clean separation of triggering but shared resource control

**Cons**:
- Shared semaphore coupling between WorkerPool and ChatMessageExecutor
- WorkerPool currently uses `MaxConcurrentSessions` DB count check, not a semaphore — would need refactoring
- Chat could starve sessions (or vice versa) under contention

### Recommendation: **Option A**

Separate executor is simplest, lowest latency, and follows the Karpathy principle of minimum complexity. The total LLM budget concern is theoretical — in practice, `MaxConcurrentSessions` (e.g., 3) + `MaxConcurrentChats` (e.g., 3) = 6 concurrent LLM calls is well within capacity. If unified budgeting becomes necessary later, Option C can be retrofitted. Old TARSy had no limit at all and worked fine.

---

## Q2: Investigation Context Capture Timing

**Context**: Old TARSy captures investigation context at chat creation time via `_capture_session_context()` and stores it in `chat.conversation_history`. The current new TARSy `ChatService.BuildChatContext()` builds it lazily (queries session on each call).

**The question**: When should investigation context be captured and stored?

### Option A: Capture at chat creation, store on Chat record (recommended)

Add `investigation_context` text field to Chat schema. Populate once when chat is created using `FormatInvestigationContext()` on the session's timeline events.

**Pros**:
- Consistent context across all messages in a chat
- No repeated DB queries for timeline events
- Matches old TARSy's proven approach
- Investigation data is immutable after session completes, so there's no staleness concern

**Cons**:
- Requires schema migration (new field on Chat)
- Stores potentially large text in DB (could be 10–50KB for complex investigations)
- Context is frozen at creation time (but investigation is finished, so this is fine)

### Option B: Build lazily per message

Query timeline events from DB each time a message is processed. No storage on Chat.

**Pros**:
- No schema change
- No data duplication
- Always "fresh" (though investigation is immutable)

**Cons**:
- Multiple DB queries per message (timeline events, stages, executions)
- Inconsistency risk if data is modified between messages (unlikely but possible with soft deletes)
- Performance cost for each message

### Recommendation: **Option A**

Matches old TARSy, better performance, and the investigation data doesn't change after session completion. The storage cost is negligible compared to the timeline events themselves.

---

## Q3: Investigation Context Content — What to Include

**Context**: Old TARSy captures investigation context with `include_thinking=False`, filtering to INVESTIGATION, FINAL_ANALYSIS, FORCED_CONCLUSION interaction types. New TARSy uses timeline events (which are richer but include more noise).

**The question**: Which timeline event types should be included in the chat investigation context?

### Option A: Exclude thinking, include everything else (recommended)

Include: `llm_response`, `final_analysis`, `llm_tool_call` (with summary dedup), `code_execution`, `google_search_result`, `url_context_result`, `error`.
Exclude: `llm_thinking`, `executive_summary`, `mcp_tool_summary` (already deduped into tool calls).

**Pros**:
- Rich context (tool calls + results give the chat agent actionable data)
- Matches old TARSy's intent (no thinking, key investigation content)
- Leverages existing `formatTimelineEvents()` dedup logic

**Cons**:
- Can be large for investigations with many tool calls
- Tool results (even summarized) can be verbose

### Option B: Only final analyses and responses

Include only `final_analysis` and `llm_response` events. Skip all tool calls.

**Pros**:
- Much smaller context
- Focuses on conclusions, not process

**Cons**:
- Loses specific data points the chat agent might need to reference
- User might ask "what did the tool show?" and the agent won't have that data
- Significant departure from old TARSy (which included investigation interactions)

### Option C: Staged summary — executive summary + per-stage final analyses

Use the executive summary for the high-level view, plus each stage's final analysis.

**Pros**:
- Very compact
- Clean hierarchical context

**Cons**:
- Loses all tool data and intermediate findings
- Executive summary may not exist (fail-open generation)
- Severely limits the chat agent's ability to reference specifics

### Recommendation: **Option A**

Richest context without the noise of thinking tokens. The existing tool call/summary dedup in `formatTimelineEvents()` already manages verbosity. This matches old TARSy's approach while leveraging the new timeline event system.

---

## Q4: Chat History — What to Include from Previous Exchanges

**Context**: Old TARSy formats chat history with user questions and full assistant response text (from the LLM interaction conversation). New TARSy would build history from ChatUserMessages and their response Stages.

**The question**: How much detail from previous chat exchanges should be included in subsequent messages' context?

### Option A: User question + final analysis only (recommended)

For each previous exchange, include the user's question and the assistant's final answer (from the `final_analysis` timeline event of the response stage).

**Pros**:
- Concise — previous Q&A don't bloat the context
- Focus on conclusions, not process
- Scales better with many exchanges

**Cons**:
- Previous tool calls/observations are lost (can't reference "what you found earlier")
- If the agent did significant tool work in a previous exchange, the next exchange doesn't know about it

### Option B: User question + full response (all timeline events)

Include the user's question plus all timeline events from the response stage (tool calls, responses, analysis).

**Pros**:
- Full continuity — agent can reference its own previous tool results
- More coherent multi-turn conversations

**Cons**:
- Context grows rapidly (each exchange adds tool calls + results)
- After 3–4 exchanges, context window could be exhausted
- Requires truncation strategy for older exchanges

### Option C: User question + assistant messages only

Include user question and assistant role messages from the conversation (no tool results, no thinking). This is what old TARSy does — it includes the full LLM conversation minus thinking.

**Pros**:
- Moderate context size
- Captures the assistant's reasoning and responses
- Matches old TARSy

**Cons**:
- Assistant messages may reference tool results not in context
- Medium-sized growth per exchange

### Recommendation: **Option A**

Final analysis only keeps the context manageable and focused. The chat agent has access to MCP tools if it needs fresh data. Users typically ask independent follow-up questions rather than building on the agent's previous tool work. If full conversation continuity becomes important, Option C can be added later without breaking changes.

---

## Q5: One-at-a-Time Enforcement

**Context**: Old TARSy doesn't explicitly enforce one-at-a-time per chat — it relies on the UI sending one message at a time. However, nothing prevents concurrent messages, which could cause interleaved tool calls and confused responses.

**The question**: Should we enforce one concurrent execution per chat at the server level?

### Option A: Enforce server-side (recommended)

Return 409 Conflict if a new message is sent while a previous one is still processing. Check for active Stages with `chat_id = chatID AND status IN (pending, active)`.

**Pros**:
- Prevents confused/interleaved responses
- Clean server-side guarantee
- Simple for clients to handle (show "waiting" state)

**Cons**:
- Users must wait for the previous response to complete or cancel it
- Slightly worse UX if responses are slow (can't queue up questions)

### Option B: Allow concurrent, process sequentially

Accept multiple messages, queue them, process in order. Each sees the previous exchange's result.

**Pros**:
- Users can queue questions without waiting
- Better UX for fast typists

**Cons**:
- Significant implementation complexity (ordered queue per chat)
- Later messages wait for all previous to complete
- Cancellation becomes complex (cancel one in the middle?)
- Context dependency between sequential messages

### Option C: No enforcement (like old TARSy)

Accept concurrent messages, process them independently.

**Pros**:
- Simplest implementation

**Cons**:
- Interleaved responses make no contextual sense
- Race condition on stage_index
- Confusing UX

### Recommendation: **Option A**

Server-side enforcement is simple, prevents bugs, and the UI naturally supports it (disable send button while processing). Users can always cancel and resend.

---

## Q6: Stage Event Cleanup for Chat

**Context**: For sessions, the Worker schedules event cleanup 60s after completion (`scheduleEventCleanup`). Chat stages also generate Events in the DB for WebSocket delivery.

**The question**: When should chat stage Events be cleaned up?

### Option A: Same pattern — cleanup after each chat stage completes (recommended)

After each chat response stage reaches terminal status, schedule cleanup of its transient Events after a 60s grace period.

**Pros**:
- Consistent with session pattern
- Prevents Event table growth from active chat conversations
- Simple implementation

**Cons**:
- Many small cleanup operations for active chat sessions

### Option B: Cleanup when chat goes idle

Defer cleanup until no messages have been sent for N minutes. Batch-cleanup all chat stage Events.

**Pros**:
- Fewer cleanup operations
- Events available for longer (better for reconnection catchup)

**Cons**:
- More complex idle detection
- Event table grows during active conversations

### Recommendation: **Option A**

Consistent with existing patterns. The 60s grace period is sufficient for WebSocket delivery.

---

## Q7: Heartbeat for Chat Executions

**Context**: Session execution uses a heartbeat goroutine to update `last_interaction_at` for orphan detection. Old TARSy does the same for chat via `_record_chat_interaction_periodically()`.

**The question**: Should chat executions have heartbeats?

### Option A: Heartbeat on Chat.last_interaction_at (recommended)

Update `Chat.last_interaction_at` periodically during chat message processing. Used for orphan detection (if pod crashes mid-processing).

**Pros**:
- Enables orphan detection for chat (matches session pattern)
- Chat schema already has `pod_id` and `last_interaction_at` fields
- Matches old TARSy

**Cons**:
- Extra DB writes during execution
- Orphan detection for chat is less critical (chat is user-driven, they'll notice)

### Option B: No heartbeat for chat

Chat executions are short-lived (typically 1–2 minutes). If the pod crashes, the Stage stays in `active` status, but the user just sees "no response" and can resend.

**Pros**:
- Simpler
- Chat is user-driven — they'll notice and retry

**Cons**:
- Stale `active` stages could block one-at-a-time check
- No automatic recovery

### Recommendation: **Option A**

The schema already supports it, and it's needed to prevent stale active stages from permanently blocking new messages. Even if we don't run full orphan detection for chat, the heartbeat data enables manual cleanup or future automation.

---

## Q8: Chat Timeout Configuration

**Context**: Sessions have `SessionTimeout` (e.g., 15 minutes). Chat messages are shorter but could still hang.

**The question**: What timeout should chat executions use?

### Option A: Dedicated ChatTimeout config (recommended)

New config value `chat_timeout` (default: 10 minutes) in the queue config section.

**Pros**:
- Independent tuning for chat vs sessions
- Can be shorter than session timeout
- Explicit configuration

### Option B: Reuse SessionTimeout

Same timeout for both.

**Pros**:
- No new config
- Simple

**Cons**:
- Session timeout (15 min) is too long for a single chat response
- Can't tune independently

### Recommendation: **Option A**

Chat responses should be faster than full investigations. A default of 10 minutes provides plenty of headroom while being tighter than the 15-minute session timeout.
