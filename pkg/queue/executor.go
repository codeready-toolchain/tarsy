package queue

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/codeready-toolchain/tarsy/pkg/agent/prompt"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

// RealSessionExecutor implements SessionExecutor using the agent framework.
type RealSessionExecutor struct {
	cfg           *config.Config
	dbClient      *ent.Client
	llmClient     agent.LLMClient
	agentFactory  *agent.AgentFactory
	promptBuilder *prompt.PromptBuilder
}

// NewRealSessionExecutor creates a new session executor.
func NewRealSessionExecutor(cfg *config.Config, dbClient *ent.Client, llmClient agent.LLMClient) *RealSessionExecutor {
	controllerFactory := controller.NewFactory()
	return &RealSessionExecutor{
		cfg:           cfg,
		dbClient:      dbClient,
		llmClient:     llmClient,
		agentFactory:  agent.NewAgentFactory(controllerFactory),
		promptBuilder: prompt.NewPromptBuilder(cfg.MCPServerRegistry),
	}
}

// Execute runs the session through the agent chain.
// Phase 3.1: single stage, single agent only.
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

	// Phase 3.1: execute first stage only
	stageConfig := chain.Stages[0]
	if len(stageConfig.Agents) == 0 {
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("stage %q has no agents", stageConfig.Name),
		}
	}
	agentConfig := stageConfig.Agents[0]

	// 2. Initialize services
	stageService := services.NewStageService(e.dbClient)
	messageService := services.NewMessageService(e.dbClient)
	timelineService := services.NewTimelineService(e.dbClient)
	interactionService := services.NewInteractionService(e.dbClient, messageService)

	// 3. Create Stage DB record
	stg, err := stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          session.ID,
		StageName:          stageConfig.Name,
		StageIndex:         1,
		ExpectedAgentCount: 1,
	})
	if err != nil {
		logger.Error("Failed to create stage", "error", err)
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("failed to create stage: %w", err),
		}
	}

	// 4. Create AgentExecution DB record
	exec, err := stageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           stg.ID,
		SessionID:         session.ID,
		AgentName:         agentConfig.Name,
		AgentIndex:        1,
		IterationStrategy: string(agentConfig.IterationStrategy),
	})
	if err != nil {
		logger.Error("Failed to create agent execution", "error", err)
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("failed to create agent execution: %w", err),
		}
	}

	// 5. Resolve agent config from hierarchy
	resolvedConfig, err := agent.ResolveAgentConfig(e.cfg, chain, stageConfig, agentConfig)
	if err != nil {
		logger.Error("Failed to resolve agent config", "error", err)
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("failed to resolve agent config: %w", err),
		}
	}

	// 6. Build execution context
	execCtx := &agent.ExecutionContext{
		SessionID:      session.ID,
		StageID:        stg.ID,
		ExecutionID:    exec.ID,
		AgentName:      agentConfig.Name,
		AgentIndex:     1,
		AlertData:      session.AlertData,
		AlertType:      session.AlertType,
		RunbookContent: config.GetBuiltinConfig().DefaultRunbook, // Phase 6 adds real runbook fetching
		Config:         resolvedConfig,
		LLMClient:      e.llmClient,
		ToolExecutor:   agent.NewStubToolExecutor(nil), // Phase 3.2 stub; Phase 4: MCP client
		PromptBuilder:  e.promptBuilder,
		Services: &agent.ServiceBundle{
			Timeline:    timelineService,
			Message:     messageService,
			Interaction: interactionService,
			Stage:       stageService,
		},
	}

	// 7. Create and run agent
	agentInstance, err := e.agentFactory.CreateAgent(execCtx)
	if err != nil {
		logger.Error("Failed to create agent", "error", err)
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  fmt.Errorf("failed to create agent: %w", err),
		}
	}

	agentResult, err := agentInstance.Execute(ctx, execCtx, "")
	if err != nil {
		logger.Error("Agent execution error", "error", err)
		if updateErr := stageService.UpdateAgentExecutionStatus(ctx, exec.ID, agentexecution.StatusFailed, err.Error()); updateErr != nil {
			logger.Error("Failed to update agent execution status after error", "error", updateErr)
		}
		if updateErr := stageService.UpdateStageStatus(ctx, stg.ID); updateErr != nil {
			logger.Error("Failed to update stage status after error", "error", updateErr)
		}
		return &ExecutionResult{
			Status: alertsession.StatusFailed,
			Error:  err,
		}
	}

	// 8. Update AgentExecution status based on result.
	// If DB updates fail, override the result to Failed so the session
	// isn't marked as completed while internal records are inconsistent.
	entStatus := mapAgentStatusToEntStatus(agentResult.Status)
	errMsg := ""
	if agentResult.Error != nil {
		errMsg = agentResult.Error.Error()
	}
	if err := stageService.UpdateAgentExecutionStatus(ctx, exec.ID, entStatus, errMsg); err != nil {
		logger.Error("Failed to update agent execution status", "error", err)
		return &ExecutionResult{
			Status:        alertsession.StatusFailed,
			FinalAnalysis: agentResult.FinalAnalysis,
			Error:         fmt.Errorf("agent completed but status update failed: %w", err),
		}
	}

	// 9. Aggregate stage status
	if err := stageService.UpdateStageStatus(ctx, stg.ID); err != nil {
		logger.Error("Failed to update stage status", "error", err)
		return &ExecutionResult{
			Status:        alertsession.StatusFailed,
			FinalAnalysis: agentResult.FinalAnalysis,
			Error:         fmt.Errorf("agent completed but stage status update failed: %w", err),
		}
	}

	// 10. Map agent result -> queue result
	logger.Info("Session executor: execution completed",
		"status", agentResult.Status,
		"tokens_total", agentResult.TokensUsed.TotalTokens)

	return &ExecutionResult{
		Status:        mapAgentStatusToSessionStatus(agentResult.Status),
		FinalAnalysis: agentResult.FinalAnalysis,
		Error:         agentResult.Error,
	}
}

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
