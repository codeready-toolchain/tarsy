package memory

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsMemoryTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"recall_past_investigations", true},
		{"search_past_sessions", true},
		{"load_skill", false},
		{"dispatch_agent", false},
		{"kubernetes.get_pods", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsMemoryTool(tt.name))
		})
	}
}

func TestToolExecutor_ListTools_PrependToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server1.read_file", Description: "Reads a file"},
		{Name: "server1.write_file", Description: "Writes a file"},
	})
	svc := &Service{}
	te := NewToolExecutor(inner, svc, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 4)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.Equal(t, ToolSearchPastSessions, tools[1].Name)
	assert.Equal(t, "server1.read_file", tools[2].Name)
	assert.Equal(t, "server1.write_file", tools[3].Name)
}

func TestToolExecutor_ListTools_NilService(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Empty(t, tools, "no memory tools when service is nil")
}

func TestToolExecutor_ListTools_NilInner(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 2)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.Equal(t, ToolSearchPastSessions, tools[1].Name)
}

func TestToolExecutor_ListTools_DeduplicatesInner(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: ToolRecallPastInvestigations, Description: "should be filtered out"},
		{Name: ToolSearchPastSessions, Description: "should also be filtered out"},
		{Name: "server1.read_file", Description: "Reads a file"},
	})
	svc := &Service{}
	te := NewToolExecutor(inner, svc, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 3)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.NotEqual(t, "should be filtered out", tools[0].Description)
	assert.Equal(t, ToolSearchPastSessions, tools[1].Name)
	assert.NotEqual(t, "should also be filtered out", tools[1].Description)
}

func TestToolExecutor_Execute_DelegatesToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor(nil)
	te := NewToolExecutor(inner, nil, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      "server1.read_file",
		Arguments: `{"path": "/etc/config"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "call-1", result.CallID)
	assert.False(t, result.IsError)
}

func TestToolExecutor_Execute_UnknownToolNilInner(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:   "call-1",
		Name: "nonexistent_tool",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unknown tool")
}

func TestToolExecutor_Execute_RecallNilService(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolRecallPastInvestigations,
		Arguments: `{"query": "test"}`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "memory service is not available")
}

func TestToolExecutor_Execute_ColonPrefixedRecallNilService(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      "vertex:" + ToolRecallPastInvestigations,
		Arguments: `{"query": "test"}`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "memory service is not available")
}

func TestToolExecutor_Execute_RecallEmptyQuery(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolRecallPastInvestigations,
		Arguments: `{"query": ""}`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "'query' is required")
}

func TestToolExecutor_Execute_RecallInvalidJSON(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolRecallPastInvestigations,
		Arguments: `not-json`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "invalid arguments")
}

func TestToolExecutor_Close_DelegatesToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor(nil)
	te := NewToolExecutor(inner, nil, "", "default", nil)
	assert.NoError(t, te.Close())
}

func TestToolExecutor_Close_NilInner(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)
	assert.NoError(t, te.Close())
}

func TestToolExecutor_ListTools_RecallToolDefinition(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 2)

	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.Contains(t, tools[0].Description, "Search distilled knowledge from past investigations")
	assert.Contains(t, tools[0].ParametersSchema, `"query"`)
	assert.Contains(t, tools[0].ParametersSchema, `"limit"`)
}

func TestToolExecutor_Execute_SessionSearchNilService(t *testing.T) {
	te := NewToolExecutor(nil, nil, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolSearchPastSessions,
		Arguments: `{"query": "test"}`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "memory service is not available")
}

func TestToolExecutor_Execute_SessionSearchEmptyQuery(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolSearchPastSessions,
		Arguments: `{"query": ""}`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "'query' is required")
}

func TestToolExecutor_Execute_SessionSearchInvalidJSON(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolSearchPastSessions,
		Arguments: `not-json`,
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "invalid arguments")
}

func TestToolExecutor_ListTools_SearchSessionsToolDefinition(t *testing.T) {
	svc := &Service{}
	te := NewToolExecutor(nil, svc, "", "default", nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 2)

	assert.Equal(t, ToolSearchPastSessions, tools[1].Name)
	assert.Contains(t, tools[1].Description, "Search past investigation sessions")
	assert.Contains(t, tools[1].ParametersSchema, `"query"`)
	assert.Contains(t, tools[1].ParametersSchema, `"alert_type"`)
	assert.Contains(t, tools[1].ParametersSchema, `"days_back"`)
	assert.Contains(t, tools[1].ParametersSchema, `"limit"`)
}

func TestBuildSessionSummarizationPrompt(t *testing.T) {
	analysis := "Root cause: unauthorized deployment"
	quality := "accurate"
	feedback := "Good investigation, thorough evidence gathering"

	sessions := []SessionSearchResult{
		{
			SessionID:             "sess-001",
			AlertData:             "Alert: user john-doe triggered policy violation",
			AlertType:             "security",
			FinalAnalysis:         &analysis,
			QualityRating:         &quality,
			InvestigationFeedback: &feedback,
			ChatExchanges: []ChatExchange{
				{Question: "What namespace was john-doe active in?", Answer: "john-doe was active in namespace prod with 3 failed deployments"},
				{Question: "Was this correlated with the outage?", Answer: "No direct correlation found"},
			},
			CreatedAt: time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC),
		},
		{
			SessionID: "sess-002",
			AlertData: "Alert: user john-doe high CPU in prod",
			AlertType: "resource",
			CreatedAt: time.Date(2026, 3, 14, 8, 0, 0, 0, time.UTC),
		},
	}

	prompt := buildSessionSummarizationPrompt("john-doe", sessions)

	assert.Contains(t, prompt, "Search query: john-doe")
	assert.Contains(t, prompt, "Matched sessions (2)")

	assert.Contains(t, prompt, "Session 1 (ID: sess-001, 2026-03-15 10:30)")
	assert.Contains(t, prompt, "Alert type: security")
	assert.Contains(t, prompt, "user john-doe triggered policy violation")
	assert.Contains(t, prompt, "Root cause: unauthorized deployment")
	assert.Contains(t, prompt, "Quality assessment: accurate")
	assert.Contains(t, prompt, "Human review feedback: Good investigation")

	assert.Contains(t, prompt, "Follow-up conversations (2):")
	assert.Contains(t, prompt, "Q1: What namespace was john-doe active in?")
	assert.Contains(t, prompt, "A1: john-doe was active in namespace prod with 3 failed deployments")
	assert.Contains(t, prompt, "Q2: Was this correlated with the outage?")
	assert.Contains(t, prompt, "A2: No direct correlation found")

	assert.Contains(t, prompt, "Session 2 (ID: sess-002, 2026-03-14 08:00)")
	assert.Contains(t, prompt, "Alert type: resource")
	assert.Contains(t, prompt, "(none recorded)")
	assert.NotContains(t, prompt, "Quality assessment: \nHuman")
}

func TestBuildSessionSummarizationPrompt_NilOptionalFields(t *testing.T) {
	sessions := []SessionSearchResult{
		{
			SessionID: "sess-003",
			AlertData: "Alert: pod restart",
			AlertType: "infra",
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	prompt := buildSessionSummarizationPrompt("pod", sessions)

	assert.Contains(t, prompt, "(none recorded)")
	assert.NotContains(t, prompt, "Quality assessment:")
	assert.NotContains(t, prompt, "Human review feedback:")
	assert.NotContains(t, prompt, "Follow-up conversations")
}

func TestBuildSessionSummarizationPrompt_EmptyChatAnswer(t *testing.T) {
	sessions := []SessionSearchResult{
		{
			SessionID: "sess-004",
			AlertData: "Alert: test",
			AlertType: "test",
			ChatExchanges: []ChatExchange{
				{Question: "Any follow-up?", Answer: ""},
			},
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	prompt := buildSessionSummarizationPrompt("test", sessions)

	assert.Contains(t, prompt, "Q1: Any follow-up?")
	assert.Contains(t, prompt, "A1: (no response recorded)")
}

func TestToolExecutor_ExcludeIDs(t *testing.T) {
	excludeIDs := map[string]struct{}{
		"mem-1": {},
		"mem-3": {},
	}
	te := NewToolExecutor(nil, nil, "", "default", excludeIDs)

	// Verify exclude IDs are stored
	assert.Len(t, te.excludeIDs, 2)
	_, has1 := te.excludeIDs["mem-1"]
	assert.True(t, has1)
	_, has3 := te.excludeIDs["mem-3"]
	assert.True(t, has3)
}
