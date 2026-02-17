# TARSy Architecture Context

Cumulative architectural knowledge from all completed phases. Read this alongside `project-plan.md` for full context when designing or implementing new phases.

**Last updated after**: Phase 6 (E2E Testing)

---

## Architecture Overview

### Go/Python Split

**Go Orchestrator** owns all orchestration: agent lifecycle, iteration control loops, MCP tool execution, prompt building, conversation management, chain execution, state persistence, WebSocket streaming.

**Python LLM Service** is a thin, stateless LLM API proxy: receives messages + config via gRPC, calls LLM provider API, streams response chunks back (text, thinking, tool calls, grounding). Zero state, zero orchestration, zero MCP. Exists solely because LLM provider SDKs have best support in Python.

Communication: gRPC with insecure credentials (same pod). RPC: `Generate(GenerateRequest) returns (stream GenerateResponse)`.

### Five-Layer Data Model

```
AlertSession (session metadata, status, alert data)
  └─ Stage (chain stage — configuration + coordination)
       └─ AgentExecution (individual agent work within a stage)
            ├─ Layer 1: TimelineEvent (UX timeline — what the user sees)
            ├─ Layer 2: Message (LLM conversation — linear, no duplication)
            └─ Layer 3-4: LLMInteraction / MCPInteraction (trace/observability)
  └─ Event (WebSocket distribution — transient)
  └─ Chat → ChatUserMessage (follow-up chat)
```

Key design: **no stored output fields** on Stage or AgentExecution. Context is built lazily via `Agent.BuildStageContext()` when the next stage needs it.

### Package Layout

```
pkg/
├── api/              # HTTP handlers (session, timeline, trace, chat, health), requests, responses, error mapping
├── agent/            # Agent interface, lifecycle, LLM client, tool executor
│   ├── controller/   # Iteration controllers (FunctionCalling, Synthesis), tool execution, summarization
│   ├── context/      # Context formatter, investigation formatter, stage context builder
│   └── prompt/       # Prompt builder, templates, instructions, components
├── config/           # Loader, registries, builtin config, enums, validator
├── database/         # Client, migrations
├── events/           # EventPublisher, ConnectionManager, NotifyListener
├── masking/          # Data masking service (regex patterns, code maskers, K8s Secret masker)
├── mcp/              # MCP client infrastructure (client, executor, transport, health, testing helpers)
├── models/           # MCP selection, trace API response types, shared types
├── queue/            # Worker, WorkerPool, orphan detection, session executor, chat executor
└── services/         # Session, Stage, Timeline, Message, Interaction, Chat, Event, Alert, SystemWarnings
ent/
├── schema/           # Ent schema definitions
deploy/
├── config/           # tarsy.yaml.example, llm-providers.yaml.example, .env.example
├── podman-compose.yml
proto/
└── llm_service.proto
llm-service/
└── llm/providers/    # Python LLM providers (GoogleNativeProvider, LangChainProvider)
test/
├── e2e/              # End-to-end tests (harness, mocks, helpers, scenarios, golden files)
│   └── testdata/     # YAML configs per scenario, golden files, expected event definitions
├── database/         # SharedTestDB, NewTestClient (test DB helpers)
└── util/             # SetupTestDatabase, schema helpers, connection string utilities
```

---

## Session Execution Flow

The end-to-end happy path from alert submission to completion:

1. **API handler** receives `POST /api/v1/alerts` → validates → `AlertService.SubmitAlert()` → creates `AlertSession` (status=`pending`) with `chain_id` resolved from alert type
2. **Worker pool** polls for pending sessions → `SessionService.ClaimNextPendingSession()` uses `FOR UPDATE SKIP LOCKED` → sets status=`in_progress`, assigns `pod_id`
3. **SessionExecutor.Execute()** (`pkg/queue/executor.go`):
   - Resolves chain config from `ChainRegistry`
   - Initializes shared services (StageService, MessageService, TimelineService, InteractionService)
   - **Chain loop**: iterates over `chain.Stages` sequentially, tracking `dbStageIndex` (which may differ from config index when synthesis stages are inserted)
     - Checks context cancellation before starting each stage
     - Calls `executeStage()` which handles all stages uniformly (1 or N agents):
       - Builds execution configs via `buildConfigs()` (1 entry for single-agent, N for multi-agent/replica)
       - Creates `Stage` DB record (via `StageService.CreateStage`) with `ParallelType`, `SuccessPolicy`, `ExpectedAgentCount`
       - Updates session progress + publishes `stage.status: started` (inside `executeStage`, after Stage creation — so `stageID` is always available)
       - Launches goroutines (one per config) with `sync.WaitGroup` + buffered channel
       - Each goroutine calls `executeAgent()`:
         - Creates `AgentExecution` DB record (via `StageService.CreateAgentExecution`)
         - Resolves agent config via hierarchy: `ResolveAgentConfig(cfg, chain, stage, agent)` → `ResolvedAgentConfig`
         - Creates per-agent-execution MCP ToolExecutor (or stub) with `defer Close()`
         - Builds `ExecutionContext` with all dependencies
         - Creates agent via `AgentFactory.CreateAgent()` → `BaseAgent` with appropriate `Controller`
         - Calls `agent.Execute(ctx, execCtx, prevStageContext)`
         - Updates `AgentExecution` status
       - Waits for ALL goroutines (even on failures — success policy determines outcome)
       - Collects results, sorts by launch index, aggregates status via `aggregateStatus()`
       - Calls `StageService.UpdateStageStatus()` for DB consistency
     - Publishes `stage.status` (terminal status) from chain loop
     - Increments `dbStageIndex`
     - On failure: returns immediately (fail-fast, no subsequent stages)
     - **Synthesis** (automatic when `len(agentResults) > 1`):
       - `executeSynthesisStage()` creates a **separate Stage DB record** (name: `"{StageName} - Synthesis"`)
       - Builds synthesis context from parallel agents' full timeline events via `buildSynthesisContext()`
       - Runs synthesis agent as single-agent execution
       - Synthesis result replaces investigation result in `completedStages` for downstream context
       - Increments `dbStageIndex` again
     - Builds `prevStageContext` for next stage via `BuildStageContext()`
   - **Post-chain**: extracts `finalAnalysis` from last completed stage (reverse search)
   - **Executive summary**: direct LLM call (fail-open), creates session-level `executive_summary` timeline event
4. **BaseAgent.Execute()** → delegates to `Controller.Run()`
5. **Controller.Run()** executes the iteration loop (see below)
6. **Worker** updates `AlertSession` with final status, `final_analysis`, `executive_summary`, `completed_at`

**Design principle**: One `executeStage()` handles all stages uniformly. A single-agent stage is not a special case — it's a stage with N=1 agents. The same goroutine + WaitGroup + channel pattern handles N=1 identically to N=3. No separate code paths for single vs multi-agent execution.

## Chat Execution Flow

Follow-up chat allows users to ask questions about completed investigations. Chat is a 1:1 extension of an `AlertSession` — after an investigation reaches a terminal state, users send messages that each trigger a single-agent execution with access to the same MCP tools.

**Design principle**: Chat is a prompt concern, not a controller concern. The same controllers (FunctionCalling, Synthesis) handle both investigation and chat — the `ChatContext` on `ExecutionContext` triggers chat-specific prompting. No separate chat controllers. Same iteration limits, same `forceConclusion()` at `MaxIterations`, same per-iteration timeout.

### End-to-End Chat Flow

1. **API handler** receives `POST /api/v1/sessions/:id/chat/messages` → validates terminal session + chat enabled + no active execution + content valid
2. **Get-or-create Chat**: `ChatService.GetOrCreateChat(sessionID, author)` → returns existing Chat or creates one (first message creates transparently)
3. **Publish events**: `chat.created` (if first message) + creates `ChatUserMessage` record
4. **Submit to ChatMessageExecutor** (async, returns 202 immediately with `{chat_id, message_id, stage_id}`)
5. **ChatMessageExecutor.execute()** (goroutine):
   - Resolves chain + chat agent config via `ResolveChatAgentConfig()`
   - Resolves MCP selection via shared `resolveMCPSelection()` helper
   - Creates `Stage` (with `chat_id`, `chat_user_message_id`) + `AgentExecution` records
   - Creates `user_question` timeline event (before building context, so it's included in timeline)
   - Builds `ChatContext` via `GetSessionTimeline()` → `FormatInvestigationContext()` (unified timeline context)
   - Publishes `stage.status: started`, starts heartbeat (`Chat.last_interaction_at`)
   - Creates MCP ToolExecutor via shared `createToolExecutor()` helper
   - Runs `agent.Execute()` (same controllers as investigation)
   - Agent streams response via existing WebSocket events
   - Updates `Stage`/`AgentExecution` terminal status, publishes `stage.status: completed/failed`
   - Schedules stage event cleanup (60s grace period)
6. **Publish** `chat.user_message` event (in handler, after Submit returns)
7. **Cancel**: Existing `POST /api/v1/sessions/:id/cancel` extended to also cancel chat executions via `ChatMessageExecutor.CancelBySessionID()`

### Lifecycle Constraints

- **One Chat per session**: `AlertSession` → `Chat` is 1:1 (schema enforces uniqueness on `session_id`)
- **Terminal sessions only**: Chat available for completed/failed/timed_out sessions. Not for pending/in_progress/cancelling/cancelled
- **One-at-a-time per chat**: Only one message can be actively executing per chat. New message while processing → 409 Conflict
- **Chat enabled check**: `chain.Chat.Enabled` must be true; if `chain.Chat` is nil, chat is treated as disabled
- **Message cleanup on rejection**: If Submit rejects (active execution or shutting down), the created `ChatUserMessage` is deleted to avoid orphans

### Context Building

All context — original investigation AND previous chat exchanges — comes from the session's timeline events. No separate "chat history" builder. Each chat message creates a `user_question` timeline event before the agent runs, so `GetSessionTimeline()` returns a complete chronological record. No filtering — all event types pass through (including `llm_thinking`). Two existing functions (`GetSessionTimeline` + `FormatInvestigationContext`), zero new formatting code.

---

## Iteration Loop Flows

### FunctionCallingController (`pkg/agent/controller/function_calling.go`)

Handles both `native-thinking` and `langchain` strategies.

1. `PromptBuilder.BuildFunctionCallingMessages()` → system + user messages
2. Store initial messages, list tools
3. **Loop** (up to `MaxIterations`):
   - Call LLM with streaming AND tool bindings (structured function calling)
   - Create native tool events (code execution, grounding) if present
   - If **tool calls in response**: store assistant message with tool calls → execute each tool → append tool result messages (role=`tool` with `tool_call_id`) → continue
   - If **no tool calls**: this IS the final answer → create `final_analysis` event → return completed
4. If max iterations reached: `forceConclusion()` — call LLM WITHOUT tools to force text-only response

### Key difference

FunctionCalling: tools bound as structured definitions, LLM returns `ToolCallChunk` objects. Works with any provider (Gemini via google-native backend, others via langchain backend).
Synthesis: tool-less single LLM call for synthesizing multi-agent investigation results.

---

## Key Entity Fields

### AlertSession
`id`, `alert_data` (TEXT), `agent_type`, `alert_type`, `status` (pending/in_progress/cancelling/completed/failed/cancelled/timed_out), `chain_id`, `pod_id`, `final_analysis`, `executive_summary`, `mcp_selection` (JSON override), `author`, `runbook_url`, `deleted_at` (soft delete), timestamps (`created_at`, `started_at`, `completed_at`, `last_interaction_at`)

### Stage
`id`, `session_id`, `stage_name`, `stage_index`, `expected_agent_count`, `parallel_type` (multi_agent/replica, nullable), `success_policy` (all/any, nullable), `chat_id` (nullable — set for chat response stages), `chat_user_message_id` (nullable), `status`, `error_message`, timestamps

### AgentExecution
`id`, `stage_id`, `session_id` (denormalized), `agent_name`, `agent_index`, `iteration_strategy`, `llm_provider` (optional — resolved provider name e.g. `"gemini-2.5-pro"`), `status`, `error_message`, timestamps

### TimelineEvent
`id`, `session_id`, `stage_id` (**optional** — null for session-level events like `executive_summary`), `execution_id` (**optional** — null for session-level events), `sequence_number`, `event_type` (llm_thinking/llm_response/llm_tool_call/mcp_tool_summary/error/user_question/executive_summary/final_analysis/code_execution/google_search_result/url_context_result), `status` (streaming/completed/failed/cancelled/timed_out), `content` (TEXT, grows during streaming), `metadata` (JSON), timestamps

### Message
`id`, `session_id`, `stage_id`, `execution_id`, `sequence_number`, `role` (system/user/assistant/tool), `content`, `tool_calls` (JSON array, assistant messages), `tool_call_id` + `tool_name` (tool result messages), `created_at`

---

## REST API Surface

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `/health` | Health check |
| POST | `/api/v1/alerts` | Submit alert → creates pending session |
| GET | `/api/v1/sessions/:id` | Get session status and details |
| POST | `/api/v1/sessions/:id/cancel` | Cancel running session or active chat execution |
| POST | `/api/v1/sessions/:id/chat/messages` | Send chat message (auto-creates chat on first message, 202 Accepted) |
| GET | `/api/v1/sessions/:id/timeline` | Get session timeline events ordered by sequence |
| GET | `/api/v1/sessions/:id/trace` | Trace interaction list grouped by stage → execution |
| GET | `/api/v1/sessions/:id/trace/llm/:interaction_id` | Full LLM interaction detail with reconstructed conversation |
| GET | `/api/v1/sessions/:id/trace/mcp/:interaction_id` | Full MCP interaction detail (arguments, result, available tools) |
| GET | `/api/v1/ws` | WebSocket connection for real-time streaming |

---

## Core Interfaces & Contracts

### Agent Interface (`pkg/agent/agent.go`)

```go
type Agent interface {
    Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

`BaseAgent` delegates to a `Controller` via strategy pattern.

### Controller Interface (`pkg/agent/base_agent.go`)

```go
type Controller interface {
    Run(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

Implementations: `FunctionCallingController`, `SynthesisController`, `SingleCallController`.

Strategy-to-controller mapping:
- `native-thinking` → `FunctionCallingController` (Gemini structured function calling, `google-native` backend)
- `langchain` → `FunctionCallingController` (multi-provider function calling via LangChain backend)
- `synthesis` / `synthesis-native-thinking` → `SynthesisController` (tool-less single call; backend from config)

Chat is handled by the same controllers — chat is a prompt concern, not a controller concern.

### LLMClient Interface (`pkg/agent/llm_client.go`)

```go
type LLMClient interface {
    Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error)
    Close() error
}
```

`GRPCLLMClient` implements this. Chunk types: `TextChunk`, `ThinkingChunk`, `ToolCallChunk`, `CodeExecutionChunk`, `UsageChunk`, `ErrorChunk`, `GroundingChunk`.

### ToolExecutor Interface (`pkg/agent/tool_executor.go`)

```go
type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall) (*ToolResult, error)
    ListTools(ctx context.Context) ([]ToolDefinition, error)
    Close() error // Cleanup transports and subprocesses
}
```

Implementations: `StubToolExecutor` (no-op Close, for testing), `mcp.ToolExecutor` (real MCP-backed execution).

### SessionExecutor (`pkg/queue/types.go`)

```go
type SessionExecutor interface {
    Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult
}
```

Bridges the queue worker to the agent framework. Implementation: `RealSessionExecutor` in `pkg/queue/executor.go`.

### ChatMessageExecutor (`pkg/queue/chat_executor.go`)

```go
type ChatMessageExecutor struct {
    cfg, dbClient, llmClient, mcpFactory, agentFactory, eventPublisher, promptBuilder, execConfig
    // Services: timelineService, stageService, chatService, messageService, interactionService
    // Active execution tracking: mu (sync.RWMutex), activeExecs (map[string]context.CancelFunc), wg, stopped
}

func (e *ChatMessageExecutor) Submit(ctx context.Context, input ChatExecuteInput) (stageID string, err error)
func (e *ChatMessageExecutor) CancelExecution(chatID string) bool
func (e *ChatMessageExecutor) CancelBySessionID(ctx context.Context, sessionID string) bool
func (e *ChatMessageExecutor) Stop()
```

Each message spawns a goroutine — no pool, no semaphore, no queue. One-at-a-time per chat (enforced via `StageService.GetActiveStageForChat()`) naturally limits load. `CancelBySessionID` looks up the chat for a session and cancels any active execution — used by the cancel handler to provide unified cancellation.

Key internal methods: `execute()` (goroutine body — full lifecycle from config resolution to agent execution to cleanup), `buildChatContext()` (calls `GetSessionTimeline` + `FormatInvestigationContext`), `runChatHeartbeat()` (periodic `Chat.last_interaction_at` updates), `scheduleStageEventCleanup()` / `cleanupStageEvents()` (60s grace period after terminal status).

Config types:
- `ChatExecuteInput` — `Chat *ent.Chat`, `Message *ent.ChatUserMessage`, `Session *ent.AlertSession`
- `ChatMessageExecutorConfig` — `SessionTimeout` (default: 15m), `HeartbeatInterval` (default: 30s)

Key internal methods on `RealSessionExecutor`:
- `executeStage()` — creates Stage DB record, launches goroutines (one per execution config), collects results via WaitGroup + buffered channel, aggregates status via success policy. Same code path for 1 or N agents
- `executeAgent(ctx, input, stg, agentConfig, agentIndex, displayName)` — per-agent-execution lifecycle: DB record → config resolution → MCP creation → agent execution → status update. `displayName` overrides `agentConfig.Name` for DB/logs (differs for replicas)
- `executeSynthesisStage()` — creates separate synthesis Stage DB record, builds context from parallel agents' timeline events, runs synthesis agent. Called from chain loop when `len(agentResults) > 1`
- `buildConfigs()` / `buildMultiAgentConfigs()` / `buildReplicaConfigs()` — build execution configs from stage config. Replicas name agents `{BaseName}-1`, `{BaseName}-2`, etc.
- `buildSynthesisContext()` — queries `TimelineService.GetAgentTimeline()` per agent, builds `[]AgentInvestigation`, calls `FormatInvestigationForSynthesis()`
- `aggregateStatus()` — in-memory status aggregation matching `StageService.UpdateStageStatus()` logic. `SuccessPolicyAny`: completed if any agent completed. `SuccessPolicyAll`: completed only if all agents completed
- `aggregateError()` — builds detailed error for failed stages. Single-agent: returns agent's error. Multi-agent: lists each non-successful agent with status and error
- `resolvedSuccessPolicy()` — hierarchy: `stageConfig.SuccessPolicy` → `cfg.Defaults.SuccessPolicy` → `SuccessPolicyAny`
- `collectAndSort()` — drains indexed channel, sorts by launch index
- `buildStageContext()` — converts `[]stageResult` to `BuildStageContext()` input
- `generateExecutiveSummary()` — LLM call for session summary (fail-open)
- `updateSessionProgress()` — non-blocking DB update for dashboard visibility
- `mapCancellation()` — maps context errors to session status (timed_out/cancelled)

Shared package-level helpers in `pkg/queue/executor.go` (used by both `RealSessionExecutor` and `ChatMessageExecutor`):
- `createToolExecutor(ctx, mcpFactory, serverIDs, toolFilter, logger)` — MCP-backed executor or stub fallback
- `resolveMCPSelection(session, resolvedConfig, mcpRegistry)` — MCP server/tool filter resolution from session override or config
- `publishStageStatus(eventPublisher, sessionID, stageID, stageName, stageIndex, status)` — stage lifecycle event publishing

### Stage Context Builder (`pkg/agent/context/stage_context.go`)

```go
type StageResult struct {
    StageName     string
    FinalAnalysis string
}

func BuildStageContext(stages []StageResult) string
```

Formats completed stage results into a context string for the next stage's agent prompt. Wraps each stage's `final_analysis` with `<!-- CHAIN_CONTEXT_START/END -->` markers. Context flows in-memory through the chain loop (no DB query needed — the internal `stageResult.finalAnalysis` in the executor comes from `agent.ExecutionResult.FinalAnalysis` and is mapped to the public `StageResult` for `BuildStageContext()`).

**Note**: `StageResult` (exported, 2 fields: `StageName`, `FinalAnalysis`) is the public API for context building. The internal `stageResult` (unexported, in `pkg/queue/executor.go`) carries additional executor metadata (`stageID`, `status`, `err`, `agentResults`) that isn't exposed through the public API.

### Investigation Formatter (`pkg/agent/context/investigation_formatter.go`)

```go
type AgentInvestigation struct {
    AgentName    string
    AgentIndex   int
    Strategy     string                  // e.g., "native-thinking", "langchain"
    LLMProvider  string                  // e.g., "gemini-2.5-pro"
    Status       alertsession.Status     // completed, failed, etc.
    Events       []*ent.TimelineEvent    // full investigation from GetAgentTimeline
    ErrorMessage string                  // for failed agents
}

func FormatInvestigationForSynthesis(agents []AgentInvestigation, stageName string) string
```

Formats multi-agent full investigation histories for the synthesis agent. Uses timeline events (which include thinking, tool calls, tool results, and responses) rather than raw messages. Each agent's investigation is wrapped with identifying metadata (name, strategy, provider, status).

**Shared formatting**: `formatTimelineEvents()` is a shared helper used by both `FormatInvestigationContext()` (single-agent context for follow-up chat) and `FormatInvestigationForSynthesis()` (multi-agent context for synthesis). Handles tool call / summary deduplication: when an `llm_tool_call` is immediately followed by an `mcp_tool_summary`, the helper emits the tool name + arguments from the call but substitutes the summary content for the raw result, skipping the summary event. `formatToolCallHeader()` extracts server name, tool name, and arguments from event metadata.

### PromptBuilder (`pkg/agent/prompt/builder.go`)

```go
func (b *PromptBuilder) BuildFunctionCallingMessages(execCtx, prevStageContext) []ConversationMessage
func (b *PromptBuilder) BuildSynthesisMessages(execCtx, prevStageContext) []ConversationMessage
func (b *PromptBuilder) BuildForcedConclusionPrompt(iteration, strategy) string
func (b *PromptBuilder) ComposeInstructions(execCtx) string
func (b *PromptBuilder) ComposeChatInstructions(execCtx) string
func (b *PromptBuilder) BuildMCPSummarizationSystemPrompt(serverName, toolName, maxTokens) string
func (b *PromptBuilder) BuildMCPSummarizationUserPrompt(context, server, tool, result) string
func (b *PromptBuilder) BuildExecutiveSummarySystemPrompt() string
func (b *PromptBuilder) BuildExecutiveSummaryUserPrompt(finalAnalysis) string
func (b *PromptBuilder) MCPServerRegistry() *config.MCPServerRegistry  // Per-server config lookup (used by summarization)
```

Three-tier instruction composition: General SRE → MCP server instructions → custom agent instructions.

### EventPublisher (`pkg/events/publisher.go`)

```go
func (p *EventPublisher) Publish(ctx, sessionID, channel, payload) error        // Persistent (DB + NOTIFY)
func (p *EventPublisher) PublishTransient(ctx, channel, payload) error           // Transient (NOTIFY only)
func (p *EventPublisher) PublishStageStatus(ctx, sessionID, payload StageStatusPayload) error  // Stage lifecycle
```

The `agent.EventPublisher` interface (`pkg/agent/context.go`) exposes typed methods: `PublishTimelineCreated`, `PublishTimelineCompleted`, `PublishStreamChunk`, `PublishSessionStatus`, `PublishStageStatus`, `PublishChatCreated`, `PublishChatUserMessage`.

### ConnectionManager (`pkg/events/manager.go`)

```go
func (m *ConnectionManager) HandleConnection(parentCtx, conn)
func (m *ConnectionManager) Broadcast(channel, event)
```

### MCP Client (`pkg/mcp/client.go`)

```go
type Client struct { /* manages MCP SDK sessions for multiple servers */ }

func (c *Client) Initialize(ctx context.Context, serverIDs []string) error
func (c *Client) InitializeServer(ctx context.Context, serverID string) error
func (c *Client) ListTools(ctx context.Context, serverID string) ([]*mcpsdk.Tool, error)
func (c *Client) ListAllTools(ctx context.Context) (map[string][]*mcpsdk.Tool, error)
func (c *Client) CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (*mcpsdk.CallToolResult, error)
func (c *Client) Close() error
func (c *Client) HasSession(serverID string) bool
func (c *Client) FailedServers() map[string]string
```

Thread-safe session manager wrapping the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.3.0). One `Client` instance per alert session (short-lived). Per-session tool cache (never invalidated — natural freshness). Per-server `sync.Mutex` for session recreation to prevent thundering herd.

### MCP Tool Executor (`pkg/mcp/executor.go`)

```go
type ToolExecutor struct { /* implements agent.ToolExecutor backed by MCP */ }

func (e *ToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error)
func (e *ToolExecutor) ListTools(ctx context.Context) ([]agent.ToolDefinition, error)
func (e *ToolExecutor) Close() error
```

Bridge between controllers and MCP. Execute flow: normalize tool name → split `server.tool` → validate server/tool access → parse ActionInput → `Client.CallTool()` → convert result. Tool errors returned as `ToolResult{IsError: true}` (MCP convention), Go errors reserved for infrastructure failures.

### MCP Client Factory (`pkg/mcp/client_factory.go`)

```go
type ClientFactory struct { /* creates per-session Client instances */ }

func (f *ClientFactory) CreateClient(ctx context.Context, serverIDs []string) (*Client, error)
func (f *ClientFactory) CreateToolExecutor(ctx, serverIDs, toolFilter) (*ToolExecutor, *Client, error)
```

`CreateToolExecutor` is the primary entry point used by the session executor.

### MCP Health Monitor (`pkg/mcp/health.go`)

```go
type HealthMonitor struct { /* background health check loop */ }

func (m *HealthMonitor) Start(ctx context.Context)
func (m *HealthMonitor) Stop()
func (m *HealthMonitor) GetStatuses() map[string]*HealthStatus
func (m *HealthMonitor) GetCachedTools() map[string][]*mcpsdk.Tool
func (m *HealthMonitor) IsHealthy() bool
```

Dedicated long-lived `Client` for health probing (not shared with sessions). Checks every 15s via `ListTools`. On failure: attempts session recreation, then marks unhealthy + adds `SystemWarning`. On recovery: clears warning.

### System Warnings Service (`pkg/services/system_warnings.go`)

```go
type SystemWarningsService struct { /* in-memory warning store */ }

func (s *SystemWarningsService) AddWarning(category, message, details, serverID string) string
func (s *SystemWarningsService) GetWarnings() []*SystemWarning
func (s *SystemWarningsService) ClearByServerID(category, serverID string) bool
```

Thread-safe, not persisted. Warnings are transient and reset on restart. `AddWarning` deduplicates by category+serverID (replaces existing). Used by `HealthMonitor` for MCP health warnings; general-purpose for future non-MCP warnings. Exposed via health endpoint for dashboard.

---

## MCP Client Infrastructure

### Package Layout (`pkg/mcp/`)

```
pkg/mcp/
├── client.go           # Client — MCP SDK session manager
├── client_factory.go   # ClientFactory — per-session creation
├── executor.go         # ToolExecutor — implements agent.ToolExecutor
├── params.go           # ActionInput parameter parsing (multi-format cascade)
├── router.go           # Tool name normalization, splitting, validation
├── recovery.go         # Error classification, retry constants
├── health.go           # HealthMonitor — background health checks
├── tokens.go           # Token estimation, truncation utilities (storage + summarization)
└── transport.go        # Transport creation from config (stdio/HTTP/SSE)
```

### Tool Lifecycle During Execution

```
Controller (FunctionCalling: "server__tool" + JSON via ToolCallDelta)
  → ToolExecutor.Execute(ToolCall)
    → NormalizeToolName: server__tool → server.tool (function calling reverse mapping)
    → SplitToolName: "server" + "tool"
    → resolveToolCall: validate server in allowed list, check tool filter
    → ParseActionInput: JSON → YAML → key-value → raw string cascade
    → Client.CallTool(ctx, serverID, toolName, params)
      → session.CallTool() with 90s timeout
      → On error: ClassifyError → NoRetry / RetryNewSession → retry once with jittered backoff
    → extractTextContent(result) — concatenate TextContent items
    → Return ToolResult{Content, IsError}
```

### ActionInput Parameter Parsing (`params.go`)

Multi-format cascade (first successful parse wins):
1. JSON object → `map[string]any`
2. JSON non-object (string, number, array) → `{"input": value}`
3. YAML with complex structures (arrays, nested maps) → `map[string]any`
4. Key-value pairs (`key: value` or `key=value`, comma/newline separated) → `map[string]any`
5. Single raw string → `{"input": string}`

Type coercion for key-value: `true/false` → bool, `null/none` → nil, integers, floats, strings.

### Tool Name Routing (`router.go`)

- **NormalizeToolName**: `server__tool` → `server.tool` (reverse Gemini function name mapping)
- **SplitToolName**: strict regex validation, splits into serverID + toolName
- NativeThinking controller does `.` → `__` when passing tools to LLM; executor reverses transparently

### Error Classification & Recovery (`recovery.go`)

| Error Type | Recovery Action |
|------------|-----------------|
| Context cancelled/deadline | NoRetry |
| Network timeout | NoRetry |
| Network error (connection refused, reset, etc.) | RetryNewSession |
| EOF, broken pipe, connection closed | RetryNewSession |
| MCP JSON-RPC protocol error (parse, invalid request/params, method not found) | NoRetry |
| Unknown errors | NoRetry (safe default) |

Recovery: max 1 retry, jittered backoff (250–750ms), session recreation for transport failures.

### Transport Layer (`transport.go`)

Maps `config.TransportConfig` to MCP SDK transports:
- **stdio**: `CommandTransport` wrapping `os/exec.Command`. Inherits parent env + config overrides.
- **HTTP**: `StreamableClientTransport`. Optional bearer token (via `http.RoundTripper` wrapper), TLS config, timeout.
- **SSE**: `SSEClientTransport`. Same HTTP client customization as HTTP transport.

### MCP Operation Timeouts

| Constant | Value | Purpose |
|----------|-------|---------|
| `MCPInitTimeout` | 30s | Per-server initialization (transport + MCP handshake) |
| `OperationTimeout` | 90s | CallTool / ListTools deadline (must be < iteration timeout of 120s) |
| `MCPHealthPingTimeout` | 5s | Health check ping (fast fail for monitoring) |
| `MCPHealthInterval` | 15s | Health check loop interval |
| `ReinitTimeout` | 10s | Session recreation during recovery |
| `RetryBackoffMin/Max` | 250–750ms | Jittered backoff between retries |

### Startup & Runtime Health

**Startup (eager, fatal on failure)**: All configured MCP servers must initialize before TARSy becomes ready. Broken configs or unreachable servers prevent the readiness probe from passing. Rolling updates in OpenShift/K8s ensure no downtime.

**Runtime (HealthMonitor)**: Background checks every 15s detect degradation. Unhealthy servers surface as `SystemWarning`s in the health endpoint/dashboard. On recovery, warnings are cleared automatically.

### Per-Agent-Execution Isolation

Every agent execution gets its own `Client` instance with independent MCP SDK sessions (created via `createToolExecutor()`, torn down via `defer Close()`). No shared state between agents or stages. Go's `http.Client` handles HTTP connection pooling internally, so per-execution overhead for HTTP/SSE is just the MCP `Initialize` handshake. Stdio transports spawn a subprocess per execution. This per-agent-execution lifecycle means Phase 5.2 parallel agents work without refactoring.

### Integration Points

- **Session executor** (`pkg/queue/executor.go`): `executeAgent()` calls `createToolExecutor()` per agent execution (creates MCP-backed executor or falls back to stub). `defer Close()` ensures cleanup.
- **NativeThinking controller**: replaces `.` → `__` in tool names for Gemini function calling compatibility. Executor reverses transparently.
- **Prompt builder**: `appendUnavailableServerWarnings()` warns the LLM about servers that failed per-execution initialization.
- **Health endpoint**: includes `SystemWarningsService.GetWarnings()` in response (informational, does not cause 503).

---

## Data Masking (`pkg/masking/`)

### Design Principles

1. **Fail-closed for MCP** — on masking failure, redact entire content rather than leaking secrets
2. **Fail-open for alerts** — on masking failure, continue with unmasked data (availability over secrecy for user-provided data)
3. **One-way masking** — original values never stored or recoverable
4. **Code maskers before regex** — structural maskers (K8s Secrets) run first for precision, then regex patterns sweep
5. **Compile once** — all patterns compiled at service creation; no hot-reload
6. **Single chokepoint** — MCP content masked in `ToolExecutor.Execute()` before `ToolResult` is returned; all downstream consumers see masked content

### Package Layout

```
pkg/masking/
├── service.go              # MaskingService — core orchestrator
├── pattern.go              # CompiledPattern, pattern resolution, group expansion
├── masker.go               # Masker interface for code-based maskers
├── kubernetes_secret.go    # KubernetesSecretMasker implementation
├── service_test.go
├── pattern_test.go
├── kubernetes_secret_test.go
└── testdata/               # K8s Secret/ConfigMap YAML/JSON fixtures
```

### Data Flow

```
MCP Tool Call:
  MCP Server → Client.CallTool() → extractTextContent()
    → MaskingService.MaskToolResult(content, serverID) → masked content
    → ToolResult → Controller → Timeline/Messages/LLM (all see masked content)

Alert Submission:
  POST /api/v1/alerts → AlertService.SubmitAlert()
    → MaskingService.MaskAlertData(alertData) → masked data
    → DB INSERT (masked alert_data stored)
```

### Masker Interface (`pkg/masking/masker.go`)

```go
type Masker interface {
    Name() string                  // Unique identifier (matches config.GetBuiltinConfig().CodeMaskers key)
    AppliesTo(data string) bool    // Lightweight pre-check (string contains, not parsing)
    Mask(data string) string       // Apply masking; defensive — returns original on error
}
```

### MaskingService (`pkg/masking/service.go`)

```go
type MaskingService struct {
    registry      *config.MCPServerRegistry
    patterns      map[string]*CompiledPattern  // Built-in + custom compiled patterns
    patternGroups map[string][]string          // Group name → pattern names
    codeMaskers   map[string]Masker            // Registered code-based maskers
    alertMasking  AlertMaskingConfig           // Alert payload masking settings
}

func NewMaskingService(registry *config.MCPServerRegistry, alertCfg AlertMaskingConfig) *MaskingService
func (s *MaskingService) MaskToolResult(content string, serverID string) string  // Fail-closed
func (s *MaskingService) MaskAlertData(data string) string                       // Fail-open
```

Singleton created once at startup. Thread-safe, stateless aside from compiled patterns.

### CompiledPattern (`pkg/masking/pattern.go`)

```go
type CompiledPattern struct {
    Name        string
    Regex       *regexp.Regexp
    Replacement string
    Description string
}
```

### KubernetesSecretMasker (`pkg/masking/kubernetes_secret.go`)

Structural code masker that distinguishes K8s `Secret` from `ConfigMap` resources. Parses YAML (multi-document) and JSON to mask only Secret `data`/`stringData` fields. Handles `SecretList`, `List` with mixed items, and JSON-in-annotations (`last-applied-configuration`). Returns original data on parse errors (defensive).

### Masking Execution Order

1. Code-based maskers run first (structural, context-aware)
2. Regex patterns sweep remaining content (general)

### Integration Points

- **ToolExecutor** (`pkg/mcp/executor.go`): `maskingService` field, called after `extractTextContent()` in `Execute()`. Nil-safe (backward compat).
- **ClientFactory** (`pkg/mcp/client_factory.go`): Holds `maskingService`, passes through to `ToolExecutor`.
- **AlertService** (`pkg/services/alert_service.go`): `maskingService` field, called in `SubmitAlert()` before DB insert. Nil-safe.
- **Startup wiring**: `MaskingService` created once, passed to both `ClientFactory` and `AlertService`.

### Configuration

Per-server MCP masking (existing `MCPServerConfig.DataMasking`): `enabled`, `pattern_groups`, `patterns`, `custom_patterns`.

Alert masking under `defaults.alert_masking`: `enabled` (default: true), `pattern_group` (default: "security").

Replacement format: `[MASKED_X]` (not `__X__` to avoid Markdown bold rendering).

---

## Tool Result Summarization (`pkg/agent/controller/summarize.go`)

### Architecture Decision: Controller-Level Summarization

Summarization is an LLM orchestration concern, not an MCP infrastructure concern. The `ToolExecutor` lacks LLMClient, conversation context, EventPublisher, and services — all required for summarization. Instead, summarization happens in the controller after `ToolExecutor.Execute()` returns, via a shared `maybeSummarize()` function called from the FunctionCallingController through the common `executeToolCall()` path (`pkg/agent/controller/tool_execution.go`).

### Summarization Flow

```
Controller iteration loop:
  1. LLM returns tool call
     → Create llm_tool_call timeline event (status: streaming, args in metadata)

  2. ToolExecutor.Execute(toolCall) → raw ToolResult (masked by Phase 4.2)

  3. Complete llm_tool_call event (status: completed, content = storage-truncated raw result)

  4. Check summarization:
     a. Look up SummarizationConfig for this server (via PromptBuilder.MCPServerRegistry())
     b. If disabled or not configured → use raw result
     c. EstimateTokens(rawResult) — approximate (~4 chars/token)
     d. If below threshold → use raw result

  5. Summarize (threshold exceeded):
     a. TruncateForSummarization(rawResult) — safety-net for LLM input
     b. Build summarization prompts (system + user with conversation context)
     c. Create mcp_tool_summary timeline event (status: streaming)
     d. Call LLM with streaming → publish stream.chunk for each delta
     e. Complete mcp_tool_summary event with full summary text
     f. Record LLMInteraction (type: "summarization")
     g. Result for conversation = wrapped summary (not raw)

  6. Use result in conversation (summarized or raw)
```

### Summarization Interfaces

```go
// SummarizationResult holds the outcome of a summarization attempt.
type SummarizationResult struct {
    Content       string
    WasSummarized bool
    Usage         *agent.TokenUsage
}

func maybeSummarize(ctx, execCtx, serverID, toolName, rawContent, conversationContext, eventSeq) (*SummarizationResult, error)
func buildConversationContext(messages []agent.ConversationMessage) string  // Excludes system messages
```

### Token Estimation & Truncation (`pkg/mcp/tokens.go`)

```go
func EstimateTokens(text string) int                // ~4 chars/token heuristic (threshold check, not billing)
func TruncateForStorage(content string) string       // 8000 tokens — UI/DB protection for llm_tool_call + MCPInteraction
func TruncateForSummarization(content string) string // 100,000 tokens — safety net for summarization LLM input
```

Two independent truncation concerns:
1. **Storage truncation** — Always applied to raw results in `llm_tool_call` completion and MCPInteraction. Lower threshold. Protects dashboard.
2. **Summarization input safety net** — Larger limit; gives the summarizer maximum data while bounding context window.

No separate conversation truncation for non-summarized results. If below the summarization threshold, the result is already small enough. Summarization *is* the mechanism for controlling result size.

### Summarization Failure Policy: Fail-Open

If the summarization LLM call fails (timeout, error, empty response), the raw result is used. The investigation continues with a larger context window cost. Matches investigation-availability-first philosophy.

### Summarization Configuration

Per-server in `MCPServerConfig.Summarization`: `enabled`, `size_threshold_tokens` (default: 5000), `summary_max_token_limit` (default: 1000).

---

## Tool Call Lifecycle Events

### Single-Event Lifecycle Pattern

Tool calls use a single `llm_tool_call` timeline event with a streaming lifecycle (same pattern as `llm_response`), rather than separate events for call and result:

```
[llm_tool_call] created (status: streaming)
  → metadata: {server_name, tool_name, arguments}
  → Dashboard shows: "Calling server.tool..." with spinner

[llm_tool_call] completed (status: completed)
  → content: storage-truncated raw result
  → metadata enriched: {is_error}
  → Dashboard shows: tool result

(if summarization triggered):
  [mcp_tool_summary] created (status: streaming) → "Summarizing..."
  [stream.chunk] ...                              → Summary LLM token deltas
  [mcp_tool_summary] completed (status: completed) → Summary stored
```

Arguments live in metadata (not content) so they survive the content update on completion. On catchup: one event in DB, status tells the state — no multi-event correlation needed.

### Timeline Event Helpers (`pkg/agent/controller/timeline.go`)

```go
func createToolCallEvent(ctx, execCtx, serverID, toolName, arguments, eventSeq) (*ent.TimelineEvent, error)
func completeToolCallEvent(ctx, execCtx, event, content, isError)
```

`createToolResultEvent` was removed — the raw result lives on the completed `llm_tool_call` event. The `tool_result` and `mcp_tool_call` enum values were removed from the TimelineEvent schema (never had production data).

### Shared Tool Execution (`pkg/agent/controller/tool_execution.go`)

The FunctionCallingController uses shared tool execution logic through `executeToolCall()`, which handles: tool call event creation → `ToolExecutor.Execute()` → event completion → summarization check → MCPInteraction recording.

---

## Per-Alert MCP Selection Override

### Override Semantics: Replace, Not Merge

When an alert provides `mcp_selection`, it **replaces** the chain/agent's MCP server list entirely. The override is the authoritative, complete server set for this alert. Tool filtering within a server is additive restriction (empty list = all tools).

### Data Flow

```
POST /api/v1/alerts with mcp_selection JSON
  → AlertService.SubmitAlert() stores to AlertSession.mcp_selection
  → Worker claims session → RealSessionExecutor.Execute()
    → resolveMCPSelection(session, resolvedConfig)
      → Deserialize via ParseMCPSelectionConfig()
      → Validate all servers exist in MCPServerRegistry
      → Build serverIDs + toolFilter
      → Apply NativeToolsOverride if present
    → ClientFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
```

### Key Types

```go
// pkg/models/mcp_selection.go
func ParseMCPSelectionConfig(raw map[string]interface{}) (*MCPSelectionConfig, error)

// pkg/agent/context.go — added to ResolvedAgentConfig
NativeToolsOverride *models.NativeToolsConfig  // Per-alert native tools override (nil = use provider defaults)
```

### Validation

API-level validation (immediate 400 for unknown servers) AND execution-time validation in `resolveMCPSelection()` (defense in depth against config changes between submission and execution).

---

## Chain Orchestration & Session Completion

### Chain Loop (`pkg/queue/executor.go`)

The `RealSessionExecutor.Execute()` method iterates over `chain.Stages` sequentially using a `dbStageIndex` counter. Each config stage produces at least one DB stage, plus an optional synthesis stage when >1 agent ran. On stage failure, the chain stops immediately (fail-fast). Stage context accumulates in-memory via `[]stageResult` and is formatted into `prevStageContext` for each subsequent stage.

Internal types:
- `stageResult` — `stageID`, `stageName`, `status`, `finalAnalysis`, `err`, `agentResults []agentResult`
- `agentResult` — `executionID`, `status`, `finalAnalysis`, `err`
- `executionConfig` — `agentConfig` (config.StageAgentConfig), `displayName` (for DB/logs)
- `indexedAgentResult` — `index`, `result` (pairs result with launch order for sorting)
- `executeStageInput`, `generateSummaryInput`

### Backend Derivation

Backend (Python provider routing) is resolved from iteration strategy, not from LLM provider type:

| Strategy | Backend | Reason |
|----------|---------|--------|
| `native-thinking` | `"google-native"` | Requires Google SDK for native thinking/tool calling |
| `langchain` | `"langchain"` | Multi-provider function calling via LangChain |
| `synthesis` | `"langchain"` | Multi-provider synthesis |
| `synthesis-native-thinking` | `"google-native"` | Gemini thinking for synthesis |

Non-agent LLM calls inherit backend from context: summarization uses agent's `execCtx.Config.Backend`; executive summary resolves its own from chain/system default strategy.

### Executive Summary Generation

After all stages complete, a direct LLM call generates a short executive summary:

1. **Provider hierarchy**: `chain.executive_summary_provider` → `chain.llm_provider` → `defaults.llm_provider`
2. **Backend hierarchy**: `chain.iteration_strategy` → `defaults.iteration_strategy` → `ResolveBackend()`
3. Uses `PromptBuilder.BuildExecutiveSummarySystemPrompt()` + `BuildExecutiveSummaryUserPrompt(finalAnalysis)`
4. Single non-streaming LLM call (no tools)
5. Creates session-level `executive_summary` timeline event (no `stage_id`/`execution_id`)

**Failure policy**: Fail-open. If generation fails, the session still completes successfully. Error stored in `AlertSession.executive_summary_error`.

### ExecutionResult (`pkg/queue/types.go`)

```go
type ExecutionResult struct {
    Status                alertsession.Status
    FinalAnalysis         string
    ExecutiveSummary      string
    ExecutiveSummaryError string  // Records why summary generation failed
    Error                 error
}
```

### Stage Status Event (`pkg/events/payloads.go`)

```go
type StageStatusPayload struct {
    Type       string `json:"type"`        // always "stage.status"
    SessionID  string `json:"session_id"`
    StageID    string `json:"stage_id"`    // always present (started event published after Stage DB record creation)
    StageName  string `json:"stage_name"`
    StageIndex int    `json:"stage_index"` // 1-based
    Status     string `json:"status"`      // started, completed, failed, timed_out, cancelled
    Timestamp  string `json:"timestamp"`
}
```

---

## Key Types

### ExecutionContext (`pkg/agent/context.go`)

Carries all runtime state for an agent execution: `SessionID`, `StageID`, `ExecutionID`, `AgentName`, `AlertData`, `AlertType`, `RunbookContent`, `Config` (ResolvedAgentConfig), `LLMClient`, `ToolExecutor`, `Services` (ServiceBundle), `PromptBuilder`, `EventPublisher`, `ChatContext`.

### ChatContext (`pkg/agent/context.go`)

```go
type ChatContext struct {
    UserQuestion        string
    InvestigationContext string
}
```

Carries chat-specific data for controllers. `InvestigationContext` is the formatted output of `FormatInvestigationContext()` containing the full investigation + prior chat exchanges. When `ChatContext` is non-nil on `ExecutionContext`, controllers use chat-specific prompting.

### ResolvedAgentConfig (`pkg/agent/context.go`)

Runtime configuration after hierarchy resolution (defaults → agent → chain → stage → stage-agent): `AgentName`, `IterationStrategy`, `LLMProvider`, `MaxIterations`, `IterationTimeout`, `MCPServers`, `CustomInstructions`, `Backend`.

`Backend` (`"google-native"` or `"langchain"`) is resolved from iteration strategy via `ResolveBackend()` in `pkg/agent/config_resolver.go`. Constants: `BackendGoogleNative`, `BackendLangChain`. This replaces the old approach of deriving backend from provider type.

### Chat Agent Config Resolution (`pkg/agent/config_resolver.go`)

`ResolveChatAgentConfig(cfg, chain, chatCfg)` resolves agent configuration for chat execution using the chain's `ChatConfig`. Same hierarchy patterns as `ResolveAgentConfig()`:

| Field | Resolution Order | Fallback |
|-------|-----------------|----------|
| **Agent** | `chatCfg.Agent` | `"ChatAgent"` (built-in) |
| **IterationStrategy** | defaults → agentDef → chain → chatCfg | `defaults.iteration_strategy` |
| **LLMProvider** | defaults → chain → chatCfg | `defaults.llm_provider` |
| **MaxIterations** | defaults → agentDef → chain → chatCfg | `defaults.max_iterations` |
| **MCPServers** | agentDef → chain (or `aggregateChainMCPServers()`) → chatCfg | `[]` |
| **Backend** | Derived from resolved `IterationStrategy` via `ResolveBackend()` | — |

MCP servers for chat follow a specific priority: session's `mcp_selection` override (from original alert) → chain chat config → union of all MCP servers from chain stages (via `aggregateChainMCPServers()`).

### ServiceBundle (`pkg/agent/context.go`)

Service dependencies injected into controllers: `Timeline` (TimelineService), `Message` (MessageService), `Interaction` (InteractionService), `Stage` (StageService).

### GenerateInput (`pkg/agent/llm_client.go`)

LLM call input: `SessionID`, `ExecutionID`, `Messages`, `Config` (LLMProviderConfig), `Tools`, `Backend`. The `Backend` field is set by callers from `execCtx.Config.Backend` (agent execution) or resolved separately (executive summary). All controllers and summarization pass backend through to gRPC.

### LLMResponse (`pkg/agent/controller/helpers.go`)

Aggregated response from streaming: `Text`, `ThinkingText`, `ToolCalls`, `CodeExecutions`, `Groundings`, `Usage`.

### IterationState (`pkg/agent/iteration.go`)

Shared iteration tracking: `CurrentIteration`, `MaxIterations`, `LastInteractionFailed`, `ConsecutiveTimeoutFailures`. Constants: `MaxConsecutiveTimeouts = 2`, `DefaultIterationTimeout = 120s`.

---

## Configuration System

### Structure

- **File-based**: YAML config in `deploy/config/`, version-controlled
- **In-memory registries**: Loaded once at startup via `config.Initialize(ctx, configDir)`
- **No hot reload**: Restart required for config changes
- **Hierarchy**: built-in defaults → YAML user config → env vars → per-alert overrides

### Registries (`pkg/config/`)

| Registry | Lookup | Key Types |
|----------|--------|-----------|
| `AgentRegistry` | `Get(name)` | `AgentConfig`: MCPServers, CustomInstructions, IterationStrategy, MaxIterations |
| `ChainRegistry` | `Get(chainID)`, `GetByAlertType(alertType)` | `ChainConfig`: AlertTypes, Stages[], Chat, LLMProvider, MaxIterations, IterationStrategy, ExecutiveSummaryProvider |
| `MCPServerRegistry` | `Get(id)` | `MCPServerConfig`: Transport, Instructions, DataMasking, Summarization |
| `LLMProviderRegistry` | `Get(name)` | `LLMProviderConfig`: Type, Model, APIKeyEnv, MaxToolResultTokens, NativeTools |

Python receives config via gRPC `LLMConfig` (provider, model, api_key_env, backend, native_tools, etc.). Python does not read files or env directly.

### gRPC Protocol

`LLMConfig.backend` field routes to Python provider:
- `"langchain"` → `LangChainProvider` (multi-provider: OpenAI, Anthropic, xAI, Google, VertexAI)
- `"google-native"` → `GoogleNativeProvider` (Gemini-specific thinking features)

---

## Streaming & Events

### WebSocket Protocol

Client connects, subscribes to channels (`session:{id}`, `sessions`), receives events.

**Client actions**: `subscribe`, `unsubscribe`, `catchup` (with `last_event_id`), `ping`

**Persistent events** (DB + NOTIFY): `timeline_event.created`, `timeline_event.completed` (includes `event_type` for observability), `session.status`, `stage.status`, `chat.created`, `chat.user_message`

**Transient events** (NOTIFY only, no DB): `stream.chunk` (LLM token deltas)

All chat events are published to `session:{session_id}` — the same channel as investigation events. No separate chat channel.

### Event Type Conventions

- **Single `.status` type** when the payload shape is the same across all states: `session.status`, `stage.status` (with `status` field: started/completed/failed/timed_out/cancelled)
- **Separate types** when payloads carry fundamentally different data: `timeline_event.created` (full context) vs `timeline_event.completed` (event_id + final content only)
- **Standalone type** for transient high-frequency events: `stream.chunk`

Stage events are published from the **executor** (chain loop), not from controllers. Controllers are unaware of stage boundaries.

### Cross-Pod Delivery

PostgreSQL `NOTIFY`/`LISTEN` via `NotifyListener` (`pkg/events/listener.go`). `pgx.WaitForNotification` in a goroutine. NOTIFY payload limit: truncation at 7900 bytes.

### Publishing Pattern

DB INSERT + `pg_notify` in the same transaction for persistent events. `PublishTransient` for token streaming. Publish failures are non-blocking (logged, don't stop agent execution).

### Catchup

**Auto-catchup on subscribe**: New channel subscriptions automatically receive prior events for that channel — no explicit `catchup` action needed. On reconnect, client can also send `catchup` with `last_event_id` for fine-grained replay. Server returns missed events (limit: 200). If overflow, sends `catchup.overflow` signaling client to do full REST reload.

---

## Trace / Observability API (`pkg/api/handler_trace.go`)

Three-level trace endpoints for inspecting investigation internals. Designed for the dashboard's trace view and for e2e test verification of all 4 data layers (WS events, API responses, LLM interactions, MCP interactions).

### Level 1: Interaction List (`GET /sessions/:id/trace`)

Returns interactions grouped in a stage → execution hierarchy. Session-level interactions (e.g., executive summary) are returned separately.

```go
// pkg/models/interaction.go
type TraceListResponse struct {
    Stages              []TraceStageGroup         // Stage → execution → interactions hierarchy
    SessionInteractions []LLMInteractionListItem  // Session-level (e.g., executive summary)
}

type TraceStageGroup struct {
    StageID, StageName string
    Executions         []TraceExecutionGroup
}

type TraceExecutionGroup struct {
    ExecutionID, AgentName string
    LLMInteractions        []LLMInteractionListItem
    MCPInteractions        []MCPInteractionListItem
}
```

### Level 2: LLM Interaction Detail (`GET /sessions/:id/trace/llm/:interaction_id`)

Full LLM interaction with reconstructed conversation from the Message table. For self-contained interactions (summarization) that don't use the Message table, the conversation is extracted from inline `llm_request` JSON.

### Level 2: MCP Interaction Detail (`GET /sessions/:id/trace/mcp/:interaction_id`)

Full MCP interaction: tool arguments, tool result, available tools, timing, error details.

### Startup Validation

`Server.ValidateWiring()` checks all required services (timeline, interaction, stage, session, chat, alert, event publisher, etc.) are set before the HTTP server starts accepting requests. Called from `cmd/tarsy/main.go` after all setters. Prevents cryptic 503 errors from nil service fields at request time.

---

## E2E Testing (`test/e2e/`)

In-process e2e tests boot a full TARSy instance per test (`TestApp`) with real PostgreSQL (testcontainers, per-test schema), real event streaming, real WebSocket — only LLM (`ScriptedLLMClient` with dual sequential/agent-routed dispatch) and MCP servers (in-memory SDK via `mcpsdk.InMemoryTransport`) are mocked. The real `mcp.Client` → `mcp.ToolExecutor` pipeline is exercised. MCP test support via `pkg/mcp/testing.go` (`InjectSession`, `NewTestClientFactory`).

7 scenarios: Pipeline (comprehensive golden-file verification of all 4 data layers), FailureResilience (policy=any, exec summary fail-open), FailurePropagation (policy=all, fail-fast), Cancellation, Timeout, Concurrency (MaxConcurrentSessions), MultiReplica (cross-replica WS via NOTIFY/LISTEN).

Makefile: `test-unit` (pkg only), `test-e2e` (e2e only), `test-go` (all Go + coverage), `test` (Go + Python).

See `docs/archive/phase6-e2e-testing-design.md` for full details on test infrastructure, mock design, golden file system, and scenario coverage.

---

## Cross-Cutting Patterns

| Pattern | Description |
|---------|-------------|
| **Progressive DB writes** | Timeline events, messages, interactions written during execution, not batched at end |
| **Context-based cancellation** | `context.Context` drives timeouts and cancellation throughout; hierarchical: session 15m, iteration 120s |
| **Service layer** | Services over repositories; explicit transactions with `defer tx.Rollback()` before `tx.Commit()` |
| **Strategy pattern** | Controllers are pluggable strategies; controller factory maps iteration strategy → controller |
| **Lazy context building** | No stored output fields; `BuildStageContext()` called on-demand when next stage needs previous results |
| **Fail-fast validation** | Config validation at startup; reject oversized alerts at API layer (413 for > 1MB) |
| **Handler → Service → DB** | HTTP handlers: bind → validate → transform → service → response; `mapServiceError` for error mapping |
| **Author extraction** | `X-Forwarded-User` > `X-Forwarded-Email` > `"api-client"` |
| **Forced conclusion** | At max iterations: one extra LLM call without tools, asking for best conclusion with available data |
| **Queue = sessions table** | `FOR UPDATE SKIP LOCKED` claim pattern; `pod_id` ownership; orphan detection (all pods, no leader) |
| **Soft deletes** | `deleted_at` on AlertSession; 90-day retention; hard delete can be added later |
| **Native tools suppression** | When MCP tools are present, native tools (code execution, search) are disabled in Python |
| **Per-agent-execution MCP isolation** | Each agent execution gets its own MCP Client with independent SDK sessions; no shared state between stages or parallel agents |
| **Tool errors as content** | MCP tool errors → `ToolResult{IsError: true}` (LLM-observable). Go errors → `error` return (infrastructure only) |
| **Eager startup validation** | All configured MCP servers must initialize at startup (readiness probe fails otherwise); runtime degradation detected by HealthMonitor |
| **Multi-format input parsing** | ActionInput cascade: JSON → YAML → key-value → raw string; parsing in executor, not parser |
| **Fail-closed masking (MCP)** | Masking failure on MCP tool results → full redaction notice; secrets never leak to LLM/timeline |
| **Fail-open masking (alerts)** | Masking failure on alert payloads → continue with unmasked data; availability over secrecy for user-provided data |
| **Code maskers before regex** | Structural maskers (K8s Secrets) run first for precision, then regex patterns sweep remaining secrets |
| **Controller-level summarization** | Summarization is LLM orchestration, not MCP infrastructure; happens after ToolExecutor.Execute() in shared `executeToolCall()` |
| **Fail-open summarization** | Summarization LLM failure → use raw result; investigation continues with larger context cost |
| **Single-event tool lifecycle** | `llm_tool_call` uses streaming lifecycle (created→completed) — no separate tool_result event; args in metadata survive content update |
| **Two-tier truncation** | Storage truncation (8K tokens, UI-safe) independent from summarization input truncation (100K tokens, LLM safety net) |
| **Replace-not-merge override** | Per-alert MCP selection replaces chain server list entirely; override is the authoritative server set |
| **Backend from strategy** | `Backend` field resolved from iteration strategy via `ResolveBackend()` — not derived from LLM provider type. All callers pass it through `GenerateInput.Backend` |
| **Fail-fast chain execution** | Stage failure stops the chain immediately; no subsequent stages execute. Session gets the failed stage's status |
| **Fail-open executive summary** | Executive summary LLM failure → session still completes successfully; error stored in `executive_summary_error` field |
| **Session-level timeline events** | `executive_summary` events have null `stage_id`/`execution_id` (schema fields made optional in Phase 5.1) |
| **In-memory context passing** | Stage context flows through chain loop via `stageResult.finalAnalysis` (from `ExecutionResult.FinalAnalysis`); no additional DB query needed |
| **Non-blocking progress tracking** | `current_stage_index`/`current_stage_id` updated best-effort; failure is logged but doesn't stop execution |
| **Unified stage execution** | All stages use the same goroutine + WaitGroup + channel machinery regardless of agent count. A single-agent stage is N=1, not a special case. No separate sequential/parallel code paths |
| **WaitGroup over errgroup** | `sync.WaitGroup` + buffered channel chosen over `errgroup` because all agents must complete regardless of individual failures — success policy determines the overall outcome. `errgroup` cancels on first error, which is wrong for `policy: any` |
| **Automatic synthesis** | Synthesis runs automatically after every successful stage with >1 agent execution. No opt-out for multi-agent stages. Single-agent stages skip synthesis entirely. Synthesis creates its own Stage DB record (separate from investigation) |
| **Synthesis replaces investigation** | For multi-agent stages, synthesis result replaces the investigation result in `completedStages`. Subsequent stages see only the synthesized output, not raw per-agent results. Avoids redundancy and context window waste |
| **Synthesis failure is fatal** | If synthesis fails, the chain stops (fail-fast). No fail-open fallback — synthesis is a configured chain step that influences subsequent stages. Parallel agents' work is preserved in DB for debugging |
| **Timeline events for synthesis context** | Synthesis receives full investigation history via timeline events (not messages) — includes thinking content, tool calls, tool results, and final analyses. Messages lack thinking content entirely (no thinking field in Message schema) |
| **Tool call/summary dedup in formatting** | Shared `formatTimelineEvents()` deduplicates: when `llm_tool_call` is followed by `mcp_tool_summary`, shows tool header + summary content instead of raw result. Prevents bloated synthesis context |
| **Success policy defaulting** | `resolvedSuccessPolicy()`: stage config → `defaults.success_policy` → `SuccessPolicyAny` (fallback). `UpdateStageStatus()` also defaults nil to `SuccessPolicyAny`. Matches old TARSy behavior |
| **dbStageIndex tracking** | Chain loop tracks `dbStageIndex` separately from config stage index. Incremented for both investigation and synthesis stages. Ensures correct stage ordering when synthesis stages are inserted |
| **Replica naming convention** | Replicas named `{BaseName}-1`, `{BaseName}-2`, etc. Config resolution uses base agent name for registry lookup; display name only for DB records and logging |
| **Chat is a prompt concern** | Same controllers handle investigation and chat — `ChatContext` on `ExecutionContext` triggers chat-specific prompting. No separate chat controllers, same iteration limits, same `forceConclusion()` |
| **Unified timeline context for chat** | Chat context built from `GetSessionTimeline()` + `FormatInvestigationContext()` — no separate chat history builder. Each chat message creates `user_question` timeline event before agent runs; subsequent messages see full history |
| **No concurrency pool for chat** | Each chat message spawns a goroutine directly — no pool, no semaphore. One-at-a-time per chat (enforced) naturally limits load. If needed, a semaphore is a one-line addition |
| **Shared executor helpers** | `createToolExecutor()`, `resolveMCPSelection()`, `publishStageStatus()` refactored from `RealSessionExecutor` methods to package-level functions in `pkg/queue/executor.go` — shared by both investigation and chat executors |
| **Chat message cleanup on rejection** | If `Submit` rejects (active execution or shutting down), handler deletes the created `ChatUserMessage` to prevent orphaned records |
| **Chat shutdown ordering** | Chat executor stops before worker pool. Marks `stopped` (rejects new submissions with 503), cancels all active contexts, waits for goroutines to drain |
| **Cancel succeeds if either session or chat cancelled** | Cancel handler attempts both worker pool and chat cancellation; returns success if either succeeded. Prevents 409 errors when cancelling a chat on an already-completed session |
| **Background context for post-cancellation DB updates** | After cancellation/timeout, DB status updates and event publishing use `context.Background()` instead of the cancelled context — prevents failed writes from losing terminal status |
| **Status override from context error** | On context cancellation, agent execution status is derived from `ctx.Err()`: `DeadlineExceeded` → `timed_out`, other cancellation → `cancelled` — overriding the agent's raw reported status |
| **Auto-catchup on WebSocket subscribe** | New subscribers receive prior events for their channel immediately on subscription — no separate catchup request needed |
| **Startup wiring validation** | `Server.ValidateWiring()` checks all required services are set before HTTP server starts. Prevents cryptic 503s from nil service fields at request time |
| **In-process e2e testing** | E2e tests boot full TARSy in-process with mock LLM + in-memory MCP servers. Real DB (testcontainers), real WebSocket, real event streaming. Per-test schema isolation |
| **Dual-dispatch LLM mock** | `ScriptedLLMClient` uses sequential fallback + agent-routed dispatch. Parallel agents get deterministic responses via route matching on agent name from system prompt |
| **Real MCP stack in e2e tests** | E2e tests exercise the full `mcp.Client` → `mcp.ToolExecutor` pipeline backed by in-memory MCP SDK servers, not a custom mock — validates tool routing, name mangling, masking in every test |
| **Golden file verification** | Pipeline test asserts all 4 data layers via golden files (session, stages, timeline, 31 interaction details). Other tests use targeted assertions. `-update` flag regenerates goldens |

---

## Technology Stack

| Area | Choice |
|------|--------|
| Language (orchestrator) | Go |
| Language (LLM service) | Python |
| Database | PostgreSQL |
| ORM | Ent (type-safe, generated) |
| Migrations | golang-migrate + Atlas CLI |
| HTTP framework | Echo v5 (labstack/echo) |
| WebSocket | coder/websocket (RFC 6455) |
| Inter-service | gRPC (protobuf) |
| Config format | YAML with `{{.VAR}}` env interpolation |
| Local dev | Podman Compose |
| Testing | testcontainers-go for integration tests |
| MCP client | MCP Go SDK v1.3.0 (`github.com/modelcontextprotocol/go-sdk`) |
| Python LLM | google-genai (Gemini native), LangChain (multi-provider) |

---

## Deferred Items Tracker

### Deferred to Phase 8+

- **Real LangChainProvider**: Currently stubs to GoogleNativeProvider. Phase 8.2 adds real multi-provider support.
- **Runbook fetching**: `RunbookContent` uses builtin default. Phase 8.1 adds GitHub integration.
- **Per-provider code execution mapping**: Documented but not implemented until multi-LLM.

### Deferred (No Phase Specified)

- Audit trail
- LLM cost calculation (token counts stored, no $ calculation)
- Prometheus metrics
- Hard delete support (schema ready, not implemented)
- WebSocket origin validation (currently `InsecureSkipVerify: true`)

---

## References

- Full design docs for completed phases: `docs/archive/`
- Old TARSy codebase: `/home/igels/Projects/AI/tarsy-bot`
- Proto definition: `proto/llm_service.proto`
- Ent schemas: `ent/schema/`
- Config examples: `deploy/config/`
