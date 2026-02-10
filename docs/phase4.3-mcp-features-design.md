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
  ├─ 1. LLM returns tool call
  │     → Extract serverID, toolName via mcp.SplitToolName()
  │     → Create llm_tool_call timeline event (status: streaming, args in metadata)
  │     → Dashboard shows: "Calling server.tool..." with spinner
  │
  ├─ 2. ToolExecutor.Execute(toolCall) → raw ToolResult (masked by Phase 4.2)
  │
  ├─ 3. Complete llm_tool_call event (status: completed)
  │     → content = truncateForStorage(rawResult) — UI/DB-safe
  │     → metadata enriched with {is_error}
  │     → Dashboard shows: tool result
  │
  ├─ 4. Check summarization:
  │     a. Look up SummarizationConfig for this server
  │     b. If disabled or not configured → use raw result, skip to step 6
  │     c. Estimate token count of raw result
  │     d. If below threshold → use raw result, skip to step 6
  │
  ├─ 5. Summarize (threshold exceeded):
  │     a. Safety-net truncate raw result for summarization LLM input
  │     b. Build summarization prompts (system + user with full conversation context)
  │     c. Create mcp_tool_summary timeline event (status: streaming)
  │        → Dashboard sees streaming status → shows "Summarizing..." indicator
  │     d. Call LLM with streaming → publish stream.chunk for each delta
  │     e. Complete mcp_tool_summary timeline event with full summary text
  │     f. Record LLM interaction (type: "summarization")
  │     g. Result for conversation = summary (not raw)
  │
  ├─ 6. Use result in conversation:
  │     - ReAct: append as "Observation: {result}" user message
  │     - NativeThinking: append as tool result message (role=tool)
  │
  └─ 7. Continue iteration loop
```

### Token Estimation (`pkg/mcp/tokens.go`)

Go has tiktoken ports (e.g., `pkoukk/tiktoken-go` with 866+ stars, listed in OpenAI's official cookbook; `tiktoken-go/tokenizer` as a pure Go alternative with embedded vocabularies). However, for threshold checking, approximate estimation is sufficient — we're comparing against a configurable threshold (default 5000 tokens), not computing exact costs.

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
            "The full output is available in the tool call event above.]\n\n%s",
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
```

### Summarization Timeline Events

The existing `callLLMWithStreaming` creates `llm_response` type events. For summarization, we need `mcp_tool_summary` events. Rather than modifying `callLLMWithStreaming` (which is well-tested for iteration LLM calls), we create a dedicated `callSummarizationLLMWithStreaming` that uses `mcp_tool_summary` as the event type. This avoids adding conditional logic to the existing streaming path.

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

**1. Storage truncation** (UI/DB protection) — Always applied to raw results stored in `llm_tool_call` completion content and MCPInteraction records. Uses a lower threshold to protect the dashboard from rendering massive text. Applied regardless of whether summarization is triggered.

**2. Summarization input safety net** — When feeding the summarization LLM, truncate to a larger limit (model's context window minus prompt overhead). The summarizer should get as much data as possible for quality, but bounded as a safety net against extremely large results.

No separate conversation truncation for non-summarized results. If a result is below the summarization threshold, it's already small enough. If summarization is disabled, that's a deliberate choice. Summarization *is* the mechanism for controlling result size in the conversation.

```go
// truncateAtLineBoundary is the shared truncation logic. It cuts at the last newline
// before the limit to avoid splitting mid-line — important when the content is
// indented JSON, YAML, or log output (preserves logical line boundaries).
// Old TARSy used the same rfind('\n') approach.
func truncateAtLineBoundary(content string, maxChars int, marker string) string {
    if maxChars <= 0 || len(content) <= maxChars {
        return content
    }
    truncated := content[:maxChars]
    if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
        truncated = truncated[:idx]
    }
    return truncated + fmt.Sprintf(
        "\n\n[TRUNCATED: %s — Original size: %dKB, limit: %dKB]",
        marker, len(content)/1024, maxChars/1024,
    )
}

// truncateForStorage truncates tool output for llm_tool_call completion content
// and MCPInteraction records. Protects the UI from rendering massive text blobs.
// Applied to ALL raw results, regardless of whether summarization is triggered.
func truncateForStorage(content string, maxTokens int) string {
    return truncateAtLineBoundary(content, maxTokens*charsPerToken,
        "Output exceeded storage display limit")
}

// truncateForSummarization truncates tool output before sending to the summarization LLM.
// Safety net — summarization prompt + truncated output must fit in the model's context window.
// Uses a larger limit than storage truncation to give the summarizer maximum data.
func truncateForSummarization(content string, maxTokens int) string {
    return truncateAtLineBoundary(content, maxTokens*charsPerToken,
        "Output exceeded summarization input limit")
}
```

```
Raw MCP result (masked)
  ├─ truncateForStorage() → llm_tool_call completion + MCPInteraction (lower limit, UI-safe)
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

// Extract server/tool info for events and summarization
normalizedName := mcp.NormalizeToolName(toolCall.Name)
serverID, toolName, _ := mcp.SplitToolName(normalizedName)

// Create llm_tool_call timeline event (status: streaming — dashboard shows spinner)
toolCallEvent, _ := createToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, &eventSeq)

// Execute tool
result, err := execCtx.ToolExecutor.Execute(iterCtx, toolCall)
// ... (existing error handling) ...

// Complete llm_tool_call event with storage-truncated raw result
storageTruncated := truncateForStorage(result.Content, storageMaxTokens)
completeToolCallEvent(ctx, execCtx, toolCallEvent, storageTruncated, result.IsError)

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

// Extract server/tool info for events and summarization
normalizedName := mcp.NormalizeToolName(toolCall.Name)
serverID, toolName, _ := mcp.SplitToolName(normalizedName)

// Create llm_tool_call timeline event (status: streaming — dashboard shows spinner)
toolCallEvent, _ := createToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, &eventSeq)

// Execute tool
result, err := execCtx.ToolExecutor.Execute(iterCtx, toolCall)
// ... (existing error handling) ...

// Complete llm_tool_call event with storage-truncated raw result
storageTruncated := truncateForStorage(result.Content, storageMaxTokens)
completeToolCallEvent(ctx, execCtx, toolCallEvent, storageTruncated, result.IsError)

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

#### Migration Reference

See `pkg/database/migrations/README.md` for the full migration conventions. Key facts:

- **Ent enums are VARCHAR** — Ent stores enum fields as `character varying`, not PostgreSQL ENUM types. Adding or removing enum values does **not** require a database migration. Validation happens at the application level.
- **Workflow for enum-only changes**: Edit `ent/schema/*.go` → `make ent-generate` → `make build && make test`.
- **Workflow for structural changes** (new columns, tables, indexes): Edit `ent/schema/*.go` → `make ent-generate` → `make db-start` → `make migrate-create NAME=describe_change` → review generated SQL → `make build && make test`.

#### LLMInteraction: Add "summarization" interaction type

```go
// ent/schema/llminteraction.go
field.Enum("interaction_type").
    Values("iteration", "final_analysis", "executive_summary", "chat_response", "summarization"),
```

**No database migration required** — enum-only change (VARCHAR column, app-level validation).

#### TimelineEvent: Enum Cleanup

The `event_type` enum already includes `mcp_tool_summary` (added in Phase 3 anticipating Phase 4). No new values needed.

However, Phase 4.3 changes the event model, making two existing enum values obsolete:

| Enum value | Current usage | Phase 4.3 status |
|------------|---------------|------------------|
| `llm_tool_call` | Created once per tool call | **Kept** — now uses streaming lifecycle (created → completed) |
| `tool_result` | Separate event per tool result | **Remove** — result moves to `llm_tool_call` completion content |
| `mcp_tool_call` | Never used (added anticipating Phase 4) | **Remove** — `llm_tool_call` lifecycle replaces this |
| `mcp_tool_summary` | Never used (added anticipating Phase 4) | **Kept** — used for summarization streaming |

Changes to `ent/schema/timelineevent.go`:

1. **Remove `mcp_tool_call`** from the `Values()` list (never used, no data to migrate).
2. **Remove `tool_result`** from the `Values()` list. Phase 4.3 updates the Phase 3 controllers to stop creating these. Since there is no production database, there are no existing rows to migrate.
3. **Update the comment block** for `llm_tool_call` to document the streaming lifecycle pattern:

```go
//   llm_tool_call      — Tool call lifecycle event. Created with status "streaming" when the
//                        LLM requests a tool call (metadata: server_name, tool_name, arguments).
//                        Completed with the storage-truncated raw result in content and
//                        is_error in metadata after ToolExecutor.Execute() returns.
//                        Replaces the separate tool_result event from Phase 3.
```

**No database migration required** — all enum-only changes.

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

Currently, when a controller executes a tool, the frontend has no visibility until the tool result timeline event appears after completion. For long-running MCP calls (10–90 seconds), the UI appears frozen. This feature adds real-time lifecycle tracking to tool calls.

### Current State

| Component | Status |
|-----------|--------|
| `llm_tool_call` timeline event type | ✅ Exists (created once, no lifecycle) |
| `tool_result` timeline event type | ✅ Exists (separate event for result) |
| `stream.chunk` event type | ✅ Exists (for LLM streaming) |
| `EventPublisher.PublishTransient()` | ✅ Exists |
| `callLLMWithStreaming` pattern | ✅ Exists |
| Tool call lifecycle (start → complete) | ❌ Not implemented |
| Summarization streaming | ❌ Not implemented |

### Design: Single-Event Tool Call Lifecycle

**Key insight**: From the dashboard's perspective, a tool call is **one thing** with a lifecycle (started → completed). Having separate `llm_tool_call`, `mcp_tool_call`, and `tool_result` events forces the dashboard to correlate three events per tool call — complex, fragile, and prone to race conditions on reconnect.

**Solution**: Reuse the existing `llm_tool_call` event with a streaming lifecycle pattern (same as `llm_response`). One event per tool call, two states:

```
Frontend Timeline:

1. [llm_tool_call] created (status: streaming)
     → metadata: {server_name, tool_name, arguments}
     → Dashboard shows: "Calling server.tool..." with spinner

2. [llm_tool_call] completed (status: completed)
     → content: storage-truncated raw result
     → metadata enriched: {is_error}
     → Dashboard shows: tool result

3. (if summarization triggered):
     [mcp_tool_summary] created (status: streaming)  ← Dashboard shows "Summarizing..."
     [stream.chunk] ...                               ← Summarization LLM token deltas
     [mcp_tool_summary] completed (status: completed) ← Summary stored
```

**This eliminates `tool_result` as a separate event type.** The raw result lives on the completed `llm_tool_call` event. No correlation needed — the dashboard receives created/completed events for the same event ID.

On catchup: one event in DB, status tells you the state. If status is `streaming`, tool is still executing. If `completed`, result is in content.

#### Changes to `llm_tool_call` Event Format

**Before (Phase 3):**
- Created once with content=arguments, metadata={tool_name}
- Separate `tool_result` event created later with content=result

**After (Phase 4.3):**
- Created with status=`streaming`, content="" (empty, like streaming LLM events), metadata={tool_name, server_name, arguments}
- Completed with content=storage-truncated result, metadata enriched with {is_error}

Arguments move from `content` to `metadata` so they survive the content update on completion and remain accessible to the dashboard at all times.

#### Changes to `createToolCallEvent` Helper

```go
// createToolCallEvent creates a streaming llm_tool_call timeline event.
// The event starts with status "streaming" and is completed after tool execution
// via completeToolCallEvent. Arguments are in metadata (not content) so they
// survive the content update on completion.
func createToolCallEvent(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    serverID, toolName string,
    arguments string,
    eventSeq *int,
) (*ent.TimelineEvent, error) {
    return createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmToolCall, "", map[string]interface{}{
        "server_name": serverID,
        "tool_name":   toolName,
        "arguments":   arguments,
    }, eventSeq)
}
```

**Note**: `createTimelineEvent` already publishes `timeline_event.created` with status `streaming` (content is empty). This matches the existing LLM streaming pattern.

#### New: `completeToolCallEvent` Helper

```go
// completeToolCallEvent completes an llm_tool_call timeline event with the tool result.
// Called after ToolExecutor.Execute() returns. The content is the storage-truncated
// raw result. Metadata is enriched with is_error.
func completeToolCallEvent(
    ctx context.Context,
    execCtx *agent.ExecutionContext,
    event *ent.TimelineEvent,
    content string,
    isError bool,
) {
    if event == nil {
        return
    }
    metadata := map[string]interface{}{"is_error": isError}
    execCtx.Services.Timeline.CompleteTimelineEvent(ctx, event.ID, content, metadata, nil)

    // Publish completion to WebSocket
    if execCtx.EventPublisher != nil {
        channel := events.SessionChannel(execCtx.SessionID)
        execCtx.EventPublisher.Publish(ctx, execCtx.SessionID, channel, map[string]interface{}{
            "type":      events.EventTypeTimelineCompleted,
            "event_id":  event.ID,
            "content":   content,
            "status":    "completed",
            "metadata":  metadata,
            "timestamp": time.Now().Format(time.RFC3339Nano),
        })
    }
}
```

#### `createToolResultEvent` Removed

The existing `createToolResultEvent` helper is no longer used. The raw result is stored as the completion content of the `llm_tool_call` event.

#### Summarization LLM Streaming

The summarization LLM call uses the same `stream.chunk` event type as regular LLM streaming, but with an `mcp_tool_summary` timeline event type. The frontend can distinguish summarization chunks by the `event_type` field in the `timeline_event.created` event.

The `callSummarizationLLMWithStreaming` function follows the same pattern as `callLLMWithStreaming`:

1. First text chunk → create `mcp_tool_summary` timeline event (status: `streaming`)
2. Subsequent chunks → publish `stream.chunk` with delta
3. Stream complete → finalize timeline event (status: `completed`)

Metadata on the `mcp_tool_summary` timeline event:

```go
metadata := map[string]interface{}{
    "server_name":         serverID,
    "tool_name":           toolName,
    "original_tokens":     estimatedTokens,
    "summarization_backend": execCtx.Config.LLMProvider.Backend, // e.g. "gemini_native", "langchain"
    "summarization_model":   execCtx.Config.LLMProvider.Model,
}
```

### Tool Name Extraction

Both controllers need to extract `serverID` and `toolName` from the tool call name for event metadata and summarization. This uses the existing `mcp.SplitToolName()`:

```go
// In controller, when processing a tool call:
normalizedName := mcp.NormalizeToolName(toolCall.Name) // server__tool → server.tool
serverID, toolName, _ := mcp.SplitToolName(normalizedName)
// serverID and toolName used for tool call events and summarization
```

If `SplitToolName` fails (shouldn't happen since ToolExecutor validates), the full name is used as fallback.

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
│       ├── helpers.go         # createToolCallEvent, completeToolCallEvent
│       ├── summarize.go       # maybeSummarize, callSummarizationLLMWithStreaming (NEW)
│       ├── react.go           # Tool execution + summarization integration
│       └── native_thinking.go # Tool execution + summarization integration
├── models/
│   └── mcp_selection.go       # ParseMCPSelectionConfig added
├── events/
│   └── types.go               # (no changes needed for Phase 4.3)
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
| `pkg/agent/controller/react.go` | Replace tool call/result events with single-event lifecycle, add summarization |
| `pkg/agent/controller/native_thinking.go` | Same as ReAct |
| `pkg/agent/controller/helpers.go` | Rewrite `createToolCallEvent` (streaming pattern), add `completeToolCallEvent`, remove `createToolResultEvent` |
| `pkg/mcp/executor.go` | Remove Phase 4.3 stub comment |
| `ent/schema/llminteraction.go` | Add `"summarization"` to interaction_type enum (no migration) |
| `ent/schema/timelineevent.go` | Remove `mcp_tool_call` and `tool_result` from enum, update `llm_tool_call` comment (no migration) |

### New Files

| File | Purpose |
|------|---------|
| `pkg/mcp/tokens.go` | `EstimateTokens`, `truncateAtLineBoundary`, `truncateForStorage`, `truncateForSummarization` |
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

### Step 3: Tool Call Lifecycle Events

1. Remove `tool_result` and `mcp_tool_call` from TimelineEvent schema enum + update `llm_tool_call` comment + `make ent-generate`
2. Rewrite `createToolCallEvent` to use streaming pattern (content="", args in metadata)
3. Add `completeToolCallEvent` helper
4. Remove `createToolResultEvent` helper
5. Update ReAct and NativeThinking controllers to use single-event lifecycle
6. Write tests for event creation, completion, and publishing

### Step 4: Tool Result Summarization

1. Add `"summarization"` to LLMInteraction schema enum + `make ent-generate` (no migration needed — VARCHAR enum)
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
| `truncateAtLineBoundary` / `truncateForStorage` / `truncateForSummarization` | Below limit, at limit, above limit, zero limit, newline boundary respected, no newlines (hard cut fallback), indented JSON content |
| `resolveMCPSelection` | No override, valid override, invalid server, tool filtering |
| `maybeSummarize` | Below threshold, above threshold, disabled, LLM failure, empty summary |
| `buildConversationContext` | System messages excluded, multi-turn context |
| `createToolCallEvent` / `completeToolCallEvent` | Streaming lifecycle: creation with args in metadata, completion with result in content |

### Integration Tests

Follow existing patterns: real PostgreSQL via `testdb.NewTestClient(t)`, in-memory MCP servers via `startTestServer(t, ...)`, real event streaming via `setupStreamingTest(t)`. See `pkg/mcp/integration_test.go`, `pkg/queue/integration_test.go`, `pkg/events/integration_test.go`, and `pkg/services/integration_test.go` for reference.

#### Feature 1: Per-Alert MCP Selection Override (`pkg/queue/integration_test.go`)

Uses real DB + mock executor to verify the override wiring in `RealSessionExecutor`:

| Scenario | Setup | Validates |
|----------|-------|-----------|
| Session with MCP override → only override servers used | Create session with `mcp_selection` JSON containing 1 of 2 configured servers. Mock executor captures `CreateToolExecutor` args. | `serverIDs` matches override, not chain config |
| Session with tool filter → only specified tools available | Override with `"tools": ["get_pods"]` for one server. | `toolFilter` passed correctly to `CreateToolExecutor` |
| Session with invalid server in override → execution fails | Override references `"nonexistent-server"`. | `resolveMCPSelection` returns error, session status = failed |
| Session without MCP override → chain config used | No `mcp_selection` on session. | `serverIDs` matches chain config servers |
| Native tools override applied | Override with `native_tools: {google_search: false}`. | `ResolvedAgentConfig.NativeToolsOverride` set correctly |

#### Feature 2: Tool Result Summarization (`pkg/agent/controller/integration_test.go`)

New integration test file for controller-level flows. Uses real DB for timeline/interaction records, in-memory MCP servers for tool execution, and a mock LLM client for controlled responses:

| Scenario | Setup | Validates |
|----------|-------|-----------|
| Tool result exceeds threshold → summarized | MCP server returns large result (> 5000 tokens). Mock LLM returns summary on summarization call. Server config has `summarization.enabled: true`. | `mcp_tool_summary` timeline event created in DB with summary content. `LLMInteraction` record with `type: "summarization"` in DB. Conversation message contains summary (not raw). |
| Tool result below threshold → not summarized | MCP server returns small result. | No `mcp_tool_summary` event. No summarization `LLMInteraction`. Conversation message contains raw result. |
| Summarization disabled for server → not summarized | Large result but `summarization.enabled: false`. | No summarization attempted. Raw result used. |
| Summarization LLM fails → fail-open with raw result | Mock LLM returns error on summarization call. | No `mcp_tool_summary` event created. Raw result used in conversation. Investigation continues (no error propagation). |
| Summarization LLM returns empty → fail-open | Mock LLM returns empty string. | Same as LLM failure — raw result used. |
| Storage truncation applied to large result | MCP server returns result > storage limit. | `llm_tool_call` completion content is truncated at newline boundary. MCPInteraction record also truncated. Full (non-truncated) result still sent to summarization LLM. |
| Multiple tool calls in one iteration → each independently handled | Two tool calls: one large (summarized), one small (raw). | Correct summarization decision per tool call. Both `llm_tool_call` events in DB with correct lifecycle. |

#### Feature 3: Tool Call Streaming (`pkg/events/integration_test.go` or `pkg/agent/controller/integration_test.go`)

Uses real DB + real event streaming infrastructure to verify the full lifecycle:

| Scenario | Setup | Validates |
|----------|-------|-----------|
| Tool call lifecycle events published | Execute tool via controller. Subscribe to session WebSocket. | `timeline_event.created` with `event_type: llm_tool_call`, `status: streaming`, metadata has `{server_name, tool_name, arguments}`. Then `timeline_event.completed` with `status: completed`, content has result, metadata has `{is_error}`. |
| Tool call event persisted correctly | Execute tool. Query DB. | Single `llm_tool_call` event in DB with `status: completed`, content = storage-truncated result, metadata = merged (creation + completion). |
| Summarization streaming lifecycle | Large tool result triggers summarization. Subscribe to WebSocket. | `timeline_event.created` with `event_type: mcp_tool_summary`, `status: streaming`. One or more `stream.chunk` events with deltas. Then `timeline_event.completed` for the summary. |
| Catchup on reconnect shows correct state | Execute tool, complete it, then query timeline. | Timeline query returns `llm_tool_call` with `completed` status and result in content. No orphaned `streaming` events. |

#### End-to-End: Full Pipeline (`pkg/queue/integration_test.go`)

Combines all three features in a single flow. This is the most comprehensive test — exercises the full path from session creation through tool execution with override, summarization, and streaming:

| Scenario | Setup | Validates |
|----------|-------|-----------|
| Alert with MCP override → tool execution → summarization → timeline | Create session with `mcp_selection` override (1 server). MCP server returns large result. Mock LLM handles both iteration and summarization calls. | (1) Only override server used. (2) `llm_tool_call` event with streaming lifecycle. (3) `mcp_tool_summary` event with summary. (4) `LLMInteraction` with `type: "summarization"`. (5) Conversation message contains summary. |

#### Test Infrastructure Notes

- **Mock LLM client**: Needs to handle multiple calls (iteration + summarization) with distinguishable behavior. The mock should inspect the system prompt to distinguish summarization calls from iteration calls and return appropriate responses.
- **In-memory MCP servers**: Use `startTestServer(t, name, tools)` from `pkg/mcp/client_test.go`. Configure tools to return configurable-size responses for threshold testing.
- **Timeline assertions**: Query `TimelineEvent` records from DB, verify `event_type`, `status`, `content`, and `metadata` fields.
- **Interaction assertions**: Query `LLMInteraction` and `MCPInteraction` records, verify types and linked timeline events.

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
| Tool call streaming | `mcp.tool_call.started` event | `llm_tool_call` with streaming lifecycle (created → completed) | Single-event lifecycle — no correlation needed, reliable catchup on reconnect |
| Summarization streaming | `llm.stream.chunk` with `stream_type: summarization` | `stream.chunk` with `mcp_tool_summary` timeline event | Reuses existing streaming pattern with appropriate event type |
| Raw result storage | Stored before summarization | `llm_tool_call` completion content (storage-truncated) | Simpler — raw result lives on the same event, no separate `tool_result` |
| Fail-open summarization | Yes | Yes | Same — availability over perfection |
