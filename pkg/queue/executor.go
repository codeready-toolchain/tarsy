package queue

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	executionID   string
	stageName     string
	status        alertsession.Status // mapped from agent status
	finalAnalysis string
	err           error
}

// agentResult captures the outcome of a single agent execution within a stage.
type agentResult struct {
	executionID   string
	status        agent.ExecutionStatus
	finalAnalysis string
	err           error
}

// executeStageInput groups all parameters for executeStage to keep the call signature clean.
type executeStageInput struct {
	session     *ent.AlertSession
	chain       *config.ChainConfig
	stageConfig config.StageConfig
	stageIndex  int // 0-based
	prevContext string

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
	var completedStages []stageResult
	prevContext := ""

	for i, stageCfg := range chain.Stages {
		// Check for cancellation between stages
		if r := e.mapCancellation(ctx); r != nil {
			return r
		}

		// Update session progress (before stage starts)
		e.updateSessionProgress(ctx, session.ID, i, "")

		// Publish stage started event
		e.publishStageStatus(ctx, session.ID, "", stageCfg.Name, i, events.StageStatusStarted)

		sr := e.executeStage(ctx, executeStageInput{
			session:            session,
			chain:              chain,
			stageConfig:        stageCfg,
			stageIndex:         i,
			prevContext:        prevContext,
			stageService:       stageService,
			messageService:     messageService,
			timelineService:    timelineService,
			interactionService: interactionService,
		})

		// Update session progress (after stage created, with stageID)
		e.updateSessionProgress(ctx, session.ID, i, sr.stageID)

		// Publish stage terminal status
		terminalStatus := events.StageStatusCompleted
		switch sr.status {
		case alertsession.StatusFailed:
			terminalStatus = events.StageStatusFailed
		case alertsession.StatusTimedOut:
			terminalStatus = events.StageStatusTimedOut
		case alertsession.StatusCancelled:
			terminalStatus = events.StageStatusCancelled
		}
		e.publishStageStatus(ctx, session.ID, sr.stageID, sr.stageName, i, terminalStatus)

		completedStages = append(completedStages, sr)

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

		// Build context for next stage
		prevContext = e.buildStageContext(completedStages)
	}

	// 4. Extract final analysis from completed stages
	finalAnalysis := extractFinalAnalysis(completedStages)

	// 5. Generate executive summary (fail-open)
	var execSummary string
	var execSummaryErr string
	if finalAnalysis != "" {
		summary, summaryErr := e.generateExecutiveSummary(ctx, session, chain, finalAnalysis, timelineService)
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
// executeStage — single stage execution
// ────────────────────────────────────────────────────────────

// executeStage creates the Stage DB record, executes the first agent, and
// updates stage status. Returns a stageResult capturing the outcome.
func (e *RealSessionExecutor) executeStage(ctx context.Context, input executeStageInput) stageResult {
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_name", input.stageConfig.Name,
		"stage_index", input.stageIndex,
	)

	// Guard: parallel execution not yet supported (Phase 5.2)
	if len(input.stageConfig.Agents) == 0 {
		return stageResult{
			stageName: input.stageConfig.Name,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("stage %q has no agents", input.stageConfig.Name),
		}
	}
	if len(input.stageConfig.Agents) > 1 {
		return stageResult{
			stageName: input.stageConfig.Name,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("stage %q has %d agents — parallel agent execution not yet supported", input.stageConfig.Name, len(input.stageConfig.Agents)),
		}
	}
	if input.stageConfig.Replicas > 1 {
		return stageResult{
			stageName: input.stageConfig.Name,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("stage %q has %d replicas — parallel agent execution not yet supported", input.stageConfig.Name, input.stageConfig.Replicas),
		}
	}

	agentConfig := input.stageConfig.Agents[0]

	// Create Stage DB record
	stg, err := input.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.session.ID,
		StageName:          input.stageConfig.Name,
		StageIndex:         input.stageIndex + 1, // 1-based in DB
		ExpectedAgentCount: 1,
	})
	if err != nil {
		logger.Error("Failed to create stage", "error", err)
		return stageResult{
			stageName: input.stageConfig.Name,
			status:    alertsession.StatusFailed,
			err:       fmt.Errorf("failed to create stage: %w", err),
		}
	}

	// Execute agent
	ar := e.executeAgent(ctx, input, stg, agentConfig, 0)

	// Update stage status (aggregates from agent executions)
	if updateErr := input.stageService.UpdateStageStatus(ctx, stg.ID); updateErr != nil {
		logger.Error("Failed to update stage status", "error", updateErr)
		return stageResult{
			stageID:       stg.ID,
			executionID:   ar.executionID,
			stageName:     input.stageConfig.Name,
			status:        alertsession.StatusFailed,
			finalAnalysis: ar.finalAnalysis,
			err:           fmt.Errorf("agent completed but stage status update failed: %w", updateErr),
		}
	}

	return stageResult{
		stageID:       stg.ID,
		executionID:   ar.executionID,
		stageName:     input.stageConfig.Name,
		status:        mapAgentStatusToSessionStatus(ar.status),
		finalAnalysis: ar.finalAnalysis,
		err:           ar.err,
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
) agentResult {
	logger := slog.With(
		"session_id", input.session.ID,
		"stage_id", stg.ID,
		"agent_name", agentConfig.Name,
		"agent_index", agentIndex,
	)

	// Create AgentExecution DB record
	exec, err := input.stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         input.session.ID,
		AgentName:         agentConfig.Name,
		AgentIndex:        agentIndex + 1, // 1-based in DB
		IterationStrategy: string(agentConfig.IterationStrategy),
	})
	if err != nil {
		logger.Error("Failed to create agent execution", "error", err)
		return agentResult{
			status: agent.ExecutionStatusFailed,
			err:    fmt.Errorf("failed to create agent execution: %w", err),
		}
	}

	// Resolve agent config from hierarchy
	resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, input.chain, input.stageConfig, agentConfig)
	if err != nil {
		logger.Error("Failed to resolve agent config", "error", err)
		return agentResult{
			executionID: exec.ID,
			status:      agent.ExecutionStatusFailed,
			err:         fmt.Errorf("failed to resolve agent config: %w", err),
		}
	}

	// Resolve MCP servers and tool filter
	serverIDs, toolFilter, err := e.resolveMCPSelection(input.session, resolvedConfig)
	if err != nil {
		logger.Error("Failed to resolve MCP selection", "error", err)
		return agentResult{
			executionID: exec.ID,
			status:      agent.ExecutionStatusFailed,
			err:         fmt.Errorf("invalid MCP selection: %w", err),
		}
	}

	// Create MCP tool executor
	toolExecutor, failedServers := e.createToolExecutor(ctx, serverIDs, toolFilter, logger)
	defer func() { _ = toolExecutor.Close() }()

	// Build execution context
	execCtx := &agent.ExecutionContext{
		SessionID:      input.session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		AgentName:      agentConfig.Name,
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
			executionID: exec.ID,
			status:      agent.ExecutionStatusFailed,
			err:         fmt.Errorf("failed to create agent: %w", err),
		}
	}

	result, err := agentInstance.Execute(ctx, execCtx, input.prevContext)
	if err != nil {
		logger.Error("Agent execution error", "error", err)
		if updateErr := input.stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusFailed, err.Error()); updateErr != nil {
			logger.Error("Failed to update agent execution status after error", "error", updateErr)
		}
		return agentResult{
			executionID: exec.ID,
			status:      agent.ExecutionStatusFailed,
			err:         err,
		}
	}

	// Update AgentExecution status
	entStatus := mapAgentStatusToEntStatus(result.Status)
	errMsg := ""
	if result.Error != nil {
		errMsg = result.Error.Error()
	}
	if updateErr := input.stageService.UpdateAgentExecutionStatus(ctx, exec.ID, entStatus, errMsg); updateErr != nil {
		logger.Error("Failed to update agent execution status", "error", updateErr)
		return agentResult{
			executionID:   exec.ID,
			status:        agent.ExecutionStatusFailed,
			finalAnalysis: result.FinalAnalysis,
			err:           fmt.Errorf("agent completed but status update failed: %w", updateErr),
		}
	}

	return agentResult{
		executionID:   exec.ID,
		status:        result.Status,
		finalAnalysis: result.FinalAnalysis,
		err:           result.Error,
	}
}

// ────────────────────────────────────────────────────────────
// Helper methods
// ────────────────────────────────────────────────────────────

// createToolExecutor creates an MCP tool executor or falls back to a stub.
func (e *RealSessionExecutor) createToolExecutor(
	ctx context.Context,
	serverIDs []string,
	toolFilter map[string][]string,
	logger *slog.Logger,
) (agent.ToolExecutor, map[string]string) {
	if e.mcpFactory != nil && len(serverIDs) > 0 {
		mcpExecutor, mcpClient, mcpErr := e.mcpFactory.CreateToolExecutor(ctx, serverIDs, toolFilter)
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

// publishStageStatus publishes a stage.status event. Nil-safe for EventPublisher.
func (e *RealSessionExecutor) publishStageStatus(ctx context.Context, sessionID, stageID, stageName string, stageIndex int, status string) {
	if e.eventPublisher == nil {
		return
	}
	if err := e.eventPublisher.PublishStageStatus(ctx, sessionID, events.StageStatusPayload{
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
) (string, error) {
	logger := slog.With("session_id", session.ID)

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

	ch, err := e.llmClient.Generate(ctx, input)
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

	// Create session-level timeline event (no stage_id, no execution_id)
	// Use a fixed sequence number — executive summary is always the last event
	_, err = timelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      session.ID,
		SequenceNumber: executiveSummarySeqNum,
		EventType:      timelineevent.EventTypeExecutiveSummary,
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
// Returns (serverIDs, toolFilter, error).
func (e *RealSessionExecutor) resolveMCPSelection(
	session *ent.AlertSession,
	resolvedConfig *agent.ResolvedAgentConfig,
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
		if !e.cfg.MCPServerRegistry.Has(sel.Name) {
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
