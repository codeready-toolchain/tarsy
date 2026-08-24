package queue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

const composeConcatHeading = "\n\n## Action result\n\n"

func formatComposeConcat(upstreamReport, actionMemo string) string {
	return upstreamReport + composeConcatHeading + actionMemo
}

// executeComposeStage runs the compose agent after a successful action stage.
// Fail-open: LLM errors and empty output become a failed stage whose
// final_analysis is mechanical concat; the caller still appends and continues.
// Cancel/timeout is returned as-is and must not concat-complete.
func (e *RealSessionExecutor) executeComposeStage(
	ctx context.Context,
	input executeStageInput,
	actionResult stageResult,
	upstreamReport, actionMemo string,
) stageResult {
	composeStageName := actionResult.stageName + " - Amended Report"
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_name", composeStageName,
		"stage_index", input.stageIndex,
	)

	if r := e.mapCancellation(ctx); r != nil {
		return stageResult{
			stageName: composeStageName,
			stageType: stage.StageTypeCompose,
			status:    r.Status,
			err:       r.Error,
		}
	}

	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          composeStageName,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: 1,
		StageType:          string(stage.StageTypeCompose),
		ReferencedStageID:  &actionResult.stageID,
	})
	if err != nil {
		if r := e.mapCancellation(ctx); r != nil {
			return stageResult{stageName: composeStageName, stageType: stage.StageTypeCompose, status: r.Status, err: r.Error}
		}
		logger.Error("Failed to create compose stage", "error", err)
		return stageResult{
			stageName:         composeStageName,
			stageType:         stage.StageTypeCompose,
			referencedStageID: &actionResult.stageID,
			status:            alertsession.StatusFailed,
			finalAnalysis:     formatComposeConcat(upstreamReport, actionMemo),
			err:               fmt.Errorf("failed to create compose stage: %w", err),
		}
	}

	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, composeStageName, input.stageIndex, stg.StageType, stg.ReferencedStageID, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, composeStageName,
		input.stageIndex, input.totalExpectedStages, 1,
		"Amending report...")
	publishExecutionProgressFromExecutor(ctx, e.eventPublisher, input.session.ID, stg.ID, "",
		events.ProgressPhaseAmending, "Amending report...")

	agentCfg := config.StageAgentConfig{Name: config.AgentNameCompose}
	if input.chain.ComposeProvider != "" {
		agentCfg.LLMProvider = input.chain.ComposeProvider
	} else if e.cfg.Defaults != nil {
		agentCfg.LLMProvider = e.cfg.Defaults.ComposeProvider
	}

	composeInput := input
	composeInput.composeUpstreamReport = upstreamReport
	composeInput.composeActionMemo = actionMemo

	ar := e.executeAgent(ctx, composeInput, stg, agentCfg, 0, config.AgentNameCompose)

	sessionStatus := mapAgentStatusToSessionStatus(ar.status)
	if sessionStatus == alertsession.StatusCancelled || sessionStatus == alertsession.StatusTimedOut {
		if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
			logger.Error("Failed to update compose stage status", "error", updateErr)
		}
		return stageResult{
			stageID:           stg.ID,
			stageName:         composeStageName,
			stageType:         stg.StageType,
			referencedStageID: stg.ReferencedStageID,
			status:            sessionStatus,
			finalAnalysis:     ar.finalAnalysis,
			err:               ar.err,
			agentResults:      []agentResult{ar},
		}
	}

	finalAnalysis := strings.TrimSpace(ar.finalAnalysis)
	if sessionStatus != alertsession.StatusCompleted || finalAnalysis == "" {
		concat := formatComposeConcat(upstreamReport, actionMemo)
		if ar.executionID != "" && sessionStatus == alertsession.StatusCompleted {
			if updateErr := input.stageService.UpdateAgentExecutionStatus(
				context.Background(), ar.executionID, agentexecution.StatusFailed, "compose LLM returned empty output",
			); updateErr != nil {
				logger.Warn("Failed to mark empty compose execution as failed", "error", updateErr)
			}
		}
		e.persistComposeConcat(context.Background(), input, stg.ID, ar.executionID, concat)
		finalAnalysis = concat
		sessionStatus = alertsession.StatusFailed
		if ar.err == nil {
			ar.err = fmt.Errorf("compose LLM returned empty output")
		}
		logger.Warn("Compose stage failed (fail-open concat)", "error", ar.err)
	}

	if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
		logger.Error("Failed to update compose stage status", "error", updateErr)
	}

	return stageResult{
		stageID:           stg.ID,
		stageName:         composeStageName,
		stageType:         stg.StageType,
		referencedStageID: stg.ReferencedStageID,
		status:            sessionStatus,
		finalAnalysis:     finalAnalysis,
		err:               ar.err,
		agentResults:      []agentResult{ar},
	}
}

func (e *RealSessionExecutor) persistComposeConcat(
	ctx context.Context,
	input executeStageInput,
	stageID, executionID, concat string,
) {
	if input.timelineService == nil || executionID == "" {
		return
	}

	tlEvents, err := input.timelineService.GetAgentTimeline(ctx, executionID)
	if err != nil {
		slog.Warn("Failed to load compose timeline for concat persist",
			"execution_id", executionID, "error", err)
	} else {
		for _, evt := range tlEvents {
			if evt.EventType == timelineevent.EventTypeFinalAnalysis {
				if updateErr := input.timelineService.UpdateTimelineEvent(ctx, evt.ID, concat); updateErr != nil {
					slog.Warn("Failed to update compose concat final_analysis",
						"event_id", evt.ID, "error", updateErr)
				}
				return
			}
		}
	}

	seq, seqErr := input.timelineService.GetMaxSequenceForExecution(ctx, executionID)
	if seqErr != nil {
		slog.Warn("Failed to get max timeline sequence for compose concat",
			"execution_id", executionID, "error", seqErr)
		seq = 0
	}
	stageIDCopy := stageID
	execIDCopy := executionID
	if _, createErr := input.timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      input.session.ID,
		StageID:        &stageIDCopy,
		ExecutionID:    &execIDCopy,
		SequenceNumber: seq + 1,
		EventType:      timelineevent.EventTypeFinalAnalysis,
		Status:         timelineevent.StatusCompleted,
		Content:        concat,
		Metadata:       map[string]any{"interaction_type": "composition", "source": "concat_fallback"},
	}); createErr != nil {
		slog.Warn("Failed to create compose concat final_analysis event",
			"execution_id", executionID, "error", createErr)
	}
}
