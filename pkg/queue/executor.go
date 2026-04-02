package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/orchestrator"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/agent/skill"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// RealSessionExecutor implements SessionExecutor using the agent framework.
type RealSessionExecutor struct {
	cfg              *config.Config
	dbClient         *ent.Client
	llmClient        agent.LLMClient
	eventPublisher   agent.EventPublisher
	agentFactory     *agent.AgentFactory
	promptBuilder    *prompt.PromptBuilder
	mcpFactory       *mcp.ClientFactory
	runbookService   *runbook.Service
	subAgentRegistry *config.SubAgentRegistry
	memoryService    *memory.Service
	memoryConfig     *config.MemoryConfig
}

// NewRealSessionExecutor creates a new session executor.
// eventPublisher may be nil (streaming disabled).
// mcpFactory may be nil (MCP disabled — uses stub tool executor).
// runbookService may be nil (uses config default runbook content).
// memoryService and memoryConfig may be nil (memory disabled).
func NewRealSessionExecutor(cfg *config.Config, dbClient *ent.Client, llmClient agent.LLMClient, eventPublisher agent.EventPublisher, mcpFactory *mcp.ClientFactory, runbookService *runbook.Service, memoryService *memory.Service, memoryConfig *config.MemoryConfig) *RealSessionExecutor {
	controllerFactory := controller.NewFactory()
	return &RealSessionExecutor{
		cfg:              cfg,
		dbClient:         dbClient,
		llmClient:        llmClient,
		eventPublisher:   eventPublisher,
		agentFactory:     agent.NewAgentFactory(controllerFactory),
		promptBuilder:    prompt.NewPromptBuilder(cfg.MCPServerRegistry),
		mcpFactory:       mcpFactory,
		runbookService:   runbookService,
		subAgentRegistry: config.BuildSubAgentRegistry(cfg.AgentRegistry.GetAll()),
		memoryService:    memoryService,
		memoryConfig:     memoryConfig,
	}
}

// resolveRunbook resolves runbook content for a session using the RunbookService.
// Falls back to config defaults on error or when the service is nil.
func (e *RealSessionExecutor) resolveRunbook(ctx context.Context, session *ent.AlertSession) string {
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
		slog.Warn("Runbook resolution failed, using default",
			"session_id", session.ID,
			"error", err)
		return configDefault
	}
	return content
}

// ────────────────────────────────────────────────────────────
// Internal types
// ────────────────────────────────────────────────────────────

// stageResult captures the outcome of a single stage execution.
type stageResult struct {
	stageID           string
	stageName         string
	stageType         stage.StageType
	referencedStageID *string
	status            alertsession.Status // mapped from agent status
	finalAnalysis     string
	err               error
	agentResults      []agentResult // always populated (1 entry for single-agent, N for multi-agent)
}

// agentResult captures the outcome of a single agent execution within a stage.
type agentResult struct {
	executionID     string
	status          agent.ExecutionStatus
	finalAnalysis   string
	err             error
	llmBackend      string // resolved backend (for synthesis context)
	llmProviderName string // resolved provider name (for synthesis context)
}

// executionConfig wraps agent config with display name for stage execution.
type executionConfig struct {
	agentConfig config.StageAgentConfig
	displayName string // for DB record and logs (differs from config name for replicas)
}

// indexedAgentResult pairs an agentResult with its original launch index.
type indexedAgentResult struct {
	index  int
	result agentResult
}

// executeStageInput groups all parameters for executeStage to keep the call signature clean.
type executeStageInput struct {
	session     *ent.AlertSession
	chain       *config.ChainConfig
	stageConfig config.StageConfig
	stageIndex  int // 0-based DB stage index (includes synthesis stages)
	prevContext string

	// Total expected stages (config + synthesis + executive summary).
	// Used for progress reporting so CurrentStageIndex never exceeds TotalStages.
	totalExpectedStages int

	// Precomputed once per session
	runbookContent string

	// Services (shared across stages)
	stageService       *services.StageService
	messageService     *services.MessageService
	timelineService    *services.TimelineService
	interactionService *services.InteractionService
}

// ────────────────────────────────────────────────────────────
// Execute — main entry point (chain loop)
// ────────────────────────────────────────────────────────────

// Execute runs the session through the agent chain.
// Stages are executed sequentially. On any stage failure, the chain stops (fail-fast).
// After all stages complete, an executive summary is generated (fail-open).
func (e *RealSessionExecutor) Execute(ctx context.Context, session *ent.AlertSession) *ExecutionResult {
	logger := slog.With(
		"session_id", session.ID,
		"chain_id", session.ChainID,
		"alert_type", session.AlertType,
		"alert_data_bytes", len(session.AlertData),
	)
	logger.Info("Session executor: starting execution")

	// 1. Resolve chain configuration
	chain, err := e.cfg.GetChain(session.ChainID)
	if err != nil {
		logger.Error("Failed to resolve chain config", "error", err)
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("chain %q not found: %w", session.ChainID, err),
		}
	}

	if len(chain.Stages) == 0 {
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("chain %q has no stages", session.ChainID),
		}
	}

	// 2. Initialize services and resolve runbook (shared across all stages)
	stageService := services.NewStageService(e.dbClient)
	messageService := services.NewMessageService(e.dbClient)
	timelineService := services.NewTimelineService(e.dbClient)
	interactionService := services.NewInteractionService(e.dbClient, messageService)
	runbookContent := e.resolveRunbook(ctx, session)

	// 3. Sequential chain loop
	// dbStageIndex tracks the actual DB stage index, which may differ from the
	// config stage index when synthesis stages are inserted.
	// totalExpectedStages includes config stages + synthesis + executive summary,
	// so progress reporting never shows CurrentStageIndex > TotalStages.
	var completedStages []stageResult
	prevContext := ""
	dbStageIndex := 0
	totalExpectedStages := countExpectedStages(chain)

	for _, stageCfg := range chain.Stages {
		// Check for cancellation between stages
		if r := e.mapCancellation(ctx); r != nil {
			return r
		}

		// session progress + stage.status: started are published inside executeStage()
		// after Stage DB record is created (so stageID is always present)
		sr := e.executeStage(ctx, executeStageInput{
			session:             session,
			chain:               chain,
			stageConfig:         stageCfg,
			stageIndex:          dbStageIndex,
			prevContext:         prevContext,
			totalExpectedStages: totalExpectedStages,
			runbookContent:      runbookContent,
			stageService:        stageService,
			messageService:      messageService,
			timelineService:     timelineService,
			interactionService:  interactionService,
		})

		// Publish stage terminal status (use background context — ctx may be cancelled)
		publishStageStatus(context.Background(), e.eventPublisher, session.ID, sr.stageID, sr.stageName, dbStageIndex, sr.stageType, sr.referencedStageID, mapTerminalStatus(sr))
		dbStageIndex++

		// Fail-fast: if stage didn't complete, stop the chain
		if sr.status != alertsession.StatusCompleted {
			if r := e.mapCancellation(ctx); r != nil {
				return r
			}
			logger.Warn("Stage failed, stopping chain",
				"stage_name", sr.stageName,
				"stage_status", sr.status,
				"error", sr.err,
			)
			return &ExecutionResult{
				Status: sr.status,
				Error:  sr.err,
			}
		}

		// Synthesis runs after stages with >1 agent (mandatory, no opt-out)
		if len(sr.agentResults) > 1 {
			synthSr := e.executeSynthesisStage(ctx, executeStageInput{
				session:             session,
				chain:               chain,
				stageConfig:         stageCfg,
				stageIndex:          dbStageIndex,
				prevContext:         prevContext,
				totalExpectedStages: totalExpectedStages,
				runbookContent:      runbookContent,
				stageService:        stageService,
				messageService:      messageService,
				timelineService:     timelineService,
				interactionService:  interactionService,
			}, sr)

			// Publish synthesis stage terminal status (use background context — ctx may be cancelled)
			publishStageStatus(context.Background(), e.eventPublisher, session.ID, synthSr.stageID, synthSr.stageName, dbStageIndex, synthSr.stageType, synthSr.referencedStageID, mapTerminalStatus(synthSr))
			dbStageIndex++

			if synthSr.status != alertsession.StatusCompleted {
				if r := e.mapCancellation(ctx); r != nil {
					return r
				}
				logger.Warn("Synthesis failed, stopping chain",
					"stage_name", synthSr.stageName,
					"stage_status", synthSr.status,
					"error", synthSr.err,
				)
				return &ExecutionResult{
					Status: synthSr.status,
					Error:  synthSr.err,
				}
			}

			// Synthesis result replaces investigation result for context passing
			completedStages = append(completedStages, synthSr)
		} else {
			completedStages = append(completedStages, sr)
		}

		// Build context for next stage
		prevContext = e.buildStageContext(completedStages)
	}

	// 4. Extract final analysis from completed stages
	finalAnalysis := extractFinalAnalysis(completedStages)

	// 5. Generate executive summary as a typed stage (fail-open).
	// Only run when there is a final analysis to summarize.
	var execSummary string
	var execSummaryErr string
	if finalAnalysis != "" {
		execSr := e.executeExecSummaryStage(ctx, executeStageInput{
			session:             session,
			chain:               chain,
			stageIndex:          dbStageIndex,
			prevContext:         finalAnalysis, // ExecSummaryController reads this as the text to summarize
			totalExpectedStages: totalExpectedStages,
			runbookContent:      runbookContent,
			stageService:        stageService,
			messageService:      messageService,
			timelineService:     timelineService,
			interactionService:  interactionService,
		})
		publishStageStatus(context.Background(), e.eventPublisher, session.ID, execSr.stageID, execSr.stageName, dbStageIndex, execSr.stageType, execSr.referencedStageID, mapTerminalStatus(execSr))
		if execSr.status == alertsession.StatusCompleted {
			execSummary = execSr.finalAnalysis
		} else if execSr.err != nil {
			logger.Warn("Executive summary stage failed (fail-open)", "error", execSr.err)
			execSummaryErr = execSr.err.Error()
		}
	}

	if r := e.mapCancellation(ctx); r != nil {
		return r
	}

	logger.Info("Session executor: execution completed",
		"stages_completed", len(completedStages),
		"has_final_analysis", finalAnalysis != "",
		"has_executive_summary", execSummary != "",
	)

	return &ExecutionResult{
		Status:                alertsession.StatusCompleted,
		FinalAnalysis:         finalAnalysis,
		ExecutiveSummary:      execSummary,
		ExecutiveSummaryError: execSummaryErr,
	}
}

// ────────────────────────────────────────────────────────────
// executeStage — unified stage execution (1 or N agents)
// ────────────────────────────────────────────────────────────

// executeStage creates the Stage DB record, launches goroutines for all agents,
// collects results, and aggregates status via success policy.
// A single-agent stage is not a special case — it's just N=1.
func (e *RealSessionExecutor) executeStage(ctx context.Context, input executeStageInput) stageResult {
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_name", input.stageConfig.Name,
		"stage_index", input.stageIndex,
	)

	if len(input.stageConfig.Agents) == 0 {
		return stageResult{
			stageName: input.stageConfig.Name,
			stageType: stage.StageTypeInvestigation,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("stage %q has no agents", input.stageConfig.Name),
		}
	}

	// 1. Build execution configs (1 for single-agent, N for multi-agent/replica)
	configs := buildConfigs(input.stageConfig)
	policy := e.resolvedSuccessPolicy(input)

	// 2. Derive stage type from agent types
	stageType := stage.StageTypeInvestigation
	if e.allAgentsAreAction(input.stageConfig) {
		stageType = stage.StageTypeAction
	}

	// 3. Create Stage DB record
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          input.stageConfig.Name,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: len(configs),
		ParallelType:       parallelTypePtr(input.stageConfig),
		SuccessPolicy:      successPolicyPtr(input.stageConfig, policy),
		StageType:          string(stageType),
	})
	if err != nil {
		if r := e.mapCancellation(ctx); r != nil {
			return stageResult{stageName: input.stageConfig.Name, stageType: stage.StageTypeInvestigation, status: r.Status, err: r.Error}
		}
		logger.Error("Failed to create stage", "error", err)
		return stageResult{
			stageName: input.stageConfig.Name,
			stageType: stage.StageTypeInvestigation,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create stage: %w", err),
		}
	}

	// 4. Update session progress + publish stage.status: started (stageID now available)
	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, input.stageConfig.Name, input.stageIndex, stg.StageType, stg.ReferencedStageID, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, input.stageConfig.Name,
		input.stageIndex, input.totalExpectedStages, len(configs),
		fmt.Sprintf("Starting stage: %s", input.stageConfig.Name))

	// 5. Launch goroutines (one per execution config — even if just one)
	results := make(chan indexedAgentResult, len(configs))
	var wg sync.WaitGroup

	for i, cfg := range configs {
		wg.Add(1)
		go func(idx int, agentCfg config.StageAgentConfig, displayName string) {
			defer wg.Done()
			ar := e.executeAgent(ctx, input, stg, agentCfg, idx, displayName)
			results <- indexedAgentResult{index: idx, result: ar}
		}(i, cfg.agentConfig, cfg.displayName)
	}

	// 6. Wait for ALL goroutines to complete
	wg.Wait()
	close(results)

	// 7. Collect and sort by original index
	agentResults := collectAndSort(results)

	// 8. Aggregate status via success policy
	stageStatus := aggregateStatus(agentResults, policy)

	// 9. Update Stage in DB (use background context — ctx may be cancelled)
	if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
		logger.Error("Failed to update stage status", "error", updateErr)
	}

	// For single-agent stages, finalAnalysis comes directly from the agent.
	// For multi-agent stages, synthesis produces it (chain loop handles this).
	finalAnalysis := ""
	if len(agentResults) == 1 {
		finalAnalysis = agentResults[0].finalAnalysis
	}

	// For completed action stages, parse the YES/NO marker from the
	// final analysis, strip it, and persist the boolean on the stage record.
	// Also clean the marker from timeline events so traces show clean text
	// (raw LLM output is preserved in LLM interactions).
	if stageType == stage.StageTypeAction && stageStatus == alertsession.StatusCompleted && finalAnalysis != "" {
		taken, cleaned, parseErr := controller.ExtractActionsTaken(finalAnalysis)
		if parseErr != nil {
			logger.Warn("Failed to parse YES/NO marker from action stage",
				"stage_id", stg.ID, "error", parseErr)
		} else {
			finalAnalysis = cleaned
			if setErr := input.stageService.SetActionsExecuted(context.Background(), stg.ID, taken); setErr != nil {
				logger.Error("Failed to persist actions_executed", "stage_id", stg.ID, "error", setErr)
			}
			if len(agentResults) == 1 {
				stripActionMarkerFromTimeline(input.timelineService, agentResults[0].executionID, logger)
			}
		}
	}

	return stageResult{
		stageID:       stg.ID,
		stageName:     input.stageConfig.Name,
		stageType:     stg.StageType,
		status:        stageStatus,
		finalAnalysis: finalAnalysis,
		err:           aggregateError(agentResults, stageStatus, input.stageConfig),
		agentResults:  agentResults,
	}
}

// stripActionMarkerFromTimeline removes the YES/NO marker from
// final_analysis and llm_response timeline events for the given execution.
// Best-effort: logs warnings on failure but never blocks the pipeline.
func stripActionMarkerFromTimeline(timelineService *services.TimelineService, executionID string, logger *slog.Logger) {
	events, err := timelineService.GetAgentTimeline(context.Background(), executionID)
	if err != nil {
		logger.Warn("Failed to get timeline for action marker cleanup", "execution_id", executionID, "error", err)
		return
	}
	for _, evt := range events {
		if evt.EventType != timelineevent.EventTypeFinalAnalysis && evt.EventType != timelineevent.EventTypeLlmResponse {
			continue
		}
		_, evtCleaned, extractErr := controller.ExtractActionsTaken(evt.Content)
		if extractErr != nil {
			continue
		}
		if evtCleaned == "" {
			continue
		}
		if updateErr := timelineService.UpdateTimelineEvent(context.Background(), evt.ID, evtCleaned); updateErr != nil {
			logger.Warn("Failed to strip action marker from timeline event",
				"event_id", evt.ID, "event_type", evt.EventType, "error", updateErr)
		}
	}
}

// ────────────────────────────────────────────────────────────
// executeAgent — single agent execution within a stage
// ────────────────────────────────────────────────────────────

func (e *RealSessionExecutor) executeAgent(
	ctx context.Context,
	input executeStageInput,
	stg *ent.Stage,
	agentConfig config.StageAgentConfig,
	agentIndex int,
	displayName string, // overrides agentConfig.Name for DB record/logs; config name still used for registry lookup
) agentResult {
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_id", stg.ID,
		"agent_name", displayName,
		"agent_index", agentIndex,
	)

	// Best-effort provider/backend for the error path (before ResolveAgentConfig
	// succeeds). The happy path uses resolvedConfig instead, keeping
	// ResolveAgentConfig as the single source of truth.
	var fallbackProviderName string
	fallbackBackend := agent.DefaultLLMBackend
	if e.cfg.Defaults != nil {
		fallbackProviderName = e.cfg.Defaults.LLMProvider
		if e.cfg.Defaults.LLMBackend != "" {
			fallbackBackend = e.cfg.Defaults.LLMBackend
		}
	}
	if input.chain.LLMProvider != "" {
		fallbackProviderName = input.chain.LLMProvider
	}
	if agentConfig.LLMProvider != "" {
		fallbackProviderName = agentConfig.LLMProvider
	}
	if input.chain.LLMBackend != "" {
		fallbackBackend = input.chain.LLMBackend
	}
	if agentConfig.LLMBackend != "" {
		fallbackBackend = agentConfig.LLMBackend
	}

	// Resolve agent config from hierarchy (before creating execution record
	// so the DB record captures the correctly resolved iteration strategy).
	resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, input.chain, input.stageConfig, agentConfig)
	if err != nil {
		resErr := fmt.Errorf("failed to resolve agent config: %w", err)
		logger.Error("Failed to resolve agent config", "error", err)

		// Best-effort: create a failed AgentExecution record so the stage can
		// be finalized via UpdateStageStatus. Without this, the stage has no
		// executions and UpdateStageStatus is a no-op, leaving it "pending".
		exec, createErr := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
			StageID:     stg.ID,
			SessionID:   input.session.ID,
			AgentName:   displayName,
			AgentIndex:  agentIndex + 1, // 1-based in DB
			LLMBackend:  fallbackBackend,
			LLMProvider: fallbackProviderName,
		})
		if createErr != nil {
			logger.Error("Failed to create failed agent execution record", "error", createErr)
			// Last resort: directly mark stage as failed so the pipeline doesn't stay in_progress.
			if stageErr := input.stageService.ForceStageFailure(context.Background(), stg.ID, resErr.Error()); stageErr != nil {
				logger.Error("Failed to force stage to failed state", "error", stageErr)
			}
			return agentResult{
				status: agent.ExecutionStatusFailed,
				err:    resErr,
			}
		}
		// Mark the execution as failed with the resolution error.
		if updateErr := input.stageService.UpdateAgentExecutionStatus(
			context.Background(), exec.ID, agentexecution.StatusFailed, resErr.Error(),
		); updateErr != nil {
			logger.Error("Failed to update agent execution status to failed", "error", updateErr)
		}
		publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(agentexecution.StatusFailed), resErr.Error())
		return agentResult{
			executionID:     exec.ID,
			status:          agent.ExecutionStatusFailed,
			err:             resErr,
			llmBackend:      string(fallbackBackend),
			llmProviderName: fallbackProviderName,
		}
	}

	// Create AgentExecution DB record with resolved strategy and provider
	exec, err := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:     stg.ID,
		SessionID:   input.session.ID,
		AgentName:   displayName,
		AgentIndex:  agentIndex + 1, // 1-based in DB
		LLMBackend:  resolvedConfig.LLMBackend,
		LLMProvider: resolvedConfig.LLMProviderName,
	})
	if err != nil {
		logger.Error("Failed to create agent execution", "error", err)
		return agentResult{
			status:          agent.ExecutionStatusFailed,
			err:             fmt.Errorf("failed to create agent execution: %w", err),
			llmBackend:      string(resolvedConfig.LLMBackend),
			llmProviderName: resolvedConfig.LLMProviderName,
		}
	}

	// Mark execution as active and notify the frontend immediately so it can
	// track this agent as non-terminal while it runs.
	if updateErr := input.stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusActive, ""); updateErr != nil {
		logger.Warn("Failed to update agent execution to active", "error", updateErr)
	}
	publishExecutionStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(agentexecution.StatusActive), "")

	// Metadata carried on all agentResult returns below (for synthesis context).
	resolvedBackend := string(resolvedConfig.LLMBackend)

	// Resolve MCP servers and tool filter
	serverIDs, toolFilter, err := resolveMCPSelection(input.session, resolvedConfig, e.cfg.MCPServerRegistry)
	if err != nil {
		logger.Error("Failed to resolve MCP selection", "error", err)
		failErr := fmt.Errorf("invalid MCP selection: %w", err)
		if updateErr := input.stageService.UpdateAgentExecutionStatus(
			context.Background(), exec.ID, agentexecution.StatusFailed, failErr.Error(),
		); updateErr != nil {
			logger.Error("Failed to update agent execution status after MCP error", "error", updateErr)
		}
		publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(agentexecution.StatusFailed), failErr.Error())
		return agentResult{
			executionID:     exec.ID,
			status:          agent.ExecutionStatusFailed,
			err:             failErr,
			llmBackend:      resolvedBackend,
			llmProviderName: resolvedConfig.LLMProviderName,
		}
	}

	// Create MCP tool executor
	toolExecutor, failedServers := createToolExecutor(ctx, e.mcpFactory, serverIDs, toolFilter, logger)
	defer func() { _ = toolExecutor.Close() }()

	// Retrieve memories for auto-injection into system prompt (only for agent types
	// whose prompts consume MemoryBriefing — investigation, action, orchestrator).
	var memoryBriefing *agent.MemoryBriefing
	if e.memoryService != nil && e.memoryConfig != nil && agentTypeSupportsMemory(resolvedConfig.Type) {
		memoryBriefing = e.retrieveMemories(ctx, input.session, logger)
		if memoryBriefing != nil {
			if edgeErr := e.dbClient.AlertSession.UpdateOneID(input.session.ID).
				AddInjectedMemoryIDs(memoryBriefing.InjectedIDs...).
				Exec(ctx); edgeErr != nil {
				logger.Warn("Failed to record injected memory IDs", "error", edgeErr)
			}
		}
	}

	// Build execution context
	execCtx := &agent.ExecutionContext{
		SessionID:      input.session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		AgentName:      displayName,
		AgentIndex:     agentIndex + 1, // 1-based
		AlertData:      input.session.AlertData,
		AlertType:      input.session.AlertType,
		StageType:      string(stg.StageType),
		RunbookContent: input.runbookContent,
		Config:         resolvedConfig,
		LLMClient:      e.llmClient,
		EventPublisher: e.eventPublisher,
		PromptBuilder:  e.promptBuilder,
		FailedServers:  failedServers,
		MemoryBriefing: memoryBriefing,
		Services: &agent.ServiceBundle{
			Timeline:    input.timelineService,
			Message:     input.messageService,
			Interaction: input.interactionService,
			Stage:       input.stageService,
		},
	}

	subAgentRefs := resolveSubAgents(input.chain, input.stageConfig, agentConfig)
	if len(subAgentRefs) > 0 {
		registry := e.subAgentRegistry.Filter(subAgentRefs.Names())
		if len(registry.Entries()) > 0 {
			agentDef, getErr := e.cfg.GetAgent(agentConfig.Name)
			if getErr != nil {
				failErr := fmt.Errorf("failed to get agent config for orchestration: %w", getErr)
				logger.Error("Failed to get agent definition for orchestration", "error", getErr)
				if updateErr := input.stageService.UpdateAgentExecutionStatus(
					context.Background(), exec.ID, agentexecution.StatusFailed, failErr.Error(),
				); updateErr != nil {
					logger.Error("Failed to update agent execution status", "error", updateErr)
				}
				publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(agentexecution.StatusFailed), failErr.Error())
				return agentResult{
					executionID:     exec.ID,
					status:          agent.ExecutionStatusFailed,
					err:             failErr,
					llmBackend:      resolvedBackend,
					llmProviderName: resolvedConfig.LLMProviderName,
				}
			}

			guardrails := resolveOrchestratorGuardrails(e.cfg, agentDef)

			deps := &orchestrator.SubAgentDeps{
				Config:             e.cfg,
				Chain:              input.chain,
				AgentFactory:       e.agentFactory,
				MCPFactory:         e.mcpFactory,
				LLMClient:          e.llmClient,
				EventPublisher:     e.eventPublisher,
				PromptBuilder:      e.promptBuilder,
				StageService:       input.stageService,
				TimelineService:    input.timelineService,
				MessageService:     input.messageService,
				InteractionService: input.interactionService,
				AlertData:          input.session.AlertData,
				AlertType:          input.session.AlertType,
				RunbookContent:     input.runbookContent,
				WrapToolExecutor:   e.memoryToolWrapper(input.session),
			}

			runner := orchestrator.NewSubAgentRunner(ctx, deps, exec.ID, input.session.ID, stg.ID, registry, guardrails, subAgentRefs)
			toolExecutor = orchestrator.NewCompositeToolExecutor(toolExecutor, runner, registry)
			execCtx.SubAgentCollector = orchestrator.NewResultCollector(runner)
			execCtx.SubAgentCatalog = applyCatalogOverrides(registry.Entries(), subAgentRefs)
		}
	}

	// Wrap with skill tool executor (after orchestrator)
	if len(resolvedConfig.OnDemandSkills) > 0 && e.cfg.SkillRegistry != nil {
		toolExecutor = skill.NewSkillToolExecutor(toolExecutor, e.cfg.SkillRegistry, resolvedConfig.OnDemandSkillNameSet())
	}

	// Wrap with memory tool executor (outermost layer — same agent-type guard)
	if e.memoryService != nil && e.memoryConfig != nil && agentTypeSupportsMemory(resolvedConfig.Type) {
		excludeIDs := memoryExcludeIDs(memoryBriefing)
		toolExecutor = memory.NewToolExecutor(
			toolExecutor, e.memoryService, input.session.ID, "default", excludeIDs,
		)
	}

	execCtx.ToolExecutor = toolExecutor

	agentInstance, err := e.agentFactory.CreateAgent(execCtx)
	if err != nil {
		logger.Error("Failed to create agent", "error", err)
		failErr := fmt.Errorf("failed to create agent: %w", err)
		if updateErr := input.stageService.UpdateAgentExecutionStatus(
			context.Background(), exec.ID, agentexecution.StatusFailed, failErr.Error(),
		); updateErr != nil {
			logger.Error("Failed to update agent execution status after agent creation error", "error", updateErr)
		}
		publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(agentexecution.StatusFailed), failErr.Error())
		return agentResult{
			executionID:     exec.ID,
			status:          agent.ExecutionStatusFailed,
			err:             failErr,
			llmBackend:      resolvedBackend,
			llmProviderName: resolvedConfig.LLMProviderName,
		}
	}

	result, err := agentInstance.Execute(ctx, execCtx, input.prevContext)
	if err != nil {
		// Determine whether the error was caused by context cancellation/timeout.
		// When the context is cancelled (e.g. user cancel), the agent may fail with
		// an unrelated error (e.g. "failed to store assistant message") because it
		// tried to operate on a cancelled context. Override to the correct status.
		errStatus := agent.StatusFromErr(ctx.Err())
		entErrStatus := mapAgentStatusToEntStatus(errStatus)
		logger.Error("Agent execution error", "error", err, "resolved_status", errStatus)
		if updateErr := input.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entErrStatus, err.Error()); updateErr != nil {
			logger.Error("Failed to update agent execution status after error", "error", updateErr)
		}
		publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(entErrStatus), err.Error())
		return agentResult{
			executionID:     exec.ID,
			status:          errStatus,
			err:             err,
			llmBackend:      resolvedBackend,
			llmProviderName: resolvedConfig.LLMProviderName,
		}
	}

	// When the session context is cancelled/timed-out, the agent may return a
	// misleading status (e.g. "failed" due to a validation error caused by an
	// empty LLM response, or "completed" with empty content). Override to the
	// correct terminal status based on ctx.Err(). Only skip the override if the
	// agent already reported the right cancellation/timeout status.
	if result != nil && ctx.Err() != nil &&
		result.Status != agent.ExecutionStatusCancelled &&
		result.Status != agent.ExecutionStatusTimedOut {
		result.Status = agent.StatusFromErr(ctx.Err())
		result.Error = ctx.Err()
	}

	// Update AgentExecution status (use background context — ctx may be cancelled)
	entStatus := mapAgentStatusToEntStatus(result.Status)
	errMsg := ""
	if result.Error != nil {
		errMsg = result.Error.Error()
	}
	if updateErr := input.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entStatus, errMsg); updateErr != nil {
		logger.Error("Failed to update agent execution status", "error", updateErr)
		return agentResult{
			executionID:     exec.ID,
			status:          agent.ExecutionStatusFailed,
			finalAnalysis:   result.FinalAnalysis,
			err:             fmt.Errorf("agent completed but status update failed: %w", updateErr),
			llmBackend:      resolvedBackend,
			llmProviderName: resolvedConfig.LLMProviderName,
		}
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, input.session.ID, stg.ID, exec.ID, agentIndex+1, string(entStatus), errMsg)

	return agentResult{
		executionID:     exec.ID,
		status:          result.Status,
		finalAnalysis:   result.FinalAnalysis,
		err:             result.Error,
		llmBackend:      resolvedBackend,
		llmProviderName: resolvedConfig.LLMProviderName,
	}
}

// allAgentsAreAction returns true if every agent in the stage resolves to AgentTypeAction.
// Uses the same resolution order as ResolveAgentConfig: stage override > agent definition.
// Returns false on any error (e.g. agent not found) — the error will be caught later
// by ResolveAgentConfig in executeAgent.
func (e *RealSessionExecutor) allAgentsAreAction(stageConfig config.StageConfig) bool {
	for _, ac := range stageConfig.Agents {
		agentType := ac.Type
		if agentType == "" {
			agentDef, err := e.cfg.GetAgent(ac.Name)
			if err != nil {
				return false
			}
			agentType = agentDef.Type
		}
		if agentType != config.AgentTypeAction {
			return false
		}
	}
	return true
}
