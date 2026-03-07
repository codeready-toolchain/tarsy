package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Action Chain test — two-stage chain with investigation + action stage:
//  1. investigation (Investigator, google-native) — tool call + final answer
//  2. remediation   (Remediator, action type, google-native) — tool call + final answer
//  + Executive summary
//
// Verifies:
//   - action stage_type persisted in DB and propagated in stage.status events
//   - safety preamble injected into action agent's system prompt
//   - investigation context flows into action stage
//   - exec summary receives action stage's amended report
// ────────────────────────────────────────────────────────────

func TestE2E_ActionChain(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Stage 1: investigation (Investigator, google-native) ──

	// Iteration 1: thinking + text + tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the service status."},
			&agent.TextChunk{Content: "Checking service health."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_service_status", Arguments: `{"service":"api-gateway"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 25, TotalTokens: 105},
		},
	})
	// Iteration 2: thinking + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Service is down, confirmed by health check."},
			&agent.TextChunk{Content: "Investigation complete: api-gateway is DOWN. Health check confirms 503 errors since 10:00 UTC."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 40, TotalTokens: 140},
		},
	})

	// ── Stage 2: remediation (Remediator, action type, google-native) ──

	// Iteration 1: thinking + text + tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Evidence is clear — api-gateway is down. Restarting the service."},
			&agent.TextChunk{Content: "Restarting api-gateway based on confirmed outage."},
			&agent.ToolCallChunk{CallID: "call-2", Name: "test-mcp__restart_service", Arguments: `{"service":"api-gateway"}`},
			&agent.UsageChunk{InputTokens: 120, OutputTokens: 30, TotalTokens: 150},
		},
	})
	// Iteration 2: thinking + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Service restarted successfully."},
			&agent.TextChunk{Content: "Investigation complete: api-gateway was DOWN.\n\n## Actions Taken\nRestarted api-gateway. Service now healthy (200 OK)."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "api-gateway outage detected and remediated by automated restart."},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 20, TotalTokens: 80},
		},
	})

	// ── MCP tool results ──
	serviceStatus := `{"service":"api-gateway","status":"DOWN","error":"503 Service Unavailable","since":"10:00 UTC"}`
	restartResult := `{"service":"api-gateway","action":"restart","result":"success","new_status":"healthy"}`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "action-chain")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_service_status": StaticToolHandler(serviceStatus),
				"restart_service":    StaticToolHandler(restartResult),
			},
		}),
	)

	// Connect WS and subscribe.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-action", "api-gateway returning 503 errors")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "session.status" && e.Parsed["status"] == "completed"
	}, 5*time.Second, "waiting for session.status=completed WS event")

	// ── Session assertions ──
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["executive_summary"])

	// ── Stage assertions: investigation + remediation + exec_summary ──
	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 3)

	assert.Equal(t, "investigation", stages[0].StageName)
	assert.Equal(t, stage.StageTypeInvestigation, stages[0].StageType)

	assert.Equal(t, "remediation", stages[1].StageName)
	assert.Equal(t, stage.StageTypeAction, stages[1].StageType)

	assert.Equal(t, "Executive Summary", stages[2].StageName)
	assert.Equal(t, stage.StageTypeExecSummary, stages[2].StageType)

	// ── Verify action agent received safety preamble ──
	captured := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(captured), 3, "need at least investigation iter1 + action iter1 + exec_summary")

	// The action stage's first call is captured[2] (investigation has 2 calls).
	actionInput := captured[2]
	var hasSafetyPreamble bool
	for _, msg := range actionInput.Messages {
		if strings.Contains(msg.Content, "Action Agent Safety Guidelines") &&
			strings.Contains(msg.Content, "Prefer inaction over incorrect action") {
			hasSafetyPreamble = true
			break
		}
	}
	assert.True(t, hasSafetyPreamble, "action agent should have safety preamble in system prompt")

	// ── Verify context flow: action stage receives investigation findings ──
	var hasInvestigationContext bool
	for _, msg := range actionInput.Messages {
		if strings.Contains(msg.Content, "api-gateway is DOWN") &&
			strings.Contains(msg.Content, "503 errors") {
			hasInvestigationContext = true
			break
		}
	}
	assert.True(t, hasInvestigationContext, "action stage should receive investigation context")

	// ── Verify exec summary receives action stage's amended report ──
	execSummaryInput := captured[len(captured)-1]
	var hasActionContent bool
	for _, msg := range execSummaryInput.Messages {
		if strings.Contains(msg.Content, "Actions Taken") &&
			strings.Contains(msg.Content, "Restarted api-gateway") {
			hasActionContent = true
			break
		}
	}
	assert.True(t, hasActionContent, "exec summary should include action stage's report")

	// ── LLM call count ──
	// Investigator (2) + Remediator (2) + Exec summary (1) = 5
	assert.Equal(t, 5, llm.CallCount())

	// ── WS event structural assertions ──
	AssertEventsInOrder(t, ws.Events(), testdata.ActionChainExpectedEvents)
}
