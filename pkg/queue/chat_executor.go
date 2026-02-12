package queue

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/event"
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

// ────────────────────────────────────────────────────────────
// Input and config types
// ────────────────────────────────────────────────────────────

// ChatExecuteInput groups all parameters needed to execute a chat message.
type ChatExecuteInput struct {
	Chat    *ent.Chat
	Message *ent.ChatUserMessage
	Session *ent.AlertSession
}

// ChatMessageExecutorConfig holds configuration for the chat message executor.
type ChatMessageExecutorConfig struct {
	SessionTimeout    time.Duration // Max duration for a chat execution (default: 15 minutes)
	HeartbeatInterval time.Duration // Heartbeat frequency (default: 30s)
}

// ────────────────────────────────────────────────────────────
// ChatMessageExecutor
// ────────────────────────────────────────────────────────────

// ChatMessageExecutor handles asynchronous chat message processing.
// It manages a single goroutine per chat (one-at-a-time enforcement),
// supports cancellation, and graceful shutdown.
type ChatMessageExecutor struct {
	// Dependencies
	cfg            *config.Config
	dbClient       *ent.Client
	llmClient      agent.LLMClient
	mcpFactory     *mcp.ClientFactory
	agentFactory   *agent.AgentFactory
	eventPublisher agent.EventPublisher
	promptBuilder  *prompt.PromptBuilder
	execConfig     ChatMessageExecutorConfig

	// Services
	timelineService    *services.TimelineService
	stageService       *services.StageService
	chatService        *services.ChatService
	messageService     *services.MessageService
	interactionService *services.InteractionService

	// Active execution tracking (for cancellation + shutdown)
	mu          sync.RWMutex
	activeExecs map[string]context.CancelFunc // chatID → cancel
	wg          sync.WaitGroup                // tracks active goroutines for shutdown
	stopped     bool                          // reject new submissions after Stop()
}

// NewChatMessageExecutor creates a new ChatMessageExecutor.
func NewChatMessageExecutor(
	cfg *config.Config,
	dbClient *ent.Client,
	llmClient agent.LLMClient,
	mcpFactory *mcp.ClientFactory,
	eventPublisher agent.EventPublisher,
	execConfig ChatMessageExecutorConfig,
) *ChatMessageExecutor {
	controllerFactory := controller.NewFactory()
	msgService := services.NewMessageService(dbClient)
	return &ChatMessageExecutor{
		cfg:                cfg,
		dbClient:           dbClient,
		llmClient:          llmClient,
		mcpFactory:         mcpFactory,
		agentFactory:       agent.NewAgentFactory(controllerFactory),
		eventPublisher:     eventPublisher,
		promptBuilder:      prompt.NewPromptBuilder(cfg.MCPServerRegistry),
		execConfig:         execConfig,
		timelineService:    services.NewTimelineService(dbClient),
		stageService:       services.NewStageService(dbClient),
		chatService:        services.NewChatService(dbClient),
		messageService:     msgService,
		interactionService: services.NewInteractionService(dbClient, msgService),
		activeExecs:        make(map[string]context.CancelFunc),
	}
}

// ────────────────────────────────────────────────────────────
// Submit — entry point for chat message processing
// ────────────────────────────────────────────────────────────

// Submit validates the one-at-a-time constraint, creates a Stage record,
// and launches asynchronous execution. Returns the stage ID for the response.
func (e *ChatMessageExecutor) Submit(ctx context.Context, input ChatExecuteInput) (string, error) {
	// 1. Fast-fail if already stopped (avoids unnecessary DB work)
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return "", ErrShuttingDown
	}
	e.mu.RUnlock()

	// 2. Check one-at-a-time constraint
	activeStage, err := e.stageService.GetActiveStageForChat(ctx, input.Chat.ID)
	if err != nil {
		return "", fmt.Errorf("failed to check active chat stage: %w", err)
	}
	if activeStage != nil {
		return "", ErrChatExecutionActive
	}

	// 3. Get next stage index (continues from investigation stages)
	maxIndex, err := e.stageService.GetMaxStageIndex(ctx, input.Session.ID)
	if err != nil {
		return "", fmt.Errorf("failed to get max stage index: %w", err)
	}
	stageIndex := maxIndex + 1

	// 4. Create Stage record
	chatID := input.Chat.ID
	messageID := input.Message.ID
	stg, err := e.stageService.CreateStage(ctx, models.CreateStageRequest{
		SessionID:          input.Session.ID,
		StageName:          "Chat Response",
		StageIndex:         stageIndex,
		ExpectedAgentCount: 1,
		ChatID:             &chatID,
		ChatUserMessageID:  &messageID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create chat stage: %w", err)
	}

	// 5. Atomically check stopped + register goroutine to prevent race with Stop().
	// This second check is necessary because Stop() could have been called between
	// the fast-fail check and here; holding RLock through wg.Add(1) ensures Stop
	// cannot complete wg.Wait() before this goroutine is tracked.
	e.mu.RLock()
	if e.stopped {
		e.mu.RUnlock()
		return "", ErrShuttingDown
	}
	e.wg.Add(1)
	e.mu.RUnlock()

	// 6. Launch goroutine with detached context (not tied to HTTP request lifecycle)
	go e.execute(context.Background(), input, stg.ID, stageIndex)

	return stg.ID, nil
}

// ────────────────────────────────────────────────────────────
// execute — async execution flow
// ────────────────────────────────────────────────────────────

func (e *ChatMessageExecutor) execute(parentCtx context.Context, input ChatExecuteInput, stageID string, stageIndex int) {
	defer e.wg.Done()

	logger := slog.With(
		"session_id", input.Session.ID,
		"chat_id", input.Chat.ID,
		"stage_id", stageID,
		"message_id", input.Message.ID,
	)
	logger.Info("Chat executor: starting execution")

	// Create cancellable context with timeout
	execCtx, cancel := context.WithTimeout(parentCtx, e.execConfig.SessionTimeout)
	defer cancel()

	// Register for cancellation
	e.registerExecution(input.Chat.ID, cancel)
	defer e.unregisterExecution(input.Chat.ID)

	// --- All failure paths must update stage terminal status ---

	// 1. Resolve chain + chat agent config
	chain, err := e.cfg.GetChain(input.Chat.ChainID)
	if err != nil {
		logger.Error("Failed to resolve chain config", "error", err)
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	resolvedConfig, err := agent.ResolveChatAgentConfig(e.cfg, chain, chain.Chat)
	if err != nil {
		logger.Error("Failed to resolve chat agent config", "error", err)
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	// 2. Resolve MCP selection (shared helper, handles session override)
	serverIDs, toolFilter, err := resolveMCPSelection(input.Session, resolvedConfig, e.cfg.MCPServerRegistry)
	if err != nil {
		logger.Error("Failed to resolve MCP selection", "error", err)
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	// 3. Create AgentExecution record
	exec, err := e.stageService.CreateAgentExecution(execCtx, models.CreateAgentExecutionRequest{
		StageID:           stageID,
		SessionID:         input.Session.ID,
		AgentName:         resolvedConfig.AgentName,
		AgentIndex:        1,
		IterationStrategy: resolvedConfig.IterationStrategy,
	})
	if err != nil {
		logger.Error("Failed to create agent execution", "error", err)
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	// 4. Create user_question timeline event (before building context, so it's included)
	maxSeq, err := e.timelineService.GetMaxSequenceNumber(execCtx, input.Session.ID)
	if err != nil {
		logger.Warn("Failed to get max sequence number", "error", err)
		maxSeq = 0 // fallback to 1
	}
	_, err = e.timelineService.CreateTimelineEvent(execCtx, models.CreateTimelineEventRequest{
		SessionID:      input.Session.ID,
		StageID:        &stageID,
		ExecutionID:    &exec.ID,
		SequenceNumber: maxSeq + 1,
		EventType:      timelineevent.EventTypeUserQuestion,
		Content:        input.Message.Content,
	})
	if err != nil {
		logger.Warn("Failed to create user_question timeline event", "error", err)
		// Non-fatal: continue execution
	}

	// 5. Build ChatContext (GetSessionTimeline → FormatInvestigationContext)
	chatContext := e.buildChatContext(execCtx, input)

	// 6. Update Stage status: active, publish stage.status: started, start heartbeat
	if updateErr := e.stageService.UpdateAgentExecutionStatus(execCtx, exec.ID, agentexecution.StatusActive, ""); updateErr != nil {
		logger.Warn("Failed to update agent execution to active", "error", updateErr)
	}
	publishStageStatus(execCtx, e.eventPublisher, input.Session.ID, stageID, "Chat Response", stageIndex, events.StageStatusStarted)

	heartbeatCtx, cancelHeartbeat := context.WithCancel(execCtx)
	defer cancelHeartbeat()
	go e.runChatHeartbeat(heartbeatCtx, input.Chat.ID)

	// 7. Create MCP ToolExecutor (shared helper, same as investigation)
	toolExecutor, failedServers := createToolExecutor(execCtx, e.mcpFactory, serverIDs, toolFilter, logger)
	defer func() { _ = toolExecutor.Close() }()

	// 8. Build ExecutionContext (with ChatContext populated)
	agentExecCtx := &agent.ExecutionContext{
		SessionID:      input.Session.ID,
		StageID:        stageID,
		ExecutionID:    exec.ID,
		AgentName:      resolvedConfig.AgentName,
		AgentIndex:     1,
		AlertData:      input.Session.AlertData,
		AlertType:      input.Session.AlertType,
		RunbookContent: config.GetBuiltinConfig().DefaultRunbook,
		Config:         resolvedConfig,
		LLMClient:      e.llmClient,
		ToolExecutor:   toolExecutor,
		EventPublisher: e.eventPublisher,
		PromptBuilder:  e.promptBuilder,
		ChatContext:    chatContext,
		FailedServers:  failedServers,
		Services: &agent.ServiceBundle{
			Timeline:    e.timelineService,
			Message:     e.messageService,
			Interaction: e.interactionService,
			Stage:       e.stageService,
		},
	}

	// 9. Create agent via AgentFactory
	agentInstance, err := e.agentFactory.CreateAgent(agentExecCtx)
	if err != nil {
		logger.Error("Failed to create agent", "error", err)
		if updateErr := e.stageService.UpdateAgentExecutionStatus(execCtx, exec.ID, agentexecution.StatusFailed, err.Error()); updateErr != nil {
			logger.Error("Failed to update agent execution status", "error", updateErr)
		}
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	// 10. Execute agent (same path as investigation — controller handles chat via ChatContext)
	result, execErr := agentInstance.Execute(execCtx, agentExecCtx, "") // no prevStageContext for chat

	// 11. Determine terminal status
	terminalStatus := events.StageStatusFailed
	agentStatus := agent.ExecutionStatusFailed
	errMsg := ""
	if execErr != nil {
		errMsg = execErr.Error()
		// Check if the error was due to cancellation/timeout
		if execCtx.Err() == context.DeadlineExceeded {
			agentStatus = agent.ExecutionStatusTimedOut
			terminalStatus = events.StageStatusTimedOut
		} else if execCtx.Err() != nil {
			agentStatus = agent.ExecutionStatusCancelled
			terminalStatus = events.StageStatusCancelled
		}
	} else if result != nil {
		agentStatus = result.Status
		terminalStatus = mapChatAgentStatus(result.Status)
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
	}

	// 12. Update AgentExecution terminal status (use background context — execCtx may be cancelled)
	entStatus := mapAgentStatusToEntStatus(agentStatus)
	if updateErr := e.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entStatus, errMsg); updateErr != nil {
		logger.Error("Failed to update agent execution status", "error", updateErr)
	}

	// 13. Update Stage terminal status
	if updateErr := e.stageService.UpdateStageStatus(context.Background(), stageID); updateErr != nil {
		logger.Error("Failed to update stage status", "error", updateErr)
	}

	// 14. Publish stage.status: completed/failed/cancelled/timed_out
	publishStageStatus(context.Background(), e.eventPublisher, input.Session.ID, stageID, "Chat Response", stageIndex, terminalStatus)

	// 15. Stop heartbeat
	cancelHeartbeat()

	// 16. Schedule event cleanup (cutoff = now, so events from subsequent stages are preserved)
	e.scheduleStageEventCleanup(stageID, time.Now())

	logger.Info("Chat executor: execution complete", "status", terminalStatus)
}

// ────────────────────────────────────────────────────────────
// Context building
// ────────────────────────────────────────────────────────────

// buildChatContext retrieves the full session timeline and formats it for the chat agent.
func (e *ChatMessageExecutor) buildChatContext(ctx context.Context, input ChatExecuteInput) *agent.ChatContext {
	timelineEvents, err := e.timelineService.GetSessionTimeline(ctx, input.Session.ID)
	if err != nil {
		slog.Warn("Failed to get session timeline for chat context",
			"session_id", input.Session.ID,
			"error", err,
		)
		// Fail-open: empty context (agent still has tools)
		return &agent.ChatContext{UserQuestion: input.Message.Content}
	}

	return &agent.ChatContext{
		UserQuestion:         input.Message.Content,
		InvestigationContext: agentctx.FormatInvestigationContext(timelineEvents),
	}
}

// ────────────────────────────────────────────────────────────
// Cancellation
// ────────────────────────────────────────────────────────────

// CancelExecution cancels the active execution for a chat.
// Returns true if an active execution was found and cancelled.
func (e *ChatMessageExecutor) CancelExecution(chatID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if cancel, ok := e.activeExecs[chatID]; ok {
		cancel()
		return true
	}
	return false
}

// CancelBySessionID looks up the chat for the given session and cancels any active execution.
// Returns true if an active execution was found and cancelled.
func (e *ChatMessageExecutor) CancelBySessionID(ctx context.Context, sessionID string) bool {
	chatObj, err := e.chatService.GetChatBySessionID(ctx, sessionID)
	if err != nil || chatObj == nil {
		return false
	}
	return e.CancelExecution(chatObj.ID)
}

// Stop marks the executor as stopped, cancels all active executions, and waits
// for goroutines to drain. Safe to call multiple times.
func (e *ChatMessageExecutor) Stop() {
	e.mu.Lock()
	e.stopped = true
	// Cancel all active executions
	for _, cancel := range e.activeExecs {
		cancel()
	}
	e.mu.Unlock()

	// Wait for goroutines to finish
	e.wg.Wait()
}

// ────────────────────────────────────────────────────────────
// Heartbeat
// ────────────────────────────────────────────────────────────

// runChatHeartbeat periodically updates Chat.last_interaction_at for orphan detection.
func (e *ChatMessageExecutor) runChatHeartbeat(ctx context.Context, chatID string) {
	interval := e.execConfig.HeartbeatInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.dbClient.Chat.UpdateOneID(chatID).
				SetLastInteractionAt(time.Now()).
				Exec(ctx); err != nil {
				slog.Warn("Chat heartbeat update failed",
					"chat_id", chatID,
					"error", err,
				)
			}
		}
	}
}

// ────────────────────────────────────────────────────────────
// Event cleanup
// ────────────────────────────────────────────────────────────

// scheduleStageEventCleanup schedules deletion of transient Event records
// after a 60-second grace period (same pattern as Worker).
// cutoff is the timestamp at which this stage finished; only events created
// at or before this time are deleted, preserving events from subsequent stages.
func (e *ChatMessageExecutor) scheduleStageEventCleanup(stageID string, cutoff time.Time) {
	time.AfterFunc(60*time.Second, func() {
		if err := e.cleanupStageEvents(context.Background(), stageID, cutoff); err != nil {
			slog.Warn("Failed to cleanup stage events after grace period",
				"stage_id", stageID,
				"error", err,
			)
		}
	})
}

// cleanupStageEvents removes transient Event records for a given stage's session,
// restricted to events created at or before the cutoff time so that events from
// a subsequent stage started within the grace period are preserved.
func (e *ChatMessageExecutor) cleanupStageEvents(ctx context.Context, stageID string, cutoff time.Time) error {
	stg, err := e.stageService.GetStageByID(ctx, stageID, false)
	if err != nil {
		return fmt.Errorf("failed to get stage for cleanup: %w", err)
	}
	_, err = e.dbClient.Event.Delete().
		Where(
			event.SessionIDEQ(stg.SessionID),
			event.CreatedAtLTE(cutoff),
		).
		Exec(ctx)
	return err
}

// ────────────────────────────────────────────────────────────
// Internal helpers
// ────────────────────────────────────────────────────────────

// registerExecution tracks a chat execution for cancellation support.
func (e *ChatMessageExecutor) registerExecution(chatID string, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.activeExecs[chatID] = cancel
}

// unregisterExecution removes a chat execution from tracking.
func (e *ChatMessageExecutor) unregisterExecution(chatID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.activeExecs, chatID)
}

// finishStage publishes terminal stage status and updates the Stage DB record.
// Used for early-exit error paths.
func (e *ChatMessageExecutor) finishStage(stageID, sessionID, stageName string, stageIndex int, status, errMsg string) {
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, stageName, stageIndex, status)
	if updateErr := e.stageService.UpdateStageStatus(context.Background(), stageID); updateErr != nil {
		slog.Warn("Failed to update stage status on early exit",
			"stage_id", stageID,
			"error", updateErr,
			"original_error", errMsg,
		)
	}
}

// mapChatAgentStatus maps agent execution status to event status string.
// NOTE: This parallels mapTerminalStatus in executor.go which maps
// alertsession.Status → event status. If the mapping logic changes,
// both functions should be updated to stay consistent.
func mapChatAgentStatus(status agent.ExecutionStatus) string {
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
