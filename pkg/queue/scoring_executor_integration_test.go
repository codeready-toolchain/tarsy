package queue

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	util "github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func assertGolden(t *testing.T, name string, actual string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")

	if *updateGolden {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(actual), 0o644))
		t.Logf("updated golden file: %s", path)
		return
	}

	expected, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing — run with -update to create: %s", path)
	assert.Equal(t, string(expected), actual, "golden mismatch: %s", path)
}

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

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, pub, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	scoreID, _, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
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
	assert.Equal(t, "Reflection", stages[0].StageName)
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

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// First scoring succeeds
	_, _, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	require.NoError(t, err)

	// Second scoring fails with ErrScoringInProgress
	_, _, err = executor.prepareScoring(ctx, session.ID, "test-user", false)
	assert.ErrorIs(t, err, ErrScoringInProgress)
}

func TestScoringExecutor_PrepareScoring_RejectsNonTerminalSession(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusInProgress)

	_, _, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in a terminal state")
}

func TestScoringExecutor_PrepareScoring_RejectsDisabledScoring(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	_, _, err := executor.prepareScoring(ctx, session.ID, "auto", true) // checkEnabled=true
	assert.ErrorIs(t, err, ErrScoringDisabled)
}

func TestScoringExecutor_PrepareScoring_BypassesDisabledCheck(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// With checkEnabled=false (API re-score), disabled flag is bypassed
	scoreID, _, err := executor.prepareScoring(ctx, session.ID, "user@test.com", false)
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

	executor := NewScoringExecutor(cfg, entClient, llm, pub, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	scoreID, err := executor.SubmitScoring(t.Context(), session.ID, "api-user", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)

	// Verify score record exists immediately (before async execution completes)
	score, err := entClient.SessionScore.Get(t.Context(), scoreID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusInProgress, score.Status)

	// Poll until the background goroutine reaches a terminal state so we
	// don't cancel it by calling Stop() (which now cancels active contexts).
	require.Eventually(t, func() bool {
		s, getErr := entClient.SessionScore.Get(t.Context(), scoreID)
		return getErr == nil && s.Status != sessionscore.StatusInProgress && s.Status != sessionscore.StatusPending
	}, 5*time.Second, 50*time.Millisecond, "score should reach terminal state")

	executor.Stop()

	assert.True(t, pub.hasStageStatus("Reflection", "started"), "expected scoring stage started event")
}

func TestScoringExecutor_ScoreSessionAsync_SilentWhenScoringDisabled(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, false) // scoring disabled

	llm := &mockLLMClient{}
	pub := &testEventPublisher{}

	executor := NewScoringExecutor(cfg, entClient, llm, pub, nil, nil)

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

	// LLM that blocks until released or context is cancelled.
	blockCh := make(chan struct{})
	llm := &blockingMockLLMClient{blockCh: blockCh}
	pub := &testEventPublisher{}

	executor := NewScoringExecutor(cfg, entClient, llm, pub, nil, nil)

	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Submit scoring (goroutine will block in LLM call)
	scoreID, err := executor.SubmitScoring(t.Context(), session.ID, "test", false)
	require.NoError(t, err)
	assert.NotEmpty(t, scoreID)

	// Give the goroutine time to reach the LLM call.
	time.Sleep(200 * time.Millisecond)

	// New submissions should be rejected after Stop() marks stopped=true.
	// Stop() also cancels in-flight contexts, so the blocked LLM returns.
	stopped := make(chan struct{})
	go func() {
		executor.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		// Expected: Stop() cancelled the context, LLM unblocked, goroutine finished.
	case <-time.After(5 * time.Second):
		t.Fatal("Stop() did not return after cancelling contexts")
	}

	// New submissions should be rejected
	_, err = executor.SubmitScoring(t.Context(), session.ID, "test2", false)
	assert.ErrorIs(t, err, ErrShuttingDown)

	// Score should be in a terminal failed/cancelled state
	score, err := entClient.SessionScore.Get(t.Context(), scoreID)
	require.NoError(t, err)
	assert.NotEqual(t, sessionscore.StatusInProgress, score.Status)
}

func TestScoringExecutor_PrepareScoring_StageIndexAfterExistingStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)

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

	scoreID, _, err := executor.prepareScoring(ctx, session.ID, "test-user", false)
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
			executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
			session := createScoringTestSession(t, entClient, chainID, status)

			scoreID, _, err := executor.prepareScoring(t.Context(), session.ID, "test", false)
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

	executor := NewScoringExecutor(cfg, entClient, llm, pub, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Prepare records
	scoreID, stageID, err := executor.prepareScoring(t.Context(), session.ID, "test", false)
	require.NoError(t, err)

	// Execute (will fail because LLM doesn't produce parseable score)
	executor.executeScoring(t.Context(), scoreID, stageID, session.ID)

	// Verify the score was marked as failed
	score, err := entClient.SessionScore.Get(t.Context(), scoreID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusFailed, score.Status)
	assert.NotNil(t, score.CompletedAt)
	assert.NotNil(t, score.ErrorMessage)

	// Verify terminal stage events were published
	assert.True(t, pub.hasStageStatus("Reflection", "started"))
	assert.True(t, pub.hasStageStatus("Reflection", "failed"))
}

func TestScoringExecutor_BuildScoringContext_FiltersStageTypes(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
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

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_filters_stage_types", result)
}

func TestScoringExecutor_BuildScoringContext_EmptyForNoStages(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)

	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	result := executor.buildScoringContext(t.Context(), session)

	// Should still produce output (the header), but no stage content
	assert.NotEmpty(t, result, "should produce investigation history header even with no stages")
}

func TestScoringExecutor_BuildScoringContext_TimelineEventsIncluded(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	stgID := uuid.New().String()
	_, err := entClient.Stage.Create().
		SetID(stgID).
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(0).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeInvestigation).
		Save(ctx)
	require.NoError(t, err)

	execID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(execID).
		SetStageID(stgID).
		SetSessionID(session.ID).
		SetAgentName("Investigator").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(execID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeLlmThinking).
		SetContent("Analyzing pod metrics for anomalies").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(execID).
		SetSequenceNumber(2).
		SetEventType(timelineevent.EventTypeLlmToolCall).
		SetContent("pod-1 Running, pod-2 CrashLoopBackOff").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{
			"server_name": "k8s",
			"tool_name":   "get_pods",
			"arguments":   `{"namespace":"prod"}`,
		}).
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(execID).
		SetSequenceNumber(3).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Root cause: pod-2 is OOMKilled").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_timeline_events", result)
}

func TestScoringExecutor_BuildScoringContext_ParallelAgentsWithSynthesis(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Investigation stage with 2 parallel agents
	investStgID := uuid.New().String()
	_, err := entClient.Stage.Create().
		SetID(investStgID).
		SetSessionID(session.ID).
		SetStageName("parallel-investigation").
		SetStageIndex(0).
		SetExpectedAgentCount(2).
		SetStageType(stage.StageTypeInvestigation).
		Save(ctx)
	require.NoError(t, err)

	exec1ID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(exec1ID).
		SetStageID(investStgID).
		SetSessionID(session.ID).
		SetAgentName("LogAnalyzer").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(investStgID).
		SetExecutionID(exec1ID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Log analysis: detected OOM events").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	exec2ID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(exec2ID).
		SetStageID(investStgID).
		SetSessionID(session.ID).
		SetAgentName("MetricsChecker").
		SetAgentIndex(2).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(investStgID).
		SetExecutionID(exec2ID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Memory usage at 98%, CPU normal").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	// Synthesis stage referencing the investigation stage
	synthStgID := uuid.New().String()
	_, err = entClient.Stage.Create().
		SetID(synthStgID).
		SetSessionID(session.ID).
		SetStageName("synthesis").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeSynthesis).
		SetReferencedStageID(investStgID).
		Save(ctx)
	require.NoError(t, err)

	synthExecID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(synthExecID).
		SetStageID(synthStgID).
		SetSessionID(session.ID).
		SetAgentName("Synthesizer").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(synthStgID).
		SetExecutionID(synthExecID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Combined: OOM + high memory confirm memory leak").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_parallel_with_synthesis", result)
}

func TestScoringExecutor_BuildScoringContext_ExecutiveSummary(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	// Create an investigation stage (so context isn't empty)
	stgID := uuid.New().String()
	_, err := entClient.Stage.Create().
		SetID(stgID).
		SetSessionID(session.ID).
		SetStageName("investigation").
		SetStageIndex(0).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeInvestigation).
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.AgentExecution.Create().
		SetID(uuid.New().String()).
		SetStageID(stgID).
		SetSessionID(session.ID).
		SetAgentName("Investigator").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	// Session-level executive summary event (no stage/execution)
	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeExecutiveSummary).
		SetContent("Overall: memory leak in pod-2 caused cascading failures").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_executive_summary", result)
}

func TestScoringExecutor_BuildScoringContext_OrchestratedStage(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	stgID := uuid.New().String()
	_, err := entClient.Stage.Create().
		SetID(stgID).
		SetSessionID(session.ID).
		SetStageName("orchestrated-investigation").
		SetStageIndex(0).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeInvestigation).
		Save(ctx)
	require.NoError(t, err)

	// Orchestrator execution (no parent)
	orchExecID := uuid.New().String()
	_, err = entClient.AgentExecution.Create().
		SetID(orchExecID).
		SetStageID(stgID).
		SetSessionID(session.ID).
		SetAgentName("Orchestrator").
		SetAgentIndex(1).
		SetLlmBackend("langchain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(orchExecID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Orchestrator conclusion: delegated to sub-agents").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	// Sub-agent 1 (parent = orchestrator)
	sub1ExecID := uuid.New().String()
	task1 := "Analyze pod logs"
	_, err = entClient.AgentExecution.Create().
		SetID(sub1ExecID).
		SetStageID(stgID).
		SetSessionID(session.ID).
		SetAgentName("LogWorker").
		SetAgentIndex(2).
		SetLlmBackend("langchain").
		SetParentExecutionID(orchExecID).
		SetTask(task1).
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(sub1ExecID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Sub-agent found: OOM in pod-2 logs").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	// Sub-agent 2 (parent = orchestrator)
	sub2ExecID := uuid.New().String()
	task2 := "Check resource limits"
	_, err = entClient.AgentExecution.Create().
		SetID(sub2ExecID).
		SetStageID(stgID).
		SetSessionID(session.ID).
		SetAgentName("ResourceWorker").
		SetAgentIndex(3).
		SetLlmBackend("langchain").
		SetParentExecutionID(orchExecID).
		SetTask(task2).
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	_, err = entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stgID).
		SetExecutionID(sub2ExecID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeFinalAnalysis).
		SetContent("Resource limits: memory limit 512Mi too low").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_orchestrated_stage", result)
}

func TestScoringExecutor_BuildScoringContext_FullPipeline(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := t.Context()

	chainID := "test-chain"
	cfg := scoringTestConfig(chainID, true)
	executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{}, &testEventPublisher{}, nil, nil)
	session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

	type stageSetup struct {
		id        string
		name      string
		idx       int
		stageType stage.StageType
		refID     *string
		agents    []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}
	}

	createStage := func(s stageSetup) {
		t.Helper()
		builder := entClient.Stage.Create().
			SetID(s.id).
			SetSessionID(session.ID).
			SetStageName(s.name).
			SetStageIndex(s.idx).
			SetExpectedAgentCount(len(s.agents)).
			SetStageType(s.stageType)
		if s.refID != nil {
			builder = builder.SetReferencedStageID(*s.refID)
		}
		_, err := builder.Save(ctx)
		require.NoError(t, err)

		for _, a := range s.agents {
			execBuilder := entClient.AgentExecution.Create().
				SetID(uuid.New().String()).
				SetStageID(s.id).
				SetSessionID(session.ID).
				SetAgentName(a.name).
				SetAgentIndex(a.idx).
				SetLlmBackend("langchain").
				SetStatus("completed")
			if a.parentExecID != "" {
				execBuilder = execBuilder.SetParentExecutionID(a.parentExecID)
			}
			exec, err := execBuilder.Save(ctx)
			require.NoError(t, err)

			if a.finalAnalysis != "" {
				_, err = entClient.TimelineEvent.Create().
					SetID(uuid.New().String()).
					SetSessionID(session.ID).
					SetStageID(s.id).
					SetExecutionID(exec.ID).
					SetSequenceNumber(1).
					SetEventType(timelineevent.EventTypeFinalAnalysis).
					SetContent(a.finalAnalysis).
					SetStatus(timelineevent.StatusCompleted).
					SetMetadata(map[string]interface{}{}).
					Save(ctx)
				require.NoError(t, err)
			}
		}
	}

	// 1. Investigation (parallel)
	investID := uuid.New().String()
	createStage(stageSetup{
		id: investID, name: "investigation", idx: 0, stageType: stage.StageTypeInvestigation,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "Investigator", idx: 1, finalAnalysis: "Found OOM events"},
			{name: "MetricsChecker", idx: 2, finalAnalysis: "Memory at 98%"},
		},
	})

	// 2. Synthesis (references investigation)
	synthID := uuid.New().String()
	createStage(stageSetup{
		id: synthID, name: "synthesis", idx: 1, stageType: stage.StageTypeSynthesis, refID: &investID,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "Synthesizer", idx: 1, finalAnalysis: "Confirmed memory leak"},
		},
	})

	// 3. Action (remediation)
	actionID := uuid.New().String()
	createStage(stageSetup{
		id: actionID, name: "remediation", idx: 2, stageType: stage.StageTypeAction,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "Remediator", idx: 1, finalAnalysis: "Restarted pod-2 successfully"},
		},
	})

	// 4. Exec summary
	execSumID := uuid.New().String()
	createStage(stageSetup{
		id: execSumID, name: "exec-summary", idx: 3, stageType: stage.StageTypeExecSummary,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "ExecSummaryAgent", idx: 1, finalAnalysis: "Investigation complete"},
		},
	})

	// 5. Chat (should be excluded)
	chatID := uuid.New().String()
	createStage(stageSetup{
		id: chatID, name: "chat", idx: 4, stageType: stage.StageTypeChat,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "ChatAgent", idx: 1, finalAnalysis: "Chat response here"},
		},
	})

	// 6. Previous scoring (should be excluded)
	prevScoreID := uuid.New().String()
	createStage(stageSetup{
		id: prevScoreID, name: "scoring", idx: 5, stageType: stage.StageTypeScoring,
		agents: []struct {
			name          string
			idx           int
			parentExecID  string
			finalAnalysis string
		}{
			{name: "ScoringAgent", idx: 1, finalAnalysis: "Old score: 70"},
		},
	})

	// Session-level executive summary
	_, err := entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeExecutiveSummary).
		SetContent("Executive: memory leak caused cascading pod failures").
		SetStatus(timelineevent.StatusCompleted).
		SetMetadata(map[string]interface{}{}).
		Save(ctx)
	require.NoError(t, err)

	result := executor.buildScoringContext(ctx, session)
	assertGolden(t, "context_full_pipeline", result)
}

// blockingMockLLMClient blocks Generate until blockCh is closed.
type blockingMockLLMClient struct {
	blockCh chan struct{}
}

func (m *blockingMockLLMClient) Generate(ctx context.Context, _ *agent.GenerateInput) (<-chan agent.Chunk, error) {
	select {
	case <-m.blockCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	ch := make(chan agent.Chunk, 1)
	ch <- &agent.TextChunk{Content: "done"}
	close(ch)
	return ch, nil
}

func (m *blockingMockLLMClient) Close() error { return nil }
