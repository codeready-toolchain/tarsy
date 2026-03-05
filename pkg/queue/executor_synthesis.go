package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
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

	if r := e.mapCancellation(ctx); r != nil {
		return stageResult{
			stageName: synthStageName,
			stageType: stage.StageTypeSynthesis,
			status:    r.Status,
			err:       r.Error,
		}
	}

	// Create synthesis Stage DB record
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          synthStageName,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: 1,
		StageType:          string(stage.StageTypeSynthesis),
	})
	if err != nil {
		if r := e.mapCancellation(ctx); r != nil {
			return stageResult{stageName: synthStageName, stageType: stage.StageTypeSynthesis, status: r.Status, err: r.Error}
		}
		logger.Error("Failed to create synthesis stage", "error", err)
		return stageResult{
			stageName: synthStageName,
			stageType: stage.StageTypeSynthesis,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create synthesis stage: %w", err),
		}
	}

	// Update session progress + publish stage.status: started
	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, synthStageName, input.stageIndex, stg.StageType, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, synthStageName,
		input.stageIndex, input.totalExpectedStages, 1,
		"Synthesizing...")
	publishExecutionProgressFromExecutor(ctx, e.eventPublisher, input.session.ID, stg.ID, "",
		events.ProgressPhaseSynthesizing, fmt.Sprintf("Starting synthesis for %s", parallelResult.stageName))

	// Build synthesis agent config — synthesis: block is optional, defaults apply
	synthAgentConfig := config.StageAgentConfig{
		Name: config.AgentNameSynthesis,
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
		stageType:     stg.StageType,
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

