package queue

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	util "github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────

func scoringTestConfig(chainID string, scoringEnabled bool) *config.Config {
	maxIter := 1
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:   "test-provider",
			LLMBackend:    config.LLMBackendLangChain,
			MaxIterations: &maxIter,
		},
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			config.AgentNameScoring: {
				Type:          config.AgentTypeScoring,
				LLMBackend:    config.LLMBackendLangChain,
				MaxIterations: &maxIter,
			},
		}),
		LLMProviderRegistry: config.NewLLMProviderRegistry(map[string]*config.LLMProviderConfig{
			"test-provider": {
				Type:  config.LLMProviderTypeGoogle,
				Model: "test-model",
			},
		}),
		ChainRegistry: config.NewChainRegistry(map[string]*config.ChainConfig{
			chainID: {
				AlertTypes: []string{"test-alert"},
				Scoring: &config.ScoringConfig{
					Enabled: scoringEnabled,
				},
				Stages: []config.StageConfig{
					{
						Name:   "investigation",
						Agents: []config.StageAgentConfig{{Name: "TestAgent"}},
					},
				},
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(nil),
	}
}

func createScoringTestSession(t *testing.T, client *ent.Client, chainID string, status alertsession.Status) *ent.AlertSession {
	t.Helper()
	session, err := client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("test alert data").
		SetAgentType("test").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(status).
		SetAuthor("test-user").
		Save(context.Background())
	require.NoError(t, err)
	return session
}

// ────────────────────────────────────────────────────────────
// Integration tests
// ────────────────────────────────────────────────────────────

func TestScoringExecutor_PrepareScoring_CreatesRecords(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	pub := &testEventPublisher{}

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, pub)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	scoreID, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)

	// Verify SessionScore created
	score, err := entClient.SessionScore.Get(ctx, scoreID)
	require.NoError(t, err)
	assert.Equal(t, session.ID, score.SessionID)
	assert.Equal(t, sessionscore.StatusInProgress, score.Status)
	assert.Equal(t, "test-user", score.ScoreTriggeredBy)
	assert.NotNil(t, score.StageID, "stage_id should be set")

	// Verify Stage created
	stages, err := entClient.Stage.Query().
		Where(stage.SessionIDEQ(session.ID), stage.StageTypeEQ(stage.StageTypeScoring)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, stages, 1)
	assert.Equal(t, "Scoring", stages[0].StageName)
	assert.Equal(t, 1, stages[0].StageIndex) // GetMaxStageIndex returns 0 for no stages, so +1 = 1

	// Verify AgentExecution created
	execs, err := stages[0].QueryAgentExecutions().All(ctx)
	require.NoError(t, err)
	require.Len(t, execs, 1)
	assert.Equal(t, config.AgentNameScoring, execs[0].AgentName)
}

func TestScoringExecutor_PrepareScoring_RejectsDuplicateInProgress(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// First scoring succeeds
	_, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	require.NoError(t, err)

	// Second scoring fails with ErrScoringInProgress
	_, err = executor.prepareScoring(ctx, session.ID, "test-user", false)
	assert.ErrorIs(t, err, ErrScoringInProgress)
}

func TestScoringExecutor_PrepareScoring_RejectsNonTerminalSession(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusInProgress)

	_, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in a terminal state")
}

func TestScoringExecutor_PrepareScoring_RejectsDisabledScoring(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	_, err := executor.prepareScoring(ctx, session.ID, "auto", true) // checkEnabled=true
	assert.ErrorIs(t, err, ErrScoringDisabled)
}

func TestScoringExecutor_PrepareScoring_BypassesDisabledCheck(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// With checkEnabled=false (API re-score), disabled flag is bypassed
	scoreID, err := executor.prepareScoring(ctx, session.ID, "user@test.com", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)
}

func TestScoringExecutor_SubmitScoring_ReturnsScoreID(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	pub := &testEventPublisher{}

	// LLM that returns a simple text response (scoring controller will fail to parse,
	// but we're testing the executor's record creation and async launch, not the LLM)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "Score: 75"}}},
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "No missing tools."}}},
		},
	}

	executor := NewScoringExecutor(cfg, entClient, llm, pub)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	scoreID, err := executor.SubmitScoring(t.Context(), session.ID, "api-user", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)

	// Verify score record exists immediately (before async execution completes)
	score, err := entClient.SessionScore.Get(t.Context(), scoreID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusInProgress, score.Status)

	// Wait for async execution to complete
	executor.Stop()

	// Verify stage events were published
	assert.True(t, pub.hasStageStatus("Scoring", "started"), "expected scoring stage started event")
}

func TestScoringExecutor_ScoreSessionAsync_SilentWhenScoringDisabled(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	llm := &mockLLMClient{}
	pub := &testEventPublisher{}

	executor := NewScoringExecutor(cfg, entClient, llm, pub)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	executor.ScoreSessionAsync(session.ID, "auto", true)
	executor.Stop()

	// Verify no LLM calls were made
	llm.mu.Lock()
	llmCalls := llm.callCount
	llm.mu.Unlock()
	assert.Equal(t, 0, llmCalls)

	// Verify no scoring records were created
	scores, err := entClient.SessionScore.Query().All(t.Context())
	require.NoError(t, err)
	assert.Empty(t, scores)
}

func TestScoringExecutor_GracefulShutdown(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	// LLM that blocks until we release it
	blockCh := make(chan struct{})
	llm := &blockingMockLLMClient{blockCh: blockCh}
	pub := &testEventPublisher{}

	executor := NewScoringExecutor(cfg, entClient, llm, pub)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Submit scoring (will block in LLM call)
	scoreID, err := executor.SubmitScoring(t.Context(), session.ID, "test", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)

	// Stop should block until LLM completes
	stopped := make(chan struct{})
	go func() {
		executor.Stop()
		close(stopped)
	}()

	// Verify Stop is blocked
	select {
	case <-stopped:
		t.Fatal("Stop() returned before LLM released")
	case <-time.After(100 * time.Millisecond):
		// Expected: Stop is waiting
	}

	// New submissions should be rejected
	_, err = executor.SubmitScoring(t.Context(), session.ID, "test2", false)
	assert.ErrorIs(t, err, ErrShuttingDown)

	// Release the LLM
	close(blockCh)

	// Stop should now complete
	select {
	case <-stopped:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return after LLM released")
	}
}

func TestScoringExecutor_PrepareScoring_StageIndexAfterExistingStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Create an existing stage (index 0)
	_, err := entClient.Stage.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageName("Investigation").
		SetStageIndex(0).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeInvestigation).
		Save(ctx)
	require.NoError(t, err)

	scoreID, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	require.NoError(t, err)

	// Existing stage has index 0, so scoring stage should have index 1
	score, err := entClient.SessionScore.Get(ctx, scoreID)
	require.NoError(t, err)

	scoringStage, err := entClient.Stage.Get(ctx, *score.StageID)
	require.NoError(t, err)
	assert.Equal(t, 1, scoringStage.StageIndex) // max(0) + 1 = 1
}

func TestScoringExecutor_PrepareScoring_AcceptsAllTerminalStatuses(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	terminalStatuses := []alertsession.Status{
		alertsession.StatusCompleted,
		alertsession.StatusFailed,
		alertsession.StatusTimedOut,
		alertsession.StatusCancelled,
	}

	for _, status := range terminalStatuses {
		t.Run(string(status), func(t *testing.T) {
			executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})
			session := createScoringTestSession(t, entClient, chainID, status)

			scoreID, err := executor.prepareScoring(t.Context(), session.ID, "test", false)
			require.NoError(t, err)
			assert.NotEmpty(t, scoreID)
		})
	}
}

func TestScoringExecutor_ExecuteScoring_FailsGracefully(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	pub := &testEventPublisher{}

	// LLM that returns two responses (scoring controller needs 2+ calls,
	// but the mock only has 2 so the retry will fail)
	llm := &mockLLMClient{
		responses: []mockLLMResponse{
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "Analysis without a numeric score"}}},
			{chunks: []agent.Chunk{&agent.TextChunk{Content: "No tools missing."}}},
		},
	}

	executor := NewScoringExecutor(cfg, entClient, llm, pub)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Prepare records
	scoreID, err := executor.prepareScoring(t.Context(), session.ID, "test", false)
	require.NoError(t, err)

	// Execute (will fail because LLM doesn't produce parseable score)
	executor.executeScoring(t.Context(), scoreID, session.ID)

	// Verify the score was marked as failed
	score, err := entClient.SessionScore.Get(t.Context(), scoreID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusFailed, score.Status)
	assert.NotNil(t, score.CompletedAt)
	assert.NotNil(t, score.ErrorMessage)

	// Verify terminal stage events were published
	assert.True(t, pub.hasStageStatus("Scoring", "started"))
	assert.True(t, pub.hasStageStatus("Scoring", "failed"))
}

func TestScoringExecutor_BuildScoringContext_FiltersStageTypes(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Create stages of various types
	createStageWithExec := func(name string, idx int, stageType stage.StageType) {
		t.Helper()
		stgID := uuid.New().String()
		_, err := entClient.Stage.Create().
			SetID(stgID).
			SetSessionID(session.ID).
			SetStageName(name).
			SetStageIndex(idx).
			SetExpectedAgentCount(1).
			SetStageType(stageType).
			Save(ctx)
		require.NoError(t, err)

		_, err = entClient.AgentExecution.Create().
			SetID(uuid.New().String()).
			SetStageID(stgID).
			SetSessionID(session.ID).
			SetAgentName("test-agent").
			SetAgentIndex(1).
			SetLlmBackend("langchain").
			Save(ctx)
		require.NoError(t, err)
	}

	createStageWithExec("Investigation", 0, stage.StageTypeInvestigation)
	createStageWithExec("Action", 1, stage.StageTypeAction)
	createStageWithExec("Chat", 2, stage.StageTypeChat)
	createStageWithExec("Exec Summary", 3, stage.StageTypeExecSummary)
	createStageWithExec("Previous Scoring", 4, stage.StageTypeScoring)

	result := executor.buildScoringContext(ctx, session.ID)

	// Investigation, Action, and Exec Summary stages should be included
	assert.Contains(t, result, "Investigation")
	assert.Contains(t, result, "Action")
	assert.Contains(t, result, "Exec Summary")

	// Chat and Scoring stages should be excluded
	assert.NotContains(t, result, "Chat")
	assert.NotContains(t, result, "Previous Scoring")
}

func TestScoringExecutor_BuildScoringContext_EmptyForNoStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{})
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	result := executor.buildScoringContext(t.Context(), session.ID)

	// Should still produce output (the header), but no stage content
	assert.NotEmpty(t, result, "should produce investigation history header even with no stages")
}

// blockingMockLLMClient blocks Generate until blockCh is closed.
type blockingMockLLMClient struct {
	blockCh chan struct{}
	mu      sync.Mutex
}

func (m *blockingMockLLMClient) Generate(_ context.Context, _ *agent.GenerateInput) (<-chan agent.Chunk, error) {
	<-m.blockCh
	ch := make(chan agent.Chunk, 1)
	ch <- &agent.TextChunk{Content: "done"}
	close(ch)
	return ch, nil
}

func (m *blockingMockLLMClient) Close() error { return nil }
