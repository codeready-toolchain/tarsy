package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubToolExecutor_Execute(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "k8s.get_pods", Description: "Get pods"},
	}
	executor := NewStubToolExecutor(tools)

	result, err := executor.Execute(context.Background(), ToolCall{
		ID:        "call-1",
		Name:      "k8s.get_pods",
		Arguments: `{"namespace": "default"}`,
	})

	require.NoError(t, err)
	assert.Equal(t, "call-1", result.CallID)
	assert.Equal(t, "k8s.get_pods", result.Name)
	assert.Contains(t, result.Content, "[stub]")
	assert.Contains(t, result.Content, "k8s.get_pods")
	assert.Contains(t, result.Content, "namespace")
	assert.False(t, result.IsError)
}

func TestStubToolExecutor_ListTools(t *testing.T) {
	t.Run("returns configured tools", func(t *testing.T) {
		tools := []ToolDefinition{
			{Name: "k8s.get_pods", Description: "Get pods"},
			{Name: "k8s.get_logs", Description: "Get logs"},
		}
		executor := NewStubToolExecutor(tools)

		listed, err := executor.ListTools(context.Background())
		require.NoError(t, err)
		assert.Len(t, listed, 2)
		assert.Equal(t, "k8s.get_pods", listed[0].Name)
	})

	t.Run("empty tools returns nil", func(t *testing.T) {
		executor := NewStubToolExecutor(nil)

		listed, err := executor.ListTools(context.Background())
		require.NoError(t, err)
		assert.Nil(t, listed)
	})
}
