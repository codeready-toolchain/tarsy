package memory

import (
	"testing"

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
	te := NewToolExecutor(inner, nil, "default", nil, nil, nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 3)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.Equal(t, "server1.read_file", tools[1].Name)
	assert.Equal(t, "server1.write_file", tools[2].Name)
}

func TestToolExecutor_ListTools_NilInner(t *testing.T) {
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 1)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
}

func TestToolExecutor_ListTools_DeduplicatesInner(t *testing.T) {
	inner := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: ToolRecallPastInvestigations, Description: "should be filtered out"},
		{Name: "server1.read_file", Description: "Reads a file"},
	})
	te := NewToolExecutor(inner, nil, "default", nil, nil, nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)

	assert.Len(t, tools, 2)
	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.NotEqual(t, "should be filtered out", tools[0].Description)
}

func TestToolExecutor_Execute_DelegatesToInner(t *testing.T) {
	inner := agent.NewStubToolExecutor(nil)
	te := NewToolExecutor(inner, nil, "default", nil, nil, nil)

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
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)

	result, err := te.Execute(t.Context(), agent.ToolCall{
		ID:   "call-1",
		Name: "nonexistent_tool",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unknown tool")
}

func TestToolExecutor_Execute_RecallEmptyQuery(t *testing.T) {
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)

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
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)

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
	te := NewToolExecutor(inner, nil, "default", nil, nil, nil)
	assert.NoError(t, te.Close())
}

func TestToolExecutor_Close_NilInner(t *testing.T) {
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)
	assert.NoError(t, te.Close())
}

func TestToolExecutor_ListTools_RecallToolDefinition(t *testing.T) {
	te := NewToolExecutor(nil, nil, "default", nil, nil, nil)

	tools, err := te.ListTools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 1)

	assert.Equal(t, ToolRecallPastInvestigations, tools[0].Name)
	assert.Contains(t, tools[0].Description, "Search learnings from past investigations")
	assert.Contains(t, tools[0].ParametersSchema, `"query"`)
	assert.Contains(t, tools[0].ParametersSchema, `"limit"`)
}

func TestToolExecutor_ExcludeIDs(t *testing.T) {
	excludeIDs := map[string]struct{}{
		"mem-1": {},
		"mem-3": {},
	}
	te := NewToolExecutor(nil, nil, "default", nil, nil, excludeIDs)

	// Verify exclude IDs are stored
	assert.Len(t, te.excludeIDs, 2)
	_, has1 := te.excludeIDs["mem-1"]
	assert.True(t, has1)
	_, has3 := te.excludeIDs["mem-3"]
	assert.True(t, has3)
}
