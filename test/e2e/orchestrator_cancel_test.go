package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Orchestrator cancellation test.
// SREOrchestrator dispatches 2 sub-agents, then all block.
// Session is cancelled via API, verifying cascading cancellation.
// ────────────────────────────────────────────────────────────

func TestE2E_OrchestratorCancellation(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Barrier: wait until all 3 agents (orchestrator iteration 2 + 2 sub-agents) are blocked.
	var blockedMu sync.Mutex
	blockedCount := 0
	allBlocked := make(chan struct{})
	onBlockCh := make(chan struct{}, 3)
	go func() {
		for range onBlockCh {
			blockedMu.Lock()
			blockedCount++
			if blockedCount >= 3 {
				close(allBlocked)
			}
			blockedMu.Unlock()
		}
	}()

	// Orchestrator iteration 1: dispatch both sub-agents.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-d1", Name: "dispatch_agent",
				Arguments: `{"name":"LogAnalyzer","task":"Check logs for errors"}`},
			&agent.ToolCallChunk{CallID: "orch-d2", Name: "dispatch_agent",
				Arguments: `{"name":"GeneralWorker","task":"Analyze alert data"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Orchestrator iteration 2: blocks until cancelled.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		BlockUntilCancelled: true,
		OnBlock:             onBlockCh,
	})

	// Sub-agents: both block until cancelled.
	llm.AddRouted("LogAnalyzer", LLMScriptEntry{
		BlockUntilCancelled: true,
		OnBlock:             onBlockCh,
	})
	llm.AddRouted("GeneralWorker", LLMScriptEntry{
		BlockUntilCancelled: true,
		OnBlock:             onBlockCh,
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "orchestrator-cancel")),
		WithLLMClient(llm),
		WithSessionTimeout(2*time.Minute),
	)

	// Connect WS.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-orchestrator-cancel", "Cancellation test alert")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait until all 3 agents are blocked.
	select {
	case <-allBlocked:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for all agents to block")
	}

	// Cancel the session.
	app.CancelSession(t, sessionID)

	// Wait for cancelled status.
	app.WaitForSessionStatus(t, sessionID, "cancelled")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "cancelled"
	}, 10*time.Second, "expected session.status cancelled WS event")

	// ── DB assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "cancelled", session["status"])

	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 1)
	assert.Equal(t, "cancelled", string(stages[0].Status))

	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 3, "expected orchestrator + 2 sub-agents")
	for _, e := range execs {
		assert.Equal(t, "cancelled", string(e.Status),
			"execution %s (%s) should be cancelled", e.ID, e.AgentName)
	}

	// ── WS event assertions ──
	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)
	AssertEventsInOrder(t, wsEvents, testdata.OrchestratorCancellationExpectedEvents)
}
