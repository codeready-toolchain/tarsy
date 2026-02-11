# Phase 5.3: Follow-up Chat — Design

## Overview

Add follow-up chat capability so users can ask questions about completed investigations. A chat is a 1:1 extension of an `AlertSession` — after an investigation completes (or fails/is cancelled), users create a chat and send messages. Each message triggers a single-agent execution with access to the same MCP tools, producing a streamed response.

**Current state**: Chat infrastructure exists (ent schemas, ChatService, ChatContext, prompt builder chat branching, investigation formatter, chat instructions/templates, ChatAgent config, chain ChatConfig). No execution pipeline, no API surface.

**Target state**: Full end-to-end chat: API handlers → async execution → streaming response via existing WebSocket. Each chat message spawns a goroutine that runs the agent framework — no pool, no queue, no concurrency limits. Chats are user-initiated and rare; complexity is not warranted.

**Design principle**: Chat is a prompt concern, not a controller concern. The same controllers (ReAct, NativeThinking) handle both investigation and chat — the `ChatContext` on `ExecutionContext` triggers chat-specific prompting. No separate chat controllers. Same iteration limits, same `forceConclusion()` at `MaxIterations`, same per-iteration timeout.

---

## Chat Lifecycle

### End-to-End Flow

```
1. User checks availability:
   GET /api/v1/sessions/:id/chat-available
     → session terminal? chat enabled? has investigation data?
     → Returns { available: true/false, chat_id?: "..." }

2. User creates chat (once per session):
   POST /api/v1/sessions/:id/chat
     → Validates terminal session + chat enabled + no existing chat
     → Creates Chat record (lightweight — no context capture)
     → Publishes chat.created event
     → Returns { chat_id, session_id, created_at }

3. User sends message:
   POST /api/v1/chats/:id/messages  { content: "Why is pod X crashing?" }
     → Validates chat exists, no active execution, content valid
     → Creates ChatUserMessage record
     → Publishes chat.user_message event
     → Submits to ChatMessageExecutor (async, returns immediately)
     → Returns { message_id, stage_id }

4. ChatMessageExecutor processes (goroutine):
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

5. User gets history:
   GET /api/v1/chats/:id/history
     → Returns chat + user messages + response stages (ordered)

6. User cancels active execution:
   POST /api/v1/chats/:id/cancel
     → Cancels context for active execution
     → Agent stops, Stage marked failed
```

### Lifecycle Constraints

- **One chat per session**: `AlertSession` → `Chat` is 1:1 (existing schema enforces uniqueness on `session_id`).
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
    client           *ent.Client
    chainRegistry    *config.ChainRegistry
    agentRegistry    *config.AgentRegistry
    providerRegistry *config.LLMProviderRegistry
    mcpClientFactory *mcp.ClientFactory
    agentFactory     *agent.AgentFactory
    eventPublisher   agent.EventPublisher
    promptBuilder    agent.PromptBuilder

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
    //    Query: any Stage with chat_id = input.ChatID AND status IN (pending, active)?
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
    execCtx, cancel := context.WithTimeout(ctx, e.config.SessionTimeout)
    defer cancel()

    // Register for cancellation
    e.registerExecution(input.ChatID, cancel)
    defer e.unregisterExecution(input.ChatID)

    // 1. Resolve chat agent config
    agentConfig := e.resolveChatAgentConfig(input.Chat.ChainID)

    // 2. Create AgentExecution record

    // 3. Create user_question timeline event (before building context, so it's included)
    e.timelineService.CreateTimelineEvent(execCtx, models.CreateTimelineEventRequest{
        SessionID:   input.Session.ID,
        StageID:     &stageID,
        ExecutionID: &executionID,
        EventType:   timelineevent.EventTypeUserQuestion,
        Content:     input.Message.Content,
    })

    // 4. Build ChatContext (GetSessionTimeline → FormatInvestigationContext)
    chatContext := e.buildChatContext(execCtx, input)

    // Update Stage status: active
    // Publish stage.status: started
    // Start heartbeat
    go e.runChatHeartbeat(execCtx, input.Chat.ID)

    // 6. Create MCP ToolExecutor (per-execution isolation, same as investigation)
    toolExec, mcpClient, err := e.mcpClientFactory.CreateToolExecutor(...)
    if toolExec != nil { defer toolExec.Close() }

    // 7. Build ExecutionContext (with ChatContext populated)
    agentExecCtx := &agent.ExecutionContext{
        SessionID:   input.Session.ID,
        StageID:     stageID,
        ExecutionID: executionID,
        AgentName:   agentConfig.AgentName,
        AlertData:   input.Session.AlertData,
        AlertType:   input.Session.AlertType,
        Config:      agentConfig,
        LLMClient:   llmClient,
        ToolExecutor: toolExec,
        EventPublisher: e.eventPublisher,
        Services:    serviceBundle,
        PromptBuilder: e.promptBuilder,
        ChatContext: chatContext,
        FailedServers: failedServers,
    }

    // 8. Create agent via AgentFactory → BaseAgent with appropriate Controller
    chatAgent := e.agentFactory.CreateAgent(agentExecCtx)

    // 9. Execute agent (same path as investigation — controller handles chat via ChatContext)
    result, err := chatAgent.Execute(execCtx, agentExecCtx, "")  // no prevStageContext for chat

    // 10. Update AgentExecution + Stage terminal status
    // 11. Publish stage.status: completed/failed

    // 12. Stop heartbeat, schedule event cleanup
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

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/api/v1/sessions/:id/chat` | Create chat for session |
| GET | `/api/v1/sessions/:id/chat-available` | Check chat availability |
| GET | `/api/v1/chats/:id` | Get chat details |
| POST | `/api/v1/chats/:id/messages` | Send message |
| GET | `/api/v1/chats/:id/messages` | Get chat message history |
| POST | `/api/v1/chats/:id/cancel` | Cancel active execution |

### POST `/api/v1/sessions/:id/chat` — Create Chat

**Request**: No body (session ID from path, author from headers).

**Response** (201):
```json
{
  "chat_id": "uuid",
  "session_id": "uuid",
  "created_by": "user@example.com",
  "created_at": "2026-02-11T10:00:00Z"
}
```

**Errors**:
- 404: Session not found
- 400: Session not in terminal state
- 409: Chat already exists for this session
- 400: Chat not enabled for this chain

**Handler flow**:
1. Extract author from `X-Forwarded-User` / `X-Forwarded-Email` / `"api-client"`
2. Get session, validate terminal status
3. Resolve chain config, validate `chat.enabled`
4. Check for existing chat (409 if exists)
5. `ChatService.CreateChat()` (no context capture — built lazily per message)
6. Publish `chat.created` event
7. Return chat response

### GET `/api/v1/sessions/:id/chat-available` — Check Availability

**Response** (200):
```json
{
  "available": true,
  "chat_id": "uuid-or-null",
  "reason": "string-if-not-available"
}
```

**Checks** (in order):
1. Session exists
2. If chat already exists → `available: true, chat_id: "..."`
3. Session in terminal state (completed/failed/cancelled)
4. Session has at least one timeline event (investigation actually ran)
5. Chain config has `chat.enabled: true`

If any check fails, `available: false` with `reason` explaining why.

### POST `/api/v1/chats/:id/messages` — Send Message

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
  "message_id": "uuid",
  "stage_id": "uuid",
  "chat_id": "uuid"
}
```

Returns 202 because processing is asynchronous. The `stage_id` allows the client to track the response via WebSocket events on the session channel.

**Errors**:
- 404: Chat not found
- 409: Active execution in progress (one-at-a-time)
- 400: Empty or oversized content
- 503: Shutting down

**Handler flow**:
1. Validate content
2. Extract author
3. `ChatService.AddChatMessage()` → creates ChatUserMessage
4. Publish `chat.user_message` event
5. `ChatMessageExecutor.Submit()` → creates Stage, launches async execution
6. Return message_id + stage_id

### GET `/api/v1/chats/:id` — Get Chat

**Response** (200):
```json
{
  "chat_id": "uuid",
  "session_id": "uuid",
  "created_by": "user@example.com",
  "created_at": "2026-02-11T10:00:00Z"
}
```

### GET `/api/v1/chats/:id/messages` — Get Message History

**Response** (200):
```json
{
  "messages": [
    {
      "message_id": "uuid",
      "content": "Why is pod X crashing?",
      "author": "user@example.com",
      "created_at": "2026-02-11T10:05:00Z",
      "stage_id": "uuid",
      "stage_status": "completed"
    }
  ]
}
```

Each message is enriched with its response stage info (stage_id, status). This gives the client a complete picture of the conversation without separate stage queries.

### POST `/api/v1/chats/:id/cancel` — Cancel Execution

**Response** (200):
```json
{
  "cancelled": true
}
```

**Errors**:
- 404: Chat not found
- 409: No active execution to cancel

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

### ChatContext Simplification

With unified timeline context, `ChatContext` no longer needs a separate `ChatHistory` field:

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

```go
func (e *ChatMessageExecutor) resolveChatAgentConfig(chainID string) *agent.ResolvedAgentConfig {
    chain := e.chainRegistry.Get(chainID)
    chatCfg := chain.Chat  // may be nil

    // Agent name
    agentName := "ChatAgent"
    if chatCfg != nil && chatCfg.Agent != "" {
        agentName = chatCfg.Agent
    }

    // Look up agent config from registry
    agentCfg := e.agentRegistry.Get(agentName)

    // Iteration strategy: chat config → chain → defaults
    strategy := resolveStrategy(chatCfg, chain, defaults)

    // LLM provider: chat config → chain → defaults
    provider := resolveProvider(chatCfg, chain, defaults)

    // MCP servers: chat config → session override → chain stages
    servers := resolveMCPServers(chatCfg, session, chain)

    // Max iterations: chat config → chain → defaults
    maxIter := resolveMaxIterations(chatCfg, chain, defaults)

    return &agent.ResolvedAgentConfig{
        AgentName:         agentName,
        IterationStrategy: strategy,
        LLMProvider:       providerConfig,
        MaxIterations:     maxIter,
        IterationTimeout:  defaults.IterationTimeout,
        MCPServers:        servers,
        CustomInstructions: agentCfg.CustomInstructions,
        Backend:           agent.ResolveBackend(strategy),
    }
}
```

### MCP Server Resolution for Chat

MCP servers for chat follow a specific priority:

1. **Session MCP selection override**: If the original alert had `mcp_selection`, chat inherits it (the user intended specific servers for this alert)
2. **Chain chat config**: `chain.chat.mcp_servers` if specified
3. **Chain stages aggregate**: Union of all MCP servers from chain stage configs (provides access to all servers the investigation used)

The session's `mcp_selection` is stored on `AlertSession.mcp_selection` and is available at chat time.

---

## Chat Availability Guards

### Availability Check Logic

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
1. Chat exists (404 if not)
2. No active execution for this chat (409 if active — one-at-a-time)
3. System not shutting down (503)

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
// Chat endpoints
v1.POST("/sessions/:id/chat", s.createChatHandler)
v1.GET("/sessions/:id/chat-available", s.chatAvailableHandler)
v1.GET("/chats/:id", s.getChatHandler)
v1.POST("/chats/:id/messages", s.sendChatMessageHandler)
v1.GET("/chats/:id/messages", s.getChatMessagesHandler)
v1.POST("/chats/:id/cancel", s.cancelChatHandler)
```

### Startup Wiring (`main.go` or equivalent)

```go
// Create ChatMessageExecutor (after WorkerPool, shares same dependencies)
chatExecutor := queue.NewChatMessageExecutor(
    dbClient, chainRegistry, agentRegistry, providerRegistry,
    mcpClientFactory, agentFactory, eventPublisher, promptBuilder,
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
2. **CreateChat**: No changes needed (existing implementation is sufficient)

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
| Chat availability | Terminal status check, chain config check, investigation data check |

### Integration Tests (testcontainers)

| Test | What It Validates |
|------|-------------------|
| Create chat for completed session | End-to-end chat creation, availability validation |
| Send message and get response | Full execution pipeline with mock LLM |
| Chat context accumulation | 2nd message sees investigation + 1st exchange via unified timeline |
| One-at-a-time enforcement | Second message rejected while first is processing |
| Cancellation | Active execution cancelled, stage marked failed |
| Chat not available for in-progress session | Availability guard rejects |

### Existing Test Preservation

The existing PromptBuilder chat tests, investigation formatter tests, and ChatService tests continue to pass — this phase extends them, not replaces them.

---

## Implementation Plan

### Step 1: Service Changes

1. Add `GetMaxStageIndex()` to StageService
2. Add `GetActiveStageForChat()` query (for one-at-a-time check)

### Step 2: ChatMessageExecutor

1. Create `ChatMessageExecutor` in `pkg/queue/chat_executor.go`
2. Implement `Submit()`, `execute()`, `CancelExecution()`, `Stop()`
3. Implement `buildChatContext()` — calls `GetSessionTimeline()` + `FormatInvestigationContext()` (no filter — both functions already exist)
4. Create `user_question` timeline event at start of each message execution (before agent runs)
5. Implement `resolveChatAgentConfig()` (config hierarchy resolution)
6. Implement one-at-a-time check
7. Implement heartbeat (`runChatHeartbeat()`) — periodic `Chat.last_interaction_at` + `pod_id` updates
8. Implement stage event cleanup (`cleanupStageEvents()`) — 60s grace period after terminal status
9. Test: one-at-a-time enforcement, cancellation, shutdown, config resolution, context assembly, heartbeat, cleanup

### Step 3: API Handlers

1. Create chat handler file(s) in `pkg/api/`
2. Implement all 6 endpoints
3. Wire into `Server.setupRoutes()`
4. Wire `ChatService` and `ChatMessageExecutor` into `Server`
5. Test: request validation, error mapping, response formats

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
