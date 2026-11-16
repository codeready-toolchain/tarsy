package queue

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/pkg/metrics"
	util "github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────

const postInvestigationTestAgent = "FollowThroughAgent"

func postInvestigationTestConfig(chainID string, entries ...config.PostInvestigationConfig) *config.Config {
	maxIter := 3
	return &config.Config{
		Defaults: &config.Defaults{
			LLMProvider:   "test-provider",
			LLMBackend:    config.LLMBackendLangChain,
			MaxIterations: &maxIter,
		},
		DashboardURL: "https://tarsy.example.com",
		AgentRegistry: config.NewAgentRegistry(map[string]*config.AgentConfig{
			config.AgentNameScoring: {
				Type:          config.AgentTypeScoring,
				LLMBackend:    config.LLMBackendLangChain,
				MaxIterations: &maxIter,
			},
			postInvestigationTestAgent: {
				Type:               config.AgentTypePostInvestigation,
				CustomInstructions: "Open a ticket only for confirmed findings.",
				MaxIterations:      &maxIter,
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
				Scoring:    &config.ScoringConfig{Enabled: true},
				Stages: []config.StageConfig{
					{
						Name:   "investigation",
						Agents: []config.StageAgentConfig{{Name: "TestAgent"}},
					},
				},
				PostInvestigation: entries,
			},
		}),
		MCPServerRegistry: config.NewMCPServerRegistry(nil),
	}
}

// seedScoredSession creates a completed session with a completed scoring
// stage (index 1) and score. Returns the session and score.
func seedScoredSession(t *testing.T, client *ent.Client, chainID string, labels []string, totalScore int) (*ent.AlertSession, *ent.SessionScore) {
	t.Helper()
	ctx := context.Background()

	create := client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("disk usage above threshold on node-7").
		SetAgentType("test").
		SetAlertType("test-alert").
		SetChainID(chainID).
		SetStatus(alertsession.StatusCompleted).
		SetFinalAnalysis("ORIGINAL-FINAL-ANALYSIS").
		SetExecutiveSummary("EXEC-SUMMARY").
		SetAuthor("test-user")
	if labels != nil {
		create.SetLabels(labels)
	}
	session, err := create.Save(ctx)
	require.NoError(t, err)

	stageID := uuid.New().String()
	_, err = client.Stage.Create().
		SetID(stageID).
		SetSessionID(session.ID).
		SetStageName(scoringStageName).
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeScoring).
		SetStatus(stage.StatusCompleted).
		Save(ctx)
	require.NoError(t, err)

	score, err := client.SessionScore.Create().
		SetID(uuid.New().String()).
		SetSessionID(session.ID).
		SetStageID(stageID).
		SetScoreTriggeredBy("auto").
		SetStatus(sessionscore.StatusCompleted).
		SetTotalScore(totalScore).
		SetScoreAnalysis("SCORE-ANALYSIS").
		SetToolImprovementReport("TOOL-REPORT").
		SetFailureTags([]string{"incomplete_evidence"}).
		SetCompletedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	return session, score
}

func textResponse(text string) mockLLMResponse {
	return mockLLMResponse{chunks: []agent.Chunk{&agent.TextChunk{Content: text}}}
}

// runPostInvestigation runs the entries and waits until every run finished.
func runPostInvestigation(e *PostInvestigationExecutor, sessionID, scoreID string) {
	e.RunAsync(sessionID, scoreID)
	e.Stop(time.Minute)
}

func postInvestigationStages(t *testing.T, client *ent.Client, sessionID string) []*ent.Stage {
	t.Helper()
	stages, err := client.Stage.Query().
		Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypePostInvestigation)).
		WithAgentExecutions().
		Order(ent.Asc(stage.FieldStageIndex)).
		All(context.Background())
	require.NoError(t, err)
	return stages
}

func finalAnalysisEvent(t *testing.T, client *ent.Client, stageID string) *ent.TimelineEvent {
	t.Helper()
	evt, err := client.TimelineEvent.Query().
		Where(timelineevent.StageIDEQ(stageID), timelineevent.EventTypeEQ(timelineevent.EventTypeFinalAnalysis)).
		Only(context.Background())
	require.NoError(t, err)
	return evt
}

// ────────────────────────────────────────────────────────────
// Executor
// ────────────────────────────────────────────────────────────

func TestPostInvestigationExecutor_RunsPassingEntries(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	chainID := "pi-passing-chain"
	cfg := postInvestigationTestConfig(chainID,
		config.PostInvestigationConfig{Name: "first", Agent: postInvestigationTestAgent},
		config.PostInvestigationConfig{Name: "gated-out", Agent: postInvestigationTestAgent, LabelsAny: []string{"action"}},
		config.PostInvestigationConfig{Name: "second", Agent: postInvestigationTestAgent},
	)
	llm := &mockLLMClient{
		capture: true,
		responses: []mockLLMResponse{
			textResponse("Nothing to follow up on.\n\nNO"),
			textResponse("Nothing to follow up on.\n\nNO"),
		},
	}
	executor := NewPostInvestigationExecutor(cfg, entClient, llm, nil, &testEventPublisher{}, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, []string{"noise"}, 80)

	skippedBefore := testutil.ToFloat64(metrics.PostInvestigationRunsTotal.WithLabelValues(chainID, "gated-out", string(postInvestigationOutcomeSkippedGate)))
	runPostInvestigation(executor, session.ID, score.ID)

	stages := postInvestigationStages(t, entClient, session.ID)
	require.Len(t, stages, 2, "the gated-out entry must not create a stage")
	names := []string{stages[0].StageName, stages[1].StageName}
	assert.ElementsMatch(t, []string{"first", "second"}, names)
	for i, stg := range stages {
		assert.Equal(t, i+2, stg.StageIndex, "stages follow the scoring stage")
		assert.Equal(t, stage.StatusCompleted, stg.Status)
		require.NotNil(t, stg.ActionsExecuted)
		assert.False(t, *stg.ActionsExecuted)
		require.Len(t, stg.Edges.AgentExecutions, 1)
		assert.Equal(t, postInvestigationTestAgent, stg.Edges.AgentExecutions[0].AgentName)
		assert.Equal(t, agentexecution.StatusCompleted, stg.Edges.AgentExecutions[0].Status)
	}

	skippedAfter := testutil.ToFloat64(metrics.PostInvestigationRunsTotal.WithLabelValues(chainID, "gated-out", string(postInvestigationOutcomeSkippedGate)))
	assert.Equal(t, skippedBefore+1, skippedAfter)

	fresh, err := entClient.AlertSession.Get(context.Background(), session.ID)
	require.NoError(t, err)
	require.Len(t, fresh.PostInvestigationSkips, 1)
	assert.Equal(t, "gated-out", fresh.PostInvestigationSkips[0].Name)
	assert.Equal(t, "needs a label in action (session has noise)", fresh.PostInvestigationSkips[0].Detail)
	assert.Equal(t, float64(2), testutil.ToFloat64(metrics.PostInvestigationRunsTotal.WithLabelValues(chainID, "first", string(postInvestigationOutcomeCompletedNoAction)))+
		testutil.ToFloat64(metrics.PostInvestigationRunsTotal.WithLabelValues(chainID, "second", string(postInvestigationOutcomeCompletedNoAction))))

	// The prompt carries the post-investigation context, not the investigation layer.
	llm.mu.Lock()
	require.NotEmpty(t, llm.capturedInputs)
	var user string
	for _, m := range llm.capturedInputs[0].Messages {
		if m.Role == agent.RoleUser {
			user = m.Content
		}
	}
	llm.mu.Unlock()
	assert.Contains(t, user, "## Completed Investigation")
	assert.Contains(t, user, "Total score: 80/100")
	assert.Contains(t, user, "disk usage above threshold on node-7")
}

func TestPostInvestigationExecutor_ActedMarkerPersistedAndStripped(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	chainID := "pi-acted-chain"
	cfg := postInvestigationTestConfig(chainID,
		config.PostInvestigationConfig{Name: "ticket", Agent: postInvestigationTestAgent},
	)
	llm := &mockLLMClient{responses: []mockLLMResponse{
		textResponse("Opened https://tracker.example.com/issues/42 with the findings.\n\nYES"),
	}}
	pub := &testEventPublisher{}
	executor := NewPostInvestigationExecutor(cfg, entClient, llm, nil, pub, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, nil, 90)

	runPostInvestigation(executor, session.ID, score.ID)

	stages := postInvestigationStages(t, entClient, session.ID)
	require.Len(t, stages, 1)
	assert.Equal(t, stage.StatusCompleted, stages[0].Status)
	require.NotNil(t, stages[0].ActionsExecuted)
	assert.True(t, *stages[0].ActionsExecuted)

	evt := finalAnalysisEvent(t, entClient, stages[0].ID)
	assert.Equal(t, "Opened https://tracker.example.com/issues/42 with the findings.", strings.TrimSpace(evt.Content))

	// Stage events carry the post_investigation type so the dashboard renders them.
	pub.mu.Lock()
	defer pub.mu.Unlock()
	var statuses []string
	for _, p := range pub.stageStatuses {
		if p.StageID == stages[0].ID {
			assert.Equal(t, string(stage.StageTypePostInvestigation), p.StageType)
			assert.Equal(t, "ticket", p.StageName)
			assert.Equal(t, stages[0].StageIndex+1, p.StageIndex, "event indexes are 1-based")
			statuses = append(statuses, p.Status)
		}
	}
	assert.Equal(t, []string{"started", "completed"}, statuses)
}

func TestPostInvestigationExecutor_UnparsableMarkerLeavesNull(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	chainID := "pi-unparsable-chain"
	cfg := postInvestigationTestConfig(chainID,
		config.PostInvestigationConfig{Name: "ticket", Agent: postInvestigationTestAgent},
	)
	llm := &mockLLMClient{responses: []mockLLMResponse{
		textResponse("No follow-through warranted."),
	}}
	executor := NewPostInvestigationExecutor(cfg, entClient, llm, nil, &testEventPublisher{}, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, nil, 90)

	runPostInvestigation(executor, session.ID, score.ID)

	stages := postInvestigationStages(t, entClient, session.ID)
	require.Len(t, stages, 1)
	assert.Equal(t, stage.StatusCompleted, stages[0].Status)
	assert.Nil(t, stages[0].ActionsExecuted)
	assert.Equal(t, "No follow-through warranted.", finalAnalysisEvent(t, entClient, stages[0].ID).Content)
}

func TestPostInvestigationExecutor_FailOpen(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()
	chainID := "pi-fail-open-chain"
	cfg := postInvestigationTestConfig(chainID,
		// Unknown agent: config resolution fails for this entry only.
		config.PostInvestigationConfig{Name: "broken", Agent: "MissingAgent"},
		config.PostInvestigationConfig{Name: "healthy", Agent: postInvestigationTestAgent},
	)
	llm := &mockLLMClient{responses: []mockLLMResponse{
		textResponse("Nothing to do.\n\nNO"),
	}}
	executor := NewPostInvestigationExecutor(cfg, entClient, llm, nil, &testEventPublisher{}, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, nil, 90)

	runPostInvestigation(executor, session.ID, score.ID)

	stages := postInvestigationStages(t, entClient, session.ID)
	require.Len(t, stages, 2)
	byName := map[string]*ent.Stage{stages[0].StageName: stages[0], stages[1].StageName: stages[1]}

	broken := byName["broken"]
	require.NotNil(t, broken)
	assert.Equal(t, stage.StatusFailed, broken.Status)
	require.NotNil(t, broken.ErrorMessage)
	assert.Contains(t, *broken.ErrorMessage, "MissingAgent")
	require.Len(t, broken.Edges.AgentExecutions, 1, "a failed execution is kept for the audit trail")
	assert.Equal(t, agentexecution.StatusFailed, broken.Edges.AgentExecutions[0].Status)

	healthy := byName["healthy"]
	require.NotNil(t, healthy)
	assert.Equal(t, stage.StatusCompleted, healthy.Status)

	// The session and its score are untouched.
	reloaded, err := entClient.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.StatusCompleted, reloaded.Status)
	require.NotNil(t, reloaded.FinalAnalysis)
	assert.Equal(t, "ORIGINAL-FINAL-ANALYSIS", *reloaded.FinalAnalysis)
	reloadedScore, err := entClient.SessionScore.Get(ctx, score.ID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusCompleted, reloadedScore.Status)
	require.NotNil(t, reloadedScore.TotalScore)
	assert.Equal(t, 90, *reloadedScore.TotalScore)
}

func TestPostInvestigationExecutor_SkipsIncompleteScore(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	chainID := "pi-incomplete-score-chain"
	cfg := postInvestigationTestConfig(chainID,
		config.PostInvestigationConfig{Name: "ticket", Agent: postInvestigationTestAgent},
	)
	executor := NewPostInvestigationExecutor(cfg, entClient, &mockLLMClient{}, nil, &testEventPublisher{}, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, nil, 90)
	_, err := entClient.SessionScore.UpdateOneID(score.ID).SetStatus(sessionscore.StatusFailed).Save(context.Background())
	require.NoError(t, err)

	runPostInvestigation(executor, session.ID, score.ID)

	assert.Empty(t, postInvestigationStages(t, entClient, session.ID))
}

func TestPostInvestigationExecutor_MemoryToolsWithoutBriefing(t *testing.T) {
	entClient, db := util.SetupTestDatabase(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx, `ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)
	memCfg := &config.MemoryConfig{
		Enabled:   true,
		MaxInject: 5,
		Embedding: config.EmbeddingConfig{Dimensions: 3},
	}
	memSvc := memory.NewService(entClient, db, &fixedEmbedder{vec: []float32{1, 0, 0}}, memCfg)

	chainID := "pi-memory-chain"
	cfg := postInvestigationTestConfig(chainID,
		config.PostInvestigationConfig{Name: "ticket", Agent: postInvestigationTestAgent},
	)
	llm := &mockLLMClient{capture: true, responses: []mockLLMResponse{
		textResponse("Nothing to do.\n\nNO"),
	}}
	executor := NewPostInvestigationExecutor(cfg, entClient, llm, nil, &testEventPublisher{}, memSvc, memCfg)
	session, score := seedScoredSession(t, entClient, chainID, nil, 90)

	runPostInvestigation(executor, session.ID, score.ID)

	llm.mu.Lock()
	require.NotEmpty(t, llm.capturedInputs)
	first := llm.capturedInputs[0]
	llm.mu.Unlock()

	var toolNames []string
	for _, tool := range first.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	assert.Contains(t, toolNames, memory.ToolRecallPastInvestigations)
	for _, m := range first.Messages {
		if m.Role == agent.RoleSystem {
			assert.NotContains(t, m.Content, "Lessons from Past Investigations")
		}
	}
}

func TestPostInvestigationExecutor_CreateStageRetriesOnIndexConflict(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	chainID := "pi-index-chain"
	cfg := postInvestigationTestConfig(chainID)
	executor := NewPostInvestigationExecutor(cfg, entClient, &mockLLMClient{}, nil, &testEventPublisher{}, nil, nil)
	session, _ := seedScoredSession(t, entClient, chainID, nil, 90)

	// N concurrent creators: each loses at most N-1 times, so N attempts suffice.
	const creators = postInvestigationStageAttempts
	var wg sync.WaitGroup
	errs := make([]error, creators)
	for i := range creators {
		wg.Go(func() {
			_, errs[i] = executor.createStage(context.Background(), session.ID, "entry")
		})
	}
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	stages := postInvestigationStages(t, entClient, session.ID)
	require.Len(t, stages, creators)
	for i, stg := range stages {
		assert.Equal(t, i+2, stg.StageIndex)
	}
}

func TestPostInvestigationExecutor_BuildContext(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	ctx := context.Background()
	chainID := "pi-context-chain"
	cfg := postInvestigationTestConfig(chainID)
	executor := NewPostInvestigationExecutor(cfg, entClient, &mockLLMClient{}, nil, &testEventPublisher{}, nil, nil)
	session, score := seedScoredSession(t, entClient, chainID, []string{"noise"}, 72)

	createStageWithFinalAnalysis := func(name string, idx int, stageType stage.StageType, content string) {
		t.Helper()
		stgID := uuid.New().String()
		_, err := entClient.Stage.Create().
			SetID(stgID).
			SetSessionID(session.ID).
			SetStageName(name).
			SetStageIndex(idx).
			SetExpectedAgentCount(1).
			SetStageType(stageType).
			SetStatus(stage.StatusCompleted).
			Save(ctx)
		require.NoError(t, err)
		execID := uuid.New().String()
		_, err = entClient.AgentExecution.Create().
			SetID(execID).
			SetStageID(stgID).
			SetSessionID(session.ID).
			SetAgentName("SomeAgent").
			SetAgentIndex(1).
			SetLlmBackend("langchain").
			SetStatus(agentexecution.StatusCompleted).
			Save(ctx)
		require.NoError(t, err)
		_, err = entClient.TimelineEvent.Create().
			SetID(uuid.New().String()).
			SetSessionID(session.ID).
			SetStageID(stgID).
			SetExecutionID(execID).
			SetSequenceNumber(idx).
			SetEventType(timelineevent.EventTypeFinalAnalysis).
			SetContent(content).
			Save(ctx)
		require.NoError(t, err)
	}

	t.Run("compact context", func(t *testing.T) {
		got := executor.buildContext(ctx, session, score, config.PostInvestigationConfig{Name: "x"})

		assert.Contains(t, got, "- Session ID: "+session.ID)
		assert.Contains(t, got, "- Chain: "+chainID)
		assert.Contains(t, got, "- Link: https://tarsy.example.com/sessions/"+session.ID)
		assert.Contains(t, got, "### Final Report\n\nORIGINAL-FINAL-ANALYSIS")
		assert.Contains(t, got, "### Executive Summary\n\nEXEC-SUMMARY")
		assert.Contains(t, got, "Labels: noise")
		assert.Contains(t, got, "- Total score: 72/100")
		assert.Contains(t, got, "- Failure tags: incomplete_evidence")
		assert.Contains(t, got, "SCORE-ANALYSIS")
		assert.Contains(t, got, "TOOL-REPORT")
		assert.NotContains(t, got, "INVESTIGATION TIMELINE")
		assert.NotContains(t, got, "ORIGINAL ALERT", "alert data is rendered by the prompt builder")
	})

	createStageWithFinalAnalysis("investigation", 0, stage.StageTypeInvestigation, "INVESTIGATION-FINDING")
	createStageWithFinalAnalysis("take-action - Amended Report", 2, stage.StageTypeCompose, "AMENDED-REPORT")

	t.Run("compose report preferred", func(t *testing.T) {
		got := executor.buildContext(ctx, session, score, config.PostInvestigationConfig{Name: "x"})
		assert.Contains(t, got, "### Final Report\n\nAMENDED-REPORT")
		assert.NotContains(t, got, "ORIGINAL-FINAL-ANALYSIS")
	})

	t.Run("include_investigation adds the timeline", func(t *testing.T) {
		got := executor.buildContext(ctx, session, score, config.PostInvestigationConfig{Name: "x", IncludeInvestigation: true})
		assert.Contains(t, got, "### Full Investigation")
		assert.Contains(t, got, "INVESTIGATION TIMELINE")
		assert.Contains(t, got, "INVESTIGATION-FINDING")
		assert.NotContains(t, got, "ORIGINAL ALERT")
	})

	t.Run("no link without dashboard url", func(t *testing.T) {
		noURL := postInvestigationTestConfig(chainID)
		noURL.DashboardURL = ""
		e := NewPostInvestigationExecutor(noURL, entClient, &mockLLMClient{}, nil, &testEventPublisher{}, nil, nil)
		got := e.buildContext(ctx, session, score, config.PostInvestigationConfig{Name: "x"})
		assert.NotContains(t, got, "- Link:")
	})
}

// ────────────────────────────────────────────────────────────
// Scoring trigger
// ────────────────────────────────────────────────────────────

type recordingPostInvestigationRunner struct {
	mu    sync.Mutex
	calls [][2]string
}

func (r *recordingPostInvestigationRunner) RunAsync(sessionID, scoreID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, [2]string{sessionID, scoreID})
}

func scoringSuccessResponses() []mockLLMResponse {
	return []mockLLMResponse{
		textResponse("The conclusion is correct and well supported.\n\n80"),
		textResponse("No tooling gaps found."),
	}
}

func TestScoringExecutor_TriggersPostInvestigation(t *testing.T) {
	entry := config.PostInvestigationConfig{Name: "ticket", Agent: postInvestigationTestAgent}

	tests := []struct {
		name          string
		entries       []config.PostInvestigationConfig
		responses     []mockLLMResponse
		autoTriggered bool
		wantCalls     int
	}{
		{name: "auto-triggered completed score", entries: []config.PostInvestigationConfig{entry}, responses: scoringSuccessResponses(), autoTriggered: true, wantCalls: 1},
		{name: "API re-score", entries: []config.PostInvestigationConfig{entry}, responses: scoringSuccessResponses(), autoTriggered: false, wantCalls: 0},
		{name: "chain without entries", entries: nil, responses: scoringSuccessResponses(), autoTriggered: true, wantCalls: 0},
		{
			name:    "failed score",
			entries: []config.PostInvestigationConfig{entry},
			responses: []mockLLMResponse{
				textResponse("Analysis without a numeric score"),
				textResponse("No tools missing."),
			},
			autoTriggered: true,
			wantCalls:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entClient, _ := util.SetupTestDatabase(t)
			chainID := "pi-trigger-chain"
			cfg := postInvestigationTestConfig(chainID, tt.entries...)

			executor := NewScoringExecutor(cfg, entClient, &mockLLMClient{responses: tt.responses}, &testEventPublisher{}, nil, nil)
			runner := &recordingPostInvestigationRunner{}
			executor.SetPostInvestigationExecutor(runner)
			session := createScoringTestSession(t, entClient, chainID, alertsession.StatusCompleted)

			scoreID, stageID, err := executor.prepareScoring(t.Context(), session.ID, "auto", true)
			require.NoError(t, err)
			executor.executeScoring(t.Context(), scoreID, stageID, session.ID, tt.autoTriggered)

			runner.mu.Lock()
			defer runner.mu.Unlock()
			require.Len(t, runner.calls, tt.wantCalls)
			if tt.wantCalls > 0 {
				assert.Equal(t, [2]string{session.ID, scoreID}, runner.calls[0])
			}
		})
	}
}

// ────────────────────────────────────────────────────────────
// Orphan recovery
// ────────────────────────────────────────────────────────────

type stuckPostInvestigationIDs struct {
	sessionID string
	stageID   string
	execID    string
	eventID   string
}

// seedStuckPostInvestigation creates a completed session owned by podID with a
// post-investigation stage in stageStatus, an active execution and a
// streaming timeline event.
func seedStuckPostInvestigation(
	t *testing.T, client *ent.Client, podID string,
	stageStatus stage.Status, startedAt, sessionCompletedAt time.Time,
) stuckPostInvestigationIDs {
	t.Helper()
	ctx := context.Background()
	ids := stuckPostInvestigationIDs{
		sessionID: uuid.New().String(),
		stageID:   uuid.New().String(),
		execID:    uuid.New().String(),
		eventID:   uuid.New().String(),
	}

	_, err := client.AlertSession.Create().
		SetID(ids.sessionID).
		SetAlertData("stuck post-investigation").
		SetAgentType("test-agent").
		SetAlertType("test-alert").
		SetChainID("test-chain").
		SetStatus(alertsession.StatusCompleted).
		SetCompletedAt(sessionCompletedAt).
		SetPodID(podID).
		SetAuthor("test-user").
		Save(ctx)
	require.NoError(t, err)

	stageCreate := client.Stage.Create().
		SetID(ids.stageID).
		SetSessionID(ids.sessionID).
		SetStageName("ticket").
		SetStageIndex(2).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypePostInvestigation).
		SetStatus(stageStatus)
	if stageStatus != stage.StatusPending {
		stageCreate.SetStartedAt(startedAt)
	}
	_, err = stageCreate.Save(ctx)
	require.NoError(t, err)

	_, err = client.AgentExecution.Create().
		SetID(ids.execID).
		SetStageID(ids.stageID).
		SetSessionID(ids.sessionID).
		SetAgentName(postInvestigationTestAgent).
		SetAgentIndex(1).
		SetLlmBackend("test").
		SetStatus(agentexecution.StatusActive).
		SetStartedAt(startedAt).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.TimelineEvent.Create().
		SetID(ids.eventID).
		SetSessionID(ids.sessionID).
		SetStageID(ids.stageID).
		SetExecutionID(ids.execID).
		SetSequenceNumber(1).
		SetEventType(timelineevent.EventTypeLlmResponse).
		SetContent("partial output").
		SetStatus(timelineevent.StatusStreaming).
		Save(ctx)
	require.NoError(t, err)

	return ids
}

func assertPostInvestigationOrphanClosed(t *testing.T, client *ent.Client, ids stuckPostInvestigationIDs) {
	t.Helper()
	ctx := context.Background()

	stg, err := client.Stage.Get(ctx, ids.stageID)
	require.NoError(t, err)
	assert.Equal(t, stage.StatusFailed, stg.Status)
	require.NotNil(t, stg.ErrorMessage)
	assert.Equal(t, orphanedPostInvestigationError, *stg.ErrorMessage)

	exec, err := client.AgentExecution.Get(ctx, ids.execID)
	require.NoError(t, err)
	assert.Equal(t, agentexecution.StatusFailed, exec.Status)

	evt, err := client.TimelineEvent.Get(ctx, ids.eventID)
	require.NoError(t, err)
	assert.Equal(t, timelineevent.StatusTimedOut, evt.Status)
}

func assertPostInvestigationStageStatus(t *testing.T, client *ent.Client, ids stuckPostInvestigationIDs, want stage.Status) {
	t.Helper()
	stg, err := client.Stage.Get(context.Background(), ids.stageID)
	require.NoError(t, err)
	assert.Equal(t, want, stg.Status)
}

func TestStartupPostInvestigationOrphanCleanup(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	podID := "startup-pi-pod"
	now := time.Now()

	active := seedStuckPostInvestigation(t, entClient, podID, stage.StatusActive, now, now)
	pending := seedStuckPostInvestigation(t, entClient, podID, stage.StatusPending, now, now)
	otherPod := seedStuckPostInvestigation(t, entClient, "other-pod", stage.StatusActive, now, now)
	completed := seedStuckPostInvestigation(t, entClient, podID, stage.StatusCompleted, now, now)
	deleted := seedStuckPostInvestigation(t, entClient, podID, stage.StatusActive, now, now)
	_, err := entClient.AlertSession.UpdateOneID(deleted.sessionID).SetDeletedAt(now).Save(context.Background())
	require.NoError(t, err)

	require.NoError(t, CleanupStartupPostInvestigationOrphans(context.Background(), entClient, podID))

	assertPostInvestigationOrphanClosed(t, entClient, active)
	assertPostInvestigationOrphanClosed(t, entClient, pending)
	assertPostInvestigationStageStatus(t, entClient, otherPod, stage.StatusActive)
	assertPostInvestigationStageStatus(t, entClient, completed, stage.StatusCompleted)
	assertPostInvestigationStageStatus(t, entClient, deleted, stage.StatusActive)
}

func TestStalePostInvestigationRecovery(t *testing.T) {
	entClient, _ := util.SetupTestDatabase(t)
	now := time.Now()
	maxTimeout := 10 * time.Minute

	staleActive := seedStuckPostInvestigation(t, entClient, "any-pod", stage.StatusActive,
		now.Add(-maxTimeout-postInvestigationStaleMargin-time.Minute), now.Add(-time.Hour))
	freshActive := seedStuckPostInvestigation(t, entClient, "any-pod", stage.StatusActive,
		now.Add(-time.Minute), now.Add(-time.Hour))
	stalePending := seedStuckPostInvestigation(t, entClient, "any-pod", stage.StatusPending,
		now, now.Add(-scoringTimeout-maxTimeout-postInvestigationStaleMargin-time.Minute))
	freshPending := seedStuckPostInvestigation(t, entClient, "any-pod", stage.StatusPending,
		now, now.Add(-time.Minute))
	deleted := seedStuckPostInvestigation(t, entClient, "any-pod", stage.StatusActive,
		now.Add(-maxTimeout-postInvestigationStaleMargin-time.Minute), now.Add(-time.Hour))
	_, err := entClient.AlertSession.UpdateOneID(deleted.sessionID).SetDeletedAt(now).Save(context.Background())
	require.NoError(t, err)

	pool := &WorkerPool{
		podID:  "test-pod",
		client: entClient,
		config: intTestQueueConfig(),
	}

	t.Run("disabled without entries", func(t *testing.T) {
		require.NoError(t, pool.recoverStalePostInvestigation(context.Background()))
		assertPostInvestigationStageStatus(t, entClient, staleActive, stage.StatusActive)
	})

	t.Run("recovers stale stages only", func(t *testing.T) {
		pool.SetPostInvestigationMaxTimeout(maxTimeout)
		require.NoError(t, pool.detectAndRecoverOrphans(context.Background()))

		assertPostInvestigationOrphanClosed(t, entClient, staleActive)
		assertPostInvestigationOrphanClosed(t, entClient, stalePending)
		assertPostInvestigationStageStatus(t, entClient, freshActive, stage.StatusActive)
		assertPostInvestigationStageStatus(t, entClient, freshPending, stage.StatusPending)
		assertPostInvestigationStageStatus(t, entClient, deleted, stage.StatusActive)
	})
}
