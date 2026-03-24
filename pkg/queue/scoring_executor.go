package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/event"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
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
	reflector      *memory.Reflector

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
	var refl *memory.Reflector
	if memoryService != nil {
		memCfg = config.ResolvedMemoryConfig(cfg.Defaults)
		refl = &memory.Reflector{}
	}

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
		runbookService:     runbookService,
		memoryService:      memoryService,
		memoryConfig:       memCfg,
		reflector:          refl,
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
		SetStageName("Scoring").
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
	publishStageStatus(ctx, e.eventPublisher, sessionID, stageID, "Scoring", stg.StageIndex, stage.StageTypeScoring, nil, events.StageStatusStarted)

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

	if scoreCompleted {
		logger.Info("Scoring executor: completed successfully")
	} else {
		logger.Warn("Scoring executor: failed", "error", errMsg)
	}

	// Run memory extraction before publishing the final scoring status.
	if scoreCompleted && e.memoryService != nil && e.memoryConfig != nil {
		e.runMemoryExtraction(ctx, session, investigationContext, result, agentExecCtx)
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
	result *agent.ExecutionResult,
	agentExecCtx *agent.ExecutionContext,
) {
	logger := slog.With("session_id", session.ID)

	if session.FinalAnalysis == nil {
		logger.Info("No final analysis — skipping memory extraction")
		return
	}

	e.publishScoreUpdated(session.ID, events.ScoringStatusMemorizing)

	// Parse the scoring result from the agent's FinalAnalysis JSON.
	var scoringResult controller.ScoringResult
	if err := json.Unmarshal([]byte(result.FinalAnalysis), &scoringResult); err != nil {
		logger.Warn("Failed to parse scoring result for reflector", "error", err)
		return
	}

	project := "default"

	// Fetch existing memories for dedup context.
	existingMemories, err := e.memoryService.FindSimilar(
		ctx, project, *session.FinalAnalysis, e.memoryConfig.ReflectorMemoryLimit,
	)
	if err != nil {
		logger.Warn("Failed to fetch existing memories for reflector", "error", err)
		existingMemories = nil
	}

	reflectorInput := memory.ReflectorInput{
		InvestigationContext: investigationContext,
		ScoringResult:       scoringResult,
		ExistingMemories:    existingMemories,
		AlertType:           session.AlertType,
		ChainID:             session.ChainID,
		LLMClient:           e.llmClient,
		Config:              agentExecCtx.Config,
		SessionID:           session.ID,
		ExecutionID:         agentExecCtx.ExecutionID,
	}

	output, err := e.reflector.Run(ctx, reflectorInput)
	if err != nil {
		logger.Warn("Reflector failed", "error", err)
		return
	}

	// Record the Reflector LLM interaction for observability.
	e.recordReflectorInteraction(ctx, agentExecCtx, output)

	// Apply memory actions.
	var alertTypePtr *string
	if session.AlertType != "" {
		alertTypePtr = &session.AlertType
	}
	chainIDPtr := &session.ChainID

	if applyErr := e.memoryService.ApplyReflectorActions(
		ctx, project, session.ID, alertTypePtr, chainIDPtr, scoringResult.TotalScore, output.Result,
	); applyErr != nil {
		logger.Warn("Failed to apply reflector actions",
			"error", applyErr, "project", project, "session_id", session.ID)
	}

	if output.Result != nil {
		logger.Info("Memory extraction completed",
			"created", len(output.Result.Create),
			"reinforced", len(output.Result.Reinforce),
			"deprecated", len(output.Result.Deprecate))
	}
}

// recordReflectorInteraction saves an LLMInteraction record for the Reflector call.
func (e *ScoringExecutor) recordReflectorInteraction(ctx context.Context, execCtx *agent.ExecutionContext, output *memory.ReflectorOutput) {
	if output == nil || execCtx.Services == nil || execCtx.Services.Interaction == nil {
		return
	}

	durationMs := int(output.Duration.Milliseconds())
	var inputTokens, outputTokens, totalTokens *int
	if output.Usage != nil {
		inputTokens = &output.Usage.InputTokens
		outputTokens = &output.Usage.OutputTokens
		totalTokens = &output.Usage.TotalTokens
	}

	if _, err := e.interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
		SessionID:       execCtx.SessionID,
		StageID:         &execCtx.StageID,
		ExecutionID:     &execCtx.ExecutionID,
		InteractionType: string(llminteraction.InteractionTypeMemoryExtraction),
		ModelName:       execCtx.Config.LLMProvider.Model,
		LLMRequest:      map[string]interface{}{"type": "reflector"},
		LLMResponse:     map[string]interface{}{"text": output.Text},
		InputTokens:     inputTokens,
		OutputTokens:    outputTokens,
		TotalTokens:     totalTokens,
		DurationMs:      &durationMs,
		ThinkingContent: nilIfEmpty(output.Thinking),
	}); err != nil {
		slog.Warn("Failed to record reflector LLM interaction",
			"session_id", execCtx.SessionID, "error", err)
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
	var sb strings.Builder

	// Section 1: Original alert
	sb.WriteString("## ORIGINAL ALERT\n\n")
	if session.AlertData != "" {
		sb.WriteString(session.AlertData)
	} else {
		sb.WriteString("(No alert data available)")
	}
	sb.WriteString("\n\n")

	// Section 2: Runbook
	runbookContent := e.resolveRunbook(ctx, session)
	if runbookContent != "" {
		sb.WriteString("## RUNBOOK\n\n")
		sb.WriteString(runbookContent)
		sb.WriteString("\n\n")
	}

	// Section 3: Available tools per agent + investigation timeline
	timeline, toolsByExec := e.buildInvestigationData(ctx, session.ID)

	if len(toolsByExec) > 0 {
		sb.WriteString("## AVAILABLE TOOLS PER AGENT\n\n")
		sb.WriteString(toolsByExec)
	}

	// Section 4: Investigation timeline
	sb.WriteString("## INVESTIGATION TIMELINE\n\n")
	sb.WriteString(timeline)

	return sb.String()
}

// buildInvestigationData retrieves the structured investigation history for scoring
// and the per-agent available tools. Returns the formatted timeline and tools sections.
func (e *ScoringExecutor) buildInvestigationData(ctx context.Context, sessionID string) (timeline string, toolsSection string) {
	logger := slog.With("session_id", sessionID)

	stages, err := e.stageService.GetStagesBySession(ctx, sessionID, true)
	if err != nil {
		logger.Warn("Failed to get stages for scoring context", "error", err)
		return "", ""
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

	// Track tools per agent (agent name → formatted tool list)
	type agentTools struct {
		name  string
		tools string
	}
	var allAgentTools []agentTools

	var investigations []agentctx.StageInvestigation
	for _, stg := range stages {
		switch stg.StageType {
		case stage.StageTypeInvestigation, stage.StageTypeExecSummary, stage.StageTypeAction:
		default:
			continue
		}

		execs := stg.Edges.AgentExecutions
		sort.Slice(execs, func(i, j int) bool {
			return execs[i].AgentIndex < execs[j].AgentIndex
		})
		agents := make([]agentctx.AgentInvestigation, len(execs))
		for i, exec := range execs {
			var tlEvents []*ent.TimelineEvent
			tl, tlErr := e.timelineService.GetAgentTimeline(ctx, exec.ID)
			if tlErr != nil {
				logger.Warn("Failed to get agent timeline for scoring context",
					"execution_id", exec.ID, "error", tlErr)
			} else {
				tlEvents = tl
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

			// Gather available tools for this agent
			if toolsStr := e.formatAgentTools(ctx, exec.ID); toolsStr != "" {
				allAgentTools = append(allAgentTools, agentTools{name: exec.AgentName, tools: toolsStr})
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

	executiveSummary := e.getExecutiveSummary(ctx, sessionID)
	timeline = agentctx.FormatStructuredInvestigation(investigations, executiveSummary)

	// Deduplicate agent tools (same agent name across stages shows tools only once)
	seen := make(map[string]bool)
	var sb strings.Builder
	for _, at := range allAgentTools {
		if seen[at.name] {
			continue
		}
		seen[at.name] = true
		sb.WriteString("### ")
		sb.WriteString(at.name)
		sb.WriteString("\n")
		sb.WriteString(at.tools)
		sb.WriteString("\n")
	}

	return timeline, sb.String()
}

// formatAgentTools queries mcp_interactions for tool_list entries for an execution
// and formats them as a bullet list.
func (e *ScoringExecutor) formatAgentTools(ctx context.Context, executionID string) string {
	interactions, err := e.dbClient.MCPInteraction.Query().
		Where(
			mcpinteraction.ExecutionIDEQ(executionID),
			mcpinteraction.InteractionTypeEQ(mcpinteraction.InteractionTypeToolList),
		).
		Order(ent.Asc(mcpinteraction.FieldServerName)).
		All(ctx)
	if err != nil || len(interactions) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, mi := range interactions {
		for _, rawTool := range mi.AvailableTools {
			toolMap, ok := rawTool.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := toolMap["name"].(string)
			desc, _ := toolMap["description"].(string)
			if name == "" {
				continue
			}
			sb.WriteString("- ")
			sb.WriteString(mi.ServerName)
			sb.WriteString(".")
			sb.WriteString(name)
			if desc != "" {
				sb.WriteString(" — ")
				sb.WriteString(desc)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// resolveRunbook resolves runbook content for scoring context.
func (e *ScoringExecutor) resolveRunbook(ctx context.Context, session *ent.AlertSession) string {
	configDefault := ""
	if e.cfg.Defaults != nil {
		configDefault = e.cfg.Defaults.Runbook
	}

	if e.runbookService == nil {
		return configDefault
	}

	alertURL := ""
	if session.RunbookURL != nil {
		alertURL = *session.RunbookURL
	}

	content, err := e.runbookService.Resolve(ctx, alertURL)
	if err != nil {
		slog.Warn("Scoring runbook resolution failed, using default",
			"session_id", session.ID, "error", err)
		return configDefault
	}
	return content
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
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, "Scoring", stageIndex, stage.StageTypeScoring, nil, status)
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
