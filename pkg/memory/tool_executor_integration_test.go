package memory_test

import (
	"encoding/json"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recallToolCall(t *testing.T, query string, limit int) agent.ToolCall {
	t.Helper()
	args := map[string]any{"query": query}
	if limit > 0 {
		args["limit"] = limit
	}
	b, err := json.Marshal(args)
	require.NoError(t, err)
	return agent.ToolCall{
		ID:        "call-1",
		Name:      memory.ToolRecallPastInvestigations,
		Arguments: string(b),
	}
}

func TestToolExecutor_Recall_FormattedResults(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Check PgBouncer health first", Category: "procedural", Valence: "positive"},
			{Content: "OOMKill uses working_set_bytes", Category: "episodic", Valence: "neutral"},
		}})
	require.NoError(t, err)

	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, nil)
	result, err := te.Execute(ctx, recallToolCall(t, "check health", 10))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Equal(t, "call-1", result.CallID)
	assert.Contains(t, result.Content, "Found 2 relevant memories")
	assert.Contains(t, result.Content, "[procedural, positive, learned just now] Check PgBouncer health first")
	assert.Contains(t, result.Content, "[episodic, neutral, learned just now] OOMKill uses working_set_bytes")
}

func TestToolExecutor_Recall_ExcludesInjectedIDs(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Memory A", Category: "procedural", Valence: "positive"},
			{Content: "Memory B", Category: "semantic", Valence: "neutral"},
		}})
	require.NoError(t, err)

	all, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	require.Len(t, all, 2)

	excludeIDs := map[string]struct{}{all[0].ID: {}}
	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, excludeIDs)

	result, err := te.Execute(ctx, recallToolCall(t, "anything", 10))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "Found 1 relevant memories")
	assert.Contains(t, result.Content, all[1].Content)
	assert.NotContains(t, result.Content, all[0].Content)
}

func TestToolExecutor_Recall_AllExcluded(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Only memory", Category: "procedural", Valence: "positive"},
		}})
	require.NoError(t, err)

	all, err := svc.FindSimilar(ctx, "default", "anything", 10)
	require.NoError(t, err)
	require.Len(t, all, 1)

	excludeIDs := map[string]struct{}{all[0].ID: {}}
	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, excludeIDs)

	result, err := te.Execute(ctx, recallToolCall(t, "anything", 10))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "No relevant memories found")
}

func TestToolExecutor_Recall_LimitApplied(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Memory 1", Category: "procedural", Valence: "positive"},
			{Content: "Memory 2", Category: "procedural", Valence: "positive"},
			{Content: "Memory 3", Category: "procedural", Valence: "positive"},
			{Content: "Memory 4", Category: "procedural", Valence: "positive"},
			{Content: "Memory 5", Category: "procedural", Valence: "positive"},
		}})
	require.NoError(t, err)

	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, nil)

	result, err := te.Execute(ctx, recallToolCall(t, "test", 2))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "Found 2 relevant memories")
}

func TestToolExecutor_Recall_NoMemoriesInDB(t *testing.T) {
	svc, _ := newTestService(t, []float32{1, 0, 0})

	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, nil)

	result, err := te.Execute(t.Context(), recallToolCall(t, "nothing matches", 0))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "No relevant memories found")
}

func TestToolExecutor_Recall_DefaultLimit(t *testing.T) {
	svc, sessionID := newTestService(t, []float32{1, 0, 0})
	ctx := t.Context()

	// Seed 3 memories, request with no limit → default 10 → returns all 3.
	err := svc.ApplyReflectorActions(ctx, "default", sessionID, nil, nil, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "A", Category: "procedural", Valence: "positive"},
			{Content: "B", Category: "semantic", Valence: "neutral"},
			{Content: "C", Category: "episodic", Valence: "negative"},
		}})
	require.NoError(t, err)

	te := memory.NewToolExecutor(nil, svc, "default", nil, nil, nil)

	result, err := te.Execute(ctx, recallToolCall(t, "test", 0))
	require.NoError(t, err)

	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "Found 3 relevant memories")
}

func TestToolExecutor_Recall_DelegatesNonRecallToInner(t *testing.T) {
	svc, _ := newTestService(t, []float32{1, 0, 0})
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server1.read_file", Description: "Reads a file"},
	})
	te := memory.NewToolExecutor(inner, svc, "default", nil, nil, nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-2",
		Name:      "server1.read_file",
		Arguments: `{"path": "/etc/hosts"}`,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "call-2", result.CallID)
}
