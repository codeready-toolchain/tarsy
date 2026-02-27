package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
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
	if serverConfig.Summarization != nil && !serverConfig.Summarization.Enabled {
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
	summary, usage, err := callSummarizationLLM(ctx, execCtx, systemPrompt, userPrompt, serverID, toolName, estimatedTokens, eventSeq)
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

// callSummarizationLLM performs the summarization LLM call with streaming.
// Creates an mcp_tool_summary timeline event and streams chunks to WebSocket clients.
// Records an LLMInteraction with type "summarization".
func callSummarizationLLM(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	systemPrompt, userPrompt string,
	serverID, toolName string,
	estimatedTokens int,
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
		Backend:     execCtx.Config.LLMBackend,
	}

	// Use dedicated summarization streaming (creates mcp_tool_summary events, not llm_response)
	streamed, err := callSummarizationLLMWithStreaming(ctx, execCtx, input, serverID, toolName, estimatedTokens, eventSeq)
	if err != nil {
		return "", nil, fmt.Errorf("summarization LLM call failed: %w", err)
	}

	summary := strings.TrimSpace(streamed.Text)
	if summary == "" {
		return "", nil, fmt.Errorf("summarization produced empty result")
	}

	// Record LLM interaction with inline conversation for observability.
	// Summarization has its own self-contained conversation (system + user + assistant)
	// that is separate from the iteration's message sequence, so we store it
	// inline in llm_request rather than in the Message table.
	recordSummarizationInteraction(ctx, execCtx, messages, summary,
		streamed.LLMResponse, startTime)

	return summary, streamed.Usage, nil
}

// callSummarizationLLMWithStreaming is analogous to callLLMWithStreaming but
// creates mcp_tool_summary timeline events instead of llm_response events.
// The streaming pattern is identical (create event -> stream chunks -> finalize).
// Simpler than callLLMWithStreaming: no thinking event (summarization has no thinking stream).
func callSummarizationLLMWithStreaming(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	input *agent.GenerateInput,
	serverID, toolName string,
	estimatedTokens int,
	eventSeq *int,
) (*StreamedResponse, error) {
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	stream, err := execCtx.LLMClient.Generate(llmCtx, input)
	if err != nil {
		return nil, fmt.Errorf("summarization LLM Generate failed: %w", err)
	}

	// If no EventPublisher, use simple collection (no streaming events)
	if execCtx.EventPublisher == nil {
		resp, err := collectStream(stream)
		if err != nil {
			return nil, err
		}
		return &StreamedResponse{LLMResponse: resp}, nil
	}

	// Track streaming timeline event
	var summaryEventID string
	var eventCreateFailed bool
	pid := parentExecID(execCtx)

	metadata := map[string]interface{}{
		"server_name":     serverID,
		"tool_name":       toolName,
		"original_tokens": estimatedTokens,
	}
	if execCtx.Config.LLMProvider != nil {
		metadata["summarization_model"] = execCtx.Config.LLMProvider.Model
	}

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

	resp, err := collectStreamWithCallback(stream, callback)
	if err != nil {
		// Mark streaming event as failed if it was created
		if summaryEventID != "" {
			failContent := fmt.Sprintf("Summarization streaming failed: %s", err.Error())
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
		return nil, err
	}

	// Finalize summary event
	if summaryEventID != "" {
		finalizeStreamingEvent(ctx, execCtx, summaryEventID, timelineevent.EventTypeMcpToolSummary, resp.Text, "summary")
	}

	return &StreamedResponse{
		LLMResponse:      resp,
		TextEventCreated: summaryEventID != "",
	}, nil
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
) {
	durationMs := int(time.Since(startTime).Milliseconds())

	var inputTokens, outputTokens, totalTokens *int
	var textLen int

	if resp != nil {
		if resp.Usage != nil {
			inputTokens = &resp.Usage.InputTokens
			outputTokens = &resp.Usage.OutputTokens
			totalTokens = &resp.Usage.TotalTokens
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
		InteractionType: "summarization",
		ModelName:       execCtx.Config.LLMProvider.Model,
		LLMRequest: map[string]any{
			"messages_count": len(inputMessages),
			"iteration":      0,
			"conversation":   conversation,
		},
		LLMResponse: map[string]any{
			"text_length":      textLen,
			"tool_calls_count": 0,
		},
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  totalTokens,
		DurationMs:   &durationMs,
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
