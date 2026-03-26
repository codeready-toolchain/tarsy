package controller

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
)

// SingleShotConfig parameterizes SingleShotController behavior.
type SingleShotConfig struct {
	// BuildMessages produces the initial conversation messages.
	BuildMessages func(*agent.ExecutionContext, string) []agent.ConversationMessage

	// ThinkingFallback uses ThinkingText as final analysis when resp.Text is empty.
	ThinkingFallback bool

	// InteractionLabel is recorded in LLM interactions (e.g. InteractionTypeSynthesis).
	InteractionLabel llminteraction.InteractionType
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
		InteractionLabel: llminteraction.InteractionTypeSynthesis,
	})
}

// NewExecSummaryController creates a SingleShotController configured for executive summary.
// prevStageContext receives the finalAnalysis text from the preceding investigation/synthesis stages.
func NewExecSummaryController(pb agent.PromptBuilder) *SingleShotController {
	return NewSingleShotController(SingleShotConfig{
		BuildMessages: func(_ *agent.ExecutionContext, prevStageContext string) []agent.ConversationMessage {
			return []agent.ConversationMessage{
				{Role: agent.RoleSystem, Content: pb.BuildExecutiveSummarySystemPrompt()},
				{Role: agent.RoleUser, Content: pb.BuildExecutiveSummaryUserPrompt(prevStageContext)},
			}
		},
		ThinkingFallback: false,
		InteractionLabel: llminteraction.InteractionTypeExecutiveSummary,
	})
}

// Run executes a single LLM call and returns the result.
func (c *SingleShotController) Run(
	ctx context.Context,
	execCtx *agent.ExecutionContext,
	prevStageContext string,
) (*agent.ExecutionResult, error) {
	startTime := time.Now()
	fbState := NewFallbackState(execCtx)

	// Initialize eventSeq from DB to avoid collisions with events created
	// before this controller starts (consistent with IteratingController).
	eventSeq, seqErr := execCtx.Services.Timeline.GetMaxSequenceForExecution(ctx, execCtx.ExecutionID)
	if seqErr != nil {
		slog.Warn("Failed to get max event sequence for execution, starting from 0",
			"execution_id", execCtx.ExecutionID, "error", seqErr)
	}

	// Initialize msgSeq from DB to avoid collisions when multiple controllers
	// run under the same execution (e.g. scoring + reflector).
	msgSeq, msgSeqErr := execCtx.Services.Message.GetMaxSequenceForExecution(ctx, execCtx.ExecutionID)
	if msgSeqErr != nil {
		slog.Warn("Failed to get max message sequence for execution, starting from 0",
			"execution_id", execCtx.ExecutionID, "error", msgSeqErr)
	}
	fbState.SingleShot = true

	// 1. Build messages via config-provided builder
	if c.cfg.BuildMessages == nil {
		return nil, fmt.Errorf("BuildMessages function is nil")
	}
	messages := c.cfg.BuildMessages(execCtx, prevStageContext)

	// 2. Store initial messages
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

	// 3. Single LLM call with streaming (no tools), with fallback retry
	var streamed *StreamedResponse
	var err error
	var totalUsage agent.TokenUsage
	emptyRetries := 0
	for {
		if status, done := agent.StatusFromContextErr(ctx); done {
			return &agent.ExecutionResult{
				Status:     status,
				Error:      fmt.Errorf("%s interrupted: %w", c.cfg.InteractionLabel, ctx.Err()),
				TokensUsed: totalUsage,
			}, nil
		}
		llmStart := time.Now()
		streamed, err = callLLMWithStreaming(ctx, execCtx, execCtx.LLMClient, &agent.GenerateInput{
			SessionID:   execCtx.SessionID,
			ExecutionID: execCtx.ExecutionID,
			Messages:    messages,
			Config:      execCtx.Config.LLMProvider,
			Tools:       nil, // No MCP tools; native tools (Google Search) may still activate
			Backend:     execCtx.Config.LLMBackend,
			ClearCache:  fbState.consumeClearCache(),
		}, &eventSeq)
		metrics.ObserveLLMCall(execCtx.Config.LLMProviderName, execCtx.Config.LLMProvider.Model,
			time.Since(llmStart), metricsTokens(streamed, err), err)
		if err == nil {
			accumulateUsage(&totalUsage, streamed.LLMResponse)
			resp := streamed.LLMResponse
			hasContent := strings.TrimSpace(resp.Text) != "" || (c.cfg.ThinkingFallback && strings.TrimSpace(resp.ThinkingText) != "")
			if hasContent || emptyRetries >= maxEmptyResponseRetries {
				break
			}
			emptyRetries++
			slog.Warn("LLM returned empty response, retrying",
				"session_id", execCtx.SessionID, "label", c.cfg.InteractionLabel,
				"attempt", emptyRetries, "max_attempts", maxEmptyResponseRetries)
			messages = append(messages, agent.ConversationMessage{
				Role:    agent.RoleUser,
				Content: "Your previous response was empty. Please provide a response.",
			})
			storeObservationMessage(ctx, execCtx, "Your previous response was empty. Please provide a response.", &msgSeq)
			startTime = time.Now()
			continue
		}
		if !tryFallback(ctx, execCtx, fbState, err, &eventSeq) {
			createTimelineEvent(ctx, execCtx, timelineevent.EventTypeError, err.Error(), nil, &eventSeq)
			return nil, fmt.Errorf("%s LLM call failed: %w", c.cfg.InteractionLabel, err)
		}
		startTime = time.Now()
	}

	if status, done := agent.StatusFromContextErr(ctx); done {
		return &agent.ExecutionResult{
			Status:     status,
			Error:      fmt.Errorf("%s interrupted: %w", c.cfg.InteractionLabel, ctx.Err()),
			TokensUsed: totalUsage,
		}, nil
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
	createTimelineEvent(ctx, execCtx, timelineevent.EventTypeFinalAnalysis, finalAnalysis,
		map[string]interface{}{"interaction_type": string(c.cfg.InteractionLabel)}, &eventSeq)

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
		TokensUsed:    totalUsage,
	}, nil
}
