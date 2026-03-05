package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/events"
)

// maxEmptyResponseRetries is the number of times to retry when the LLM
// returns an empty text response with no tool calls before accepting it.
const maxEmptyResponseRetries = 2

// IteratingController implements the multi-turn tool-calling loop.
// Used by both google-native (Google SDK) and langchain (multi-provider) backends.
// Tool calls come as structured ToolCallChunk values (not parsed from text).
// Completion signal: a response without any ToolCalls.
type IteratingController struct{}

// NewIteratingController creates a new iterating controller.
func NewIteratingController() *IteratingController {
	return &IteratingController{}
}

// Run executes the native thinking iteration loop.
func (c *IteratingController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	maxIter := execCtx.Config.MaxIterations
	totalUsage := agent.TokenUsage{}
	state := &agent.IterationState{MaxIterations: maxIter}
	fbState := NewFallbackState(execCtx)
	msgSeq := 0
	emptyRetries := 0

	// Initialize eventSeq from DB to avoid collisions with events created
	// before this loop starts (e.g., task_assigned from orchestrator dispatch).
	eventSeq, seqErr := execCtx.Services.Timeline.GetMaxSequenceForExecution(ctx, execCtx.ExecutionID)
	if seqErr != nil {
		slog.Warn("Failed to get max sequence for execution, starting from 0",
			"execution_id", execCtx.ExecutionID, "error", seqErr)
	}

	// 1. Build initial conversation via prompt builder
	if execCtx.PromptBuilder == nil {
		return nil, fmt.Errorf("PromptBuilder is nil: cannot call BuildFunctionCallingMessages")
	}
	messages := execCtx.PromptBuilder.BuildFunctionCallingMessages(execCtx, prevStageContext)

	// 2. Store initial messages in DB
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 3. Get available tools
	tools, err := execCtx.ToolExecutor.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Tool names stay in canonical "server.tool" format.
	// The LLM service handles backend-specific encoding (e.g. "server__tool" for Gemini).

	// Record tool_list interactions for the trace view (one per MCP server).
	recordToolListInteractions(ctx, execCtx, tools)

	// Main iteration loop
	for iteration := 0; iteration < maxIter; iteration++ {
		state.CurrentIteration = iteration + 1

		// Publish execution progress: investigating
		publishExecutionProgress(ctx, execCtx, events.ProgressPhaseInvestigating,
			fmt.Sprintf("Iteration %d/%d", iteration+1, maxIter))

		if state.ShouldAbortOnTimeouts() {
			return failedResult(state, totalUsage), nil
		}

		// Drain any sub-agent results that arrived while tools were executing
		// or the LLM was being called. Non-blocking — skipped when nil.
		if collector := execCtx.SubAgentCollector; collector != nil {
			for {
				msg, ok := collector.TryDrainResult()
				if !ok {
					break
				}
				messages = append(messages, msg)
				storeObservationMessage(ctx, execCtx, msg.Content, &msgSeq)
			}
		}

		iterCtx, iterCancel := context.WithTimeout(ctx, execCtx.Config.IterationTimeout)
		startTime := time.Now()

		// Call LLM WITH tools and streaming (native function calling).
		// LLM call gets its own sub-timeout within the iteration budget.
		llmCtx, llmCancel := context.WithTimeout(iterCtx, execCtx.Config.LLMCallTimeout)
		streamed, err := callLLMWithStreaming(llmCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       tools, // Tools bound for native calling
			Backend:     execCtx.Config.LLMBackend,
			ClearCache:  fbState.consumeClearCache(),
		}, &eventSeq)
		llmCancel()

		if err != nil {
			iterCancel()

			// If the parent context is cancelled/expired, return immediately
			// instead of burning through retry iterations with the same error.
			if status, done := agent.StatusFromContextErr(ctx); done {
				return &agent.ExecutionResult{
					Status:     status,
					Error:      fmt.Errorf("execution interrupted: %w", err),
					TokensUsed: totalUsage,
				}, nil
			}

			// Try fallback to a different provider before exhausting retries
			if tryFallback(ctx, execCtx, fbState, err, &eventSeq) {
				continue
			}

			var poe *PartialOutputError
			isRecoverablePartial := errors.As(err, &poe) && !poe.IsLoop
			if isRecoverablePartial {
				// Google can fail mid-stream after emitting partial output.
				// Treat as recoverable: continue with retry context without
				// marking this iteration as a hard failure.
			} else {
				createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
				state.RecordFailure(err.Error(), isTimeoutError(err))
			}

			// Build retry message based on error type
			errMsg := buildRetryMessage(err)
			messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: errMsg})
			storeObservationMessage(ctx, execCtx, errMsg, &msgSeq)
			continue
		}
		resp := streamed.LLMResponse

		accumulateUsage(&totalUsage, resp)
		state.RecordSuccess()

		// Record thinking content (only if not already created by streaming)
		if !streamed.ThinkingEventCreated && resp.ThinkingText != "" {
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, resp.ThinkingText, map[string]interface{}{
				"source": "native",
			}, &eventSeq)
		}

		// Create native tool events (code execution, grounding)
		createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
		createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

		// Check for tool calls in response
		if len(resp.ToolCalls) > 0 {
			// Record text alongside tool calls (only if not already created by streaming)
			if !streamed.TextEventCreated && resp.Text != "" {
				createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmResponse, resp.Text, nil, &eventSeq)
			}

			// Store assistant message WITH tool calls
			assistantMsg, storeErr := storeAssistantMessageWithToolCalls(ctx, execCtx, resp, &msgSeq)
			if storeErr != nil {
				iterCancel()
				return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
			}
			recordLLMInteraction(ctx, execCtx, iteration+1, llminteraction.InteractionTypeIteration, len(messages), resp, &assistantMsg.ID, startTime)

			// Append assistant message to conversation
			messages = append(messages, agent.ConversationMessage{
				Role:      agent.RoleAssistant,
				Content:   resp.Text,
				ToolCalls: resp.ToolCalls,
			})

			// Execute each tool call and append results
			for _, tc := range resp.ToolCalls {
				tcResult := executeToolCall(iterCtx, execCtx, tc, messages, &eventSeq)

				if tcResult.IsError {
					state.RecordFailure(tcResult.Content, isTimeoutError(tcResult.Err))
				}
				accumulateTokenUsage(&totalUsage, tcResult.Usage)

				messages = append(messages, agent.ConversationMessage{
					Role:       agent.RoleTool,
					Content:    tcResult.Content,
					ToolCallID: tc.ID,
					ToolName:   tc.Name,
				})
				storeToolResultMessage(ctx, execCtx, tc.ID, tc.Name, tcResult.Content, &msgSeq)
			}
		} else {
			// No tool calls — check for pending sub-agents before treating as final
			if collector := execCtx.SubAgentCollector; collector != nil && collector.HasPending() {
				// Persist the assistant's intermediate response before waiting
				assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
				if storeErr != nil {
					iterCancel()
					return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
				}
				recordLLMInteraction(ctx, execCtx, iteration+1, llminteraction.InteractionTypeIteration, len(messages), resp, &assistantMsg.ID, startTime)

				if resp.Text != "" {
					messages = append(messages, agent.ConversationMessage{
						Role:    agent.RoleAssistant,
						Content: resp.Text,
					})
				}

				msg, waitErr := collector.WaitForResult(ctx)
				if waitErr != nil {
					iterCancel()
					return &agent.ExecutionResult{
						Status:     agent.StatusFromErr(waitErr),
						Error:      fmt.Errorf("sub-agent wait interrupted: %w", waitErr),
						TokensUsed: totalUsage,
					}, nil
				}
				messages = append(messages, msg)
				storeObservationMessage(ctx, execCtx, msg.Content, &msgSeq)
				iterCancel()
				continue
			}

			// Empty response retry: if the LLM returned no text, nudge it to
			// respond before accepting a blank final answer.
			if resp.Text == "" && emptyRetries < maxEmptyResponseRetries {
				emptyRetries++
				slog.Warn("LLM returned empty response, retrying",
					"session_id", execCtx.SessionID, "attempt", emptyRetries,
					"max_attempts", maxEmptyResponseRetries)
				retryMsg := "Your previous response was empty. Please provide a response."
				messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: retryMsg})
				storeObservationMessage(ctx, execCtx, retryMsg, &msgSeq)
				iterCancel()
				continue
			}

			// No tool calls, no pending sub-agents — this is the final answer
			assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
			if storeErr != nil {
				iterCancel()
				return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
			}
			recordLLMInteraction(ctx, execCtx, iteration+1, llminteraction.InteractionTypeIteration, len(messages), resp, &assistantMsg.ID, startTime)

			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, resp.Text, nil, &eventSeq)

			iterCancel()
			return &agent.ExecutionResult{
				Status:        agent.ExecutionStatusCompleted,
				FinalAnalysis: resp.Text,
				TokensUsed:    totalUsage,
			}, nil
		}

		iterCancel()
	}

	// Max iterations — force conclusion (call LLM WITHOUT tools)
	return c.forceConclusion(ctx, execCtx, messages, &totalUsage, state, fbState, &msgSeq, &eventSeq)
}

// forceConclusion forces the LLM to produce a final answer by calling without tools.
func (c *IteratingController) forceConclusion(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	totalUsage *agent.TokenUsage,
	state *agent.IterationState,
	fbState *FallbackState,
	msgSeq *int,
	eventSeq *int,
) (*agent.ExecutionResult, error) {
	// Publish execution progress: concluding
	publishExecutionProgress(ctx, execCtx, events.ProgressPhaseConcluding,
		fmt.Sprintf("Forcing conclusion after %d iterations", state.CurrentIteration))

	// Append forced conclusion prompt
	conclusionPrompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(state.CurrentIteration)
	messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: conclusionPrompt})
	storeObservationMessage(ctx, execCtx, conclusionPrompt, msgSeq)

	startTime := time.Now()

	// Metadata for forced conclusion — carried by all streaming events + final_analysis.
	forcedMeta := map[string]interface{}{
		"forced_conclusion": true,
		"iterations_used":   state.CurrentIteration,
		"max_iterations":    state.MaxIterations,
	}

	// Call LLM WITHOUT tools with streaming — forces text-only response.
	// Apply LLM call timeout (the parent ctx is the session context here).
	// On failure, attempt fallback to another provider before giving up.
	var streamed *StreamedResponse
	var err error
	emptyRetries := 0
	for {
		if status, done := agent.StatusFromContextErr(ctx); done {
			return &agent.ExecutionResult{
				Status:     status,
				Error:      fmt.Errorf("forced conclusion interrupted: %w", ctx.Err()),
				TokensUsed: *totalUsage,
			}, nil
		}
		llmCtx, llmCancel := context.WithTimeout(ctx, execCtx.Config.LLMCallTimeout)
		streamed, err = callLLMWithStreaming(llmCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       nil, // No tools — force conclusion
			Backend:     execCtx.Config.LLMBackend,
			ClearCache:  fbState.consumeClearCache(),
		}, eventSeq, forcedMeta)
		llmCancel()
		if err == nil {
			accumulateUsage(totalUsage, streamed.LLMResponse)
			if streamed.LLMResponse.Text != "" || emptyRetries >= maxEmptyResponseRetries {
				break
			}
			emptyRetries++
			slog.Warn("LLM returned empty response during forced conclusion, retrying",
				"session_id", execCtx.SessionID, "attempt", emptyRetries,
				"max_attempts", maxEmptyResponseRetries)
			retryMsg := "Your previous response was empty. Please provide a response."
			messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: retryMsg})
			storeObservationMessage(ctx, execCtx, retryMsg, msgSeq)
			startTime = time.Now()
			continue
		}
		if !tryFallback(ctx, execCtx, fbState, err, eventSeq) {
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, eventSeq)
			return &agent.ExecutionResult{
				Status:     agent.ExecutionStatusFailed,
				Error:      fmt.Errorf("forced conclusion LLM call failed: %w", err),
				TokensUsed: *totalUsage,
			}, nil
		}
		startTime = time.Now()
	}
	resp := streamed.LLMResponse

	assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, msgSeq)
	if storeErr != nil {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError,
			fmt.Sprintf("failed to store forced conclusion message: %v", storeErr), nil, eventSeq)
		return &agent.ExecutionResult{
			Status:     agent.ExecutionStatusFailed,
			Error:      fmt.Errorf("failed to store forced conclusion message: %w", storeErr),
			TokensUsed: *totalUsage,
		}, nil
	}
	recordLLMInteraction(ctx, execCtx, state.CurrentIteration+1, llminteraction.InteractionTypeForcedConclusion, len(messages), resp, &assistantMsg.ID, startTime)

	if !streamed.ThinkingEventCreated && resp.ThinkingText != "" {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, resp.ThinkingText,
			mergeMetadata(map[string]interface{}{"source": "native"}, forcedMeta), eventSeq)
	}

	// Create native tool events (can occur during forced conclusion too)
	createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, eventSeq)
	createGroundingEvents(ctx, execCtx, resp.Groundings, eventSeq)

	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, resp.Text, forcedMeta, eventSeq)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: resp.Text,
		TokensUsed:    *totalUsage,
	}, nil
}

// buildRetryMessage crafts an error context message for the LLM based on the
// error type. For loop errors it instructs directness; for partial stream
// errors it includes the partial output for continuity.
func buildRetryMessage(err error) string {
	var poe *PartialOutputError
	if !errors.As(err, &poe) {
		return fmt.Sprintf("Error from previous attempt: %s. Please try again.", err.Error())
	}

	if poe.IsLoop {
		return "Your previous response got stuck in a repetitive output loop and was cancelled. " +
			"Please provide a direct, concise response. Do not deliberate excessively."
	}

	if poe.PartialText != "" {
		partial := poe.PartialText
		const maxPartialLen = 2000
		if len(partial) > maxPartialLen {
			partial = partial[:maxPartialLen] + "..."
		}
		return fmt.Sprintf(
			"Error from previous attempt: %s\n\nYour partial response before the error:\n---\n%s\n---\n\n"+
				"Please continue from where you left off or provide a complete response.",
			poe.Cause.Error(), partial,
		)
	}

	return fmt.Sprintf("Error from previous attempt: %s. Please try again.", err.Error())
}
