package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// executeExecSummaryStage runs the executive summary agent as a typed stage.
// input.prevContext must be set to the final analysis text by the caller.
// Fail-open: always returns a stageResult; caller extracts summary from finalAnalysis.
func (e *RealSessionExecutor) executeExecSummaryStage(ctx context.Context, input executeStageInput) stageResult {
	logger := slog.With("session_id", input.session.ID)

	// Create exec summary Stage DB record.
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          "Executive Summary",
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: 1,
		StageType:          string(stage.StageTypeExecSummary),
	})
	if err != nil {
		if r := e.mapCancellation(ctx); r != nil {
			return stageResult{stageName: "Executive Summary", stageType: stage.StageTypeExecSummary, status: r.Status, err: r.Error}
		}
		logger.Error("Failed to create exec summary stage", "error", err)
		return stageResult{
			stageName: "Executive Summary",
			stageType: stage.StageTypeExecSummary,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create exec summary stage: %w", err),
		}
	}

	// Update session progress pointer, then publish events.
	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, "Executive Summary", input.stageIndex, stage.StageTypeExecSummary, nil, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, "Executive Summary",
		input.stageIndex, input.totalExpectedStages, 0, "Generating executive summary")
	publishExecutionProgressFromExecutor(ctx, e.eventPublisher, input.session.ID, stg.ID, "",
		events.ProgressPhaseFinalizing, "Generating executive summary")

	// Build exec summary agent config via the dedicated resolver so
	// defaults.executive_summary_* and compose/exec-summary fallback knobs apply
	// and the investigation stage list does not leak.
	agentCfg := config.StageAgentConfig{Name: config.AgentNameExecSummary}
	execInput := input
	execInput.stageConfig = config.StageConfig{}
	resolved, err := agent.ResolveExecSummaryConfig(e.cfg, input.chain)
	ar := e.executeResolvedAgent(ctx, execInput, stg, agentCfg, 0, config.AgentNameExecSummary, resolved, err)

	// Update exec summary stage status (use background context — ctx may be cancelled).
	if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
		logger.Error("Failed to update exec summary stage status", "error", updateErr)
	}

	return stageResult{
		stageID:       stg.ID,
		stageName:     "Executive Summary",
		stageType:     stg.StageType,
		status:        mapAgentStatusToSessionStatus(ar.status),
		finalAnalysis: ar.finalAnalysis,
		err:           ar.err,
		agentResults:  []agentResult{ar},
	}
}
