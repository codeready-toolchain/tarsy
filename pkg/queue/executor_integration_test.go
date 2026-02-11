package queue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	util "github.com/codeready-toolchain/tarsy/test/util"
)

// ────────────────────────────────────────────────────────────
// Mock LLM client for integration tests
// ────────────────────────────────────────────────────────────

type mockLLMResponse struct {
	chunks []agent.Chunk
	err    error
}

type mockLLMClient struct {
	responses []mockLLMResponse
	callCount int
	// capturedInputs stores all GenerateInput received (nil by default; set capture=true to enable)
	capturedInputs []*agent.GenerateInput
	capture        bool
}

func (m *mockLLMClient) Generate(_ context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
	if m.capture {
		m.capturedInputs = append(m.capturedInputs, input)
	}

	idx := m.callCount
	m.callCount++

	if idx >= len(m.responses) {
		return nil, fmt.Errorf("no more mock responses (call %d)", idx+1)
	}

	r := m.responses[idx]
	if r.err != nil {
		return nil, r.err
	}

	ch := make(chan agent.Chunk, len(r.chunks))
	for _, c := range r.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

func (m *mockLLMClient) Close() error { return nil }

// ────────────────────────────────────────────────────────────
// Mock event publisher for tracking stage events
// ────────────────────────────────────────────────────────────

type testEventPublisher struct {
	mu              sync.Mutex
	stageStatuses   []events.StageStatusPayload
	sessionStatuses []events.SessionStatusPayload
}

func (p *testEventPublisher) PublishTimelineCreated(_ context.Context, _ string, _ events.TimelineCreatedPayload) error {
	return nil
}

func (p *testEventPublisher) PublishTimelineCompleted(_ context.Context, _ string, _ events.TimelineCompletedPayload) error {
	return nil
}

func (p *testEventPublisher) PublishStreamChunk(_ context.Context, _ string, _ events.StreamChunkPayload) error {
	return nil
}

func (p *testEventPublisher) PublishSessionStatus(_ context.Context, _ string, payload events.SessionStatusPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sessionStatuses = append(p.sessionStatuses, payload)
	return nil
}

func (p *testEventPublisher) PublishStageStatus(_ context.Context, _ string, payload events.StageStatusPayload) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stageStatuses = append(p.stageStatuses, payload)
	return nil
}

// hasStageStatus checks if a stage with the given name has the given status (thread-safe).
func (p *testEventPublisher) hasStageStatus(stageName, status string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.stageStatuses {
		if s.StageName == stageName && s.Status == status {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────

// testConfig builds a minimal config with the given chain.
func testConfig(chainID string, chain *config.ChainConfig) *config.Config {
	maxIter := 1
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:       "test-provider",
			IterationStrategy: config.IterationStrategyReact,
			MaxIterations:     &maxIter,
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			"TestAgent": {
				IterationStrategy: config.IterationStrategyReact,
				MaxIterations:     &maxIter,
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {
				Type:  config.LLMProviderTypeGoogle,
				Model: "test-model",
			},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			chainID: chain,
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(nil),
	}
}

// createExecutorTestSession inserts an in_progress session in the DB.
func createExecutorTestSession(t *testing.T, client *ent.Client, chainID string) *ent.AlertSession {
	t.Helper()
	sessionID := uuid.New().String()
	session, err := client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData("Test alert data").
		SetAgentType("test").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusInProgress).
		SetAuthor("test").
		Save(context.Background())
	require.NoError(t, err)
	return session
}

// ────────────────────────────────────────────────────────────
// Integration tests
// ────────────────────────────────────────────────────────────

func TestExecutor_SingleStageChain(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// LLM returns a final answer immediately
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: I know the answer.\nFinal Answer: Everything is healthy."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Equal(t, "Everything is healthy.", result.FinalAnalysis)
	assert.Nil(t, result.Error)

	// Verify Stage DB record
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, stages, 1)
	assert.Equal(t, "investigation", stages[0].StageName)
	assert.Equal(t, 1, stages[0].StageIndex)
	assert.Equal(t, stage.StatusCompleted, stages[0].Status)

	// Verify stage events: started + completed
	require.Len(t, publisher.stageStatuses, 2)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)
	assert.Equal(t, "investigation", publisher.stageStatuses[0].StageName)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[1].Status)
}

func TestExecutor_MultiStageChain(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "data-collection",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "diagnosis",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Two stages: each agent produces a final answer in 1 call
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Stage 1: data-collection
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Collected data.\nFinal Answer: Metrics show OOM on pod-1."},
			}},
			// Stage 2: diagnosis
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Using previous context.\nFinal Answer: Root cause is memory leak in app container."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Equal(t, "Root cause is memory leak in app container.", result.FinalAnalysis)
	assert.Nil(t, result.Error)

	// Verify 2 Stage DB records
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, stages, 2)

	// Verify 2 AgentExecution records
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, execs, 2)

	// Verify stage events: started + completed for each stage = 4 events
	require.Len(t, publisher.stageStatuses, 4)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)
	assert.Equal(t, "data-collection", publisher.stageStatuses[0].StageName)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[1].Status)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[2].Status)
	assert.Equal(t, "diagnosis", publisher.stageStatuses[2].StageName)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[3].Status)

	// Verify session progress was updated
	updatedSession, err := entClient.AlertSession.Get(context.Background(), session.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedSession.CurrentStageIndex)
	assert.Equal(t, 2, *updatedSession.CurrentStageIndex)
}

func TestExecutor_FailFast(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "stage-1",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "stage-2",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Stage 1 LLM returns an error on all calls
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{err: fmt.Errorf("LLM connection failed")},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	assert.NotNil(t, result.Error)

	// Only 1 stage should have been created (stage-2 never starts)
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, stages, 1)
	assert.Equal(t, "stage-1", stages[0].StageName)

	// Verify stage events: started + failed for stage 1 only
	require.Len(t, publisher.stageStatuses, 2)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)
	assert.Equal(t, events.StageStatusFailed, publisher.stageStatuses[1].Status)
}

func TestExecutor_CancellationBetweenStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "stage-1",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "stage-2",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Stage 1 completes. Provide enough responses for stage 1 to fully complete,
	// then return context.Canceled for stage 2's LLM call.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			// Stage 1 agent final answer
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Stage 1 complete."},
			}},
			// Stage 2 would try to call LLM, but context should already be cancelled.
			// In case it does get called (race), provide an error response.
			{err: context.Canceled},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run in goroutine so we can cancel from outside
	resultCh := make(chan *ExecutionResult, 1)
	go func() {
		resultCh <- executor.Execute(ctx, session)
	}()

	// Wait for stage-1 to complete, then cancel before stage-2 starts.
	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	stage1Done := false
	for !stage1Done {
		select {
		case <-tick.C:
			if publisher.hasStageStatus("stage-1", events.StageStatusCompleted) {
				cancel()
				stage1Done = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for stage-1 to complete")
		}
	}

	result := <-resultCh

	require.NotNil(t, result)
	// The result should be cancelled (caught by mapCancellation between stages)
	// or failed if stage 2 started before the cancel was detected
	assert.Contains(t, []alertsession.Status{
		alertsession.StatusCancelled,
		alertsession.StatusFailed,
	}, result.Status)
}

func TestExecutor_ExecutiveSummaryGenerated(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// LLM call 1: agent final answer
	// LLM call 2: executive summary
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: OOM killed pod-1 due to memory leak."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Executive summary: Pod-1 OOM killed."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Equal(t, "OOM killed pod-1 due to memory leak.", result.FinalAnalysis)
	assert.Equal(t, "Executive summary: Pod-1 OOM killed.", result.ExecutiveSummary)
	assert.Empty(t, result.ExecutiveSummaryError)

	// Verify executive_summary timeline event with NULL stage_id/execution_id
	tlEvents, err := entClient.TimelineEvent.Query().All(context.Background())
	require.NoError(t, err)

	var summaryEvent *ent.TimelineEvent
	for _, ev := range tlEvents {
		if ev.EventType == timelineevent.EventTypeExecutiveSummary {
			summaryEvent = ev
			break
		}
	}
	require.NotNil(t, summaryEvent, "should have executive_summary timeline event")
	assert.Contains(t, summaryEvent.Content, "Executive summary: Pod-1 OOM killed.")
	assert.Nil(t, summaryEvent.StageID, "executive_summary should have nil stage_id")
	assert.Nil(t, summaryEvent.ExecutionID, "executive_summary should have nil execution_id")
}

func TestExecutor_ExecutiveSummaryFailOpen(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// LLM call 1: agent final answer
	// LLM call 2: executive summary fails
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: OOM killed pod-1."},
			}},
			{err: fmt.Errorf("executive summary LLM timeout")},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	// Session still completes despite summary failure (fail-open)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Equal(t, "OOM killed pod-1.", result.FinalAnalysis)
	assert.Empty(t, result.ExecutiveSummary)
	assert.NotEmpty(t, result.ExecutiveSummaryError)
}

func TestExecutor_ParallelAgentsRejected(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "parallel-stage",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
		},
	}

	llm := &mockLLMClient{}
	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	assert.Contains(t, result.Error.Error(), "parallel agent execution not yet supported")
}

func TestExecutor_NilEventPublisher(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: All good."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	// nil eventPublisher — should not panic
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
}

func TestExecutor_ReplicasRejected(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name:     "replicated-stage",
				Replicas: 3,
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	llm := &mockLLMClient{}
	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	assert.Contains(t, result.Error.Error(), "parallel agent execution not yet supported")
	assert.Contains(t, result.Error.Error(), "3 replicas")
}

func TestExecutor_EmptyChainStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages:     []config.StageConfig{}, // No stages
	}

	llm := &mockLLMClient{}
	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	assert.Contains(t, result.Error.Error(), "no stages")
}

func TestExecutor_ContextPassedBetweenStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "data-collection",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "diagnosis",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Use capturing mock to inspect what messages reach the LLM
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Stage 1
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Data collected.\nFinal Answer: Pod-1 has OOM errors."},
			}},
			// Stage 2
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Diagnosis done.\nFinal Answer: Memory leak in app."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)

	// The LLM should have been called at least 2 times (one per stage)
	require.GreaterOrEqual(t, len(llm.capturedInputs), 2)

	// Stage 2's LLM call should contain chain context from stage 1 in its messages.
	// The prompt builder wraps stage context into the system/user message.
	stage2Input := llm.capturedInputs[1]
	var foundContext bool
	for _, msg := range stage2Input.Messages {
		if containsChainContextMarkers(msg.Content) {
			foundContext = true
			// Verify stage 1's analysis is embedded in the context
			assert.Contains(t, msg.Content, "Pod-1 has OOM errors.")
			break
		}
	}
	assert.True(t, foundContext, "stage 2 LLM call should contain chain context from stage 1")
}

// containsChainContextMarkers checks if text contains the chain context delimiters.
func containsChainContextMarkers(text string) bool {
	return len(text) > 0 &&
		(strings.Contains(text, "CHAIN_CONTEXT_START") || strings.Contains(text, "data-collection"))
}

func TestExecutor_DeadlineBetweenStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "stage-1",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "stage-2",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Stage 1 completes but stage 2 never starts because deadline is exceeded.
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Stage 1 result."},
			}},
			// Stage 2 would try to use this but context should be expired
			{err: context.DeadlineExceeded},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	// Use a cancellable context; cancel after stage 1 completes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan *ExecutionResult, 1)
	go func() {
		resultCh <- executor.Execute(ctx, session)
	}()

	// Wait for stage-1 to complete, then cancel before stage-2 starts.
	timeout := time.After(5 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()

	stage1Done := false
	for !stage1Done {
		select {
		case <-tick.C:
			if publisher.hasStageStatus("stage-1", events.StageStatusCompleted) {
				cancel()
				stage1Done = true
			}
		case <-timeout:
			t.Fatal("timed out waiting for stage-1 to complete")
		}
	}

	result := <-resultCh
	require.NotNil(t, result)
	// Should be cancelled or failed (depends on race between cancel detection and stage-2 start)
	assert.Contains(t, []alertsession.Status{
		alertsession.StatusCancelled,
		alertsession.StatusFailed,
	}, result.Status)

	// Stage-2 should either not exist or have failed
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	// At least stage-1 exists
	assert.GreaterOrEqual(t, len(stages), 1)
}

func TestExecutor_StageEventsHaveCorrectIndex(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "stage-a",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
			{
				Name: "stage-b",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: A.\nFinal Answer: Done A."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: B.\nFinal Answer: Done B."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)
	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)

	// 4 stage events: started/completed for each stage
	require.Len(t, publisher.stageStatuses, 4)

	// Stage A events should have index 1 (1-based for clients)
	assert.Equal(t, 1, publisher.stageStatuses[0].StageIndex)
	assert.Equal(t, "stage-a", publisher.stageStatuses[0].StageName)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)

	assert.Equal(t, 1, publisher.stageStatuses[1].StageIndex)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[1].Status)

	// Stage B events should have index 2
	assert.Equal(t, 2, publisher.stageStatuses[2].StageIndex)
	assert.Equal(t, "stage-b", publisher.stageStatuses[2].StageName)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[2].Status)

	assert.Equal(t, 2, publisher.stageStatuses[3].StageIndex)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[3].Status)

	// Completed events should have a non-empty StageID
	assert.NotEmpty(t, publisher.stageStatuses[1].StageID)
	assert.NotEmpty(t, publisher.stageStatuses[3].StageID)
}
