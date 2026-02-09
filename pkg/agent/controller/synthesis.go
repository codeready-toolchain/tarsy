package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// SynthesisController implements a tool-less, single LLM call for synthesis.
// Used for both "synthesis" (LangChain) and "synthesis-native-thinking" (Gemini)
// strategies. The difference is the backend provider, configured in LLMProviderConfig.
type SynthesisController struct{}

// NewSynthesisController creates a new synthesis controller.
func NewSynthesisController() *SynthesisController {
	return &SynthesisController{}
}

// Run executes a single LLM call to synthesize previous stage results.
func (c *SynthesisController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	startTime := time.Now()
	msgSeq := 0
	eventSeq := 0

	// 1. Build messages from previous stage context
	messages := c.buildMessages(execCtx, prevStageContext)

	// 2. Store initial messages
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 3. Single LLM call (no tools)
	resp, err := callLLM(ctx, execCtx.LLMClient, &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil, // Synthesis never uses tools
	})
	if err != nil {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
		return nil, fmt.Errorf("synthesis LLM call failed: %w", err)
	}

	// 4. Record thinking content (synthesis-native-thinking may produce thinking)
	if resp.ThinkingText != "" {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, resp.ThinkingText, map[string]interface{}{
			"source": "native",
		}, &eventSeq)
	}

	// Create native tool events (code execution, grounding)
	createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
	createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

	// 5. Compute final analysis with fallback to thinking text when resp.Text is empty
	// (e.g., when the LLM only produced ThinkingChunks)
	finalAnalysis := resp.Text
	if finalAnalysis == "" && resp.ThinkingText != "" {
		finalAnalysis = resp.ThinkingText
	}

	// 6. Record final analysis
	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, finalAnalysis, nil, &eventSeq)

	// 7. Store assistant message and LLM interaction
	// Use finalAnalysis for storage so the persisted message reflects the fallback
	storeResp := resp
	if resp.Text == "" && finalAnalysis != "" {
		// Create a shallow copy with the fallback text so the stored message isn't empty
		respCopy := *resp
		respCopy.Text = finalAnalysis
		storeResp = &respCopy
	}
	assistantMsg, storeErr := storeAssistantMessage(ctx, execCtx, storeResp, &msgSeq)
	if storeErr != nil {
		return nil, fmt.Errorf("failed to store assistant message: %w", storeErr)
	}
	recordLLMInteraction(ctx, execCtx, 1, "synthesis", len(messages), storeResp, &assistantMsg.ID, startTime)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: finalAnalysis,
		TokensUsed:    tokenUsageFromResp(resp),
	}, nil
}

// buildMessages creates the synthesis conversation.
// Phase 3.3 will replace this with the prompt builder.
func (c *SynthesisController) buildMessages(
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) []agent.ConversationMessage {
	messages := []agent.ConversationMessage{
		{
			Role: "system",
			Content: fmt.Sprintf("You are %s, an AI SRE agent.\n\n%s\n\n"+
				"Your task is to synthesize the investigation results from multiple agents "+
				"into a single coherent analysis.",
				execCtx.AgentName, execCtx.Config.CustomInstructions),
		},
	}

	var userContent strings.Builder
	if prevStageContext != "" {
		userContent.WriteString("## Investigation Results from Previous Agents\n\n")
		userContent.WriteString(prevStageContext)
		userContent.WriteString("\n\n")
	}
	userContent.WriteString("## Alert Data\n\n")
	userContent.WriteString(execCtx.AlertData)
	userContent.WriteString("\n\nPlease synthesize the above investigation results into a comprehensive analysis.")

	messages = append(messages, agent.ConversationMessage{
		Role:    "user",
		Content: userContent.String(),
	})

	return messages
}
