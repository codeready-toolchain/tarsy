# Phase 5.3: Follow-up Chat — Design

## Overview

Add follow-up chat capability so users can ask questions about completed investigations. A chat is a 1:1 extension of an `AlertSession` — after an investigation completes (or fails/is cancelled), users send messages. The first message transparently creates the chat; each message triggers a single-agent execution with access to the same MCP tools, producing a streamed response.

**Current state**: Chat infrastructure exists (ent schemas, ChatService, ChatContext, prompt builder chat branching, investigation formatter, chat instructions/templates, ChatAgent config, chain ChatConfig). No execution pipeline, no API surface.

**Target state**: Full end-to-end chat: API handlers → async execution → streaming response via existing WebSocket. Each chat message spawns a goroutine that runs the agent framework — no pool, no queue, no concurrency limits. Chats are user-initiated and rare; complexity is not warranted.

**Design principle**: Chat is a prompt concern, not a controller concern. The same controllers (ReAct, NativeThinking) handle both investigation and chat — the `ChatContext` on `ExecutionContext` triggers chat-specific prompting. No separate chat controllers. Same iteration limits, same `forceConclusion()` at `MaxIterations`, same per-iteration timeout.

---

## Chat Lifecycle

### End-to-End Flow

```
1. User sends message:
   POST /api/v1/sessions/:id/chat/messages  { content: "Why is pod X crashing?" }
     → Validates terminal session + chat enabled + no active execution + content valid
     → Get-or-create Chat record (first message creates it transparently)
     → Creates ChatUserMessage record
     → Publishes chat.created event (if first message)
     → Publishes chat.user_message event
     → Submits to ChatMessageExecutor (async, returns immediately)
     → Returns { chat_id, message_id, stage_id }

2. ChatMessageExecutor processes (goroutine):
     → Resolves chat agent config from chain
     → Creates Stage (chat_id, chat_user_message_id) + AgentExecution
     → Creates user_question timeline event
     → Builds ChatContext (GetSessionTimeline → FormatInvestigationContext)
     → Publishes stage.status: started
     → Starts heartbeat (Chat.last_interaction_at)
     → Runs agent.Execute() (same controllers as investigation)
     → Agent streams response via existing WebSocket events
     → Updates Stage/AgentExecution terminal status
     → Publishes stage.status: completed/failed
     → Schedules stage event cleanup (60s grace period)

3. User reviews history:
   Existing session timeline API returns full chronological record
     → Investigation events + user_question events + chat response stages

4. User cancels active execution:
   POST /api/v1/sessions/:id/cancel  (existing endpoint, extended)
     → Cancels active investigation OR active chat execution
     → Agent stops, Stage marked failed
```

### Lifecycle Constraints

- **One Chat record per session**: `AlertSession` → `Chat` is 1:1 (existing schema enforces uniqueness on `session_id`). The `Chat` is a container — users can send unlimited messages within it, each producing a response.
- **Terminal sessions only**: Chat is available only for completed/failed/cancelled sessions. Not for pending/in_progress/cancelling.
- **One-at-a-time per chat**: Only one message can be actively executing per chat. Sending a new message while one is processing returns 409 Conflict.
- **Chat enabled check**: Chain config `chat.enabled` must be true (default: true in most chains).

---

## Chat Execution Architecture

### ChatMessageExecutor

A new component in `pkg/queue/` that manages chat message execution. Each message spawns a goroutine — no pool, no semaphore, no queue. Chats are user-initiated, rare, and short-lived; concurrency control is not warranted. One-at-a-time per chat (enforced) naturally limits load.

```go
// ChatMessageExecutor processes chat messages through the agent framework.
// Each message runs in its own goroutine. No concurrency pool — chats are
// rare and user-initiated. One-at-a-time per chat provides natural limiting.
type ChatMessageExecutor struct {
    // Dependencies
    cfg              *config.Config          // full config (for ResolveChatAgentConfig, MCP registry)
    client           *ent.Client
    llmClient        agent.LLMClient
    mcpClientFactory *mcp.ClientFactory
    agentFactory     *agent.AgentFactory
    eventPublisher   agent.EventPublisher
    promptBuilder    agent.PromptBuilder
    execConfig       ChatMessageExecutorConfig

    // Active execution tracking (for cancellation + shutdown)
    mu               sync.RWMutex
    activeExecs      map[string]context.CancelFunc  // chatID → cancel
    wg               sync.WaitGroup                 // tracks active goroutines for shutdown
    stopped          bool                           // reject new submissions after Stop()
}
```

**Why no concurrency pool**: Old TARSy processed chats via `asyncio.create_task()` with no limits and worked fine. Chats are user-initiated (one at a time per chat, per user), making thundering herd impossible. The one-at-a-time enforcement means at most N concurrent chat executions where N = number of active chats — in practice, very few. If this ever becomes a concern, a semaphore is a one-line addition.

### Submit Flow

```go
func (e *ChatMessageExecutor) Submit(ctx context.Context, input ChatExecuteInput) (stageID string, err error) {
    // 1. Reject if stopped (graceful shutdown)
    if e.stopped { return "", ErrShuttingDown }

    // 2. Check one-at-a-time constraint
    //    Query: any Stage with chat_id = input.Chat.ID AND status IN (pending, active)?
    //    If yes → return ErrChatExecutionActive

    // 3. Create Stage record (status: pending)
    //    stage_name: "Chat Response"
    //    chat_id, chat_user_message_id set
    //    stage_index: max(session stages) + 1

    // 4. Launch goroutine
    e.wg.Add(1)
    go e.execute(ctx, input, stageID)

    return stageID, nil
}
```

### Execute Flow

```go
func (e *ChatMessageExecutor) execute(ctx context.Context, input ChatExecuteInput, stageID string) {
    defer e.wg.Done()

    // Create cancellable context with timeout (reuses SessionTimeout from queue config)
    execCtx, cancel := context.WithTimeout(ctx, e.execConfig.SessionTimeout)
    defer cancel()

    // Register for cancellation
    e.registerExecution(input.Chat.ID, cancel)
    defer e.unregisterExecution(input.Chat.ID)

    // 1. Resolve chain + chat agent config
    chain, _ := e.cfg.GetChain(input.Chat.ChainID)
    agentConfig, _ := agent.ResolveChatAgentConfig(e.cfg, chain, chain.Chat)

    // 2. Resolve MCP selection (shared helper, handles session override)
    serverIDs, toolFilter, _ := resolveMCPSelection(input.Session, agentConfig, e.cfg.MCPServerRegistry)

    // 3. Create AgentExecution record

    // 4. Create user_question timeline event (before building context, so it's included)
    e.timelineService.CreateTimelineEvent(execCtx, models.CreateTimelineEventRequest{
        SessionID:   input.Session.ID,
        StageID:     &stageID,
        ExecutionID: &executionID,
        EventType:   timelineevent.EventTypeUserQuestion,
        Content:     input.Message.Content,
    })

    // 5. Build ChatContext (GetSessionTimeline → FormatInvestigationContext)
    chatContext := e.buildChatContext(execCtx, input)

    // 6. Update Stage status: active, publish stage.status: started, start heartbeat
    publishStageStatus(e.eventPublisher, input.Session.ID, stageID, "Chat Response", stageIndex, events.StageStatusStarted)
    go e.runChatHeartbeat(execCtx, input.Chat.ID)

    // 7. Create MCP ToolExecutor (shared helper, same as investigation)
    toolExec, failedServers := createToolExecutor(execCtx, e.mcpClientFactory, serverIDs, toolFilter, logger)
    defer func() { _ = toolExec.Close() }()

    // 8. Build ExecutionContext (with ChatContext populated)
    agentExecCtx := &agent.ExecutionContext{
        SessionID:      input.Session.ID,
        StageID:        stageID,
        ExecutionID:    executionID,
        AgentName:      agentConfig.AgentName,
        AlertData:      input.Session.AlertData,
        AlertType:      input.Session.AlertType,
        Config:         agentConfig,
        LLMClient:      e.llmClient,
        ToolExecutor:   toolExec,
        EventPublisher: e.eventPublisher,
        Services:       serviceBundle,
        PromptBuilder:  e.promptBuilder,
        ChatContext:    chatContext,
        FailedServers:  failedServers,
    }

    // 9. Create agent via AgentFactory → BaseAgent with appropriate Controller
    chatAgent := e.agentFactory.CreateAgent(agentExecCtx)

    // 10. Execute agent (same path as investigation — controller handles chat via ChatContext)
    result, err := chatAgent.Execute(execCtx, agentExecCtx, "")  // no prevStageContext for chat

    // 11. Update AgentExecution + Stage terminal status
    // 12. Publish stage.status: completed/failed

    // 13. Stop heartbeat, schedule event cleanup
}
```

### Heartbeat

During execution, a heartbeat goroutine periodically updates `Chat.last_interaction_at` and sets `Chat.pod_id`. Same pattern as session heartbeat in the Worker. This enables orphan detection: if the pod crashes mid-execution, stale active stages can be identified and cleaned up (preventing the one-at-a-time check from permanently blocking).

```go
// Started inside execute(), cancelled on completion
go e.runChatHeartbeat(execCtx, input.Chat.ID)
```

### Stage Event Cleanup

After each chat response stage reaches terminal status, transient Event records (used for WebSocket delivery) are cleaned up after a 60s grace period. Same pattern as `Worker.scheduleEventCleanup()`:

```go
// At end of execute(), after stage reaches terminal status
time.AfterFunc(60*time.Second, func() {
    e.cleanupStageEvents(context.Background(), stageID)
})
```

### Cancellation

```go
func (e *ChatMessageExecutor) CancelExecution(chatID string) bool {
    e.mu.RLock()
    defer e.mu.RUnlock()
    if cancel, ok := e.activeExecs[chatID]; ok {
        cancel()
        return true
    }
    return false
}
```

Mirrors `WorkerPool.CancelSession()`. The agent framework already handles context cancellation gracefully (iteration loop checks `ctx.Err()`).

### Graceful Shutdown

```go
func (e *ChatMessageExecutor) Stop() {
    e.mu.Lock()
    e.stopped = true
    // Cancel all active executions
    for _, cancel := range e.activeExecs {
        cancel()
    }
    e.mu.Unlock()

    // Wait for goroutines to finish
    e.wg.Wait()
}
```

On shutdown: mark stopped (new submissions rejected with 503), cancel all active contexts, wait for goroutines to drain. Simple — no pool to tear down.

---

## API Surface

### New Endpoints

All endpoints are session-scoped. Since chat is 1:1 with session, the session ID is all the client needs — no separate chat ID to track.

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/sessions/:id/chat/messages` | Send message (auto-creates chat on first message) |

**That's it — one new endpoint.** Everything else reuses existing infrastructure:

- **Chat history**: Existing session timeline API returns the full chronological record including `user_question` events and chat response stages.
- **Cancellation**: Existing `POST /api/v1/sessions/:id/cancel` extended to also cancel chat executions via `ChatMessageExecutor.CancelExecution()`.
- **Chat availability**: No dedicated endpoint. Deferred to Phase 7.2 (Dashboard — History Views), where the session GET/list endpoints will be enriched with chat metadata (chat_id, message_count, chat_enabled), pagination, filters, etc.

### POST `/api/v1/sessions/:id/chat/messages` — Send Message

The primary chat endpoint. On the first message, creates the Chat record transparently (get-or-create pattern). Subsequent messages reuse the existing chat.

**Request**:
```json
{
  "content": "Why is pod X crashing?"
}
```

Content validation: 1–100,000 characters (matches old TARSy).

**Response** (202 Accepted):
```json
{
  "chat_id": "uuid",
  "message_id": "uuid",
  "stage_id": "uuid"
}
```

Returns 202 because processing is asynchronous. The `stage_id` allows the client to track the response via WebSocket events on the session channel.

**Errors**:
- 404: Session not found
- 400: Session not in terminal state / chat not enabled / empty or oversized content
- 409: Active execution in progress (one-at-a-time)
- 503: Shutting down

**Handler flow**:
1. Get session, validate terminal status
2. Resolve chain config, validate `chat.enabled`
3. Validate content
4. Extract author from `X-Forwarded-User` / `X-Forwarded-Email` / `"api-client"`
5. Get-or-create Chat: `ChatService.GetOrCreateChat(sessionID, author)` → returns Chat (creates if first message, reuses if exists)
6. Check no active execution for this chat (409 if active)
7. `ChatService.AddChatMessage()` → creates ChatUserMessage
8. Publish `chat.created` event (if chat was just created)
9. Publish `chat.user_message` event
10. `ChatMessageExecutor.Submit()` → creates Stage, launches async execution
11. Return chat_id + message_id + stage_id

---

## WebSocket Events

### New Event Types

Two new event types. All other chat streaming uses existing event types.

```go
const (
    EventTypeChatCreated     = "chat.created"
    EventTypeChatUserMessage = "chat.user_message"
)
```

### Event Payloads

```go
type ChatCreatedPayload struct {
    Type      string `json:"type"`       // "chat.created"
    SessionID string `json:"session_id"`
    ChatID    string `json:"chat_id"`
    CreatedBy string `json:"created_by"`
    Timestamp string `json:"timestamp"`
}

type ChatUserMessagePayload struct {
    Type      string `json:"type"`       // "chat.user_message"
    SessionID string `json:"session_id"`
    ChatID    string `json:"chat_id"`
    MessageID string `json:"message_id"`
    Content   string `json:"content"`
    Author    string `json:"author"`
    StageID   string `json:"stage_id"`  // response stage (for tracking)
    Timestamp string `json:"timestamp"`
}
```

### Channel Routing

All chat events are published to `session:{session_id}` — the same channel as investigation events. The dashboard subscribes to the session channel and receives both investigation and chat events. No separate chat channel needed.

This matches old TARSy's approach and keeps the WebSocket subscription model simple.

### Event Flow for a Chat Message

```
[chat.created]                → Chat record created (first message only)
[chat.user_message]           → User sent a question
[stage.status: started]       → Chat response stage started
[timeline_event.created]      → llm_thinking/llm_response/llm_tool_call events
[stream.chunk]                → LLM token deltas (transient)
[timeline_event.completed]    → Final events completed
[stage.status: completed]     → Chat response stage finished
```

The client distinguishes chat stages from investigation stages via the `stage.chat_id` field (non-null for chat stages).

---

## Context Building

### Unified Timeline Context

All context — original investigation AND previous chat exchanges — comes from a single source: the session's timeline events. No separate "chat history" builder.

**Key enabler**: Each chat message creates a `user_question` timeline event before the agent runs. This means `GetSessionTimeline()` returns a complete chronological record:

```
[Investigation stage 1 events: tool calls, responses, final analysis]
[Investigation stage 2 events: ...]
[user_question: "Why is pod X crashing?"]        ← chat message 1
[Chat response stage events: tool calls, response, final analysis]
[user_question: "Can you check memory limits?"]   ← chat message 2
[Chat response stage events: ...]                 ← current message runs here
```

Each subsequent chat message naturally sees the full history of everything before it — the original investigation AND all prior chat exchanges with their full tool work. No stripping down to final analyses, no separate correlation logic.

**Build flow** (inside `ChatMessageExecutor.buildChatContext()`):
1. `TimelineService.GetSessionTimeline(sessionID)` — single indexed query, already exists
2. `FormatInvestigationContext(events)` — already exists, handles all event types

**No filtering**. All event types pass through — including `llm_thinking`. This matches the synthesis context builder (which already includes thinking via the shared `formatTimelineEvents()`). Thinking content is valuable: it shows the agent's reasoning, which helps the chat agent understand *why* decisions were made, not just *what* happened. The same unfiltered context is reusable for future needs like investigation quality evaluation.

`formatTimelineEvents()` already handles every event type gracefully, including tool call / summary deduplication. No filter function needed — two existing functions, zero new code.

### user_question Timeline Event

Created by the executor at step 3 of the execute flow, BEFORE building context (step 4). This ensures the user's question appears in the timeline and is included when `GetSessionTimeline()` is called for context building. The `user_question` event type already exists in the TimelineEvent schema. The `formatTimelineEvents()` default case renders it as `**user question:**` which is appropriate.

### ChatContext Cleanup

With unified timeline context, the existing `ChatHistory` field and `ChatExchange` struct in `pkg/agent/context.go` are no longer needed. **Delete them:**

- **Delete** `ChatExchange` struct from `pkg/agent/context.go`
- **Delete** `ChatHistory []ChatExchange` field from `ChatContext`
- **Delete** `FormatChatHistory()` function from `pkg/agent/prompt/chat.go` and its usage in `buildChatUserMessage()`
- **Delete** `FormatChatHistory` tests from `pkg/agent/prompt/chat_test.go`
- **Delete** `ChatService.GetChatHistory()` from `pkg/services/chat_service.go` (replaced by timeline-based context)
- **Delete** `ChatHistoryResponse` from `pkg/models/chat.go`
- **Update** tests in `builder_test.go`, `builder_integration_test.go`, `chat_service_test.go` that reference `ChatHistory`/`ChatExchange`

`ChatContext` becomes:

```go
// ChatContext carries chat-specific data for controllers.
type ChatContext struct {
    UserQuestion        string
    InvestigationContext string
}
```

Builder:

```go
func (e *ChatMessageExecutor) buildChatContext(ctx context.Context, input ChatExecuteInput) *agent.ChatContext {
    events, err := e.timelineService.GetSessionTimeline(ctx, input.Session.ID)
    if err != nil {
        // Fail-open: empty context (agent still has tools)
        return &agent.ChatContext{UserQuestion: input.Message.Content}
    }

    return &agent.ChatContext{
        UserQuestion:        input.Message.Content,
        InvestigationContext: agentctx.FormatInvestigationContext(events),
    }
}
```

### Context Size Considerations

Context grows with each chat exchange (thinking + tool calls + responses accumulate). For long conversations:

- Tool call/summary deduplication already helps (existing formatter feature)
- The growth is bounded by one-at-a-time enforcement (user must wait for each response)
- Future: could add truncation of older exchanges if context window becomes a concern (not in scope for this phase)

---

## Chat Agent Config Resolution

Chat agent configuration follows a resolution hierarchy similar to investigation agents, using the chain's `ChatConfig`:

### Resolution Hierarchy

| Field | Resolution Order | Fallback |
|-------|-----------------|----------|
| **Agent** | `chain.chat.agent` | `"ChatAgent"` (built-in) |
| **IterationStrategy** | `chain.chat.iteration_strategy` → `chain.iteration_strategy` | `defaults.iteration_strategy` |
| **LLMProvider** | `chain.chat.llm_provider` → `chain.llm_provider` | `defaults.llm_provider` |
| **MCPServers** | `chain.chat.mcp_servers` → session's `mcp_selection` → aggregate from chain stages | `[]` |
| **MaxIterations** | `chain.chat.max_iterations` → `chain.max_iterations` | `defaults.max_iterations` |
| **Backend** | Derived from resolved `IterationStrategy` via `ResolveBackend()` | — |

### Config Resolution Function

Lives in `pkg/agent/config_resolver.go` alongside the existing `ResolveAgentConfig()`. Reuses the same resolution patterns — `ChatConfig` has the same fields as `StageAgentConfig`:

```go
// ResolveChatAgentConfig builds the agent configuration for a chat execution.
// Hierarchy: defaults → agent definition → chain → chat config.
func ResolveChatAgentConfig(
    cfg *config.Config,
    chain *config.ChainConfig,
    chatCfg *config.ChatConfig, // may be nil (defaults apply)
) (*ResolvedAgentConfig, error) {
    // Agent name: chatCfg.Agent → "ChatAgent"
    agentName := "ChatAgent"
    if chatCfg != nil && chatCfg.Agent != "" {
        agentName = chatCfg.Agent
    }

    // Resolve via same hierarchy as ResolveAgentConfig(), but with
    // ChatConfig fields instead of StageConfig/StageAgentConfig fields.
    // Strategy: defaults → agent def → chain → chatCfg
    // Provider: defaults → chain → chatCfg
    // MaxIter:  defaults → agent def → chain → chatCfg
    // MCP:     agent def → chain → chatCfg
    // ... (same pattern as ResolveAgentConfig)
}
```

### MCP Server Resolution for Chat

MCP servers for chat follow a specific priority:

1. **Session MCP selection override**: If the original alert had `mcp_selection`, chat inherits it (the user intended specific servers for this alert)
2. **Chain chat config**: `chain.chat.mcp_servers` if specified
3. **Chain stages aggregate**: Union of all MCP servers from chain stage configs (provides access to all servers the investigation used)

The session's `mcp_selection` is stored on `AlertSession.mcp_selection` and is available at chat time.

---

## Validation Logic

### Chat Eligibility Check

Used by the send-message handler to validate the session before accepting a chat message:

```go
func isChatAvailable(session *ent.AlertSession, chain *config.ChainConfig) (bool, string) {
    // 1. Terminal state check
    if !isTerminalStatus(session.Status) {
        return false, "session is still in progress"
    }

    // 2. Chat enabled check
    if chain.Chat != nil && !chain.Chat.Enabled {
        return false, "chat is not enabled for this chain"
    }
    // If chain.Chat is nil, chat is enabled by default

    // 3. Investigation data check (session must have actually run)
    // Check for at least one timeline event or stage
    if !hasInvestigationData(session) {
        return false, "session has no investigation data"
    }

    return true, ""
}

func isTerminalStatus(status alertsession.Status) bool {
    return status == alertsession.StatusCompleted ||
           status == alertsession.StatusFailed ||
           status == alertsession.StatusCancelled
}
```

### Send Message Guards

Before accepting a new message:
1. Session exists and is terminal (404/400)
2. Chat enabled for chain (400)
3. No active execution for this chat (409 if active — one-at-a-time)
4. System not shutting down (503)

The one-at-a-time check queries for any Stage with `chat_id = chatID` AND `status IN (pending, active)`.

---

## Schema Changes

### No Schema Changes for Chat

Investigation context is built lazily per message from existing timeline events. No new fields on Chat. The existing Chat schema has everything needed: `session_id` (to query timeline events), `chain_id` (for config resolution), `pod_id` + `last_interaction_at` (for orphan detection).

### No Status Field on ChatUserMessage

ChatUserMessage remains a simple record of what the user asked. Processing status is tracked on the response Stage (which has `status: pending → active → completed/failed`). This avoids dual status tracking.

### Stage Index for Chat Stages

Chat stages continue the session's stage index sequence:

```go
// Query max stage_index for this session, increment
maxIndex, _ := stageService.GetMaxStageIndex(ctx, sessionID)
chatStageIndex := maxIndex + 1
```

This ensures correct ordering when viewing a session's complete history (investigation stages + chat response stages).

---

## Internal Types

### ChatExecuteInput

```go
type ChatExecuteInput struct {
    Chat    *ent.Chat
    Message *ent.ChatUserMessage
    Session *ent.AlertSession
}
```

### ChatMessageExecutorConfig

```go
type ChatMessageExecutorConfig struct {
    SessionTimeout time.Duration // Reused from QueueConfig (default: 15 minutes)
}
```

### Extended EventPublisher Interface

```go
// Add to agent.EventPublisher interface
PublishChatCreated(ctx context.Context, sessionID string, payload events.ChatCreatedPayload) error
PublishChatUserMessage(ctx context.Context, sessionID string, payload events.ChatUserMessagePayload) error
```

---

## Integration Points

### Server Wiring (`pkg/api/server.go`)

The `Server` struct gains:
- `chatService *services.ChatService` — for chat CRUD operations
- `chatExecutor *queue.ChatMessageExecutor` — for message execution

New routes registered in `setupRoutes()`:

```go
// Chat endpoint
v1.POST("/sessions/:id/chat/messages", s.sendChatMessageHandler)
// Cancellation: existing POST /sessions/:id/cancel handler extended to also cancel chat executions
// History: existing session timeline API covers chat events (no new endpoint needed)
// Availability: deferred to Phase 7.2 (session GET/list enriched with chat metadata)
```

### Startup Wiring (`main.go` or equivalent)

```go
// Create ChatMessageExecutor (after WorkerPool, shares same dependencies)
chatExecutor := queue.NewChatMessageExecutor(
    cfg, dbClient, llmClient, mcpClientFactory,
    agentFactory, eventPublisher, promptBuilder,
    chatExecutorConfig,
)

// Pass to server
server.SetChatExecutor(chatExecutor)
server.SetChatService(chatService)

// Shutdown: stop chat executor before worker pool
chatExecutor.Stop()
workerPool.Stop()
```

### ChatService Enhancements

Minimal changes to the existing `ChatService`:

1. **BuildChatContext**: Removed or deprecated — context building moves to the executor (uses `TimelineService.GetSessionTimeline()` + `FormatInvestigationContext()` directly)
2. **GetOrCreateChat**: New method — get-or-create pattern for the send-message handler. Returns the existing Chat or creates one. Uses a unique constraint on `session_id` to handle race conditions (INSERT ON CONFLICT DO NOTHING + SELECT fallback)

### Agent Framework (No Changes)

The agent framework requires **no changes**. Controllers already check `execCtx.ChatContext != nil` to switch between investigation and chat prompting. The prompt builder already has chat-specific methods. The `ExecutionContext.ChatContext` field is already defined.

---

## Testing Strategy

### Unit Tests

| Component | Test Focus |
|-----------|------------|
| Chat API handlers | Request validation, error responses, author extraction |
| ChatMessageExecutor | One-at-a-time enforcement, cancellation, graceful shutdown, heartbeat, event cleanup |
| Chat context building | user_question event creation, GetSessionTimeline → FormatInvestigationContext integration |
| Chat config resolution | Hierarchy resolution, fallbacks, MCP server resolution |
| Chat eligibility validation | Terminal status check, chain config check, investigation data check |

### Integration Tests (testcontainers)

| Test | What It Validates |
|------|-------------------|
| First message creates chat | Send message auto-creates chat, full execution pipeline with mock LLM |
| Chat context accumulation | 2nd message sees investigation + 1st exchange via unified timeline |
| One-at-a-time enforcement | Second message rejected while first is processing |
| Cancellation | Active execution cancelled, stage marked failed |
| Chat rejected for in-progress session | Send message returns 400 for non-terminal session |

### Existing Test Preservation

The existing PromptBuilder chat tests, investigation formatter tests, and ChatService tests continue to pass — this phase extends them, not replaces them.

---

## Code Reuse & Refactoring

Several pieces of `RealSessionExecutor` and `Worker` logic are needed by `ChatMessageExecutor`. Rather than duplicating, extract shared code to appropriate locations.

### Config Resolution → `pkg/agent/config_resolver.go`

Add `ResolveChatAgentConfig()` alongside the existing `ResolveAgentConfig()`. Chat config resolution is a variant of the same hierarchy logic — `ChatConfig` has the same fields (`Agent`, `IterationStrategy`, `LLMProvider`, `MCPServers`, `MaxIterations`) as `StageAgentConfig`. The new function takes `*config.Config`, `*config.ChainConfig`, and `*config.ChatConfig`, constructs the resolution chain (defaults → agent definition → chain → chat config), and returns `*ResolvedAgentConfig`. This keeps all config resolution in one file and avoids duplicating the provider lookup, strategy resolution, and fallback logic.

### MCP Helpers → package-level functions in `pkg/queue/executor.go`

Two methods on `RealSessionExecutor` need to become package-level functions so `ChatMessageExecutor` can call them (both are in the same `pkg/queue/` package):

1. **`createToolExecutor()`** → `createToolExecutor(ctx, mcpFactory, serverIDs, toolFilter, logger)`. Drop the receiver; pass `mcpFactory` as a parameter. The logic (factory exists + servers > 0 → use factory, else stub) is unchanged.

2. **`resolveMCPSelection()`** → `resolveMCPSelection(session, resolvedConfig, mcpRegistry)`. Drop the receiver; pass `mcpRegistry` as a parameter. The logic (parse session's `mcp_selection` override, validate against registry, build serverIDs + toolFilter) is unchanged.

Both stay in `executor.go` — they're execution helpers, not generic utilities. The only change is removing the `RealSessionExecutor` receiver and passing dependencies as parameters.

### Status Mappers → already shared

`mapAgentStatusToEntStatus()`, `mapAgentStatusToSessionStatus()`, and `mapTerminalStatus()` are already package-level functions in `executor.go`. Since `chat_executor.go` is in the same `pkg/queue/` package, they're already accessible. **No extraction needed.**

### Stage Status Publishing → package-level function in `pkg/queue/executor.go`

`publishStageStatus()` is currently a method on `RealSessionExecutor`. Extract to package-level: `publishStageStatus(eventPublisher, sessionID, stageID, stageName, stageIndex, status)`. Both executors use it.

### Heartbeat & Event Cleanup → `pkg/queue/chat_executor.go` (re-implement pattern)

`Worker.runHeartbeat()` and `Worker.scheduleEventCleanup()` operate on `AlertSession` and use `Worker`'s `client` field. Chat equivalents operate on `Chat` and use `ChatMessageExecutor`'s `client` field. The pattern is identical (ticker + update / AfterFunc + delete) but the target entity differs. **Re-implement the pattern** in `chat_executor.go` rather than over-abstracting a shared "heartbeat runner." Two simple functions, each < 15 lines.

### Prompt Builder → `pkg/agent/prompt/chat.go` (keep, clean up)

`buildChatUserMessage()` is reusable and needed — it builds the chat prompt (investigation context + current question + tools). **Keep it.** Just remove the `FormatChatHistory()` call and the `ChatHistory` block from it. `FormatChatHistory()` and `pluralS()` are deleted.

### Summary

| What | Where | Action |
|------|-------|--------|
| `ResolveChatAgentConfig()` | `pkg/agent/config_resolver.go` | **Add** new function alongside existing resolver |
| `createToolExecutor()` | `pkg/queue/executor.go` | **Refactor** from method to package-level function |
| `resolveMCPSelection()` | `pkg/queue/executor.go` | **Refactor** from method to package-level function |
| `publishStageStatus()` | `pkg/queue/executor.go` | **Refactor** from method to package-level function |
| Status mappers | `pkg/queue/executor.go` | **No change** — already package-level, already shared |
| Heartbeat / cleanup | `pkg/queue/chat_executor.go` | **Re-implement** pattern (different target entity) |
| `buildChatUserMessage()` | `pkg/agent/prompt/chat.go` | **Keep**, remove `FormatChatHistory` call |
| `FormatChatHistory()` | `pkg/agent/prompt/chat.go` | **Delete** |

---

## Implementation Plan

### Step 0: Refactoring for Reuse

1. Extract `createToolExecutor()`, `resolveMCPSelection()`, `publishStageStatus()` from `RealSessionExecutor` methods to package-level functions in `pkg/queue/executor.go` (drop receiver, pass deps as params)
2. Add `ResolveChatAgentConfig()` to `pkg/agent/config_resolver.go`
3. Clean up `pkg/agent/prompt/chat.go`: remove `FormatChatHistory()` call from `buildChatUserMessage()`, delete `FormatChatHistory()` and `pluralS()`
4. Clean up `pkg/agent/context.go`: delete `ChatExchange` struct, remove `ChatHistory` field from `ChatContext`
5. Clean up `pkg/services/chat_service.go`: delete `GetChatHistory()`; `pkg/models/chat.go`: delete `ChatHistoryResponse`
6. Update affected tests (`builder_test.go`, `builder_integration_test.go`, `chat_test.go`, `chat_service_test.go`)
7. Verify existing tests pass — this step is pure refactoring, no new behavior

### Step 1: Service Changes

1. Add `GetMaxStageIndex()` to StageService
2. Add `GetActiveStageForChat()` query (for one-at-a-time check)
3. Add `GetOrCreateChat()` to ChatService (get-or-create pattern for send-message handler)

### Step 2: ChatMessageExecutor

1. Create `ChatMessageExecutor` in `pkg/queue/chat_executor.go`
2. Implement `Submit()`, `execute()`, `CancelExecution()`, `Stop()`
3. Implement `buildChatContext()` — calls `GetSessionTimeline()` + `FormatInvestigationContext()` (no filter — both functions already exist)
4. Create `user_question` timeline event at start of each message execution (before agent runs)
5. Chat config resolution calls `ResolveChatAgentConfig()` from Step 0
6. MCP tool executor creation calls shared `createToolExecutor()` from Step 0
7. Implement one-at-a-time check
8. Implement heartbeat (`runChatHeartbeat()`) — periodic `Chat.last_interaction_at` + `pod_id` updates
9. Implement stage event cleanup (`cleanupStageEvents()`) — 60s grace period after terminal status
10. Test: one-at-a-time enforcement, cancellation, shutdown, config resolution, context assembly, heartbeat, cleanup

### Step 3: API Handlers

1. Create chat handler file in `pkg/api/`
2. Implement `POST /sessions/:id/chat/messages` (auto-creates chat on first message)
3. Extend existing `cancelSessionHandler` to also cancel chat executions via `ChatMessageExecutor.CancelExecution()`
4. Wire into `Server.setupRoutes()`
5. Wire `ChatService` and `ChatMessageExecutor` into `Server`
6. Test: request validation, error mapping, response formats, get-or-create chat, unified cancel

### Step 4: WebSocket Events

1. Add `ChatCreatedPayload` and `ChatUserMessagePayload` to `pkg/events/payloads.go`
2. Add `EventTypeChatCreated` and `EventTypeChatUserMessage` constants
3. Add `PublishChatCreated()` and `PublishChatUserMessage()` to `EventPublisher`
4. Update `agent.EventPublisher` interface
5. Test: event publishing and payload format

### Step 5: Startup Wiring & Integration

1. Wire `ChatMessageExecutor` creation in startup
2. Wire `ChatService` and `ChatMessageExecutor` into Server
3. Add shutdown ordering (chat executor stops before worker pool)
4. Integration tests: full end-to-end chat flow with testcontainers

### Step 6: Configuration

1. No new config — `ChatMessageExecutor` reuses `SessionTimeout` from existing queue config
2. Update config examples in `deploy/config/` if needed (document that timeout applies to chat too)
