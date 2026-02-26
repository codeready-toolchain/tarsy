package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

// SubAgentRunner manages the lifecycle of sub-agent goroutines within an
// orchestrator execution. It provides push-based result delivery (via a
// buffered channel) and lifecycle management (cancel, wait).
type SubAgentRunner struct {
	mu         sync.Mutex
	executions map[string]*subAgentExecution
	// Slots reserved by in-flight Dispatch calls that passed the concurrency
	// check but haven't registered in executions yet. Protected by mu.
	reserved int

	// Buffered channel for completed sub-agent results.
	// Capacity = MaxConcurrentAgents to prevent goroutine blocking.
	resultsCh chan *SubAgentResult

	// Closed during CancelAll to signal goroutines that the orchestrator is
	// shutting down and results should be dropped. Individual sub-agent
	// cancellations still deliver their result to resultsCh.
	closeCh chan struct{}

	// Atomic count of sub-agents whose results have not yet been consumed.
	pending int32

	// parentCtx is the session-level context used to derive sub-agent contexts.
	// Sub-agent goroutines must NOT use the per-iteration context from
	// executeToolCall (which is cancelled at the end of each iteration).
	parentCtx context.Context

	deps         *SubAgentDeps
	parentExecID string
	sessionID    string
	stageID      string

	// Atomic counter for sub-agent agent_index (starts at 1).
	nextSubAgentIndex int32

	registry   *config.SubAgentRegistry
	guardrails *OrchestratorGuardrails
}

// NewSubAgentRunner creates a runner for managing sub-agents within an
// orchestrator execution. parentCtx should be the session-level context
// (not a per-iteration context) so sub-agent goroutines outlive individual
// orchestrator iterations.
func NewSubAgentRunner(
	parentCtx context.Context,
	deps *SubAgentDeps,
	parentExecID string,
	sessionID string,
	stageID string,
	registry *config.SubAgentRegistry,
	guardrails *OrchestratorGuardrails,
) *SubAgentRunner {
	return &SubAgentRunner{
		executions:   make(map[string]*subAgentExecution),
		resultsCh:    make(chan *SubAgentResult, guardrails.MaxConcurrentAgents),
		closeCh:      make(chan struct{}),
		parentCtx:    parentCtx,
		deps:         deps,
		parentExecID: parentExecID,
		sessionID:    sessionID,
		stageID:      stageID,
		registry:     registry,
		guardrails:   guardrails,
	}
}

// Dispatch starts a sub-agent to execute the given task. Returns immediately
// with the execution ID. The sub-agent result will be delivered to the results
// channel when the goroutine finishes.
func (r *SubAgentRunner) Dispatch(ctx context.Context, name, task string) (string, error) {
	if _, ok := r.registry.Get(name); !ok {
		return "", fmt.Errorf("%w: %s", ErrAgentNotFound, name)
	}

	// Reserve a slot atomically with the concurrency check to prevent TOCTOU
	// races where concurrent Dispatch calls both pass the check.
	r.mu.Lock()
	activeCount := 0
	for _, exec := range r.executions {
		if exec.status == agent.ExecutionStatusActive {
			activeCount++
		}
	}
	if activeCount+r.reserved >= r.guardrails.MaxConcurrentAgents {
		r.mu.Unlock()
		return "", fmt.Errorf("%w: limit is %d", ErrMaxConcurrentAgents, r.guardrails.MaxConcurrentAgents)
	}
	r.reserved++
	r.mu.Unlock()

	// Release the reservation on any error path. On success, it's released
	// when the execution is registered in r.executions.
	releaseReservation := true
	defer func() {
		if releaseReservation {
			r.mu.Lock()
			r.reserved--
			r.mu.Unlock()
		}
	}()

	agentIndex := int(atomic.AddInt32(&r.nextSubAgentIndex, 1))

	resolvedConfig, err := agent.ResolveAgentConfig(
		r.deps.Config, r.deps.Chain,
		config.StageConfig{},
		config.StageAgentConfig{Name: name},
	)
	if err != nil {
		return "", fmt.Errorf("failed to resolve config for sub-agent %s: %w", name, err)
	}

	parentID := r.parentExecID
	exec, err := r.deps.StageService.CreateAgentExecution(ctx, models.CreateAgentExecutionRequest{
		StageID:           r.stageID,
		SessionID:         r.sessionID,
		AgentName:         name,
		AgentIndex:        agentIndex,
		LLMBackend:        resolvedConfig.LLMBackend,
		LLMProvider:       resolvedConfig.LLMProviderName,
		ParentExecutionID: &parentID,
		Task:              &task,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create sub-agent execution record: %w", err)
	}
	executionID := exec.ID

	if updateErr := r.deps.StageService.UpdateAgentExecutionStatus(
		ctx, executionID, agentexecution.StatusActive, "",
	); updateErr != nil {
		slog.Warn("Failed to mark sub-agent execution as active",
			"execution_id", executionID, "error", updateErr)
	}

	maxSeq, _ := r.deps.TimelineService.GetMaxSequenceNumber(ctx, executionID)
	_, _ = r.deps.TimelineService.CreateTimelineEvent(ctx, models.CreateTimelineEventRequest{
		SessionID:      r.sessionID,
		StageID:        &r.stageID,
		ExecutionID:    &executionID,
		SequenceNumber: maxSeq + 1,
		EventType:      timelineevent.EventTypeTaskAssigned,
		Status:         timelineevent.StatusCompleted,
		Content:        task,
	})

	subCtx, cancel := context.WithTimeout(r.parentCtx, r.guardrails.AgentTimeout)

	subExec := &subAgentExecution{
		executionID: executionID,
		agentName:   name,
		task:        task,
		status:      agent.ExecutionStatusActive,
		cancel:      cancel,
		done:        make(chan struct{}),
	}

	// Register the execution and release the reservation in a single lock hold
	// so concurrent Dispatch calls see a consistent count. cancel is set above
	// so Cancel() never sees a nil function pointer.
	r.mu.Lock()
	r.executions[executionID] = subExec
	r.reserved--
	releaseReservation = false
	r.mu.Unlock()

	atomic.AddInt32(&r.pending, 1)

	go r.runSubAgent(subCtx, cancel, subExec, resolvedConfig, agentIndex)

	return executionID, nil
}

// runSubAgent executes a sub-agent in a goroutine and delivers the result.
func (r *SubAgentRunner) runSubAgent(
	ctx context.Context,
	cancel context.CancelFunc,
	exec *subAgentExecution,
	resolvedConfig *agent.ResolvedAgentConfig,
	agentIndex int,
) {
	defer cancel()
	defer close(exec.done)

	logger := slog.With(
		"parent_exec_id", r.parentExecID,
		"sub_exec_id", exec.executionID,
		"sub_agent", exec.agentName,
	)

	toolExecutor := r.createSubAgentToolExecutor(ctx, resolvedConfig, logger)
	defer func() { _ = toolExecutor.Close() }()

	execCtx := &agent.ExecutionContext{
		SessionID:      r.sessionID,
		StageID:        r.stageID,
		ExecutionID:    exec.executionID,
		AgentName:      exec.agentName,
		AgentIndex:     agentIndex,
		AlertData:      r.deps.AlertData,
		AlertType:      r.deps.AlertType,
		RunbookContent: r.deps.RunbookContent,
		Config:         resolvedConfig,
		LLMClient:      r.deps.LLMClient,
		ToolExecutor:   toolExecutor,
		EventPublisher: r.deps.EventPublisher,
		PromptBuilder:  r.deps.PromptBuilder,
		SubAgent: &agent.SubAgentContext{
			Task:         exec.task,
			ParentExecID: r.parentExecID,
		},
		Services: &agent.ServiceBundle{
			Timeline:    r.deps.TimelineService,
			Message:     r.deps.MessageService,
			Interaction: r.deps.InteractionService,
			Stage:       r.deps.StageService,
		},
	}

	agentInstance, err := r.deps.AgentFactory.CreateAgent(execCtx)
	if err != nil {
		logger.Error("Failed to create sub-agent", "error", err)
		r.completeSubAgent(exec, agent.ExecutionStatusFailed, "", err.Error())
		return
	}

	result, err := agentInstance.Execute(ctx, execCtx, "")
	if err != nil {
		status := agent.ExecutionStatusFailed
		if ctx.Err() == context.DeadlineExceeded {
			status = agent.ExecutionStatusTimedOut
		} else if ctx.Err() != nil {
			status = agent.ExecutionStatusCancelled
		}
		logger.Error("Sub-agent execution error", "error", err, "resolved_status", status)
		r.completeSubAgent(exec, status, "", err.Error())
		return
	}

	// BaseAgent wraps controller errors in result.Error (returning (result, nil)).
	var errMsg string
	if result.Error != nil {
		errMsg = result.Error.Error()
	}
	r.completeSubAgent(exec, result.Status, result.FinalAnalysis, errMsg)
}

// completeSubAgent updates the execution record and delivers the result.
func (r *SubAgentRunner) completeSubAgent(
	exec *subAgentExecution,
	status agent.ExecutionStatus,
	finalAnalysis string,
	errMsg string,
) {
	r.mu.Lock()
	exec.status = status
	r.mu.Unlock()

	entStatus := mapToEntStatus(status)
	if updateErr := r.deps.StageService.UpdateAgentExecutionStatus(
		context.Background(), exec.executionID, entStatus, errMsg,
	); updateErr != nil {
		slog.Warn("Failed to update sub-agent execution status",
			"execution_id", exec.executionID, "status", status, "error", updateErr)
	}

	result := &SubAgentResult{
		ExecutionID: exec.executionID,
		AgentName:   exec.agentName,
		Task:        exec.task,
		Status:      status,
		Result:      finalAnalysis,
		Error:       errMsg,
	}

	// Non-blocking on shutdown: if closeCh is closed (CancelAll during cleanup),
	// drop the result. The orchestrator is shutting down and won't consume it.
	// Individual sub-agent cancellations still deliver their result normally.
	select {
	case r.resultsCh <- result:
	case <-r.closeCh:
	}
}

// createSubAgentToolExecutor builds a ToolExecutor for the sub-agent's MCP servers.
func (r *SubAgentRunner) createSubAgentToolExecutor(
	ctx context.Context,
	resolvedConfig *agent.ResolvedAgentConfig,
	logger *slog.Logger,
) agent.ToolExecutor {
	if r.deps.MCPFactory != nil && len(resolvedConfig.MCPServers) > 0 {
		mcpExecutor, _, mcpErr := r.deps.MCPFactory.CreateToolExecutor(
			ctx, resolvedConfig.MCPServers, nil,
		)
		if mcpErr != nil {
			logger.Warn("Failed to create MCP tool executor for sub-agent, using stub",
				"error", mcpErr)
			return agent.NewStubToolExecutor(nil)
		}
		return mcpExecutor
	}
	return agent.NewStubToolExecutor(nil)
}

// TryGetNext returns a completed sub-agent result without blocking.
// Returns (nil, false) if no results are available.
func (r *SubAgentRunner) TryGetNext() (*SubAgentResult, bool) {
	select {
	case result := <-r.resultsCh:
		atomic.AddInt32(&r.pending, -1)
		return result, true
	default:
		return nil, false
	}
}

// WaitForNext blocks until a sub-agent result is available or the context
// is cancelled. Called when the LLM has no tool calls but sub-agents are
// still pending.
func (r *SubAgentRunner) WaitForNext(ctx context.Context) (*SubAgentResult, error) {
	select {
	case result := <-r.resultsCh:
		atomic.AddInt32(&r.pending, -1)
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// HasPending returns true if any sub-agent results have not been consumed.
func (r *SubAgentRunner) HasPending() bool {
	return atomic.LoadInt32(&r.pending) > 0
}

// Cancel cancels a specific sub-agent by execution ID.
// Returns a human-readable status string.
func (r *SubAgentRunner) Cancel(executionID string) (string, error) {
	r.mu.Lock()
	exec, ok := r.executions[executionID]
	if !ok {
		r.mu.Unlock()
		return "", fmt.Errorf("%w: %s", ErrExecutionNotFound, executionID)
	}
	if exec.status != agent.ExecutionStatusActive {
		status := exec.status
		r.mu.Unlock()
		return fmt.Sprintf("already %s", status), nil
	}
	r.mu.Unlock()

	exec.cancel()
	return "cancellation requested", nil
}

// List returns a snapshot of all dispatched sub-agents and their statuses.
func (r *SubAgentRunner) List() []SubAgentStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	statuses := make([]SubAgentStatus, 0, len(r.executions))
	for _, exec := range r.executions {
		statuses = append(statuses, SubAgentStatus{
			ExecutionID: exec.executionID,
			AgentName:   exec.agentName,
			Task:        exec.task,
			Status:      exec.status,
		})
	}
	return statuses
}

// CancelAll cancels all running sub-agent contexts and signals goroutines
// to drop undelivered results (via closeCh).
func (r *SubAgentRunner) CancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	select {
	case <-r.closeCh:
		// already closed
	default:
		close(r.closeCh)
	}

	for _, exec := range r.executions {
		if exec.status == agent.ExecutionStatusActive && exec.cancel != nil {
			exec.cancel()
		}
	}
}

// WaitAll waits for all sub-agent goroutines to finish. Called during cleanup
// from CompositeToolExecutor.Close.
func (r *SubAgentRunner) WaitAll(ctx context.Context) {
	r.mu.Lock()
	execs := make([]*subAgentExecution, 0, len(r.executions))
	for _, exec := range r.executions {
		execs = append(execs, exec)
	}
	r.mu.Unlock()

	for _, exec := range execs {
		select {
		case <-exec.done:
		case <-ctx.Done():
			return
		}
	}
}

func mapToEntStatus(status agent.ExecutionStatus) agentexecution.Status {
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
