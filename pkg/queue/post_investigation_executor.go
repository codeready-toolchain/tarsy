package queue

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/event"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/agent/skill"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/cost"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

const (
	// postInvestigationDefaultTimeout applies to entries without a timeout.
	postInvestigationDefaultTimeout = 10 * time.Minute
	// postInvestigationPrepareTimeout bounds loading, gating and record creation.
	postInvestigationPrepareTimeout = 30 * time.Second
	// postInvestigationStageAttempts bounds stage index allocation retries
	// when a concurrent stage (e.g. chat) takes the same index.
	postInvestigationStageAttempts = 3
)

// postInvestigationOutcome is the outcome label of tarsy_post_investigation_runs_total.
type postInvestigationOutcome string

const (
	postInvestigationOutcomeSkippedGate       postInvestigationOutcome = "skipped_gate"
	postInvestigationOutcomeCompletedActed    postInvestigationOutcome = "completed_acted"
	postInvestigationOutcomeCompletedNoAction postInvestigationOutcome = "completed_no_action"
	postInvestigationOutcomeFailed            postInvestigationOutcome = "failed"
	postInvestigationOutcomeTimedOut          postInvestigationOutcome = "timed_out"
	postInvestigationOutcomeCancelled         postInvestigationOutcome = "cancelled"
)

// PostInvestigationExecutor runs a chain's post_investigation entries after
// automatic scoring completes. Each passing entry gets its own stage and agent
// execution. Runs are fail-open: errors only fail that entry's stage and never
// touch the session, its score or its final analysis.
type PostInvestigationExecutor struct {
	cfg            *config.Config
	dbClient       *ent.Client
	llmClient      agent.LLMClient
	mcpFactory     *mcp.ClientFactory
	agentFactory   *agent.AgentFactory
	eventPublisher agent.EventPublisher
	promptBuilder  *prompt.PromptBuilder

	stageService       *services.StageService
	timelineService    *services.TimelineService
	messageService     *services.MessageService
	interactionService *services.InteractionService
	costBook           *cost.Book

	memoryService    *memory.Service
	memoryConfig     *config.MemoryConfig
	subAgentRegistry *config.SubAgentRegistry
	contextBuilder   *InvestigationContextBuilder

	mu            sync.RWMutex
	wg            sync.WaitGroup
	stopped       bool
	cleanupTimers []*time.Timer
	activeCancels map[string]context.CancelFunc // stageID → cancel
}

// NewPostInvestigationExecutor creates a new PostInvestigationExecutor.
// memoryService and memoryConfig may be nil (memory tools disabled).
func NewPostInvestigationExecutor(
	cfg *config.Config,
	dbClient *ent.Client,
	llmClient agent.LLMClient,
	mcpFactory *mcp.ClientFactory,
	eventPublisher agent.EventPublisher,
	memoryService *memory.Service,
	memoryConfig *config.MemoryConfig,
) *PostInvestigationExecutor {
	msgService := services.NewMessageService(dbClient)
	stageService := services.NewStageService(dbClient)
	timelineService := services.NewTimelineService(dbClient)

	return &PostInvestigationExecutor{
		cfg:                cfg,
		dbClient:           dbClient,
		llmClient:          llmClient,
		mcpFactory:         mcpFactory,
		agentFactory:       agent.NewAgentFactory(controller.NewFactory()),
		eventPublisher:     eventPublisher,
		promptBuilder:      prompt.NewPromptBuilder(cfg.MCPServerRegistry, cfg.Holidays),
		stageService:       stageService,
		timelineService:    timelineService,
		messageService:     msgService,
		interactionService: services.NewInteractionService(dbClient, msgService, nil),
		memoryService:      memoryService,
		memoryConfig:       memoryConfig,
		subAgentRegistry:   config.BuildSubAgentRegistry(cfg.AgentRegistry.GetAll()),
		// No runbook service: the timeline-only builder method never resolves runbooks.
		contextBuilder: NewInvestigationContextBuilder(cfg, dbClient, stageService, timelineService, nil),
		activeCancels:  make(map[string]context.CancelFunc),
	}
}

// SetCostBook sets the price book used when persisting LLM interaction costs.
// Rebuilds the interaction service so subsequent writes use the book.
func (e *PostInvestigationExecutor) SetCostBook(book *cost.Book) {
	e.costBook = book
	e.interactionService = services.NewInteractionService(e.dbClient, e.messageService, book)
}

// postInvestigationRun is a prepared entry: stage and execution records exist.
type postInvestigationRun struct {
	entry      config.PostInvestigationConfig
	resolved   *agent.ResolvedAgentConfig
	stageID    string
	stageIndex int
	execID     string
}

// RunAsync evaluates the gates of the session's chain entries and runs every
// passing entry in the background. It is trigger-agnostic: the caller decides
// when post-investigation should run. A no-op once the executor is stopped.
func (e *PostInvestigationExecutor) RunAsync(sessionID, scoreID string) {
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		slog.Info("Post-investigation executor stopped, skipping run",
			"session_id", sessionID, "score_id", scoreID)
		return
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	go func() {
		defer e.wg.Done()
		e.run(sessionID, scoreID)
	}()
}

// run loads the session and score, evaluates gates, creates records
// sequentially (stage indexes must not collide) and launches the entries.
func (e *PostInvestigationExecutor) run(sessionID, scoreID string) {
	logger := slog.With("session_id", sessionID, "score_id", scoreID)

	ctx, cancel := context.WithTimeout(context.Background(), postInvestigationPrepareTimeout)
	defer cancel()

	session, err := e.dbClient.AlertSession.Get(ctx, sessionID)
	if err != nil {
		logger.Error("Post-investigation: failed to load session", "error", err)
		return
	}
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		logger.Error("Post-investigation: failed to resolve chain", "chain_id", session.ChainID, "error", err)
		return
	}
	if len(chain.PostInvestigation) == 0 {
		return
	}
	score, err := e.dbClient.SessionScore.Get(ctx, scoreID)
	if err != nil {
		logger.Error("Post-investigation: failed to load score", "error", err)
		return
	}
	if score.Status != sessionscore.StatusCompleted {
		logger.Info("Post-investigation: score not completed, skipping", "score_status", score.Status)
		return
	}

	var runs []postInvestigationRun
	for _, entry := range chain.PostInvestigation {
		if ok, reason := evaluatePostInvestigationGate(entry, score.TotalScore, session.Labels); !ok {
			logger.Info("Post-investigation gate not met", "entry", entry.Name, "reason", reason)
			recordPostInvestigationOutcome(session.ChainID, entry.Name, postInvestigationOutcomeSkippedGate)
			continue
		}
		run, err := e.prepareEntry(ctx, session, chain, entry)
		if err != nil {
			logger.Error("Post-investigation: failed to prepare entry", "entry", entry.Name, "error", err)
			recordPostInvestigationOutcome(session.ChainID, entry.Name, postInvestigationOutcomeFailed)
			continue
		}
		runs = append(runs, run)
	}

	// Safe to Add here: this goroutine still holds a wg count, so Stop's
	// Wait cannot return before the entries are tracked.
	for _, r := range runs {
		e.wg.Add(1)
		go e.executeEntry(session, score, chain, r)
	}
}

// prepareEntry creates the stage and agent execution for one entry. A config
// resolution failure still leaves a failed stage behind for the audit trail.
func (e *PostInvestigationExecutor) prepareEntry(
	ctx context.Context,
	session *ent.AlertSession,
	chain *config.ChainConfig,
	entry config.PostInvestigationConfig,
) (postInvestigationRun, error) {
	stg, err := e.createStage(ctx, session.ID, entry.Name)
	if err != nil {
		return postInvestigationRun{}, err
	}

	resolved, err := agent.ResolvePostInvestigationConfig(e.cfg, chain, entry)
	if err != nil {
		errMsg := fmt.Sprintf("failed to resolve post-investigation config: %v", err)
		e.createFailedExecution(stg.ID, session.ID, entry.Agent, errMsg)
		e.finishStage(stg.ID, session.ID, entry.Name, stg.StageIndex, errMsg)
		return postInvestigationRun{}, errors.New(errMsg)
	}

	exec, err := e.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:         stg.ID,
		SessionID:       session.ID,
		AgentName:       resolved.AgentName,
		AgentIndex:      1,
		LLMBackend:      resolved.LLMBackend,
		LLMProvider:     resolved.LLMProviderName,
		ModelName:       resolved.ModelName(),
		ReasoningEffort: resolved.LLMProvider.EffectiveReasoningEffort(),
	})
	if err != nil {
		e.finishStage(stg.ID, session.ID, entry.Name, stg.StageIndex, err.Error())
		return postInvestigationRun{}, fmt.Errorf("failed to create agent execution: %w", err)
	}

	return postInvestigationRun{
		entry:      entry,
		resolved:   resolved,
		stageID:    stg.ID,
		stageIndex: stg.StageIndex,
		execID:     exec.ID,
	}, nil
}

// createStage allocates the next stage index and creates the stage, retrying
// when a concurrent writer took the same (session_id, stage_index).
func (e *PostInvestigationExecutor) createStage(ctx context.Context, sessionID, name string) (*ent.Stage, error) {
	var lastErr error
	for range postInvestigationStageAttempts {
		maxIndex, err := e.stageService.GetMaxStageIndex(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("failed to get max stage index: %w", err)
		}
		stg, err := e.stageService.CreateStage(ctx, models.CreateStageRequest{
			SessionID:          sessionID,
			StageName:          name,
			StageIndex:         maxIndex + 1,
			ExpectedAgentCount: 1,
			StageType:          string(stage.StageTypePostInvestigation),
		})
		if err == nil {
			return stg, nil
		}
		if !ent.IsConstraintError(err) {
			return nil, fmt.Errorf("failed to create post-investigation stage: %w", err)
		}
		lastErr = err
	}
	return nil, fmt.Errorf("failed to allocate stage index after %d attempts: %w", postInvestigationStageAttempts, lastErr)
}

// executeEntry runs one prepared entry under its own timeout.
func (e *PostInvestigationExecutor) executeEntry(
	session *ent.AlertSession,
	score *ent.SessionScore,
	chain *config.ChainConfig,
	r postInvestigationRun,
) {
	defer e.wg.Done()

	logger := slog.With("session_id", session.ID, "stage_id", r.stageID, "entry", r.entry.Name)
	logger.Info("Post-investigation: starting entry")

	ctx, cancel := context.WithTimeout(context.Background(), cmp.Or(r.entry.Timeout, postInvestigationDefaultTimeout))
	defer cancel()
	e.trackCancel(r.stageID, cancel)
	defer e.removeCancel(r.stageID)

	outcome := e.runEntry(ctx, logger, session, score, chain, r)
	recordPostInvestigationOutcome(session.ChainID, r.entry.Name, outcome)
	logger.Info("Post-investigation: entry finished", "outcome", outcome)

	e.scheduleEventCleanup(r.stageID, time.Now())
}

// runEntry executes the agent and drives the stage and execution to a
// terminal status. Returns the metric outcome.
func (e *PostInvestigationExecutor) runEntry(
	ctx context.Context,
	logger *slog.Logger,
	session *ent.AlertSession,
	score *ent.SessionScore,
	chain *config.ChainConfig,
	r postInvestigationRun,
) postInvestigationOutcome {
	fail := func(errMsg string) postInvestigationOutcome {
		e.failExecution(r, session.ID, errMsg)
		return postInvestigationOutcomeFailed
	}

	if err := e.stageService.UpdateAgentExecutionStatus(ctx, r.execID, agentexecution.StatusActive, ""); err != nil {
		logger.Warn("Failed to update agent execution to active", "error", err)
	}
	// Moves the stage to active with started_at (used by stale recovery).
	if err := e.stageService.UpdateStageStatus(ctx, r.stageID); err != nil {
		logger.Warn("Failed to update stage to active", "error", err)
	}
	publishExecutionStatus(ctx, e.eventPublisher, session.ID, r.stageID, r.execID, 1, string(agentexecution.StatusActive), "")
	publishStageStatus(ctx, e.eventPublisher, session.ID, r.stageID, r.entry.Name, r.stageIndex, stage.StageTypePostInvestigation, nil, events.StageStatusStarted)

	investigationContext := e.buildContext(ctx, session, score, r.entry)

	// The session's per-alert MCP override targets the investigation and is
	// not applied: the entry's resolved servers are used as-is.
	toolExecutor, failedServers := createToolExecutor(ctx, e.mcpFactory, r.resolved.MCPServers, nil, r.execID, logger)
	defer func() { _ = toolExecutor.Close() }()

	toolExecutor, subCollector, subCatalog, err := attachSubAgents(ctx, subAgentWiring{
		cfg:                e.cfg,
		chain:              chain,
		registry:           e.subAgentRegistry,
		refs:               r.entry.SubAgents, // chain-level sub_agents are not inherited
		agentName:          r.resolved.AgentName,
		parentExecID:       r.execID,
		stageID:            r.stageID,
		session:            session,
		agentFactory:       e.agentFactory,
		mcpFactory:         e.mcpFactory,
		llmClient:          e.llmClient,
		eventPublisher:     e.eventPublisher,
		promptBuilder:      e.promptBuilder,
		stageService:       e.stageService,
		timelineService:    e.timelineService,
		messageService:     e.messageService,
		interactionService: e.interactionService,
		memoryService:      e.memoryService,
		memoryConfig:       e.memoryConfig,
	}, toolExecutor)
	if err != nil {
		logger.Error("Failed to wire sub-agents", "error", err)
		return fail(err.Error())
	}

	if len(r.resolved.OnDemandSkills) > 0 && e.cfg.SkillRegistry != nil {
		toolExecutor = skill.NewSkillToolExecutor(toolExecutor, e.cfg.SkillRegistry, r.resolved.OnDemandSkillNameSet())
	}

	// Memory tools without a briefing, same as chat.
	if e.memoryService != nil && e.memoryConfig != nil {
		toolExecutor = memory.NewToolExecutor(toolExecutor, e.memoryService, session.ID, "default", nil)
	}

	agentExecCtx := &agent.ExecutionContext{
		SessionID:                      session.ID,
		StageID:                        r.stageID,
		ExecutionID:                    r.execID,
		AgentName:                      r.resolved.AgentName,
		AgentIndex:                     1,
		AlertData:                      session.AlertData,
		AlertType:                      session.AlertType,
		StageType:                      string(stage.StageTypePostInvestigation),
		Config:                         r.resolved,
		LLMClient:                      e.llmClient,
		ToolExecutor:                   toolExecutor,
		EventPublisher:                 e.eventPublisher,
		PromptBuilder:                  e.promptBuilder,
		FailedServers:                  failedServers,
		SubAgentCollector:              subCollector,
		SubAgentCatalog:                subCatalog,
		LLMProviders:                   e.cfg.LLMProviderRegistry,
		DefaultSummarization:           defaultSummarization(e.cfg),
		SummarizationFallbackProviders: agent.ResolveSummarizationFallback(e.cfg),
		Services: &agent.ServiceBundle{
			Timeline:    e.timelineService,
			Message:     e.messageService,
			Interaction: e.interactionService,
			Stage:       e.stageService,
		},
	}

	agentInstance, err := e.agentFactory.CreateAgent(agentExecCtx)
	if err != nil {
		logger.Error("Failed to create agent", "error", err)
		return fail(err.Error())
	}

	// The prompt builder renders the context under "Completed Investigation".
	result, execErr := agentInstance.Execute(ctx, agentExecCtx, investigationContext)

	agentStatus := agent.ExecutionStatusFailed
	errMsg := ""
	if execErr != nil {
		errMsg = execErr.Error()
		if ctx.Err() != nil {
			agentStatus = agent.StatusFromErr(ctx.Err())
		}
	} else if result != nil {
		if ctx.Err() != nil && !result.KeepCompletedWrapUp(ctx.Err()) {
			agentStatus = agent.StatusFromErr(ctx.Err())
		} else {
			agentStatus = result.Status
		}
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
	}

	// Parse the YES/NO marker into the stage's actions_executed and strip it
	// from the timeline. On parse failure the column stays NULL.
	actionsTaken := false
	if agentStatus == agent.ExecutionStatusCompleted && result != nil && result.FinalAnalysis != "" {
		taken, _, parseErr := controller.ExtractActionsTaken(result.FinalAnalysis)
		if parseErr != nil {
			logger.Warn("Failed to parse YES/NO marker from post-investigation stage", "error", parseErr)
		} else {
			actionsTaken = taken
			if setErr := e.stageService.SetActionsExecuted(context.Background(), r.stageID, taken); setErr != nil {
				logger.Error("Failed to persist actions_executed", "error", setErr)
			}
			stripActionMarkerFromTimeline(e.timelineService, r.execID, logger)
		}
	}

	entStatus := mapAgentStatusToEntStatus(agentStatus)
	if err := e.stageService.UpdateAgentExecutionStatus(context.Background(), r.execID, entStatus, errMsg); err != nil {
		logger.Error("Failed to update agent execution status", "error", err)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, session.ID, r.stageID, r.execID, 1, string(entStatus), errMsg)

	if err := e.stageService.UpdateStageStatus(context.Background(), r.stageID); err != nil {
		logger.Error("Failed to update stage status", "error", err)
	}
	publishStageStatus(context.Background(), e.eventPublisher, session.ID, r.stageID, r.entry.Name, r.stageIndex, stage.StageTypePostInvestigation, nil, mapChatAgentStatus(agentStatus))

	return postInvestigationOutcomeFor(agentStatus, actionsTaken)
}

// ────────────────────────────────────────────────────────────
// Gate and context
// ────────────────────────────────────────────────────────────

// evaluatePostInvestigationGate applies an entry's optional gate (AND of all
// set conditions). Returns false and a reason when the gate is not met.
// Labels compare case-insensitively; unlabeled sessions never match labels_any.
func evaluatePostInvestigationGate(entry config.PostInvestigationConfig, totalScore *int, labels []string) (bool, string) {
	if entry.MinScore != nil {
		if totalScore == nil {
			return false, "no total score"
		}
		if *totalScore < *entry.MinScore {
			return false, fmt.Sprintf("total score %d is below min_score %d", *totalScore, *entry.MinScore)
		}
	}
	if len(entry.LabelsAny) > 0 && !hasAnyLabel(labels, entry.LabelsAny) {
		return false, "session has none of labels_any"
	}
	if len(entry.LabelsNone) > 0 && hasAnyLabel(labels, entry.LabelsNone) {
		return false, "session has a label from labels_none"
	}
	return true, ""
}

func hasAnyLabel(labels, want []string) bool {
	for _, l := range labels {
		for _, w := range want {
			if strings.EqualFold(l, w) {
				return true
			}
		}
	}
	return false
}

// buildContext renders the completed investigation for the agent: session
// metadata, final report, executive summary, labels, scoring verdict and,
// with include_investigation, the full timeline. Alert data is not repeated
// here; the prompt builder renders it from the execution context.
func (e *PostInvestigationExecutor) buildContext(
	ctx context.Context,
	session *ent.AlertSession,
	score *ent.SessionScore,
	entry config.PostInvestigationConfig,
) string {
	var sb strings.Builder

	sb.WriteString("### Session\n\n")
	fmt.Fprintf(&sb, "- Session ID: %s\n", session.ID)
	fmt.Fprintf(&sb, "- Alert type: %s\n", session.AlertType)
	fmt.Fprintf(&sb, "- Chain: %s\n", session.ChainID)
	fmt.Fprintf(&sb, "- Status: %s\n", session.Status)
	if e.cfg.DashboardURL != "" {
		fmt.Fprintf(&sb, "- Link: %s/sessions/%s\n", e.cfg.DashboardURL, session.ID)
	}

	report := latestComposeReport(ctx, e.stageService, e.timelineService, session.ID)
	if report == "" {
		report = stringFromNillable(session.FinalAnalysis)
	}
	sb.WriteString("\n### Final Report\n\n")
	sb.WriteString(cmp.Or(report, "(no final report)"))
	sb.WriteString("\n")

	sb.WriteString("\n### Executive Summary\n\n")
	sb.WriteString(cmp.Or(stringFromNillable(session.ExecutiveSummary), "(none)"))
	sb.WriteString("\n")

	sb.WriteString("\n### Session Labels\n\nLabels: ")
	sb.WriteString(persistedLabelsValue(session.Labels))
	sb.WriteString("\n")

	sb.WriteString("\n### Scoring Verdict\n\n")
	if score.TotalScore != nil {
		fmt.Fprintf(&sb, "- Total score: %d/100\n", *score.TotalScore)
	} else {
		sb.WriteString("- Total score: (none)\n")
	}
	failureTags := "(none)"
	if len(score.FailureTags) > 0 {
		failureTags = strings.Join(score.FailureTags, ", ")
	}
	fmt.Fprintf(&sb, "- Failure tags: %s\n", failureTags)
	sb.WriteString("\n#### Score Analysis\n\n")
	sb.WriteString(cmp.Or(stringFromNillable(score.ScoreAnalysis), "(none)"))
	sb.WriteString("\n\n#### Tool Improvement Report\n\n")
	sb.WriteString(cmp.Or(stringFromNillable(score.ToolImprovementReport), "(none)"))
	sb.WriteString("\n")

	if entry.IncludeInvestigation {
		// The executive summary is already rendered above.
		sb.WriteString("\n### Full Investigation\n\n")
		sb.WriteString(e.contextBuilder.BuildInvestigation(ctx, session.ID, ""))
	}

	return sb.String()
}

// ────────────────────────────────────────────────────────────
// Terminal helpers
// ────────────────────────────────────────────────────────────

// failExecution marks the entry's execution and stage failed.
func (e *PostInvestigationExecutor) failExecution(r postInvestigationRun, sessionID, errMsg string) {
	if err := e.stageService.UpdateAgentExecutionStatus(
		context.Background(), r.execID, agentexecution.StatusFailed, errMsg,
	); err != nil {
		slog.Error("Failed to update agent execution status to failed", "execution_id", r.execID, "error", err)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, sessionID, r.stageID, r.execID, 1, string(agentexecution.StatusFailed), errMsg)
	e.finishStage(r.stageID, sessionID, r.entry.Name, r.stageIndex, errMsg)
}

// finishStage publishes a failed stage status and forces the stage failed
// (works without executions, unlike UpdateStageStatus).
func (e *PostInvestigationExecutor) finishStage(stageID, sessionID, stageName string, stageIndex int, errMsg string) {
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, stageName, stageIndex, stage.StageTypePostInvestigation, nil, events.StageStatusFailed)
	if err := e.stageService.ForceStageFailure(context.Background(), stageID, errMsg); err != nil {
		slog.Warn("Failed to mark post-investigation stage failed", "stage_id", stageID, "error", err)
	}
}

// createFailedExecution creates a best-effort failed AgentExecution for the
// audit trail when the entry fails before its execution record exists.
func (e *PostInvestigationExecutor) createFailedExecution(stageID, sessionID, agentName, errMsg string) {
	exec, err := e.stageService.CreateAgentExecution(context.Background(), models.CreateAgentExecutionRequest{
		StageID:    stageID,
		SessionID:  sessionID,
		AgentName:  agentName,
		AgentIndex: 1,
	})
	if err != nil {
		slog.Error("Failed to create failed agent execution record", "stage_id", stageID, "error", err)
		return
	}
	if err := e.stageService.UpdateAgentExecutionStatus(
		context.Background(), exec.ID, agentexecution.StatusFailed, errMsg,
	); err != nil {
		slog.Error("Failed to update agent execution status to failed", "execution_id", exec.ID, "error", err)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, sessionID, stageID, exec.ID, 1, string(agentexecution.StatusFailed), errMsg)
}

// postInvestigationOutcomeFor maps a terminal agent status to the metric outcome.
func postInvestigationOutcomeFor(status agent.ExecutionStatus, actionsTaken bool) postInvestigationOutcome {
	switch status {
	case agent.ExecutionStatusCompleted:
		if actionsTaken {
			return postInvestigationOutcomeCompletedActed
		}
		return postInvestigationOutcomeCompletedNoAction
	case agent.ExecutionStatusTimedOut:
		return postInvestigationOutcomeTimedOut
	case agent.ExecutionStatusCancelled:
		return postInvestigationOutcomeCancelled
	default:
		return postInvestigationOutcomeFailed
	}
}

func recordPostInvestigationOutcome(chainID, entryName string, outcome postInvestigationOutcome) {
	metrics.PostInvestigationRunsTotal.WithLabelValues(chainID, entryName, string(outcome)).Inc()
}

// MaxPostInvestigationTimeout returns the longest post-investigation entry
// timeout across all chains (default applied), or 0 when no chain has entries.
func MaxPostInvestigationTimeout(cfg *config.Config) time.Duration {
	var maxTimeout time.Duration
	if cfg.ChainRegistry == nil {
		return 0
	}
	for _, chain := range cfg.ChainRegistry.GetAll() {
		for _, entry := range chain.PostInvestigation {
			maxTimeout = max(maxTimeout, cmp.Or(entry.Timeout, postInvestigationDefaultTimeout))
		}
	}
	return maxTimeout
}

// ────────────────────────────────────────────────────────────
// Lifecycle
// ────────────────────────────────────────────────────────────

// trackCancel registers a cancel func for an in-flight entry.
// If the executor already force-cancelled, the cancel func is invoked immediately.
func (e *PostInvestigationExecutor) trackCancel(stageID string, cancel context.CancelFunc) {
	e.mu.Lock()
	if e.activeCancels != nil {
		e.activeCancels[stageID] = cancel
	} else {
		cancel()
	}
	e.mu.Unlock()
}

// removeCancel deregisters the cancel func for a finished entry.
func (e *PostInvestigationExecutor) removeCancel(stageID string) {
	e.mu.Lock()
	if e.activeCancels != nil {
		delete(e.activeCancels, stageID)
	}
	e.mu.Unlock()
}

// Stop marks the executor as stopped, waits up to gracePeriod for in-flight
// entries to complete naturally, then cancels any remaining contexts and
// waits for goroutines to drain.
func (e *PostInvestigationExecutor) Stop(gracePeriod time.Duration) {
	e.mu.Lock()
	e.stopped = true
	for _, t := range e.cleanupTimers {
		t.Stop()
	}
	e.cleanupTimers = nil
	e.mu.Unlock()

	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return
	case <-time.After(gracePeriod):
		slog.Warn("Post-investigation grace period expired, cancelling in-flight runs")
	}

	e.mu.Lock()
	for _, cancel := range e.activeCancels {
		cancel()
	}
	e.activeCancels = nil
	e.mu.Unlock()

	<-done
}

// scheduleEventCleanup schedules deletion of transient Event records after a
// grace period. The timer is tracked so Stop() can cancel it.
func (e *PostInvestigationExecutor) scheduleEventCleanup(stageID string, cutoff time.Time) {
	timer := time.AfterFunc(60*time.Second, func() {
		e.mu.RLock()
		stopped := e.stopped
		e.mu.RUnlock()
		if stopped {
			return
		}

		stg, err := e.stageService.GetStageByID(context.Background(), stageID, false)
		if err != nil {
			slog.Warn("Failed to get stage for post-investigation event cleanup", "stage_id", stageID, "error", err)
			return
		}
		if _, err := e.dbClient.Event.Delete().
			Where(
				event.SessionIDEQ(stg.SessionID),
				event.CreatedAtLTE(cutoff),
			).
			Exec(context.Background()); err != nil {
			slog.Warn("Failed to cleanup post-investigation stage events", "stage_id", stageID, "error", err)
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
