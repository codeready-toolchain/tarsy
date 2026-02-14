package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	agentctx "github.com/codeready-toolchain/tarsy/pkg/agent/context"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// RealSessionExecutor implements SessionExecutor using the agent framework.
type RealSessionExecutor struct {
	cfg            *config.Config
	dbClient       *ent.Client
	llmClient      agent.LLMClient
	eventPublisher agent.EventPublisher
	agentFactory   *agent.AgentFactory
	promptBuilder  *prompt.PromptBuilder
	mcpFactory     *mcp.ClientFactory
}

// NewRealSessionExecutor creates a new session executor.
// eventPublisher may be nil (streaming disabled).
// mcpFactory may be nil (MCP disabled — uses stub tool executor).
func NewRealSessionExecutor(cfg *config.Config, dbClient *ent.Client, llmClient agent.LLMClient, eventPublisher agent.EventPublisher, mcpFactory *mcp.ClientFactory) *RealSessionExecutor {
	controllerFactory := controller.NewFactory()
	return &RealSessionExecutor{
		cfg:            cfg,
		dbClient:       dbClient,
		llmClient:      llmClient,
		eventPublisher: eventPublisher,
		agentFactory:   agent.NewAgentFactory(controllerFactory),
		promptBuilder:  prompt.NewPromptBuilder(cfg.MCPServerRegistry),
		mcpFactory:     mcpFactory,
	}
}

// ────────────────────────────────────────────────────────────
// Internal types
// ────────────────────────────────────────────────────────────

// stageResult captures the outcome of a single stage execution.
type stageResult struct {
	stageID       string
	stageName     string
	status        alertsession.Status // mapped from agent status
	finalAnalysis string
	err           error
	agentResults  []agentResult // always populated (1 entry for single-agent, N for multi-agent)
}

// agentResult captures the outcome of a single agent execution within a stage.
type agentResult struct {
	executionID       string
	status            agent.ExecutionStatus
	finalAnalysis     string
	err               error
	iterationStrategy string // resolved strategy (for synthesis context)
	llmProviderName   string // resolved provider name (for synthesis context)
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

	// 2. Initialize services (shared across all stages)
	stageService := services.NewStageService(e.dbClient)
	messageService := services.NewMessageService(e.dbClient)
	timelineService := services.NewTimelineService(e.dbClient)
	interactionService := services.NewInteractionService(e.dbClient, messageService)

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
			stageService:        stageService,
			messageService:      messageService,
			timelineService:     timelineService,
			interactionService:  interactionService,
		})

		// Publish stage terminal status (use background context — ctx may be cancelled)
		publishStageStatus(context.Background(), e.eventPublisher, session.ID, sr.stageID, sr.stageName, dbStageIndex, mapTerminalStatus(sr))
		dbStageIndex++

		// Fail-fast: if stage didn't complete, stop the chain
		if sr.status != alertsession.StatusCompleted {
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
				stageService:        stageService,
				messageService:      messageService,
				timelineService:     timelineService,
				interactionService:  interactionService,
			}, sr)

			// Publish synthesis stage terminal status (use background context — ctx may be cancelled)
			publishStageStatus(context.Background(), e.eventPublisher, session.ID, synthSr.stageID, synthSr.stageName, dbStageIndex, mapTerminalStatus(synthSr))
			dbStageIndex++

			if synthSr.status != alertsession.StatusCompleted {
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

	// 5. Generate executive summary (fail-open)
	var execSummary string
	var execSummaryErr string
	if finalAnalysis != "" {
		summary, summaryErr := e.generateExecutiveSummary(ctx, session, chain, finalAnalysis, timelineService, interactionService)
		if summaryErr != nil {
			logger.Warn("Executive summary generation failed (fail-open)",
				"error", summaryErr)
			execSummaryErr = summaryErr.Error()
		} else {
			execSummary = summary
		}
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
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("stage %q has no agents", input.stageConfig.Name),
		}
	}

	// 1. Build execution configs (1 for single-agent, N for multi-agent/replica)
	configs := buildConfigs(input.stageConfig)
	policy := e.resolvedSuccessPolicy(input)

	// 2. Create Stage DB record
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          input.stageConfig.Name,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: len(configs),
		ParallelType:       parallelTypePtr(input.stageConfig),
		SuccessPolicy:      successPolicyPtr(input.stageConfig, policy),
	})
	if err != nil {
		logger.Error("Failed to create stage", "error", err)
		return stageResult{
			stageName: input.stageConfig.Name,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create stage: %w", err),
		}
	}

	// 3. Update session progress + publish stage.status: started (stageID now available)
	e.updateSessionProgress(ctx, input.session.ID, input.stageIndex, stg.ID)
	publishStageStatus(ctx, e.eventPublisher, input.session.ID, stg.ID, input.stageConfig.Name, input.stageIndex, events.StageStatusStarted)
	publishSessionProgress(ctx, e.eventPublisher, input.session.ID, input.stageConfig.Name,
		input.stageIndex, input.totalExpectedStages, len(configs),
		fmt.Sprintf("Starting stage: %s", input.stageConfig.Name))

	// 4. Launch goroutines (one per execution config — even if just one)
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

	// 5. Wait for ALL goroutines to complete
	wg.Wait()
	close(results)

	// 6. Collect and sort by original index
	agentResults := collectAndSort(results)

	// 7. Aggregate status via success policy
	stageStatus := aggregateStatus(agentResults, policy)

	// 8. Update Stage in DB (use background context — ctx may be cancelled)
	if updateErr := input.stageService.UpdateStageStatus(context.Background(), stg.ID); updateErr != nil {
		logger.Error("Failed to update stage status", "error", updateErr)
	}

	// For single-agent stages, finalAnalysis comes directly from the agent.
	// For multi-agent stages, synthesis produces it (chain loop handles this).
	finalAnalysis := ""
	if len(agentResults) == 1 {
		finalAnalysis = agentResults[0].finalAnalysis
	}

	return stageResult{
		stageID:       stg.ID,
		stageName:     input.stageConfig.Name,
		status:        stageStatus,
		finalAnalysis: finalAnalysis,
		err:           aggregateError(agentResults, stageStatus, input.stageConfig),
		agentResults:  agentResults,
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

	// Best-effort provider name for the error path (before ResolveAgentConfig
	// succeeds). The happy path uses resolvedConfig.LLMProviderName instead,
	// keeping ResolveAgentConfig as the single source of truth.
	fallbackProviderName := e.cfg.Defaults.LLMProvider
	if input.chain.LLMProvider != "" {
		fallbackProviderName = input.chain.LLMProvider
	}
	if agentConfig.LLMProvider != "" {
		fallbackProviderName = agentConfig.LLMProvider
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
		return agentResult{
			executionID:     exec.ID,
			status:          agent.ExecutionStatusFailed,
			err:             resErr,
			llmProviderName: fallbackProviderName,
		}
	}

	// Create AgentExecution DB record with resolved strategy and provider
	exec, err := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         input.session.ID,
		AgentName:         displayName,
		AgentIndex:        agentIndex + 1, // 1-based in DB
		IterationStrategy: resolvedConfig.IterationStrategy,
		LLMProvider:       resolvedConfig.LLMProviderName,
	})
	if err != nil {
		logger.Error("Failed to create agent execution", "error", err)
		return agentResult{
			status: agent.ExecutionStatusFailed,
			err:    fmt.Errorf("failed to create agent execution: %w", err),
		}
	}

	// Metadata carried on all agentResult returns below (for synthesis context).
	resolvedStrategy := string(resolvedConfig.IterationStrategy)

	// Resolve MCP servers and tool filter
	serverIDs, toolFilter, err := resolveMCPSelection(input.session, resolvedConfig, e.cfg.MCPServerRegistry)
	if err != nil {
		logger.Error("Failed to resolve MCP selection", "error", err)
		return agentResult{
			executionID:       exec.ID,
			status:            agent.ExecutionStatusFailed,
			err:               fmt.Errorf("invalid MCP selection: %w", err),
			iterationStrategy: resolvedStrategy,
			llmProviderName:   resolvedConfig.LLMProviderName,
		}
	}

	// Create MCP tool executor
	toolExecutor, failedServers := createToolExecutor(ctx, e.mcpFactory, serverIDs, toolFilter, logger)
	defer func() { _ = toolExecutor.Close() }()

	// Build execution context
	execCtx := &agent.ExecutionContext{
		SessionID:      input.session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		AgentName:      displayName,
		AgentIndex:     agentIndex + 1, // 1-based
		AlertData:      input.session.AlertData,
		AlertType:      input.session.AlertType,
		RunbookContent: config.GetBuiltinConfig().DefaultRunbook,
		Config:         resolvedConfig,
		LLMClient:      e.llmClient,
		ToolExecutor:   toolExecutor,
		EventPublisher: e.eventPublisher,
		PromptBuilder:  e.promptBuilder,
		FailedServers:  failedServers,
		Services: &agent.ServiceBundle{
			Timeline:    input.timelineService,
			Message:     input.messageService,
			Interaction: input.interactionService,
			Stage:       input.stageService,
		},
	}

	agentInstance, err := e.agentFactory.CreateAgent(execCtx)
	if err != nil {
		logger.Error("Failed to create agent", "error", err)
		return agentResult{
			executionID:       exec.ID,
			status:            agent.ExecutionStatusFailed,
			err:               fmt.Errorf("failed to create agent: %w", err),
			iterationStrategy: resolvedStrategy,
			llmProviderName:   resolvedConfig.LLMProviderName,
		}
	}

	result, err := agentInstance.Execute(ctx, execCtx, input.prevContext)
	if err != nil {
		// Determine whether the error was caused by context cancellation/timeout.
		// When the context is cancelled (e.g. user cancel), the agent may fail with
		// an unrelated error (e.g. "failed to store assistant message") because it
		// tried to operate on a cancelled context. Override to the correct status.
		errStatus := agent.ExecutionStatusFailed
		if ctx.Err() == context.DeadlineExceeded {
			errStatus = agent.ExecutionStatusTimedOut
		} else if ctx.Err() != nil {
			errStatus = agent.ExecutionStatusCancelled
		}
		entErrStatus := mapAgentStatusToEntStatus(errStatus)
		logger.Error("Agent execution error", "error", err, "resolved_status", errStatus)
		if updateErr := input.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entErrStatus, err.Error()); updateErr != nil {
			logger.Error("Failed to update agent execution status after error", "error", updateErr)
		}
		return agentResult{
			executionID:       exec.ID,
			status:            errStatus,
			err:               err,
			iterationStrategy: resolvedStrategy,
			llmProviderName:   resolvedConfig.LLMProviderName,
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
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = agent.ExecutionStatusTimedOut
			result.Error = ctx.Err()
		} else {
			result.Status = agent.ExecutionStatusCancelled
			result.Error = ctx.Err()
		}
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
			executionID:       exec.ID,
			status:            agent.ExecutionStatusFailed,
			finalAnalysis:     result.FinalAnalysis,
			err:               fmt.Errorf("agent completed but status update failed: %w", updateErr),
			iterationStrategy: resolvedStrategy,
			llmProviderName:   resolvedConfig.LLMProviderName,
		}
	}

	return agentResult{
		executionID:       exec.ID,
		status:            result.Status,
		finalAnalysis:     result.FinalAnalysis,
		err:               result.Error,
		iterationStrategy: resolvedStrategy,
		llmProviderName:   resolvedConfig.LLMProviderName,
	}
}

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
		Name:              "SynthesisAgent",
		IterationStrategy: config.IterationStrategySynthesis,
	}
	if s := input.stageConfig.Synthesis; s != nil {
		if s.Agent != "" {
			synthAgentConfig.Name = s.Agent
		}
		if s.IterationStrategy != "" {
			synthAgentConfig.IterationStrategy = s.IterationStrategy
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
			AgentIndex:  i + 1,                // 1-based
			Strategy:    ar.iterationStrategy, // resolved at execution time
			LLMProvider: ar.llmProviderName,   // resolved at execution time
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
// Helper methods
// ────────────────────────────────────────────────────────────

// createToolExecutor creates an MCP tool executor or falls back to a stub.
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
func createToolExecutor(
	ctx context.Context,
	mcpFactory *mcp.ClientFactory,
	serverIDs []string,
	toolFilter map[string][]string,
	logger *slog.Logger,
) (agent.ToolExecutor, map[string]string) {
	if mcpFactory != nil && len(serverIDs) > 0 {
		mcpExecutor, mcpClient, mcpErr := mcpFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
		if mcpErr != nil {
			logger.Warn("Failed to create MCP tool executor, using stub", "error", mcpErr)
			return agent.NewStubToolExecutor(nil), nil
		}
		var failedServers map[string]string
		if mcpClient != nil {
			failedServers = mcpClient.FailedServers()
		}
		return mcpExecutor, failedServers
	}
	return agent.NewStubToolExecutor(nil), nil
}

// mapCancellation checks if the context was cancelled or timed out and returns
// an appropriate ExecutionResult, or nil if the context is still active.
func (e *RealSessionExecutor) mapCancellation(ctx context.Context) *ExecutionResult {
	if ctx.Err() == nil {
		return nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return &ExecutionResult{
			Status: alertsession.StatusTimedOut,
			Error:  fmt.Errorf("session timed out"),
		}
	}
	return &ExecutionResult{
		Status: alertsession.StatusCancelled,
		Error:  context.Canceled,
	}
}

// buildStageContext converts completed stageResults into a context string
// for the next stage's agent prompt.
func (e *RealSessionExecutor) buildStageContext(stages []stageResult) string {
	results := make([]agentctx.StageResult, len(stages))
	for i, s := range stages {
		results[i] = agentctx.StageResult{
			StageName:     s.stageName,
			FinalAnalysis: s.finalAnalysis,
		}
	}
	return agentctx.BuildStageContext(results)
}

// extractFinalAnalysis returns the final analysis from the last completed stage.
// Searches in reverse to find the most recent stage with a non-empty analysis.
func extractFinalAnalysis(stages []stageResult) string {
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].finalAnalysis != "" {
			return stages[i].finalAnalysis
		}
	}
	return ""
}

// updateSessionProgress updates current_stage_index and current_stage_id on the session.
// Non-blocking: logs warning on failure.
func (e *RealSessionExecutor) updateSessionProgress(ctx context.Context, sessionID string, stageIndex int, stageID string) {
	update := e.dbClient.AlertSession.UpdateOneID(sessionID).
		SetCurrentStageIndex(stageIndex + 1) // 1-based in DB

	if stageID != "" {
		update = update.SetCurrentStageID(stageID)
	}

	if err := update.Exec(ctx); err != nil {
		slog.Warn("Failed to update session progress",
			"session_id", sessionID,
			"stage_index", stageIndex,
			"stage_id", stageID,
			"error", err,
		)
	}
}

// publishSessionProgress publishes a session.progress transient event to the global channel.
// Nil-safe for EventPublisher. Best-effort: logs on failure, never aborts.
func publishSessionProgress(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageName string, stageIndex, totalStages, activeExecutions int, statusText string) {
	if eventPublisher == nil {
		return
	}
	// 1-based index for clients, clamped so it never exceeds TotalStages.
	currentIndex := stageIndex + 1
	if totalStages > 0 && currentIndex > totalStages {
		currentIndex = totalStages
	}
	if err := eventPublisher.PublishSessionProgress(ctx, events.SessionProgressPayload{
		Type:              events.EventTypeSessionProgress,
		SessionID:         sessionID,
		CurrentStageName:  stageName,
		CurrentStageIndex: currentIndex,
		TotalStages:       totalStages,
		ActiveExecutions:  activeExecutions,
		StatusText:        statusText,
		Timestamp:         time.Now().Format(time.RFC3339Nano),
	}); err != nil {
		slog.Warn("Failed to publish session progress",
			"session_id", sessionID,
			"stage_name", stageName,
			"error", err,
		)
	}
}

// publishExecutionProgress publishes an execution.progress transient event.
// Nil-safe for EventPublisher. Best-effort: logs on failure, never aborts.
func publishExecutionProgressFromExecutor(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageID, executionID, phase, message string) {
	if eventPublisher == nil {
		return
	}
	if err := eventPublisher.PublishExecutionProgress(ctx, sessionID, events.ExecutionProgressPayload{
		Type:        events.EventTypeExecutionProgress,
		SessionID:   sessionID,
		StageID:     stageID,
		ExecutionID: executionID,
		Phase:       phase,
		Message:     message,
		Timestamp:   time.Now().Format(time.RFC3339Nano),
	}); err != nil {
		slog.Warn("Failed to publish execution progress",
			"session_id", sessionID,
			"phase", phase,
			"error", err,
		)
	}
}

// publishStageStatus publishes a stage.status event. Nil-safe for EventPublisher.
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
func publishStageStatus(ctx context.Context, eventPublisher agent.EventPublisher, sessionID, stageID, stageName string, stageIndex int, status string) {
	if eventPublisher == nil {
		return
	}
	if err := eventPublisher.PublishStageStatus(ctx, sessionID, events.StageStatusPayload{
		Type:       events.EventTypeStageStatus,
		SessionID:  sessionID,
		StageID:    stageID,
		StageName:  stageName,
		StageIndex: stageIndex + 1, // 1-based for clients
		Status:     status,
		Timestamp:  time.Now().Format(time.RFC3339Nano),
	}); err != nil {
		slog.Warn("Failed to publish stage status",
			"session_id", sessionID,
			"stage_name", stageName,
			"status", status,
			"error", err,
		)
	}
}

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
	providerName := e.cfg.Defaults.LLMProvider
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

	// Resolve backend from chain-level strategy or defaults
	strategy := e.cfg.Defaults.IterationStrategy
	if chain.IterationStrategy != "" {
		strategy = chain.IterationStrategy
	}
	backend := agent.ResolveBackend(strategy)

	// Build prompts
	systemPrompt := e.promptBuilder.BuildExecutiveSummarySystemPrompt()
	userPrompt := e.promptBuilder.BuildExecutiveSummaryUserPrompt(finalAnalysis)

	messages := []agent.ConversationMessage{
		{Role: agent.RoleSystem, Content: systemPrompt},
		{Role: agent.RoleUser, Content: userPrompt},
	}

	// Single LLM call — no tools, consume full response from stream
	input := &agent.GenerateInput{
		SessionID: session.ID,
		Messages:  messages,
		Config:    provider,
		Backend:   backend,
	}

	// Derive a cancellable context so the producer goroutine in Generate
	// is always cleaned up when we return (e.g. on ErrorChunk early exit).
	llmCtx, llmCancel := context.WithCancel(ctx)
	defer llmCancel()

	ch, err := e.llmClient.Generate(llmCtx, input)
	if err != nil {
		return "", fmt.Errorf("executive summary LLM call failed: %w", err)
	}

	// Collect full text response
	var sb strings.Builder
	for chunk := range ch {
		switch c := chunk.(type) {
		case *agent.TextChunk:
			sb.WriteString(c.Content)
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
	interaction, createErr := interactionService.CreateLLMInteraction(ctx, models.CreateLLMInteractionRequest{
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
	})
	if createErr != nil {
		logger.Warn("Failed to record executive summary LLM interaction",
			"error", createErr)
	} else if e.eventPublisher != nil {
		// Publish interaction.created for trace view live updates.
		if pubErr := e.eventPublisher.PublishInteractionCreated(ctx, session.ID, events.InteractionCreatedPayload{
			Type:            events.EventTypeInteractionCreated,
			SessionID:       session.ID,
			InteractionID:   interaction.ID,
			InteractionType: events.InteractionTypeLLM,
			Timestamp:       time.Now().Format(time.RFC3339Nano),
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

// ────────────────────────────────────────────────────────────
// MCP selection resolution
// ────────────────────────────────────────────────────────────

// resolveMCPSelection determines the MCP servers and tool filter for this session.
// If the session has an MCP override (mcp_selection JSON), it replaces the chain
// config entirely (replace semantics, not merge).
//
// Side effect: when the override includes NativeTools, this method mutates
// resolvedConfig.NativeToolsOverride so the downstream LLM call picks up the
// override. This coupling keeps MCP selection logic in one place rather than
// splitting it across the executor flow.
//
// Package-level function shared by RealSessionExecutor and ChatMessageExecutor.
// Returns (serverIDs, toolFilter, error).
func resolveMCPSelection(
	session *ent.AlertSession,
	resolvedConfig *agent.ResolvedAgentConfig,
	mcpRegistry *config.MCPServerRegistry,
) ([]string, map[string][]string, error) {
	// No override — use chain config (existing behavior)
	if len(session.McpSelection) == 0 {
		return resolvedConfig.MCPServers, nil, nil
	}

	// Deserialize override
	override, err := models.ParseMCPSelectionConfig(session.McpSelection)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse MCP selection: %w", err)
	}
	if override == nil {
		// ParseMCPSelectionConfig returns nil for empty maps
		return resolvedConfig.MCPServers, nil, nil
	}

	// Build serverIDs and toolFilter from override
	serverIDs := make([]string, 0, len(override.Servers))
	toolFilter := make(map[string][]string)

	for _, sel := range override.Servers {
		// Validate server exists in registry
		if mcpRegistry != nil && !mcpRegistry.Has(sel.Name) {
			return nil, nil, fmt.Errorf("MCP server %q from override not found in configuration", sel.Name)
		}
		serverIDs = append(serverIDs, sel.Name)

		// Only add to toolFilter if specific tools are requested
		if len(sel.Tools) > 0 {
			toolFilter[sel.Name] = sel.Tools
		}
	}

	// Return nil toolFilter if no server has tool restrictions
	if len(toolFilter) == 0 {
		toolFilter = nil
	}

	// Apply native tools override to the resolved config
	if override.NativeTools != nil {
		resolvedConfig.NativeToolsOverride = override.NativeTools
	}

	return serverIDs, toolFilter, nil
}

// countExpectedStages computes the total number of progress steps for the chain,
// including synthesis stages (for multi-agent/replica stages) and the executive
// summary step. Used for accurate progress reporting so CurrentStageIndex never
// exceeds TotalStages.
func countExpectedStages(chain *config.ChainConfig) int {
	total := len(chain.Stages)
	for _, stageCfg := range chain.Stages {
		if len(stageCfg.Agents) > 1 || stageCfg.Replicas > 1 {
			total++ // synthesis stage will follow
		}
	}
	total++ // executive summary step
	return total
}

// ────────────────────────────────────────────────────────────
// Config builders
// ────────────────────────────────────────────────────────────

// buildConfigs creates execution configs for a stage. For single-agent stages,
// returns a single config. Same path, no branching.
func buildConfigs(stageCfg config.StageConfig) []executionConfig {
	if stageCfg.Replicas > 1 {
		return buildReplicaConfigs(stageCfg)
	}
	return buildMultiAgentConfigs(stageCfg)
}

// buildMultiAgentConfigs creates one executionConfig per agent in the stage.
// For single-agent stages, returns []executionConfig with 1 entry.
func buildMultiAgentConfigs(stageCfg config.StageConfig) []executionConfig {
	configs := make([]executionConfig, len(stageCfg.Agents))
	for i, agentCfg := range stageCfg.Agents {
		configs[i] = executionConfig{
			agentConfig: agentCfg,
			displayName: agentCfg.Name,
		}
	}
	return configs
}

// buildReplicaConfigs replicates the first agent config N times.
// Display names: {BaseName}-1, {BaseName}-2, etc.
func buildReplicaConfigs(stageCfg config.StageConfig) []executionConfig {
	baseAgent := stageCfg.Agents[0]
	configs := make([]executionConfig, stageCfg.Replicas)
	for i := 0; i < stageCfg.Replicas; i++ {
		configs[i] = executionConfig{
			agentConfig: baseAgent,
			displayName: fmt.Sprintf("%s-%d", baseAgent.Name, i+1),
		}
	}
	return configs
}

// ────────────────────────────────────────────────────────────
// Policy resolution
// ────────────────────────────────────────────────────────────

// resolvedSuccessPolicy resolves the success policy for a stage:
// stage config > system default > fallback SuccessPolicyAny.
func (e *RealSessionExecutor) resolvedSuccessPolicy(input executeStageInput) config.SuccessPolicy {
	if input.stageConfig.SuccessPolicy != "" {
		return input.stageConfig.SuccessPolicy
	}
	if e.cfg.Defaults.SuccessPolicy != "" {
		return e.cfg.Defaults.SuccessPolicy
	}
	return config.SuccessPolicyAny
}

// parallelTypePtr returns the parallel_type for DB storage, or nil for single-agent stages.
func parallelTypePtr(stageCfg config.StageConfig) *string {
	if stageCfg.Replicas > 1 {
		s := "replica"
		return &s
	}
	if len(stageCfg.Agents) > 1 {
		s := "multi_agent"
		return &s
	}
	return nil
}

// successPolicyPtr returns the resolved success policy as *string for DB storage,
// or nil for single-agent stages (policy is irrelevant when there's only one agent).
func successPolicyPtr(stageCfg config.StageConfig, resolved config.SuccessPolicy) *string {
	if len(stageCfg.Agents) <= 1 && stageCfg.Replicas <= 1 {
		return nil
	}
	s := string(resolved)
	return &s
}

// ────────────────────────────────────────────────────────────
// Result aggregation
// ────────────────────────────────────────────────────────────

// collectAndSort drains the indexedAgentResult channel and returns results
// sorted by their original launch index.
func collectAndSort(ch <-chan indexedAgentResult) []agentResult {
	var indexed []indexedAgentResult
	for iar := range ch {
		indexed = append(indexed, iar)
	}
	sort.Slice(indexed, func(i, j int) bool {
		return indexed[i].index < indexed[j].index
	})
	results := make([]agentResult, len(indexed))
	for i, iar := range indexed {
		results[i] = iar.result
	}
	return results
}

// aggregateStatus determines the overall stage status from agent results and
// the resolved success policy. Works identically for 1 or N agents.
func aggregateStatus(results []agentResult, policy config.SuccessPolicy) alertsession.Status {
	var completed, failed, timedOut, cancelled int

	for _, r := range results {
		switch mapAgentStatusToSessionStatus(r.status) {
		case alertsession.StatusCompleted:
			completed++
		case alertsession.StatusTimedOut:
			timedOut++
		case alertsession.StatusCancelled:
			cancelled++
		default:
			failed++
		}
	}

	nonSuccess := failed + timedOut + cancelled

	switch policy {
	case config.SuccessPolicyAll:
		if nonSuccess == 0 {
			return alertsession.StatusCompleted
		}
	default: // SuccessPolicyAny (default when unset)
		if completed > 0 {
			return alertsession.StatusCompleted
		}
	}

	// Stage failed — use most specific terminal status when uniform
	if nonSuccess == timedOut {
		return alertsession.StatusTimedOut
	}
	if nonSuccess == cancelled {
		return alertsession.StatusCancelled
	}
	return alertsession.StatusFailed
}

// aggregateError builds a descriptive error for failed stages.
// Single-agent: returns the agent's error directly.
// Multi-agent: lists each non-successful agent with details.
func aggregateError(results []agentResult, stageStatus alertsession.Status, stageCfg config.StageConfig) error {
	if stageStatus == alertsession.StatusCompleted {
		return nil
	}

	// Single agent — passthrough
	if len(results) == 1 {
		return results[0].err
	}

	// Multi-agent — build descriptive error
	var nonSuccess int
	for _, r := range results {
		if mapAgentStatusToSessionStatus(r.status) != alertsession.StatusCompleted {
			nonSuccess++
		}
	}

	policy := "any"
	if stageCfg.SuccessPolicy != "" {
		policy = string(stageCfg.SuccessPolicy)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "multi-agent stage failed: %d/%d executions failed (policy: %s)", nonSuccess, len(results), policy)

	sb.WriteString("\n\nFailed agents:")
	for i, r := range results {
		sessionStatus := mapAgentStatusToSessionStatus(r.status)
		if sessionStatus == alertsession.StatusCompleted {
			continue
		}
		errMsg := "unknown error"
		if r.err != nil {
			errMsg = r.err.Error()
		}
		fmt.Fprintf(&sb, "\n  - agent %d (%s): %s", i+1, sessionStatus, errMsg)
	}

	return fmt.Errorf("%s", sb.String())
}

// mapTerminalStatus extracts a terminal status string for event publishing.
func mapTerminalStatus(sr stageResult) string {
	switch sr.status {
	case alertsession.StatusCompleted:
		return events.StageStatusCompleted
	case alertsession.StatusFailed:
		return events.StageStatusFailed
	case alertsession.StatusTimedOut:
		return events.StageStatusTimedOut
	case alertsession.StatusCancelled:
		return events.StageStatusCancelled
	default:
		return events.StageStatusFailed
	}
}

// ────────────────────────────────────────────────────────────
// Status mappers
// ────────────────────────────────────────────────────────────

// mapAgentStatusToEntStatus converts agent.ExecutionStatus to ent agentexecution.Status.
// Pending/Active statuses fall through to Failed intentionally — they should
// never reach this mapper since BaseAgent always sets a terminal status before
// returning. Mapping Active to Failed (rather than Active) prevents leaving
// AgentExecution records in a non-terminal state permanently.
func mapAgentStatusToEntStatus(status agent.ExecutionStatus) agentexecution.Status {
	switch status {
	case agent.ExecutionStatusCompleted:
		return agentexecution.StatusCompleted
	case agent.ExecutionStatusFailed:
		return agentexecution.StatusFailed
	case agent.ExecutionStatusTimedOut:
		return agentexecution.StatusTimedOut
	case agent.ExecutionStatusCancelled:
		return agentexecution.StatusCancelled
	default:
		return agentexecution.StatusFailed
	}
}

// mapAgentStatusToSessionStatus converts agent.ExecutionStatus to alertsession.Status.
// Pending/Active statuses fall through to Failed intentionally — they should never
// reach this mapper since BaseAgent always sets a terminal status before returning.
func mapAgentStatusToSessionStatus(status agent.ExecutionStatus) alertsession.Status {
	switch status {
	case agent.ExecutionStatusCompleted:
		return alertsession.StatusCompleted
	case agent.ExecutionStatusFailed:
		return alertsession.StatusFailed
	case agent.ExecutionStatusTimedOut:
		return alertsession.StatusTimedOut
	case agent.ExecutionStatusCancelled:
		return alertsession.StatusCancelled
	default:
		return alertsession.StatusFailed
	}
}
