package memory_test

import (
	stdsql "database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/codeready-toolchain/tarsy/test/util"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// addSessionSearchColumn adds the tsvector column to alert_sessions for tests.
func addSessionSearchColumn(t *testing.T, db *stdsql.DB) {
	t.Helper()
	_, err := db.ExecContext(t.Context(),
		`ALTER TABLE alert_sessions ADD COLUMN IF NOT EXISTS search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED`)
	require.NoError(t, err)
}

type sessionSearchTestEnv struct {
	svc       *memory.Service
	db        *stdsql.DB
	entClient *ent.Client
}

func newSessionSearchEnv(t *testing.T) *sessionSearchTestEnv {
	t.Helper()
	entClient, db := util.SetupTestDatabase(t)

	addMemorySearchColumns(t, db)
	addSessionSearchColumn(t, db)

	cfg := &config.MemoryConfig{
		Enabled: true,
		Embedding: config.EmbeddingConfig{
			Dimensions: 3,
		},
	}
	svc := memory.NewService(entClient, db, &fakeEmbedder{vec: []float32{1, 0, 0}}, cfg)

	return &sessionSearchTestEnv{svc: svc, db: db, entClient: entClient}
}

func (env *sessionSearchTestEnv) createSession(t *testing.T, alertData, alertType, status string, analysis *string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := env.db.ExecContext(t.Context(),
		`INSERT INTO alert_sessions (session_id, alert_data, agent_type, alert_type, chain_id, status, created_at, started_at)
		 VALUES ($1, $2, 'test', $3, 'test-chain', $4, NOW(), NOW())`,
		id, alertData, alertType, status)
	require.NoError(t, err)

	if analysis != nil {
		_, err = env.db.ExecContext(t.Context(),
			`UPDATE alert_sessions SET final_analysis = $1 WHERE session_id = $2`,
			*analysis, id)
		require.NoError(t, err)
	}
	return id
}

func TestSearchSessions_SingleTermMatch(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	analysis := "User john-doe created a suspicious deployment"
	env.createSession(t, "Alert: user john-doe triggered policy violation in namespace prod", "security", "completed", &analysis)
	env.createSession(t, "Alert: high CPU on node worker-1", "resource", "completed", nil)

	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "john-doe",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].AlertData, "john-doe")
	assert.Equal(t, "security", results[0].AlertType)
	require.NotNil(t, results[0].FinalAnalysis)
	assert.Contains(t, *results[0].FinalAnalysis, "john-doe")
}

func TestSearchSessions_MultiTermAND(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: user john-doe in namespace prod triggered OOMKill", "security", "completed", nil)
	env.createSession(t, "Alert: user jane-doe in namespace staging triggered OOMKill", "security", "completed", nil)

	// Both "john-doe" AND "prod" must match
	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "john-doe prod",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].AlertData, "john-doe")
	assert.Contains(t, results[0].AlertData, "prod")
}

func TestSearchSessions_NoMatches(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: high memory usage on node worker-1", "resource", "completed", nil)

	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "nonexistent-entity",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestSearchSessions_OnlyCompletedSessions(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: user alice in namespace test triggered restart", "security", "completed", nil)
	env.createSession(t, "Alert: user alice in namespace dev triggered restart", "security", "in_progress", nil)
	env.createSession(t, "Alert: user alice in namespace staging triggered restart", "security", "failed", nil)

	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "alice",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].AlertData, "namespace test")
}

func TestSearchSessions_AlertTypeFilter(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: nginx-proxy high latency in prod", "performance", "completed", nil)
	env.createSession(t, "Alert: nginx-proxy security vulnerability detected", "security", "completed", nil)

	alertType := "security"
	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query:     "nginx-proxy",
		AlertType: &alertType,
		Limit:     10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].AlertData, "security vulnerability")
}

func TestSearchSessions_DaysBackFilter(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	// Insert a recent session
	env.createSession(t, "Alert: coolify deployment failed in namespace apps", "deployment", "completed", nil)

	// Insert an old session (91 days ago)
	oldID := uuid.New().String()
	_, err := env.db.ExecContext(ctx,
		`INSERT INTO alert_sessions (session_id, alert_data, agent_type, alert_type, chain_id, status, created_at, started_at)
		 VALUES ($1, $2, 'test', 'deployment', 'test-chain', 'completed', $3, $3)`,
		oldID, "Alert: coolify deployment failed in namespace legacy", time.Now().AddDate(0, 0, -91))
	require.NoError(t, err)

	// Default 30 days — only recent session
	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query:    "coolify",
		DaysBack: 30,
		Limit:    10,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Contains(t, results[0].AlertData, "namespace apps")

	// 365 days — both sessions
	results, err = env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query:    "coolify",
		DaysBack: 365,
		Limit:    10,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSearchSessions_LimitApplied(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	for i := range 5 {
		env.createSession(t, "Alert: repeated issue with service myapp on node "+uuid.New().String()[:8],
			"performance", "completed", nil)
		_ = i
	}

	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "myapp",
		Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSearchSessions_OrderedByCreatedAtDesc(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	// Insert sessions with different timestamps
	id1 := uuid.New().String()
	_, err := env.db.ExecContext(ctx,
		`INSERT INTO alert_sessions (session_id, alert_data, agent_type, alert_type, chain_id, status, created_at, started_at)
		 VALUES ($1, 'Alert: qemu-kvm high CPU on host-A', 'test', 'resource', 'test-chain', 'completed', $2, $2)`,
		id1, time.Now().Add(-2*time.Hour))
	require.NoError(t, err)

	id2 := uuid.New().String()
	_, err = env.db.ExecContext(ctx,
		`INSERT INTO alert_sessions (session_id, alert_data, agent_type, alert_type, chain_id, status, created_at, started_at)
		 VALUES ($1, 'Alert: qemu-kvm high CPU on host-B', 'test', 'resource', 'test-chain', 'completed', $2, $2)`,
		id2, time.Now().Add(-1*time.Hour))
	require.NoError(t, err)

	results, err := env.svc.SearchSessions(ctx, memory.SessionSearchParams{
		Query: "qemu-kvm",
		Limit: 10,
	})
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, id2, results[0].SessionID, "most recent session should be first")
	assert.Equal(t, id1, results[1].SessionID)
}

func sessionSearchToolCall(t *testing.T, query string, limit int) agent.ToolCall {
	t.Helper()
	args := map[string]any{"query": query}
	if limit > 0 {
		args["limit"] = limit
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	return agent.ToolCall{
		ID:        "call-ss-1",
		Name:      memory.ToolSearchPastSessions,
		Arguments: string(b),
	}
}

func TestToolExecutor_SessionSearch_ReturnsSummarizationRequest(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	analysis := "User john-doe created an unauthorized deployment"
	env.createSession(t, "Alert: user john-doe triggered policy violation", "security", "completed", &analysis)

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil)

	result, err := te.Execute(ctx, sessionSearchToolCall(t, "john-doe", 0))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "call-ss-1", result.CallID)

	require.NotNil(t, result.RequiredSummarization, "matched sessions should request summarization")
	assert.Contains(t, result.RequiredSummarization.SystemPrompt, "summarization assistant for TARSy")
	assert.Contains(t, result.RequiredSummarization.UserPrompt, "john-doe")
	assert.Contains(t, result.RequiredSummarization.UserPrompt, "unauthorized deployment")
	require.NotNil(t, result.RequiredSummarization.TransformResult, "should have a result transformer")
	transformed := result.RequiredSummarization.TransformResult("test summary")
	assert.Contains(t, transformed, "<historical_context>")
	assert.Contains(t, transformed, "</historical_context>")
	assert.Contains(t, transformed, "HISTORICAL data from past sessions")
	assert.Contains(t, transformed, "test summary")
	assert.Contains(t, result.Content, "john-doe")
}

func TestToolExecutor_SessionSearch_NoMatches(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: high CPU on worker node", "resource", "completed", nil)

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil)

	result, err := te.Execute(ctx, sessionSearchToolCall(t, "nonexistent-entity", 0))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "No matching sessions found")
	assert.Nil(t, result.RequiredSummarization, "no-match results should not request summarization")
}

func TestToolExecutor_SessionSearch_WithAlertTypeParam(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	env.createSession(t, "Alert: nginx-proxy latency spike in prod", "performance", "completed", nil)
	env.createSession(t, "Alert: nginx-proxy CVE detected", "security", "completed", nil)

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil)

	args, err := json.Marshal(map[string]any{
		"query":      "nginx-proxy",
		"alert_type": "security",
	})
	require.NoError(t, err)

	result, err := te.Execute(ctx, agent.ToolCall{
		ID:        "call-at-1",
		Name:      memory.ToolSearchPastSessions,
		Arguments: string(args),
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.NotNil(t, result.RequiredSummarization)
	assert.Contains(t, result.RequiredSummarization.UserPrompt, "CVE detected")
	assert.NotContains(t, result.RequiredSummarization.UserPrompt, "latency spike")
}

func TestToolExecutor_SessionSearch_LimitClampedToMax(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	for range 12 {
		env.createSession(t, "Alert: repeated issue with service webapp on node "+uuid.New().String()[:8],
			"performance", "completed", nil)
	}

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil)

	args, err := json.Marshal(map[string]any{
		"query": "webapp",
		"limit": 999,
	})
	require.NoError(t, err)

	result, err := te.Execute(ctx, agent.ToolCall{
		ID:        "call-lc-1",
		Name:      memory.ToolSearchPastSessions,
		Arguments: string(args),
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.NotNil(t, result.RequiredSummarization)
	assert.Contains(t, result.RequiredSummarization.UserPrompt, "Matched sessions (10)")
}

// createChatStage creates a chat stage with an agent execution and timeline
// events for user_question and final_analysis. Returns the stage ID.
func (env *sessionSearchTestEnv) createChatStage(t *testing.T, sessionID string, stageIndex int, question, answer string) string {
	t.Helper()
	ctx := t.Context()

	stageID := uuid.New().String()
	stg, err := env.entClient.Stage.Create().
		SetID(stageID).
		SetSessionID(sessionID).
		SetStageName("Chat").
		SetStageIndex(stageIndex).
		SetExpectedAgentCount(1).
		SetStageType("chat").
		SetStatus("completed").
		SetStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	execID := uuid.New().String()
	exec, err := env.entClient.AgentExecution.Create().
		SetID(execID).
		SetStage(stg).
		SetSessionID(sessionID).
		SetAgentName("ChatAgent").
		SetAgentIndex(1).
		SetStatus("completed").
		SetLlmBackend("test").
		Save(ctx)
	require.NoError(t, err)

	now := time.Now()
	_, err = env.entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetExecutionID(execID).
		SetAgentExecution(exec).
		SetSequenceNumber(1).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetEventType("user_question").
		SetStatus("completed").
		SetContent(question).
		Save(ctx)
	require.NoError(t, err)

	_, err = env.entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetExecutionID(execID).
		SetAgentExecution(exec).
		SetSequenceNumber(2).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetEventType("final_analysis").
		SetStatus("completed").
		SetContent(answer).
		Save(ctx)
	require.NoError(t, err)

	return stageID
}

func TestFetchChatExchanges_WithChatStages(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	sessionID := env.createSession(t, "Alert: user alice triggered policy violation", "security", "completed", nil)
	env.createChatStage(t, sessionID, 1, "What namespace was alice active in?", "alice was active in namespace prod")
	env.createChatStage(t, sessionID, 2, "Any related deployments?", "No deployments found in the last 24h")

	exchanges, err := env.svc.FetchChatExchanges(ctx, []string{sessionID})
	require.NoError(t, err)

	require.Len(t, exchanges[sessionID], 2)
	assert.Equal(t, "What namespace was alice active in?", exchanges[sessionID][0].Question)
	assert.Equal(t, "alice was active in namespace prod", exchanges[sessionID][0].Answer)
	assert.Equal(t, "Any related deployments?", exchanges[sessionID][1].Question)
	assert.Equal(t, "No deployments found in the last 24h", exchanges[sessionID][1].Answer)
}

func TestFetchChatExchanges_NoChatStages(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	sessionID := env.createSession(t, "Alert: high CPU on node worker-1", "resource", "completed", nil)

	exchanges, err := env.svc.FetchChatExchanges(ctx, []string{sessionID})
	require.NoError(t, err)
	assert.Empty(t, exchanges[sessionID])
}

func TestFetchChatExchanges_EmptySessionIDs(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	exchanges, err := env.svc.FetchChatExchanges(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, exchanges)
}

func TestFetchChatExchanges_MultipleSessionsMixed(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	sess1 := env.createSession(t, "Alert: user bob triggered restart", "security", "completed", nil)
	env.createChatStage(t, sess1, 1, "What pod restarted?", "Pod nginx-abc restarted 3 times")

	sess2 := env.createSession(t, "Alert: high memory on node-5", "resource", "completed", nil)
	// sess2 has no chat stages

	exchanges, err := env.svc.FetchChatExchanges(ctx, []string{sess1, sess2})
	require.NoError(t, err)

	require.Len(t, exchanges[sess1], 1)
	assert.Equal(t, "What pod restarted?", exchanges[sess1][0].Question)
	assert.Empty(t, exchanges[sess2])
}

func TestToolExecutor_SessionSearch_IncludesChatExchanges(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	analysis := "User john-doe deployed unauthorized workload"
	sessionID := env.createSession(t, "Alert: user john-doe triggered policy violation", "security", "completed", &analysis)
	env.createChatStage(t, sessionID, 1, "What namespace was affected?", "namespace prod was affected with 3 unauthorized pods")

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil)

	result, err := te.Execute(ctx, sessionSearchToolCall(t, "john-doe", 0))
	require.NoError(t, err)
	assert.False(t, result.IsError)
	require.NotNil(t, result.RequiredSummarization)

	prompt := result.RequiredSummarization.UserPrompt
	assert.Contains(t, prompt, "john-doe")
	assert.Contains(t, prompt, "unauthorized workload")
	assert.Contains(t, prompt, "Follow-up conversations (1):")
	assert.Contains(t, prompt, "Q1: What namespace was affected?")
	assert.Contains(t, prompt, "A1: namespace prod was affected with 3 unauthorized pods")
}

func TestFetchChatExchanges_CappedAtMaxPerSession(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	sessionID := env.createSession(t, "Alert: user eve triggered many follow-ups", "security", "completed", nil)
	for i := range 7 {
		env.createChatStage(t, sessionID, i+1,
			"Question "+uuid.New().String()[:8],
			"Answer "+uuid.New().String()[:8])
	}

	exchanges, err := env.svc.FetchChatExchanges(ctx, []string{sessionID})
	require.NoError(t, err)
	assert.Len(t, exchanges[sessionID], 5, "should cap at maxChatExchangesPerSession")
}

func TestFetchChatExchanges_QuestionOnlyNoAnswer(t *testing.T) {
	env := newSessionSearchEnv(t)
	ctx := t.Context()

	sessionID := env.createSession(t, "Alert: user dave triggered restart", "security", "completed", nil)

	// Create a chat stage with only a user_question event (no final_analysis).
	stageID := uuid.New().String()
	stg, err := env.entClient.Stage.Create().
		SetID(stageID).
		SetSessionID(sessionID).
		SetStageName("Chat").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStageType("chat").
		SetStatus("completed").
		SetStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)

	execID := uuid.New().String()
	exec, err := env.entClient.AgentExecution.Create().
		SetID(execID).
		SetStage(stg).
		SetSessionID(sessionID).
		SetAgentName("ChatAgent").
		SetAgentIndex(1).
		SetStatus("completed").
		SetLlmBackend("test").
		Save(ctx)
	require.NoError(t, err)

	now := time.Now()
	_, err = env.entClient.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetExecutionID(execID).
		SetAgentExecution(exec).
		SetSequenceNumber(1).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetEventType("user_question").
		SetStatus("completed").
		SetContent("What happened to the pod?").
		Save(ctx)
	require.NoError(t, err)

	exchanges, err := env.svc.FetchChatExchanges(ctx, []string{sessionID})
	require.NoError(t, err)

	require.Len(t, exchanges[sessionID], 1)
	assert.Equal(t, "What happened to the pod?", exchanges[sessionID][0].Question)
	assert.Empty(t, exchanges[sessionID][0].Answer, "answer should be empty when no final_analysis event exists")
}
