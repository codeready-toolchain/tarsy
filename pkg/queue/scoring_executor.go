package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	"github.com/google/uuid"
)

const scoringTimeout = 10 * time.Minute
const feedbackReflectorTimeout = 3 * time.Minute
const scoringStageName = "Evaluation & Learning"

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
	runbookService     *runbook.Service

	memoryService  *memory.Service
	memoryConfig   *config.MemoryConfig
	contextBuilder *InvestigationContextBuilder

	mu            sync.RWMutex
	wg            sync.WaitGroup
	stopped       bool
	cleanupTimers []*time.Timer
	activeCancels map[string]context.CancelFunc // scoreID → cancel
}

// NewScoringExecutor creates a new ScoringExecutor.
// runbookService may be nil (runbook content will be omitted from the scoring context).
// memoryService may be nil (memory extraction will be skipped).
func NewScoringExecutor(
	cfg *config.Config,
	dbClient *ent.Client,
	llmClient agent.LLMClient,
	eventPublisher agent.EventPublisher,
	runbookService *runbook.Service,
	memoryService *memory.Service,
) *ScoringExecutor {
	controllerFactory := controller.NewFactory()
	msgService := services.NewMessageService(dbClient)

	var memCfg *config.MemoryConfig
	if memoryService != nil {
		memCfg = config.ResolvedMemoryConfig(cfg.Defaults)
	}

	stageService := services.NewStageService(dbClient)
	timelineService := services.NewTimelineService(dbClient)

	return &ScoringExecutor{
		cfg:                cfg,
		dbClient:           dbClient,
		llmClient:          llmClient,
		agentFactory:       agent.NewAgentFactory(controllerFactory),
		eventPublisher:     eventPublisher,
		promptBuilder:      prompt.NewPromptBuilder(cfg.MCPServerRegistry),
		stageService:       stageService,
		timelineService:    timelineService,
		interactionService: services.NewInteractionService(dbClient, msgService),
		messageService:     msgService,
		runbookService:     runbookService,
		memoryService:      memoryService,
		memoryConfig:       memCfg,
		contextBuilder:     NewInvestigationContextBuilder(cfg, dbClient, stageService, timelineService, runbookService),
		activeCancels:      make(map[string]context.CancelFunc),
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

		scoreID, stageID, err := e.prepareScoring(ctx, sessionID, triggeredBy, checkEnabled)
		if err != nil {
			cancel()
			if !errors.Is(err, ErrScoringDisabled) {
				slog.Warn("Async scoring preparation failed",
					"session_id", sessionID, "error", err)
			}
			return
		}
		e.trackCancel(scoreID, cancel)
		defer e.removeCancel(scoreID)
		e.executeScoring(ctx, scoreID, stageID, sessionID)
	}()
}

// SubmitScoring creates the scoring records (stage, session_score, execution)
// synchronously and launches the LLM evaluation in a background goroutine.
// Returns the session_score ID immediately for the API response.
// checkEnabled controls whether the chain's scoring.enabled flag is enforced.
func (e *ScoringExecutor) SubmitScoring(ctx context.Context, sessionID, triggeredBy string, checkEnabled bool) (string, error) {
	// Claim the worker slot before creating DB records to prevent orphans
	// if Stop() is called between record creation and goroutine registration.
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return "", ErrShuttingDown
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	scoreID, stageID, err := e.prepareScoring(ctx, sessionID, triggeredBy, checkEnabled)
	if err != nil {
		e.wg.Done()
		return "", err
	}

	go func() {
		defer e.wg.Done()
		execCtx, cancel := context.WithTimeout(context.Background(), scoringTimeout)
		e.trackCancel(scoreID, cancel)
		defer e.removeCancel(scoreID)
		e.executeScoring(execCtx, scoreID, stageID, sessionID)
	}()

	return scoreID, nil
}

// prepareScoring validates preconditions and creates all DB records (stage,
// session_score, agent_execution). Returns the score ID and stage ID on success.
func (e *ScoringExecutor) prepareScoring(ctx context.Context, sessionID, triggeredBy string, checkEnabled bool) (string, string, error) {
	// 1. Load session
	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		return "", "", fmt.Errorf("failed to load session: %w", err)
	}

	// 2. Validate terminal state
	if !IsTerminalStatus(session.Status) {
		return "", "", fmt.Errorf("session %s is not in a terminal state (status: %s)", sessionID, session.Status)
	}

	// 3. Resolve chain config
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve chain config: %w", err)
	}

	// 4. Check scoring enabled (for auto-trigger; API bypasses)
	if checkEnabled && (chain.Scoring == nil || !chain.Scoring.Enabled) {
		return "", "", ErrScoringDisabled
	}

	// 5. Resolve scoring config
	resolvedConfig, err := agent.ResolveScoringConfig(e.cfg, chain, chain.Scoring)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve scoring config: %w", err)
	}

	// 6. Get next stage index
	maxIndex, err := e.stageService.GetMaxStageIndex(ctx, sessionID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get max stage index: %w", err)
	}
	stageIndex := maxIndex + 1

	// 7. Create Stage + SessionScore in a transaction to prevent orphan stages.
	promptHash := fmt.Sprintf("%x", prompt.GetCurrentPromptHash())
	scoreID := uuid.New().String()
	stageID := uuid.New().String()

	tx, err := e.dbClient.Tx(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to start transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Stage.Create().
		SetID(stageID).
		SetSessionID(sessionID).
		SetStageName(scoringStageName).
		SetStageIndex(stageIndex).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeScoring).
		SetStatus(stage.StatusPending).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return "", "", ErrScoringInProgress
		}
		return "", "", fmt.Errorf("failed to create scoring stage: %w", err)
	}

	_, err = tx.SessionScore.Create().
		SetID(scoreID).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetScoreTriggeredBy(triggeredBy).
		SetPromptHash(promptHash).
		SetStatus(sessionscore.StatusInProgress).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return "", "", ErrScoringInProgress
		}
		return "", "", fmt.Errorf("failed to create session score: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("failed to commit scoring records: %w", err)
	}

	// 8. Create AgentExecution record (outside tx — stage already committed)
	scoringProviderName := resolveScoringProviderName(e.cfg.Defaults, chain, chain.Scoring)
	_, err = e.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:     stageID,
		SessionID:   sessionID,
		AgentName:   resolvedConfig.AgentName,
		AgentIndex:  1,
		LLMBackend:  resolvedConfig.LLMBackend,
		LLMProvider: scoringProviderName,
	})
	if err != nil {
		e.failScore(scoreID, "failed to create agent execution: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stageIndex, events.StageStatusFailed, err.Error())
		return "", "", fmt.Errorf("failed to create agent execution: %w", err)
	}

	return scoreID, stageID, nil
}

// executeScoring runs the LLM evaluation phase for a previously prepared scoring.
func (e *ScoringExecutor) executeScoring(ctx context.Context, scoreID, stageID, sessionID string) {
	logger := slog.With("session_id", sessionID, "score_id", scoreID, "stage_id", stageID)
	logger.Info("Scoring executor: starting evaluation")

	// Notify dashboard so it shows the scoring spinner
	e.publishScoreUpdated(sessionID, events.ScoringStatusInProgress)

	score, err := e.dbClient.SessionScore.Get(ctx, scoreID)
	if err != nil {
		logger.Error("Failed to load session score for execution", "error", err)
		e.failScore(scoreID, "failed to load session score: "+err.Error())
		_ = e.stageService.ForceStageFailure(context.Background(), stageID, "failed to load session score: "+err.Error())
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}

	stg, err := e.stageService.GetStageByID(ctx, stageID, true)
	if err != nil {
		logger.Error("Failed to load scoring stage", "error", err)
		e.failScore(scoreID, "failed to load scoring stage: "+err.Error())
		_ = e.stageService.ForceStageFailure(context.Background(), stageID, "failed to load scoring stage: "+err.Error())
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}

	_ = score // loaded for validation; stageID already known from prepareScoring

	execs := stg.Edges.AgentExecutions
	if len(execs) == 0 {
		e.failScore(scoreID, "no agent execution found for scoring stage")
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, "no agent execution")
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}
	exec := execs[0]

	// Resolve config (need it for the agent factory)
	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		e.failScore(scoreID, "failed to load session: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		e.failScore(scoreID, "failed to resolve chain: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}
	resolvedConfig, err := agent.ResolveScoringConfig(e.cfg, chain, chain.Scoring)
	if err != nil {
		e.failScore(scoreID, "failed to resolve scoring config: "+err.Error())
		e.finishScoringStage(stageID, sessionID, stg.StageIndex, events.StageStatusFailed, err.Error())
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
		return
	}
	promptHash := fmt.Sprintf("%x", prompt.GetCurrentPromptHash())

	// Publish stage started
	if updateErr := e.stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, ""); updateErr != nil {
		logger.Warn("Failed to update agent execution to active", "error", updateErr)
	}
	publishExecutionStatus(ctx, e.eventPublisher, sessionID, stageID, exec.ID, 1, string(agentexecution.StatusActive), "")
	publishStageStatus(ctx, e.eventPublisher, sessionID, stageID, scoringStageName, stg.StageIndex, stage.StageTypeScoring, nil, events.StageStatusStarted)

	// Build investigation context (includes alert data, runbook, available tools, timeline)
	investigationContext := e.buildScoringContext(ctx, session)

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
		e.publishScoreUpdated(sessionID, events.ScoringStatusFailed)
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

	// Persist the score BEFORE publishing terminal statuses so that a
	// persistence failure can flip the terminal state to failed.
	scoreCompleted := false
	if agentStatus == agent.ExecutionStatusCompleted && result != nil {
		if completeErr := e.completeScore(scoreID, result.FinalAnalysis, promptHash); completeErr != nil {
			agentStatus = agent.ExecutionStatusFailed
			errMsg = completeErr.Error()
		} else {
			scoreCompleted = true
		}
	}
	if !scoreCompleted {
		e.failScore(scoreID, errMsg)
	}

	// Run memory extraction while the scoring execution is still active so
	// reflector timeline events stream to the dashboard in real-time.
	if scoreCompleted && e.memoryService != nil && e.memoryConfig != nil {
		e.runMemoryExtraction(ctx, session, investigationContext, result, agentExecCtx)
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
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, scoringStageName, stg.StageIndex, stage.StageTypeScoring, nil, stageEventStatus)

	if scoreCompleted {
		logger.Info("Scoring executor: completed successfully")
	} else {
		logger.Warn("Scoring executor: failed", "error", errMsg)
	}

	// Notify global sessions channel so the dashboard refreshes the score
	scoringStatus := events.ScoringStatusFailed
	if scoreCompleted {
		scoringStatus = events.ScoringStatusCompleted
	}
	e.publishScoreUpdated(sessionID, scoringStatus)

	// Schedule event cleanup
	e.scheduleEventCleanup(stageID, time.Now())
}

// runMemoryExtraction runs the Reflector to extract memories from a completed investigation.
// All errors are warn-logged — extraction never blocks scoring.
func (e *ScoringExecutor) runMemoryExtraction(
	ctx context.Context,
	session *ent.AlertSession,
	investigationContext string,
	scoringResult *agent.ExecutionResult,
	agentExecCtx *agent.ExecutionContext,
) {
	logger := slog.With("session_id", session.ID)

	if session.FinalAnalysis == nil {
		logger.Info("No final analysis — skipping memory extraction")
		return
	}

	e.publishScoreUpdated(session.ID, events.ScoringStatusMemorizing)

	var parsedScore controller.ScoringResult
	if err := json.Unmarshal([]byte(scoringResult.FinalAnalysis), &parsedScore); err != nil {
		logger.Warn("Failed to parse scoring result for reflector", "error", err)
		return
	}

	project := "default"

	existingMemories, err := e.memoryService.FindSimilar(
		ctx, project, *session.FinalAnalysis, e.memoryConfig.ReflectorMemoryLimit,
	)
	if err != nil {
		logger.Warn("Failed to fetch existing memories for reflector", "error", err)
		existingMemories = nil
	}

	reflectorCtrl := memory.NewReflectorController(memory.ReflectorInput{
		InvestigationContext: investigationContext,
		ScoringResult:        parsedScore,
		ExistingMemories:     existingMemories,
		AlertType:            session.AlertType,
		ChainID:              session.ChainID,
	})

	reflectorResult, err := reflectorCtrl.Run(ctx, agentExecCtx, "")
	if err != nil {
		logger.Warn("Reflector failed", "error", err)
		return
	}

	parsed, ok := memory.ParseReflectorResponse(reflectorResult.FinalAnalysis)
	if !ok {
		logger.Warn("Reflector output could not be parsed — no memories extracted",
			"response_length", len(reflectorResult.FinalAnalysis))
		return
	}
	if parsed.IsEmpty() {
		logger.Info("Reflector found no new learnings")
		return
	}

	var alertTypePtr *string
	if session.AlertType != "" {
		alertTypePtr = &session.AlertType
	}
	chainIDPtr := &session.ChainID

	if applyErr := e.memoryService.ApplyReflectorActions(
		ctx, project, session.ID, alertTypePtr, chainIDPtr, parsedScore.TotalScore, parsed,
	); applyErr != nil {
		logger.Warn("Failed to apply reflector actions",
			"error", applyErr, "project", project, "session_id", session.ID)
	}

	logger.Info("Memory extraction completed",
		"created", len(parsed.Create),
		"reinforced", len(parsed.Reinforce),
		"deprecated", len(parsed.Deprecate))
}

// publishScoreUpdated notifies the global sessions channel that scoring
// started or finished, so the dashboard can show the spinner / final score.
func (e *ScoringExecutor) publishScoreUpdated(sessionID string, scoringStatus events.ScoringStatus) {
	if e.eventPublisher == nil {
		return
	}
	if err := e.eventPublisher.PublishSessionScoreUpdated(context.Background(), sessionID, events.SessionScoreUpdatedPayload{
		BasePayload: events.BasePayload{
			Type:      events.EventTypeSessionScoreUpdated,
			SessionID: sessionID,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
		ScoringStatus: scoringStatus,
	}); err != nil {
		slog.Warn("Failed to publish session score updated",
			"session_id", sessionID, "scoring_status", scoringStatus, "error", err)
	}
}

// trackCancel registers a cancel func for an in-flight scoring run.
// If the executor is already stopped, the cancel func is invoked immediately.
func (e *ScoringExecutor) trackCancel(scoreID string, cancel context.CancelFunc) {
	e.mu.Lock()
	if e.activeCancels != nil {
		e.activeCancels[scoreID] = cancel
	} else {
		cancel()
	}
	e.mu.Unlock()
}

// removeCancel deregisters the cancel func for a finished scoring run.
func (e *ScoringExecutor) removeCancel(scoreID string) {
	e.mu.Lock()
	if e.activeCancels != nil {
		delete(e.activeCancels, scoreID)
	}
	e.mu.Unlock()
}

// Stop marks the executor as stopped, cancels in-flight scoring contexts
// and pending cleanup timers, then waits for active goroutines to drain.
func (e *ScoringExecutor) Stop() {
	e.mu.Lock()
	e.stopped = true
	for _, cancel := range e.activeCancels {
		cancel()
	}
	e.activeCancels = nil
	for _, t := range e.cleanupTimers {
		t.Stop()
	}
	e.cleanupTimers = nil
	e.mu.Unlock()

	e.wg.Wait()
}

// ────────────────────────────────────────────────────────────
// Context building
// ────────────────────────────────────────────────────────────

// buildScoringContext builds the complete scoring context including alert data,
// runbook, available tools per agent, and the investigation timeline.
func (e *ScoringExecutor) buildScoringContext(ctx context.Context, session *ent.AlertSession) string {
	return e.contextBuilder.Build(ctx, session)
}

// ────────────────────────────────────────────────────────────
// Feedback Reflector
// ────────────────────────────────────────────────────────────

// RunFeedbackReflectorAsync spawns a goroutine to run the feedback Reflector.
// The execution is attached to the session's existing scoring stage so its
// timeline events and LLM interactions are visible alongside the original score.
func (e *ScoringExecutor) RunFeedbackReflectorAsync(sessionID, feedbackText, qualityRating string) {
	if e.memoryService == nil {
		return
	}

	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	cancelKey := "feedback-" + sessionID

	go func() {
		defer e.wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), feedbackReflectorTimeout)
		e.trackCancel(cancelKey, cancel)
		defer e.removeCancel(cancelKey)

		if err := e.runFeedbackReflector(ctx, sessionID, feedbackText, qualityRating); err != nil {
			slog.Warn("Feedback reflector failed",
				"session_id", sessionID, "error", err)
		}
	}()
}

func (e *ScoringExecutor) runFeedbackReflector(ctx context.Context, sessionID, feedbackText, qualityRating string) error {
	logger := slog.With("session_id", sessionID)

	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		return fmt.Errorf("resolve chain: %w", err)
	}
	resolvedConfig, err := agent.ResolveScoringConfig(e.cfg, chain, chain.Scoring)
	if err != nil {
		return fmt.Errorf("resolve scoring config: %w", err)
	}

	scoringStages, err := e.dbClient.Stage.Query().
		Where(
			stage.SessionIDEQ(sessionID),
			stage.StageTypeEQ(stage.StageTypeScoring),
		).
		Order(ent.Desc(stage.FieldStageIndex)).
		Limit(1).
		All(ctx)
	if err != nil {
		return fmt.Errorf("find scoring stage: %w", err)
	}
	if len(scoringStages) == 0 {
		logger.Info("No scoring stage found — skipping feedback reflector")
		return nil
	}
	stageID := scoringStages[0].ID

	execID := uuid.New().String()
	_, err = e.dbClient.AgentExecution.Create().
		SetID(execID).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetAgentName("FeedbackReflector").
		SetAgentIndex(0).
		SetLlmBackend(string(resolvedConfig.LLMBackend)).
		SetStatus(agentexecution.StatusActive).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("create feedback execution: %w", err)
	}

	execCtx := &agent.ExecutionContext{
		SessionID:      sessionID,
		StageID:        stageID,
		ExecutionID:    execID,
		AgentName:      "FeedbackReflector",
		AgentIndex:     0,
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

	project := "default"

	investigationContext := e.contextBuilder.Build(ctx, session)

	existingMemories, err := e.memoryService.GetBySessionID(ctx, sessionID)
	if err != nil {
		logger.Warn("Failed to fetch session memories for feedback reflector", "error", err)
	}
	var memHints []memory.Memory
	for _, m := range existingMemories {
		if !m.Deprecated {
			memHints = append(memHints, memory.Memory{
				ID: m.ID, Content: m.Content, Category: m.Category,
				Valence: m.Valence, Confidence: m.Confidence, SeenCount: m.SeenCount,
			})
		}
	}

	reflectorCtrl := memory.NewFeedbackReflectorController(memory.FeedbackReflectorInput{
		FeedbackText:         feedbackText,
		QualityRating:        qualityRating,
		InvestigationContext: investigationContext,
		ExistingMemories:     memHints,
		AlertType:            session.AlertType,
		ChainID:              session.ChainID,
	})

	result, err := reflectorCtrl.Run(ctx, execCtx, "")
	if err != nil {
		_ = e.dbClient.AgentExecution.UpdateOneID(execID).
			SetStatus(agentexecution.StatusFailed).
			SetErrorMessage(err.Error()).
			Exec(context.Background())
		return fmt.Errorf("feedback reflector run: %w", err)
	}

	parsed, ok := memory.ParseReflectorResponse(result.FinalAnalysis)
	if !ok {
		logger.Warn("Feedback reflector output could not be parsed",
			"response_length", len(result.FinalAnalysis))
		_ = e.dbClient.AgentExecution.UpdateOneID(execID).
			SetStatus(agentexecution.StatusCompleted).
			Exec(context.Background())
		return nil
	}
	if parsed.IsEmpty() {
		logger.Info("Feedback reflector found no new learnings")
		_ = e.dbClient.AgentExecution.UpdateOneID(execID).
			SetStatus(agentexecution.StatusCompleted).
			Exec(context.Background())
		return nil
	}

	var alertTypePtr *string
	if session.AlertType != "" {
		alertTypePtr = &session.AlertType
	}
	chainIDPtr := &session.ChainID

	if applyErr := e.memoryService.ApplyFeedbackReflectorActions(
		ctx, project, sessionID, alertTypePtr, chainIDPtr, parsed,
	); applyErr != nil {
		_ = e.dbClient.AgentExecution.UpdateOneID(execID).
			SetStatus(agentexecution.StatusFailed).
			SetErrorMessage(applyErr.Error()).
			Exec(context.Background())
		return fmt.Errorf("apply feedback reflector actions: %w", applyErr)
	}

	_ = e.dbClient.AgentExecution.UpdateOneID(execID).
		SetStatus(agentexecution.StatusCompleted).
		Exec(context.Background())

	logger.Info("Feedback reflector completed",
		"created", len(parsed.Create),
		"reinforced", len(parsed.Reinforce),
		"deprecated", len(parsed.Deprecate))
	return nil
}

// ────────────────────────────────────────────────────────────
// SessionScore updates
// ────────────────────────────────────────────────────────────

// completeScore parses the scoring result JSON and updates the SessionScore record.
// Returns an error if parsing or persistence fails.
func (e *ScoringExecutor) completeScore(scoreID, finalAnalysisJSON, promptHash string) error {
	var result controller.ScoringResult
	if err := json.Unmarshal([]byte(finalAnalysisJSON), &result); err != nil {
		return fmt.Errorf("failed to parse scoring result: %w", err)
	}

	now := time.Now()
	if err := e.dbClient.SessionScore.UpdateOneID(scoreID).
		SetTotalScore(result.TotalScore).
		SetScoreAnalysis(result.ScoreAnalysis).
		SetToolImprovementReport(result.ToolImprovementReport).
		SetFailureTags(result.FailureTags).
		SetPromptHash(promptHash).
		SetStatus(sessionscore.StatusCompleted).
		SetCompletedAt(now).
		Exec(context.Background()); err != nil {
		return fmt.Errorf("failed to persist session score: %w", err)
	}
	return nil
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
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, scoringStageName, stageIndex, stage.StageTypeScoring, nil, status)
	if updateErr := e.stageService.ForceStageFailure(context.Background(), stageID, errMsg); updateErr != nil {
		slog.Warn("Failed to update scoring stage status",
			"stage_id", stageID, "error", updateErr)
	}
}

// scheduleEventCleanup schedules deletion of transient Event records after a grace period.
// The timer is tracked so Stop() can cancel it.
func (e *ScoringExecutor) scheduleEventCleanup(stageID string, cutoff time.Time) {
	timer := time.AfterFunc(60*time.Second, func() {
		e.mu.RLock()
		stopped := e.stopped
		e.mu.RUnlock()
		if stopped {
			return
		}

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

	e.mu.Lock()
	if e.stopped {
		timer.Stop()
	} else {
		e.cleanupTimers = append(e.cleanupTimers, timer)
	}
	e.mu.Unlock()
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
// the hierarchy: defaults → defaults.Scoring → chain → scoringCfg.
func resolveScoringProviderName(defaults *config.Defaults, chain *config.ChainConfig, scoringCfg *config.ScoringConfig) string {
	var providerName string
	if defaults != nil {
		providerName = defaults.LLMProvider
		if defaults.Scoring != nil && defaults.Scoring.LLMProvider != "" {
			providerName = defaults.Scoring.LLMProvider
		}
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
