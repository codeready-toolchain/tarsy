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
	"github.com/codeready-toolchain/tarsy/pkg/runbook"
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
	runbookService *runbook.Service

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
// runbookService may be nil (uses config default runbook content).
func NewChatMessageExecutor(
	cfg *config.Config,
	dbClient *ent.Client,
	llmClient agent.LLMClient,
	mcpFactory *mcp.ClientFactory,
	eventPublisher agent.EventPublisher,
	execConfig ChatMessageExecutorConfig,
	runbookService *runbook.Service,
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
		runbookService:     runbookService,
		timelineService:    services.NewTimelineService(dbClient),
		stageService:       services.NewStageService(dbClient),
		chatService:        services.NewChatService(dbClient),
		messageService:     msgService,
		interactionService: services.NewInteractionService(dbClient, msgService),
		activeExecs:        make(map[string]context.CancelFunc),
	}
}

// resolveRunbook resolves runbook content for a session using the RunbookService.
// Falls back to config defaults on error or when the service is nil.
func (e *ChatMessageExecutor) resolveRunbook(ctx context.Context, session *ent.AlertSession) string {
	if e.runbookService == nil {
		return e.cfg.Defaults.Runbook
	}

	alertURL := ""
	if session.RunbookURL != nil {
		alertURL = *session.RunbookURL
	}

	content, err := e.runbookService.Resolve(ctx, alertURL)
	if err != nil {
		slog.Warn("Chat runbook resolution failed, using default",
			"session_id", session.ID,
			"error", err)
		return e.cfg.Defaults.Runbook
	}
	return content
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

	// Resolve LLM provider name for observability (defaults → chain → chat).
	// Hoisted above config resolution so it's available in error paths.
	// Uses the shared helper to stay in sync with ResolveChatAgentConfig.
	chatProviderName := agent.ResolveChatProviderName(e.cfg.Defaults, chain, chain.Chat)

	resolvedConfig, err := agent.ResolveChatAgentConfig(e.cfg, chain, chain.Chat)
	if err != nil {
		logger.Error("Failed to resolve chat agent config", "error", err)
		// Best-effort: create a failed AgentExecution for audit trail.
		e.createFailedChatExecution(stageID, input.Session.ID, "chat", chatProviderName, err.Error(), logger)
		e.finishStage(stageID, input.Session.ID, "Chat Response", stageIndex, events.StageStatusFailed, err.Error())
		return
	}

	// 2. Resolve MCP selection (shared helper, handles session override)
	serverIDs, toolFilter, err := resolveMCPSelection(input.Session, resolvedConfig, e.cfg.MCPServerRegistry)
	if err != nil {
		logger.Error("Failed to resolve MCP selection", "error", err)
		// Best-effort: create a failed AgentExecution for audit trail.
		e.createFailedChatExecution(stageID, input.Session.ID, resolvedConfig.AgentName, chatProviderName, err.Error(), logger)
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
		LLMProvider:       chatProviderName,
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
	userQuestionSeq := maxSeq + 1
	userQuestionEvent, err := e.timelineService.CreateTimelineEvent(execCtx, models.CreateTimelineEventRequest{
		SessionID:      input.Session.ID,
		StageID:        &stageID,
		ExecutionID:    &exec.ID,
		SequenceNumber: userQuestionSeq,
		EventType:      timelineevent.EventTypeUserQuestion,
		Status:         timelineevent.StatusCompleted, // fire-and-forget: full content known at creation
		Content:        input.Message.Content,
	})
	if err != nil {
		logger.Warn("Failed to create user_question timeline event", "error", err)
		// Non-fatal: continue execution
	} else if e.eventPublisher != nil {
		// Publish via WS so the dashboard can render the user question in real time.
		if pubErr := e.eventPublisher.PublishTimelineCreated(execCtx, input.Session.ID, events.TimelineCreatedPayload{
			BasePayload: events.BasePayload{
				Type:      events.EventTypeTimelineCreated,
				SessionID: input.Session.ID,
				Timestamp: userQuestionEvent.CreatedAt.Format(time.RFC3339Nano),
			},
			EventID:        userQuestionEvent.ID,
			StageID:        stageID,
			ExecutionID:    exec.ID,
			EventType:      timelineevent.EventTypeUserQuestion,
			Status:         timelineevent.StatusCompleted,
			Content:        input.Message.Content,
			SequenceNumber: userQuestionSeq,
		}); pubErr != nil {
			logger.Warn("Failed to publish user_question timeline event", "error", pubErr)
		}
	}

	// 5. Build ChatContext (structured investigation history)
	chatContext := e.buildChatContext(execCtx, input)

	// 6. Update Stage status: active, publish stage.status: started, start heartbeat
	if updateErr := e.stageService.UpdateAgentExecutionStatus(execCtx, exec.ID, agentexecution.StatusActive, ""); updateErr != nil {
		logger.Warn("Failed to update agent execution to active", "error", updateErr)
	}
	publishExecutionStatus(execCtx, e.eventPublisher, input.Session.ID, stageID, exec.ID, 1, string(agentexecution.StatusActive), "")
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
		RunbookContent: e.resolveRunbook(execCtx, input.Session),
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
		publishExecutionStatus(execCtx, e.eventPublisher, input.Session.ID, stageID, exec.ID, 1, string(agentexecution.StatusFailed), err.Error())
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
		// Check if context was cancelled during execution even though agent returned OK
		if execCtx.Err() == context.DeadlineExceeded {
			agentStatus = agent.ExecutionStatusTimedOut
			terminalStatus = events.StageStatusTimedOut
		} else if execCtx.Err() != nil {
			agentStatus = agent.ExecutionStatusCancelled
			terminalStatus = events.StageStatusCancelled
		} else {
			agentStatus = result.Status
			terminalStatus = mapChatAgentStatus(result.Status)
		}
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
	}

	// 12. Update AgentExecution terminal status (use background context — execCtx may be cancelled)
	entStatus := mapAgentStatusToEntStatus(agentStatus)
	if updateErr := e.stageService.UpdateAgentExecutionStatus(context.Background(), exec.ID, entStatus, errMsg); updateErr != nil {
		logger.Error("Failed to update agent execution status", "error", updateErr)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, input.Session.ID, stageID, exec.ID, 1, string(entStatus), errMsg)

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

// buildChatContext retrieves the structured investigation history for the chat agent.
// Stages are grouped with per-agent timelines, matching the structured format
// used by synthesis. Synthesis results are paired with their parent stages.
func (e *ChatMessageExecutor) buildChatContext(ctx context.Context, input ChatExecuteInput) *agent.ChatContext {
	logger := slog.With("session_id", input.Session.ID)

	// 1. Get all stages for the session (with agent execution edges).
	stages, err := e.stageService.GetStagesBySession(ctx, input.Session.ID, true)
	if err != nil {
		logger.Warn("Failed to get stages for chat context", "error", err)
		return &agent.ChatContext{UserQuestion: input.Message.Content}
	}

	// 2. Build structured stage investigations.
	//    - Investigation stages → per-agent timelines
	//    - Synthesis stages (name ends with " - Synthesis") → paired with parent
	//    - Chat stages (have chat_id) → treated as previous chat Q&A
	//    Synthesis results are keyed by parent stage ID for pairing.
	//    Since stages are ordered by StageIndex, each synthesis stage is paired
	//    with the closest preceding investigation stage whose name matches.
	//    This avoids collisions when multiple stages share the same name.
	synthResults := make(map[string]string) // parent stage ID → synthesis final analysis
	for i, stg := range stages {
		if strings.HasSuffix(stg.StageName, " - Synthesis") {
			parentName := strings.TrimSuffix(stg.StageName, " - Synthesis")
			// Scan backwards to find the nearest parent investigation stage.
			for j := i - 1; j >= 0; j-- {
				if stages[j].StageName == parentName {
					if fa := e.extractFinalAnalysis(ctx, stg); fa != "" {
						synthResults[stages[j].ID] = fa
					}
					break
				}
			}
		}
	}

	var investigations []agentctx.StageInvestigation
	var previousChats []chatQA
	var executiveSummary string

	for _, stg := range stages {
		// Skip synthesis stages — already paired above.
		if strings.HasSuffix(stg.StageName, " - Synthesis") {
			continue
		}

		// Chat stages → collect as previous Q&A (skip the current chat's stage).
		if stg.ChatID != nil && *stg.ChatID != "" {
			isCurrentChat := stg.ChatUserMessageID != nil && *stg.ChatUserMessageID == input.Message.ID
			if !isCurrentChat {
				if qa := e.buildChatQA(ctx, stg); qa.Question != "" {
					previousChats = append(previousChats, qa)
				}
			}
			continue
		}

		// Investigation stage — build per-agent timelines.
		// Sort by agent_index for deterministic ordering (edge loading doesn't guarantee order).
		execs := stg.Edges.AgentExecutions
		sort.Slice(execs, func(i, j int) bool {
			return execs[i].AgentIndex < execs[j].AgentIndex
		})
		agents := make([]agentctx.AgentInvestigation, len(execs))
		for i, exec := range execs {
			var events []*ent.TimelineEvent
			timeline, tlErr := e.timelineService.GetAgentTimeline(ctx, exec.ID)
			if tlErr != nil {
				logger.Warn("Failed to get agent timeline for chat context",
					"execution_id", exec.ID, "error", tlErr)
			} else {
				events = timeline
			}

			agents[i] = agentctx.AgentInvestigation{
				AgentName:    exec.AgentName,
				AgentIndex:   exec.AgentIndex,
				Strategy:     exec.IterationStrategy,
				LLMProvider:  stringFromNillable(exec.LlmProvider),
				Status:       mapExecStatusToSessionStatus(exec.Status),
				Events:       events,
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

	// 3. Get executive summary from session-level timeline event.
	executiveSummary = e.getExecutiveSummary(ctx, input.Session.ID)

	// 4. Format the structured investigation context.
	formattedContext := agentctx.FormatStructuredInvestigation(investigations, executiveSummary)

	// 5. Append previous chat Q&A if any.
	if len(previousChats) > 0 {
		formattedContext += formatPreviousChats(previousChats)
	}

	return &agent.ChatContext{
		UserQuestion:         input.Message.Content,
		InvestigationContext: formattedContext,
	}
}

// chatQA holds a previous chat question and answer for context.
type chatQA struct {
	Question string
	Answer   string
}

// extractFinalAnalysis gets the final_analysis content from a stage's timeline.
// Uses pre-loaded stg.Edges.AgentExecutions (loaded via GetStagesBySession with
// withExecutions=true) to avoid a redundant DB round-trip.
func (e *ChatMessageExecutor) extractFinalAnalysis(ctx context.Context, stg *ent.Stage) string {
	execs := stg.Edges.AgentExecutions
	if len(execs) == 0 {
		return ""
	}
	for _, exec := range execs {
		timeline, err := e.timelineService.GetAgentTimeline(ctx, exec.ID)
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

// buildChatQA extracts the user question and agent answer from a chat stage.
func (e *ChatMessageExecutor) buildChatQA(ctx context.Context, stg *ent.Stage) chatQA {
	var qa chatQA
	execs := stg.Edges.AgentExecutions
	if len(execs) == 0 {
		return qa
	}
	// Chat stage has one execution — get its timeline.
	timeline, err := e.timelineService.GetAgentTimeline(ctx, execs[0].ID)
	if err != nil {
		return qa
	}
	for _, evt := range timeline {
		if evt.EventType == timelineevent.EventTypeUserQuestion {
			qa.Question = evt.Content
		}
		if evt.EventType == timelineevent.EventTypeFinalAnalysis {
			qa.Answer = evt.Content
		}
	}
	return qa
}

// getExecutiveSummary retrieves the executive summary from session-level timeline events.
func (e *ChatMessageExecutor) getExecutiveSummary(ctx context.Context, sessionID string) string {
	// Executive summary is stored as a session-level timeline event with no execution_id.
	// Use GetSessionTimeline and filter for executive_summary event type.
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

// formatPreviousChats formats previous chat Q&A for the context.
func formatPreviousChats(chats []chatQA) string {
	var sb strings.Builder
	sb.WriteString("## Previous Chat Messages\n\n")
	for i, qa := range chats {
		fmt.Fprintf(&sb, "**Q%d:** %s\n\n", i+1, qa.Question)
		if qa.Answer != "" {
			fmt.Fprintf(&sb, "**A%d:** %s\n\n", i+1, qa.Answer)
		}
	}
	return sb.String()
}

// mapExecStatusToSessionStatus maps agent execution status to alertsession.Status.
func mapExecStatusToSessionStatus(status agentexecution.Status) alertsession.Status {
	switch status {
	case agentexecution.StatusCompleted:
		return alertsession.StatusCompleted
	case agentexecution.StatusFailed:
		return alertsession.StatusFailed
	case agentexecution.StatusCancelled:
		return alertsession.StatusCancelled
	case agentexecution.StatusTimedOut:
		return alertsession.StatusTimedOut
	default:
		return alertsession.StatusInProgress
	}
}

// stringFromNillable safely dereferences a *string, returning "" for nil.
func stringFromNillable(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

// createFailedChatExecution creates a best-effort failed AgentExecution record
// for audit trail when early-exit errors occur before the normal execution
// record is created. Errors are logged but not returned (best-effort).
func (e *ChatMessageExecutor) createFailedChatExecution(
	stageID, sessionID, agentName, llmProvider, errMsg string, logger *slog.Logger,
) {
	// Use context.Background() for both create and status update since this is
	// a best-effort audit path — the incoming ctx may be near its deadline.
	exec, createErr := e.stageService.CreateAgentExecution(context.Background(), models.CreateAgentExecutionRequest{
		StageID:     stageID,
		SessionID:   sessionID,
		AgentName:   agentName,
		AgentIndex:  1,
		LLMProvider: llmProvider,
	})
	if createErr != nil {
		logger.Error("Failed to create failed agent execution record", "error", createErr)
		return
	}
	if updateErr := e.stageService.UpdateAgentExecutionStatus(
		context.Background(), exec.ID, agentexecution.StatusFailed, errMsg,
	); updateErr != nil {
		logger.Error("Failed to update agent execution status to failed", "error", updateErr)
	}
	publishExecutionStatus(context.Background(), e.eventPublisher, sessionID, stageID, exec.ID, 1, string(agentexecution.StatusFailed), errMsg)
}

// finishStage publishes terminal stage status and updates the Stage DB record.
// Used for early-exit error paths where no AgentExecution may exist yet.
// Uses ForceStageFailure to directly set terminal status, bypassing the
// execution-derived UpdateStageStatus which is a no-op without executions.
func (e *ChatMessageExecutor) finishStage(stageID, sessionID, stageName string, stageIndex int, status, errMsg string) {
	publishStageStatus(context.Background(), e.eventPublisher, sessionID, stageID, stageName, stageIndex, status)
	if updateErr := e.stageService.ForceStageFailure(context.Background(), stageID, errMsg); updateErr != nil {
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
