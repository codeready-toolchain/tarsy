package memory_test

import (
	stdsql "database/sql"
	"encoding/json"
	"testing"
	"time"

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
	svc *memory.Service
	db  *stdsql.DB
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

	return &sessionSearchTestEnv{svc: svc, db: db}
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

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil, nil, nil)

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

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil, nil, nil)

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

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil, nil, nil)

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

	te := memory.NewToolExecutor(nil, env.svc, "", "default", nil, nil, nil)

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
