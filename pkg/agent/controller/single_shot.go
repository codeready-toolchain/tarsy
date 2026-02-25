package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
)

// SingleShotConfig parameterizes SingleShotController behavior.
type SingleShotConfig struct {
	// BuildMessages produces the initial conversation messages.
	BuildMessages func(*agent.ExecutionContext, string) []agent.ConversationMessage

	// ThinkingFallback uses ThinkingText as final analysis when resp.Text is empty.
	ThinkingFallback bool

	// InteractionLabel is recorded in LLM interactions (e.g. "synthesis").
	InteractionLabel string
}

// SingleShotController executes a single LLM call with no MCP tools.
// Parameterized via SingleShotConfig so the same controller serves synthesis,
// scoring, and any future single-shot agent types.
//
// Note: while no MCP tools are bound, native Gemini tools (Google Search, URL Context)
// may still be available when using google-native backend, since the Gemini API
// only suppresses native tools when MCP tools are present.
type SingleShotController struct {
	cfg SingleShotConfig
}

// NewSingleShotController creates a new single-shot controller.
func NewSingleShotController(cfg SingleShotConfig) *SingleShotController {
	return &SingleShotController{cfg: cfg}
}

// NewSynthesisController creates a SingleShotController configured for synthesis.
func NewSynthesisController(pb agent.PromptBuilder) *SingleShotController {
	return NewSingleShotController(SingleShotConfig{
		BuildMessages:    pb.BuildSynthesisMessages,
		ThinkingFallback: true,
		InteractionLabel: "synthesis",
	})
}

// Run executes a single LLM call and returns the result.
func (c *SingleShotController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	startTime := time.Now()
	msgSeq := 0
	eventSeq := 0

	// 1. Build messages via config-provided builder
	if c.cfg.BuildMessages == nil {
		return nil, fmt.Errorf("BuildMessages function is nil")
	}
	messages := c.cfg.BuildMessages(execCtx, prevStageContext)

	// 2. Store initial messages
	if err := storeMessages(ctx, execCtx, messages, &msgSeq); err != nil {
		return nil, err
	}

	// 3. Single LLM call with streaming (no tools)
	streamed, err := callLLMWithStreaming(ctx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
		SessionID:   execCtx.SessionID,
		ExecutionID: execCtx.ExecutionID,
		Messages:    messages,
		Config:      execCtx.Config.LLMProvider,
		Tools:       nil, // No MCP tools; native tools (Google Search) may still activate
		Backend:     execCtx.Config.LLMBackend,
	}, &eventSeq)
	if err != nil {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
		return nil, fmt.Errorf("%s LLM call failed: %w", c.cfg.InteractionLabel, err)
	}
	resp := streamed.LLMResponse

	// 4. Record thinking content (only if not already created by streaming)
	if !streamed.ThinkingEventCreated && resp.ThinkingText != "" {
		createTimelineEvent(ctx, execCtx, timelineevent.EventTypeLlmThinking, resp.ThinkingText, map[string]interface{}{
			"source": "native",
		}, &eventSeq)
	}

	// Create native tool events (code execution, grounding)
	createCodeExecutionEvents(ctx, execCtx, resp.CodeExecutions, &eventSeq)
	createGroundingEvents(ctx, execCtx, resp.Groundings, &eventSeq)

	// 5. Compute final analysis with optional thinking fallback
	finalAnalysis := resp.Text
	if c.cfg.ThinkingFallback && finalAnalysis == "" && resp.ThinkingText != "" {
		finalAnalysis = resp.ThinkingText
	}

	// 6. Record final analysis
	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, finalAnalysis, nil, &eventSeq)

	// 7. Store assistant message and LLM interaction
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
	recordLLMInteraction(ctx, execCtx, 1, c.cfg.InteractionLabel, len(messages), storeResp, &assistantMsg.ID, startTime)

	return &agent.ExecutionResult{
		Status:        agent.ExecutionStatusCompleted,
		FinalAnalysis: finalAnalysis,
		TokensUsed:    tokenUsageFromResp(resp),
	}, nil
}
