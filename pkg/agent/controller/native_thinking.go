package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// NativeThinkingController implements the Gemini native function calling loop.
// Tool calls come as structured ToolCallChunk values (not parsed from text).
// Completion signal: a response without any ToolCalls.
type NativeThinkingController struct{}

// NewNativeThinkingController creates a new native thinking controller.
func NewNativeThinkingController() *NativeThinkingController {
	return &NativeThinkingController{}
}

// Run executes the native thinking iteration loop.
func (c *NativeThinkingController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	maxIter := execCtx.Config.MaxIterations
	totalUsage := agent.TokenUsage{}
	state := &agent.IterationState{MaxIterations: maxIter}
	msgSeq := 0
	eventSeq := 0

	// 1. Build initial conversation via prompt builder
	if execCtx.PromptBuilder == nil {
		return nil, fmt.Errorf("PromptBuilder is nil: cannot call BuildNativeThinkingMessages")
	}
	messages := execCtx.PromptBuilder.BuildNativeThinkingMessages(execCtx, prevStageContext)

	// 2. Store initial messages in DB
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 3. Get available tools
	tools, err := execCtx.ToolExecutor.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// Main iteration loop
	for iteration := 0; iteration < maxIter; iteration++ {
		state.CurrentIteration = iteration + 1

		if state.ShouldAbortOnTimeouts() {
			return failedResult(state, totalUsage), nil
		}

		iterCtx, iterCancel := context.WithTimeout(ctx, execCtx.Config.IterationTimeout)
		startTime := time.Now()

		// Call LLM WITH tools and streaming (native function calling)
		streamed, err := callLLMWithStreaming(iterCtx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       tools, // Tools bound for native calling
		}, &eventSeq)

		if err != nil {
			iterCancel()
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
			state.RecordFailure(err.Error(), isTimeoutError(err))

			// Add error context as user message
			errMsg := fmt.Sprintf("Error from previous attempt: %s. Please try again.", err.Error())
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
			recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

			// Append assistant message to conversation
			messages = append(messages, agent.ConversationMessage{
				Role:      agent.RoleAssistant,
				Content:   resp.Text,
				ToolCalls: resp.ToolCalls,
			})

			// Execute each tool call and append results
			for _, tc := range resp.ToolCalls {
				createToolCallEvent(ctx, execCtx, tc.Name, tc.Arguments, &eventSeq)

				result, toolErr := execCtx.ToolExecutor.Execute(iterCtx, tc)
				if toolErr != nil {
					// Tool execution failed
					state.RecordFailure(toolErr.Error(), isTimeoutError(toolErr))
					errContent := fmt.Sprintf("Error executing tool: %s", toolErr.Error())
					createToolResultEvent(ctx, execCtx, errContent, true, &eventSeq)

					// Append error as tool result message
					messages = append(messages, agent.ConversationMessage{
						Role:       agent.RoleTool,
						Content:    errContent,
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
					})
					storeToolResultMessage(ctx, execCtx, tc.ID, tc.Name, errContent, &msgSeq)
				} else {
					createToolResultEvent(ctx, execCtx, result.Content, result.IsError, &eventSeq)

					// Append result as tool result message
					messages = append(messages, agent.ConversationMessage{
						Role:       agent.RoleTool,
						Content:    result.Content,
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
					})
					storeToolResultMessage(ctx, execCtx, tc.ID, tc.Name, result.Content, &msgSeq)
				}
			}
		} else {
			// No tool calls — this is the final answer
			assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, resp, &msgSeq)
			if storeErr != nil {
				iterCancel()
				return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
			}
			recordLLMInteraction(ctx, execCtx, iteration+1, "iteration", len(messages), resp, &assistantMsg.ID, startTime)

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
	return c.forceConclusion(ctx, execCtx, messages, &totalUsage, state, &msgSeq, &eventSeq)
}

// forceConclusion forces the LLM to produce a final answer by calling without tools.
func (c *NativeThinkingController) forceConclusion(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	messages []agent.ConversationMessage,
	totalUsage *agent.TokenUsage,
	state *agent.IterationState,
	msgSeq *int,
	eventSeq *int,
) (*agent.ExecutionResult, error) {
	if state.LastInteractionFailed {
		return &agent.ExecutionResult{
			Status: agent.ExecutionStatusFailed,
			Error: fmt.Errorf("max iterations (%d) reached with last interaction failed: %s",
				state.MaxIterations, state.LastErrorMessage),
			TokensUsed: *totalUsage,
		}, nil
	}

	// Append forced conclusion prompt
	conclusionPrompt := execCtx.PromptBuilder.BuildForcedConclusionPrompt(state.CurrentIteration, config.IterationStrategyNativeThinking)
	messages = append(messages, agent.ConversationMessage{Role: agent.RoleUser, Content: conclusionPrompt})
	storeObservationMessage(ctx, execCtx, conclusionPrompt, msgSeq)

	startTime := time.Now()

	// Call LLM WITHOUT tools with streaming — forces text-only response
	streamed, err := callLLMWithStreaming(ctx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil, // No tools — force conclusion
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
	assistantMsg, _ := storeAssistantMessage(ctx, execCtx, resp, msgSeq)
	var msgID *string
	if assistantMsg != nil {
		msgID = &assistantMsg.ID
	}
	recordLLMInteraction(ctx, execCtx, state.CurrentIteration+1, "forced_conclusion", len(messages), resp, msgID, startTime)

	if !streamed.ThinkingEventCreated && resp.ThinkingText != "" {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, resp.ThinkingText, map[string]interface{}{
			"source": "native",
		}, eventSeq)
	}

	// Create native tool events (can occur during forced conclusion too)
	createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, eventSeq)
	createGroundingEvents(ctx, execCtx, resp.Groundings, eventSeq)

	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, resp.Text, nil, eventSeq)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: resp.Text,
		TokensUsed:    *totalUsage,
	}, nil
}
