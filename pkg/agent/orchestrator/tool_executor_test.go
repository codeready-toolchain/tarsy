package orchestrator

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompositeToolExecutor_ListTools_CombinesMCPAndOrchestration(t *testing.T) {
	mcpStub := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server1.read_file", Description: "Reads a file"},
		{Name: "server1.write_file", Description: "Writes a file"},
	})
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)

	c := NewCompositeToolExecutor(mcpStub, runner, registry)
	tools, err := c.ListTools(context.Background())
	require.NoError(t, err)

	// Orchestration tools come first, then MCP tools
	assert.Len(t, tools, len(orchestrationTools)+2)
	assert.Equal(t, ToolDispatchAgent, tools[0].Name)
	assert.Equal(t, ToolCancelAgent, tools[1].Name)
	assert.Equal(t, ToolListAgents, tools[2].Name)
	assert.Equal(t, "server1.read_file", tools[3].Name)
	assert.Equal(t, "server1.write_file", tools[4].Name)
}

func TestCompositeToolExecutor_ListTools_NilMCPExecutor(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)

	c := NewCompositeToolExecutor(nil, runner, registry)
	tools, err := c.ListTools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, len(orchestrationTools))
}

func TestCompositeToolExecutor_Execute_DispatchAgent(t *testing.T) {
	runner := newMinimalRunner(5)
	// Pre-populate an execution to verify dispatch goes through the runner.
	// Since runner has no DB deps, dispatch will fail. We assert the error is
	// returned as a non-fatal tool result (IsError=true), not a Go error.
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	args, _ := json.Marshal(map[string]string{"name": "TestAgent", "task": "do something"})
	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID:        "call-1",
		Name:      ToolDispatchAgent,
		Arguments: string(args),
	})
	require.NoError(t, err)
	assert.Equal(t, "call-1", result.CallID)
	// Runner dispatches to the registry, but StageService is nil.
	// The dispatch goes through registry validation first. "TestAgent" is in the
	// registry that was built from a map containing "TestAgent".
	// But with a nil StageService, it will fail at the CreateAgentExecution step.
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "dispatch failed")
}

func TestCompositeToolExecutor_Execute_DispatchAgent_ValidationError(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	t.Run("missing args", func(t *testing.T) {
		args, _ := json.Marshal(map[string]string{"name": "TestAgent"})
		result, err := c.Execute(context.Background(), agent.ToolCall{
			ID: "call-1", Name: ToolDispatchAgent, Arguments: string(args),
		})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "'task' are required")
	})

	t.Run("bad json", func(t *testing.T) {
		result, err := c.Execute(context.Background(), agent.ToolCall{
			ID: "call-2", Name: ToolDispatchAgent, Arguments: "not json",
		})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "invalid arguments")
	})
}

func TestCompositeToolExecutor_Execute_CancelAgent(t *testing.T) {
	runner := newMinimalRunner(5)

	// Pre-populate an active execution in the runner
	runner.mu.Lock()
	runner.executions["exec-42"] = &subAgentExecution{
		executionID: "exec-42",
		agentName:   "TestAgent",
		status:      agent.ExecutionStatusActive,
		cancel:      func() {},
		done:        make(chan struct{}),
	}
	runner.mu.Unlock()

	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	args, _ := json.Marshal(map[string]string{"execution_id": "exec-42"})
	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-3", Name: ToolCancelAgent, Arguments: string(args),
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "cancellation requested")
}

func TestCompositeToolExecutor_Execute_CancelAgent_ValidationError(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	t.Run("missing execution_id", func(t *testing.T) {
		result, err := c.Execute(context.Background(), agent.ToolCall{
			ID: "call-v1", Name: ToolCancelAgent, Arguments: `{}`,
		})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "'execution_id' is required")
	})

	t.Run("bad json", func(t *testing.T) {
		result, err := c.Execute(context.Background(), agent.ToolCall{
			ID: "call-v2", Name: ToolCancelAgent, Arguments: "not json",
		})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, result.Content, "invalid arguments")
	})
}

func TestCompositeToolExecutor_Execute_CancelAgent_NotFound(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	args, _ := json.Marshal(map[string]string{"execution_id": "nonexistent"})
	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-4", Name: ToolCancelAgent, Arguments: string(args),
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "cancel failed")
}

func TestCompositeToolExecutor_Execute_ListAgents_Empty(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-5", Name: ToolListAgents, Arguments: "{}",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "No sub-agents dispatched")
}

func TestCompositeToolExecutor_Execute_ListAgents_WithEntries(t *testing.T) {
	runner := newMinimalRunner(5)
	runner.mu.Lock()
	runner.executions["e1"] = &subAgentExecution{
		executionID: "e1", agentName: "AgentA", task: "task A",
		status: agent.ExecutionStatusActive,
	}
	runner.mu.Unlock()

	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-6", Name: ToolListAgents, Arguments: "{}",
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "AgentA")
	assert.Contains(t, result.Content, "active")
}

func TestCompositeToolExecutor_Execute_MCPTool(t *testing.T) {
	mcpStub := agent.NewStubToolExecutor([]agent.ToolDefinition{
		{Name: "server.read_file"},
	})
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(mcpStub, runner, registry)

	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-7", Name: "server.read_file", Arguments: `{"path": "/tmp/foo"}`,
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, "server.read_file")
}

func TestCompositeToolExecutor_Execute_UnknownTool_NilMCP(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	result, err := c.Execute(context.Background(), agent.ToolCall{
		ID: "call-8", Name: "nonexistent.tool", Arguments: "{}",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "unknown tool")
}

func TestCompositeToolExecutor_Close_CancelsAndWaits(t *testing.T) {
	runner := newMinimalRunner(5)

	cancelled := int32(0)
	doneCh := make(chan struct{})
	runner.mu.Lock()
	runner.executions["e1"] = &subAgentExecution{
		executionID: "e1",
		status:      agent.ExecutionStatusActive,
		cancel: func() {
			atomic.AddInt32(&cancelled, 1)
			close(doneCh)
		},
		done: doneCh,
	}
	runner.mu.Unlock()

	registry := config.BuildSubAgentRegistry(nil)
	mcpStub := agent.NewStubToolExecutor(nil)
	c := NewCompositeToolExecutor(mcpStub, runner, registry)

	err := c.Close()
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&cancelled), "cancel should have been called")
}

func TestCompositeToolExecutor_Close_NilMCPExecutor(t *testing.T) {
	runner := newMinimalRunner(5)
	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	err := c.Close()
	require.NoError(t, err)
}

func TestCompositeToolExecutor_Close_Timeout(t *testing.T) {
	runner := newMinimalRunner(5)
	// Create an execution that never completes
	runner.mu.Lock()
	runner.executions["stuck"] = &subAgentExecution{
		executionID: "stuck",
		status:      agent.ExecutionStatusActive,
		cancel:      func() {},
		done:        make(chan struct{}), // never closed
	}
	runner.mu.Unlock()

	registry := config.BuildSubAgentRegistry(nil)
	c := NewCompositeToolExecutor(nil, runner, registry)

	// Override the close timeout to be short for testing purposes.
	// Close() uses a hard-coded 30s timeout internally, so we test that
	// it at least doesn't panic and returns within a reasonable time.
	// For a more thorough test, we'd need to inject the timeout, but
	// that's not worth the API complexity.
	done := make(chan struct{})
	go func() {
		_ = c.Close()
		close(done)
	}()

	select {
	case <-done:
		// Close returned (may have timed out internally, that's fine)
	case <-time.After(35 * time.Second):
		t.Fatal("Close did not return within 35 seconds")
	}
}
