package queue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// ────────────────────────────────────────────────────────────
// Synthesis stage execution
// ────────────────────────────────────────────────────────────

// executeSynthesisStage runs a synthesis agent after a multi-agent stage.
// Creates its own Stage DB record, separate from the investigation stage.
func (e *RealSessionExecutor) executeSynthesisStage(
	ctx context.Context,
	input executeStageInput,
	parallelResult stageResult,
) stageResult {
	synthStageName := parallelResult.stageName + " - Synthesis"
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_name", synthStageName,
		"stage_index", input.stageIndex,
	)

	// Create synthesis Stage DB record
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          synthStageName,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: 1,
		// No parallel_type, no success_policy (single-agent synthesis)
	})
	if err != nil {
		logger.Error("Failed to create synthesis stage", "error", err)
		return stageResult{
			stageName: synthStageName,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create synthesis stage: %w", err),
		}
	}

	// Update session progress + publish stage.status: started
	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, synthStageName, input.stageIndex, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, synthStageName,
		input.stageIndex, input.totalExpectedStages, 1,
		"Synthesizing...")
	publishExecutionProgressFromExecutor(ctx, e.eventPublisher, input.session.ID, stg.ID, "",
		events.ProgressPhaseSynthesizing, fmt.Sprintf("Starting synthesis for %s", parallelResult.stageName))

	// Build synthesis agent config — synthesis: block is optional, defaults apply
	synthAgentConfig := config.StageAgentConfig{
		Name: "SynthesisAgent",
	}
	if s := input.stageConfig.Synthesis; s != nil {
		if s.Agent != "" {
			synthAgentConfig.Name = s.Agent
		}
		if s.LLMBackend != "" {
			synthAgentConfig.LLMBackend = s.LLMBackend
		}
		if s.LLMProvider != "" {
			synthAgentConfig.LLMProvider = s.LLMProvider
		}
	}

	// Build synthesis context: query full conversation history for each parallel agent
	synthContext := e.buildSynthesisContext(ctx, parallelResult, input)

	// Execute synthesis agent — override prevContext to feed parallel investigation histories
	synthInput := input
	synthInput.prevContext = synthContext

	ar := e.executeAgent(ctx, synthInput, stg, synthAgentConfig, 0, synthAgentConfig.Name)

	// Update synthesis stage status (use background context — ctx may be cancelled)
	if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
		logger.Error("Failed to update synthesis stage status", "error", updateErr)
	}

	return stageResult{
		stageID:       stg.ID,
		stageName:     synthStageName,
		status:        mapAgentStatusToSessionStatus(ar.status),
		finalAnalysis: ar.finalAnalysis,
		err:           ar.err,
		agentResults:  []agentResult{ar},
	}
}

// buildSynthesisContext queries the full timeline for each parallel agent
// and formats it for the synthesis agent.
func (e *RealSessionExecutor) buildSynthesisContext(
	ctx context.Context,
	parallelResult stageResult,
	input executeStageInput,
) string {
	configs := buildConfigs(input.stageConfig)

	investigations := make([]agentctx.AgentInvestigation, len(parallelResult.agentResults))
	for i, ar := range parallelResult.agentResults {
		// Use display name from configs (handles replica naming)
		displayName := ""
		if i < len(configs) {
			displayName = configs[i].displayName
		}
		if displayName == "" && i < len(input.stageConfig.Agents) {
			displayName = input.stageConfig.Agents[i].Name
		}

		investigation := agentctx.AgentInvestigation{
			AgentName:   displayName,
			AgentIndex:  i + 1,              // 1-based
			LLMBackend:  ar.llmBackend,      // resolved at execution time
			LLMProvider: ar.llmProviderName, // resolved at execution time
			Status:      mapAgentStatusToSessionStatus(ar.status),
		}

		if ar.err != nil {
			investigation.ErrorMessage = ar.err.Error()
		}

		// Query full timeline for this agent execution
		if ar.executionID != "" {
			timeline, err := input.timelineService.GetAgentTimeline(ctx, ar.executionID)
			if err != nil {
				slog.Warn("Failed to get agent timeline for synthesis",
					"execution_id", ar.executionID,
					"error", err,
				)
			} else {
				investigation.Events = timeline
			}
		}

		investigations[i] = investigation
	}

	return agentctx.FormatInvestigationForSynthesis(investigations, input.stageConfig.Name)
}

// ────────────────────────────────────────────────────────────
// Executive summary
// ────────────────────────────────────────────────────────────

// executiveSummarySeqNum is a sentinel sequence number ensuring the executive
// summary timeline event sorts after all stage events.
const executiveSummarySeqNum = 999_999

// generateExecutiveSummary generates an executive summary from the final analysis.
// Uses a single LLM call (no tools, no streaming to timeline).
// Fail-open: returns ("", error) on failure; caller decides how to handle.
func (e *RealSessionExecutor) generateExecutiveSummary(
	ctx context.Context,
	session *ent.AlertSession,
	chain *config.ChainConfig,
	finalAnalysis string,
	timelineService *services.TimelineService,
	interactionService *services.InteractionService,
) (string, error) {
	logger := slog.With("session_id", session.ID)
	startTime := time.Now()

	// Publish session progress: finalizing.
	// Executive summary is the last expected step; use totalExpectedStages - 1 as
	// the 0-based index so CurrentStageIndex (1-based) equals totalExpectedStages.
	totalExpectedStages := countExpectedStages(chain)
	publishSessionProgress(ctx, e.eventPublisher, session.ID, "Executive Summary",
		totalExpectedStages-1, totalExpectedStages, 0, "Generating executive summary")
	publishExecutionProgressFromExecutor(ctx, e.eventPublisher, session.ID, "", "",
		events.ProgressPhaseFinalizing, "Generating executive summary")

	// Resolve LLM provider: chain.executive_summary_provider → chain.llm_provider → defaults.llm_provider
	var providerName string
	if e.cfg.Defaults != nil {
		providerName = e.cfg.Defaults.LLMProvider
	}
	if chain.LLMProvider != "" {
		providerName = chain.LLMProvider
	}
	if chain.ExecutiveSummaryProvider != "" {
		providerName = chain.ExecutiveSummaryProvider
	}
	provider, err := e.cfg.GetLLMProvider(providerName)
	if err != nil {
		return "", fmt.Errorf("executive summary LLM provider %q not found: %w", providerName, err)
	}

	// Resolve backend from chain-level LLM backend or defaults
	backend := agent.DefaultLLMBackend
	if e.cfg.Defaults != nil && e.cfg.Defaults.LLMBackend != "" {
		backend = e.cfg.Defaults.LLMBackend
	}
	if chain.LLMBackend != "" {
		backend = chain.LLMBackend
	}

	// Build prompts
	systemPrompt := e.promptBuilder.BuildExecutiveSummarySystemPrompt()
	userPrompt := e.promptBuilder.BuildExecutiveSummaryUserPrompt(finalAnalysis)

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemPrompt},
		{Role: agent.RoleUser, Content: userPrompt},
	}

	// Single LLM call — no tools, consume full response from stream
	llmInput := &agent.GenerateInput{
		SessionID: session.ID,
		Messages:  messages,
		Config:    provider,
		Backend:   backend,
	}

	// Derive a cancellable context so the producer goroutine in Generate
	// is always cleaned up when we return (e.g. on ErrorChunk early exit).
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	ch, err := e.llmClient.Generate(llmCtx, llmInput)
	if err != nil {
		return "", fmt.Errorf("executive summary LLM call failed: %w", err)
	}

	// Collect full text response and token usage.
	var sb strings.Builder
	var usage agent.TokenUsage
	for chunk := range ch {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			sb.WriteString(c.Content)
		case *agent.UsageChunk:
			usage.InputTokens += c.InputTokens
			usage.OutputTokens += c.OutputTokens
			usage.TotalTokens += c.TotalTokens
		case *agent.ErrorChunk:
			return "", fmt.Errorf("executive summary LLM error: %s", c.Message)
		}
	}

	summary := sb.String()
	if summary == "" {
		return "", fmt.Errorf("executive summary LLM returned empty response")
	}

	durationMs := int(time.Since(startTime).Milliseconds())

	// Record session-level LLM interaction with inline conversation for observability.
	conversation := []map[string]string{
		{"role": string(agent.RoleSystem), "content": systemPrompt},
		{"role": string(agent.RoleUser), "content": userPrompt},
		{"role": string(agent.RoleAssistant), "content": summary},
	}
	createReq := models.CreateLLMInteractionRequest{
		SessionID:       session.ID,
		InteractionType: "executive_summary",
		ModelName:       provider.Model,
		LLMRequest: map[string]any{
			"messages_count": len(messages),
			"conversation":   conversation,
		},
		LLMResponse: map[string]any{
			"text_length":      len(summary),
			"tool_calls_count": 0,
		},
		DurationMs: &durationMs,
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 {
		createReq.InputTokens = &usage.InputTokens
		createReq.OutputTokens = &usage.OutputTokens
		createReq.TotalTokens = &usage.TotalTokens
	}
	interaction, createErr := interactionService.CreateLLMInteraction(ctx, createReq)
	if createErr != nil {
		logger.Warn("Failed to record executive summary LLM interaction",
			"error", createErr)
	} else if e.eventPublisher != nil {
		// Publish interaction.created for trace view live updates.
		if pubErr := e.eventPublisher.PublishInteractionCreated(ctx, session.ID, events.InteractionCreatedPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeInteractionCreated,
				SessionID: session.ID,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			},
			InteractionID:   interaction.ID,
			InteractionType: events.InteractionTypeLLM,
		}); pubErr != nil {
			logger.Warn("Failed to publish interaction created for executive summary",
				"error", pubErr)
		}
	}

	// Create session-level timeline event (no stage_id, no execution_id).
	// Use a fixed sequence number — executive summary is always the last event.
	//
	// NOTE: This event is persisted to the DB only — it is NOT published to
	// WebSocket clients via EventPublisher. Clients discover the executive
	// summary through the session API response (executive_summary field) or
	// by querying the timeline after the session completes.
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		SequenceNumber: executiveSummarySeqNum,
		EventType:      timelineevent.EventTypeExecutiveSummary,
		Status:         timelineevent.StatusCompleted,
		Content:        summary,
	})
	if err != nil {
		logger.Warn("Failed to create executive summary timeline event (summary still returned)",
			"error", err)
	}

	logger.Info("Executive summary generated", "summary_length", len(summary))
	return summary, nil
}
