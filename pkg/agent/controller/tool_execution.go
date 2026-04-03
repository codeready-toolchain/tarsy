package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/orchestrator"
	"github.com/codeready-toolchain/tarsy/pkg/agent/skill"
	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// ToolType classifies a tool call for dashboard rendering and trace metadata.
type ToolType string

const (
	ToolTypeMCP          ToolType = "mcp"
	ToolTypeOrchestrator ToolType = "orchestrator"
	ToolTypeSkill        ToolType = "skill"
	ToolTypeMemory       ToolType = "memory"
	ToolTypeNative       ToolType = "google_native"
)

// geminiNativeServerID labels metrics and MCP interactions for Gemini
// provider-native tools (google_search, url_context, code_execution) that
// are not executed via MCP.
const geminiNativeServerID = "gemini-native"

// toolCallResult holds the outcome of executeToolCall for the caller to
// integrate into its conversation format (IteratingController
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

// syntheticNativeGeminiToolResult builds the role=tool message for Gemini
// provider-native tools. Grounding metadata is attached to the HTTP response,
// not returned by MCP — the model must not treat a stub as evidence.
func syntheticNativeGeminiToolResult(toolName string, groundings []agent.GroundingChunk) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The %q tool was executed by the Gemini API in this turn (not by the MCP tool runner). "+
		"Base factual claims on the assistant text above, grounding/url_context events in the timeline, "+
		"and the digest below — do not invent URLs, file paths, or metrics that do not appear here.\n\n",
		toolName)

	if toolName == string(config.GoogleNativeToolCodeExecution) {
		b.WriteString("For interpreted code output, see code_execution timeline events and the model response text.\n\n")
	}

	querySeen := make(map[string]struct{})
	var queries []string
	sourceSeen := make(map[string]struct{})
	var sourceLines []string
	for _, g := range groundings {
		for _, q := range g.WebSearchQueries {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			if _, ok := querySeen[q]; ok {
				continue
			}
			querySeen[q] = struct{}{}
			queries = append(queries, q)
		}
		for _, s := range g.Sources {
			u := strings.TrimSpace(s.URI)
			if u == "" {
				continue
			}
			if _, ok := sourceSeen[u]; ok {
				continue
			}
			sourceSeen[u] = struct{}{}
			title := strings.TrimSpace(s.Title)
			if title != "" {
				sourceLines = append(sourceLines, fmt.Sprintf("- %s (%s)", u, title))
			} else {
				sourceLines = append(sourceLines, "- "+u)
			}
		}
	}
	if len(queries) > 0 {
		b.WriteString("Web search queries (from grounding metadata):\n")
		for _, q := range queries {
			fmt.Fprintf(&b, "- %s\n", q)
		}
		b.WriteByte('\n')
	}
	if len(sourceLines) > 0 {
		b.WriteString("Sources (from grounding metadata):\n")
		for _, line := range sourceLines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
		return b.String()
	}
	b.WriteString("No grounding metadata (queries or source URLs) was present in this response turn. " +
		"If you lack independent verification, state that explicitly.")
	return b.String()
}

// omitLLMToolCallForNativeSearchOrURL is true when the dashboard already shows a dedicated
// google_search_result or url_context_result (or URL/search fallbacks from tool args), so we
// skip the generic llm_tool_call row that would duplicate the same turn with synthetic digest text.
//
// The google_search argument parser (parseGoogleSearchToolArgumentQueries) runs only inside
// the GoogleNativeToolGoogleSearch branch — not for MCP or other tool names.
func omitLLMToolCallForNativeSearchOrURL(
	effectiveName string,
	groundings []agent.GroundingChunk,
	toolArguments string,
) bool {
	switch effectiveName {
	case string(config.GoogleNativeToolGoogleSearch):
		return groundingsHaveAnySources(groundings) ||
			groundingsHaveAnyWebSearchQueries(groundings) ||
			len(parseGoogleSearchToolArgumentQueries(toolArguments)) > 0
	case string(config.GoogleNativeToolURLContext):
		if groundingsHaveAnySources(groundings) {
			return true
		}
		return len(parseURLContextToolArgumentURLs(toolArguments)) > 0
	default:
		return false
	}
}

// executeToolCall runs a single tool call through the full lifecycle:
//  1. Normalize and split tool name for events/summarization
//  2. Create streaming llm_tool_call event (dashboard spinner), unless omitted for native search/URL
//  3. Execute the tool via ToolExecutor (or synthesize result for Gemini native tools)
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
	sameTurnGroundings []agent.GroundingChunk,
	eventSeq *int,
) toolCallResult {
	// Step 1: Normalize and split tool name (colon-prefixed orchestration names first)
	plainFixed := mcp.NormalizeBuiltinPlainToolName(call.Name)
	effectiveName := mcp.NormalizeToolName(plainFixed)
	if execCtx.Config != nil && execCtx.Config.LLMBackend == config.LLMBackendNativeGemini {
		effectiveName = config.CanonicalGoogleNativeToolWireName(effectiveName)
	}
	serverID, toolName, splitErr := mcp.SplitToolName(effectiveName)
	var toolType ToolType
	if splitErr != nil {
		toolName = effectiveName
		if orchestrator.IsOrchestrationTool(toolName) {
			serverID = orchestrator.OrchestrationServerName
			toolType = ToolTypeOrchestrator
		} else if skill.IsSkillTool(toolName) {
			toolType = ToolTypeSkill
		} else if k, ok := builtintools.KindForPlainTool(toolName); ok && k == builtintools.KindMemory {
			toolType = ToolTypeMemory
		} else if config.IsGoogleNativeToolWireName(effectiveName) {
			serverID = geminiNativeServerID
			toolType = ToolTypeNative
		} else {
			toolType = ToolTypeMCP
		}
	} else {
		toolType = ToolTypeMCP
	}

	providerNativeTool := execCtx.Config != nil &&
		execCtx.Config.LLMBackend == config.LLMBackendNativeGemini &&
		config.IsGoogleNativeToolWireName(effectiveName)
	omitToolCallTimeline := providerNativeTool &&
		omitLLMToolCallForNativeSearchOrURL(effectiveName, sameTurnGroundings, call.Arguments)

	// Publish execution progress: gathering_info
	publishExecutionProgress(ctx, execCtx, events.ProgressPhaseGatheringInfo,
		fmt.Sprintf("Calling %s.%s", serverID, toolName))

	// Step 2: Create streaming llm_tool_call event (dashboard shows spinner).
	// Native google_search / url_context: omit when grounding (or URL-arg fallback) already produced a timeline row.
	var toolCallEvent *ent.TimelineEvent
	if !omitToolCallTimeline {
		var createErr error
		toolCallEvent, createErr = createToolCallEvent(ctx, execCtx, serverID, toolName, toolType, call.Arguments, eventSeq)
		if createErr != nil {
			slog.Warn("Failed to create tool call event", "error", createErr, "tool", call.Name)
		}
	}

	// Step 3: Execute the tool with its own timeout within the iteration budget.
	toolCtx, toolCancel := context.WithTimeout(ctx, execCtx.Config.ToolCallTimeout)
	startTime := time.Now()
	execCall := call
	// Only rewrite the tool name when colon-prefix built-in correction applied.
	// Otherwise preserve the LLM name (e.g. server__tool) — MCP executor normalizes.
	if plainFixed != call.Name {
		execCall.Name = effectiveName
	}

	var result *agent.ToolResult
	var toolErr error
	if providerNativeTool {
		result = &agent.ToolResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: syntheticNativeGeminiToolResult(effectiveName, sameTurnGroundings),
			IsError: false,
		}
	} else {
		result, toolErr = execCtx.ToolExecutor.Execute(toolCtx, execCall)
	}
	toolCancel()

	metrics.MCPCallsTotal.WithLabelValues(serverID, toolName).Inc()
	metrics.MCPDurationSeconds.WithLabelValues(serverID, toolName).Observe(time.Since(startTime).Seconds())

	if toolErr != nil {
		metrics.MCPErrorsTotal.WithLabelValues(serverID, toolName).Inc()
		errContent := fmt.Sprintf("Error executing tool: %s", toolErr.Error())
		completeToolCallEvent(ctx, execCtx, toolCallEvent, errContent, true)
		recordMCPInteraction(ctx, execCtx, serverID, toolName, call.Arguments, nil, startTime, toolErr)
		return toolCallResult{Content: errContent, IsError: true, Err: toolErr}
	}

	if result.IsError {
		metrics.MCPErrorsTotal.WithLabelValues(serverID, toolName).Inc()
	}

	// Record MCP interaction (raw data preserved in trace for debugging)
	recordMCPInteraction(ctx, execCtx, serverID, toolName, call.Arguments, result, startTime, nil)

	// When Gemini streaming omits url_context_metadata/grounding sources but the model still
	// issued a url_context function call, emit a dashboard row from parsed tool arguments.
	if providerNativeTool && !result.IsError &&
		effectiveName == string(config.GoogleNativeToolURLContext) {
		createURLContextFallbackFromToolArgs(ctx, execCtx, call.Arguments, sameTurnGroundings, eventSeq)
	}
	if providerNativeTool && !result.IsError &&
		effectiveName == string(config.GoogleNativeToolGoogleSearch) {
		createGoogleSearchFallbackFromToolArgs(ctx, execCtx, call.Arguments, sameTurnGroundings, eventSeq)
	}

	content := result.Content
	var usage *agent.TokenUsage

	// Step 4–5: Complete tool call event and optionally summarize.
	//
	// RequiredSummarization (e.g. search_past_sessions): the tool returned raw
	// DB data that isn't useful in the dashboard. Run the LLM summarization
	// first, then complete the tool call event with the summary so the
	// dashboard shows a single card with the digest. The separate
	// mcp_tool_summary timeline event is skipped (createTimelineEvent=false)
	// but the LLM interaction is still recorded for observability.
	//
	// Regular tools: complete with the raw result, then optionally summarize
	// large results via maybeSummarize (creates a separate mcp_tool_summary).
	if !result.IsError && result.RequiredSummarization != nil {
		estimatedTokens := mcp.EstimateTokens(result.Content)
		var streamEventID string
		if toolCallEvent != nil {
			streamEventID = toolCallEvent.ID
		}
		summary, sumUsage, sumErr := callSummarizationLLM(ctx, execCtx,
			result.RequiredSummarization.SystemPrompt,
			result.RequiredSummarization.UserPrompt,
			serverID, toolName, estimatedTokens, eventSeq,
			summarizationStreamTarget{existingEventID: streamEventID})
		if sumErr != nil {
			slog.Warn("Required summarization failed",
				"server", serverID, "tool", toolName, "error", sumErr)
			content = "Unable to retrieve session history — summarization failed."
			result.IsError = true
		} else {
			content = summary
			if result.RequiredSummarization.TransformResult != nil {
				content = result.RequiredSummarization.TransformResult(content)
			}
			usage = sumUsage
		}
		completeToolCallEvent(ctx, execCtx, toolCallEvent, content, result.IsError)
	} else {
		storageTruncated := mcp.TruncateForStorage(result.Content)
		completeToolCallEvent(ctx, execCtx, toolCallEvent, storageTruncated, result.IsError)

		if !result.IsError {
			convContext := buildConversationContext(messages)
			sumResult, sumErr := maybeSummarize(ctx, execCtx, serverID, toolName,
				result.Content, convContext, eventSeq)
			if sumErr == nil && sumResult.WasSummarized {
				content = sumResult.Content
				usage = sumResult.Usage
			}
		}
	}

	return toolCallResult{Content: content, IsError: result.IsError, Usage: usage}
}

// toolListEntry is the per-tool object stored in available_tools.
type toolListEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// recordToolListInteractions records one tool_list MCP interaction per server,
// capturing the tools that were available to the agent at execution start.
// Each tool entry includes its name and description for the trace view.
// Best-effort: logs on failure but never aborts the investigation.
func recordToolListInteractions(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	tools []agent.ToolDefinition,
) {
	if len(tools) == 0 {
		return
	}

	// Group tools by server, preserving name + description.
	// Mirrors the classification logic in executeToolCall:
	//   - MCP tools (server__tool format) → server name from split
	//   - Orchestration tools (dispatch_agent, etc.) → OrchestrationServerName
	//   - Other built-in tools (load_skill, etc.) → empty-string server
	byServer := make(map[string][]toolListEntry)
	for _, t := range tools {
		normalized := mcp.NormalizeToolName(t.Name)
		serverID, toolName, err := mcp.SplitToolName(normalized)
		if err != nil {
			toolName = t.Name
			if orchestrator.IsOrchestrationTool(toolName) {
				serverID = orchestrator.OrchestrationServerName
			}
		}
		byServer[serverID] = append(byServer[serverID], toolListEntry{
			Name:        toolName,
			Description: t.Description,
		})
	}

	// Sort server IDs for deterministic creation order
	// (matters for created_at-based ordering in trace view).
	serverIDs := make([]string, 0, len(byServer))
	for id := range byServer {
		serverIDs = append(serverIDs, id)
	}
	sort.Strings(serverIDs)

	for _, serverID := range serverIDs {
		entries := byServer[serverID]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name < entries[j].Name
		})

		availableTools := make([]any, len(entries))
		for i, e := range entries {
			availableTools[i] = e
		}

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
