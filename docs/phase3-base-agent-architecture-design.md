# Phase 3.1: Base Agent Architecture — Detailed Design

**Status**: 🔵 Design Phase
**Last Updated**: 2026-02-07

## Overview

This document details the Base Agent Architecture for the new TARSy implementation. Phase 3.1 establishes the foundational interfaces, the enhanced gRPC protocol, and the session executor framework that all subsequent phases build upon.

**Phase 3.1 Scope**: Interfaces, proto evolution, executor framework, Python LLM service cleanup. This phase produces a minimal working flow: alert → single-stage investigation (without tools) → result. Real tool execution (MCP) comes in Phase 4; multi-stage chains and parallelism in Phase 5; full iteration controllers in Phase 3.2.

**Key Design Principles:**
- All orchestration in Go (agent lifecycle, iteration control, conversation management, prompt building)
- Python is a thin LLM API proxy (receives messages via gRPC, calls LLM, streams back). Stateless for conversation data; caches SDK clients and thought signatures in memory.
- Strategy pattern for iteration controllers (pluggable execution strategies)
- Progressive DB writes during execution (not at the end)
- Context-based cancellation and timeouts throughout
- Configuration hierarchy resolution at runtime

**What This Phase Delivers:**
- Enhanced gRPC protocol supporting tool calls, tool definitions, and usage metadata
- Production-quality Python LLM service (single provider: Gemini)
- Go Agent interface and lifecycle management
- Go Iteration Controller interface
- Session Executor framework (replaces stub from Phase 2.3)
- Agent execution context
- Runtime configuration resolution
- Conversation management (message building, tool call/result flow)
- Basic "single-call" controller for end-to-end validation

**What This Phase Does NOT Deliver:**
- Full iteration controllers (Phase 3.2: ReAct, native thinking, synthesis, chat, etc.)
- Prompt templates and builder framework (Phase 3.3)
- MCP client and tool execution (Phase 4)
- Multi-stage chain orchestration (Phase 5)
- WebSocket streaming infrastructure (Phase 3.4)

---

## Architecture: Go/Python Boundary

### The Split

```
┌─────────────────────────────────────────────────────────────────┐
│                     Go Orchestrator                              │
│                                                                  │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────────┐   │
│  │   Session    │  │   Agent      │  │  Iteration           │   │
│  │   Executor   │──│   Instance   │──│  Controller           │   │
│  │             │  │              │  │  (ReAct, Native, etc.)│   │
│  └─────────────┘  └──────────────┘  └──────────┬───────────┘   │
│                                                  │               │
│                    ┌─────────────────────────────┼───────┐       │
│                    │                             │       │       │
│                    ▼                             ▼       ▼       │
│          ┌─────────────────┐          ┌──────────┐ ┌────────┐   │
│          │   Conversation  │          │  Prompt  │ │  MCP   │   │
│          │   Manager       │          │  Builder │ │ Client │   │
│          │  (messages,     │          │  (Go)    │ │ (Go)   │   │
│          │   tool results) │          └──────────┘ │Phase 4 │   │
│          └────────┬────────┘                       └────────┘   │
│                   │ gRPC (messages + tools + config)             │
│                   ▼                                              │
└───────────────────┬──────────────────────────────────────────────┘
                    │
                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Python LLM Service                             │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │  gRPC Servicer                                            │   │
│  │  - Receives: messages + tools + LLMConfig                 │   │
│  │  - Resolves API key from environment                      │   │
│  │  - Calls LLM provider API (Gemini, OpenAI, etc.)          │   │
│  │  - Streams back: text chunks, thinking chunks, tool calls │   │
│  │  - ZERO state, ZERO orchestration, ZERO MCP               │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

### What Go Owns

| Concern | Go Package | Notes |
|---------|-----------|-------|
| Agent interface & lifecycle | `pkg/agent/` | New package |
| Iteration controllers | `pkg/agent/controller/` | Strategy pattern |
| ReAct text parser | `pkg/agent/parser/` | Phase 3.2 — parses complete LLM text for tool calls |
| Session executor | `pkg/queue/executor.go` | Replaces stub |
| Prompt building | `pkg/agent/prompt/` | Phase 3.3 |
| Conversation management | `pkg/agent/conversation/` | Message building |
| MCP client & tools | `pkg/mcp/` | Phase 4 |
| Chain orchestration | `pkg/chain/` | Phase 5 |
| Configuration resolution | `pkg/agent/config.go` | Runtime resolution |
| State persistence | `pkg/services/` | Existing services |
| WebSocket streaming | `pkg/api/` | Phase 3.4 |

### What Python Owns

| Concern | Location | Notes |
|---------|----------|-------|
| LLM API calls | `llm-service/` | gRPC servicer |
| Provider backends | `llm-service/llm/providers/` | GoogleNative (Phase 3.1), LangChain (Phase 3.2) |
| Provider registry | `llm-service/llm/providers/registry.py` | Routes `backend` field to provider |
| Streaming responses | `llm-service/llm/servicer.py` | Chunks → gRPC stream |
| Transient retry logic | `llm-service/llm/providers/` | Rate limits, empty responses (hidden from Go) |
| Thought signature cache | `llm-service/llm/providers/google_native.py` | In-memory per execution_id, 1h TTL cleanup |
| Native tools exclusivity | `llm-service/llm/providers/` | MCP present → suppress native tools |
| Tool name conversion | `llm-service/llm/providers/` | `server.tool` ↔ provider-specific format |

### The Iteration Loop (Go-Driven)

The core investigation loop runs entirely in Go. Each "iteration" is a separate gRPC call to Python:

```
Go iteration loop:
    1. Build/update conversation messages
    2. Call Python via gRPC: Generate(messages, tools, config)
    3. Stream response chunks:
       - Text chunks → TimelineEvent (streaming)
       - Thinking chunks → TimelineEvent (streaming)
       - Tool call chunks → collect for execution
    4. On stream completion:
       - Update TimelineEvent (completed, final content)
       - Create LLMInteraction record (debug)
    5. If tool calls present:
       - Execute each tool via MCP client (Phase 4)
       - Add assistant message (with tool_calls) to conversation
       - Add tool result messages to conversation
       - Create MCPInteraction records (debug)
       - Go to step 1
    6. If no tool calls (LLM finished):
       - Extract final analysis
       - Return result
```

For ReAct controllers, step 5 works differently:
- Parse tool calls from the text response (not structured tool calls)
- Tool results added as "Observation:" in a user message
- Same loop otherwise

### Alert Data Handling

Alert data is **arbitrary text** — not parsed, not assumed to be JSON. Clients can send any content (JSON, plain text, YAML, etc.). It's stored as `TEXT` in the database and passed verbatim to the LLM in the user message.

**Size limit**: A reasonable limit is enforced at the API submission layer to prevent abuse and ensure LLM context doesn't blow up. Following old TARSy's pattern:

```go
// pkg/agent/constants.go

// MaxAlertDataSize is the maximum allowed size for alert data (1 MB).
// Alerts exceeding this limit are rejected at API submission time (HTTP 413).
// This prevents abuse and keeps LLM context manageable.
const MaxAlertDataSize = 1 * 1024 * 1024 // 1 MB
```

Enforcement happens in the HTTP handler (API layer), not in the agent:

```go
// pkg/api/handlers.go (alert submission handler)

if len(input.Data) > agent.MaxAlertDataSize {
    return echo.NewHTTPError(http.StatusRequestEntityTooLarge,
        fmt.Sprintf("alert data exceeds maximum size of %d bytes", agent.MaxAlertDataSize))
}
```

**Note**: Unlike old TARSy which truncated silently, new TARSy rejects oversized payloads with HTTP 413. This is cleaner — the client knows the data didn't arrive intact rather than processing a truncated alert that might produce misleading results.

---

## Proto/gRPC Evolution

### Current State (PoC)

The current proto has:
- `GenerateWithThinking` RPC with deprecated fields
- Basic `Message` type (role + content only)
- `ThinkingChunk` with thinking/response/error variants
- No tool support

### New Proto Design

The new proto adds tool support, usage metadata, and a cleaner message model. This is a breaking change from the PoC proto — the old `GenerateWithThinking` RPC and its message types will be removed entirely (no backward compatibility needed since new TARSy is not in production yet).

```protobuf
syntax = "proto3";

package llm.v1;

option go_package = "github.com/codeready-toolchain/tarsy/proto;llmv1";

service LLMService {
  // New: Production RPC for agent framework
  rpc Generate(GenerateRequest) returns (stream GenerateResponse);

  // PoC RPC — removed in Phase 3.1 (replaced by Generate)
  // rpc GenerateWithThinking(ThinkingRequest) returns (stream ThinkingChunk);
}

// ────────────────────────────────────────────────────────────
// New messages (Phase 3.1+)
// ────────────────────────────────────────────────────────────

// GenerateRequest is the input for a single LLM call.
// Go builds the full conversation and sends it each time.
// Python is stateless — each request is self-contained.
message GenerateRequest {
  string session_id = 1;                    // For logging/tracing
  string execution_id = 5;                  // AgentExecution ID — used by Python for thought signature cache key
  repeated ConversationMessage messages = 2; // Full conversation history
  LLMConfig llm_config = 3;                 // Provider configuration
  repeated ToolDefinition tools = 4;         // Available tools (empty = no tools)
}

// ConversationMessage represents a message in the LLM conversation.
// Supports all roles: system, user, assistant, tool.
message ConversationMessage {
  // Role of the message sender
  string role = 1;  // "system", "user", "assistant", "tool"

  // Text content of the message.
  // For system/user: the message text
  // For assistant: the text response (may coexist with tool_calls)
  // For tool: the tool execution result
  string content = 2;

  // For assistant messages that request tool execution.
  // Present when the LLM wants to call tools.
  repeated ToolCall tool_calls = 3;

  // For tool result messages (role = "tool").
  // Links this result back to the original tool call.
  string tool_call_id = 4;

  // For tool result messages (role = "tool").
  // The name of the tool that was called.
  string tool_name = 5;
}

// ToolDefinition describes a tool available to the LLM.
// Maps to MCP tool schemas discovered from MCP servers.
// Names use canonical "server.tool" format (e.g., "kubernetes-server.resources_get").
// Python providers convert to/from provider-specific formats as needed
// (e.g., Gemini requires "server__tool" — dots not allowed in function names).
message ToolDefinition {
  string name = 1;              // Canonical tool name: "server.tool" format
  string description = 2;       // Human-readable description
  string parameters_schema = 3; // JSON Schema string for tool parameters
}

// ToolCall represents a tool invocation requested by the LLM.
// Used both in streaming responses (ToolCallDelta) and conversation
// history (ConversationMessage.tool_calls).
message ToolCall {
  string id = 1;         // Unique call ID (for matching results)
  string name = 2;       // Tool name
  string arguments = 3;  // JSON string of arguments
}

// GenerateResponse is a streaming chunk from the LLM.
// Multiple chunks form a complete response.
message GenerateResponse {
  oneof content {
    TextDelta text = 1;
    ThinkingDelta thinking = 2;
    ToolCallDelta tool_call = 3;
    UsageInfo usage = 4;
    ErrorInfo error = 5;
    CodeExecutionDelta code_execution = 6; // Gemini code execution results
  }

  // True when this is the last chunk for this Generate call.
  // After this, no more chunks will be sent.
  bool is_final = 10;
}

// CodeExecutionDelta carries Gemini code execution results.
// When code_execution native tool is enabled, Gemini generates Python code,
// executes it in a sandbox, and returns the results. These are intermediate
// reasoning artifacts — the model's final text already incorporates the results.
// Streamed as deltas because the model may execute code multiple times per response.
message CodeExecutionDelta {
  string code = 1;    // The generated Python code
  string result = 2;  // Execution output (stdout/stderr)
}

// TextDelta is a chunk of the LLM's text response.
message TextDelta {
  string content = 1;
}

// ThinkingDelta is a chunk of the LLM's internal reasoning
// (Gemini native thinking, Claude extended thinking, etc.)
message ThinkingDelta {
  string content = 1;
}

// ToolCallDelta signals the LLM wants to call a tool.
// Multiple ToolCallDeltas may appear in a single response
// (LLMs can request multiple tool calls at once).
message ToolCallDelta {
  string call_id = 1;   // Unique ID for this call (for matching results)
  string name = 2;      // Tool name
  string arguments = 3; // JSON arguments (complete, not streamed incrementally)
}

// UsageInfo reports token consumption for this LLM call.
// Sent as the last content chunk (before is_final).
message UsageInfo {
  int32 input_tokens = 1;
  int32 output_tokens = 2;
  int32 total_tokens = 3;
  int32 thinking_tokens = 4; // Tokens used for native thinking (if applicable)
}

// ErrorInfo signals an error from the LLM provider.
message ErrorInfo {
  string message = 1;
  string code = 2;
  bool retryable = 3;
}
```

### LLMConfig Update

The existing `LLMConfig` (from Phase 2 proto) needs one new field for Phase 3.1:

```protobuf
message LLMConfig {
  // ... existing fields (provider, model, api_key_env, etc.) ...
  string backend = 10;  // Provider backend: "langchain" (default), "google-native", etc.
}
```

The `backend` field tells Python which provider implementation to use (see Q1 decision). Go sets it based on the iteration strategy during config resolution. If empty, defaults to `"langchain"`.

### Key Design Decisions

**1. Stateless per-request model**: Each `GenerateRequest` contains the FULL conversation history. Python never stores conversation state between calls. Go rebuilds messages each time. This is simpler, more debuggable, and matches how LLM APIs actually work (they're stateless too). Note: Python does cache SDK client instances (connection pools, auth tokens) and thought signatures in memory — this is infrastructure/provider state, not application state.

**2. Tool calls as structured data**: `ToolCallDelta` provides structured tool call information for native function calling (Gemini, OpenAI). For ReAct controllers, tool calls are parsed from text on the Go side — the proto doesn't need to handle that case.

**3. Separate tool result role**: `ConversationMessage` with `role = "tool"` carries tool results back to the LLM. The `tool_call_id` links results to their corresponding calls. This follows the OpenAI/Anthropic convention and maps cleanly to Gemini's `FunctionResponse`.

**4. Usage metadata**: `UsageInfo` enables token tracking, cost estimation, and the `max_tool_result_tokens` enforcement in Go.

**5. Clean break from PoC**: Old `GenerateWithThinking` RPC and its message types (`ThinkingRequest`, `ThinkingChunk`, `Message`) are removed entirely in Phase 3.1. No backward compatibility needed — new TARSy is not in production yet.

### Provider Mapping

The Python service translates between the provider-agnostic proto and provider-specific SDKs:

| Proto | Gemini | OpenAI | Anthropic |
|-------|--------|--------|-----------|
| `ConversationMessage(role="tool")` | `FunctionResponse` part | `tool` role message | `tool_result` content block |
| `ToolCallDelta` | `FunctionCall` part | `tool_calls` array | `tool_use` content block |
| `ToolDefinition` | `FunctionDeclaration` | `tools[].function` | `tools[].input_schema` |
| `ThinkingDelta` | Thinking part | Not applicable | Extended thinking block |

---

## Agent Interface (Go)

### Core Interface

```go
// pkg/agent/agent.go

// Agent defines the interface for all TARSy agents.
// Agents are the primary execution units — they investigate alerts
// using LLM calls and (optionally) MCP tools.
//
// Agents are created per-execution (not shared between sessions).
// Each AgentExecution record in the DB corresponds to one Agent instance.
type Agent interface {
    // Execute runs the agent's investigation.
    //
    // ctx carries the session timeout and cancellation signal.
    // execCtx provides all execution dependencies and state.
    // prevStageContext is the output from the previous stage (empty for first stage).
    //
    // Returns the execution result with status and final analysis.
    // All intermediate state (TimelineEvents, Messages, Interactions) is
    // written to DB progressively during execution by the iteration controller.
    Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

### Execution Result

```go
// pkg/agent/agent.go

// ExecutionStatus represents the status of an agent execution.
type ExecutionStatus string

const (
    ExecutionStatusPending   ExecutionStatus = "pending"
    ExecutionStatusActive    ExecutionStatus = "active"
    ExecutionStatusCompleted ExecutionStatus = "completed"
    ExecutionStatusFailed    ExecutionStatus = "failed"
    ExecutionStatusTimedOut  ExecutionStatus = "timed_out"
    ExecutionStatusCancelled ExecutionStatus = "cancelled"
)

// ExecutionResult is returned by Agent.Execute().
// Lightweight — all intermediate state was already written to DB during execution.
type ExecutionResult struct {
    // Status is the terminal status for this agent execution.
    Status ExecutionStatus

    // FinalAnalysis is the agent's conclusion (if completed).
    FinalAnalysis string

    // Error contains error details (if failed/timed_out).
    Error error

    // TokensUsed tracks total token consumption across all LLM calls.
    TokensUsed TokenUsage
}

// TokenUsage aggregates token consumption across multiple LLM calls.
type TokenUsage struct {
    InputTokens    int
    OutputTokens   int
    TotalTokens    int
    ThinkingTokens int
}
```

### Execution Context

```go
// pkg/agent/context.go

// ExecutionContext carries all dependencies and state needed by an agent
// during execution. Created by the session executor for each agent run.
//
// ExecutionContext is NOT the same as context.Context (Go's cancellation).
// context.Context handles cancellation/timeout.
// ExecutionContext provides dependencies and configuration.
type ExecutionContext struct {
    // Identity
    SessionID   string
    StageID     string
    ExecutionID string
    AgentName   string
    AgentIndex  int

    // Alert data (pulled from AlertSession by executor)
    // Arbitrary text — not parsed, not assumed to be JSON.
    // Passed directly to the LLM as part of the user message.
    // Oversized payloads rejected at API submission time (MaxAlertDataSize).
    AlertData string

    // Configuration (resolved from hierarchy)
    Config *ResolvedAgentConfig

    // Dependencies (injected by executor)
    LLMClient        LLMClient           // gRPC client to Python service
    Services         *ServiceBundle       // DB services for persistence
    // MCPClient     MCPClient            // Phase 4: MCP tool execution
    // EventPublisher EventPublisher      // Phase 3.4: WebSocket events
}

// ServiceBundle groups all service dependencies needed during execution.
type ServiceBundle struct {
    Timeline    *services.TimelineService
    Message     *services.MessageService
    Interaction *services.InteractionService
    Stage       *services.StageService
}

// ResolvedAgentConfig is the fully-resolved configuration for an agent execution.
// All hierarchy levels (defaults → chain → stage → agent) have been applied.
type ResolvedAgentConfig struct {
    AgentName         string
    IterationStrategy config.IterationStrategy
    LLMProvider       *config.LLMProviderConfig
    MaxIterations     int
    MCPServers        []string
    CustomInstructions string
    // MCPSelection  *models.MCPSelectionConfig  // Phase 4
}
```

### Agent Implementation Pattern

```go
// pkg/agent/base_agent.go

// BaseAgent provides the common agent implementation.
// It delegates iteration logic to a controller (strategy pattern).
//
// In old TARSy, each agent type (KubernetesAgent, ChatAgent, SynthesisAgent)
// was a separate class. In new TARSy, the AGENT is mostly the same —
// differentiation comes from configuration (custom_instructions, mcp_servers)
// and the iteration controller (strategy).
type BaseAgent struct {
    controller Controller
}

// NewBaseAgent creates an agent with the given iteration controller.
func NewBaseAgent(controller Controller) *BaseAgent {
    return &BaseAgent{controller: controller}
}

func (a *BaseAgent) Execute(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error) {
    // 1. Mark agent execution as active
    if err := execCtx.Services.Stage.UpdateAgentExecutionStatus(
        ctx, execCtx.ExecutionID, ExecutionStatusActive,
    ); err != nil {
        return nil, fmt.Errorf("failed to mark execution active: %w", err)
    }

    // 2. Delegate to iteration controller
    result, err := a.controller.Run(ctx, execCtx, prevStageContext)

    // 3. Handle context cancellation/timeout
    if err != nil {
        if ctx.Err() == context.DeadlineExceeded {
            return &ExecutionResult{Status: ExecutionStatusTimedOut, Error: err}, nil
        }
        if ctx.Err() == context.Canceled {
            return &ExecutionResult{Status: ExecutionStatusCancelled, Error: err}, nil
        }
        return &ExecutionResult{Status: ExecutionStatusFailed, Error: err}, nil
    }

    return result, nil
}

```

### Agent Factory

```go
// pkg/agent/factory.go

// AgentFactory creates Agent instances from resolved configuration.
// It selects the appropriate iteration controller based on the
// iteration_strategy in the resolved config.
type AgentFactory struct {
    llmClient LLMClient
    services  *ServiceBundle
}

func NewAgentFactory(llmClient LLMClient, services *ServiceBundle) *AgentFactory {
    return &AgentFactory{
        llmClient: llmClient,
        services:  services,
    }
}

// CreateAgent builds an Agent instance for the given execution context.
// The controller is selected based on execCtx.Config.IterationStrategy.
func (f *AgentFactory) CreateAgent(execCtx *ExecutionContext) (Agent, error) {
    controller, err := f.createController(execCtx)
    if err != nil {
        return nil, fmt.Errorf("failed to create controller for strategy %q: %w",
            execCtx.Config.IterationStrategy, err)
    }
    return NewBaseAgent(controller), nil
}

func (f *AgentFactory) createController(execCtx *ExecutionContext) (Controller, error) {
    switch execCtx.Config.IterationStrategy {
    case config.IterationStrategyReact:
        return nil, fmt.Errorf("react controller not yet implemented (Phase 3.2)")
    case config.IterationStrategyNativeThinking:
        return nil, fmt.Errorf("native thinking controller not yet implemented (Phase 3.2)")
    // ... other strategies (Phase 3.2)

    default:
        // Phase 3.1: basic single-call controller for validation
        return NewSingleCallController(execCtx), nil
    }
}
```

---

## Iteration Controller Interface (Go)

### Core Interface

```go
// pkg/agent/controller/controller.go

// Controller defines the iteration strategy interface.
// Each controller implements a different investigation pattern
// (ReAct, native thinking, synthesis, etc.)
//
// Controllers are responsible for:
// - Building the initial conversation (system prompt, user prompt)
// - Running the LLM call loop (call → parse → tool → repeat)
// - Writing TimelineEvents and Messages to DB during execution
// - Extracting the final analysis from the LLM response
//
// Controllers are NOT responsible for:
// - Agent lifecycle (handled by BaseAgent)
// - Session state management (handled by executor)
// - MCP client management (injected via ExecutionContext)
// - Cross-stage context formatting (handled by ContextFormatter)
type Controller interface {
    // Run executes the iteration strategy.
    //
    // ctx carries cancellation/timeout.
    // execCtx provides dependencies and configuration.
    // prevStageContext is text from previous stage (empty for first stage).
    //
    // Returns the execution result. All intermediate state (TimelineEvents,
    // Messages, LLMInteractions) written to DB during execution.
    Run(ctx context.Context, execCtx *ExecutionContext, prevStageContext string) (*ExecutionResult, error)
}
```

### Timeline Event Types

Controllers create `TimelineEvent` records during execution. Each event type has specific semantics:

| Event Type | Semantics | Streamed to Frontend? | Included in Cross-Stage Context? |
|---|---|---|---|
| `llm_thinking` | LLM reasoning/thought content. Covers both native model thinking (Gemini, `metadata.source = "native"`) and ReAct parsed thoughts (`"Thought: ..."`, `metadata.source = "react"`). Frontend renders them differently per source. | Yes (live) | Only for synthesis strategies |
| `llm_response` | Regular LLM text during intermediate iterations. Produced alongside tool calls in native thinking, or as intermediate output before the agent is done. Maps to old TARSy's `INTERMEDIATE_RESPONSE`. | Yes (live) | Yes |
| `llm_tool_call` | LLM requested a tool call (native function calling). Metadata: `tool_name`, `server_name`, `arguments`. | Yes | Yes (in ReAct/synthesis formatters) |
| `mcp_tool_call` | MCP tool execution was invoked. Metadata: `tool_name`, `server_name`. | Yes | Yes |
| `mcp_tool_summary` | MCP tool result summary. | Yes | Yes |
| `final_analysis` | Agent's final conclusion — no more iterations or tool calls. Maps to old TARSy's `FINAL_ANSWER`. Primary content for next-stage context. | Yes (live) | Yes (primary) |
| `user_question` | User question in chat mode. | Yes | No |
| `executive_summary` | High-level session summary. | Yes | No |

**Key distinction: `llm_response` vs `final_analysis`**

A single Gemini native thinking call can produce multiple event types. For example, during an intermediate iteration:

```
LLM call → response contains:
  ├── thinking content    → llm_thinking event
  ├── regular text        → llm_response event (the LLM is "talking" while also calling tools)
  └── tool calls          → llm_tool_call events
```

On the final iteration (no tool calls):

```
LLM call → response contains:
  ├── thinking content    → llm_thinking event
  └── conclusion text     → final_analysis event (no tool calls = agent is done)
```

With ReAct controllers, the LLM returns a single text blob that is **parsed** into structured parts (Thought, Action, Observation, Final Answer). The controller creates the appropriate event types based on parsed content.

### Controller Types (Phase 3.2)

These will be implemented in Phase 3.2, listed here for architectural context:

| Strategy | Controller | Tools? | Use Case |
|----------|-----------|--------|----------|
| `react` | ReActController | Yes (text-parsed) | Standard investigation |
| `native-thinking` | NativeThinkingController | Yes (structured) | Gemini native function calling |
| `synthesis` | SynthesisController | No | Synthesize parallel results |
| `synthesis-native-thinking` | SynthesisNativeController | No | Synthesis with native thinking |

**Dropped from old TARSy** (never used in production, can be added later if needed — strategy pattern allows new controllers without refactoring):
- `react-stage` (ReactStageController) — stage-specific data collection variant of ReAct
- `react-final-analysis` (FinalAnalysisController) — tool-less final analysis variant

> **Cleanup required**: Remove `react-stage` and `react-final-analysis` from existing Phase 2 code — enums in `pkg/config/enums.go`, YAML config examples, validation logic, and any references in built-in configurations.

### Phase 3.1: Basic Single-Call Controller

For Phase 3.1 validation, a minimal controller that makes one LLM call without tools:

```go
// pkg/agent/controller/single_call.go

// SingleCallController makes a single LLM call and returns the response.
// Used for Phase 3.1 validation only. Real controllers in Phase 3.2.
type SingleCallController struct {
    execCtx *ExecutionContext
}

func NewSingleCallController(execCtx *ExecutionContext) *SingleCallController {
    return &SingleCallController{execCtx: execCtx}
}

func (c *SingleCallController) Run(
    ctx context.Context,
    execCtx *ExecutionContext,
    prevStageContext string,
) (*ExecutionResult, error) {
    // 1. Build initial messages
    messages := c.buildMessages(execCtx, prevStageContext)

    // 2. Store system + user messages in DB
    for i, msg := range messages {
        _, err := execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
            SessionID:      execCtx.SessionID,
            StageID:        execCtx.StageID,
            ExecutionID:    execCtx.ExecutionID,
            SequenceNumber: i + 1,
            Role:           msg.Role,
            Content:        msg.Content,
        })
        if err != nil {
            return nil, fmt.Errorf("failed to store message: %w", err)
        }
    }

    // 3. Create TimelineEvent (streaming)
    // SingleCallController makes one LLM call with no tools, so the response
    // is always the final analysis (not an intermediate llm_response).
    event, err := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
        SessionID:      execCtx.SessionID,
        StageID:        execCtx.StageID,
        ExecutionID:    execCtx.ExecutionID,
        SequenceNumber: len(messages) + 1, // After system + user messages
        EventType:      timelineevent.EventTypeFinalAnalysis,
        Content:        "",
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create timeline event: %w", err)
    }

    // 4. Call LLM via gRPC
    var fullText strings.Builder
    var thinkingText strings.Builder
    var usage *TokenUsage

    stream, err := execCtx.LLMClient.Generate(ctx, &GenerateInput{
        SessionID: execCtx.SessionID,
        Messages:  messages,
        Config:    execCtx.Config.LLMProvider,
        Tools:     nil, // No tools in Phase 3.1
    })
    if err != nil {
        return nil, fmt.Errorf("LLM call failed: %w", err)
    }

    for chunk := range stream {
        switch c := chunk.(type) {
        case *TextChunk:
            fullText.WriteString(c.Content)
        case *ThinkingChunk:
            thinkingText.WriteString(c.Content)
        case *UsageChunk:
            usage = &TokenUsage{
                InputTokens:    int(c.InputTokens),
                OutputTokens:   int(c.OutputTokens),
                TotalTokens:    int(c.TotalTokens),
                ThinkingTokens: int(c.ThinkingTokens),
            }
        case *ErrorChunk:
            return nil, fmt.Errorf("LLM error: %s (code: %s)", c.Message, c.Code)
        }
    }

    // 5. Update TimelineEvent (completed)
    if err := execCtx.Services.Timeline.CompleteTimelineEvent(
        ctx, event.EventID, fullText.String(),
    ); err != nil {
        return nil, fmt.Errorf("failed to complete timeline event: %w", err)
    }

    // 6. Store assistant message
    _, err = execCtx.Services.Message.CreateMessage(ctx, models.CreateMessageRequest{
        SessionID:      execCtx.SessionID,
        StageID:        execCtx.StageID,
        ExecutionID:    execCtx.ExecutionID,
        SequenceNumber: len(messages) + 1,
        Role:           message.RoleAssistant,
        Content:        fullText.String(),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to store assistant message: %w", err)
    }

    // 7. Create LLMInteraction (debug record)
    // ... (omitted for brevity, creates debug record with full request/response)

    // 8. Return result
    tokenUsage := TokenUsage{}
    if usage != nil {
        tokenUsage = *usage
    }

    return &ExecutionResult{
        Status:        ExecutionStatusCompleted,
        FinalAnalysis: fullText.String(),
        TokensUsed:    tokenUsage,
    }, nil
}

func (c *SingleCallController) buildMessages(
    execCtx *ExecutionContext,
    prevStageContext string,
) []ConversationMessage {
    messages := []ConversationMessage{
        {
            Role:    message.RoleSystem,
            Content: fmt.Sprintf("You are %s, an AI SRE agent.\n\n%s",
                execCtx.AgentName, execCtx.Config.CustomInstructions),
        },
    }

    // Build user message with alert data
    // Alert data is arbitrary text — not assumed to be JSON.
    // It's included verbatim in the user message for the LLM to analyze.
    var userContent strings.Builder
    if prevStageContext != "" {
        userContent.WriteString("Previous investigation context:\n")
        userContent.WriteString(prevStageContext)
        userContent.WriteString("\n\nContinue the investigation based on the alert below.\n\n")
    }
    userContent.WriteString("## Alert Data\n\n")
    userContent.WriteString(execCtx.AlertData)

    messages = append(messages, ConversationMessage{
        Role:    message.RoleUser,
        Content: userContent.String(),
    })

    return messages
}

```

### Context Formatter

Context formatting is **separate from controllers**. Controllers handle the LLM call loop;
context formatters handle how a completed execution's artifacts are presented to the next stage's LLM.

Old TARSy uses two distinct formats depending on strategy:
- **Sequential stages**: passes the agent's final analysis (last assistant message)
- **Synthesis strategies**: passes the full conversation history (`ROLE: content` pairs)
- **Tool results**: formatted as `Observation: server.tool: {result}`
- **Cross-stage boundaries**: wrapped with `<!-- Analysis Result START/END -->` markers

```go
// pkg/agent/context/formatter.go

// ContextFormatter formats a completed agent execution's artifacts
// into text for consumption by the next stage's LLM.
//
// Different strategies need different formatting:
// - Sequential stages: final analysis only
// - Synthesis: full investigation history (conversation + tool results)
//
// The executor calls FormatStageContext after a stage completes,
// passing the result to the next stage as prevStageContext.
type ContextFormatter interface {
    // FormatExecution formats a single agent execution's timeline events
    // into text. Used by the executor when building stage context.
    FormatExecution(ctx context.Context, execution *ent.AgentExecution, events []*ent.TimelineEvent) (string, error)

    // FormatStageContext formats one or more agent executions from a completed
    // stage into text for the next stage. Handles both single-agent and
    // parallel (multi-agent) stages.
    FormatStageContext(ctx context.Context, stage *ent.Stage, executions []*ent.AgentExecution) (string, error)
}
```

#### Phase 3.1: Simple Context Formatter

For Phase 3.1, a type-aware formatter that labels events by type. Sufficient for single-call
executions (one `llm_response` event per execution).

```go
// pkg/agent/context/simple_formatter.go

// SimpleContextFormatter formats timeline events with type labels.
// Used in Phase 3.1. Phase 3.2 adds strategy-specific formatters
// (ReAct conversation history, synthesis investigation history).
type SimpleContextFormatter struct {
    timelineSvc *services.TimelineService
    stageSvc    *services.StageService
}

func (f *SimpleContextFormatter) FormatExecution(
    ctx context.Context,
    execution *ent.AgentExecution,
    events []*ent.TimelineEvent,
) (string, error) {
    var sb strings.Builder

    for _, event := range events {
        if event.Status != timelineevent.StatusCompleted {
            continue
        }

        switch event.EventType {
        case timelineevent.EventTypeFinalAnalysis:
            // Primary content for cross-stage context.
            sb.WriteString(fmt.Sprintf("## Analysis Result\n\n%s\n", event.Content))
        case timelineevent.EventTypeLlmResponse:
            // Intermediate LLM text (produced alongside tool calls in native thinking,
            // or as intermediate output before the agent is done).
            // Included in context so the next stage sees the full investigation trail.
            sb.WriteString(fmt.Sprintf("### LLM Response\n\n%s\n", event.Content))
        case timelineevent.EventTypeLlmThinking:
            // Thinking is internal reasoning — include for synthesis,
            // skip for sequential stages. Phase 3.2 will differentiate.
            continue
        case timelineevent.EventTypeLlmToolCall:
            // LLM requested a tool call (native function calling).
            toolName, _ := event.Metadata["tool_name"].(string)
            sb.WriteString(fmt.Sprintf("Tool request [%s]: %s\n", toolName, event.Content))
        case timelineevent.EventTypeMcpToolCall:
            toolName, _ := event.Metadata["tool_name"].(string)
            sb.WriteString(fmt.Sprintf("Tool call [%s]: %s\n", toolName, event.Content))
        case timelineevent.EventTypeMcpToolSummary:
            sb.WriteString(fmt.Sprintf("Tool result: %s\n", event.Content))
        default:
            sb.WriteString(event.Content)
            sb.WriteString("\n")
        }
    }

    return sb.String(), nil
}

func (f *SimpleContextFormatter) FormatStageContext(
    ctx context.Context,
    stage *ent.Stage,
    executions []*ent.AgentExecution,
) (string, error) {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("### Results from '%s' stage:\n\n", stage.StageName))

    for i, exec := range executions {
        events, err := f.timelineSvc.GetAgentTimeline(ctx, exec.ID)
        if err != nil {
            return "", fmt.Errorf("failed to get timeline for execution %s: %w", exec.ID, err)
        }

        if len(executions) > 1 {
            sb.WriteString(fmt.Sprintf("#### Agent %d: %s\n", i+1, exec.AgentName))
            sb.WriteString(fmt.Sprintf("**Status**: %s\n\n", exec.Status))
        }

        // Wrap content with boundaries to prevent markdown conflicts
        sb.WriteString("<!-- Analysis Result START -->\n")

        content, err := f.FormatExecution(ctx, exec, events)
        if err != nil {
            return "", err
        }
        // Escape HTML comment markers inside content
        content = strings.ReplaceAll(content, "<!--", "&lt;!--")
        content = strings.ReplaceAll(content, "-->", "--&gt;")
        sb.WriteString(content)

        sb.WriteString("<!-- Analysis Result END -->\n\n")
    }

    return sb.String(), nil
}
```

#### Phase 3.2+: Strategy-Specific Formatters (Future)

Phase 3.2 will add richer formatters matching old TARSy's patterns:

- **`ReActContextFormatter`** — formats full ReAct conversation history including
  tool calls and observations (e.g., `Observation: server.tool: {result}`).
  Used by synthesis strategies that need the full investigation trail.
- **`SynthesisContextFormatter`** — formats parallel agent results with metadata
  (agent name, provider, iteration strategy, status) and per-agent investigation history.
  Matches old TARSy's `format_previous_stages_context()`.
- **`NativeThinkingContextFormatter`** — includes structured tool call/result pairs
  from Gemini's native function calling.

The `ContextFormatter` interface is strategy-agnostic — the executor selects the
appropriate formatter based on the *consuming* stage's strategy (the stage that will
*read* the context), not the producing stage.

---

## LLM Client (Go)

### Client Interface

```go
// pkg/agent/llm_client.go

// LLMClient is the Go-side interface for calling the Python LLM service.
// It wraps the gRPC connection and provides a channel-based streaming API.
type LLMClient interface {
    // Generate sends a conversation to the LLM and returns a stream of chunks.
    // The returned channel is closed when the stream completes.
    // Errors are delivered as ErrorChunk values in the channel.
    Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error)

    // Close releases the gRPC connection.
    Close() error
}

// GenerateInput is the Go-side representation of a Generate request.
type GenerateInput struct {
    SessionID string
    Messages  []ConversationMessage
    Config    *config.LLMProviderConfig
    Tools     []ToolDefinition          // nil = no tools
}

// ConversationMessage is the Go-side message type.
// Role uses the message.Role enum from the ent schema.
type ConversationMessage struct {
    Role       message.Role // message.RoleSystem, RoleUser, RoleAssistant, RoleTool
    Content    string
    ToolCalls  []ToolCall   // For assistant messages
    ToolCallID string       // For tool result messages
    ToolName   string       // For tool result messages
}

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
    Name             string
    Description      string
    ParametersSchema string // JSON Schema
}

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
    ID        string
    Name      string
    Arguments string // JSON
}

// ChunkType identifies the kind of streaming chunk.
type ChunkType string

const (
    ChunkTypeText          ChunkType = "text"
    ChunkTypeThinking      ChunkType = "thinking"
    ChunkTypeToolCall      ChunkType = "tool_call"
    ChunkTypeCodeExecution ChunkType = "code_execution"
    ChunkTypeUsage         ChunkType = "usage"
    ChunkTypeError         ChunkType = "error"
)

// Chunk is the interface for all streaming chunk types.
type Chunk interface {
    chunkType() ChunkType
}

type TextChunk struct{ Content string }
type ThinkingChunk struct{ Content string }
type ToolCallChunk struct{ CallID, Name, Arguments string }
type CodeExecutionChunk struct{ Code, Result string }
type UsageChunk struct{ InputTokens, OutputTokens, TotalTokens, ThinkingTokens int32 }
type ErrorChunk struct{ Message, Code string; Retryable bool }

func (c *TextChunk) chunkType() ChunkType          { return ChunkTypeText }
func (c *ThinkingChunk) chunkType() ChunkType       { return ChunkTypeThinking }
func (c *ToolCallChunk) chunkType() ChunkType       { return ChunkTypeToolCall }
func (c *CodeExecutionChunk) chunkType() ChunkType  { return ChunkTypeCodeExecution }
func (c *UsageChunk) chunkType() ChunkType          { return ChunkTypeUsage }
func (c *ErrorChunk) chunkType() ChunkType          { return ChunkTypeError }
```

### gRPC Implementation

```go
// pkg/agent/llm_grpc.go

// GRPCLLMClient implements LLMClient using gRPC to the Python service.
type GRPCLLMClient struct {
    conn   *grpc.ClientConn
    client llmv1.LLMServiceClient
}

func NewGRPCLLMClient(addr string) (*GRPCLLMClient, error) {
    // Insecure credentials: Go and Python run in the same pod (localhost).
    // If deployment changes to separate pods/nodes, switch to TLS.
    conn, err := grpc.NewClient(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to LLM service: %w", err)
    }

    return &GRPCLLMClient{
        conn:   conn,
        client: llmv1.NewLLMServiceClient(conn),
    }, nil
}

func (c *GRPCLLMClient) Generate(ctx context.Context, input *GenerateInput) (<-chan Chunk, error) {
    // Build proto request
    req := &llmv1.GenerateRequest{
        SessionId: input.SessionID,
        Messages:  toProtoMessages(input.Messages),
        LlmConfig: toProtoLLMConfig(input.Config),
        Tools:     toProtoTools(input.Tools),
    }

    // Start streaming RPC
    stream, err := c.client.Generate(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("gRPC Generate failed: %w", err)
    }

    // Stream chunks to channel
    ch := make(chan Chunk, 32)
    go func() {
        defer close(ch)
        for {
            resp, err := stream.Recv()
            if err == io.EOF {
                return
            }
            if err != nil {
                // Phase 3.1: mark all gRPC errors as non-retryable since Python
                // handles transient retries internally (Q5). Phase 3.2+ can inspect
                // grpc status codes (Unavailable, ResourceExhausted) to set Retryable
                // for Go's strategic retry logic.
                ch <- &ErrorChunk{Message: err.Error(), Retryable: false}
                return
            }
            chunk := fromProtoResponse(resp)
            if chunk != nil {
                ch <- chunk
            }
        }
    }()

    return ch, nil
}

func (c *GRPCLLMClient) Close() error {
    return c.conn.Close()
}
```

---

## Session Executor (Go)

### Replacing the Stub

The Phase 2.3 stub executor returns "completed" immediately. Phase 3.1 replaces it with a real executor that:
1. Resolves chain configuration
2. Creates Stage and AgentExecution DB records
3. Resolves agent configuration from hierarchy
4. Creates an Agent via AgentFactory
5. Runs the agent
6. Returns the result

### Executor Implementation

```go
// pkg/queue/executor.go

// SessionExecutor processes alert sessions by executing agent chains.
// Implements the queue.SessionExecutor interface defined in Phase 2.3.
type SessionExecutor struct {
    cfg          *config.Config
    dbClient     *ent.Client
    agentFactory *agent.AgentFactory
    ctxFormatter agentctx.ContextFormatter
    services     *agent.ServiceBundle
}

func NewSessionExecutor(
    cfg *config.Config,
    dbClient *ent.Client,
    llmClient agent.LLMClient,
) *SessionExecutor {
    svcBundle := &agent.ServiceBundle{
        Timeline:    services.NewTimelineService(dbClient),
        Message:     services.NewMessageService(dbClient),
        Interaction: services.NewInteractionService(dbClient),
        Stage:       services.NewStageService(dbClient),
    }

    return &SessionExecutor{
        cfg:          cfg,
        dbClient:     dbClient,
        agentFactory: agent.NewAgentFactory(llmClient, svcBundle),
        ctxFormatter: agentctx.NewSimpleContextFormatter(svcBundle.Timeline, svcBundle.Stage),
        services:     svcBundle,
    }
}

// Execute processes a single session. Called by the worker after claiming.
// This is Phase 3.1: single-stage execution only.
// Multi-stage and parallel execution added in Phase 5.
func (e *SessionExecutor) Execute(ctx context.Context, session *ent.AlertSession) *queue.ExecutionResult {
    log := slog.With("session_id", session.ID)
    log.Info("Starting session execution")

    // 1. Resolve chain configuration
    chain, err := e.cfg.GetChain(session.ChainID)
    if err != nil {
        log.Error("Chain not found", "chain_id", session.ChainID, "error", err)
        return &queue.ExecutionResult{
            Status: string(agent.ExecutionStatusFailed),
            Error:  fmt.Errorf("chain %q not found: %w", session.ChainID, err),
        }
    }

    // 2. Execute first stage only (Phase 3.1)
    // Phase 5 will add multi-stage sequential execution with:
    //   for i, stageConfig := range chain.Stages {
    //       result, err := e.executeStage(ctx, session, chain, stageConfig, i, prevStageContext)
    //       prevStageContext, _ = e.ctxFormatter.FormatStageContext(ctx, stage, executions)
    //   }
    if len(chain.Stages) == 0 {
        return &queue.ExecutionResult{
            Status: string(agent.ExecutionStatusFailed),
            Error:  fmt.Errorf("chain %q has no stages", session.ChainID),
        }
    }

    stageConfig := chain.Stages[0]
    result, err := e.executeStage(ctx, session, chain, stageConfig, 0, "")
    if err != nil {
        return &queue.ExecutionResult{
            Status: string(agent.ExecutionStatusFailed),
            Error:  err,
        }
    }

    return &queue.ExecutionResult{
        Status:        string(result.Status),
        FinalAnalysis: result.FinalAnalysis,
        Error:         result.Error,
    }
}

func (e *SessionExecutor) executeStage(
    ctx context.Context,
    session *ent.AlertSession,
    chain *config.ChainConfig,
    stageConfig config.StageConfig,
    stageIndex int,
    prevStageContext string,
) (*agent.ExecutionResult, error) {
    log := slog.With("session_id", session.ID, "stage_name", stageConfig.Name)

    // 1. Create Stage record
    stage, err := e.services.Stage.CreateStage(ctx, models.CreateStageRequest{
        SessionID:          session.ID,
        StageName:          stageConfig.Name,
        StageIndex:         stageIndex,
        ExpectedAgentCount: len(stageConfig.Agents),
        Status:             string(agent.ExecutionStatusActive),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create stage: %w", err)
    }

    // 2. Execute first agent (Phase 3.1: single agent per stage)
    // Phase 5 will add parallel agent execution
    if len(stageConfig.Agents) == 0 {
        return nil, fmt.Errorf("stage %q has no agents configured", stageConfig.Name)
    }
    agentConfig := stageConfig.Agents[0]

    // 3. Create AgentExecution record
    execution, err := e.services.Stage.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
        StageID:           stage.StageID,
        SessionID:         session.ID,
        AgentName:         agentConfig.Name,
        AgentIndex:        1,
        IterationStrategy: string(agentConfig.IterationStrategy),
        Status:            string(agent.ExecutionStatusPending),
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create agent execution: %w", err)
    }

    // 4. Resolve agent configuration from hierarchy
    resolvedConfig, err := e.resolveAgentConfig(chain, stageConfig, agentConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to resolve agent config: %w", err)
    }

    // 5. Build execution context
    execCtx := &agent.ExecutionContext{
        SessionID:   session.ID,
        StageID:     stage.StageID,
        ExecutionID: execution.ExecutionID,
        AgentName:   agentConfig.Name,
        AgentIndex:  1,
        AlertData:   session.AlertData, // Arbitrary text from the alert submission
        Config:      resolvedConfig,
        LLMClient:   e.agentFactory.LLMClient(),
        Services:    e.services,
    }

    // 6. Create agent and execute
    agentInstance, err := e.agentFactory.CreateAgent(execCtx)
    if err != nil {
        return nil, fmt.Errorf("failed to create agent: %w", err)
    }

    log.Info("Executing agent", "agent_name", agentConfig.Name,
        "strategy", resolvedConfig.IterationStrategy)

    result, err := agentInstance.Execute(ctx, execCtx, prevStageContext)
    if err != nil {
        return nil, fmt.Errorf("agent execution failed: %w", err)
    }

    // 7. Update AgentExecution status
    if err := e.services.Stage.UpdateAgentExecutionStatus(
        ctx, execution.ExecutionID, result.Status,
    ); err != nil {
        log.Error("Failed to update execution status", "error", err)
    }

    // 8. Update Stage status (aggregation)
    if err := e.services.Stage.UpdateStageStatus(ctx, stage.StageID); err != nil {
        log.Error("Failed to update stage status", "error", err)
    }

    return result, nil
}
```

---

## Configuration Resolution at Runtime

### Hierarchy

Configuration is resolved at runtime from multiple levels (lowest to highest priority):

```
1. System Defaults    (tarsy.yaml → defaults section)
2. Agent Definition   (tarsy.yaml → agents section or builtin.go)
3. Chain Config       (tarsy.yaml → agent_chains → chain-level llm_provider, max_iterations)
4. Stage Config       (tarsy.yaml → agent_chains → stages → stage-level overrides)
5. Stage Agent Config (tarsy.yaml → agent_chains → stages → agents[] → per-agent overrides)
```

### Resolution Implementation

```go
// pkg/agent/config_resolver.go

// ResolveAgentConfig builds the final agent configuration by applying
// the hierarchy: defaults → agent definition → chain → stage → stage-agent.
func (e *SessionExecutor) resolveAgentConfig(
    chain *config.ChainConfig,
    stageConfig config.StageConfig,
    agentConfig config.StageAgentConfig,
) (*agent.ResolvedAgentConfig, error) {
    // Start with system defaults
    defaults := e.cfg.Defaults

    // Get agent definition (built-in or user-defined)
    agentDef, err := e.cfg.GetAgent(agentConfig.Name)
    if err != nil {
        return nil, fmt.Errorf("agent %q not found: %w", agentConfig.Name, err)
    }

    // Resolve iteration strategy (stage-agent > agent-def > defaults)
    strategy := defaults.IterationStrategy
    if agentDef.IterationStrategy != "" {
        strategy = agentDef.IterationStrategy
    }
    if agentConfig.IterationStrategy != "" {
        strategy = agentConfig.IterationStrategy
    }

    // Resolve LLM provider (stage-agent > chain > defaults)
    providerName := defaults.LLMProvider
    if chain.LLMProvider != "" {
        providerName = chain.LLMProvider
    }
    if agentConfig.LLMProvider != "" {
        providerName = agentConfig.LLMProvider
    }
    provider, err := e.cfg.GetLLMProvider(providerName)
    if err != nil {
        return nil, fmt.Errorf("LLM provider %q not found: %w", providerName, err)
    }

    // Resolve max iterations (stage-agent > stage > chain > agent-def > defaults)
    maxIter := 20 // built-in default
    if defaults.MaxIterations != nil {
        maxIter = *defaults.MaxIterations
    }
    if agentDef.MaxIterations != nil {
        maxIter = *agentDef.MaxIterations
    }
    if chain.MaxIterations != nil {
        maxIter = *chain.MaxIterations
    }
    if stageConfig.MaxIterations != nil {
        maxIter = *stageConfig.MaxIterations
    }
    if agentConfig.MaxIterations != nil {
        maxIter = *agentConfig.MaxIterations
    }

    // Resolve MCP servers (stage-agent > agent-def)
    mcpServers := agentDef.MCPServers
    if len(agentConfig.MCPServers) > 0 {
        mcpServers = agentConfig.MCPServers
    }

    return &agent.ResolvedAgentConfig{
        AgentName:          agentConfig.Name,
        IterationStrategy:  strategy,
        LLMProvider:        provider,
        MaxIterations:      maxIter,
        MCPServers:         mcpServers,
        CustomInstructions: agentDef.CustomInstructions,
    }, nil
}
```

---

## Conversation Management

### How Messages Flow

```
Iteration 1:
  Go builds: [system, user]
  Go sends to Python → LLM response (text only, no tools)
  Go stores: [system, user, assistant] in Message table
  Done.

Iteration 1 (with tools):
  Go builds: [system, user]
  Go sends to Python → LLM response includes tool_calls
  Go stores: [system, user, assistant(+tool_calls)] in Message table
  Go executes tools, stores results
  Go stores: [tool_result_1, tool_result_2] in Message table

Iteration 2:
  Go builds: [system, user, assistant(+tool_calls), tool_result_1, tool_result_2]
  Go sends to Python → LLM response (text only)
  Go stores: [assistant] in Message table
  Done.
```

### Message Types

| Role | Content | ToolCalls | ToolCallID | When |
|------|---------|-----------|------------|------|
| `system` | Instructions | — | — | Start of conversation |
| `user` | Alert data, prompt | — | — | Start of conversation |
| `assistant` | LLM text response | Tool calls (if any) | — | After each LLM call |
| `tool` | Tool execution result | — | Links to call ID | After tool execution |

### For ReAct (Phase 3.2)

ReAct controllers don't use structured tool calls. Instead:
- Tools are described in the system prompt as text
- LLM responds with `Action: tool_name\nAction Input: {...}` in the text
- Go parses the text to extract tool calls
- Tool results sent as a `user` message: `Observation: <result>`
- No `ToolCall` or `tool` role messages needed

This means the proto supports both patterns:
- Native function calling: uses `ToolCallDelta` + `tool` role messages
- ReAct: uses only text messages (tool parsing in Go)

---

## Message Schema Migration

### Current Schema

The current `Message` Ent schema (from Phase 2) has:
- `role` enum: `system`, `user`, `assistant`
- `content` text
- No tool-related fields

### Required Changes

Phase 3.1 introduces tool support in the proto and conversation management. The Message table needs matching fields so Go can persist the full conversation including tool calls and tool results. These fields are added now (Phase 3.1) even though actual tool execution comes in Phase 4 — the conversation management and proto already support tools, and the schema should be ready.

**Ent schema changes** (`ent/schema/message.go`):

```go
// Updated Message schema for Phase 3.1
func (Message) Fields() []ent.Field {
    return []ent.Field{
        // ... existing fields unchanged ...

        field.Enum("role").
            Values("system", "user", "assistant", "tool"),
            //                                     ^^^^ NEW: tool result messages

        // NEW: Tool-related fields for native function calling
        field.JSON("tool_calls", []map[string]interface{}{}).
            Optional().
            Comment("For assistant messages: tool calls requested by LLM [{id, name, arguments}]"),
        field.String("tool_call_id").
            Optional().
            Nillable().
            Comment("For tool messages: links result to the original tool call"),
        field.String("tool_name").
            Optional().
            Nillable().
            Comment("For tool messages: name of the tool that was called"),
    }
}
```

### Field Mapping

| Message Role | content | tool_calls | tool_call_id | tool_name |
|---|---|---|---|---|
| `system` | Instructions | nil | nil | nil |
| `user` | Alert data, prompt, observations | nil | nil | nil |
| `assistant` | LLM text response | `[{id, name, args}]` or nil | nil | nil |
| `tool` | Tool execution result | nil | links to call | tool name |

### Migration

This is an additive migration (new columns with `Optional`/`Nillable`) — no existing data is affected. Ent auto-migration handles it:

```go
// No manual migration needed. Ent's auto-migration adds:
// - New enum value "tool" to role column
// - New nullable columns: tool_calls (JSON), tool_call_id (VARCHAR), tool_name (VARCHAR)
```

### CreateMessageRequest Update

The `models.CreateMessageRequest` struct needs corresponding fields:

```go
type CreateMessageRequest struct {
    // ... existing fields ...
    Role           message.Role // message.RoleSystem, RoleUser, RoleAssistant, RoleTool
    Content        string
    ToolCalls      []ToolCallData `json:"tool_calls,omitempty"`      // For assistant messages
    ToolCallID     string         `json:"tool_call_id,omitempty"`    // For tool messages
    ToolName       string         `json:"tool_name,omitempty"`       // For tool messages
}

type ToolCallData struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // JSON string
}
```

---

## Python LLM Service Updates

### Cleanup Required

The current PoC Python service needs these changes for Phase 3.1:

1. **Replace PoC `GenerateWithThinking` RPC with new `Generate` RPC** (clean break — remove old RPC and message types entirely)
2. **Remove old PoC proto types** (`ThinkingRequest`, `ThinkingChunk`, `Message`, etc.)
3. **Add tool support** — pass `ToolDefinition` to Gemini as `FunctionDeclaration`
4. **Return structured tool calls** — when Gemini returns `FunctionCall`, stream as `ToolCallDelta`
5. **Handle tool result messages** — translate `role="tool"` to Gemini's `FunctionResponse`
6. **Return usage metadata** — stream `UsageInfo` with token counts
7. **Improve error handling** — classify errors as retryable vs non-retryable

### Thinking Configuration

**Decision**: Thinking budget/config stays in Python for Phase 3.1. Python decides the thinking configuration per model name, matching old TARSy's pattern:

- **Gemini 2.5 Pro**: `thinking_budget=32768`
- **Gemini 2.5 Flash**: `thinking_budget=24576`
- **Gemini 3**: `thinking_level=HIGH`
- **Fallback**: `thinking_budget=24576` for unknown models

The proto `LLMConfig` does **not** include a `thinking_budget` field. Python infers it from the model name. This keeps things simple — Go doesn't need to know about provider-specific thinking configuration.

If we later need Go-side control (e.g., per-chain thinking budgets), we can add `optional int32 thinking_budget = N` to `LLMConfig` in the proto. For now, YAGNI.

### Python Service Architecture

The Python service uses a **dual-provider model** with a registry pattern (Q1 decision). Two provider backends, each implementing the same `LLMProvider` ABC:

1. **`GoogleNativeProvider`** — Uses `google-genai` SDK directly. For Gemini-specific thinking features (thinking content, thought signatures, ThinkingConfig). Used when `backend = "google-native"`.
2. **`LangChainProvider`** — Uses LangChain for multi-provider abstraction (Google, OpenAI, Anthropic, XAI, VertexAI). For all iteration strategies that don't require native thinking. Used when `backend = "langchain"` (default).

SDK client instances are **cached at startup** (Q4 decision) — all major LLM providers recommend reuse for connection pooling and auth token caching. API key changes require service restart.

```
llm-service/
├── llm/
│   ├── server.py              # gRPC server (async)
│   ├── servicer.py            # LLMServicer (routes to providers via registry)
│   ├── providers/
│   │   ├── base.py            # LLMProvider ABC
│   │   ├── registry.py        # Provider registry (backend_name → LLMProvider)
│   │   ├── google_native.py   # Gemini native thinking (google-genai SDK) — Phase 3.1
│   │   ├── langchain_provider.py  # LangChain multi-provider — Phase 3.2
│   │   └── (future: openai_native.py, litellm_provider.py, etc.)
│   └── proto/                 # Generated proto code
├── pyproject.toml
└── uv.lock
```

### Provider Interface

```python
# llm/providers/base.py

class LLMProvider(ABC):
    """Abstract base class for LLM providers."""

    @abstractmethod
    async def generate(
        self,
        messages: list[ConversationMessage],
        config: LLMConfig,
        tools: list[ToolDefinition] | None = None,
    ) -> AsyncIterator[GenerateResponse]:
        """Generate a streaming response from the LLM.

        Yields GenerateResponse chunks (text, thinking, tool_calls, usage, error).
        """
        ...
```

### GoogleNativeProvider (Phase 3.1)

```python
# llm/providers/google_native.py

class GoogleNativeProvider(LLMProvider):
    """Gemini provider using google-genai SDK for native thinking features."""

    def __init__(self, api_key: str):
        # Client cached at startup — reused across all requests (Q4 decision)
        self._client = genai.Client(api_key=api_key, http_options={'api_version': 'v1beta'})
        # Thought signatures cached per execution_id for multi-turn reasoning continuity (Q3 decision)
        # Keyed by execution_id (not session_id) because a session has multiple agents/stages
        # Cleaned up after 1h TTL (well above typical agent execution duration)
        self._thought_signatures: dict[str, tuple[bytes, float]] = {}  # exec_id → (signature, timestamp)

    async def generate(self, messages, config, tools=None):
        # Convert proto messages to Gemini format
        gemini_contents = self._to_gemini_contents(messages)
        gemini_tools = self._to_gemini_tools(tools) if tools else None

        # Native tools mutual exclusivity (Q6 decision):
        # If MCP tools present, suppress native tools and log warning
        native_tools_config = None
        if tools:
            if config.native_tools:
                logger.warning("MCP tools present — suppressing native_tools (Gemini limitation)")
        else:
            native_tools_config = self._build_native_tools(config.native_tools)

        # Tool name conversion (Q7 decision):
        # Canonical "server.tool" → Gemini "server__tool" (dots not allowed in function names)
        if gemini_tools:
            gemini_tools = self._convert_tool_names_to_gemini(gemini_tools)

        # Stream response
        async for chunk in self._client.aio.models.generate_content_stream(
            model=config.model,
            contents=gemini_contents,
            config=genai.types.GenerateContentConfig(
                tools=gemini_tools,
                thinking_config=self._get_thinking_config(config.model),
                # ... other config
            ),
        ):
            for part in chunk.candidates[0].content.parts:
                if part.text and part.thought:
                    yield thinking_delta(part.text)
                elif part.text:
                    yield text_delta(part.text)
                elif part.function_call:
                    yield tool_call_delta(
                        call_id=str(uuid4()),
                        name=self._convert_tool_name_from_gemini(part.function_call.name),
                        arguments=json.dumps(part.function_call.args),
                    )
                elif hasattr(part, 'executable_code') and part.executable_code:
                    yield code_execution_delta(
                        code=part.executable_code.code,
                        result="",  # Result comes in next part
                    )
                elif hasattr(part, 'code_execution_result') and part.code_execution_result:
                    yield code_execution_delta(
                        code="",
                        result=part.code_execution_result.output,
                    )
                # Cache thought signature for next turn (Q3 decision)
                if hasattr(part, 'thought_signature') and part.thought_signature:
                    self._thought_signatures[config.execution_id] = part.thought_signature

        # Usage info
        if chunk.usage_metadata:
            yield usage_info(
                input_tokens=chunk.usage_metadata.prompt_token_count,
                output_tokens=chunk.usage_metadata.candidates_token_count,
                total_tokens=chunk.usage_metadata.total_token_count,
            )
```

### Error Handling in Providers (Q5 Decision)

Python providers handle **transient retries** internally, hidden from Go:
- Rate limit errors (429): up to 3 retries with exponential backoff
- Empty responses: up to 3 retries with 3s delay
- If retries exhausted: return `ErrorInfo(retryable=true)` so Go can make strategic decisions
- Non-retryable errors (auth, config): return `ErrorInfo(retryable=false)` immediately

Go retains full control via gRPC context — timeouts and cancellation propagate through to Python and interrupt retry loops, backoff sleeps, and in-flight LLM calls immediately.

### LangChainProvider (Phase 3.2)

Added in Phase 3.2 when ReAct controllers need multi-provider support. Uses LangChain for streaming, tool binding, message conversion, and token tracking across all providers (Google, OpenAI, Anthropic, XAI, VertexAI). Matches old TARSy's `LLMClient` pattern. Same `LLMProvider` interface — Go doesn't know which backend is used.

---

## Testing Strategy

### Unit Tests (No Database)

```go
// Test agent factory creates correct controller for strategy
func TestAgentFactory_CreateAgent(t *testing.T) { ... }

// Test config resolution hierarchy
func TestResolveAgentConfig_Hierarchy(t *testing.T) { ... }

// Test conversation message building
func TestConversationManager_BuildMessages(t *testing.T) { ... }

// Test LLM client chunk parsing
func TestGRPCLLMClient_ParseChunks(t *testing.T) { ... }
```

### Integration Tests (Real Postgres)

```go
// Test session executor creates Stage + AgentExecution records
func TestSessionExecutor_CreateRecords(t *testing.T) { ... }

// Test executor with mock LLM client (returns canned response)
func TestSessionExecutor_SingleStage(t *testing.T) { ... }

// Test executor handles LLM errors gracefully
func TestSessionExecutor_LLMError(t *testing.T) { ... }

// Test executor respects context cancellation
func TestSessionExecutor_Cancellation(t *testing.T) { ... }
```

### Python Tests

```python
# Test Gemini provider with mock genai client
def test_google_provider_generate(): ...

# Test tool call conversion (proto ↔ Gemini FunctionCall)
def test_tool_call_conversion(): ...

# Test tool result conversion (proto ↔ Gemini FunctionResponse)
def test_tool_result_conversion(): ...
```

---

## Service Method Naming Alignment

The Phase 2 service methods have some naming mismatches with the Phase 3.1 design. During Phase 3.1 implementation, rename the existing methods to match the cleaner names used in this design. Clean naming takes priority over avoiding changes.

| Design Doc Name | Current Name (Phase 2) | Action | Notes |
|---|---|---|---|
| `UpdateAgentExecutionStatus(ctx, id, status)` | `UpdateAgentStatus(ctx, id, status, errorMsg)` | **Rename** | Clearer name; keep `errorMsg` parameter |
| `UpdateStageStatus(ctx, stageID)` | `AggregateStageStatus(ctx, stageID)` | **Rename** | `UpdateStageStatus` is clearer; implementation still aggregates |
| `CompleteTimelineEvent(ctx, eventID, content)` | `CompleteTimelineEvent(ctx, req, eventID)` | **Simplify** | Flatten to direct parameters instead of request struct |

**Rename rules**:
- Rename the method, update all callers (including tests)
- Keep the implementation logic unchanged
- If the new signature is simpler (fewer params), update callers to match
- Run tests after each rename to ensure nothing breaks

---

## Implementation Checklist

### Phase 3.1: Base Agent Architecture

**Proto/gRPC Evolution:**
- [ ] Design new proto messages (GenerateRequest, GenerateResponse, ConversationMessage, ToolDefinition, ToolCall, etc.)
- [ ] Add `CodeExecutionDelta` message to `GenerateResponse` oneof (Q3)
- [ ] Add `backend` field to `LLMConfig` (Q1)
- [ ] Update `ToolDefinition.name` comment to document canonical `server.tool` format (Q7)
- [ ] Add new `Generate` RPC to proto
- [ ] Remove old `GenerateWithThinking` RPC and PoC message types (clean break, not in production)
- [ ] Regenerate Go proto code
- [ ] Regenerate Python proto code
- [ ] Update Go gRPC client
- [ ] Update Python gRPC servicer

**Python LLM Service Cleanup:**
- [ ] Create provider interface (`llm/providers/base.py`) — `LLMProvider` ABC
- [ ] Create provider registry (`llm/providers/registry.py`) — routes `backend` field to provider (Q1)
- [ ] Implement `GoogleNativeProvider` (`llm/providers/google_native.py`) with cached SDK client (Q4)
- [ ] Implement thought signature caching in `GoogleNativeProvider` (Q3 — in-memory per execution_id, 1h TTL)
- [ ] Implement native tools mutual exclusivity (Q6 — suppress native_tools when MCP tools present, log warning)
- [ ] Implement tool name conversion: `server.tool` ↔ `server__tool` for Gemini (Q7)
- [ ] Implement transient retry logic: rate limits (3x backoff), empty responses (3x 3s) (Q5)
- [ ] Implement new `Generate` RPC in servicer — routes via provider registry
- [ ] Add tool definition → Gemini FunctionDeclaration conversion
- [ ] Add Gemini FunctionCall → ToolCallDelta conversion
- [ ] Add tool result message → Gemini FunctionResponse conversion
- [ ] Add UsageInfo streaming
- [ ] Add error classification (retryable vs non-retryable) with `ErrorInfo.retryable` (Q5)
- [ ] Forward SDK streaming chunks as-is — no buffering (Q8)
- [ ] Remove deprecated field usage in servicer
- [ ] Write Python tests
- [ ] (Phase 3.2) Implement `LangChainProvider` (`llm/providers/langchain_provider.py`) for ReAct controllers (Q1)

**Message Schema Migration:**
- [ ] Add `tool` value to `role` enum in `ent/schema/message.go`
- [ ] Add `tool_calls` JSON field (optional)
- [ ] Add `tool_call_id` string field (optional, nillable)
- [ ] Add `tool_name` string field (optional, nillable)
- [ ] Regenerate Ent code (`go generate ./ent`)
- [ ] Update `models.CreateMessageRequest` with tool fields
- [ ] Update `MessageService.CreateMessage()` to populate new fields
- [ ] Run auto-migration to apply schema changes

**Service Method Renames:**
- [ ] Rename `UpdateAgentStatus` → `UpdateAgentExecutionStatus`
- [ ] Rename `AggregateStageStatus` → `UpdateStageStatus`
- [ ] Simplify `CompleteTimelineEvent` signature
- [ ] Update all callers and tests

**Agent Interface (Go):**
- [ ] Create `pkg/agent/` package
- [ ] Define Agent interface
- [ ] Define ExecutionResult struct
- [ ] Implement BaseAgent (delegates to controller)
- [ ] Define ExecutionContext struct (including AlertData field)
- [ ] Define ResolvedAgentConfig struct
- [ ] Define ServiceBundle struct
- [ ] Define MaxAlertDataSize constant
- [ ] Implement AgentFactory

**Iteration Controller Interface (Go):**
- [ ] Create `pkg/agent/controller/` package
- [ ] Define Controller interface
- [ ] Implement SingleCallController (Phase 3.1 validation)

**Context Formatter (Go):**
- [ ] Create `pkg/agent/context/` package
- [ ] Define ContextFormatter interface
- [ ] Implement SimpleContextFormatter (type-aware event labels, HTML comment boundaries)
- [ ] (Phase 3.2) Implement ReActContextFormatter (full conversation with tool observations)
- [ ] (Phase 3.2) Implement SynthesisContextFormatter (parallel agent results with metadata)

**LLM Client (Go):**
- [ ] Create LLMClient interface
- [ ] Define Chunk types (TextChunk, ThinkingChunk, ToolCallChunk, UsageChunk, ErrorChunk, CodeExecutionChunk)
- [ ] Define GenerateInput, ConversationMessage, ToolDefinition, ToolCall types
- [ ] Implement GRPCLLMClient
- [ ] Replace old `pkg/llm/client.go` stub

**Session Executor (Go):**
- [ ] Implement real SessionExecutor (replaces stub)
- [ ] Implement single-stage execution flow
- [ ] Implement configuration resolution from hierarchy
- [ ] Create Stage and AgentExecution records during execution
- [ ] Integrate with AgentFactory
- [ ] Update `cmd/tarsy/main.go` to use real executor

**Testing:**
- [ ] Unit tests: AgentFactory, config resolution, conversation building, chunk parsing
- [ ] Integration tests: executor with mock LLM, DB record creation, cancellation
- [ ] Python tests: provider, tool conversion, usage info
- [ ] End-to-end test: alert submission → session execution → completion (with mock LLM)

**Alert Data Handling:**
- [ ] Add `MaxAlertDataSize` constant (1 MB) in `pkg/agent/constants.go`
- [ ] Add size validation in alert submission handler (HTTP 413 if exceeded)
- [ ] Pass `session.AlertData` into `ExecutionContext` in executor

**Cleanup:**
- [ ] Remove `pkg/queue/executor_stub.go` (or keep behind build tag for testing)
- [ ] Remove old `pkg/llm/client.go` stub
- [ ] Update main.go to initialize real LLM client and executor

---

## Design Decisions

**All orchestration in Go**: Agent lifecycle, iteration control loops, prompt building, conversation management, and MCP tool execution all live in Go. Python is a thin LLM API proxy. Rationale: Go is the orchestrator by design; keeping logic in Go means single-language debugging, native concurrency, and type safety for the complex orchestration layer.

**Stateless per-request gRPC model**: Each `GenerateRequest` contains the full conversation history. Python never stores state between calls. Rationale: Matches how LLM APIs work (they're stateless), simplifies Python service, enables easy retry/recovery, Go has full control over conversation state.

**Strategy pattern for controllers**: Iteration controllers are pluggable strategies, not agent subclasses. All agents use `BaseAgent` with a selected controller. Rationale: Old TARSy has per-agent-type classes (KubernetesAgent, ChatAgent), but the actual differentiation comes from configuration (instructions, MCP servers) and iteration strategy — not code. Strategy pattern decouples these concerns.

**Channel-based streaming in Go**: `LLMClient.Generate()` returns `<-chan Chunk` instead of callback-based streaming. Rationale: Idiomatic Go, easy to compose with select/context, natural backpressure.

**Provider-agnostic proto**: The proto uses a common message model that maps to all LLM providers (Gemini, OpenAI, Anthropic). Provider-specific translation happens in Python. Rationale: Go code never deals with provider quirks; adding a new provider only requires a Python adapter. The `backend` field in `LLMConfig` routes requests to the right Python provider implementation without changing the proto message format.

**Dual-provider model (Q1)**: Python has two provider backends behind a shared `LLMProvider` ABC: `GoogleNativeProvider` (google-genai SDK for thinking features) and `LangChainProvider` (LangChain for multi-provider abstraction). This matches old TARSy's proven dual-client pattern. A provider registry makes the system extensible — new backends can be added without refactoring existing code.

**ReAct parsing in Go (Q2)**: The ReAct text parser lives in Go (`pkg/agent/parser/`), not Python. The parser runs on complete LLM text (not streaming) — it's a line-by-line state machine, straightforward in Go. Streaming UI detection (simple substring checks) is separate and trivial. This keeps all orchestration logic in Go and the proto clean (no "ReAct mode" in Python).

**Cached SDK clients (Q4)**: Python caches SDK client instances at startup, matching old TARSy and all provider recommendations. Connection pooling, auth token caching, and TLS handshake reuse are critical for performance with up to 100 LLM calls per session.

**Two-layer error handling (Q5)**: Python handles transient retries (rate limits, empty responses) internally. Go handles strategic retries (provider failover, session-level retry budget) and retains full control via gRPC context cancellation which cuts through Python's retry loops instantly.

**Separate `tool` role for results**: Tool execution results use `role="tool"` messages with `tool_call_id` linking. Rationale: Follows OpenAI/Anthropic convention, maps cleanly to Gemini's FunctionResponse, clearly distinguishes tool results from user messages.

**Phase 3.1 includes minimal e2e**: The SingleCallController enables submitting an alert and getting a response (without tools). Rationale: Validates the full stack (Go executor → gRPC → Python → LLM → response → DB) early, catches integration issues before building complex controllers.

**Configuration resolution at creation time**: Agent config is fully resolved BEFORE creating the agent instance. The agent receives a `ResolvedAgentConfig` with all hierarchy applied. Rationale: Clean separation — resolution logic is in the executor, agents don't need registry access.

**Alert data as opaque text**: Alert data is arbitrary text, not assumed to be JSON. It's stored as `TEXT` in the DB and passed verbatim in the user message. Old TARSy assumed JSON (`Dict[str, Any]`) but that was a historical artifact. New TARSy treats it as opaque text — simpler, no parsing, works with any format.

**Alert data on ExecutionContext**: Alert data is pulled from `ent.AlertSession` by the executor and placed on `ExecutionContext.AlertData` for controllers to use when building messages. This avoids controllers needing to query the session table and keeps the dependency flow clean (executor resolves everything, controller consumes).

**Reject oversized alerts (not truncate)**: New TARSy returns HTTP 413 for alerts exceeding `MaxAlertDataSize` (1 MB) instead of silently truncating. Rationale: Processing a truncated alert produces misleading results. Better to tell the client their data didn't arrive intact.

**Message schema includes tool fields now**: Tool-related fields (`tool` role, `tool_calls`, `tool_call_id`, `tool_name`) are added to the Message table in Phase 3.1, even though tool execution comes in Phase 4. Rationale: The proto and conversation management already support tools. Adding the schema now avoids a migration gap where the Go conversation builder can't persist tool messages.

**Thinking config stays in Python**: Thinking budget/level is determined by the Python service based on model name, not passed from Go via proto. Rationale: Matches old TARSy pattern, keeps proto simple, thinking config is highly provider-specific. Can add proto support later if Go-side control becomes needed.

**Thought signatures in Python memory (Q3)**: `GoogleNativeProvider` caches thought signatures (opaque binary blobs for Gemini multi-turn reasoning continuity) in memory, keyed by `execution_id` (not `session_id` — a session has multiple stages and parallel agents, each with its own conversation). Entries are cleaned up after 1 hour TTL (well above typical agent execution duration). Acceptable because Python and Go share the same pod/container lifecycle — if the pod restarts, sessions are re-queued anyway.

**Code execution as proto metadata (Q3)**: Gemini code execution results are exposed through a `CodeExecutionDelta` in the `GenerateResponse` oneof, not hidden in Python. Go can store them in LLMInteraction records and optionally stream to the frontend.

**Native tools exclusivity in Python (Q6)**: Python silently suppresses `native_tools` when MCP tools are present (Gemini API limitation). Logs a warning. Matches old TARSy behavior. Python is the right layer because it talks to the Gemini API and understands the constraint.

**Canonical tool names in proto (Q7)**: `ToolDefinition.name` uses readable `server.tool` format. Each Python provider converts to/from its required format (e.g., Gemini: `server__tool`). Provider-specific naming restrictions are implementation details.

**Forward SDK streaming chunks as-is (Q8)**: No buffering in Python. SDK streaming granularity is already reasonable. Frontend handles any smoothing needed.

**ContextFormatter separate from Controller**: Cross-stage context formatting is NOT a controller concern. Controllers handle the LLM call loop; `ContextFormatter` handles how completed execution artifacts are presented to the next stage. The executor selects the formatter based on the *consuming* stage's strategy. Phase 3.1 uses `SimpleContextFormatter` (type-aware event labels, HTML comment boundaries matching old TARSy). Phase 3.2 adds strategy-specific formatters (ReAct conversation history, synthesis investigation history with agent metadata). Old TARSy's `format_previous_stages_context()` is the reference implementation.

---

## Decided Against

**Python iteration controllers**: Not implementing controllers in Python. Rationale: Would require Python to manage conversation state, call MCP tools, and coordinate with Go for persistence — creating a distributed state problem. Go-based controllers are simpler (single process, no network boundaries for state).

**Agent subclasses per type**: Not creating KubernetesAgent, ChatAgent, etc. as separate Go types. Rationale: In old TARSy, these classes differ mainly in configuration (instructions, MCP servers), not behavior. Strategy pattern (BaseAgent + Controller) provides the same flexibility without subclass proliferation.

**Bidirectional streaming for tool calls**: Not using bidirectional gRPC streaming (Go sends tool results mid-stream). Rationale: Adds complexity to both sides. Sequential request-response calls are simpler: Go sends conversation → Python responds → Go executes tools → Go sends updated conversation. Each call is independent and debuggable.

**Conversation state in Python**: Not storing conversation history in Python between calls. Rationale: Violates stateless principle, creates consistency problems if Python service restarts, Go already has the conversation in the Message table.

**Shared proto types between agent and config**: Not reusing config types (e.g., `config.StageAgentConfig`) in the agent package. Rationale: Config types have YAML tags and validation concerns; agent types need clean runtime interfaces. `ResolvedAgentConfig` bridges the gap.

**Hot-swappable controllers**: Not supporting changing iteration strategy mid-execution. Rationale: YAGNI. Strategy is determined at agent creation time from config. If a different strategy is needed, configure a different stage in the chain.

**Streaming tool call arguments**: Not streaming tool call arguments incrementally (some providers support this). Rationale: Tool calls are typically small JSON objects. Waiting for the complete arguments simplifies parsing and MCP invocation. Can optimize later if needed.

**Native SDKs only (no LangChain)**: Not replacing LangChain with per-provider native SDK implementations (Q1). Rationale: LangChain provides battle-tested multi-provider abstraction for streaming, tool binding, message conversion, and token tracking. Reimplementing this for each provider would be significant Phase 6 work with no clear benefit. Old TARSy's dual-client pattern (LangChain + native Gemini) is proven.

**ReAct parsing in Python**: Not parsing ReAct text responses in Python to return structured `ToolCallDelta` (Q2). Rationale: Would make Python aware of iteration strategies and TARSy's ReAct format, violating the "thin proxy" principle. The parser works on complete text (not streaming), making it a straightforward state machine in Go.

**Per-request SDK client creation**: Not creating fresh SDK clients for each gRPC call (Q4). Rationale: All major LLM providers explicitly recommend against it — loses connection pooling, repeats auth handshakes, risks auth rate limiting.

**Provider-state round-tripping through proto**: Not adding `provider_state` blob to the proto for thought signatures (Q3). Rationale: Python in-memory caching is simpler, matches old TARSy, and Python/Go share the same lifecycle. Can add proto support later if needed.

---

## MCP in Go (Phase 4 Preview)

Since the user raised this as a concern, here's a brief preview of how MCP stdio transport works in Go:

```go
// Phase 4 implementation — included here for context only

// Go has excellent subprocess management for stdio MCP transport.
// The os/exec package provides Cmd with StdinPipe/StdoutPipe.
// Goroutines handle async reading naturally (no event loop needed).

cmd := exec.CommandContext(ctx, "npx", "-y", "kubernetes-mcp-server@0.0.54",
    "--read-only", "--disable-destructive",
    "--kubeconfig", os.Getenv("KUBECONFIG"))

stdin, _ := cmd.StdinPipe()
stdout, _ := cmd.StdoutPipe()
cmd.Start()

// Write JSON-RPC request
json.NewEncoder(stdin).Encode(request)

// Read JSON-RPC response (goroutine handles async)
json.NewDecoder(stdout).Decode(&response)
```

There are also Go MCP client libraries (`mark3labs/mcp-go`) that handle the JSON-RPC protocol, session management, and transport abstraction. Stdio subprocess management in Go is actually cleaner than in Python because goroutines handle concurrent reading/writing without async/await complexity.

---

## Next Steps

After approval of this design:

1. **Implement proto evolution** — new messages, new RPC, regenerate code
2. **Update Python LLM service** — new provider architecture, Gemini provider, Generate RPC
3. **Create Go agent package** — interfaces, BaseAgent, AgentFactory
4. **Create Go controller package** — interface, SingleCallController
5. **Implement Go LLM client** — GRPCLLMClient replacing old stub
6. **Implement Session Executor** — replaces stub, single-stage execution
7. **Implement config resolution** — hierarchy resolution logic
8. **Update main.go** — wire up real executor, LLM client
9. **Write tests** — unit + integration + Python tests
10. **Validate e2e** — submit alert → execute → complete (with real Gemini call)

---

## References

- Old TARSy Agent Framework: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/agents/`
- Old TARSy Iteration Controllers: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/agents/iteration_controllers/`
- Old TARSy Chain Orchestration: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/services/`
- Phase 2 Database Design: `docs/phase2-database-persistence-design.md`
- Phase 2 Configuration Design: `docs/phase2-configuration-system-design.md`
- Phase 2 Queue/Worker Design: `docs/phase2-queue-worker-system-design.md`
- Current Proto: `proto/llm_service.proto`
