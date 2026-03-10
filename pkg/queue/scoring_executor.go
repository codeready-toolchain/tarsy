package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/event"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/google/uuid"
)

const scoringTimeout = 10 * time.Minute

// ScoringExecutor orchestrates the scoring workflow: creating stage/execution
// records, running the scoring agent, and writing results to session_scores.
// It is called asynchronously after session completion (auto-trigger) and
// on-demand via the re-score API endpoint.
type ScoringExecutor struct {
	cfg            *config.Config
	dbClient       *ent.Client
	llmClient      agent.LLMClient
	agentFactory   *agent.AgentFactory
	eventPublisher agent.EventPublisher
	promptBuilder  *prompt.PromptBuilder

	stageService       *services.StageService
	timelineService    *services.TimelineService
	interactionService *services.InteractionService
	messageService     *services.MessageService

	mu      sync.RWMutex
	wg      sync.WaitGroup
	stopped bool
}

// NewScoringExecutor creates a new ScoringExecutor.
func NewScoringExecutor(
	cfg *config.Config,
	dbClient *ent.Client,
	llmClient agent.LLMClient,
	eventPublisher agent.EventPublisher,
) *ScoringExecutor {
	controllerFactory := controller.NewFactory()
	msgService := services.NewMessageService(dbClient)
	return &ScoringExecutor{
		cfg:                cfg,
		dbClient:           dbClient,
		llmClient:          llmClient,
		agentFactory:       agent.NewAgentFactory(controllerFactory),
		eventPublisher:     eventPublisher,
		promptBuilder:      prompt.NewPromptBuilder(cfg.MCPServerRegistry),
		stageService:       services.NewStageService(dbClient),
		timelineService:    services.NewTimelineService(dbClient),
		interactionService: services.NewInteractionService(dbClient, msgService),
		messageService:     msgService,
	}
}

// ScoreSessionAsync launches scoring in a background goroutine.
// Silently returns if scoring is disabled or the executor is stopped.
// Used by the worker for auto-trigger after session completion.
func (e *ScoringExecutor) ScoreSessionAsync(sessionID, triggeredBy string, checkEnabled bool) {
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	go func() {
		defer e.wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), scoringTimeout)
		defer cancel()

		scoreID, err := e.prepareScoring(ctx, sessionID, triggeredBy, checkEnabled)
		if err != nil {
			if !errors.Is(err, ErrScoringDisabled) {
				slog.Warn("Async scoring preparation failed",
					"session_id", sessionID, "error", err)
			}
			return
		}
		e.executeScoring(ctx, scoreID, sessionID)
	}()
}

// SubmitScoring creates the scoring records (stage, session_score, execution)
// synchronously and launches the LLM evaluation in a background goroutine.
// Returns the session_score ID immediately for the API response.
// checkEnabled controls whether the chain's scoring.enabled flag is enforced.
func (e *ScoringExecutor) SubmitScoring(ctx context.Context, sessionID, triggeredBy string, checkEnabled bool) (string, error) {
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return "", ErrShuttingDown
	}
	e.mu.RUnlock()

	scoreID, err := e.prepareScoring(ctx, sessionID, triggeredBy, checkEnabled)
	if err != nil {
		return "", err
	}

	// Register goroutine with double-check against Stop()
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return "", ErrShuttingDown
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	go func() {
		defer e.wg.Done()
		execCtx, cancel := context.WithTimeout(context.Background(), scoringTimeout)
		defer cancel()
		e.executeScoring(execCtx, scoreID, sessionID)
	}()

	return scoreID, nil
}

// prepareScoring validates preconditions and creates all DB records (stage,
// session_score, agent_execution). Returns the score ID on success.
func (e *ScoringExecutor) prepareScoring(ctx context.Context, sessionID, triggeredBy string, checkEnabled bool) (string, error) {
	// 1. Load session
	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to load session: %w", err)
	}

	// 2. Validate terminal state
	if !IsTerminalStatus(session.Status) {
		return "", fmt.Errorf("session %s is not in a terminal state (status: %s)", sessionID, session.Status)
	}

	// 3. Resolve chain config
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		return "", fmt.Errorf("failed to resolve chain config: %w", err)
	}

	// 4. Check scoring enabled (for auto-trigger; API bypasses)
	if checkEnabled && (chain.Scoring == nil || !chain.Scoring.Enabled) {
		return "", ErrScoringDisabled
	}

	// 5. Resolve scoring config
	resolvedConfig, err := agent.ResolveScoringConfig(e.cfg, chain, chain.Scoring)
	if err != nil {
		return "", fmt.Errorf("failed to resolve scoring config: %w", err)
	}

	// 6. Get next stage index
	maxIndex, err := e.stageService.GetMaxStageIndex(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to get max stage index: %w", err)
	}
	stageIndex := maxIndex + 1

	// 7. Create Stage record (before SessionScore so we can set the immutable stage_id FK)
	stg, err := e.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          sessionID,
		StageName:          "Scoring",
		StageIndex:         stageIndex,
		ExpectedAgentCount: 1,
		StageType:          string(stage.StageTypeScoring),
	})
	if err != nil {
		return "", fmt.Errorf("failed to create scoring stage: %w", err)
	}

	// 8. Create SessionScore record (linked to the stage)
	promptHash := fmt.Sprintf("%x", prompt.GetCurrentPromptHash())
	scoreID := uuid.New().String()

	_, err = e.dbClient.SessionScore.Create().
		SetID(scoreID).
		SetSessionID(sessionID).
		SetStageID(stg.ID).
		SetScoreTriggeredBy(triggeredBy).
		SetPromptHash(promptHash).
		SetStatus(sessionscore.StatusInProgress).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return "", ErrScoringInProgress
		}
		return "", fmt.Errorf("failed to create session score: %w", err)
	}

	// 9. Create AgentExecution record
	scoringProviderName := resolveScoringProviderName(e.cfg.Defaults, chain, chain.Scoring)
	_, err = e.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:     stg.ID,
		SessionID:   sessionID,
		AgentName:   resolvedConfig.AgentName,
		AgentIndex:  1,
		LLMBackend:  resolvedConfig.LLMBackend,
		LLMProvider: scoringProviderName,
	})
	if err != nil {
		e.failScore(scoreID, "failed to create agent execution: "+err.Error())
		e.finishScoringStage(stg.ID, sessionID, stageIndex, events.StageStatusFailed, err.Error())
		return "", fmt.Errorf("failed to create agent execution: %w", err)
	}

	return scoreID, nil
}

// executeScoring runs the LLM evaluation phase for a previously prepared scoring.
func (e *ScoringExecutor) executeScoring(ctx context.Context, scoreID, sessionID string) {
	logger := slog.With("session_id", sessionID, "score_id", scoreID)
	logger.Info("Scoring executor: starting evaluation")

	// Load the scoring stage and execution from DB
	score, err := e.dbClient.SessionScore.Get(ctx, scoreID)
	if err != nil {
		logger.Error("Failed to load session score for execution", "error", err)
		return
	}

	stageID := ""
	if score.StageID != nil {
		stageID = *score.StageID
	}

	stg, err := e.stageService.GetStageByID(ctx, stageID, true)
	if err != nil {
		logger.Error("Failed to load scoring stage", "error", err)
		e.failScore(scoreID, "failed to load scoring stage: "+err.Error())
		return
	}

	execs := stg.Edges.AgentExecutions
	if len(execs) == 0 {
		e.failScore(scoreID, "no agent execution found for scoring stage")
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, "no agent execution")
		return
	}
	exec := execs[0]

	// Resolve config (need it for the agent factory)
	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		e.failScore(scoreID, "failed to load session: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		return
	}
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		e.failScore(scoreID, "failed to resolve chain: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		return
	}
	resolvedConfig, err := agent.ResolveScoringConfig(e.cfg, chain, chain.Scoring)
	if err != nil {
		e.failScore(scoreID, "failed to resolve scoring config: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		return
	}
	promptHash := fmt.Sprintf("%x", prompt.GetCurrentPromptHash())

	// Publish stage started
	if updateErr := e.stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, ""); updateErr != nil {
		logger.Warn("Failed to update agent execution to active", "error", updateErr)
	}
	publishExecutionStatus(ctx, e.eventPublisher, sessionID, stageID, exec.ID, 1, string(agentexecution.StatusActive), "")
	publishStageStatus(ctx, e.eventPublisher, sessionID, stageID, "Scoring", stg.StageIndex, stage.StageTypeScoring, nil, events.StageStatusStarted)

	// Build investigation context
	investigationContext := e.buildScoringContext(ctx, sessionID)

	// Build ExecutionContext and create agent
	agentExecCtx := &agent.ExecutionContext{
		SessionID:      sessionID,
		StageID:        stageID,
		ExecutionID:    exec.ID,
		AgentName:      resolvedConfig.AgentName,
		AgentIndex:     1,
		Config:         resolvedConfig,
		LLMClient:      e.llmClient,
		EventPublisher: e.eventPublisher,
		PromptBuilder:  e.promptBuilder,
		Services: &agent.ServiceBundle{
			Timeline:    e.timelineService,
			Message:     e.messageService,
			Interaction: e.interactionService,
			Stage:       e.stageService,
		},
	}

	agentInstance, err := e.agentFactory.CreateAgent(agentExecCtx)
	if err != nil {
		errMsg := err.Error()
		e.failExecution(exec.ID, sessionID, stageID, stg.StageIndex, errMsg)
		e.failScore(scoreID, errMsg)
		return
	}

	// Execute agent
	result, execErr := agentInstance.Execute(ctx, agentExecCtx, investigationContext)

	// Determine terminal status
	agentStatus := agent.ExecutionStatusFailed
	errMsg := ""
	if execErr != nil {
		errMsg = execErr.Error()
		if ctx.Err() != nil {
			agentStatus = agent.StatusFromErr(ctx.Err())
		}
	} else if result != nil {
		if ctx.Err() != nil {
			agentStatus = agent.StatusFromErr(ctx.Err())
		} else {
			agentStatus = result.Status
		}
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
	}

	// Update AgentExecution terminal status
	entStatus := mapAgentStatusToEntStatus(agentStatus)
	if updateErr := e.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entStatus, errMsg); updateErr != nil {
		logger.Error("Failed to update agent execution status", "error", updateErr)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, sessionID, stageID, exec.ID, 1, string(entStatus), errMsg)

	// Update Stage terminal status
	if updateErr := e.stageService.UpdateStageStatus(context.Background(), stageID); updateErr != nil {
		logger.Error("Failed to update stage status", "error", updateErr)
	}

	stageEventStatus := mapScoringAgentStatus(agentStatus)
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, "Scoring", stg.StageIndex, stage.StageTypeScoring, nil, stageEventStatus)

	// Update SessionScore
	if agentStatus == agent.ExecutionStatusCompleted && result != nil {
		e.completeScore(scoreID, result.FinalAnalysis, promptHash)
		logger.Info("Scoring executor: completed successfully")
	} else {
		e.failScore(scoreID, errMsg)
		logger.Warn("Scoring executor: failed", "error", errMsg)
	}

	// Schedule event cleanup
	e.scheduleEventCleanup(stageID, time.Now())
}

// Stop marks the executor as stopped and waits for active scoring goroutines to drain.
func (e *ScoringExecutor) Stop() {
	e.mu.Lock()
	e.stopped = true
	e.mu.Unlock()

	e.wg.Wait()
}

// ────────────────────────────────────────────────────────────
// Context building
// ────────────────────────────────────────────────────────────

// buildScoringContext retrieves the structured investigation history for scoring.
// Includes investigation, synthesis, exec_summary, and action stages.
// Excludes chat and scoring stages.
func (e *ScoringExecutor) buildScoringContext(ctx context.Context, sessionID string) string {
	logger := slog.With("session_id", sessionID)

	stages, err := e.stageService.GetStagesBySession(ctx, sessionID, true)
	if err != nil {
		logger.Warn("Failed to get stages for scoring context", "error", err)
		return ""
	}

	// Collect synthesis results keyed by parent stage ID
	synthResults := make(map[string]string)
	for _, stg := range stages {
		if stg.StageType == stage.StageTypeSynthesis && stg.ReferencedStageID != nil {
			if fa := extractFinalAnalysisFromStage(ctx, e.timelineService, stg); fa != "" {
				synthResults[*stg.ReferencedStageID] = fa
			}
		}
	}

	var investigations []agentctx.StageInvestigation
	for _, stg := range stages {
		switch stg.StageType {
		case stage.StageTypeInvestigation, stage.StageTypeExecSummary, stage.StageTypeAction:
			// Include these stage types so the scoring LLM sees the full pipeline.
		default:
			// Skip chat and scoring stages.
			continue
		}

		execs := stg.Edges.AgentExecutions
		sort.Slice(execs, func(i, j int) bool {
			return execs[i].AgentIndex < execs[j].AgentIndex
		})
		agents := make([]agentctx.AgentInvestigation, len(execs))
		for i, exec := range execs {
			var tlEvents []*ent.TimelineEvent
			timeline, tlErr := e.timelineService.GetAgentTimeline(ctx, exec.ID)
			if tlErr != nil {
				logger.Warn("Failed to get agent timeline for scoring context",
					"execution_id", exec.ID, "error", tlErr)
			} else {
				tlEvents = timeline
			}

			agents[i] = agentctx.AgentInvestigation{
				AgentName:    exec.AgentName,
				AgentIndex:   exec.AgentIndex,
				LLMBackend:   exec.LlmBackend,
				LLMProvider:  stringFromNillable(exec.LlmProvider),
				Status:       mapExecStatusToSessionStatus(exec.Status),
				Events:       tlEvents,
				ErrorMessage: stringFromNillable(exec.ErrorMessage),
			}
		}

		si := agentctx.StageInvestigation{
			StageName:  stg.StageName,
			StageIndex: stg.StageIndex,
			Agents:     agents,
		}
		if synth, ok := synthResults[stg.ID]; ok {
			si.SynthesisResult = synth
		}
		investigations = append(investigations, si)
	}

	// Get executive summary from session-level timeline event
	executiveSummary := e.getExecutiveSummary(ctx, sessionID)

	return agentctx.FormatStructuredInvestigation(investigations, executiveSummary)
}

// getExecutiveSummary retrieves the executive summary from session-level timeline events.
func (e *ScoringExecutor) getExecutiveSummary(ctx context.Context, sessionID string) string {
	sessionEvents, err := e.timelineService.GetSessionTimeline(ctx, sessionID)
	if err != nil {
		return ""
	}
	for _, evt := range sessionEvents {
		if evt.EventType == timelineevent.EventTypeExecutiveSummary {
			return evt.Content
		}
	}
	return ""
}

// ────────────────────────────────────────────────────────────
// SessionScore updates
// ────────────────────────────────────────────────────────────

// completeScore parses the scoring result JSON and updates the SessionScore record.
func (e *ScoringExecutor) completeScore(scoreID, finalAnalysisJSON, promptHash string) {
	var result controller.ScoringResult
	if err := json.Unmarshal([]byte(finalAnalysisJSON), &result); err != nil {
		slog.Error("Failed to parse scoring result JSON", "score_id", scoreID, "error", err)
		e.failScore(scoreID, "failed to parse scoring result: "+err.Error())
		return
	}

	now := time.Now()
	if err := e.dbClient.SessionScore.UpdateOneID(scoreID).
		SetTotalScore(result.TotalScore).
		SetScoreAnalysis(result.ScoreAnalysis).
		SetMissingToolsAnalysis(result.MissingToolsAnalysis).
		SetPromptHash(promptHash).
		SetStatus(sessionscore.StatusCompleted).
		SetCompletedAt(now).
		Exec(context.Background()); err != nil {
		slog.Error("Failed to update session score to completed", "score_id", scoreID, "error", err)
	}
}

// failScore marks a SessionScore as failed with an error message.
func (e *ScoringExecutor) failScore(scoreID, errMsg string) {
	now := time.Now()
	if err := e.dbClient.SessionScore.UpdateOneID(scoreID).
		SetStatus(sessionscore.StatusFailed).
		SetCompletedAt(now).
		SetErrorMessage(errMsg).
		Exec(context.Background()); err != nil {
		slog.Error("Failed to update session score to failed", "score_id", scoreID, "error", err)
	}
}

// ────────────────────────────────────────────────────────────
// Stage/execution terminal helpers
// ────────────────────────────────────────────────────────────

// failExecution updates agent execution and stage to failed state.
func (e *ScoringExecutor) failExecution(execID, sessionID, stageID string, stageIndex int, errMsg string) {
	if updateErr := e.stageService.UpdateAgentExecutionStatus(
		context.Background(), execID, agentexecution.StatusFailed, errMsg,
	); updateErr != nil {
		slog.Error("Failed to update agent execution status to failed", "error", updateErr)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, sessionID, stageID, execID, 1, string(agentexecution.StatusFailed), errMsg)
	e.finishScoringStage(stageID, sessionID, stageIndex, events.StageStatusFailed, errMsg)
}

// finishScoringStage publishes terminal stage status and forces stage failure.
func (e *ScoringExecutor) finishScoringStage(stageID, sessionID string, stageIndex int, status, errMsg string) {
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, "Scoring", stageIndex, stage.StageTypeScoring, nil, status)
	if updateErr := e.stageService.ForceStageFailure(context.Background(), stageID, errMsg); updateErr != nil {
		slog.Warn("Failed to update scoring stage status",
			"stage_id", stageID, "error", updateErr)
	}
}

// scheduleEventCleanup schedules deletion of transient Event records after a grace period.
func (e *ScoringExecutor) scheduleEventCleanup(stageID string, cutoff time.Time) {
	time.AfterFunc(60*time.Second, func() {
		stg, err := e.stageService.GetStageByID(context.Background(), stageID, false)
		if err != nil {
			slog.Warn("Failed to get stage for scoring event cleanup", "stage_id", stageID, "error", err)
			return
		}
		if _, err := e.dbClient.Event.Delete().
			Where(
				event.SessionIDEQ(stg.SessionID),
				event.CreatedAtLTE(cutoff),
			).
			Exec(context.Background()); err != nil {
			slog.Warn("Failed to cleanup scoring stage events", "stage_id", stageID, "error", err)
		}
	})
}

// ────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────

// IsTerminalStatus checks if a session status is terminal.
func IsTerminalStatus(status alertsession.Status) bool {
	switch status {
	case alertsession.StatusCompleted, alertsession.StatusFailed,
		alertsession.StatusCancelled, alertsession.StatusTimedOut:
		return true
	default:
		return false
	}
}

// resolveScoringProviderName resolves the LLM provider name for scoring using
// the hierarchy: defaults → chain → scoringCfg.
func resolveScoringProviderName(defaults *config.Defaults, chain *config.ChainConfig, scoringCfg *config.ScoringConfig) string {
	var providerName string
	if defaults != nil {
		providerName = defaults.LLMProvider
	}
	if chain != nil && chain.LLMProvider != "" {
		providerName = chain.LLMProvider
	}
	if scoringCfg != nil && scoringCfg.LLMProvider != "" {
		providerName = scoringCfg.LLMProvider
	}
	return providerName
}

// mapScoringAgentStatus maps agent execution status to event status string.
func mapScoringAgentStatus(status agent.ExecutionStatus) string {
	switch status {
	case agent.ExecutionStatusCompleted:
		return events.StageStatusCompleted
	case agent.ExecutionStatusFailed:
		return events.StageStatusFailed
	case agent.ExecutionStatusTimedOut:
		return events.StageStatusTimedOut
	case agent.ExecutionStatusCancelled:
		return events.StageStatusCancelled
	default:
		return events.StageStatusFailed
	}
}

// extractFinalAnalysisFromStage gets the final_analysis content from a stage's timeline.
func extractFinalAnalysisFromStage(ctx context.Context, timelineService *services.TimelineService, stg *ent.Stage) string {
	execs := stg.Edges.AgentExecutions
	if len(execs) == 0 {
		return ""
	}
	for _, exec := range execs {
		timeline, err := timelineService.GetAgentTimeline(ctx, exec.ID)
		if err != nil {
			continue
		}
		for _, evt := range timeline {
			if evt.EventType == timelineevent.EventTypeFinalAnalysis {
				return evt.Content
			}
		}
	}
	return ""
}
