# TARSy Architecture Context

Cumulative architectural knowledge from all completed phases. Read this alongside `project-plan.md` for full context when designing or implementing new phases.

**Last updated after**: Phase 3.4 (Real-time Streaming)

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
│   ├── controller/   # Iteration controllers (ReAct, NativeThinking, Synthesis), ReAct parser
│   ├── context/      # Context formatter, investigation formatter
│   └── prompt/       # Prompt builder, templates, instructions, components
├── config/           # Loader, registries, builtin config, enums, validator
├── database/         # Client, migrations
├── events/           # EventPublisher, ConnectionManager, NotifyListener
├── models/           # MCP selection, shared types
├── queue/            # Worker, WorkerPool, orphan detection, session executor
└── services/         # Session, Stage, Timeline, Message, Interaction, Chat, Event, Alert
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
   - Resolves chain config from `ChainRegistry` → gets first `StageConfig` → gets first `StageAgentConfig`
   - Creates `Stage` DB record (via `StageService.CreateStage`)
   - Creates `AgentExecution` DB record (via `StageService.CreateAgentExecution`)
   - Resolves agent config via hierarchy: `ResolveAgentConfig(cfg, chain, stage, agent)` → `ResolvedAgentConfig`
   - Builds `ExecutionContext` with all dependencies (LLMClient, ToolExecutor, services, publisher, prompt builder)
   - Creates agent via `AgentFactory.CreateAgent()` → `BaseAgent` with appropriate `Controller`
   - Calls `agent.Execute(ctx, execCtx, prevStageContext="")`
4. **BaseAgent.Execute()** → delegates to `Controller.Run()`
5. **Controller.Run()** executes the iteration loop (see below)
6. **Back in executor**: updates `AgentExecution` status, aggregates `Stage` status, returns `ExecutionResult`
7. **Worker** updates `AlertSession` with final status, `final_analysis`, `completed_at`

**Note**: Phase 3 only executes the first stage of the first agent. Phase 5 will extend the executor to loop through all stages and handle parallel agents.

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
`id`, `session_id`, `stage_id`, `execution_id`, `sequence_number`, `event_type` (llm_thinking/llm_response/llm_tool_call/tool_result/mcp_tool_call/mcp_tool_summary/error/user_question/executive_summary/final_analysis/code_execution/google_search_result/url_context_result), `status` (streaming/completed/failed/cancelled/timed_out), `content` (TEXT, grows during streaming), `metadata` (JSON), timestamps

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
}
```

Phase 3.2 provides a `StubToolExecutor`; Phase 4 will provide the MCP-backed implementation.

### SessionExecutor (`pkg/queue/types.go`)

```go
type SessionExecutor interface {
    Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult
}
```

Bridges the queue worker to the agent framework. Implementation: `RealSessionExecutor` in `pkg/queue/executor.go`.

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
```

Three-tier instruction composition: General SRE → MCP server instructions → custom agent instructions.

### EventPublisher (`pkg/events/publisher.go`)

```go
func (p *EventPublisher) Publish(ctx, sessionID, channel, payload) error        // Persistent (DB + NOTIFY)
func (p *EventPublisher) PublishTransient(ctx, channel, payload) error           // Transient (NOTIFY only)
```

### ConnectionManager (`pkg/events/manager.go`)

```go
func (m *ConnectionManager) HandleConnection(parentCtx, conn)
func (m *ConnectionManager) Broadcast(channel, event)
```

---

## Key Types

### ExecutionContext (`pkg/agent/context.go`)

Carries all runtime state for an agent execution: `SessionID`, `StageID`, `ExecutionID`, `AgentName`, `AlertData`, `AlertType`, `RunbookContent`, `Config` (ResolvedAgentConfig), `LLMClient`, `ToolExecutor`, `Services` (ServiceBundle), `PromptBuilder`, `EventPublisher`, `ChatContext`.

### ResolvedAgentConfig (`pkg/agent/context.go`)

Runtime configuration after hierarchy resolution (defaults → agent → chain → stage → stage-agent): `AgentName`, `IterationStrategy`, `LLMProvider`, `MaxIterations`, `IterationTimeout`, `MCPServers`, `CustomInstructions`.

### ServiceBundle (`pkg/agent/context.go`)

Service dependencies injected into controllers: `Timeline` (TimelineService), `Message` (MessageService), `Interaction` (InteractionService), `Stage` (StageService).

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
| `ChainRegistry` | `Get(chainID)`, `GetByAlertType(alertType)` | `ChainConfig`: AlertTypes, Stages[], Chat, LLMProvider, MaxIterations |
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

**Persistent events** (DB + NOTIFY): `timeline_event.created`, `timeline_event.completed`, `session.status`, `session.completed`, `stage.started`, `stage.completed`

**Transient events** (NOTIFY only, no DB): `stream.chunk` (LLM token deltas)

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
| Python LLM | google-genai (Gemini native), LangChain (multi-provider) |

---

## Deferred Items Tracker

### Deferred to Phase 4 (MCP Integration)

- **ActionInput parameter parsing**: ReAct parser keeps `ActionInput` as raw string. MCP client must parse into structured params (JSON → YAML → key-value → raw string cascade). See `docs/archive/phase3-iteration-controllers-design.md` deferred notes.
- **Tool name `server.tool` validation**: Currently validates dot exists. MCP client must split and validate both server and tool parts.
- **Real ToolExecutor implementation**: Replace `StubToolExecutor` with MCP-backed implementation.
- **Tool output streaming**: Phase 3.4 only publishes `timeline_event.created/completed` for tool calls. Phase 4 should add `stream.chunk` for live MCP output during execution.
- **MCP summarization call sites**: Prompt templates exist (Phase 3.3) but are not called yet.
- **Data masking**: Required for MCP tool results (moved from Phase 7).

### Deferred to Phase 5 (Chain Execution)

- **Executive summary generation**: Prompt templates exist (Phase 3.3) but call sites come with session completion logic.

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
- Stage status aggregation for mixed failures in parallel agents

---

## References

- Full design docs for completed phases: `docs/archive/`
- Old TARSy codebase: `/home/igels/Projects/AI/tarsy-bot`
- Proto definition: `proto/llm_service.proto`
- Ent schemas: `ent/schema/`
- Config examples: `deploy/config/`
