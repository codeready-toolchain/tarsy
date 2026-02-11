package queue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
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
	mu        sync.Mutex
	responses []mockLLMResponse
	callCount int
	// capturedInputs stores all GenerateInput received (nil by default; set capture=true to enable)
	capturedInputs []*agent.GenerateInput
	capture        bool
}

func (m *mockLLMClient) Generate(_ context.Context, input *agent.GenerateInput) (<-chan agent.Chunk, error) {
	m.mu.Lock()
	if m.capture {
		m.capturedInputs = append(m.capturedInputs, input)
	}

	idx := m.callCount
	m.callCount++
	m.mu.Unlock()

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
			"SynthesisAgent": {
				IterationStrategy: config.IterationStrategySynthesis,
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
	// Table-driven: both variants cancel between stages; they differ only in
	// the fallback mock error returned if stage-2's LLM call races past the
	// cancel check.
	tests := []struct {
		name          string
		stage2MockErr error
	}{
		{
			name:          "context canceled",
			stage2MockErr: context.Canceled,
		},
		{
			name:          "deadline exceeded",
			stage2MockErr: context.DeadlineExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
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

			llm := &mockLLMClient{
				responses: []mockLLMResponse{
					// Stage 1 agent final answer
					{chunks: []agent.Chunk{
						&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Stage 1 complete."},
					}},
					// Stage 2 fallback if the cancel isn't detected before the LLM call
					{err: tc.stage2MockErr},
				},
			}

			cfg := testConfig("test-chain", chain)
			publisher := &testEventPublisher{}
			executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
			session := createExecutorTestSession(t, entClient, "test-chain")

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
			// The result should be cancelled or failed depending on the race
			// between cancel detection and stage-2 start.
			assert.Contains(t, []alertsession.Status{
				alertsession.StatusCancelled,
				alertsession.StatusFailed,
			}, result.Status)

			// Stage-2 should either not exist or have failed
			stages, err := entClient.Stage.Query().All(context.Background())
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(stages), 1)
		})
	}
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

func TestExecutor_MultiAgentAllSucceed(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "parallel-investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Two agents, each gets one LLM call (max_iterations=1, final answer on first call)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Agent 1 done.\nFinal Answer: Agent 1 found OOM."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Agent 2 done.\nFinal Answer: Agent 2 found memory leak."},
			}},
			// Synthesis agent
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized: Both agents agree on memory issue."},
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
	// Final analysis comes from synthesis
	assert.Contains(t, result.FinalAnalysis, "Synthesized")

	// Verify DB: 2 stages (investigation + synthesis)
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, stages, 2)

	// Investigation stage
	assert.Equal(t, "parallel-investigation", stages[0].StageName)
	assert.Equal(t, 2, stages[0].ExpectedAgentCount)
	assert.NotNil(t, stages[0].ParallelType)
	assert.Equal(t, stage.ParallelTypeMultiAgent, *stages[0].ParallelType)
	assert.Equal(t, stage.StatusCompleted, stages[0].Status)

	// Synthesis stage
	assert.Equal(t, "parallel-investigation - Synthesis", stages[1].StageName)
	assert.Equal(t, 1, stages[1].ExpectedAgentCount)
	assert.Nil(t, stages[1].ParallelType)
	assert.Equal(t, stage.StatusCompleted, stages[1].Status)

	// Verify 3 AgentExecution records (2 investigation + 1 synthesis)
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, execs, 3)

	// Verify stage events: started+completed for investigation, started+completed for synthesis = 4
	require.Len(t, publisher.stageStatuses, 4)
	assert.Equal(t, "parallel-investigation", publisher.stageStatuses[0].StageName)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[0].Status)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[1].Status)
	assert.Equal(t, "parallel-investigation - Synthesis", publisher.stageStatuses[2].StageName)
	assert.Equal(t, events.StageStatusStarted, publisher.stageStatuses[2].Status)
	assert.Equal(t, events.StageStatusCompleted, publisher.stageStatuses[3].Status)
}

func TestExecutor_MultiAgentOneFailsPolicyAll(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name:          "investigation",
				SuccessPolicy: config.SuccessPolicyAll,
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Agent 1 succeeds, Agent 2 fails (no mock response → error)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Agent 1 OK."},
			}},
			{err: fmt.Errorf("LLM timeout")},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	// policy=all: one failure means stage fails
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	require.NotNil(t, result.Error)
	assert.Contains(t, result.Error.Error(), "multi-agent stage failed")

	// Both agents should have execution records (all agents run to completion)
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, execs, 2)
}

func TestExecutor_MultiAgentOneFailsPolicyAny(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name:          "investigation",
				SuccessPolicy: config.SuccessPolicyAny,
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Agent 1 succeeds, Agent 2 fails
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Agent 1 found the issue."},
			}},
			{err: fmt.Errorf("LLM timeout")},
			// Synthesis agent (runs because stage succeeded with >1 agent)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized from 1 successful agent."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	// policy=any: one success means stage succeeds
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Nil(t, result.Error)

	// Both agents ran + synthesis = 3 executions
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, execs, 3)
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

func TestExecutor_ReplicaAllSucceed(t *testing.T) {
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

	// 3 replicas + 1 synthesis = 4 LLM calls
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: R1.\nFinal Answer: Replica 1 result."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: R2.\nFinal Answer: Replica 2 result."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: R3.\nFinal Answer: Replica 3 result."},
			}},
			// Synthesis
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized from 3 replicas."},
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
	assert.Contains(t, result.FinalAnalysis, "Synthesized from 3 replicas")

	// Verify DB: investigation stage + synthesis stage
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, stages, 2)

	// Investigation stage: replicas=3 → parallel_type=replica
	assert.Equal(t, "replicated-stage", stages[0].StageName)
	assert.Equal(t, 3, stages[0].ExpectedAgentCount)
	assert.NotNil(t, stages[0].ParallelType)
	assert.Equal(t, stage.ParallelTypeReplica, *stages[0].ParallelType)

	// Verify replica naming: TestAgent-1, TestAgent-2, TestAgent-3
	execs, err := entClient.AgentExecution.Query().
		Where(agentexecution.StageIDEQ(stages[0].ID)).
		All(context.Background())
	require.NoError(t, err)
	require.Len(t, execs, 3)

	// Collect names (order may vary due to goroutine scheduling)
	names := make(map[string]bool)
	for _, e := range execs {
		names[e.AgentName] = true
	}
	assert.True(t, names["TestAgent-1"], "should have TestAgent-1")
	assert.True(t, names["TestAgent-2"], "should have TestAgent-2")
	assert.True(t, names["TestAgent-3"], "should have TestAgent-3")
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
	require.NotNil(t, result.Error)
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
		if containsChainContextMarkers(msg.Content, "data-collection") {
			foundContext = true
			// Verify stage 1's analysis is embedded in the context
			assert.Contains(t, msg.Content, "Pod-1 has OOM errors.")
			break
		}
	}
	assert.True(t, foundContext, "stage 2 LLM call should contain chain context from stage 1")
}

// containsChainContextMarkers checks if text contains the formal chain context
// delimiter or any of the given stage names (indicating chain context is present).
func containsChainContextMarkers(text string, stageNames ...string) bool {
	if strings.Contains(text, "CHAIN_CONTEXT_START") {
		return true
	}
	for _, name := range stageNames {
		if strings.Contains(text, name) {
			return true
		}
	}
	return false
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

	// All events should have a non-empty StageID (started events now published inside executeStage)
	for i, s := range publisher.stageStatuses {
		assert.NotEmpty(t, s.StageID, "event %d (%s) should have stageID", i, s.Status)
	}
}

// ────────────────────────────────────────────────────────────
// Phase 5.2: Parallel execution tests
// ────────────────────────────────────────────────────────────

func TestExecutor_SynthesisSkippedForSingleAgent(t *testing.T) {
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
				&agent.TextChunk{Content: "Thought: Done.\nFinal Answer: Single agent analysis."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Equal(t, "Single agent analysis.", result.FinalAnalysis)

	// Only 1 stage — no synthesis
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, stages, 1)
	assert.Equal(t, "investigation", stages[0].StageName)

	// Only 1 agent execution
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, execs, 1)
}

func TestExecutor_SynthesisFailure(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
		},
	}

	// 2 agents succeed, synthesis fails
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: A.\nFinal Answer: Result A."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: B.\nFinal Answer: Result B."},
			}},
			// Synthesis LLM call fails
			{err: fmt.Errorf("synthesis LLM timeout")},
		},
	}

	cfg := testConfig("test-chain", chain)
	publisher := &testEventPublisher{}
	executor := NewRealSessionExecutor(cfg, entClient, llm, publisher, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	// Synthesis failure causes chain to fail
	assert.Equal(t, alertsession.StatusFailed, result.Status)
	assert.NotNil(t, result.Error)

	// 2 stages: investigation (completed) + synthesis (failed)
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, stages, 2)
	assert.Equal(t, stage.StatusCompleted, stages[0].Status)

	// Verify both investigation stage events AND synthesis stage events were published
	assert.True(t, publisher.hasStageStatus("investigation", events.StageStatusStarted))
	assert.True(t, publisher.hasStageStatus("investigation", events.StageStatusCompleted))
	assert.True(t, publisher.hasStageStatus("investigation - Synthesis", events.StageStatusStarted))
	assert.True(t, publisher.hasStageStatus("investigation - Synthesis", events.StageStatusFailed))
}

func TestExecutor_SynthesisWithDefaults(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	// No synthesis: config block — defaults should apply (SynthesisAgent, synthesis strategy)
	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
				// No Synthesis field
			},
		},
	}

	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: A.\nFinal Answer: Result A."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: B.\nFinal Answer: Result B."},
			}},
			// Synthesis (SynthesisAgent uses synthesis strategy — single call, no tools)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Default synthesis result."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Contains(t, result.FinalAnalysis, "Default synthesis result")

	// Verify synthesis agent execution used default name
	execs, err := entClient.AgentExecution.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, execs, 3)

	// Find synthesis execution
	var synthExec *ent.AgentExecution
	for _, e := range execs {
		if e.AgentName == "SynthesisAgent" {
			synthExec = e
			break
		}
	}
	require.NotNil(t, synthExec, "should have SynthesisAgent execution")
	assert.Equal(t, "synthesis", synthExec.IterationStrategy)
}

func TestExecutor_MultiAgentThenSingleAgent(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name: "parallel-investigation",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
					{Name: "TestAgent"},
				},
			},
			{
				Name: "final-diagnosis",
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			// Agent 1
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: A.\nFinal Answer: Finding A."},
			}},
			// Agent 2
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: B.\nFinal Answer: Finding B."},
			}},
			// Synthesis
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized findings from parallel investigation."},
			}},
			// Final single-agent stage
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Based on synthesis.\nFinal Answer: Final diagnosis."},
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
	assert.Equal(t, "Final diagnosis.", result.FinalAnalysis)

	// 3 stages: investigation, synthesis, final-diagnosis
	stages, err := entClient.Stage.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, stages, 3)
	assert.Equal(t, "parallel-investigation", stages[0].StageName)
	assert.Equal(t, "parallel-investigation - Synthesis", stages[1].StageName)
	assert.Equal(t, "final-diagnosis", stages[2].StageName)

	// The final-diagnosis stage's LLM input should contain synthesis context.
	// Search all captured inputs (order may vary due to concurrency + executive summary).
	require.GreaterOrEqual(t, len(llm.capturedInputs), 4)
	var foundSynthesisContext bool
	for _, input := range llm.capturedInputs {
		for _, msg := range input.Messages {
			if strings.Contains(msg.Content, "Synthesized findings") || strings.Contains(msg.Content, "CHAIN_CONTEXT_START") {
				foundSynthesisContext = true
				break
			}
		}
		if foundSynthesisContext {
			break
		}
	}
	assert.True(t, foundSynthesisContext, "final stage should receive synthesis context")

	// Verify dbStageIndex tracking: investigation=1, synthesis=2, final-diagnosis=3
	assert.Equal(t, 1, stages[0].StageIndex)
	assert.Equal(t, 2, stages[1].StageIndex)
	assert.Equal(t, 3, stages[2].StageIndex)

	// 6 stage events: started+completed for each of 3 stages
	require.Len(t, publisher.stageStatuses, 6)
}

func TestExecutor_StatusAggregationEdgeCases(t *testing.T) {
	// Unit test for aggregateStatus — no DB needed
	tests := []struct {
		name     string
		results  []agentResult
		policy   config.SuccessPolicy
		expected alertsession.Status
	}{
		{
			name: "all completed, policy=all",
			results: []agentResult{
				{status: agent.ExecutionStatusCompleted},
				{status: agent.ExecutionStatusCompleted},
			},
			policy:   config.SuccessPolicyAll,
			expected: alertsession.StatusCompleted,
		},
		{
			name: "all completed, policy=any",
			results: []agentResult{
				{status: agent.ExecutionStatusCompleted},
				{status: agent.ExecutionStatusCompleted},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusCompleted,
		},
		{
			name: "one failed, policy=all → failed",
			results: []agentResult{
				{status: agent.ExecutionStatusCompleted},
				{status: agent.ExecutionStatusFailed},
			},
			policy:   config.SuccessPolicyAll,
			expected: alertsession.StatusFailed,
		},
		{
			name: "one failed, policy=any → completed",
			results: []agentResult{
				{status: agent.ExecutionStatusCompleted},
				{status: agent.ExecutionStatusFailed},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusCompleted,
		},
		{
			name: "all timed out → timed_out",
			results: []agentResult{
				{status: agent.ExecutionStatusTimedOut},
				{status: agent.ExecutionStatusTimedOut},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusTimedOut,
		},
		{
			name: "all cancelled → cancelled",
			results: []agentResult{
				{status: agent.ExecutionStatusCancelled},
				{status: agent.ExecutionStatusCancelled},
			},
			policy:   config.SuccessPolicyAll,
			expected: alertsession.StatusCancelled,
		},
		{
			name: "mixed failures → failed",
			results: []agentResult{
				{status: agent.ExecutionStatusTimedOut},
				{status: agent.ExecutionStatusFailed},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusFailed,
		},
		{
			name: "single agent completed",
			results: []agentResult{
				{status: agent.ExecutionStatusCompleted},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusCompleted,
		},
		{
			name: "single agent failed",
			results: []agentResult{
				{status: agent.ExecutionStatusFailed},
			},
			policy:   config.SuccessPolicyAny,
			expected: alertsession.StatusFailed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := aggregateStatus(tc.results, tc.policy)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestExecutor_SuccessPolicyDefaulting(t *testing.T) {
	// Unit test for resolvedSuccessPolicy
	maxIter := 1

	tests := []struct {
		name           string
		stagePolicy    config.SuccessPolicy
		defaultPolicy  config.SuccessPolicy
		expectedPolicy config.SuccessPolicy
	}{
		{
			name:           "stage policy set",
			stagePolicy:    config.SuccessPolicyAll,
			defaultPolicy:  config.SuccessPolicyAny,
			expectedPolicy: config.SuccessPolicyAll,
		},
		{
			name:           "fall through to default",
			stagePolicy:    "",
			defaultPolicy:  config.SuccessPolicyAll,
			expectedPolicy: config.SuccessPolicyAll,
		},
		{
			name:           "fall through to fallback",
			stagePolicy:    "",
			defaultPolicy:  "",
			expectedPolicy: config.SuccessPolicyAny,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			executor := &RealSessionExecutor{
				cfg: &config.Config{
					Defaults: &config.Defaults{
						SuccessPolicy:    tc.defaultPolicy,
						MaxIterations:    &maxIter,
						LLMProvider:      "test",
						IterationStrategy: config.IterationStrategyReact,
					},
				},
			}
			input := executeStageInput{
				stageConfig: config.StageConfig{
					SuccessPolicy: tc.stagePolicy,
				},
			}
			result := executor.resolvedSuccessPolicy(input)
			assert.Equal(t, tc.expectedPolicy, result)
		})
	}
}

func TestExecutor_ReplicaMixedResultsPolicyAny(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chain := &config.ChainConfig{
		AlertTypes: []string{"test-alert"},
		Stages: []config.StageConfig{
			{
				Name:          "replicated-stage",
				Replicas:      2,
				SuccessPolicy: config.SuccessPolicyAny,
				Agents: []config.StageAgentConfig{
					{Name: "TestAgent"},
				},
			},
		},
	}

	// Replica 1 succeeds, Replica 2 fails
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: R1.\nFinal Answer: Replica 1 OK."},
			}},
			{err: fmt.Errorf("Replica 2 LLM error")},
			// Synthesis (stage completed because policy=any)
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized from 1 successful replica."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)

	require.NotNil(t, result)
	// policy=any → stage succeeds if at least one replica completed
	assert.Equal(t, alertsession.StatusCompleted, result.Status)
	assert.Contains(t, result.FinalAnalysis, "Synthesized from 1 successful replica")
}

func TestExecutor_ContextIsolation(t *testing.T) {
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

	// Both agents succeed
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Agent A.\nFinal Answer: A done."},
			}},
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Thought: Agent B.\nFinal Answer: B done."},
			}},
			// Synthesis
			{chunks: []agent.Chunk{
				&agent.TextChunk{Content: "Synthesized."},
			}},
		},
	}

	cfg := testConfig("test-chain", chain)
	executor := NewRealSessionExecutor(cfg, entClient, llm, nil, nil)
	session := createExecutorTestSession(t, entClient, "test-chain")

	result := executor.Execute(context.Background(), session)
	require.NotNil(t, result)
	assert.Equal(t, alertsession.StatusCompleted, result.Status)

	// Verify each agent's timeline events are scoped to their own execution_id
	execs, err := entClient.AgentExecution.Query().
		Where(agentexecution.SessionIDEQ(session.ID)).
		All(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(execs), 2)

	for _, exec := range execs {
		// Each execution should have its own timeline events
		events, err := entClient.TimelineEvent.Query().
			Where(timelineevent.ExecutionIDEQ(exec.ID)).
			All(context.Background())
		require.NoError(t, err)
		// Each agent should have at least a final_analysis event
		assert.GreaterOrEqual(t, len(events), 1,
			"execution %s (%s) should have timeline events", exec.ID, exec.AgentName)

		// All events should reference this execution
		for _, ev := range events {
			require.NotNil(t, ev.ExecutionID)
			assert.Equal(t, exec.ID, *ev.ExecutionID)
		}
	}
}

func TestExecutor_BuildConfigs(t *testing.T) {
	// Unit test for buildConfigs, buildMultiAgentConfigs, buildReplicaConfigs

	t.Run("single agent", func(t *testing.T) {
		stageCfg := config.StageConfig{
			Agents: []config.StageAgentConfig{
				{Name: "AgentA"},
			},
		}
		configs := buildConfigs(stageCfg)
		require.Len(t, configs, 1)
		assert.Equal(t, "AgentA", configs[0].agentConfig.Name)
		assert.Equal(t, "AgentA", configs[0].displayName)
	})

	t.Run("multi-agent", func(t *testing.T) {
		stageCfg := config.StageConfig{
			Agents: []config.StageAgentConfig{
				{Name: "AgentA"},
				{Name: "AgentB"},
			},
		}
		configs := buildConfigs(stageCfg)
		require.Len(t, configs, 2)
		assert.Equal(t, "AgentA", configs[0].displayName)
		assert.Equal(t, "AgentB", configs[1].displayName)
	})

	t.Run("replicas", func(t *testing.T) {
		stageCfg := config.StageConfig{
			Replicas: 3,
			Agents: []config.StageAgentConfig{
				{Name: "KubernetesAgent"},
			},
		}
		configs := buildConfigs(stageCfg)
		require.Len(t, configs, 3)
		assert.Equal(t, "KubernetesAgent-1", configs[0].displayName)
		assert.Equal(t, "KubernetesAgent-2", configs[1].displayName)
		assert.Equal(t, "KubernetesAgent-3", configs[2].displayName)
		// All replicas share the same base config name
		for _, c := range configs {
			assert.Equal(t, "KubernetesAgent", c.agentConfig.Name)
		}
	})
}

func TestAggregateError(t *testing.T) {
	stageCfg := config.StageConfig{
		SuccessPolicy: config.SuccessPolicyAll,
	}

	t.Run("returns nil for completed stage", func(t *testing.T) {
		results := []agentResult{
			{status: agent.ExecutionStatusCompleted},
		}
		err := aggregateError(results, alertsession.StatusCompleted, stageCfg)
		assert.Nil(t, err)
	})

	t.Run("single agent passthrough", func(t *testing.T) {
		origErr := fmt.Errorf("LLM timeout")
		results := []agentResult{
			{status: agent.ExecutionStatusFailed, err: origErr},
		}
		err := aggregateError(results, alertsession.StatusFailed, stageCfg)
		assert.Equal(t, origErr, err)
	})

	t.Run("multi-agent lists each failed agent", func(t *testing.T) {
		results := []agentResult{
			{status: agent.ExecutionStatusCompleted},
			{status: agent.ExecutionStatusFailed, err: fmt.Errorf("timeout")},
			{status: agent.ExecutionStatusTimedOut, err: fmt.Errorf("deadline exceeded")},
		}
		err := aggregateError(results, alertsession.StatusFailed, stageCfg)
		require.NotNil(t, err)

		msg := err.Error()
		assert.Contains(t, msg, "2/3 executions failed (policy: all)")
		assert.Contains(t, msg, "agent 2 (failed): timeout")
		assert.Contains(t, msg, "agent 3 (timed_out): deadline exceeded")
		// Successful agent should NOT appear in the error
		assert.NotContains(t, msg, "agent 1")
	})

	t.Run("agent with nil error shows unknown error", func(t *testing.T) {
		results := []agentResult{
			{status: agent.ExecutionStatusCompleted},
			{status: agent.ExecutionStatusFailed, err: nil},
		}
		err := aggregateError(results, alertsession.StatusFailed, stageCfg)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "unknown error")
	})

	t.Run("uses default policy label when stage policy empty", func(t *testing.T) {
		results := []agentResult{
			{status: agent.ExecutionStatusFailed},
			{status: agent.ExecutionStatusFailed},
		}
		emptyCfg := config.StageConfig{} // no SuccessPolicy
		err := aggregateError(results, alertsession.StatusFailed, emptyCfg)
		require.NotNil(t, err)
		assert.Contains(t, err.Error(), "policy: any")
	})
}

func TestParallelTypePtr(t *testing.T) {
	tests := []struct {
		name     string
		cfg      config.StageConfig
		expected *string
	}{
		{
			name:     "single agent",
			cfg:      config.StageConfig{Agents: []config.StageAgentConfig{{Name: "A"}}},
			expected: nil,
		},
		{
			name: "multi-agent",
			cfg:  config.StageConfig{Agents: []config.StageAgentConfig{{Name: "A"}, {Name: "B"}}},
			expected: func() *string { s := "multi_agent"; return &s }(),
		},
		{
			name: "replicas",
			cfg:  config.StageConfig{Replicas: 3, Agents: []config.StageAgentConfig{{Name: "A"}}},
			expected: func() *string { s := "replica"; return &s }(),
		},
		{
			name:     "replicas=1 treated as single",
			cfg:      config.StageConfig{Replicas: 1, Agents: []config.StageAgentConfig{{Name: "A"}}},
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parallelTypePtr(tc.cfg)
			if tc.expected == nil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, *tc.expected, *result)
			}
		})
	}
}

func TestSuccessPolicyPtr(t *testing.T) {
	t.Run("nil for single agent", func(t *testing.T) {
		cfg := config.StageConfig{Agents: []config.StageAgentConfig{{Name: "A"}}}
		result := successPolicyPtr(cfg, config.SuccessPolicyAny)
		assert.Nil(t, result)
	})

	t.Run("returns resolved policy for multi-agent", func(t *testing.T) {
		cfg := config.StageConfig{Agents: []config.StageAgentConfig{{Name: "A"}, {Name: "B"}}}
		result := successPolicyPtr(cfg, config.SuccessPolicyAll)
		require.NotNil(t, result)
		assert.Equal(t, "all", *result)
	})

	t.Run("returns resolved policy for replicas", func(t *testing.T) {
		cfg := config.StageConfig{Replicas: 2, Agents: []config.StageAgentConfig{{Name: "A"}}}
		result := successPolicyPtr(cfg, config.SuccessPolicyAny)
		require.NotNil(t, result)
		assert.Equal(t, "any", *result)
	})
}

func TestMapTerminalStatus(t *testing.T) {
	tests := []struct {
		status   alertsession.Status
		expected string
	}{
		{alertsession.StatusCompleted, events.StageStatusCompleted},
		{alertsession.StatusFailed, events.StageStatusFailed},
		{alertsession.StatusTimedOut, events.StageStatusTimedOut},
		{alertsession.StatusCancelled, events.StageStatusCancelled},
		{alertsession.StatusInProgress, events.StageStatusFailed}, // unexpected → failed
	}

	for _, tc := range tests {
		t.Run(string(tc.status), func(t *testing.T) {
			result := mapTerminalStatus(stageResult{status: tc.status})
			assert.Equal(t, tc.expected, result)
		})
	}
}
