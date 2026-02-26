package orchestrator

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResultCollector_TryDrainResult(t *testing.T) {
	runner := &SubAgentRunner{
		resultsCh: make(chan *SubAgentResult, 1),
		closeCh:   make(chan struct{}),
		pending:   1,
	}
	runner.resultsCh <- &SubAgentResult{
		ExecutionID: "exec-1",
		AgentName:   "LogAnalyzer",
		Status:      agent.ExecutionStatusCompleted,
		Result:      "Found 42 errors",
	}

	collector := NewResultCollector(runner)

	msg, ok := collector.TryDrainResult()
	require.True(t, ok)
	assert.Equal(t, agent.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "[Sub-agent completed]")
	assert.Contains(t, msg.Content, "LogAnalyzer")
	assert.Contains(t, msg.Content, "Found 42 errors")
}

func TestResultCollector_TryDrainResult_Empty(t *testing.T) {
	runner := &SubAgentRunner{
		resultsCh: make(chan *SubAgentResult, 1),
		closeCh:   make(chan struct{}),
	}

	collector := NewResultCollector(runner)

	msg, ok := collector.TryDrainResult()
	assert.False(t, ok)
	assert.Empty(t, msg.Content)
}

func TestResultCollector_WaitForResult(t *testing.T) {
	runner := &SubAgentRunner{
		resultsCh: make(chan *SubAgentResult, 1),
		closeCh:   make(chan struct{}),
		pending:   1,
	}

	go func() {
		runner.resultsCh <- &SubAgentResult{
			ExecutionID: "exec-2",
			AgentName:   "MetricChecker",
			Status:      agent.ExecutionStatusFailed,
			Error:       "connection refused",
		}
	}()

	collector := NewResultCollector(runner)

	msg, err := collector.WaitForResult(context.Background())
	require.NoError(t, err)
	assert.Equal(t, agent.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "[Sub-agent failed]")
	assert.Contains(t, msg.Content, "connection refused")
}

func TestResultCollector_WaitForResult_ContextCancelled(t *testing.T) {
	runner := &SubAgentRunner{
		resultsCh: make(chan *SubAgentResult, 1),
		closeCh:   make(chan struct{}),
		pending:   1,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	collector := NewResultCollector(runner)

	_, err := collector.WaitForResult(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestResultCollector_HasPending(t *testing.T) {
	runner := &SubAgentRunner{
		resultsCh: make(chan *SubAgentResult, 1),
		closeCh:   make(chan struct{}),
	}

	collector := NewResultCollector(runner)

	assert.False(t, collector.HasPending())

	atomic.StoreInt32(&runner.pending, 2)
	assert.True(t, collector.HasPending())
}
