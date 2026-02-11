package controller

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
)

// toolCallResult holds the outcome of executeToolCall for the caller to
// integrate into its conversation format (ReAct observation vs NativeThinking
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

	// Step 2: Create streaming llm_tool_call event (dashboard shows spinner)
	toolCallEvent, createErr := createToolCallEvent(ctx, execCtx, serverID, toolName, call.Arguments, eventSeq)
	if createErr != nil {
		slog.Warn("Failed to create tool call event", "error", createErr, "tool", call.Name)
	}

	// Step 3: Execute the tool
	result, toolErr := execCtx.ToolExecutor.Execute(ctx, call)
	if toolErr != nil {
		errContent := fmt.Sprintf("Error executing tool: %s", toolErr.Error())
		completeToolCallEvent(ctx, execCtx, toolCallEvent, errContent, true)
		return toolCallResult{Content: errContent, IsError: true, Err: toolErr}
	}

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
