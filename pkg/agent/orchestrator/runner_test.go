package orchestrator

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	"github.com/codeready-toolchain/tarsy/pkg/services"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Channel mechanics tests (no DB) ────────────────────────────────────────

func TestSubAgentRunner_TryGetNext_Empty(t *testing.T) {
	r := newMinimalRunner(1)
	result, ok := r.TryGetNext()
	assert.Nil(t, result)
	assert.False(t, ok)
}

func TestSubAgentRunner_TryGetNext_WithResult(t *testing.T) {
	r := newMinimalRunner(1)
	atomic.StoreInt32(&r.pending, 1)
	r.resultsCh <- &SubAgentResult{
		ExecutionID: "exec-1",
		AgentName:   "TestAgent",
		Status:      agent.ExecutionStatusCompleted,
		Result:      "done",
	}

	result, ok := r.TryGetNext()
	require.True(t, ok)
	assert.Equal(t, "exec-1", result.ExecutionID)
	assert.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	assert.False(t, r.HasPending())
}

func TestSubAgentRunner_WaitForNext_GetsResult(t *testing.T) {
	r := newMinimalRunner(1)
	atomic.StoreInt32(&r.pending, 1)

	go func() {
		time.Sleep(50 * time.Millisecond)
		r.resultsCh <- &SubAgentResult{
			ExecutionID: "exec-2",
			Status:      agent.ExecutionStatusCompleted,
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := r.WaitForNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, "exec-2", result.ExecutionID)
	assert.False(t, r.HasPending())
}

func TestSubAgentRunner_WaitForNext_ContextCancelled(t *testing.T) {
	r := newMinimalRunner(1)
	atomic.StoreInt32(&r.pending, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := r.WaitForNext(ctx)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSubAgentRunner_HasPending(t *testing.T) {
	r := newMinimalRunner(5)
	assert.False(t, r.HasPending())

	atomic.StoreInt32(&r.pending, 3)
	assert.True(t, r.HasPending())

	atomic.StoreInt32(&r.pending, 0)
	assert.False(t, r.HasPending())
}

func TestSubAgentRunner_CancelAll_WaitAll(t *testing.T) {
	r := newMinimalRunner(5)

	cancelled := make(chan struct{})
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	exec1 := &subAgentExecution{
		executionID: "exec-1",
		status:      agent.ExecutionStatusActive,
		cancel: func() {
			close(cancelled)
		},
		done: make(chan struct{}),
	}
	r.mu.Lock()
	r.executions["exec-1"] = exec1
	r.mu.Unlock()

	// Simulate goroutine completing after cancel
	go func() {
		<-cancelled
		close(exec1.done)
	}()

	r.CancelAll()

	select {
	case <-cancelled:
		// cancel was called
	case <-time.After(time.Second):
		t.Fatal("cancel was not called within timeout")
	}

	r.WaitAll(ctx)
	// If we get here, WaitAll returned successfully
}

func TestSubAgentRunner_WaitAll_ContextTimeout(t *testing.T) {
	r := newMinimalRunner(1)
	exec := &subAgentExecution{
		executionID: "stuck",
		status:      agent.ExecutionStatusActive,
		cancel:      func() {},
		done:        make(chan struct{}), // never closes
	}
	r.mu.Lock()
	r.executions["stuck"] = exec
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	r.WaitAll(ctx)
	// Should return after timeout without hanging
}

// ─── Dispatch validation tests (no DB) ──────────────────────────────────────

func TestSubAgentRunner_Dispatch_AgentNotFound(t *testing.T) {
	r := newMinimalRunner(5)

	_, err := r.Dispatch(context.Background(), "NonExistentAgent", "some task")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentNotFound)
}

func TestSubAgentRunner_Dispatch_MaxConcurrentExceeded(t *testing.T) {
	r := newMinimalRunner(1)

	// Pre-populate with an active execution to hit the limit
	r.mu.Lock()
	r.executions["existing"] = &subAgentExecution{
		executionID: "existing",
		status:      agent.ExecutionStatusActive,
	}
	r.mu.Unlock()

	_, err := r.Dispatch(context.Background(), "TestAgent", "some task")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxConcurrentAgents)
}

func TestSubAgentRunner_OverridesMap(t *testing.T) {
	registry := config.BuildSubAgentRegistry(map[string]*config.AgentConfig{
		"AgentA": {Description: "Agent A"},
		"AgentB": {Description: "Agent B"},
	})
	maxIter := 5

	t.Run("nil refs produces empty overrides", func(t *testing.T) {
		runner := NewSubAgentRunner(
			context.Background(), &SubAgentDeps{},
			"parent", "sess", "stg", registry,
			&OrchestratorGuardrails{MaxConcurrentAgents: 5, AgentTimeout: time.Minute, MaxBudget: time.Minute},
			nil,
		)
		assert.Empty(t, runner.overrides)
	})

	t.Run("refs with overrides populates map", func(t *testing.T) {
		refs := config.SubAgentRefs{
			{Name: "AgentA", LLMProvider: "fast", MaxIterations: &maxIter},
			{Name: "AgentB"},
		}
		runner := NewSubAgentRunner(
			context.Background(), &SubAgentDeps{},
			"parent", "sess", "stg", registry,
			&OrchestratorGuardrails{MaxConcurrentAgents: 5, AgentTimeout: time.Minute, MaxBudget: time.Minute},
			refs,
		)
		require.Len(t, runner.overrides, 2)
		assert.Equal(t, "fast", runner.overrides["AgentA"].LLMProvider)
		assert.Equal(t, 5, *runner.overrides["AgentA"].MaxIterations)
		assert.Equal(t, "", runner.overrides["AgentB"].LLMProvider)
		assert.Nil(t, runner.overrides["AgentB"].MaxIterations)
	})

	t.Run("dispatch without override uses zero-value ref", func(t *testing.T) {
		runner := NewSubAgentRunner(
			context.Background(), &SubAgentDeps{},
			"parent", "sess", "stg", registry,
			&OrchestratorGuardrails{MaxConcurrentAgents: 5, AgentTimeout: time.Minute, MaxBudget: time.Minute},
			nil,
		)
		ref := runner.overrides["NonExistent"]
		assert.Equal(t, config.SubAgentRef{}, ref)
	})
}

// ─── Dispatch integration tests (testcontainers DB + mock agent) ────────────

func TestSubAgentRunner_Dispatch_Success(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		return &agent.ExecutionResult{
			Status:        agent.ExecutionStatusCompleted,
			FinalAnalysis: "investigation complete",
		}, nil
	})
	defer cleanup()

	execID, err := runner.Dispatch(ctx, "TestAgent", "analyze logs")
	require.NoError(t, err)
	assert.NotEmpty(t, execID)

	result, err := runner.WaitForNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, execID, result.ExecutionID)
	assert.Equal(t, "TestAgent", result.AgentName)
	assert.Equal(t, "analyze logs", result.Task)
	assert.Equal(t, agent.ExecutionStatusCompleted, result.Status)
	assert.Equal(t, "investigation complete", result.Result)
	assert.Empty(t, result.Error)
	assert.False(t, runner.HasPending())
}

func TestSubAgentRunner_Dispatch_AgentFailure(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		return &agent.ExecutionResult{
			Status: agent.ExecutionStatusFailed,
			Error:  fmt.Errorf("LLM call failed"),
		}, nil
	})
	defer cleanup()

	execID, err := runner.Dispatch(ctx, "TestAgent", "failing task")
	require.NoError(t, err)

	result, err := runner.WaitForNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, execID, result.ExecutionID)
	assert.Equal(t, agent.ExecutionStatusFailed, result.Status)
}

func TestSubAgentRunner_Dispatch_AgentError(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		return nil, fmt.Errorf("infrastructure failure")
	})
	defer cleanup()

	_, err := runner.Dispatch(ctx, "TestAgent", "erroring task")
	require.NoError(t, err)

	result, err := runner.WaitForNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, agent.ExecutionStatusFailed, result.Status)
	assert.Contains(t, result.Error, "infrastructure failure")
}

func TestSubAgentRunner_Dispatch_Timeout(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(runCtx context.Context) (*agent.ExecutionResult, error) {
		<-runCtx.Done() // blocks until timeout
		return nil, runCtx.Err()
	})
	defer cleanup()
	runner.guardrails.AgentTimeout = 200 * time.Millisecond

	_, err := runner.Dispatch(ctx, "TestAgent", "slow task")
	require.NoError(t, err)

	result, err := runner.WaitForNext(ctx)
	require.NoError(t, err)
	// The agent sees DeadlineExceeded and maps to TimedOut or Cancelled
	assert.Contains(t, []agent.ExecutionStatus{
		agent.ExecutionStatusTimedOut,
		agent.ExecutionStatusCancelled,
		agent.ExecutionStatusFailed,
	}, result.Status)
}

func TestSubAgentRunner_Cancel_RunningAgent(t *testing.T) {
	ctx := context.Background()
	started := make(chan struct{})
	runner, cleanup := setupIntegrationRunner(t, func(runCtx context.Context) (*agent.ExecutionResult, error) {
		close(started)
		<-runCtx.Done()
		return nil, runCtx.Err()
	})
	defer cleanup()

	execID, err := runner.Dispatch(ctx, "TestAgent", "cancellable task")
	require.NoError(t, err)

	// Wait for the agent to start
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not start in time")
	}

	status, err := runner.Cancel(execID)
	require.NoError(t, err)
	assert.Equal(t, "cancellation requested", status)

	result, err := runner.WaitForNext(ctx)
	require.NoError(t, err)
	assert.Equal(t, execID, result.ExecutionID)
	assert.Contains(t, []agent.ExecutionStatus{
		agent.ExecutionStatusCancelled,
		agent.ExecutionStatusFailed,
	}, result.Status)
}

func TestSubAgentRunner_Cancel_NotFound(t *testing.T) {
	r := newMinimalRunner(5)
	_, err := r.Cancel("nonexistent")
	assert.ErrorIs(t, err, ErrExecutionNotFound)
}

func TestSubAgentRunner_Cancel_AlreadyCompleted(t *testing.T) {
	r := newMinimalRunner(5)
	r.mu.Lock()
	r.executions["done-exec"] = &subAgentExecution{
		executionID: "done-exec",
		status:      agent.ExecutionStatusCompleted,
		cancel:      func() {},
		done:        make(chan struct{}),
	}
	r.mu.Unlock()

	status, err := r.Cancel("done-exec")
	require.NoError(t, err)
	assert.Contains(t, status, "already completed")
}

func TestSubAgentRunner_List(t *testing.T) {
	r := newMinimalRunner(5)
	r.mu.Lock()
	r.executions["e1"] = &subAgentExecution{
		executionID: "e1", agentName: "AgentA", task: "task A",
		status: agent.ExecutionStatusActive,
	}
	r.executions["e2"] = &subAgentExecution{
		executionID: "e2", agentName: "AgentB", task: "task B",
		status: agent.ExecutionStatusCompleted,
	}
	r.mu.Unlock()

	statuses := r.List()
	assert.Len(t, statuses, 2)

	found := make(map[string]SubAgentStatus)
	for _, s := range statuses {
		found[s.ExecutionID] = s
	}
	assert.Equal(t, agent.ExecutionStatusActive, found["e1"].Status)
	assert.Equal(t, agent.ExecutionStatusCompleted, found["e2"].Status)
}

// ─── Concurrent dispatch + result collection (integration) ──────────────────

func TestSubAgentRunner_Dispatch_ConcurrentMultipleAgents(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		time.Sleep(50 * time.Millisecond) // simulate brief work
		return &agent.ExecutionResult{
			Status:        agent.ExecutionStatusCompleted,
			FinalAnalysis: "done",
		}, nil
	})
	defer cleanup()

	const n = 3
	execIDs := make([]string, n)
	for i := 0; i < n; i++ {
		id, err := runner.Dispatch(ctx, "TestAgent", fmt.Sprintf("task-%d", i))
		require.NoError(t, err)
		execIDs[i] = id
	}

	// Collect all results
	collected := make(map[string]*SubAgentResult, n)
	for i := 0; i < n; i++ {
		result, err := runner.WaitForNext(ctx)
		require.NoError(t, err)
		collected[result.ExecutionID] = result
	}

	assert.Len(t, collected, n)
	for _, id := range execIDs {
		r, ok := collected[id]
		require.True(t, ok, "missing result for %s", id)
		assert.Equal(t, agent.ExecutionStatusCompleted, r.Status)
	}
	assert.False(t, runner.HasPending())
}

// ─── DB record verification (integration) ───────────────────────────────────

func TestSubAgentRunner_Dispatch_SetsParentExecutionAndTask(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		return &agent.ExecutionResult{
			Status:        agent.ExecutionStatusCompleted,
			FinalAnalysis: "verified",
		}, nil
	})
	defer cleanup()

	execID, err := runner.Dispatch(ctx, "TestAgent", "check DB linkage")
	require.NoError(t, err)

	// Wait for the sub-agent to complete
	_, err = runner.WaitForNext(ctx)
	require.NoError(t, err)

	// Verify the DB record has correct parent linkage and task
	dbExec, err := runner.deps.StageService.GetAgentExecutionByID(ctx, execID)
	require.NoError(t, err)

	require.NotNil(t, dbExec.ParentExecutionID, "parent_execution_id should be set")
	assert.Equal(t, runner.parentExecID, *dbExec.ParentExecutionID)

	require.NotNil(t, dbExec.Task, "task should be set")
	assert.Equal(t, "check DB linkage", *dbExec.Task)

	assert.Equal(t, "TestAgent", dbExec.AgentName)
}

// ─── Dispatch with per-reference overrides (integration) ────────────────────

func TestSubAgentRunner_Dispatch_WithOverrides(t *testing.T) {
	ctx := context.Background()
	runner, cleanup := setupIntegrationRunner(t, func(_ context.Context) (*agent.ExecutionResult, error) {
		return &agent.ExecutionResult{
			Status:        agent.ExecutionStatusCompleted,
			FinalAnalysis: "done with override",
		}, nil
	})
	defer cleanup()

	// Add a second LLM provider to the config so the override is valid.
	runner.deps.Config.LLMProviderRegistry = config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
		"test-provider": {Type: config.LLMProviderTypeGoogle, Model: "test-model", MaxToolResultTokens: 10000},
		"fast-provider": {Type: config.LLMProviderTypeGoogle, Model: "fast-model", MaxToolResultTokens: 10000},
	})

	// Set per-reference overrides: TestAgent gets fast-provider.
	runner.overrides = map[string]config.SubAgentRef{
		"TestAgent": {Name: "TestAgent", LLMProvider: "fast-provider"},
	}

	execID, err := runner.Dispatch(ctx, "TestAgent", "overridden task")
	require.NoError(t, err)

	_, err = runner.WaitForNext(ctx)
	require.NoError(t, err)

	// Verify the DB record reflects the overridden provider.
	dbExec, err := runner.deps.StageService.GetAgentExecutionByID(ctx, execID)
	require.NoError(t, err)
	require.NotNil(t, dbExec.LlmProvider, "llm_provider should be set")
	assert.Equal(t, "fast-provider", *dbExec.LlmProvider,
		"execution should use overridden LLM provider, not the default")
}

// ─── CancelAll idempotent ───────────────────────────────────────────────────

func TestSubAgentRunner_CancelAll_Idempotent(t *testing.T) {
	r := newMinimalRunner(5)
	r.mu.Lock()
	r.executions["e1"] = &subAgentExecution{
		executionID: "e1",
		status:      agent.ExecutionStatusActive,
		cancel:      func() {},
		done:        make(chan struct{}),
	}
	r.mu.Unlock()

	// First call closes closeCh and cancels
	r.CancelAll()
	// Second call must not panic (closeCh already closed)
	r.CancelAll()
}

// ─── FormatSubAgentResult ───────────────────────────────────────────────────

func TestFormatSubAgentResult_Completed(t *testing.T) {
	msg := FormatSubAgentResult(&SubAgentResult{
		ExecutionID: "exec-1",
		AgentName:   "LogAnalyzer",
		Status:      agent.ExecutionStatusCompleted,
		Result:      "Found 42 errors",
	})
	assert.Equal(t, agent.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "[Sub-agent completed]")
	assert.Contains(t, msg.Content, "LogAnalyzer")
	assert.Contains(t, msg.Content, "Found 42 errors")
}

func TestFormatSubAgentResult_Failed(t *testing.T) {
	msg := FormatSubAgentResult(&SubAgentResult{
		ExecutionID: "exec-2",
		AgentName:   "Checker",
		Status:      agent.ExecutionStatusFailed,
		Error:       "connection refused",
	})
	assert.Equal(t, agent.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "[Sub-agent failed]")
	assert.Contains(t, msg.Content, "connection refused")
}

// ─── Test helpers ───────────────────────────────────────────────────────────

// newMinimalRunner creates a SubAgentRunner with a test registry (containing
// "TestAgent") and no DB deps. Suitable for channel mechanics and validation
// tests that don't call Dispatch.
func newMinimalRunner(maxConcurrent int) *SubAgentRunner {
	registry := config.BuildSubAgentRegistry(map[string]*config.AgentConfig{
		"TestAgent": {Description: "A test agent"},
	})
	return NewSubAgentRunner(
		context.Background(),
		&SubAgentDeps{},
		"parent-exec", "session-1", "stage-1",
		registry,
		&OrchestratorGuardrails{
			MaxConcurrentAgents: maxConcurrent,
			AgentTimeout:        5 * time.Minute,
			MaxBudget:           10 * time.Minute,
		},
		nil,
	)
}

// mockControllerFactory returns a factory that produces controllers
// calling resultFn when Run is invoked. resultFn receives ctx so tests
// can respect context cancellation/timeout.
type mockControllerFactory struct {
	resultFn func(ctx context.Context) (*agent.ExecutionResult, error)
}

func (f *mockControllerFactory) CreateController(_ config.AgentType, _ *agent.ExecutionContext) (agent.Controller, error) {
	return &mockController{resultFn: f.resultFn}, nil
}

type mockController struct {
	resultFn func(ctx context.Context) (*agent.ExecutionResult, error)
}

func (c *mockController) Run(ctx context.Context, _ *agent.ExecutionContext, _ string) (*agent.ExecutionResult, error) {
	return c.resultFn(ctx)
}

// Compile-time check that noopEventPublisher satisfies agent.EventPublisher.
var _ agent.EventPublisher = noopEventPublisher{}

// noopEventPublisher satisfies agent.EventPublisher with no-ops.
type noopEventPublisher struct{}

func (noopEventPublisher) PublishTimelineCreated(_ context.Context, _ string, _ events.TimelineCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishTimelineCompleted(_ context.Context, _ string, _ events.TimelineCompletedPayload) error {
	return nil
}
func (noopEventPublisher) PublishStreamChunk(_ context.Context, _ string, _ events.StreamChunkPayload) error {
	return nil
}
func (noopEventPublisher) PublishSessionStatus(_ context.Context, _ string, _ events.SessionStatusPayload) error {
	return nil
}
func (noopEventPublisher) PublishStageStatus(_ context.Context, _ string, _ events.StageStatusPayload) error {
	return nil
}
func (noopEventPublisher) PublishChatCreated(_ context.Context, _ string, _ events.ChatCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishInteractionCreated(_ context.Context, _ string, _ events.InteractionCreatedPayload) error {
	return nil
}
func (noopEventPublisher) PublishSessionProgress(_ context.Context, _ events.SessionProgressPayload) error {
	return nil
}
func (noopEventPublisher) PublishExecutionProgress(_ context.Context, _ string, _ events.ExecutionProgressPayload) error {
	return nil
}
func (noopEventPublisher) PublishExecutionStatus(_ context.Context, _ string, _ events.ExecutionStatusPayload) error {
	return nil
}

// setupIntegrationRunner creates a fully wired SubAgentRunner backed by
// testcontainers PostgreSQL. resultFn controls what the mock agent returns.
func setupIntegrationRunner(
	t *testing.T,
	resultFn func(ctx context.Context) (*agent.ExecutionResult, error),
) (*SubAgentRunner, func()) {
	t.Helper()

	dbClient := testdb.NewTestClient(t)
	ctx := context.Background()

	stageService := services.NewStageService(dbClient.Client)
	timelineService := services.NewTimelineService(dbClient.Client)
	messageService := services.NewMessageService(dbClient.Client)
	interactionService := services.NewInteractionService(dbClient.Client, messageService)
	mcpRegistry := config.NewMCPServerRegistry(nil)
	sessionService := services.NewSessionService(
		dbClient.Client,
		config.NewChainRegistry(map[string]*config.ChainConfig{
			"test-chain": {
				AlertTypes: []string{"test"},
				Stages: []config.StageConfig{{
					Name:   "stage1",
					Agents: []config.StageAgentConfig{{Name: "TestAgent"}},
				}},
			},
		}),
		mcpRegistry,
	)

	session, err := sessionService.CreateSession(ctx, models.CreateSessionRequest{
		SessionID: uuid.New().String(),
		AlertData: "test alert",
		AgentType: "test",
		ChainID:   "test-chain",
	})
	require.NoError(t, err)

	// CreateSession creates an initial stage + execution. Query them with edges.
	stages, err := stageService.GetStagesBySession(ctx, session.ID, true)
	require.NoError(t, err)
	require.NotEmpty(t, stages)
	stageID := stages[0].ID

	// Use the pre-created execution as the parent orchestrator execution.
	executions, err := stageService.GetAgentExecutions(ctx, stageID)
	require.NoError(t, err)
	require.NotEmpty(t, executions)
	parentExecID := executions[0].ID

	testProvider := &config.LLMProviderConfig{
		Type:                config.LLMProviderTypeGoogle,
		Model:               "test-model",
		MaxToolResultTokens: 10000,
	}

	cfg := &config.Config{
		Defaults: &config.Defaults{LLMProvider: "test-provider"},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent": {Description: "A test agent"},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": testProvider,
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(nil),
	}

	chain := &config.ChainConfig{
		AlertTypes: []string{"test"},
		Stages: []config.StageConfig{{
			Name:   "stage1",
			Agents: []config.StageAgentConfig{{Name: "TestAgent"}},
		}},
	}

	agentFactory := agent.NewAgentFactory(&mockControllerFactory{resultFn: resultFn})

	registry := config.BuildSubAgentRegistry(cfg.AgentRegistry.GetAll())

	deps := &SubAgentDeps{
		Config:             cfg,
		Chain:              chain,
		AgentFactory:       agentFactory,
		MCPFactory:         nil, // sub-agents get stub executor
		LLMClient:          nil, // controller is mocked, LLM not called
		EventPublisher:     nil,
		PromptBuilder:      nil,
		StageService:       stageService,
		TimelineService:    timelineService,
		MessageService:     messageService,
		InteractionService: interactionService,
		AlertData:          "test alert",
		AlertType:          "test",
	}

	runner := NewSubAgentRunner(
		context.Background(),
		deps,
		parentExecID,
		session.ID,
		stageID,
		registry,
		&OrchestratorGuardrails{
			MaxConcurrentAgents: 5,
			AgentTimeout:        30 * time.Second,
			MaxBudget:           60 * time.Second,
		},
		nil,
	)

	return runner, func() {}
}
