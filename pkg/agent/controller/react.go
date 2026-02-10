package controller

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// ReActController implements the standard Reason + Act loop with text-based
// tool calling. This is the primary investigation strategy and supports all
// LLM providers via LangChain.
type ReActController struct{}

// NewReActController creates a new ReAct controller.
func NewReActController() *ReActController {
	return &ReActController{}
}

// Run executes the ReAct iteration loop.
func (c *ReActController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	maxIter := execCtx.Config.MaxIterations
	totalUsage := agent.TokenUsage{}
	state := &agent.IterationState{MaxIterations: maxIter}
	msgSeq := 0
	eventSeq := 0

	// 1. Get available tools (needed for prompt and validation)
	tools, err := execCtx.ToolExecutor.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// 2. Build initial conversation via prompt builder
	if execCtx.PromptBuilder == nil {
		return nil, fmt.Errorf("PromptBuilder is nil: cannot call BuildReActMessages")
	}
	messages := execCtx.PromptBuilder.BuildReActMessages(execCtx, prevStageContext, tools)

	// 3. Store initial messages in DB
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 4. Build tool name set for validation
	toolNames := buildToolNameSet(tools)

	// Main iteration loop
	for iteration := 0; iteration < maxIter; iteration++ {
		state.CurrentIteration = iteration + 1

		// Check consecutive timeout threshold
		if state.ShouldAbortOnTimeouts() {
			return failedResult(state, totalUsage), nil
		}

		// Per-iteration timeout
		iterCtx, iterCancel := context.WithTimeout(ctx, execCtx.Config.IterationTimeout)

		startTime := time.Now()

		// Call LLM with streaming (text only — tools described in system prompt, not bound)
		streamed, err := callLLMWithStreaming(iterCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       nil, // ReAct uses text-based tool calling
		}, &eventSeq)

		if err != nil {
			iterCancel()
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
			state.RecordFailure(err.Error(), isTimeoutError(err))
			observation := FormatErrorObservation(err)
			messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
			storeObservationMessage(ctx, execCtx, observation, &msgSeq)
			continue
		}
		resp := streamed.LLMResponse

		// Record LLM interaction
		accumulateUsage(&totalUsage, resp)
		assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
		if storeErr != nil {
			iterCancel()
			return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
		}
		recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

		// Append assistant response to conversation
		messages = append(messages, agent.ConversationMessage{
			Role:    agent.RoleAssistant,
			Content: resp.Text,
		})

		// Parse ReAct response
		parsed := ParseReActResponse(resp.Text)
		state.RecordSuccess()

		// Log warning if native tool data appears — this indicates stub delegation
		// or a configuration issue. Native tool events are only created by controllers
		// that use the google-native backend (NativeThinking, Synthesis).
		// Data is still available in LLMInteraction.response_metadata for debugging.
		if len(resp.CodeExecutions) > 0 || len(resp.Groundings) > 0 {
			slog.Warn("native tool data present in ReAct response (not creating timeline events)",
				"session_id", execCtx.SessionID,
				"execution_id", execCtx.ExecutionID,
				"code_executions", len(resp.CodeExecutions),
				"groundings", len(resp.Groundings),
			)
		}

		// Create timeline event for thinking content
		if parsed.Thought != "" {
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, parsed.Thought, map[string]interface{}{
				"source": "react",
			}, &eventSeq)
		}

		switch {
		case parsed.IsFinalAnswer:
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, parsed.FinalAnswer, nil, &eventSeq)
			iterCancel()
			return &agent.ExecutionResult{
				Status:        agent.ExecutionStatusCompleted,
				FinalAnalysis: parsed.FinalAnswer,
				TokensUsed:    totalUsage,
			}, nil

		case parsed.HasAction && !parsed.IsUnknownTool:
			// Valid tool call — check against available tools
			if !toolNames[parsed.Action] {
				// Tool exists in ReAct format but not in our tool list
				observation := FormatUnknownToolError(parsed.Action,
					fmt.Sprintf("Unknown tool '%s'", parsed.Action), tools)
				messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
				storeObservationMessage(ctx, execCtx, observation, &msgSeq)
			} else {
				// Execute tool
				createToolCallEvent(ctx, execCtx, parsed.Action, parsed.ActionInput, &eventSeq)

				result, toolErr := execCtx.ToolExecutor.Execute(iterCtx, agent.ToolCall{
					ID:        generateCallID(),
					Name:      parsed.Action,
					Arguments: parsed.ActionInput,
				})

				if toolErr != nil {
					state.RecordFailure(toolErr.Error(), isTimeoutError(toolErr))
					observation := FormatToolErrorObservation(toolErr)
					createToolResultEvent(ctx, execCtx, observation, true, &eventSeq)
					messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
					storeObservationMessage(ctx, execCtx, observation, &msgSeq)
				} else {
					observation := FormatObservation(result)
					createToolResultEvent(ctx, execCtx, result.Content, result.IsError, &eventSeq)
					messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
					storeObservationMessage(ctx, execCtx, observation, &msgSeq)
				}
			}

		case parsed.IsUnknownTool:
			observation := FormatUnknownToolError(parsed.Action, parsed.ErrorMessage, tools)
			messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: observation})
			storeObservationMessage(ctx, execCtx, observation, &msgSeq)

		default:
			// Malformed response — keep it, add format feedback
			feedback := GetFormatErrorFeedback(parsed)
			messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: feedback})
			storeObservationMessage(ctx, execCtx, feedback, &msgSeq)
		}

		iterCancel()
	}

	// Max iterations reached — force conclusion
	return c.forceConclusion(ctx, execCtx, messages, &totalUsage, state, &msgSeq, &eventSeq)
}

// forceConclusion forces the LLM to produce a final answer.
func (c *ReActController) forceConclusion(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	totalUsage *agent.TokenUsage,
	state *agent.IterationState,
	msgSeq *int,
	eventSeq *int,
) (*agent.ExecutionResult, error) {
	// If the last interaction failed, return failed status
	if state.LastInteractionFailed {
		return &agent.ExecutionResult{
			Status: agent.ExecutionStatusFailed,
			Error: fmt.Errorf("max iterations (%d) reached with last interaction failed: %s",
				state.MaxIterations, state.LastErrorMessage),
			TokensUsed: *totalUsage,
		}, nil
	}

	// Append forced conclusion prompt and make one more LLM call
	conclusionPrompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(state.CurrentIteration, config.IterationStrategyReact)
	messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: conclusionPrompt})
	storeObservationMessage(ctx, execCtx, conclusionPrompt, msgSeq)

	startTime := time.Now()

	// Apply same iteration timeout as the main loop to prevent indefinite hangs
	conclusionCtx, conclusionCancel := context.WithTimeout(ctx, execCtx.Config.IterationTimeout)
	defer conclusionCancel()

	streamed, err := callLLMWithStreaming(conclusionCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil,
	}, eventSeq)
	if err != nil {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, eventSeq)
		return &agent.ExecutionResult{
			Status:     agent.ExecutionStatusFailed,
			Error:      fmt.Errorf("forced conclusion LLM call failed: %w", err),
			TokensUsed: *totalUsage,
		}, nil
	}
	resp := streamed.LLMResponse

	accumulateUsage(totalUsage, resp)
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
	recordLLMInteraction(ctx, execCtx, state.CurrentIteration+1, "forced_conclusion", len(messages), resp, &assistantMsg.ID, startTime)

	// Parse forced conclusion — may or may not have ReAct format
	parsed := ParseReActResponse(resp.Text)
	if parsed.Thought != "" {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, parsed.Thought, map[string]interface{}{
			"source": "react",
		}, eventSeq)
	}

	finalAnswer := ExtractForcedConclusionAnswer(parsed)
	if finalAnswer == "" {
		// If the parser couldn't extract anything, use the raw text
		finalAnswer = resp.Text
	}
	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, finalAnswer, nil, eventSeq)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: finalAnswer,
		TokensUsed:    *totalUsage,
	}, nil
}
