# Phase 4.3: MCP Features — Detailed Design

**Status**: ✅ Design Complete (all questions resolved)
**Last Updated**: 2026-02-09

## Overview

This document details the three remaining MCP features for the new TARSy implementation. Phase 4.3 completes the MCP integration by adding per-alert MCP configuration overrides, LLM-based tool result summarization, and real-time tool output streaming.

**Phase 4.3 Scope**: Per-alert MCP selection override, LLM-based tool result summarization, MCP tool call streaming events.

**What This Phase Delivers:**
- Per-alert MCP selection override — alerts specify custom MCP servers and tool filters, overriding chain defaults
- Tool result summarization — LLM-based summarization of large MCP tool outputs, configurable per server
- MCP tool call streaming — real-time events for tool call start/complete and summarization streaming
- Token estimation utility for summarization threshold checks
- `"summarization"` LLMInteraction type for observability
- `mcp_tool_summary` timeline events for summarized content

**What This Phase Does NOT Deliver:**
- Frontend/dashboard changes (Phase 7 — Dashboard)
- Executive summary generation (Phase 5 — Session Completion)
- Multi-LLM provider support (Phase 8 — Integrations)
- New MCP transport types

**Dependencies:**
- Phase 4.1 complete (MCP client infrastructure, ToolExecutor, ClientFactory)
- Phase 4.2 complete (Data masking in ToolExecutor pipeline)
- Phase 3 complete (streaming infrastructure, controllers, PromptBuilder)

---

## Feature 1: Per-Alert MCP Selection Override

### Context

Alerts can optionally specify which MCP servers and tools to use, overriding the chain/agent defaults. This enables:
- Restricting investigation to specific servers for targeted alerts
- Filtering to specific tools when the alert context is known
- Overriding native tool settings (Google Search, code execution, URL context)

### Current State

Infrastructure is already in place but not wired:

| Component | Status |
|-----------|--------|
| `MCPSelectionConfig` model (`pkg/models/mcp_selection.go`) | ✅ Exists |
| `AlertSession.mcp_selection` DB field (JSON) | ✅ Exists |
| `SubmitAlertInput.MCP` field | ✅ Exists |
| `AlertService.SubmitAlert()` stores to DB | ✅ Exists |
| `ToolExecutor.toolFilter` field | ✅ Exists |
| `ToolExecutor.resolveToolCall()` checks filter | ✅ Exists |
| `ClientFactory.CreateToolExecutor()` accepts `toolFilter` param | ✅ Exists |
| Session executor reads `session.McpSelection` | ❌ Not implemented |
| Session executor passes override to `CreateToolExecutor` | ❌ Not implemented |
| API validation of MCP override servers | ❌ Not implemented |

### Design

#### Override Semantics: Replace, Not Merge

When an alert provides `mcp_selection`, it **replaces** the chain/agent's MCP server list entirely. This matches old TARSy behavior and is the correct semantic:

- If the alert says "use only kubernetes-server", we should not also connect to argocd-server from the chain config
- The override is the authoritative, complete server set for this alert
- Tool filtering within a server is additive restriction (empty list = all tools)

#### Data Flow

```
POST /api/v1/alerts
  body: { "data": "...", "mcp": { "servers": [...], "native_tools": {...} } }
    │
    ▼
AlertService.SubmitAlert()
  → Stores mcp_selection as JSON in AlertSession
    │
    ▼
Worker claims session → RealSessionExecutor.Execute()
  → Reads session.McpSelection (JSON)
  → Deserializes to MCPSelectionConfig
  → Resolves: override present? Use override servers. No override? Use chain config servers.
  → Converts MCPSelectionConfig → (serverIDs []string, toolFilter map[string][]string)
  → Validates all server IDs exist in MCPServerRegistry
  → ClientFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
    │
    ▼
ToolExecutor
  → serverIDs = override servers (or chain servers)
  → toolFilter = per-server tool restrictions (or nil)
  → ListTools() respects filter
  → Execute() validates against filter
```

#### Changes to Session Executor (`pkg/queue/executor.go`)

The session executor currently always passes `nil` for `toolFilter`:

```go
// Current (Phase 4.1):
mcpExecutor, mcpClient, mcpErr := e.mcpFactory.CreateToolExecutor(ctx, resolvedConfig.MCPServers, nil)
```

Phase 4.3 adds MCP selection resolution:

```go
// Phase 4.3:
func (e *RealSessionExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
    // ... (existing code through resolvedConfig) ...

    // 6. Resolve MCP servers and tool filter (per-alert override or chain config)
    serverIDs, toolFilter, err := e.resolveMCPSelection(session, resolvedConfig)
    if err != nil {
        logger.Error("Failed to resolve MCP selection", "error", err)
        return &ExecutionResult{
            Status: alertsession.StatusFailed,
            Error:  fmt.Errorf("invalid MCP selection: %w", err),
        }
    }

    // 7. Create MCP tool executor
    var toolExecutor agent.ToolExecutor
    var failedServers map[string]string
    if e.mcpFactory != nil && len(serverIDs) > 0 {
        mcpExecutor, mcpClient, mcpErr := e.mcpFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
        // ... (existing fallback logic) ...
    }
    // ...
}
```

#### New Helper: `resolveMCPSelection`

```go
// resolveMCPSelection determines the MCP servers and tool filter for this session.
// If the session has an MCP override, it replaces the chain config entirely.
// Returns (serverIDs, toolFilter, error).
func (e *RealSessionExecutor) resolveMCPSelection(
    session *ent.AlertSession,
    resolvedConfig *agent.ResolvedAgentConfig,
) ([]string, map[string][]string, error) {
    // No override — use chain config (existing behavior)
    if session.McpSelection == nil || len(session.McpSelection) == 0 {
        return resolvedConfig.MCPServers, nil, nil
    }

    // Deserialize override
    override, err := models.ParseMCPSelectionConfig(session.McpSelection)
    if err != nil {
        return nil, nil, fmt.Errorf("failed to parse MCP selection: %w", err)
    }

    // Build serverIDs and toolFilter from override
    serverIDs := make([]string, 0, len(override.Servers))
    toolFilter := make(map[string][]string)

    for _, sel := range override.Servers {
        // Validate server exists in registry
        if !e.cfg.MCPServerRegistry.Has(sel.Name) {
            return nil, nil, fmt.Errorf("MCP server %q from override not found in configuration", sel.Name)
        }
        serverIDs = append(serverIDs, sel.Name)

        // Only add to toolFilter if specific tools are requested
        if len(sel.Tools) > 0 {
            toolFilter[sel.Name] = sel.Tools
        }
    }

    // Return nil toolFilter if no server has tool restrictions
    if len(toolFilter) == 0 {
        toolFilter = nil
    }

    return serverIDs, toolFilter, nil
}
```

#### New: `ParseMCPSelectionConfig` (`pkg/models/mcp_selection.go`)

```go
// ParseMCPSelectionConfig deserializes a JSON map (from ent storage) into MCPSelectionConfig.
func ParseMCPSelectionConfig(raw map[string]interface{}) (*MCPSelectionConfig, error) {
    data, err := json.Marshal(raw)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal MCP selection: %w", err)
    }
    var config MCPSelectionConfig
    if err := json.Unmarshal(data, &config); err != nil {
        return nil, fmt.Errorf("failed to unmarshal MCP selection: %w", err)
    }
    if len(config.Servers) == 0 {
        return nil, fmt.Errorf("MCP selection must have at least one server")
    }
    return &config, nil
}
```

#### Native Tools Override

The `MCPSelectionConfig.NativeTools` field controls Gemini native tools (Google Search, code execution, URL context). This override is passed through to the LLM provider config.

```go
// In resolveMCPSelection or executor, after resolving override:
if override.NativeTools != nil {
    // Apply native tools override to the resolved LLM provider config
    resolvedConfig.NativeToolsOverride = override.NativeTools
}
```

The `ResolvedAgentConfig` gets a new optional field:

```go
type ResolvedAgentConfig struct {
    // ... existing fields ...
    NativeToolsOverride *models.NativeToolsConfig // Per-alert native tools override (nil = use provider defaults)
}
```

This is applied when building `GenerateInput` in the controllers — the NativeThinking controller already reads native tools from the provider config and can merge the override.

#### Validation at API Layer

The API handler (`POST /api/v1/alerts`) should validate MCP selection early:

```go
// In alert handler, before calling AlertService.SubmitAlert():
if input.MCP != nil {
    for _, sel := range input.MCP.Servers {
        if !mcpServerRegistry.Has(sel.Name) {
            return echo.NewHTTPError(http.StatusBadRequest,
                fmt.Sprintf("MCP server %q not found in configuration", sel.Name))
        }
    }
}
```

This gives immediate feedback to callers instead of failing at execution time.

---

## Feature 2: Tool Result Summarization

### Context

MCP tool results can be very large (e.g., listing all pods in a namespace returns thousands of lines). Large results waste LLM context window and degrade investigation quality. Summarization uses an LLM call to distill large results into concise, investigation-relevant summaries.

### Current State

| Component | Status |
|-----------|--------|
| `SummarizationConfig` (`pkg/config/types.go`) | ✅ Exists |
| Per-server `MCPServerConfig.Summarization` | ✅ Exists |
| `PromptBuilder.BuildMCPSummarizationSystemPrompt()` | ✅ Exists |
| `PromptBuilder.BuildMCPSummarizationUserPrompt()` | ✅ Exists |
| `mcpSummarizationSystemTemplate` (prompt text) | ✅ Exists |
| `mcpSummarizationUserTemplate` (prompt text) | ✅ Exists |
| `ToolExecutor.Execute()` Phase 4.3 stub comment | ✅ Exists |
| `LLMInteraction.interaction_type` "summarization" | ❌ Not in schema |
| `TimelineEvent.event_type` "mcp_tool_summary" | ✅ In schema |
| Token estimation utility | ❌ Not implemented |
| Summarization logic | ❌ Not implemented |

### Architecture Decision: Controller-Level Summarization

**Key insight**: Summarization is an LLM orchestration concern, not an MCP infrastructure concern.

The `ToolExecutor` intentionally lacks:
- `LLMClient` — cannot make LLM calls
- Conversation context — cannot build meaningful summarization prompts
- `ExecutionContext` — no session IDs, no service access for DB records
- `EventPublisher` — cannot stream summarization to frontend

Old TARSy mixed summarization into `MCPClient.call_tool()`, giving the MCP client access to the LLM client and event publishing. This was a design smell that coupled MCP infrastructure to LLM orchestration.

**New TARSy approach**: Summarization happens in the controller, after `ToolExecutor.Execute()` returns. The controller already has everything needed:

| Need | Controller Has |
|------|----------------|
| LLM client | `execCtx.LLMClient` |
| Conversation context | `messages[]` accumulated during iteration |
| Event publisher | `execCtx.EventPublisher` |
| Services (timeline, interaction) | `execCtx.Services` |
| Prompt builder | `execCtx.PromptBuilder` |
| MCP server config | Via `MCPServerRegistry` (accessible through `PromptBuilder`) |

This is cleaner because:
1. `ToolExecutor` stays focused on MCP execution (single responsibility)
2. Controller orchestrates the multi-step flow (LLM call, timeline events, streaming)
3. Conversation context is naturally available (no threading needed)
4. Same pattern as existing LLM streaming (consistent architecture)

**Consequence**: The Phase 4.3 stub in `executor.go` is removed. The summarization step moves to the controller's tool execution loop.

### Summarization Flow

```
Controller iteration loop:
  │
  ├─ 1. LLM returns tool call → create llm_tool_call timeline event
  │
  ├─ 2. Create mcp_tool_call timeline event (persistent, status: streaming)
  │
  ├─ 3. ToolExecutor.Execute(toolCall) → raw ToolResult (masked by Phase 4.2)
  │
  ├─ 4. Complete mcp_tool_call event (status: completed)
  │
  ├─ 5. Create tool_result timeline event (storage-truncated raw result)
  │
  ├─ 6. Check summarization:
  │     a. Look up SummarizationConfig for this server
  │     b. If disabled or not configured → use raw result, skip to step 8
  │     c. Estimate token count of raw result
  │     d. If below threshold → use raw result, skip to step 8
  │
  ├─ 7. Summarize (threshold exceeded):
  │     a. Publish mcp_tool_call.summarizing event (transient) → frontend shows indicator
  │     b. Safety-net truncate raw result for summarization LLM input
  │     c. Build summarization prompts (system + user with full conversation context)
  │     d. Create mcp_tool_summary timeline event (status: streaming)
  │     e. Call LLM with streaming → publish stream.chunk for each delta
  │     f. Complete mcp_tool_summary timeline event with full summary text
  │     g. Record LLM interaction (type: "summarization")
  │     h. Result for conversation = summary (not raw)
  │
  ├─ 8. Use result in conversation:
  │     - ReAct: append as "Observation: {result}" user message
  │     - NativeThinking: append as tool result message (role=tool)
  │
  └─ 9. Continue iteration loop
```

### Token Estimation (`pkg/mcp/tokens.go`)

Go doesn't have tiktoken. For threshold checking, approximate estimation is sufficient — we're comparing against a configurable threshold (default 5000 tokens), not computing exact costs.

```go
package mcp

// EstimateTokens provides a rough token count for threshold comparison.
// Uses the common heuristic of ~4 characters per token for English text.
// This is intentionally approximate — exact counts would require a
// tokenizer library and add a dependency for minimal benefit (the
// threshold is a configurable soft limit, not a hard boundary).
const charsPerToken = 4

// EstimateTokens returns an approximate token count for the given text.
func EstimateTokens(text string) int {
    if len(text) == 0 {
        return 0
    }
    return (len(text) + charsPerToken - 1) / charsPerToken // Round up
}
```

### Summarization Helper (`pkg/agent/controller/summarize.go`)

New file in the controller package — shared by both ReAct and NativeThinking controllers:

```go
package controller

// SummarizationResult holds the outcome of a summarization attempt.
type SummarizationResult struct {
    Content      string          // Summary text (or original if not summarized)
    WasSummarized bool           // Whether summarization was performed
    Usage        *agent.TokenUsage // Token usage from summarization LLM call (nil if not summarized)
}

// maybeSummarize checks if a tool result needs summarization and performs it if so.
// Returns the (possibly summarized) content and metadata about the summarization.
//
// Parameters:
//   - ctx: parent context (iteration timeout applies)
//   - execCtx: execution context with all dependencies
//   - serverID: MCP server that produced the result
//   - toolName: tool that was called
//   - rawContent: the raw tool result (already masked)
//   - conversationContext: formatted conversation so far (for summarization prompt)
//   - eventSeq: timeline event sequence counter
func maybeSummarize(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    serverID, toolName string,
    rawContent string,
    conversationContext string,
    eventSeq *int,
) (*SummarizationResult, error) {
    // 1. Look up summarization config for this server
    registry := execCtx.PromptBuilder.MCPServerRegistry()
    serverConfig, err := registry.Get(serverID)
    if err != nil || serverConfig.Summarization == nil || !serverConfig.Summarization.Enabled {
        return &SummarizationResult{Content: rawContent}, nil
    }

    sumConfig := serverConfig.Summarization

    // 2. Estimate token count
    estimatedTokens := mcp.EstimateTokens(rawContent)
    threshold := sumConfig.SizeThresholdTokens
    if threshold <= 0 {
        threshold = 5000 // Default threshold
    }

    if estimatedTokens <= threshold {
        return &SummarizationResult{Content: rawContent}, nil
    }

    // 3. Summarization needed
    slog.Info("Tool result exceeds summarization threshold",
        "server", serverID, "tool", toolName,
        "estimated_tokens", estimatedTokens, "threshold", threshold)

    maxSummaryTokens := sumConfig.SummaryMaxTokenLimit
    if maxSummaryTokens <= 0 {
        maxSummaryTokens = 1000 // Default max summary tokens
    }

    // 4. Build summarization prompts
    systemPrompt := execCtx.PromptBuilder.BuildMCPSummarizationSystemPrompt(serverID, toolName, maxSummaryTokens)
    userPrompt := execCtx.PromptBuilder.BuildMCPSummarizationUserPrompt(conversationContext, serverID, toolName, rawContent)

    // 5. Perform summarization LLM call with streaming
    summary, usage, err := callSummarizationLLM(ctx, execCtx, systemPrompt, userPrompt, serverID, toolName, eventSeq)
    if err != nil {
        slog.Warn("Summarization LLM call failed, using raw result",
            "server", serverID, "tool", toolName, "error", err)
        return &SummarizationResult{Content: rawContent}, nil // Fail-open: use raw result
    }

    // 6. Wrap summary with context note
    wrappedSummary := fmt.Sprintf(
        "[NOTE: The output from %s.%s was %d tokens (estimated) and has been summarized to preserve context window. "+
            "The full output is available in the tool_result timeline event above.]\n\n%s",
        serverID, toolName, estimatedTokens, summary)

    return &SummarizationResult{
        Content:       wrappedSummary,
        WasSummarized: true,
        Usage:         usage,
    }, nil
}
```

### Summarization LLM Call

```go
// callSummarizationLLM performs the summarization LLM call with streaming.
// Creates an mcp_tool_summary timeline event and streams chunks to WebSocket clients.
// Records an LLMInteraction with type "summarization".
func callSummarizationLLM(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    systemPrompt, userPrompt string,
    serverID, toolName string,
    eventSeq *int,
) (string, *agent.TokenUsage, error) {
    startTime := time.Now()

    messages := []agent.ConversationMessage{
        {Role: agent.RoleSystem, Content: systemPrompt},
        {Role: agent.RoleUser, Content: userPrompt},
    }

    input := &agent.GenerateInput{
        SessionID:   execCtx.SessionID,
        ExecutionID: execCtx.ExecutionID,
        Messages:    messages,
        Config:      execCtx.Config.LLMProvider,
        Tools:       nil, // No tools for summarization
    }

    // Use dedicated summarization streaming (creates mcp_tool_summary events, not llm_response)
    streamed, err := callSummarizationLLMWithStreaming(ctx, execCtx, input, serverID, toolName, eventSeq)
    if err != nil {
        return "", nil, fmt.Errorf("summarization LLM call failed: %w", err)
    }

    summary := strings.TrimSpace(streamed.Text)
    if summary == "" {
        return "", nil, fmt.Errorf("summarization produced empty result")
    }

    // Record LLM interaction for observability
    recordLLMInteraction(ctx, execCtx, 0, "summarization", len(messages),
        streamed.LLMResponse, nil, startTime)

    return summary, streamed.Usage, nil
}

### Summarization Timeline Events

The existing `callLLMWithStreaming` creates `llm_response` type events. For summarization, we need `mcp_tool_summary` events. Two approaches:

**Approach: Dedicated summarization streaming function**

Rather than modifying `callLLMWithStreaming` (which is well-tested for iteration LLM calls), create a parallel function `callSummarizationLLMWithStreaming` that uses `mcp_tool_summary` as the event type. This avoids adding conditional logic to the existing streaming path.

```go
// callSummarizationLLMWithStreaming is analogous to callLLMWithStreaming but
// creates mcp_tool_summary timeline events instead of llm_response events.
// The streaming pattern is identical (create event → stream chunks → finalize).
func callSummarizationLLMWithStreaming(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    input *agent.GenerateInput,
    serverID, toolName string,
    eventSeq *int,
) (*StreamedResponse, error) {
    // Same pattern as callLLMWithStreaming, but:
    // - Creates timelineevent.EventTypeMcpToolSummary instead of EventTypeLlmResponse
    // - Metadata includes server_name and tool_name
    // - No thinking event (summarization has no thinking stream)
    // ...
}
```

### Conversation Context for Summarization

The summarization prompt needs conversation context so the LLM knows what the investigator was looking for. The controller builds this from its accumulated messages:

```go
// buildConversationContext formats the current conversation for summarization context.
// Includes assistant thoughts and observations (not system prompt) to give the
// summarizer investigation context.
func buildConversationContext(messages []agent.ConversationMessage) string {
    var sb strings.Builder
    for _, msg := range messages {
        if msg.Role == agent.RoleSystem {
            continue // Skip system prompt (too long, not needed for context)
        }
        sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", msg.Role, msg.Content))
    }
    return sb.String()
}
```

### Tool Result Truncation (Two Concerns)

Large tool results require truncation at two independent points:

**1. Storage truncation** (UI/DB protection) — Always applied to raw results stored in `tool_result` timeline events and MCPInteraction records. Uses a lower threshold to protect the dashboard from rendering massive text. Applied regardless of whether summarization is triggered.

**2. Summarization input safety net** — When feeding the summarization LLM, truncate to a larger limit (model's context window minus prompt overhead). The summarizer should get as much data as possible for quality, but bounded as a safety net against extremely large results.

No separate conversation truncation for non-summarized results. If a result is below the summarization threshold, it's already small enough. If summarization is disabled, that's a deliberate choice. Summarization *is* the mechanism for controlling result size in the conversation.

```go
// truncateForStorage truncates tool output for timeline events and MCPInteraction records.
// This protects the UI from rendering massive text blobs. Applied to ALL raw results.
func truncateForStorage(content string, maxTokens int) string {
    if maxTokens <= 0 {
        return content
    }
    maxChars := maxTokens * charsPerToken
    if len(content) <= maxChars {
        return content
    }
    return content[:maxChars] + "\n\n[TRUNCATED: Output exceeded storage display limit]"
}

// truncateForSummarization truncates tool output before sending to the summarization LLM.
// Safety net — summarization prompt + truncated output must fit in the model's context window.
// Uses a larger limit than storage truncation to give the summarizer maximum data.
func truncateForSummarization(content string, maxTokens int) string {
    if maxTokens <= 0 {
        return content
    }
    maxChars := maxTokens * charsPerToken
    if len(content) <= maxChars {
        return content
    }
    return content[:maxChars] + "\n\n[TRUNCATED: Output exceeded summarization input limit]"
}
```

```
Raw MCP result (masked)
  ├─ truncateForStorage() → timeline event + MCPInteraction (lower limit, UI-safe)
  ├─ If summarization triggered:
  │     ├─ truncateForSummarization() → summarization LLM input (larger limit)
  │     └─ Summary → agent conversation
  └─ If NOT summarized:
        └─ Full result → agent conversation (small enough by definition)
```

### Controller Integration

Both ReAct and NativeThinking controllers need the same summarization logic. The changes are in their tool execution sections:

#### ReAct Controller Changes (`pkg/agent/controller/react.go`)

```go
// In the tool execution section of the iteration loop:

// Create mcp_tool_call timeline event (persistent, status: streaming)
mcpToolCallEvent, _ := createMCPToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, &eventSeq)

// Execute tool
result, err := execCtx.ToolExecutor.Execute(iterCtx, toolCall)
// ... (existing error handling) ...

// Complete mcp_tool_call event
completeMCPToolCallEvent(ctx, execCtx, mcpToolCallEvent, result)

// Create tool_result timeline event (storage-truncated raw result)
storageTruncated := truncateForStorage(result.Content, storageMaxTokens)
createToolResultEvent(ctx, execCtx, storageTruncated, result.IsError, &eventSeq)

// Check summarization (only for non-error results)
observationContent := result.Content
if !result.IsError {
    convContext := buildConversationContext(messages)
    sumResult, sumErr := maybeSummarize(iterCtx, execCtx, serverID, toolName,
        result.Content, convContext, &eventSeq)
    if sumErr == nil && sumResult.WasSummarized {
        observationContent = sumResult.Content
        accumulateUsageFromSummarization(&totalUsage, sumResult.Usage)
    }
}

// Append observation to conversation (uses summarized content if applicable)
observation := fmt.Sprintf("Observation: %s", observationContent)
messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
```

#### NativeThinking Controller Changes (`pkg/agent/controller/native_thinking.go`)

```go
// In the tool execution loop:

// Create mcp_tool_call timeline event (persistent, status: streaming)
mcpToolCallEvent, _ := createMCPToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, &eventSeq)

// Execute tool
result, err := execCtx.ToolExecutor.Execute(iterCtx, toolCall)
// ... (existing error handling) ...

// Complete mcp_tool_call event
completeMCPToolCallEvent(ctx, execCtx, mcpToolCallEvent, result)

// Create tool_result timeline event (storage-truncated raw result)
storageTruncated := truncateForStorage(result.Content, storageMaxTokens)
createToolResultEvent(ctx, execCtx, storageTruncated, result.IsError, &eventSeq)

// Check summarization (only for non-error results)
toolResultContent := result.Content
if !result.IsError {
    convContext := buildConversationContext(messages)
    sumResult, sumErr := maybeSummarize(iterCtx, execCtx, serverID, toolName,
        result.Content, convContext, &eventSeq)
    if sumErr == nil && sumResult.WasSummarized {
        toolResultContent = sumResult.Content
        accumulateUsageFromSummarization(&totalUsage, sumResult.Usage)
    }
}

// Append tool result message (uses summarized content if applicable)
messages = append(messages, agent.ConversationMessage{
    Role:       agent.RoleTool,
    Content:    toolResultContent,
    ToolCallID: toolCall.ID,
    ToolName:   toolCall.Name,
})
```

### Schema Changes

#### LLMInteraction: Add "summarization" interaction type

```go
// ent/schema/llminteraction.go
field.Enum("interaction_type").
    Values("iteration", "final_analysis", "executive_summary", "chat_response", "summarization"),
```

This requires a migration to add the new enum value.

### Remove Phase 4.3 Stub from ToolExecutor

The `// TODO (Phase 4.3): content = e.maybeSummarize(...)` comment in `executor.go` is removed since summarization moves to the controller level.

### Summarization Failure Policy: Fail-Open

If the summarization LLM call fails (timeout, LLM error, empty response), the raw result is used instead. This matches the investigation-availability-first philosophy:

- A failed summary is annoying but non-critical
- The raw result is still valid data
- The investigation can continue with a larger context window cost
- The failure is logged for observability

---

## Feature 3: MCP Tool Call Streaming

### Context

Currently, when a controller executes a tool, the frontend has no visibility until the tool result timeline event appears after completion. For long-running MCP calls (10–90 seconds), the UI appears frozen. This feature adds real-time events for tool call lifecycle.

### Current State

| Component | Status |
|-----------|--------|
| `stream.chunk` event type | ✅ Exists (for LLM streaming) |
| `EventPublisher.PublishTransient()` | ✅ Exists |
| `callLLMWithStreaming` pattern | ✅ Exists |
| MCP tool call start event | ❌ Not implemented |
| Summarization streaming | ❌ Not implemented |

### Design

#### MCP Tool Call Lifecycle Events

MCP tool calls are request-response (the MCP SDK does not support streaming tool results). What we can stream is the lifecycle:

```
Frontend Timeline:

1. [llm_tool_call]          ← LLM decided to call a tool (already exists)
2. [mcp_tool_call]          ← Tool execution begins (NEW — persistent timeline event)
3.   ... waiting ...         ← Frontend shows spinner/progress (status: streaming)
4. [mcp_tool_call completed] ← Tool execution finished (status update on same event)
5. [tool_result]             ← Raw result stored (already exists)
6. [mcp_tool_call.summarizing] ← Summarization starts (NEW — transient, only if summarizing)
7.   [stream.chunk] ...      ← Summarization LLM streaming (NEW — transient)
8. [mcp_tool_summary]        ← Summary stored (NEW — persistent)
```

#### New Timeline Event Type: `mcp_tool_call`

The `mcp_tool_call` timeline event is persistent (stored in DB). This gives full durability, reliable correlation with `tool_result`, proper catchup on reconnect, and full UI control for rendering tool call lifecycle. The event starts with status `streaming` (execution in progress) and is completed when the MCP call returns.

Metadata:

```go
metadata := map[string]interface{}{
    "server_name": serverID,
    "tool_name":   toolName,
    "arguments":   arguments,
}
```

This requires adding `"mcp_tool_call"` to the `TimelineEvent.event_type` enum in the schema.

#### New Transient Event Type: `mcp_tool_call.summarizing`

```go
// pkg/events/types.go

// Transient event types (NOTIFY only, no DB persistence).
const (
    // LLM streaming chunks — high-frequency, ephemeral.
    EventTypeStreamChunk = "stream.chunk"

    // MCP tool call summarization indicator — ephemeral.
    EventTypeMCPToolCallSummarizing = "mcp_tool_call.summarizing"
)
```

#### Publishing Tool Call Started (Persistent)

In both controllers, before calling `ToolExecutor.Execute()`:

```go
// Create mcp_tool_call timeline event (persistent, status: streaming)
mcpToolCallEvent, _ := createMCPToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, &eventSeq)
```

Helper function:

```go
// createMCPToolCallEvent creates a persistent timeline event indicating an MCP tool call
// has started execution. The event is created with status "streaming" and completed
// after the tool call returns. The frontend uses this to show a progress indicator.
// Returns the event (for later completion) and any error.
func createMCPToolCallEvent(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    serverID, toolName, arguments string,
    eventSeq *int,
) (*ent.TimelineEvent, error) {
    return createTimelineEvent(ctx, execCtx, timelineevent.EventTypeMcpToolCall, "", map[string]interface{}{
        "server_name": serverID,
        "tool_name":   toolName,
        "arguments":   arguments,
    }, eventSeq)
}
```

After `ToolExecutor.Execute()` returns, the event is completed:

```go
// Complete the mcp_tool_call timeline event
if mcpToolCallEvent != nil {
    execCtx.Services.Timeline.CompleteTimelineEvent(ctx, mcpToolCallEvent.ID, result.Content, nil, nil)
    // Publish completion to WebSocket
    publishTimelineCompleted(ctx, execCtx, mcpToolCallEvent.ID, "completed")
}
```

#### Publishing Summarization Started (Transient)

Before the summarization LLM call:

```go
func publishMCPToolCallSummarizing(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    serverID, toolName string,
    estimatedTokens int,
) {
    if execCtx.EventPublisher == nil {
        return
    }
    channel := events.SessionChannel(execCtx.SessionID)
    _ = execCtx.EventPublisher.PublishTransient(ctx, channel, map[string]interface{}{
        "type":             events.EventTypeMCPToolCallSummarizing,
        "session_id":       execCtx.SessionID,
        "stage_id":         execCtx.StageID,
        "execution_id":     execCtx.ExecutionID,
        "server_name":      serverID,
        "tool_name":        toolName,
        "estimated_tokens": estimatedTokens,
        "timestamp":        time.Now().Format(time.RFC3339Nano),
    })
}
```

#### Summarization LLM Streaming

The summarization LLM call uses the same `stream.chunk` event type as regular LLM streaming, but with an `mcp_tool_summary` timeline event type. The frontend can distinguish summarization chunks by the `event_type` field in the `timeline_event.created` event.

The `callSummarizationLLMWithStreaming` function follows the same pattern as `callLLMWithStreaming`:

1. First text chunk → create `mcp_tool_summary` timeline event (status: `streaming`)
2. Subsequent chunks → publish `stream.chunk` with delta
3. Stream complete → finalize timeline event (status: `completed`)

Metadata on the `mcp_tool_summary` timeline event:

```go
metadata := map[string]interface{}{
    "server_name":        serverID,
    "tool_name":          toolName,
    "original_tokens":    estimatedTokens,
    "summarization_model": execCtx.Config.LLMProvider.Model,
}
```

### Tool Name Extraction for Streaming

Both controllers need to extract `serverID` and `toolName` from the tool call name to pass to streaming helpers. This uses the existing `mcp.SplitToolName()`:

```go
// In controller, when processing a tool call:
normalizedName := mcp.NormalizeToolName(toolCall.Name) // server__tool → server.tool
serverID, toolName, _ := mcp.SplitToolName(normalizedName)
// serverID and toolName used for streaming events and summarization
```

If `SplitToolName` fails (shouldn't happen at this point since ToolExecutor validates), the full name is used as fallback.

---

## Package Layout Changes

```
pkg/
├── mcp/
│   ├── ... (existing files unchanged)
│   └── tokens.go              # EstimateTokens utility (NEW)
├── agent/
│   ├── context.go             # NativeToolsOverride field added to ResolvedAgentConfig
│   └── controller/
│       ├── helpers.go         # createMCPToolCallEvent, publishMCPToolCallSummarizing added
│       ├── summarize.go       # maybeSummarize, callSummarizationLLM (NEW)
│       ├── react.go           # Tool execution + summarization integration
│       └── native_thinking.go # Tool execution + summarization integration
├── models/
│   └── mcp_selection.go       # ParseMCPSelectionConfig added
├── events/
│   └── types.go               # New transient event type (mcp_tool_call.summarizing)
└── queue/
    └── executor.go            # resolveMCPSelection, MCP override wiring
```

---

## Integration Points

### Existing Code Touched

| File | Change |
|------|--------|
| `pkg/queue/executor.go` | Add `resolveMCPSelection()`, wire override to `CreateToolExecutor` |
| `pkg/models/mcp_selection.go` | Add `ParseMCPSelectionConfig()` |
| `pkg/agent/context.go` | Add `NativeToolsOverride` to `ResolvedAgentConfig` |
| `pkg/agent/controller/react.go` | Add summarization check after tool execution, tool call started event |
| `pkg/agent/controller/native_thinking.go` | Same as ReAct |
| `pkg/agent/controller/helpers.go` | Add `createMCPToolCallEvent`, `publishMCPToolCallSummarizing` |
| `pkg/events/types.go` | Add `EventTypeMCPToolCallSummarizing` |
| `pkg/mcp/executor.go` | Remove Phase 4.3 stub comment |
| `ent/schema/llminteraction.go` | Add `"summarization"` to interaction_type enum |
| `ent/schema/timelineevent.go` | Add `"mcp_tool_call"` to event_type enum |

### New Files

| File | Purpose |
|------|---------|
| `pkg/mcp/tokens.go` | `EstimateTokens`, `truncateForStorage`, `truncateForSummarization` |
| `pkg/mcp/tokens_test.go` | Token estimation and truncation tests |
| `pkg/agent/controller/summarize.go` | `maybeSummarize`, `callSummarizationLLMWithStreaming`, `buildConversationContext` |
| `pkg/agent/controller/summarize_test.go` | Summarization tests |
| `pkg/models/mcp_selection_test.go` | `ParseMCPSelectionConfig` tests |

---

## Implementation Order

### Step 1: Per-Alert MCP Selection Override

1. Add `ParseMCPSelectionConfig` to `pkg/models/mcp_selection.go`
2. Add `NativeToolsOverride` field to `ResolvedAgentConfig`
3. Add `resolveMCPSelection` to `pkg/queue/executor.go`
4. Wire override in `Execute()` — pass serverIDs and toolFilter to `CreateToolExecutor`
5. Add API-level validation for MCP override servers
6. Write tests: executor with override, executor without override, invalid servers, tool filtering

### Step 2: Token Estimation

1. Create `pkg/mcp/tokens.go` with `EstimateTokens`
2. Write tests for edge cases (empty string, short text, long text)

### Step 3: Tool Call Streaming Events

1. Add `"mcp_tool_call"` to `TimelineEvent.event_type` enum + generate migration
2. Add `EventTypeMCPToolCallSummarizing` constant to `pkg/events/types.go`
3. Add `createMCPToolCallEvent` and `publishMCPToolCallSummarizing` helpers
4. Integrate into ReAct and NativeThinking controllers (before/after tool execution)
5. Write tests for event creation and publishing

### Step 4: Tool Result Summarization

1. Add `"summarization"` to LLMInteraction schema enum + generate migration
2. Create `pkg/agent/controller/summarize.go` with `maybeSummarize` and `callSummarizationLLMWithStreaming`
3. Integrate into ReAct controller (after tool result, before observation append)
4. Integrate into NativeThinking controller (after tool result, before tool result message)
5. Remove Phase 4.3 stub from `executor.go`
6. Write tests: summarization triggered, not triggered, LLM failure (fail-open), streaming

### Step 5: Integration Testing

1. Test full flow: alert with MCP override → tool execution → summarization → streaming
2. Test MCP override with tool filtering
3. Test summarization with both ReAct and NativeThinking
4. Test fail-open behavior when summarization LLM fails

---

## Testing Strategy

### Unit Tests

| Area | Tests |
|------|-------|
| `ParseMCPSelectionConfig` | Valid input, empty servers, malformed JSON, nil input |
| `EstimateTokens` | Empty, short, long, Unicode |
| `truncateForStorage` / `truncateForSummarization` | Below limit, at limit, above limit, zero limit |
| `resolveMCPSelection` | No override, valid override, invalid server, tool filtering |
| `maybeSummarize` | Below threshold, above threshold, disabled, LLM failure, empty summary |
| `buildConversationContext` | System messages excluded, multi-turn context |
| `createMCPToolCallEvent` | Event creation, completion after tool returns |

### Integration Tests

| Scenario | Validates |
|----------|-----------|
| Alert with MCP override → only specified servers used | Override wiring, tool filtering |
| Tool result > threshold → summarized | Summarization pipeline end-to-end |
| Summarization LLM fails → raw result used | Fail-open behavior |
| Tool execution → started/completed events published | Streaming lifecycle |

---

## Configuration Reference

### Per-Server Summarization

```yaml
mcp_servers:
  kubernetes-server:
    transport:
      type: stdio
      command: npx
      args: ["-y", "kubernetes-mcp-server@0.0.54"]
    summarization:
      enabled: true
      size_threshold_tokens: 5000    # Trigger summarization above this
      summary_max_token_limit: 1000  # Max tokens for the summary itself
```

### Per-Alert MCP Override (API payload)

```json
{
  "alert_type": "kubernetes",
  "data": "Pod CrashLoopBackOff in namespace production...",
  "mcp": {
    "servers": [
      { "name": "kubernetes-server" },
      { "name": "argocd-server", "tools": ["get_application_status"] }
    ],
    "native_tools": {
      "google_search": false,
      "code_execution": true
    }
  }
}
```

---

## Comparison with Old TARSy

| Aspect | Old TARSy | New TARSy | Rationale |
|--------|-----------|-----------|-----------|
| Summarization location | Inside `MCPClient.call_tool()` | Controller level (`summarize.go`) | Separation of concerns — ToolExecutor stays MCP-focused |
| Summarization LLM | Same model as agent | Same model as agent (configurable later) | Simplicity; Phase 8 can add dedicated summarization model |
| Token estimation | tiktoken (Python) | Character-based heuristic (Go) | Zero dependency; accurate enough for threshold checks |
| MCP override semantics | Replace chain servers | Replace chain servers | Same behavior — override is the authoritative server set |
| Tool call streaming | `mcp.tool_call.started` event | `mcp_tool_call` persistent timeline event | Persistent for reliable UI correlation and history replay |
| Summarization streaming | `llm.stream.chunk` with `stream_type: summarization` | `stream.chunk` with `mcp_tool_summary` timeline event | Reuses existing streaming pattern with appropriate event type |
| Raw result storage | Stored before summarization | `tool_result` timeline event (always raw) | Same — raw always preserved for observability |
| Fail-open summarization | Yes | Yes | Same — availability over perfection |
