# Phase 3.2: Iteration Controllers — Detailed Design

**Status**: 🔵 Design Phase  
**Last Updated**: 2026-02-08

## Overview

This document details the Iteration Controller implementations for the new TARSy. Phase 3.2 builds on the foundation established in Phase 3.1 (base agent architecture, Controller interface, gRPC protocol, LLM client) to implement the actual iteration strategies that drive agent investigations.

**Phase 3.2 Scope**: Implementation of all iteration controllers — ReAct (text-parsed tool calling), Native Thinking (Gemini structured function calling), Synthesis (tool-less summarization), Chat variants, and Final Analysis. Also includes the ReAct parser (Go), shared iteration patterns, tool execution interface (stub for Phase 4 MCP integration), forced conclusion logic, and LLM interaction recording.

**Key Design Principles:**
- All iteration logic in Go — controllers own the loop, LLM calls, tool dispatch, and conversation management
- Strategy pattern — each controller implements `agent.Controller` interface
- Progressive DB writes — timeline events, messages, and LLM interactions written during execution (not batched at end)
- Context-based cancellation — `context.Context` drives timeouts and cancellation throughout
- Tool execution is abstracted behind a `ToolExecutor` interface — Phase 3.2 defines the interface, Phase 4 (MCP) provides the implementation
- Prompt building is minimal/placeholder — full prompt system comes in Phase 3.3

**What This Phase Delivers:**
- ReAct controller with text-based tool parsing and observation loop
- Native Thinking controller with Gemini structured function calling
- Synthesis controller (tool-less single LLM call)
- Chat controller variants (investigation context + user question)
- Final Analysis controller (tool-less comprehensive analysis)
- ReAct parser (Go implementation)
- ToolExecutor interface (with stub implementation for testing)
- Shared iteration patterns (max iterations, forced conclusion, error tracking)
- LLM interaction recording for all controllers

**What This Phase Does NOT Deliver:**
- MCP client and real tool execution (Phase 4)
- Full prompt templates and builder framework (Phase 3.3)
- Multi-stage chain orchestration (Phase 5)
- WebSocket streaming infrastructure (Phase 3.4)
- Session pause/resume (dropped — new TARSy uses forced conclusion instead)

---

## Architecture Overview

### Controller Hierarchy

```
agent.Controller (interface — defined in Phase 3.1)
├── SingleCallController        (Phase 3.1 — already implemented)
├── ReActController             (Phase 3.2 — text-parsed tool loop)
├── NativeThinkingController    (Phase 3.2 — Gemini function calling loop)
├── SynthesisController         (Phase 3.2 — tool-less single call)
└── FinalAnalysisController     (Phase 3.2 — tool-less comprehensive analysis)
```

### Strategy-to-Controller Mapping

| Iteration Strategy | Controller | Tools? | LLM Backend | Use Case |
|---|---|---|---|---|
| `react` | `ReActController` | Yes (text-parsed) | `langchain` | Standard investigation with any LLM provider |
| `native-thinking` | `NativeThinkingController` | Yes (structured) | `google-native` | Gemini-specific with native reasoning |
| `synthesis` | `SynthesisController` | No | `langchain` | Synthesize parallel stage results |
| `synthesis-native-thinking` | `SynthesisController` | No | `google-native` | Synthesis with Gemini native thinking |
| *(no new strategy)* | `FinalAnalysisController` | No | *(from config)* | Final comprehensive analysis |

**Key design decisions:**
- `synthesis` and `synthesis-native-thinking` use the **same** `SynthesisController` — the only difference is the LLM backend (set via config), not the controller logic. Both are tool-less single-call controllers.
- Chat is handled by the **same** ReAct/NativeThinking controllers — the only difference is prompt content, which is driven by `ExecutionContext` carrying chat-specific data. Chat is a prompt concern (Phase 3.3), not a controller concern.
- `FinalAnalysisController` is a distinct controller from `SynthesisController` because it serves a different purpose (comprehensive final analysis vs. synthesizing parallel results) and will have different prompt building and timeline event types.

### Shared Components

```
pkg/agent/
├── agent.go                    # Agent interface (Phase 3.1)
├── base_agent.go               # BaseAgent with controller delegation (Phase 3.1)
├── context.go                  # ExecutionContext, ServiceBundle (Phase 3.1)
├── constants.go                # MaxAlertDataSize (Phase 3.1)
├── factory.go                  # AgentFactory (Phase 3.1)
├── llm_client.go               # LLMClient interface, chunk types (Phase 3.1)
├── llm_grpc.go                 # GRPCLLMClient (Phase 3.1)
├── tool_executor.go            # NEW: ToolExecutor interface + stub (Phase 3.2)
├── iteration.go                # NEW: shared iteration helpers (Phase 3.2)
└── controller/
    ├── factory.go              # Controller factory (Phase 3.1, updated in 3.2)
    ├── single_call.go          # SingleCallController (Phase 3.1)
    ├── react.go                # NEW: ReActController (Phase 3.2)
    ├── react_parser.go         # NEW: ReAct text parser (Phase 3.2)
    ├── native_thinking.go      # NEW: NativeThinkingController (Phase 3.2)
    ├── synthesis.go            # NEW: SynthesisController (Phase 3.2)
    ├── final_analysis.go       # NEW: FinalAnalysisController (Phase 3.2)
    └── helpers.go              # NEW: shared controller helpers (Phase 3.2)
```

---

## ToolExecutor Interface

Phase 3.2 controllers need to execute tools, but real MCP integration comes in Phase 4. We define a `ToolExecutor` interface that controllers depend on, with a stub implementation for Phase 3.2 testing.

### Interface Definition

```go
// pkg/agent/tool_executor.go

// ToolExecutor abstracts tool/MCP execution for iteration controllers.
// Phase 3.2: stub implementation. Phase 4: real MCP client.
type ToolExecutor interface {
    // Execute runs a single tool call and returns the result.
    // The result is always a string (tool output or error message).
    Execute(ctx context.Context, call ToolCall) (*ToolResult, error)

    // ListTools returns available tool definitions for the current execution.
    // Returns nil if no tools are configured.
    ListTools(ctx context.Context) ([]ToolDefinition, error)
}

// ToolResult represents the output of a tool execution.
type ToolResult struct {
    CallID  string // Matches the ToolCall.ID
    Name    string // Tool name (server.tool format)
    Content string // Tool output (text)
    IsError bool   // Whether the tool returned an error
}
```

### Stub Implementation

```go
// StubToolExecutor returns canned responses for testing.
// Will be replaced by MCP client in Phase 4.
type StubToolExecutor struct {
    tools []ToolDefinition
}

func NewStubToolExecutor(tools []ToolDefinition) *StubToolExecutor {
    return &StubToolExecutor{tools: tools}
}

func (s *StubToolExecutor) Execute(ctx context.Context, call ToolCall) (*ToolResult, error) {
    return &ToolResult{
        CallID:  call.ID,
        Name:    call.Name,
        Content: fmt.Sprintf("[stub] Tool %q called with args: %s", call.Name, call.Arguments),
        IsError: false,
    }, nil
}

func (s *StubToolExecutor) ListTools(ctx context.Context) ([]ToolDefinition, error) {
    return s.tools, nil
}
```

### ExecutionContext Update

```go
// pkg/agent/context.go — add ToolExecutor to ExecutionContext
type ExecutionContext struct {
    // ... existing fields ...
    ToolExecutor ToolExecutor       // Phase 3.2: stub, Phase 4: MCP client
    // MCPClient   MCPClient        // Phase 4 — replaced by ToolExecutor
}
```

**Note:** The `MCPClient` comment in the current `ExecutionContext` will be replaced by `ToolExecutor`. The `ToolExecutor` interface is simpler and more general — it doesn't expose MCP-specific concepts to controllers, maintaining clean separation.

---

## Shared Iteration Helpers

Common patterns extracted from old TARSy controllers, shared across ReAct and Native Thinking controllers.

### Iteration State

```go
// pkg/agent/iteration.go

// IterationState tracks loop state across iterations.
// Shared by ReActController and NativeThinkingController.
type IterationState struct {
    CurrentIteration          int
    MaxIterations             int
    LastInteractionFailed     bool
    LastErrorMessage          string
    ConsecutiveTimeoutFailures int
}

// MaxConsecutiveTimeouts is the threshold for stopping iteration.
// After this many consecutive timeout failures, the controller aborts.
const MaxConsecutiveTimeouts = 2

// ShouldAbortOnTimeouts returns true if consecutive timeout failures
// have reached the threshold.
func (s *IterationState) ShouldAbortOnTimeouts() bool {
    return s.ConsecutiveTimeoutFailures >= MaxConsecutiveTimeouts
}

// RecordSuccess resets failure tracking after a successful interaction.
func (s *IterationState) RecordSuccess() {
    s.LastInteractionFailed = false
    s.LastErrorMessage = ""
    s.ConsecutiveTimeoutFailures = 0
}

// RecordFailure records a failed interaction.
func (s *IterationState) RecordFailure(errMsg string, isTimeout bool) {
    s.LastInteractionFailed = true
    s.LastErrorMessage = errMsg
    if isTimeout {
        s.ConsecutiveTimeoutFailures++
    } else {
        s.ConsecutiveTimeoutFailures = 0
    }
}
```

### Stream Collector

Controllers need to collect streaming chunks into complete responses. This pattern is already used in `SingleCallController` and needs to be reusable.

```go
// pkg/agent/controller/helpers.go

// LLMResponse holds the fully-collected response from a streaming LLM call.
type LLMResponse struct {
    Text           string
    ThinkingText   string
    ToolCalls      []agent.ToolCall
    CodeExecutions []agent.CodeExecutionChunk
    Usage          *agent.TokenUsage
}

// collectStream drains an LLM chunk channel into a complete LLMResponse.
// Returns an error if an ErrorChunk is received.
func collectStream(stream <-chan agent.Chunk) (*LLMResponse, error) {
    resp := &LLMResponse{}
    var textBuf, thinkingBuf strings.Builder

    for chunk := range stream {
        switch c := chunk.(type) {
        case *agent.TextChunk:
            textBuf.WriteString(c.Content)
        case *agent.ThinkingChunk:
            thinkingBuf.WriteString(c.Content)
        case *agent.ToolCallChunk:
            resp.ToolCalls = append(resp.ToolCalls, agent.ToolCall{
                ID:        c.CallID,
                Name:      c.Name,
                Arguments: c.Arguments,
            })
        case *agent.CodeExecutionChunk:
            resp.CodeExecutions = append(resp.CodeExecutions, agent.CodeExecutionChunk{
                Code:   c.Code,
                Result: c.Result,
            })
        case *agent.UsageChunk:
            resp.Usage = &agent.TokenUsage{
                InputTokens:    c.InputTokens,
                OutputTokens:   c.OutputTokens,
                TotalTokens:    c.TotalTokens,
                ThinkingTokens: c.ThinkingTokens,
            }
        case *agent.ErrorChunk:
            return nil, fmt.Errorf("LLM error: %s (code: %s, retryable: %v)",
                c.Message, c.Code, c.Retryable)
        }
    }

    resp.Text = textBuf.String()
    resp.ThinkingText = thinkingBuf.String()
    return resp, nil
}
```

### LLM Call Wrapper

```go
// callLLM performs a single LLM call with context cancellation support.
// Returns the complete collected response.
func callLLM(
    ctx context.Context,
    llmClient agent.LLMClient,
    input *agent.GenerateInput,
) (*LLMResponse, error) {
    // Derive a cancellable context so the producer goroutine in Generate
    // is always cleaned up when we return.
    llmCtx, llmCancel := context.WithCancel(ctx)
    defer llmCancel()

    stream, err := llmClient.Generate(llmCtx, input)
    if err != nil {
        return nil, fmt.Errorf("LLM Generate failed: %w", err)
    }

    return collectStream(stream)
}
```

### Token Usage Accumulator

```go
// accumulateUsage adds token counts from an LLM response to the running total.
func accumulateUsage(total *agent.TokenUsage, resp *LLMResponse) {
    if resp.Usage != nil {
        total.InputTokens += resp.Usage.InputTokens
        total.OutputTokens += resp.Usage.OutputTokens
        total.TotalTokens += resp.Usage.TotalTokens
        total.ThinkingTokens += resp.Usage.ThinkingTokens
    }
}
```

### LLM Interaction Recording

```go
// recordLLMInteraction creates an LLMInteraction debug record in the database.
func recordLLMInteraction(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    iteration int,
    interactionType string,
    messagesCount int,
    resp *LLMResponse,
    lastMessageID *string,
    startTime time.Time,
) error {
    durationMs := int(time.Since(startTime).Milliseconds())

    var thinkingPtr *string
    if resp.ThinkingText != "" {
        thinkingPtr = &resp.ThinkingText
    }

    var inputTokens, outputTokens, totalTokens *int
    if resp.Usage != nil {
        inputTokens = &resp.Usage.InputTokens
        outputTokens = &resp.Usage.OutputTokens
        totalTokens = &resp.Usage.TotalTokens
    }

    _, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
        SessionID:       execCtx.SessionID,
        StageID:         execCtx.StageID,
        ExecutionID:     execCtx.ExecutionID,
        InteractionType: interactionType,
        ModelName:       execCtx.Config.LLMProvider.Model,
        LastMessageID:   lastMessageID,
        LLMRequest:      map[string]any{"messages_count": messagesCount, "iteration": iteration},
        LLMResponse:     map[string]any{"text_length": len(resp.Text), "tool_calls_count": len(resp.ToolCalls)},
        ThinkingContent: thinkingPtr,
        InputTokens:     inputTokens,
        OutputTokens:    outputTokens,
        TotalTokens:     totalTokens,
        DurationMs:      &durationMs,
    })
    return err
}
```

---

## ReAct Controller

The ReAct controller implements the standard Reason + Act loop with text-based tool calling. This is the primary investigation strategy and supports all LLM providers via LangChain.

### Design

```go
// pkg/agent/controller/react.go

type ReActController struct{}

func NewReActController() *ReActController {
    return &ReActController{}
}
```

### Iteration Loop

The core loop follows old TARSy's `ReactController.execute_analysis_loop()` with improvements:

```
┌─────────────────────────────────────────────────┐
│           ReActController.Run()                  │
│                                                  │
│  1. Build initial messages                       │
│  2. Store system + user messages in DB           │
│  3. Get available tools from ToolExecutor        │
│                                                  │
│  for iteration := 0; iteration < max; iteration++│
│  │                                               │
│  │  4. Check consecutive timeout threshold       │
│  │  5. Call LLM (text generation, no tools bound)│
│  │  6. Record LLM interaction                    │
│  │  7. Parse response with ReActParser           │
│  │                                               │
│  │  ┌── Final Answer? ──────────────────────┐    │
│  │  │  Return completed result              │    │
│  │  ├── Valid tool call? ───────────────────┤    │
│  │  │  Execute tool via ToolExecutor        │    │
│  │  │  Append observation to conversation   │    │
│  │  │  Create tool_call + tool_result events│    │
│  │  ├── Unknown tool? ─────────────────────┤    │
│  │  │  Append error observation with list   │    │
│  │  │  of available tools                   │    │
│  │  ├── Malformed response? ───────────────┤    │
│  │  │  Keep malformed message in context    │    │
│  │  │  Append format error feedback         │    │
│  │  └──────────────────────────────────────┘    │
│  │                                               │
│  end loop                                        │
│                                                  │
│  8. Max iterations reached → forced conclusion   │
└─────────────────────────────────────────────────┘
```

### Run Method (Pseudocode)

```go
func (c *ReActController) Run(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) (*agent.ExecutionResult, error) {
    maxIter := execCtx.Config.MaxIterations
    totalUsage := agent.TokenUsage{}
    state := &agent.IterationState{MaxIterations: maxIter}

    // 1. Build initial conversation
    messages := c.buildMessages(execCtx, prevStageContext)

    // 2. Store initial messages in DB
    storeMessages(ctx, execCtx, messages)

    // 3. Get available tools
    tools, _ := execCtx.ToolExecutor.ListTools(ctx)

    // 4. Build tool name set for validation
    toolNames := buildToolNameSet(tools)

    // Main iteration loop
    for iteration := 0; iteration < maxIter; iteration++ {
        state.CurrentIteration = iteration + 1

        // Check consecutive timeout threshold
        if state.ShouldAbortOnTimeouts() {
            return failedResult(state), nil
        }

        startTime := time.Now()

        // Call LLM (text only — tools described in system prompt, not bound)
        resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
            SessionID:   execCtx.SessionID,
            ExecutionID: execCtx.ExecutionID,
            Messages:    messages,
            Config:      execCtx.Config.LLMProvider,
            Tools:       nil, // ReAct uses text-based tool calling
        })

        if err != nil {
            // Handle LLM call failure
            state.RecordFailure(err.Error(), isTimeoutError(err))
            messages = appendObservation(messages, formatErrorObservation(err))
            continue
        }

        // Record LLM interaction
        accumulateUsage(&totalUsage, resp)
        // Store assistant message
        assistantMsg := storeAssistantMessage(ctx, execCtx, messages, resp)
        recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

        // Append assistant response to conversation
        messages = append(messages, agent.ConversationMessage{
            Role:    "assistant",
            Content: resp.Text,
        })

        // Parse ReAct response
        parsed := ParseReActResponse(resp.Text)
        state.RecordSuccess()

        switch {
        case parsed.IsFinalAnswer:
            // Complete — create final_analysis timeline event
            createFinalAnalysisEvent(ctx, execCtx, parsed.FinalAnswer)
            return &agent.ExecutionResult{
                Status:        agent.ExecutionStatusCompleted,
                FinalAnalysis: parsed.FinalAnswer,
                TokensUsed:    totalUsage,
            }, nil

        case parsed.HasAction && !parsed.IsUnknownTool:
            // Valid tool call — execute and append observation
            createToolCallEvent(ctx, execCtx, parsed.Action, parsed.ActionInput)

            result, toolErr := execCtx.ToolExecutor.Execute(ctx, agent.ToolCall{
                ID:        generateCallID(),
                Name:      parsed.Action,
                Arguments: parsed.ActionInput,
            })

            if toolErr != nil {
                state.RecordFailure(toolErr.Error(), isTimeoutError(toolErr))
                observation := formatToolErrorObservation(toolErr)
                createToolResultEvent(ctx, execCtx, observation, true)
                messages = appendObservation(messages, observation)
            } else {
                state.RecordSuccess()
                createToolResultEvent(ctx, execCtx, result.Content, result.IsError)
                messages = appendObservation(messages, formatObservation(result))
            }

        case parsed.IsUnknownTool:
            // Unknown tool — tell LLM what tools are available
            observation := formatUnknownToolError(parsed.Action, parsed.ErrorMessage, tools)
            messages = appendObservation(messages, observation)

        default:
            // Malformed response — keep it, add format feedback
            feedback := getFormatErrorFeedback(parsed)
            messages = appendObservation(messages, feedback)
        }
    }

    // Max iterations reached — force conclusion
    return c.forceConclusion(ctx, execCtx, messages, &totalUsage, state)
}
```

### Conversation Shape (ReAct)

ReAct uses a flat message list with user-role observations (not tool-role messages):

```
[system]  You are an SRE agent. Use the following format: Thought/Action/Observation...
[user]    Alert data + context
[assistant] Thought: I need to check... Action: kubectl.get_pods Action Input: {...}
[user]    Observation: {pod data...}
[assistant] Thought: Based on the pods... Final Answer: The root cause is...
```

**Important**: ReAct does NOT use the `tool` message role. Tool calls and results are embedded in text (assistant/user messages). The LLM is called without `Tools` in `GenerateInput` — tool instructions are in the system prompt.

### Forced Conclusion

When max iterations are reached without a final answer:

```go
func (c *ReActController) forceConclusion(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    messages []agent.ConversationMessage,
    totalUsage *agent.TokenUsage,
    state *agent.IterationState,
) (*agent.ExecutionResult, error) {
    // If the last interaction failed, return failed status
    if state.LastInteractionFailed {
        return &agent.ExecutionResult{
            Status:     agent.ExecutionStatusFailed,
            Error:      fmt.Errorf("max iterations (%d) reached with last interaction failed: %s",
                state.MaxIterations, state.LastErrorMessage),
            TokensUsed: *totalUsage,
        }, nil
    }

    // Otherwise, append forced conclusion prompt and make one more LLM call
    messages = append(messages, agent.ConversationMessage{
        Role:    "user",
        Content: buildForcedConclusionPrompt(state.CurrentIteration),
    })

    resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
        SessionID:   execCtx.SessionID,
        ExecutionID: execCtx.ExecutionID,
        Messages:    messages,
        Config:      execCtx.Config.LLMProvider,
        Tools:       nil,
    })
    if err != nil {
        return &agent.ExecutionResult{
            Status:     agent.ExecutionStatusFailed,
            Error:      fmt.Errorf("forced conclusion LLM call failed: %w", err),
            TokensUsed: *totalUsage,
        }, nil
    }

    accumulateUsage(totalUsage, resp)
    // Extract final answer from forced conclusion (may or may not have ReAct format)
    finalAnswer := extractForcedConclusionAnswer(resp.Text)
    createFinalAnalysisEvent(ctx, execCtx, finalAnswer)

    return &agent.ExecutionResult{
        Status:        agent.ExecutionStatusCompleted,
        FinalAnalysis: finalAnswer,
        TokensUsed:    *totalUsage,
    }, nil
}
```

**Key change from old TARSy:** No `SessionPaused` exception. New TARSy always either completes with a forced conclusion or fails. Session pause/resume was dropped from the architecture (see Questions doc).

---

## ReAct Parser

Go implementation of old TARSy's `react_parser.py`. Parses LLM text output into structured actions.

### Parsed Response

```go
// pkg/agent/controller/react_parser.go

// ParsedReActResponse is the result of parsing an LLM response in ReAct format.
type ParsedReActResponse struct {
    // Thinking/reasoning text (everything before Action or Final Answer)
    Thought string

    // Action fields (populated when the LLM wants to call a tool)
    HasAction   bool
    Action      string // Tool name (e.g., "kubectl.get_pods")
    ActionInput string // Tool arguments (JSON string)

    // Final answer (populated when the LLM wants to conclude)
    IsFinalAnswer bool
    FinalAnswer   string

    // Error tracking
    IsUnknownTool bool   // Tool name not in available tools
    IsMalformed   bool   // Response doesn't match expected format
    ErrorMessage  string // Specific error description for LLM feedback
}
```

### Parser Logic

The parser follows old TARSy's multi-tier detection with improvements:

```go
// ParseReActResponse parses LLM text output into a structured ReAct response.
// The parser is intentionally forgiving — it tries multiple detection strategies
// before declaring a response malformed.
func ParseReActResponse(text string) *ParsedReActResponse {
    // Strategy order (most specific → most lenient):
    // 1. Section-based detection: Look for "Final Answer:", "Action:", "Action Input:" markers
    // 2. Pattern recovery: Handle common LLM format deviations
    // 3. Malformed fallback: Return with specific error feedback

    // ... implementation details ...
}
```

**Section Detection:**
1. **Final Answer**: Look for `Final Answer:` marker. Extract everything after it.
2. **Action + Action Input**: Look for `Action:` and `Action Input:` markers. Extract tool name and arguments.
3. **Thought**: Everything before `Action:` or `Final Answer:` is the thought.

**Action Input Parsing** (multi-format, matching old TARSy):
1. Try JSON parsing first (`{...}`)
2. Try YAML parsing (`key: value` lines)
3. Try key-value parsing (`key=value` lines)
4. Fall back to raw string

**Tool Name Validation:**
The parser itself does NOT validate tool names against available tools. The controller does this after parsing, setting `IsUnknownTool` if the parsed action doesn't match any available tool.

### Format Error Feedback

When the response is malformed, the parser generates specific feedback to help the LLM correct its format:

```go
// GetFormatErrorFeedback returns a specific error message describing
// what's wrong with the response format. This is appended as an
// observation to help the LLM self-correct.
func GetFormatErrorFeedback(parsed *ParsedReActResponse) string {
    // Examples:
    // "Your response is missing the 'Action:' field. Use the format: Thought: ... Action: tool_name Action Input: {...}"
    // "Your response has an Action but is missing 'Action Input:'. Provide the tool arguments."
}

// GetFormatCorrectionReminder returns a general format reminder
// used after exceptions during response processing.
func GetFormatCorrectionReminder() string {
    // Returns the expected ReAct format as a reminder
}
```

### Observation Formatting

```go
// FormatObservation formats a tool execution result as a ReAct observation.
func FormatObservation(result *agent.ToolResult) string {
    if result.IsError {
        return fmt.Sprintf("Observation: Error executing %s: %s", result.Name, result.Content)
    }
    return fmt.Sprintf("Observation: %s", result.Content)
}

// FormatUnknownToolError formats an error when the LLM requests an unknown tool.
// Includes the list of available tools so the LLM can self-correct.
func FormatUnknownToolError(toolName string, errorMsg string, availableTools []agent.ToolDefinition) string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Observation: Error - Unknown tool '%s'. ", toolName))
    sb.WriteString("Available tools:\n")
    for _, tool := range availableTools {
        sb.WriteString(fmt.Sprintf("  - %s: %s\n", tool.Name, tool.Description))
    }
    return sb.String()
}
```

---

## Native Thinking Controller

The Native Thinking controller uses Gemini's native function calling and reasoning capabilities. Instead of text-based ReAct parsing, tool calls arrive as structured data in the LLM response.

### Design

```go
// pkg/agent/controller/native_thinking.go

type NativeThinkingController struct{}

func NewNativeThinkingController() *NativeThinkingController {
    return &NativeThinkingController{}
}
```

### Key Differences from ReAct

| Aspect | ReAct | Native Thinking |
|--------|-------|-----------------|
| Tool calling | Text-parsed (Action/Action Input) | Structured (ToolCallChunk) |
| Tool definitions | In system prompt text | Bound via `GenerateInput.Tools` |
| LLM Backend | `langchain` (any provider) | `google-native` (Gemini only) |
| Response parsing | ReActParser | No parsing needed |
| Thinking content | Implicit in Thought: text | Explicit ThinkingChunk |
| Thought signatures | N/A | Managed by Python (in-memory) |
| Message format | Flat text messages | Structured tool_calls/tool role |
| Completion signal | `Final Answer:` text | Response without ToolCallChunks |

### Iteration Loop

```
┌─────────────────────────────────────────────────┐
│       NativeThinkingController.Run()             │
│                                                  │
│  1. Build initial messages                       │
│  2. Store system + user messages in DB           │
│  3. Get tools from ToolExecutor → ToolDefinitions│
│                                                  │
│  for iteration := 0; iteration < max; iteration++│
│  │                                               │
│  │  4. Check consecutive timeout threshold       │
│  │  5. Call LLM (with tools bound)               │
│  │  6. Record LLM interaction                    │
│  │  7. Collect thinking content (ThinkingChunks) │
│  │                                               │
│  │  ┌── Has ToolCallChunks? ───────────────┐     │
│  │  │  Yes:                                │     │
│  │  │  Store assistant msg with tool_calls │     │
│  │  │  Execute each tool via ToolExecutor  │     │
│  │  │  Store tool result messages           │     │
│  │  │  Create timeline events              │     │
│  │  │  Continue loop                       │     │
│  │  ├─────────────────────────────────────┤     │
│  │  │  No (text response only):           │     │
│  │  │  This is the final answer           │     │
│  │  │  Return completed result            │     │
│  │  └─────────────────────────────────────┘     │
│  │                                               │
│  end loop                                        │
│                                                  │
│  8. Max iterations reached → forced conclusion   │
└─────────────────────────────────────────────────┘
```

### Conversation Shape (Native Thinking)

Native Thinking uses structured tool messages:

```
[system]     You are an SRE agent. Investigate the alert...
[user]       Alert data + context
[assistant]  {text: "Let me check...", tool_calls: [{id: "1", name: "kubectl.get_pods", args: "{}"}]}
[tool]       {tool_call_id: "1", tool_name: "kubectl.get_pods", content: "{pod data}"}
[assistant]  {text: "Based on the investigation, the root cause is..."}
```

### Run Method (Pseudocode)

```go
func (c *NativeThinkingController) Run(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) (*agent.ExecutionResult, error) {
    maxIter := execCtx.Config.MaxIterations
    totalUsage := agent.TokenUsage{}
    state := &agent.IterationState{MaxIterations: maxIter}

    // 1. Build initial conversation
    messages := c.buildMessages(execCtx, prevStageContext)
    storeMessages(ctx, execCtx, messages)

    // 2. Get tools as ToolDefinitions for LLM binding
    tools, _ := execCtx.ToolExecutor.ListTools(ctx)

    for iteration := 0; iteration < maxIter; iteration++ {
        state.CurrentIteration = iteration + 1

        if state.ShouldAbortOnTimeouts() {
            return failedResult(state), nil
        }

        startTime := time.Now()

        // Call LLM with tools bound
        resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
            SessionID:   execCtx.SessionID,
            ExecutionID: execCtx.ExecutionID,
            Messages:    messages,
            Config:      execCtx.Config.LLMProvider,
            Tools:       tools, // Tools bound for native function calling
        })

        if err != nil {
            state.RecordFailure(err.Error(), isTimeoutError(err))
            // For native thinking, on LLM error we can't easily append an observation
            // because the conversation uses structured messages.
            // We append a user message with the error context instead.
            messages = append(messages, agent.ConversationMessage{
                Role:    "user",
                Content: fmt.Sprintf("Error from previous attempt: %s. Please try again.", err.Error()),
            })
            continue
        }

        accumulateUsage(&totalUsage, resp)
        state.RecordSuccess()

        // Store thinking content as timeline event (if present)
        if resp.ThinkingText != "" {
            createThinkingEvent(ctx, execCtx, resp.ThinkingText)
        }

        // Check for tool calls
        if len(resp.ToolCalls) > 0 {
            // Store assistant message with tool calls
            assistantMsg := storeAssistantMessageWithToolCalls(ctx, execCtx, messages, resp)
            recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

            messages = append(messages, agent.ConversationMessage{
                Role:      "assistant",
                Content:   resp.Text,
                ToolCalls: resp.ToolCalls,
            })

            // Execute each tool call
            for _, tc := range resp.ToolCalls {
                createToolCallEvent(ctx, execCtx, tc.Name, tc.Arguments)

                result, toolErr := execCtx.ToolExecutor.Execute(ctx, tc)

                var toolContent string
                var isError bool
                if toolErr != nil {
                    state.RecordFailure(toolErr.Error(), isTimeoutError(toolErr))
                    toolContent = fmt.Sprintf("Error: %s", toolErr.Error())
                    isError = true
                } else {
                    toolContent = result.Content
                    isError = result.IsError
                }

                createToolResultEvent(ctx, execCtx, toolContent, isError)

                // Store tool result message
                storeToolResultMessage(ctx, execCtx, tc.ID, tc.Name, toolContent)
                messages = append(messages, agent.ConversationMessage{
                    Role:       "tool",
                    Content:    toolContent,
                    ToolCallID: tc.ID,
                    ToolName:   tc.Name,
                })
            }
        } else {
            // No tool calls — this is the final answer
            assistantMsg := storeAssistantMessage(ctx, execCtx, messages, resp)
            recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

            createFinalAnalysisEvent(ctx, execCtx, resp.Text)

            return &agent.ExecutionResult{
                Status:        agent.ExecutionStatusCompleted,
                FinalAnalysis: resp.Text,
                TokensUsed:    totalUsage,
            }, nil
        }
    }

    // Max iterations reached — force conclusion
    return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state)
}
```

### Forced Conclusion (Native Thinking)

```go
func (c *NativeThinkingController) forceConclusion(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    messages []agent.ConversationMessage,
    tools []agent.ToolDefinition,
    totalUsage *agent.TokenUsage,
    state *agent.IterationState,
) (*agent.ExecutionResult, error) {
    if state.LastInteractionFailed {
        return &agent.ExecutionResult{
            Status:     agent.ExecutionStatusFailed,
            Error:      fmt.Errorf("max iterations (%d) reached with last interaction failed: %s",
                state.MaxIterations, state.LastErrorMessage),
            TokensUsed: *totalUsage,
        }, nil
    }

    // Make one more call WITHOUT tools to force a text conclusion
    messages = append(messages, agent.ConversationMessage{
        Role:    "user",
        Content: buildForcedConclusionPrompt(state.CurrentIteration),
    })

    resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
        SessionID:   execCtx.SessionID,
        ExecutionID: execCtx.ExecutionID,
        Messages:    messages,
        Config:      execCtx.Config.LLMProvider,
        Tools:       nil, // No tools — force text conclusion
    })
    if err != nil {
        return &agent.ExecutionResult{
            Status:     agent.ExecutionStatusFailed,
            Error:      fmt.Errorf("forced conclusion LLM call failed: %w", err),
            TokensUsed: *totalUsage,
        }, nil
    }

    accumulateUsage(totalUsage, resp)
    createFinalAnalysisEvent(ctx, execCtx, resp.Text)

    return &agent.ExecutionResult{
        Status:        agent.ExecutionStatusCompleted,
        FinalAnalysis: resp.Text,
        TokensUsed:    *totalUsage,
    }, nil
}
```

**Key pattern**: Forced conclusion for Native Thinking calls the LLM **without tools** (`Tools: nil`). This forces the LLM to produce a text response rather than requesting more tool calls.

---

## Synthesis Controller

The Synthesis controller is a tool-less single LLM call used to synthesize results from parallel stage executions. It works with both `synthesis` (LangChain) and `synthesis-native-thinking` (Google Native) strategies — the only difference is the LLM backend configured in `LLMProviderConfig`.

### Design

```go
// pkg/agent/controller/synthesis.go

type SynthesisController struct{}

func NewSynthesisController() *SynthesisController {
    return &SynthesisController{}
}
```

### Run Method

```go
func (c *SynthesisController) Run(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) (*agent.ExecutionResult, error) {
    startTime := time.Now()

    // 1. Build synthesis conversation
    // prevStageContext contains the formatted output from all parallel agents
    messages := c.buildMessages(execCtx, prevStageContext)

    // 2. Store messages
    storeMessages(ctx, execCtx, messages)

    // 3. Create timeline event for streaming
    event := createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis)

    // 4. Single LLM call (no tools)
    resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
        SessionID:   execCtx.SessionID,
        ExecutionID: execCtx.ExecutionID,
        Messages:    messages,
        Config:      execCtx.Config.LLMProvider,
        Tools:       nil, // Synthesis never uses tools
    })
    if err != nil {
        return nil, fmt.Errorf("synthesis LLM call failed: %w", err)
    }

    // 5. Complete timeline event
    completeTimelineEvent(ctx, execCtx, event.ID, resp.Text)

    // 6. Store thinking content if present (synthesis-native-thinking)
    if resp.ThinkingText != "" {
        createThinkingEvent(ctx, execCtx, resp.ThinkingText)
    }

    // 7. Store assistant message + LLM interaction
    assistantMsg := storeAssistantMessage(ctx, execCtx, messages, resp)
    recordLLMInteraction(ctx, execCtx, 1, "synthesis", len(messages), resp, &assistantMsg.ID, startTime)

    // 8. Return result
    return &agent.ExecutionResult{
        Status:        agent.ExecutionStatusCompleted,
        FinalAnalysis: resp.Text,
        TokensUsed:    tokenUsageFromResp(resp),
    }, nil
}
```

### Synthesis Conversation Shape

```
[system]  You are synthesizing results from multiple parallel investigations...
[user]    <!-- STAGE_CONTEXT_START -->
          ### Agent 1 Analysis
          {agent 1 output}
          ### Agent 2 Analysis
          {agent 2 output}
          <!-- STAGE_CONTEXT_END -->

          Synthesize the above into a comprehensive analysis.
[assistant] Based on the parallel investigations...
```

**Why one controller for two strategies?** Both `synthesis` and `synthesis-native-thinking` have identical logic: single LLM call, no tools, no iteration. The only difference is the LLM backend (LangChain vs Google Native), which is already determined by the `LLMProviderConfig.Backend` field in the resolved config. Creating two controllers with identical logic would violate DRY.

---

## Final Analysis Controller

The Final Analysis controller produces a comprehensive final analysis from accumulated investigation data. Unlike Synthesis (which synthesizes parallel results), Final Analysis operates at the end of a chain to produce the final output.

### Design

```go
// pkg/agent/controller/final_analysis.go

type FinalAnalysisController struct{}

func NewFinalAnalysisController() *FinalAnalysisController {
    return &FinalAnalysisController{}
}
```

### Run Method

```go
func (c *FinalAnalysisController) Run(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) (*agent.ExecutionResult, error) {
    startTime := time.Now()

    // 1. Build final analysis conversation
    // Different from synthesis: focuses on comprehensive analysis, not parallel result merging
    messages := c.buildMessages(execCtx, prevStageContext)

    // 2. Store messages
    storeMessages(ctx, execCtx, messages)

    // 3. Create timeline event
    event := createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis)

    // 4. Single LLM call (no tools)
    resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
        SessionID:   execCtx.SessionID,
        ExecutionID: execCtx.ExecutionID,
        Messages:    messages,
        Config:      execCtx.Config.LLMProvider,
        Tools:       nil,
    })
    if err != nil {
        return nil, fmt.Errorf("final analysis LLM call failed: %w", err)
    }

    // 5. Complete timeline event
    completeTimelineEvent(ctx, execCtx, event.ID, resp.Text)

    // 6. Store thinking content if present
    if resp.ThinkingText != "" {
        createThinkingEvent(ctx, execCtx, resp.ThinkingText)
    }

    // 7. Store assistant message + LLM interaction
    assistantMsg := storeAssistantMessage(ctx, execCtx, messages, resp)
    recordLLMInteraction(ctx, execCtx, 1, "final_analysis", len(messages), resp, &assistantMsg.ID, startTime)

    // 8. Return result
    return &agent.ExecutionResult{
        Status:        agent.ExecutionStatusCompleted,
        FinalAnalysis: resp.Text,
        TokensUsed:    tokenUsageFromResp(resp),
    }, nil
}
```

### How Final Analysis is Triggered

Final Analysis is not mapped to an `IterationStrategy` enum value. Instead, it is selected by the controller factory based on the chain configuration:

```go
// In controller/factory.go — updated for Phase 3.2
func (f *Factory) CreateController(
    strategy config.IterationStrategy,
    execCtx *agent.ExecutionContext,
) (agent.Controller, error) {
    switch strategy {
    case "":
        return NewSingleCallController(), nil
    case config.IterationStrategyReact:
        return NewReActController(), nil
    case config.IterationStrategyNativeThinking:
        return NewNativeThinkingController(), nil
    case config.IterationStrategySynthesis:
        return NewSynthesisController(), nil
    case config.IterationStrategySynthesisNativeThinking:
        return NewSynthesisController(), nil // Same controller, different LLM backend
    default:
        return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
    }
}
```

**Final Analysis as a strategy**: Since `FinalAnalysisController` needs to be selectable from chain configuration, we have two options (see Questions doc Q2):
1. Add a `final-analysis` iteration strategy enum value
2. Use `synthesis` with a chain-level flag

---

## Chat Handling

### Design Philosophy

In old TARSy, chat was handled by separate controller classes (`ChatReActController`, `ChatNativeThinkingController`). These only differed from their non-chat counterparts in `build_initial_conversation()` — they included investigation context and user questions in the prompt.

**New TARSy simplifies this**: Chat is a prompt concern, not a controller concern. The same `ReActController` and `NativeThinkingController` are used for both investigation and chat. The difference is in:

1. **ExecutionContext** — carries chat-specific data (user question, investigation context)
2. **Prompt building** — Phase 3.3 will provide chat-aware prompt builders
3. **Controller logic** — identical regardless of chat mode

### ExecutionContext Extension for Chat

```go
// pkg/agent/context.go — extend ExecutionContext

type ExecutionContext struct {
    // ... existing fields ...

    // Chat context (nil for non-chat sessions)
    ChatContext *ChatContext
}

// ChatContext carries chat-specific data for controllers.
type ChatContext struct {
    UserQuestion         string   // The user's chat question
    InvestigationContext  string   // Previous investigation summary
    ChatHistory          []agent.ConversationMessage // Previous chat messages
}
```

### How Chat Affects Controllers

Controllers check `execCtx.ChatContext != nil` only during message building (Phase 3.2 placeholder, Phase 3.3 full implementation):

```go
func (c *ReActController) buildMessages(
    execCtx *agent.ExecutionContext,
    prevStageContext string,
) []agent.ConversationMessage {
    // Phase 3.2: minimal prompt building
    // Phase 3.3: full prompt builder with chat-aware templates

    if execCtx.ChatContext != nil {
        // Include investigation context + user question in prompt
        // Specific prompt template handled by Phase 3.3 prompt builder
    }
    // ... standard prompt building ...
}
```

This design means:
- No `ChatReActController` or `ChatNativeThinkingController` needed
- No additional iteration strategies needed
- No duplication of iteration loop logic
- Chat becomes a pure prompt composition concern

---

## Controller Factory Update

```go
// pkg/agent/controller/factory.go — Phase 3.2 update

func (f *Factory) CreateController(
    strategy config.IterationStrategy,
    execCtx *agent.ExecutionContext,
) (agent.Controller, error) {
    switch strategy {
    case "":
        // Empty string defaults to single-call controller (Phase 3.1)
        return NewSingleCallController(), nil

    case config.IterationStrategyReact:
        return NewReActController(), nil

    case config.IterationStrategyNativeThinking:
        return NewNativeThinkingController(), nil

    case config.IterationStrategySynthesis:
        return NewSynthesisController(), nil

    case config.IterationStrategySynthesisNativeThinking:
        // Same SynthesisController — backend differs via LLMProviderConfig
        return NewSynthesisController(), nil

    default:
        return nil, fmt.Errorf("unknown iteration strategy: %q", strategy)
    }
}
```

---

## Timeline Events

Controllers create timeline events for real-time frontend updates. Each event type maps to specific controller actions:

### Event Types by Controller

| Event Type | ReAct | Native Thinking | Synthesis | Final Analysis |
|---|---|---|---|---|
| `llm_response` | ✅ (each iteration) | ✅ (each iteration) | — | — |
| `llm_thinking` | — | ✅ (if thinking content) | ✅ (if thinking content) | ✅ (if thinking content) |
| `tool_call` | ✅ (parsed action) | ✅ (structured call) | — | — |
| `tool_result` | ✅ (observation) | ✅ (tool output) | — | — |
| `final_analysis` | ✅ (completion) | ✅ (completion) | ✅ | ✅ |
| `error` | ✅ (on failure) | ✅ (on failure) | ✅ (on failure) | ✅ (on failure) |

### Event Creation Pattern

```go
// Shared helper used by all controllers
func createTimelineEvent(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    eventType timelineevent.EventType,
    content string,
    metadata map[string]any,
) (*ent.TimelineEvent, error) {
    return execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
        SessionID:      execCtx.SessionID,
        StageID:        execCtx.StageID,
        ExecutionID:    execCtx.ExecutionID,
        SequenceNumber: nextSequenceNumber(execCtx),
        EventType:      eventType,
        Content:        content,
        Metadata:       metadata,
    })
}
```

---

## Message Storage

### Storage Pattern by Controller

**ReAct**: Uses simple text messages (no structured tool calls):
```go
// Assistant messages: plain text with ReAct format
{Role: "assistant", Content: "Thought: ...\nAction: ...\nAction Input: ..."}
// Observations: user-role messages
{Role: "user", Content: "Observation: {tool output}"}
```

**Native Thinking**: Uses structured tool call messages:
```go
// Assistant messages: with tool_calls JSON
{Role: "assistant", Content: "Let me check...", ToolCalls: [...]}
// Tool result messages: with tool_call_id and tool_name
{Role: "tool", Content: "{output}", ToolCallID: "call_123", ToolName: "kubectl.get_pods"}
```

**Synthesis / Final Analysis**: Simple system + user + assistant:
```go
{Role: "system", Content: "You are synthesizing..."}
{Role: "user", Content: "{prev stage context}"}
{Role: "assistant", Content: "{synthesis result}"}
```

---

## Error Handling

### Error Categories

| Error Type | Source | Handling |
|---|---|---|
| LLM call failure | gRPC/provider error | Record failure, append error context, continue loop |
| Tool execution failure | MCP timeout/error | Record as tool_result error event, continue loop |
| Malformed ReAct response | Bad LLM output | Keep in context, add format feedback, continue loop |
| Consecutive timeouts | 2+ timeouts in a row | Abort with failed status |
| Context deadline exceeded | Session timeout | Propagated up by BaseAgent as `ExecutionStatusTimedOut` |
| Context cancelled | User cancellation | Propagated up by BaseAgent as `ExecutionStatusCancelled` |
| Infrastructure failure | DB write error | Return `(nil, error)` — infrastructure failure |

### Timeout Detection

```go
// isTimeoutError checks if an error is timeout-related.
// Used for consecutive timeout tracking.
func isTimeoutError(err error) bool {
    if errors.Is(err, context.DeadlineExceeded) {
        return true
    }
    // Also check for timeout strings in wrapped errors
    errStr := strings.ToLower(err.Error())
    return strings.Contains(errStr, "timeout") || strings.Contains(errStr, "timed out")
}
```

---

## Backend Selection

Per Phase 3.1 Q1 decision, Go determines which Python backend to use:

| Strategy | Backend | Reason |
|---|---|---|
| `react` | `langchain` | Multi-provider support, text-based tool calling |
| `native-thinking` | `google-native` | Gemini-specific features (thinking, function calling) |
| `synthesis` | `langchain` | Multi-provider support |
| `synthesis-native-thinking` | `google-native` | Gemini thinking for synthesis |

This is already handled by the `ResolvedAgentConfig.LLMProvider.Backend` field, set during configuration resolution. Controllers don't need to know about backends.

---

## Testing Strategy

### Unit Tests

Each controller should have comprehensive unit tests using mock `LLMClient` and `ToolExecutor`:

1. **ReAct Controller**:
   - Happy path: LLM → tool call → observation → final answer
   - Multiple iterations before final answer
   - Unknown tool handling
   - Malformed response handling and recovery
   - Consecutive timeout abort
   - Forced conclusion at max iterations
   - Context cancellation during iteration

2. **Native Thinking Controller**:
   - Happy path: LLM → tool calls → tool results → final answer
   - Multiple tool calls in single response
   - Thinking content recording
   - Forced conclusion (no tools)
   - Error recovery in tool execution

3. **Synthesis Controller**:
   - Single call with prev stage context
   - Thinking content recording (synthesis-native-thinking)
   - Error propagation

4. **Final Analysis Controller**:
   - Single call with accumulated context
   - Thinking content recording

5. **ReAct Parser**:
   - Standard format parsing
   - Final Answer detection
   - JSON/YAML/key-value action input parsing
   - Malformed response detection
   - Missing sections detection
   - Edge cases (empty response, partial format)

### Mock Interfaces

```go
// MockLLMClient for testing
type MockLLMClient struct {
    responses [][]agent.Chunk // One per call
    callIndex int
}

func (m *MockLLMClient) Generate(ctx context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
    if m.callIndex >= len(m.responses) {
        return nil, fmt.Errorf("unexpected LLM call #%d", m.callIndex+1)
    }
    ch := make(chan agent.Chunk, len(m.responses[m.callIndex]))
    for _, chunk := range m.responses[m.callIndex] {
        ch <- chunk
    }
    close(ch)
    m.callIndex++
    return ch, nil
}

// MockToolExecutor for testing
type MockToolExecutor struct {
    results map[string]*agent.ToolResult // Keyed by tool name
}

func (m *MockToolExecutor) Execute(ctx context.Context, call agent.ToolCall) (*agent.ToolResult, error) {
    if result, ok := m.results[call.Name]; ok {
        return result, nil
    }
    return nil, fmt.Errorf("unexpected tool call: %s", call.Name)
}
```

---

## Implementation Checklist

### Phase 3.2 Implementation Order

1. **Shared infrastructure** (build first, used by all controllers):
   - [ ] `pkg/agent/tool_executor.go` — ToolExecutor interface + StubToolExecutor
   - [ ] `pkg/agent/iteration.go` — IterationState, shared constants
   - [ ] `pkg/agent/controller/helpers.go` — collectStream, callLLM, recordLLMInteraction, accumulateUsage
   - [ ] Update `pkg/agent/context.go` — add ToolExecutor, ChatContext

2. **ReAct Parser** (needed before ReAct controller):
   - [ ] `pkg/agent/controller/react_parser.go` — ParseReActResponse, format helpers
   - [ ] `pkg/agent/controller/react_parser_test.go` — comprehensive parser tests

3. **ReAct Controller**:
   - [ ] `pkg/agent/controller/react.go` — full implementation
   - [ ] `pkg/agent/controller/react_test.go` — unit tests with mocks

4. **Native Thinking Controller**:
   - [ ] `pkg/agent/controller/native_thinking.go` — full implementation
   - [ ] `pkg/agent/controller/native_thinking_test.go` — unit tests with mocks

5. **Synthesis Controller**:
   - [ ] `pkg/agent/controller/synthesis.go` — full implementation
   - [ ] `pkg/agent/controller/synthesis_test.go` — unit tests

6. **Final Analysis Controller**:
   - [ ] `pkg/agent/controller/final_analysis.go` — full implementation
   - [ ] `pkg/agent/controller/final_analysis_test.go` — unit tests

7. **Factory + Integration**:
   - [ ] Update `pkg/agent/controller/factory.go` — register all new controllers
   - [ ] Update `pkg/queue/executor.go` — wire ToolExecutor into ExecutionContext
   - [ ] Integration tests with mock LLM + mock tools

---

## Design Decisions

### What Changed from Old TARSy

| Aspect | Old TARSy (Python) | New TARSy (Go) | Reason |
|---|---|---|---|
| Session pause/resume | `SessionPaused` exception at max iterations | Always force conclusion or fail | Simplifies architecture; pause/resume was rarely useful |
| Chat controllers | Separate `ChatReActController`, `ChatNativeThinkingController` | Same controllers, chat handled via `ExecutionContext` + prompts | Eliminates code duplication; chat is a prompt concern |
| Synthesis variants | Separate `SynthesisController`, `SynthesisNativeThinkingController` | Single `SynthesisController` (backend differs via config) | Both are identical single-call logic; backend is config |
| Per-iteration timeout | `asyncio.wait_for()` wrapping each iteration | Go `context.Context` with per-iteration deadline | Go-native approach to timeouts |
| Thought signatures | Python in-memory dict, passed per-call | Python in-memory (Phase 3.1 decision), transparent to Go | Same architecture, Go doesn't need to know |
| Controller dependencies | Constructor-injected LLMManager, PromptBuilder | Stateless controllers, dependencies via ExecutionContext | Simpler, more testable |
| ReAct parser | Python class with regex | Go functions (no struct state) | Functional style fits Go idioms |

### What Stayed the Same

- ReAct text-based parsing (Go owns parsing, not Python)
- Native Thinking structured tool calls (Gemini function calling)
- Max iterations with forced conclusion
- Consecutive timeout detection and abort
- Malformed response error feedback (specific, not generic)
- Unknown tool error listing available tools
- Progressive DB writes during iteration
- LLMInteraction recording for each call
- Timeline events for real-time updates

---

## References

- Old TARSy Iteration Controllers: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/agents/iteration_controllers/`
- Old TARSy ReAct Parser: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/agents/parsers/react_parser.py`
- Old TARSy Base Agent: `/home/igels/Projects/AI/tarsy-bot/backend/tarsy/agents/base_agent.py`
- Phase 3.1 Design: `docs/phase3-base-agent-architecture-design.md`
- Phase 3.1 Questions: `docs/phase3-base-agent-architecture-questions.md`
- Phase 2 Database Design: `docs/phase2-database-persistence-design.md`
- Phase 2 Configuration Design: `docs/phase2-configuration-system-design.md`
- Current Go Agent Code: `pkg/agent/`, `pkg/agent/controller/`
- Proto Definition: `proto/llm_service.proto`
