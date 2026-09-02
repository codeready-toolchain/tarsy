package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
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
	if term := contextTerminalResult(ctx, totalUsage, interruptExecution); term != nil {
		return term, nil
	}
	messages := execCtx.PromptBuilder.BuildFunctionCallingMessages(execCtx, prevStageContext)

	// 2. Store initial messages in DB
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 2.5. Emit skill_loaded timeline events for required skills (visible in UI + scoring context)
	for _, skill := range execCtx.Config.RequiredSkillContent {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeSkillLoaded,
			skill.Body,
			map[string]interface{}{"skill_name": skill.Name},
			&eventSeq,
		)
	}

	// 2.6. Emit a single memory_injected event for all pre-loaded memories
	emitMemoryInjectedEvent(ctx, execCtx, &eventSeq)

	if term := contextTerminalResult(ctx, totalUsage, interruptExecution); term != nil {
		return term, nil
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

		phase := events.ProgressPhaseInvestigating
		if execCtx.StageType == string(stage.StageTypeAction) {
			phase = events.ProgressPhaseRemediating
		}
		publishExecutionProgress(ctx, execCtx, phase,
			fmt.Sprintf("Iteration %d/%d", iteration+1, maxIter))

		if term := contextTerminalResult(ctx, totalUsage, interruptExecution); term != nil {
			return term, nil
		}

		remaining, hasDeadline := remainingTime(ctx)
		reserve := wrapUpReserve(execCtx.Config.LLMCallTimeout)

		// Drain any sub-agent results that arrived while tools were executing
		// or the LLM was being called. Non-blocking — skipped when nil.
		messages = drainSubAgentResults(ctx, execCtx, messages, &msgSeq)

		if hasDeadline && remaining <= reserve {
			return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state, fbState, &msgSeq, &eventSeq, agent.WrapUpReasonTimeBudget)
		}

		if state.ShouldAbortOnTimeouts() {
			if hasDeadline && shouldWrapUpOnTimeouts(remaining, reserve, execCtx.Config.IterationTimeout) {
				return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state, fbState, &msgSeq, &eventSeq, agent.WrapUpReasonTimeBudget)
			}
			return failedResult(state, totalUsage), nil
		}

		iterTimeout := execCtx.Config.IterationTimeout
		if hasDeadline {
			iterTimeout = min(iterTimeout, remaining-reserve)
		}
		iterCtx, iterCancel := context.WithTimeout(ctx, iterTimeout)
		startTime := time.Now()

		// Call LLM WITH tools and streaming (native function calling).
		// LLM call gets its own sub-timeout within the iteration budget.
		llmCtx, llmCancel := context.WithTimeout(iterCtx, execCtx.Config.LLMCallTimeout)
		llmStart := time.Now()
		streamed, err := callLLMWithStreaming(llmCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       tools, // Tools bound for native calling
			Backend:     execCtx.Config.LLMBackend,
			ClearCache:  fbState.consumeClearCache(),
			PromptCache: execCtx.Config.Type != config.AgentTypeAction,
		}, &eventSeq)
		llmCancel()
		metrics.ObserveLLMCall(execCtx.Config.LLMProviderName, execCtx.Config.LLMProvider.Model,
			time.Since(llmStart), metricsTokens(streamed, err), err)

		if err != nil {
			iterCancel()

			// If the parent context is cancelled/expired, return immediately
			// instead of burning through retry iterations with the same error.
			if term := contextTerminalResult(ctx, totalUsage, interruptExecution); term != nil {
				return term, nil
			}

			// Try fallback to a different provider before exhausting retries
			if tryFallback(ctx, execCtx, fbState, err, &eventSeq) {
				continue
			}

			var poe *PartialOutputError
			isRecoverablePartial := errors.As(err, &poe) && !poe.IsLoop && poe.PartialText != ""
			if isRecoverablePartial {
				// Mid-stream failure after partial output: continue from the
				// truncated text without marking this iteration as a hard failure.
			} else {
				createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
				state.RecordFailure(err.Error(), isTimeoutError(err))
			}

			if errMsg := buildRetryMessage(err); errMsg != "" {
				messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: errMsg})
				storeObservationMessage(ctx, execCtx, errMsg, &msgSeq)
			}
			continue
		}
		resp := streamed.LLMResponse

		accumulateUsage(&totalUsage, resp)
		state.RecordSuccess()
		fbState.resetCounters()

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
			emptyRetries = 0
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
				tcResult := executeToolCall(iterCtx, execCtx, tc, messages, resp.Groundings, &eventSeq)

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
				emptyRetries = 0
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

				budget, hasBudget := remainingWorkBudget(ctx, execCtx.Config)
				if hasBudget && budget <= 0 {
					iterCancel()
					messages = drainSubAgentResults(ctx, execCtx, messages, &msgSeq)
					return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state, fbState, &msgSeq, &eventSeq, agent.WrapUpReasonTimeBudget)
				}
				msg, waitErr := waitForSubAgentResult(ctx, execCtx.SubAgentCollector, budget, hasBudget)
				if waitErr != nil {
					iterCancel()
					wrap, term := waitErrorAction(ctx, waitErr, totalUsage)
					if wrap {
						messages = drainSubAgentResults(ctx, execCtx, messages, &msgSeq)
						return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state, fbState, &msgSeq, &eventSeq, agent.WrapUpReasonTimeBudget)
					}
					if term != nil {
						return term, nil
					}
					return &agent.ExecutionResult{
						Status:     agent.StatusFromErr(waitErr),
						Error:      fmt.Errorf("%s: %w", interruptSubAgentWait, waitErr),
						TokensUsed: totalUsage,
					}, nil
				}
				messages = append(messages, msg)
				storeObservationMessage(ctx, execCtx, msg.Content, &msgSeq)
				iterCancel()
				continue
			}

			// Empty response retry: if the LLM returned no text, nudge it to
			// respond before accepting a blank final answer. Skip when the
			// context is done - empty streams from cancellation are expected.
			if strings.TrimSpace(resp.Text) == "" && emptyRetries < maxEmptyResponseRetries && ctx.Err() == nil {
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

	// Max iterations — force conclusion (same tools, calling disabled)
	return c.forceConclusion(ctx, execCtx, messages, tools, &totalUsage, state, fbState, &msgSeq, &eventSeq, agent.WrapUpReasonMaxIterations)
}

// forceConclusion forces a text-only final answer while keeping the looping tool
// list bound so the prompt-cache prefix can still hit.
func (c *IteratingController) forceConclusion(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	tools []agent.ToolDefinition,
	totalUsage *agent.TokenUsage,
	state *agent.IterationState,
	fbState *FallbackState,
	msgSeq *int,
	eventSeq *int,
	reason agent.WrapUpReason,
) (*agent.ExecutionResult, error) {
	progressMsg := fmt.Sprintf("Forcing conclusion after %d iterations", state.CurrentIteration)
	if reason == agent.WrapUpReasonTimeBudget {
		progressMsg = fmt.Sprintf("Forcing conclusion (time budget) after %d iterations", state.CurrentIteration)
	}
	publishExecutionProgress(ctx, execCtx, events.ProgressPhaseConcluding, progressMsg)

	conclusionPrompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(state.CurrentIteration, reason)
	messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: conclusionPrompt})
	storeObservationMessage(ctx, execCtx, conclusionPrompt, msgSeq)

	startTime := time.Now()

	forcedMeta := map[string]any{
		"forced_conclusion": true,
		"iterations_used":   state.CurrentIteration,
		"max_iterations":    state.MaxIterations,
		"reason":            string(reason),
	}

	var streamed *StreamedResponse
	var err error
	emptyRetries := 0
	for {
		if term := contextTerminalResult(ctx, *totalUsage, interruptForcedConclusion); term != nil {
			return term, nil
		}
		llmCtx, llmCancel := context.WithTimeout(ctx, execCtx.Config.LLMCallTimeout)
		llmStart := time.Now()
		streamed, err = callLLMWithStreaming(llmCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:        execCtx.SessionID,
			ExecutionID:      execCtx.ExecutionID,
			Messages:         messages,
			Config:           execCtx.Config.LLMProvider,
			Tools:            tools,
			DisableToolCalls: true,
			Backend:          execCtx.Config.LLMBackend,
			ClearCache:       fbState.consumeClearCache(),
			PromptCache:      execCtx.Config.Type != config.AgentTypeAction,
		}, eventSeq, forcedMeta)
		llmCancel()
		metrics.ObserveLLMCall(execCtx.Config.LLMProviderName, execCtx.Config.LLMProvider.Model,
			time.Since(llmStart), metricsTokens(streamed, err), err)
		if err == nil {
			accumulateUsage(totalUsage, streamed.LLMResponse)
			if strings.TrimSpace(streamed.LLMResponse.Text) != "" {
				break
			}
			if term := contextTerminalResult(ctx, *totalUsage, interruptForcedConclusion); term != nil {
				return term, nil
			}
			if emptyRetries >= maxEmptyResponseRetries {
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
		if term := contextTerminalResult(ctx, *totalUsage, interruptForcedConclusion); term != nil {
			return term, nil
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
			mergeMetadata(map[string]any{"source": "native"}, forcedMeta), eventSeq)
	}

	createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, eventSeq)
	createGroundingEvents(ctx, execCtx, resp.Groundings, eventSeq)

	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, resp.Text, forcedMeta, eventSeq)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: resp.Text,
		TokensUsed:    *totalUsage,
		WrapUpReason:  reason,
	}, nil
}

func drainSubAgentResults(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	msgSeq *int,
) []agent.ConversationMessage {
	collector := execCtx.SubAgentCollector
	if collector == nil {
		return messages
	}
	for {
		msg, ok := collector.TryDrainResult()
		if !ok {
			break
		}
		messages = append(messages, msg)
		storeObservationMessage(ctx, execCtx, msg.Content, msgSeq)
	}
	return messages
}

func remainingWorkBudget(ctx context.Context, cfg *agent.ResolvedAgentConfig) (time.Duration, bool) {
	remaining, ok := remainingTime(ctx)
	if !ok {
		return 0, false
	}
	return remaining - wrapUpReserve(cfg.LLMCallTimeout), true
}

func waitForSubAgentResult(
	ctx context.Context,
	collector agent.SubAgentResultCollector,
	budget time.Duration,
	hasBudget bool,
) (agent.ConversationMessage, error) {
	waitCtx := ctx
	waitCancel := func() {}
	if hasBudget {
		waitCtx, waitCancel = context.WithTimeout(ctx, budget)
	}
	defer waitCancel()
	return collector.WaitForResult(waitCtx)
}

// waitErrorAction classifies a WaitForResult error.
// wrap=true means the wait clamp fired while the parent still has time.
func waitErrorAction(ctx context.Context, waitErr error, usage agent.TokenUsage) (wrap bool, term *agent.ExecutionResult) {
	if term := contextTerminalResult(ctx, usage, interruptSubAgentWait); term != nil {
		return false, term
	}
	remaining, hasDeadline := remainingTime(ctx)
	if hasDeadline && remaining > 0 && errors.Is(waitErr, context.DeadlineExceeded) {
		return true, nil
	}
	return false, nil
}

// buildRetryMessage crafts an error context message for the LLM based on the
// error type. Returns empty when the model should retry with unchanged messages
// (no-partial provider errors stay off the prompt; operators still see EventTypeError).
func buildRetryMessage(err error) string {
	var poe *PartialOutputError
	if !errors.As(err, &poe) {
		return ""
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
			"Your partial response before the error:\n---\n%s\n---\n\n"+
				"Please continue from where you left off or provide a complete response.",
			partial,
		)
	}

	return ""
}
