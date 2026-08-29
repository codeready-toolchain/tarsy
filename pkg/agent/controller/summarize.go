package controller

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// SummarizationResult holds the outcome of a summarization attempt.
type SummarizationResult struct {
	Content       string            // Summary text (or original if not summarized)
	WasSummarized bool              // Whether summarization was performed
	Usage         *agent.TokenUsage // Token usage from summarization LLM call (nil if not summarized)
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
	if execCtx.PromptBuilder == nil {
		return &SummarizationResult{Content: rawContent}, nil
	}

	registry := execCtx.PromptBuilder.MCPServerRegistry()
	if registry == nil {
		return &SummarizationResult{Content: rawContent}, nil
	}

	serverConfig, err := registry.Get(serverID)
	if err != nil {
		return &SummarizationResult{Content: rawContent}, nil
	}

	// Summarization is enabled by default; only skip if explicitly disabled
	if serverConfig.Summarization != nil && serverConfig.Summarization.SummarizationDisabled() {
		return &SummarizationResult{Content: rawContent}, nil
	}

	// 2. Estimate token count and resolve effective config (defaults for nil)
	estimatedTokens := mcp.EstimateTokens(rawContent)
	threshold := config.DefaultSizeThresholdTokens
	maxSummaryTokens := 1000
	if serverConfig.Summarization != nil {
		if serverConfig.Summarization.SizeThresholdTokens > 0 {
			threshold = serverConfig.Summarization.SizeThresholdTokens
		}
		if serverConfig.Summarization.SummaryMaxTokenLimit > 0 {
			maxSummaryTokens = serverConfig.Summarization.SummaryMaxTokenLimit
		}
	}

	if estimatedTokens <= threshold {
		return &SummarizationResult{Content: rawContent}, nil
	}

	// 3. Summarization needed
	slog.Info("Tool result exceeds summarization threshold",
		"server", serverID, "tool", toolName,
		"estimated_tokens", estimatedTokens, "threshold", threshold)

	// Publish execution progress: distilling
	publishExecutionProgress(ctx, execCtx, events.ProgressPhaseDistilling,
		fmt.Sprintf("Summarizing %s.%s (%d tokens)", serverID, toolName, estimatedTokens))

	// 4. Safety-net truncate for summarization input
	truncatedForLLM := mcp.TruncateForSummarization(rawContent)

	// 5. Build summarization prompts
	systemPrompt := execCtx.PromptBuilder.BuildMCPSummarizationSystemPrompt(serverID, toolName, maxSummaryTokens)
	userPrompt := execCtx.PromptBuilder.BuildMCPSummarizationUserPrompt(conversationContext, serverID, toolName, truncatedForLLM)

	// 6. Perform summarization LLM call with streaming
	summary, usage, err := callSummarizationLLM(ctx, execCtx, systemPrompt, userPrompt, serverID, toolName, estimatedTokens, eventSeq, summarizationStreamTarget{createEvent: true}, serverConfig.Summarization)
	if err != nil {
		slog.Warn("Summarization LLM call failed, using raw result",
			"server", serverID, "tool", toolName, "error", err)
		return &SummarizationResult{Content: rawContent}, nil // Fail-open: use raw result
	}

	// 7. Wrap summary with context note
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

// summarizationStreamTarget configures how summarization streams to the dashboard.
type summarizationStreamTarget struct {
	// createEvent creates a new mcp_tool_summary timeline event and streams to it.
	createEvent bool
	// existingEventID streams chunks to an existing timeline event (e.g. the
	// llm_tool_call event for RequiredSummarization). Takes precedence over createEvent.
	existingEventID string
}

// callSummarizationLLM performs the summarization LLM call with streaming.
// The streamTarget controls how chunks reach the dashboard:
//   - createEvent: creates a new mcp_tool_summary event and streams to it
//   - existingEventID: streams chunks to an already-created event (e.g. llm_tool_call)
//   - neither: silently collects the stream (no dashboard updates)
//
// Always records an LLMInteraction with type "summarization".
func callSummarizationLLM(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	systemPrompt, userPrompt string,
	serverID, toolName string,
	estimatedTokens int,
	eventSeq *int,
	streamTarget summarizationStreamTarget,
	serverSummarization *config.SummarizationConfig,
) (string, *agent.TokenUsage, error) {
	resolved, resolveErr := agent.ResolveSummarizationLLM(execCtx, serverSummarization)
	primaryName := resolved.ProviderName
	if primaryName == "" && execCtx != nil && execCtx.Config != nil {
		primaryName = execCtx.Config.LLMProviderName
	}
	appliedSticky := false
	if execCtx != nil {
		if sticky, ok := execCtx.SummarizationSticky[primaryName]; ok {
			resolved = sticky
			appliedSticky = true
			resolveErr = nil
		}
	}

	fbState := &FallbackState{
		SingleShot:           true,
		CurrentProviderIndex: -1,
		AttemptedProviders:   []string{resolved.ProviderName},
	}
	if appliedSticky {
		if idx := summarizationFallbackIndex(summarizationFallbackList(execCtx), resolved.ProviderName); idx >= 0 {
			fbState.CurrentProviderIndex = idx
		}
	}

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemPrompt},
		{Role: agent.RoleUser, Content: userPrompt},
	}

	attempt := 0
	var lastErr error
	if resolveErr != nil {
		lastErr = resolveErr
	}

	for {
		if lastErr == nil {
			attemptStart := time.Now()
			input := &agent.GenerateInput{
				SessionID:   execCtx.SessionID,
				ExecutionID: execCtx.ExecutionID + agent.SummarizationExecutionIDSuffix,
				Messages:    messages,
				Config:      resolved.Provider,
				Tools:       nil, // No tools for summarization
				Backend:     resolved.Backend,
				ClearCache:  attempt > 0,
			}

			thisStream := streamTarget
			if attempt > 0 && streamTarget.existingEventID != "" {
				// RequiredSummarization: only the first attempt streams into the tool-call card.
				thisStream = summarizationStreamTarget{}
			}

			streamed, err := callSummarizationLLMWithStreaming(ctx, execCtx, input, resolved, primaryName, serverID, toolName, estimatedTokens, eventSeq, thisStream)
			modelName := ""
			if resolved.Provider != nil {
				modelName = resolved.Provider.Model
			}
			metrics.ObserveLLMCall(resolved.ProviderName, modelName,
				time.Since(attemptStart), metricsTokens(streamed, err), err)
			if err == nil {
				stickSummarizationProvider(execCtx, primaryName, resolved)
				summary := strings.TrimSpace(streamed.Text)
				recordSummarizationInteraction(ctx, execCtx, messages, summary,
					streamed.LLMResponse, attemptStart, modelName)
				return summary, streamed.Usage, nil
			}
			lastErr = err
		}

		if ctx.Err() != nil {
			return "", nil, fmt.Errorf("summarization LLM call failed: %w", lastErr)
		}

		fromProvider := resolved.ProviderName
		fromBackend := resolved.Backend
		next, ok := nextSummarizationFallback(execCtx, fbState, lastErr)
		if !ok {
			return "", nil, fmt.Errorf("summarization LLM call failed: %w", lastErr)
		}
		slog.Info("Summarization falling back to next LLM provider",
			"session_id", execCtx.SessionID,
			"execution_id", execCtx.ExecutionID,
			"from_provider", fromProvider,
			"from_backend", fromBackend,
			"to_provider", next.ProviderName,
			"to_backend", next.Backend,
			"reason", lastErr.Error(),
		)
		resolved = next
		lastErr = nil
		attempt++
	}
}

func summarizationFallbackList(execCtx *agent.ExecutionContext) []agent.ResolvedFallbackEntry {
	if execCtx == nil || execCtx.Config == nil {
		return nil
	}
	return execCtx.Config.ResolvedFallbackProviders
}

func summarizationFallbackIndex(list []agent.ResolvedFallbackEntry, name string) int {
	return slices.IndexFunc(list, func(e agent.ResolvedFallbackEntry) bool {
		return e.ProviderName == name
	})
}

// stickSummarizationProvider records the answering fallback for later calls
// with the same primary. When the primary itself answers, any stale sticky
// entry is cleared so metadata and the next call match the provider in use.
func stickSummarizationProvider(execCtx *agent.ExecutionContext, primaryName string, answerer agent.ResolvedSummarizationLLM) {
	if execCtx == nil || primaryName == "" {
		return
	}
	if answerer.ProviderName == primaryName {
		delete(execCtx.SummarizationSticky, primaryName)
		return
	}
	if execCtx.SummarizationSticky == nil {
		execCtx.SummarizationSticky = make(map[string]agent.ResolvedSummarizationLLM)
	}
	execCtx.SummarizationSticky[primaryName] = answerer
}

// summarizationProviderMetadata is the answering-model fields shared by
// mcp_tool_summary events and search_past_sessions llm_tool_call completion.
func summarizationProviderMetadata(resolved agent.ResolvedSummarizationLLM, primaryName string) map[string]any {
	meta := map[string]any{}
	if resolved.Provider != nil && resolved.Provider.Model != "" {
		meta["summarization_model"] = resolved.Provider.Model
	}
	if resolved.ProviderName != "" {
		meta["summarization_provider"] = resolved.ProviderName
	}
	if resolved.ProviderName != "" && resolved.ProviderName != primaryName {
		meta["summarization_fallback"] = true
	}
	return meta
}

func summarizationAnswererMetadata(execCtx *agent.ExecutionContext, server *config.SummarizationConfig) map[string]any {
	if execCtx == nil {
		return nil
	}
	resolved, err := agent.ResolveSummarizationLLM(execCtx, server)
	primaryName := resolved.ProviderName
	if primaryName == "" && execCtx.Config != nil {
		primaryName = execCtx.Config.LLMProviderName
	}
	if sticky, ok := execCtx.SummarizationSticky[primaryName]; ok {
		resolved = sticky
	} else if err != nil {
		return nil
	}
	return summarizationProviderMetadata(resolved, primaryName)
}

// nextSummarizationFallback walks ResolvedFallbackProviders locally. It does not
// mutate the investigator, skip RequiresNativeTools, increment LLMFallbacksTotal,
// or emit provider_fallback.
func nextSummarizationFallback(
	execCtx *agent.ExecutionContext,
	state *FallbackState,
	err error,
) (agent.ResolvedSummarizationLLM, bool) {
	list := summarizationFallbackList(execCtx)
	if !state.shouldFallback(err, list) {
		return agent.ResolvedSummarizationLLM{}, false
	}

	nextIdx := state.CurrentProviderIndex + 1
	for nextIdx < len(list) {
		if slices.Contains(state.AttemptedProviders, list[nextIdx].ProviderName) {
			slog.Info("Skipping summarization fallback entry already attempted this call",
				"session_id", execCtx.SessionID,
				"execution_id", execCtx.ExecutionID,
				"skipped_provider", list[nextIdx].ProviderName,
				"skipped_backend", list[nextIdx].Backend,
			)
			nextIdx++
			continue
		}
		break
	}
	if nextIdx >= len(list) {
		return agent.ResolvedSummarizationLLM{}, false
	}

	state.CurrentProviderIndex = nextIdx
	state.AttemptedProviders = append(state.AttemptedProviders, list[nextIdx].ProviderName)
	state.resetCounters()
	return agent.SummarizationLLMFromFallback(list[nextIdx]), true
}

// callSummarizationLLMWithStreaming is analogous to callLLMWithStreaming but
// creates mcp_tool_summary timeline events instead of llm_response events.
// The streaming pattern is identical (create event -> stream chunks -> finalize).
// Simpler than callLLMWithStreaming: no thinking event (summarization has no thinking stream).
//
// streamTarget controls dashboard streaming:
//   - existingEventID: streams chunks to an already-created event (no new event created,
//     no finalization — the caller is responsible for completing the event).
//   - createEvent: creates a new mcp_tool_summary event and streams to it.
//   - neither: silently collects the stream.
func callSummarizationLLMWithStreaming(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	input *agent.GenerateInput,
	resolved agent.ResolvedSummarizationLLM,
	primaryName string,
	serverID, toolName string,
	estimatedTokens int,
	eventSeq *int,
	streamTarget summarizationStreamTarget,
) (*StreamedResponse, error) {
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	stream, err := execCtx.LLMClient.Generate(llmCtx, input)
	if err != nil {
		return nil, fmt.Errorf("summarization LLM Generate failed: %w", err)
	}

	// Stream to an existing event (e.g. the llm_tool_call for RequiredSummarization).
	// Only publishes chunks — caller completes the event.
	if streamTarget.existingEventID != "" && execCtx.EventPublisher != nil {
		pid := parentExecID(execCtx)
		callback := func(chunkType string, delta string) {
			if delta == "" || chunkType != ChunkTypeText {
				return
			}
			if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeStreamChunk,
					SessionID: execCtx.SessionID,
					Timestamp: time.Now().Format(time.RFC3339Nano),
				},
				EventID:           streamTarget.existingEventID,
				ParentExecutionID: pid,
				Delta:             delta,
			}); pubErr != nil {
				slog.Warn("Failed to publish summary stream chunk to existing event",
					"event_id", streamTarget.existingEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		}
		resp, collectErr := collectStreamWithCallback(stream, callback, nil, 0, 0)
		if collectErr != nil {
			return nil, collectErr
		}
		return streamedSummarizationResponse(resp)
	}

	if !streamTarget.createEvent || execCtx.EventPublisher == nil {
		resp, collectErr := collectStream(stream)
		if collectErr != nil {
			return nil, collectErr
		}
		return streamedSummarizationResponse(resp)
	}

	// Track streaming timeline event
	var summaryEventID string
	var eventCreateFailed bool
	pid := parentExecID(execCtx)

	metadata := map[string]any{
		"server_name":     serverID,
		"tool_name":       toolName,
		"original_tokens": estimatedTokens,
	}
	maps.Copy(metadata, summarizationProviderMetadata(resolved, primaryName))

	callback := func(chunkType string, delta string) {
		if delta == "" || chunkType != ChunkTypeText {
			return // Only handle text chunks for summarization
		}

		if eventCreateFailed {
			return
		}

		if summaryEventID == "" {
			// First text chunk — create streaming mcp_tool_summary TimelineEvent
			*eventSeq++
			event, createErr := execCtx.Services.Timeline.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
				SessionID:         execCtx.SessionID,
				StageID:           &execCtx.StageID,
				ExecutionID:       &execCtx.ExecutionID,
				ParentExecutionID: parentExecIDPtr(execCtx),
				SequenceNumber:    *eventSeq,
				EventType:         timelineevent.EventTypeMcpToolSummary,
				Content:           "",
				Metadata:          metadata,
			})
			if createErr != nil {
				slog.Warn("Failed to create streaming summary event", "session_id", execCtx.SessionID, "error", createErr)
				eventCreateFailed = true
				return
			}
			summaryEventID = event.ID
			if pubErr := execCtx.EventPublisher.PublishTimelineCreated(ctx, execCtx.SessionID, events.TimelineCreatedPayload{
				BasePayload: events.BasePayload{
					Type:      events.EventTypeTimelineCreated,
					SessionID: execCtx.SessionID,
					Timestamp: event.CreatedAt.Format(time.RFC3339Nano),
				},
				EventID:           summaryEventID,
				StageID:           execCtx.StageID,
				ExecutionID:       execCtx.ExecutionID,
				ParentExecutionID: pid,
				EventType:         timelineevent.EventTypeMcpToolSummary,
				Status:            timelineevent.StatusStreaming,
				Content:           "",
				Metadata:          metadata,
				SequenceNumber:    *eventSeq,
			}); pubErr != nil {
				slog.Warn("Failed to publish streaming summary created",
					"event_id", summaryEventID, "session_id", execCtx.SessionID, "error", pubErr)
			}
		}

		// Publish delta
		if pubErr := execCtx.EventPublisher.PublishStreamChunk(ctx, execCtx.SessionID, events.StreamChunkPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeStreamChunk,
				SessionID: execCtx.SessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			EventID:           summaryEventID,
			ParentExecutionID: pid,
			Delta:             delta,
		}); pubErr != nil {
			slog.Warn("Failed to publish summary stream chunk",
				"event_id", summaryEventID, "session_id", execCtx.SessionID, "error", pubErr)
		}
	}

	resp, err := collectStreamWithCallback(stream, callback, nil, 0, 0)
	failCreatedSummary := func(failContent string) {
		if summaryEventID == "" {
			return
		}
		if failErr := execCtx.Services.Timeline.FailTimelineEvent(ctx, summaryEventID, failContent); failErr != nil {
			slog.Warn("Failed to mark summary event as failed",
				"event_id", summaryEventID, "session_id", execCtx.SessionID, "error", failErr)
		}
		if pubErr := execCtx.EventPublisher.PublishTimelineCompleted(ctx, execCtx.SessionID, events.TimelineCompletedPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeTimelineCompleted,
				SessionID: execCtx.SessionID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			EventID:           summaryEventID,
			ParentExecutionID: pid,
			EventType:         timelineevent.EventTypeMcpToolSummary,
			Content:           failContent,
			Status:            timelineevent.StatusFailed,
		}); pubErr != nil {
			slog.Warn("Failed to publish summary failure",
				"event_id", summaryEventID, "session_id", execCtx.SessionID, "error", pubErr)
		}
	}
	if err != nil {
		failCreatedSummary(fmt.Sprintf("Summarization streaming failed: %s", err.Error()))
		return nil, err
	}

	streamed, emptyErr := streamedSummarizationResponse(resp)
	streamed.TextEventCreated = summaryEventID != ""
	if emptyErr != nil {
		// Reject before finalize so fallback does not leave a completed empty card.
		failCreatedSummary(emptyErr.Error())
		return streamed, emptyErr
	}

	if summaryEventID != "" {
		finalizeStreamingEvent(ctx, execCtx, summaryEventID, timelineevent.EventTypeMcpToolSummary, resp.Text, "summary")
	}

	return streamed, nil
}

// streamedSummarizationResponse wraps a collected LLM response and rejects
// empty or whitespace-only text so callers can fall back before completing
// a dashboard event.
func streamedSummarizationResponse(resp *LLMResponse) (*StreamedResponse, error) {
	streamed := &StreamedResponse{LLMResponse: resp}
	if resp == nil || strings.TrimSpace(resp.Text) == "" {
		return streamed, fmt.Errorf("summarization produced empty result")
	}
	return streamed, nil
}

// recordSummarizationInteraction records an LLMInteraction with the conversation
// stored inline in llm_request. Summarization conversations are self-contained
// (system + user + assistant) and don't share the iteration's message sequence,
// so we embed them directly rather than using the Message table.
func recordSummarizationInteraction(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	inputMessages []agent.ConversationMessage,
	assistantText string,
	resp *LLMResponse,
	startTime time.Time,
	modelName string,
) {
	durationMs := int(time.Since(startTime).Milliseconds())

	var inputTokens, outputTokens, totalTokens, thinkingTokens *int
	var cacheReadTokens, cacheCreationTokens *int
	var textLen int

	if resp != nil {
		if resp.Usage != nil {
			inputTokens = &resp.Usage.InputTokens
			outputTokens = &resp.Usage.OutputTokens
			totalTokens = &resp.Usage.TotalTokens
			// TokenUsage has no presence flag (proto scalars default to 0). Persist
			// thinking/cache only when > 0 so unreported LangChain zeros stay nil.
			if resp.Usage.ThinkingTokens > 0 {
				thinkingTokens = &resp.Usage.ThinkingTokens
			}
			if resp.Usage.CacheReadTokens > 0 {
				cacheReadTokens = &resp.Usage.CacheReadTokens
			}
			if resp.Usage.CacheCreationTokens > 0 {
				cacheCreationTokens = &resp.Usage.CacheCreationTokens
			}
		}
		textLen = len(resp.Text)
	}

	// Build inline conversation: input messages + assistant response.
	conversation := make([]map[string]string, 0, len(inputMessages)+1)
	for _, msg := range inputMessages {
		conversation = append(conversation, map[string]string{
			"role":    string(msg.Role),
			"content": msg.Content,
		})
	}
	conversation = append(conversation, map[string]string{
		"role":    string(agent.RoleAssistant),
		"content": assistantText,
	})

	interaction, err := execCtx.Services.Interaction.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         &execCtx.StageID,
		ExecutionID:     &execCtx.ExecutionID,
		InteractionType: string(llminteraction.InteractionTypeSummarization),
		ModelName:       modelName,
		LLMRequest: map[string]any{
			"messages_count": len(inputMessages),
			"iteration":      0,
			"conversation":   conversation,
		},
		LLMResponse: map[string]any{
			"text_length":      textLen,
			"tool_calls_count": 0,
		},
		InputTokens:         inputTokens,
		OutputTokens:        outputTokens,
		TotalTokens:         totalTokens,
		ThinkingTokens:      thinkingTokens,
		CacheReadTokens:     cacheReadTokens,
		CacheCreationTokens: cacheCreationTokens,
		DurationMs:          &durationMs,
	})
	if err != nil {
		slog.Error("Failed to record summarization LLM interaction",
			"session_id", execCtx.SessionID, "error", err)
		return
	}

	// Publish interaction.created event for trace view live updates.
	publishInteractionCreated(ctx, execCtx, interaction.ID, events.InteractionTypeLLM)
}

// buildConversationContext formats the current conversation for summarization context.
// Includes assistant thoughts and observations (not system prompt) to give the
// summarizer investigation context.
func buildConversationContext(messages []agent.ConversationMessage) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Role == agent.RoleSystem {
			continue // Skip system prompt (too long, not needed for context)
		}
		sb.WriteByte('[')
		sb.WriteString(string(msg.Role))
		sb.WriteString("]: ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
