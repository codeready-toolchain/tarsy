# TARSy Architecture Context

Cumulative architectural knowledge from all completed phases. Read this alongside `project-plan.md` for full context when designing or implementing new phases.

**Last updated after**: Phase 5.1 (Chain Orchestration)

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
            └─ Layer 3-4: LLMInteraction / MCPInteraction (debug/observability)
  └─ Event (WebSocket distribution — transient)
  └─ Chat → ChatUserMessage (follow-up chat)
```

Key design: **no stored output fields** on Stage or AgentExecution. Context is built lazily via `Agent.BuildStageContext()` when the next stage needs it.

### Package Layout

```
pkg/
├── api/              # HTTP handlers, requests, responses, error mapping
├── agent/            # Agent interface, lifecycle, LLM client, tool executor
│   ├── controller/   # Iteration controllers (ReAct, NativeThinking, Synthesis), ReAct parser, tool execution, summarization
│   ├── context/      # Context formatter, investigation formatter, stage context builder
│   └── prompt/       # Prompt builder, templates, instructions, components
├── config/           # Loader, registries, builtin config, enums, validator
├── database/         # Client, migrations
├── events/           # EventPublisher, ConnectionManager, NotifyListener
├── masking/          # Data masking service (regex patterns, code maskers, K8s Secret masker)
├── mcp/              # MCP client infrastructure (client, executor, transport, health)
├── models/           # MCP selection, shared types
├── queue/            # Worker, WorkerPool, orphan detection, session executor
└── services/         # Session, Stage, Timeline, Message, Interaction, Chat, Event, Alert, SystemWarnings
ent/
├── schema/           # Ent schema definitions
deploy/
├── config/           # tarsy.yaml.example, llm-providers.yaml.example, .env.example
├── podman-compose.yml
proto/
└── llm_service.proto
llm-service/
└── llm/providers/    # Python LLM providers (GoogleNativeProvider, LangChainProvider stub)
```

---

## Session Execution Flow

The end-to-end happy path from alert submission to completion:

1. **API handler** receives `POST /api/v1/alerts` → validates → `AlertService.SubmitAlert()` → creates `AlertSession` (status=`pending`) with `chain_id` resolved from alert type
2. **Worker pool** polls for pending sessions → `SessionService.ClaimNextPendingSession()` uses `FOR UPDATE SKIP LOCKED` → sets status=`in_progress`, assigns `pod_id`
3. **SessionExecutor.Execute()** (`pkg/queue/executor.go`):
   - Resolves chain config from `ChainRegistry`
   - Initializes shared services (StageService, MessageService, TimelineService, InteractionService)
   - **Chain loop**: iterates over `chain.Stages` sequentially
     - Checks context cancellation before starting each stage
     - Updates session progress (`current_stage_index`, `current_stage_id`)
     - Publishes `stage.status` (started)
     - Calls `executeStage()` → `executeAgent()`:
       - Creates `Stage` DB record (via `StageService.CreateStage`)
       - Creates `AgentExecution` DB record (via `StageService.CreateAgentExecution`)
       - Resolves agent config via hierarchy: `ResolveAgentConfig(cfg, chain, stage, agent)` → `ResolvedAgentConfig`
       - Creates per-agent-execution MCP ToolExecutor (or stub) with `defer Close()`
       - Builds `ExecutionContext` with all dependencies
       - Creates agent via `AgentFactory.CreateAgent()` → `BaseAgent` with appropriate `Controller`
       - Calls `agent.Execute(ctx, execCtx, prevStageContext)`
       - Updates `AgentExecution` status, aggregates `Stage` status
     - Publishes `stage.status` (terminal status)
     - On failure: returns immediately (fail-fast, no subsequent stages)
     - On success: builds `prevStageContext` for next stage via `BuildStageContext()`
   - **Post-chain**: extracts `finalAnalysis` from last completed stage (reverse search)
   - **Executive summary**: direct LLM call (fail-open), creates session-level `executive_summary` timeline event
4. **BaseAgent.Execute()** → delegates to `Controller.Run()`
5. **Controller.Run()** executes the iteration loop (see below)
6. **Worker** updates `AlertSession` with final status, `final_analysis`, `executive_summary`, `completed_at`

**Note**: Phase 5.1 executes single-agent stages only. Phase 5.2 extends `executeStage()` to handle any number of agents per stage using the same goroutine + WaitGroup machinery. A single-agent stage is not a special case — it's a stage with N=1 agents. No separate code paths for single vs multi-agent execution.

## Iteration Loop Flows

### ReAct Controller (`pkg/agent/controller/react.go`)

1. `ToolExecutor.ListTools()` → get available tools
2. `PromptBuilder.BuildReActMessages()` → system + user messages (tools described in prompt text, NOT bound to LLM)
3. Store initial messages in DB
4. **Loop** (up to `MaxIterations`):
   - Call LLM with streaming (no tool bindings — ReAct uses text-based tool calling)
   - Parse response via `ParseReActResponse()` → extracts Thought, Action, ActionInput, or FinalAnswer
   - Store assistant message + record LLM interaction
   - If **final answer**: create `final_analysis` timeline event → return completed
   - If **tool call**: create `llm_tool_call` event → `ToolExecutor.Execute(toolCall)` → create `tool_result` event → append observation as user message → continue loop
   - If **malformed**: append format feedback as user message → continue
5. If max iterations reached: `forceConclusion()` — one more LLM call without tools

### NativeThinking Controller (`pkg/agent/controller/native_thinking.go`)

1. `PromptBuilder.BuildNativeThinkingMessages()` → system + user messages
2. Store initial messages, list tools
3. **Loop** (up to `MaxIterations`):
   - Call LLM with streaming AND tool bindings (Gemini structured function calling)
   - Create native tool events (code execution, grounding) if present
   - If **tool calls in response**: store assistant message with tool calls → execute each tool → append tool result messages (role=`tool` with `tool_call_id`) → continue
   - If **no tool calls**: this IS the final answer → create `final_analysis` event → return completed
4. If max iterations reached: `forceConclusion()` — call LLM WITHOUT tools to force text-only response

### Key difference

ReAct: tools described in system prompt text, LLM outputs text like `Action: server.tool`, parsed by Go. Works with any LLM provider.
NativeThinking: tools bound as structured definitions, LLM returns `ToolCallChunk` objects. Gemini-specific.

---

## Key Entity Fields

### AlertSession
`id`, `alert_data` (TEXT), `agent_type`, `alert_type`, `status` (pending/in_progress/cancelling/completed/failed/cancelled/timed_out), `chain_id`, `pod_id`, `final_analysis`, `executive_summary`, `mcp_selection` (JSON override), `author`, `runbook_url`, `deleted_at` (soft delete), timestamps (`created_at`, `started_at`, `completed_at`, `last_interaction_at`)

### Stage
`id`, `session_id`, `stage_name`, `stage_index`, `expected_agent_count`, `parallel_type` (multi_agent/replica, nullable), `success_policy` (all/any, nullable), `status`, `error_message`, timestamps

### AgentExecution
`id`, `stage_id`, `session_id` (denormalized), `agent_name`, `agent_index`, `iteration_strategy`, `status`, `error_message`, timestamps

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
| POST | `/api/v1/sessions/:id/cancel` | Cancel a running session |
| GET | `/ws` | WebSocket connection for real-time streaming |

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

Implementations: `ReActController`, `NativeThinkingController`, `SynthesisController`, `SingleCallController`.

Strategy-to-controller mapping:
- `react` → `ReActController` (text-parsed tools, any LLM via `langchain` backend)
- `native-thinking` → `NativeThinkingController` (Gemini structured function calling, `google-native` backend)
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

Key internal methods on `RealSessionExecutor`:
- `executeStage()` — creates Stage DB record, runs agent(s) within it using unified goroutine machinery (same code path for 1 or N agents)
- `executeAgent()` — per-agent-execution lifecycle: DB record → config resolution → MCP creation → agent execution → status update
- `buildStageContext()` — converts `[]stageResult` to `BuildStageContext()` input
- `generateExecutiveSummary()` — LLM call for session summary (fail-open)
- `createToolExecutor()` — MCP-backed executor or stub fallback
- `updateSessionProgress()` — non-blocking DB update for dashboard visibility
- `mapCancellation()` — maps context errors to session status (timed_out/cancelled)

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

### PromptBuilder (`pkg/agent/prompt/builder.go`)

```go
func (b *PromptBuilder) BuildReActMessages(execCtx, prevStageContext, tools) []ConversationMessage
func (b *PromptBuilder) BuildNativeThinkingMessages(execCtx, prevStageContext) []ConversationMessage
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

The `agent.EventPublisher` interface (`pkg/agent/context.go`) exposes typed methods: `PublishTimelineCreated`, `PublishTimelineCompleted`, `PublishStreamChunk`, `PublishSessionStatus`, `PublishStageStatus`.

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
Controller (ReAct: "server.tool" + raw text | NativeThinking: "server__tool" + JSON)
  → ToolExecutor.Execute(ToolCall)
    → NormalizeToolName: server__tool → server.tool (NativeThinking reverse mapping)
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

Summarization is an LLM orchestration concern, not an MCP infrastructure concern. The `ToolExecutor` lacks LLMClient, conversation context, EventPublisher, and services — all required for summarization. Instead, summarization happens in the controller after `ToolExecutor.Execute()` returns, via a shared `maybeSummarize()` function called from both ReAct and NativeThinking controllers through the common `executeToolCall()` path (`pkg/agent/controller/tool_execution.go`).

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

Both ReAct and NativeThinking controllers share tool execution logic through `executeToolCall()`, which handles: tool call event creation → `ToolExecutor.Execute()` → event completion → summarization check → MCPInteraction recording.

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

The `RealSessionExecutor.Execute()` method iterates over `chain.Stages` sequentially. Each stage runs a single agent (Phase 5.1). On stage failure, the chain stops immediately (fail-fast). Stage context accumulates in-memory via `[]stageResult` and is formatted into `prevStageContext` for each subsequent stage.

Internal types: `stageResult` (stageID, executionID, stageName, status, finalAnalysis, error), `agentResult`, `executeStageInput`, `generateSummaryInput`.

### Backend Derivation

Backend (Python provider routing) is resolved from iteration strategy, not from LLM provider type:

| Strategy | Backend | Reason |
|----------|---------|--------|
| `react` | `"langchain"` | Text-based tool calling, any provider via LangChain |
| `native-thinking` | `"google-native"` | Requires Google SDK for native thinking/tool calling |
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
    StageID    string `json:"stage_id"`    // may be empty on "started"
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

### ResolvedAgentConfig (`pkg/agent/context.go`)

Runtime configuration after hierarchy resolution (defaults → agent → chain → stage → stage-agent): `AgentName`, `IterationStrategy`, `LLMProvider`, `MaxIterations`, `IterationTimeout`, `MCPServers`, `CustomInstructions`, `Backend`.

`Backend` (`"google-native"` or `"langchain"`) is resolved from iteration strategy via `ResolveBackend()` in `pkg/agent/config_resolver.go`. Constants: `BackendGoogleNative`, `BackendLangChain`. This replaces the old approach of deriving backend from provider type.

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
- `"langchain"` → `LangChainProvider` (multi-provider, currently a stub delegating to GoogleNative)
- `"google-native"` → `GoogleNativeProvider` (Gemini-specific thinking features)

---

## Streaming & Events

### WebSocket Protocol

Client connects, subscribes to channels (`session:{id}`, `sessions`), receives events.

**Client actions**: `subscribe`, `unsubscribe`, `catchup` (with `last_event_id`), `ping`

**Persistent events** (DB + NOTIFY): `timeline_event.created`, `timeline_event.completed`, `session.status`, `stage.status`

**Transient events** (NOTIFY only, no DB): `stream.chunk` (LLM token deltas)

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

On reconnect, client sends `catchup` with `last_event_id`. Server returns missed events (limit: 200). If overflow, sends `catchup.overflow` signaling client to do full REST reload.

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

### Deferred to Phase 5.2 (Parallel Execution)

- **Parallel stage execution**: goroutine-per-agent, result aggregation, success policy (all/any), synthesis agent invocation, replica execution
- **Stage status aggregation for mixed failures**: when parallel agents have different outcomes

### Deferred to Phase 6/7

- **Real LangChainProvider**: Currently stubs to GoogleNativeProvider. Phase 6 adds real multi-provider support.
- **Runbook fetching**: `RunbookContent` uses builtin default. Phase 7 adds GitHub integration.
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
