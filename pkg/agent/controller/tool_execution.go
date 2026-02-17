package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// toolCallResult holds the outcome of executeToolCall for the caller to
// integrate into its conversation format (FunctionCallingController
// tool message).
type toolCallResult struct {
	// Content is the tool result content to feed back to the LLM.
	// May be summarized if summarization was triggered.
	Content string
	// IsError is true if the tool execution itself failed.
	IsError bool
	// Err is the original error from tool execution (non-nil only when
	// ToolExecutor.Execute returned an error). Callers that need to inspect
	// the error type (e.g. context.DeadlineExceeded) should use this field
	// instead of parsing Content.
	Err error
	// Usage is non-nil when summarization produced token usage to accumulate.
	Usage *agent.TokenUsage
}

// executeToolCall runs a single tool call through the full lifecycle:
//  1. Normalize and split tool name for events/summarization
//  2. Create streaming llm_tool_call event (dashboard spinner)
//  3. Execute the tool via ToolExecutor
//  4. Complete the tool call event with storage-truncated result
//  5. Optionally summarize large non-error results
//
// Returns the result content (possibly summarized) and whether the call failed.
// Callers are responsible for appending the result to their conversation and
// recording state changes (RecordFailure, message storage, etc.).
func executeToolCall(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	call agent.ToolCall,
	messages []agent.ConversationMessage,
	eventSeq *int,
) toolCallResult {
	// Step 1: Normalize and split tool name
	normalizedName := mcp.NormalizeToolName(call.Name)
	serverID, toolName, splitErr := mcp.SplitToolName(normalizedName)
	if splitErr != nil {
		serverID = ""
		toolName = call.Name
	}

	// Publish execution progress: gathering_info
	publishExecutionProgress(ctx, execCtx, events.ProgressPhaseGatheringInfo,
		fmt.Sprintf("Calling %s.%s", serverID, toolName))

	// Step 2: Create streaming llm_tool_call event (dashboard shows spinner)
	toolCallEvent, createErr := createToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, eventSeq)
	if createErr != nil {
		slog.Warn("Failed to create tool call event", "error", createErr, "tool", call.Name)
	}

	// Step 3: Execute the tool
	startTime := time.Now()
	result, toolErr := execCtx.ToolExecutor.Execute(ctx, call)
	if toolErr != nil {
		errContent := fmt.Sprintf("Error executing tool: %s", toolErr.Error())
		completeToolCallEvent(ctx, execCtx, toolCallEvent, errContent, true)
		recordMCPInteraction(ctx, execCtx, serverID, toolName, call.Arguments, nil, startTime, toolErr)
		return toolCallResult{Content: errContent, IsError: true, Err: toolErr}
	}

	// Record successful MCP interaction
	recordMCPInteraction(ctx, execCtx, serverID, toolName, call.Arguments, result, startTime, nil)

	// Step 4: Complete tool call event with storage-truncated result
	storageTruncated := mcp.TruncateForStorage(result.Content)
	completeToolCallEvent(ctx, execCtx, toolCallEvent, storageTruncated, result.IsError)

	// Step 5: Summarize if applicable (non-error results only)
	content := result.Content
	var usage *agent.TokenUsage
	if !result.IsError {
		convContext := buildConversationContext(messages)
		sumResult, sumErr := maybeSummarize(ctx, execCtx, serverID, toolName,
			result.Content, convContext, eventSeq)
		if sumErr == nil && sumResult.WasSummarized {
			content = sumResult.Content
			usage = sumResult.Usage
		}
	}

	return toolCallResult{Content: content, IsError: result.IsError, Usage: usage}
}

// recordToolListInteractions records one tool_list MCP interaction per server,
// capturing the tools that were available to the agent at execution start.
// Best-effort: logs on failure but never aborts the investigation.
func recordToolListInteractions(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	tools []agent.ToolDefinition,
) {
	if len(tools) == 0 {
		return
	}

	// Group tool names by server.
	byServer := make(map[string][]string)
	for _, t := range tools {
		serverID, toolName, err := mcp.SplitToolName(t.Name)
		if err != nil {
			continue
		}
		byServer[serverID] = append(byServer[serverID], toolName)
	}

	// Sort server IDs for deterministic creation order
	// (matters for created_at-based ordering in trace view).
	serverIDs := make([]string, 0, len(byServer))
	for id := range byServer {
		serverIDs = append(serverIDs, id)
	}
	sort.Strings(serverIDs)

	for _, serverID := range serverIDs {
		toolNames := byServer[serverID]
		sort.Strings(toolNames)
		availableTools := make(map[string]any, 1)
		availableTools["tools"] = toolNames

		interaction, err := execCtx.Services.Interaction.CreateMCPInteraction(ctx, models.CreateMCPInteractionRequest{
			SessionID:       execCtx.SessionID,
			StageID:         execCtx.StageID,
			ExecutionID:     execCtx.ExecutionID,
			InteractionType: "tool_list",
			ServerName:      serverID,
			AvailableTools:  availableTools,
		})
		if err != nil {
			slog.Error("Failed to record tool_list interaction",
				"session_id", execCtx.SessionID, "server", serverID, "error", err)
			continue
		}
		publishInteractionCreated(ctx, execCtx, interaction.ID, events.InteractionTypeMCP)
	}
}

// recordMCPInteraction creates an MCPInteraction record in the database.
// Logs on failure but does not abort — mirrors recordLLMInteraction pattern.
func recordMCPInteraction(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	serverID string,
	toolName string,
	arguments string,
	result *agent.ToolResult,
	startTime time.Time,
	toolErr error,
) {
	durationMs := int(time.Since(startTime).Milliseconds())

	// Parse arguments from JSON string into map for structured storage.
	var toolArgs map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &toolArgs); err != nil {
			// Fall back to storing as raw string.
			toolArgs = map[string]any{"raw": arguments}
		}
	}

	var toolResult map[string]any
	if result != nil {
		toolResult = map[string]any{
			"content":  mcp.TruncateForStorage(result.Content),
			"is_error": result.IsError,
		}
	}

	var errMsg *string
	if toolErr != nil {
		s := toolErr.Error()
		errMsg = &s
	}

	req := models.CreateMCPInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         execCtx.StageID,
		ExecutionID:     execCtx.ExecutionID,
		InteractionType: "tool_call",
		ServerName:      serverID,
		ToolName:        &toolName,
		ToolArguments:   toolArgs,
		ToolResult:      toolResult,
		DurationMs:      &durationMs,
		ErrorMessage:    errMsg,
	}

	interaction, err := execCtx.Services.Interaction.CreateMCPInteraction(ctx, req)
	if err != nil {
		slog.Error("Failed to record MCP interaction",
			"session_id", execCtx.SessionID, "server", serverID, "tool", toolName, "error", err)
		return
	}

	// Publish interaction.created event for trace view live updates.
	publishInteractionCreated(ctx, execCtx, interaction.ID, events.InteractionTypeMCP)
}
