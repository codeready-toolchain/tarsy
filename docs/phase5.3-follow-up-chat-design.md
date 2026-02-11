# Phase 5.3: Follow-up Chat — Design

## Overview

Add follow-up chat capability so users can ask questions about completed investigations. A chat is a 1:1 extension of an `AlertSession` — after an investigation completes (or fails/is cancelled), users create a chat and send messages. Each message triggers a single-agent execution with access to the same MCP tools, producing a streamed response.

**Current state**: Chat infrastructure exists (ent schemas, ChatService, ChatContext, prompt builder chat branching, investigation formatter, chat instructions/templates, ChatAgent config, chain ChatConfig). No execution pipeline, no API surface.

**Target state**: Full end-to-end chat: API handlers → queue-based execution → streaming response via existing WebSocket. Chat messages are routed through a dedicated chat executor with concurrency control to limit concurrent LLM usage.

**Design principle**: Chat is a prompt concern, not a controller concern. The same controllers (ReAct, NativeThinking) handle both investigation and chat — the `ChatContext` on `ExecutionContext` triggers chat-specific prompting. No separate chat controllers.

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
     → Captures investigation context from timeline events
     → Creates Chat record (with investigation_context)
     → Publishes chat.created event
     → Returns { chat_id, session_id, created_at }

3. User sends message:
   POST /api/v1/chats/:id/messages  { content: "Why is pod X crashing?" }
     → Validates chat exists, no active execution, content valid
     → Creates ChatUserMessage record
     → Publishes chat.user_message event
     → Submits to ChatMessageExecutor (async, returns immediately)
     → Returns { message_id, stage_id }

4. ChatMessageExecutor processes (async):
     → Acquires concurrency slot
     → Builds ChatContext (investigation + chat history + question)
     → Resolves chat agent config from chain
     → Creates Stage (chat_id, chat_user_message_id) + AgentExecution
     → Publishes stage.status: started
     → Runs agent.Execute() (same controllers as investigation)
     → Agent streams response via existing WebSocket events
     → Updates Stage/AgentExecution terminal status
     → Publishes stage.status: completed/failed
     → Releases concurrency slot

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

A new component in `pkg/queue/` that manages chat message execution with concurrency control. Separate from the session `WorkerPool` but follows similar patterns.

```go
// ChatMessageExecutor processes chat messages through the agent framework
// with bounded concurrency to limit LLM usage.
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

    // Concurrency control
    sem              chan struct{}   // bounded semaphore (max concurrent chat executions)

    // Active execution tracking (for cancellation)
    mu               sync.RWMutex
    activeExecs      map[string]context.CancelFunc  // chatID → cancel
}
```

**Why separate from WorkerPool**: Session workers use `FOR UPDATE SKIP LOCKED` polling on the `AlertSession` table. Chat execution is triggered by API calls, not by polling. Mixing the two would either require a second polling query in the worker loop (added latency for alert processing) or a fundamentally different triggering mechanism. A separate executor is cleaner and allows independent concurrency tuning.

### Submit Flow

```go
func (e *ChatMessageExecutor) Submit(ctx context.Context, input ChatExecuteInput) (stageID string, err error) {
    // 1. Check one-at-a-time constraint
    //    Query: any Stage with chat_id = input.ChatID AND status IN (pending, active)?
    //    If yes → return ErrChatExecutionActive

    // 2. Create Stage record (status: pending)
    //    stage_name: "Chat Response"
    //    chat_id, chat_user_message_id set
    //    stage_index: max(session stages) + 1

    // 3. Return stageID to caller (for immediate API response)

    // 4. Launch goroutine (respects semaphore)
    go e.execute(ctx, input, stageID)

    return stageID, nil
}
```

### Execute Flow

```go
func (e *ChatMessageExecutor) execute(ctx context.Context, input ChatExecuteInput, stageID string) {
    // Acquire concurrency slot (blocks if at capacity)
    e.sem <- struct{}{}
    defer func() { <-e.sem }()

    // Create cancellable context
    execCtx, cancel := context.WithTimeout(ctx, e.config.ChatTimeout)
    defer cancel()

    // Register for cancellation
    e.registerExecution(input.ChatID, cancel)
    defer e.unregisterExecution(input.ChatID)

    // Update Stage status: active
    // Publish stage.status: started

    // 1. Build ChatContext
    chatContext := e.buildChatContext(execCtx, input)

    // 2. Resolve chat agent config
    agentConfig := e.resolveChatAgentConfig(input.Chat.ChainID)

    // 3. Create AgentExecution record

    // 4. Create MCP ToolExecutor (per-execution isolation, same as investigation)
    toolExec, mcpClient, err := e.mcpClientFactory.CreateToolExecutor(...)
    if toolExec != nil { defer toolExec.Close() }

    // 5. Build ExecutionContext (with ChatContext populated)
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

    // 6. Create agent via AgentFactory → BaseAgent with appropriate Controller
    chatAgent := e.agentFactory.CreateAgent(agentExecCtx)

    // 7. Execute agent (same path as investigation — controller handles chat via ChatContext)
    result, err := chatAgent.Execute(execCtx, agentExecCtx, "")  // no prevStageContext for chat

    // 8. Update AgentExecution + Stage terminal status

    // 9. Update Chat.last_interaction_at (heartbeat equivalent)
}
```

### Concurrency Control

- `MaxConcurrentChats` config value (default: 3, configurable in `queue` config section)
- Bounded channel (`sem`) acts as semaphore
- When at capacity: goroutine blocks on `e.sem <- struct{}{}` until a slot opens
- The Stage is created BEFORE acquiring the semaphore (so the API returns immediately with a stage ID)
- Stage status transitions: `pending` (created) → `active` (semaphore acquired) → terminal

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

On shutdown:
1. Stop accepting new submissions (reject with 503)
2. Cancel all active executions (cancel contexts)
3. Wait for goroutines to finish (with timeout)

Uses the same pattern as `WorkerPool.Stop()`.

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
5. Capture investigation context (timeline events → `FormatInvestigationContext()`)
6. `ChatService.CreateChat()` with investigation context
7. Publish `chat.created` event
8. Return chat response

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

### Investigation Context

Captured **once at chat creation time** and stored on the `Chat` record. This provides:
- **Consistency**: All messages in a chat see the same investigation context
- **Performance**: No re-querying timeline events for each message
- **Correctness**: Investigation data is immutable after session completes

**Capture flow**:
1. Query session's timeline events (excluding chat stages, executive summary)
2. Filter out `llm_thinking` events (too verbose, internal reasoning — matches old TARSy's `include_thinking=False`)
3. Format via `FormatInvestigationContext()` (shared with synthesis formatting)
4. Store formatted text in `Chat.investigation_context` field

**Event type filtering for context**:
- **Include**: `llm_response`, `final_analysis`, `llm_tool_call` (with summary dedup via `formatTimelineEvents()`), `code_execution`, `google_search_result`, `url_context_result`, `error`
- **Exclude**: `llm_thinking` (verbose internal reasoning), `executive_summary` (session-level, not investigation detail), `mcp_tool_summary` (deduplicated into tool calls by formatter)

### Chat History

Built **per-message** from previous chat exchanges in the same chat.

**Build flow**:
1. Query ChatUserMessages for the chat (ordered by created_at)
2. For each message (except the current one), find its response Stage
3. Query the Stage's timeline events (final_analysis event → response content)
4. Build `[]ChatExchange` with `UserQuestion` + `Messages`
5. Pass to `ChatContext.ChatHistory`

The prompt builder's existing `FormatChatHistory()` formats these exchanges into the prompt.

**What goes into chat history**:
- User question text
- Assistant's final analysis (the actual answer)
- NOT intermediate tool calls or thinking (keeps history concise)

### ChatContext Assembly

```go
func (e *ChatMessageExecutor) buildChatContext(ctx context.Context, input ChatExecuteInput) *agent.ChatContext {
    return &agent.ChatContext{
        UserQuestion:         input.Message.Content,
        InvestigationContext: input.Chat.InvestigationContext, // pre-captured
        ChatHistory:          e.buildChatHistory(ctx, input.Chat.ID, input.Message.ID),
    }
}
```

### Context Size Considerations

Investigation context can be large (multi-stage investigations with many tool calls). The existing `FormatInvestigationContext()` includes tool call results (or summaries), which can be substantial. For very large investigations:

- Tool call/summary deduplication already helps (existing formatter feature)
- Thinking exclusion reduces size significantly
- Future: could add a max context size with truncation (not in scope for this phase)

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

### Chat: Add `investigation_context` Field

Add a `Text` field to store the pre-captured investigation context:

```go
field.Text("investigation_context").
    Optional().
    Nillable().
    Comment("Pre-captured investigation context from timeline events"),
```

This replaces old TARSy's `conversation_history` field. The content is the output of `FormatInvestigationContext()` — formatted timeline events from the completed investigation.

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
    MaxConcurrentChats int           // Default: 3
    ChatTimeout        time.Duration // Default: 10 minutes
    ShutdownTimeout    time.Duration // Default: 30 seconds
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

The existing `ChatService` needs enhancements:

1. **CreateChat**: Accept and store `investigation_context`
2. **BuildChatContext**: Replaced by executor-level context building (the method can remain for backward compat but the executor calls `buildChatContext` directly using the stored investigation context)
3. **GetNextStageIndex**: New method to query max stage index for a session

### Agent Framework (No Changes)

The agent framework requires **no changes**. Controllers already check `execCtx.ChatContext != nil` to switch between investigation and chat prompting. The prompt builder already has chat-specific methods. The `ExecutionContext.ChatContext` field is already defined.

---

## Testing Strategy

### Unit Tests

| Component | Test Focus |
|-----------|------------|
| Chat API handlers | Request validation, error responses, author extraction |
| ChatMessageExecutor | Concurrency limiting, one-at-a-time enforcement, cancellation, shutdown |
| Chat context building | Investigation context capture, event filtering, chat history assembly |
| Chat config resolution | Hierarchy resolution, fallbacks, MCP server resolution |
| Chat availability | Terminal status check, chain config check, investigation data check |

### Integration Tests (testcontainers)

| Test | What It Validates |
|------|-------------------|
| Create chat for completed session | End-to-end chat creation with investigation context capture |
| Send message and get response | Full execution pipeline with mock LLM |
| Chat history accumulation | Multiple messages, each sees previous exchanges |
| One-at-a-time enforcement | Second message rejected while first is processing |
| Cancellation | Active execution cancelled, stage marked failed |
| Concurrency limit | Messages beyond limit queue up |
| Chat not available for in-progress session | Availability guard rejects |

### Existing Test Preservation

The existing PromptBuilder chat tests, investigation formatter tests, and ChatService tests continue to pass — this phase extends them, not replaces them.

---

## Implementation Plan

### Step 1: Schema & Service Changes

1. Add `investigation_context` field to Chat ent schema
2. Run `go generate ./ent` to regenerate
3. Create migration for the new field
4. Enhance `ChatService.CreateChat()` to accept investigation context
5. Add `GetMaxStageIndex()` to StageService
6. Add `GetActiveStageForChat()` query (for one-at-a-time check)

### Step 2: Investigation Context Capture

1. Create `CaptureInvestigationContext()` function in `pkg/agent/context/`
   - Queries session timeline events (excluding chat stages)
   - Filters out `llm_thinking` events
   - Calls `FormatInvestigationContext()`
2. Add event type filtering helper
3. Test context capture with various investigation shapes (single-stage, multi-stage, multi-agent with synthesis)

### Step 3: Chat History Builder

1. Create `BuildChatHistory()` function in `pkg/agent/context/` or `pkg/services/`
   - Queries ChatUserMessages for the chat
   - For each, finds response Stage and extracts final_analysis
   - Returns `[]agent.ChatExchange`
2. Test with 0, 1, multiple previous exchanges

### Step 4: ChatMessageExecutor

1. Create `ChatMessageExecutor` in `pkg/queue/chat_executor.go`
2. Implement `Submit()`, `execute()`, `CancelExecution()`, `Stop()`
3. Implement `buildChatContext()` (combines investigation context + chat history)
4. Implement `resolveChatAgentConfig()` (config hierarchy resolution)
5. Implement concurrency control (semaphore channel)
6. Implement one-at-a-time check
7. Test: concurrency limiting, cancellation, config resolution, context assembly

### Step 5: API Handlers

1. Create chat handler file(s) in `pkg/api/`
2. Implement all 6 endpoints
3. Wire into `Server.setupRoutes()`
4. Wire `ChatService` and `ChatMessageExecutor` into `Server`
5. Test: request validation, error mapping, response formats

### Step 6: WebSocket Events

1. Add `ChatCreatedPayload` and `ChatUserMessagePayload` to `pkg/events/payloads.go`
2. Add `EventTypeChatCreated` and `EventTypeChatUserMessage` constants
3. Add `PublishChatCreated()` and `PublishChatUserMessage()` to `EventPublisher`
4. Update `agent.EventPublisher` interface
5. Test: event publishing and payload format

### Step 7: Startup Wiring & Integration

1. Wire `ChatMessageExecutor` creation in startup
2. Wire `ChatService` and `ChatMessageExecutor` into Server
3. Add shutdown ordering (chat executor stops before worker pool)
4. Integration tests: full end-to-end chat flow with testcontainers

### Step 8: Configuration

1. Add `MaxConcurrentChats` and `ChatTimeout` to queue config
2. Update config examples in `deploy/config/`
