package e2e

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Scoring e2e tests — verify the complete scoring pipeline:
//   - Auto-trigger after session completion (chain scoring enabled)
//   - Re-score via API endpoint
//   - Scoring disabled chain (no auto-trigger)
//   - Scoring stage, execution, and session_scores records
//   - Scoring events published via WebSocket
//   - Duplicate scoring prevention (409 Conflict)
// ────────────────────────────────────────────────────────────

// scriptSimpleInvestigation adds a simple single-agent investigation (2 LLM calls).
// Used by scoring-disabled-chain tests that have only one investigation agent.
func scriptSimpleInvestigation(llm *ScriptedLLMClient) {
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the pod status."},
			&agent.TextChunk{Content: "Checking pods."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Pod is OOMKilled."},
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
}

// scriptExecSummary adds an executive summary response.
func scriptExecSummary(llm *ScriptedLLMClient) {
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOMKilled. Recommend increasing memory limit."})
}

// scriptRichPipeline scripts the full scoring-chain pipeline:
//
//	Stage 1: investigation (Investigator ∥ MetricsChecker) → synthesis
//	Stage 2: remediation (Remediator, action type)
//	+ Executive summary
//
// LLM calls: Investigator(2) + MetricsChecker(1) + Synthesis(1) + Remediator(2) + ExecSummary(1) = 7.
func scriptRichPipeline(llm *ScriptedLLMClient) {
	// ── Investigator (routed, parallel): tool call + final answer ──
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the pod status."},
			&agent.TextChunk{Content: "Checking pods."},
			&agent.ToolCallChunk{CallID: "inv-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddRouted("Investigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Pod is OOMKilled."},
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// ── MetricsChecker (routed, parallel): single-iteration final answer ──
	llm.AddRouted("MetricsChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing memory metrics."},
			&agent.TextChunk{Content: "Memory usage at 98% of limit (500Mi/512Mi). OOM pattern confirmed over last 2 hours."},
			&agent.UsageChunk{InputTokens: 90, OutputTokens: 25, TotalTokens: 115},
		},
	})

	// ── Synthesis (sequential, runs after parallel investigation completes) ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Combined analysis: pod-1 is OOMKilled (5 restarts) with memory at 98% of 512Mi limit. Both agents confirm memory pressure as root cause."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

	// ── Remediator (sequential, action stage): tool call + final answer ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Evidence is clear — pod needs restart with higher memory limit."},
			&agent.TextChunk{Content: "Restarting pod-1 with increased memory."},
			&agent.ToolCallChunk{CallID: "rem-1", Name: "test-mcp__restart_pod", Arguments: `{"pod":"pod-1","namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 120, OutputTokens: 30, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Restart successful."},
			&agent.TextChunk{Content: "Remediation complete: restarted pod-1.\n\n## Actions Taken\nRestarted pod-1. Recommend increasing memory limit to 1Gi."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 40, TotalTokens: 140},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOMKilled due to memory pressure. Restarted. Recommend increasing memory limit to 1Gi."})
}

// scriptScoringSuccess adds scoring LLM responses that produce a valid score.
// The scoring controller makes 2 LLM calls: score evaluation + missing tools.
func scriptScoringSuccess(llm *ScriptedLLMClient) {
	// Turn 1: Score evaluation — analysis text followed by the numeric score on the last line.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Score Analysis\n\nThe investigation demonstrated good logical flow by systematically checking pod status and identifying the OOM kill. Tool usage was relevant — the agent queried pods and confirmed the issue.\n\nHowever, no memory metrics or resource limits were checked, which would have strengthened the diagnosis.\n\n**Logical Flow:** 20/25\n**Consistency:** 22/25\n**Tool Relevance:** 18/25\n**Synthesis Quality:** 15/25\n\n75"},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 100, TotalTokens: 600},
		},
	})
	// Turn 2: Missing tools analysis.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Missing Tools Report\n\nThe following tools would improve future investigations:\n\n1. **get_resource_limits** — Query pod resource limits and requests to verify configuration.\n2. **get_memory_metrics** — Fetch Prometheus memory usage time series for trend analysis."},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 80, TotalTokens: 680},
		},
	})
}

func TestE2E_Scoring_AutoTrigger(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Rich pipeline (7) + scoring (2) = 9 total.
	scriptRichPipeline(llm)
	scriptScoringSuccess(llm)

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`
	restartResult := `{"pod":"pod-1","action":"restart","result":"success"}`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "scoring")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods":    StaticToolHandler(podsResult),
				"restart_pod": StaticToolHandler(restartResult),
			},
		}),
	)

	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	resp := app.SubmitAlert(t, "test-scoring", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for the scoring stage to complete (auto-triggered after session completion).
	var scoringStageID string
	require.Eventually(t, func() bool {
		stgs, err := app.EntClient.Stage.Query().
			Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
			All(context.Background())
		if err != nil || len(stgs) == 0 {
			return false
		}
		scoringStageID = stgs[0].ID
		return stgs[0].Status == stage.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "scoring stage did not complete")

	// ── Verify DB state ──

	stages := app.QueryStages(t, sessionID)
	stageTypes := make([]stage.StageType, len(stages))
	for i, s := range stages {
		stageTypes[i] = s.StageType
	}
	assert.Contains(t, stageTypes, stage.StageTypeInvestigation)
	assert.Contains(t, stageTypes, stage.StageTypeSynthesis, "parallel agents should trigger synthesis")
	assert.Contains(t, stageTypes, stage.StageTypeAction, "remediation stage should be action type")
	assert.Contains(t, stageTypes, stage.StageTypeExecSummary)
	assert.Contains(t, stageTypes, stage.StageTypeScoring)

	lastStage := stages[len(stages)-1]
	assert.Equal(t, "Scoring", lastStage.StageName)
	assert.Equal(t, stage.StageTypeScoring, lastStage.StageType)
	assert.Equal(t, stage.StatusCompleted, lastStage.Status)

	scoringExecs, err := lastStage.QueryAgentExecutions().All(ctx)
	require.NoError(t, err)
	require.Len(t, scoringExecs, 1)
	assert.Equal(t, config.AgentNameScoring, scoringExecs[0].AgentName)

	// ── Verify session_scores record ──

	scores, err := app.EntClient.SessionScore.Query().
		Where(sessionscore.SessionIDEQ(sessionID)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, scores, 1)

	score := scores[0]
	assert.Equal(t, sessionscore.StatusCompleted, score.Status)
	require.NotNil(t, score.TotalScore)
	assert.Equal(t, 75, *score.TotalScore)
	assert.Equal(t, "auto", score.ScoreTriggeredBy)
	require.NotNil(t, score.StageID)
	assert.Equal(t, scoringStageID, *score.StageID)
	assert.NotNil(t, score.CompletedAt)
	assert.NotNil(t, score.PromptHash)

	// ── Verify GET /api/v1/sessions/:id/score API ──

	scoreResp := app.GetScore(t, sessionID)
	assert.Equal(t, score.ID, scoreResp["score_id"], "API score_id should match DB record")
	assert.Equal(t, scoringStageID, scoreResp["stage_id"], "API stage_id should match scoring stage")

	// ── Verify scoring fields on session detail ──

	sessionDetail := app.GetSession(t, sessionID)
	assert.Equal(t, float64(75), sessionDetail["latest_score"])
	assert.Equal(t, "completed", sessionDetail["scoring_status"])
	assert.NotNil(t, sessionDetail["score_id"])

	// ── Verify scoring fields on session list ──

	listResp := app.GetSessionList(t, "")
	sessions := listResp["sessions"].([]interface{})
	require.NotEmpty(t, sessions)
	var found bool
	for _, raw := range sessions {
		s := raw.(map[string]interface{})
		if s["id"] == sessionID {
			assert.Equal(t, float64(75), s["latest_score"])
			assert.Equal(t, "completed", s["scoring_status"])
			found = true
			break
		}
	}
	require.True(t, found, "session not found in list")

	// ── Verify scoring prompts via golden files ──

	captured := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(captured), 9, "should have at least 9 captured LLM inputs")

	scoringInput1 := captured[len(captured)-2]
	scoringInput2 := captured[len(captured)-1]

	AssertGolden(t, GoldenPath("scoring", "prompt_turn1.golden"),
		[]byte(serializeLLMMessages(scoringInput1.Messages)))
	AssertGolden(t, GoldenPath("scoring", "prompt_turn2.golden"),
		[]byte(serializeLLMMessages(scoringInput2.Messages)))

	// ── Verify scoring LLM interactions in trace API ──

	traceResp := app.GetTraceList(t, sessionID)
	traceStages := traceResp["stages"].([]interface{})

	// Find the scoring stage in trace.
	var scoringTraceStage map[string]interface{}
	for _, raw := range traceStages {
		s := raw.(map[string]interface{})
		if s["stage_type"] == "scoring" {
			scoringTraceStage = s
			break
		}
	}
	require.NotNil(t, scoringTraceStage, "scoring stage should appear in trace")
	assert.Equal(t, scoringStageID, scoringTraceStage["stage_id"])

	// Scoring execution should have LLM interactions (now persisted).
	scoringTraceExecs := scoringTraceStage["executions"].([]interface{})
	require.Len(t, scoringTraceExecs, 1)
	scoringTraceExec := scoringTraceExecs[0].(map[string]interface{})
	scoringLLMInteractions := scoringTraceExec["llm_interactions"].([]interface{})
	assert.Len(t, scoringLLMInteractions, 2, "scoring should have 2 LLM interactions (eval + missing tools)")

	// Verify interaction types.
	for _, raw := range scoringLLMInteractions {
		interaction := raw.(map[string]interface{})
		assert.Equal(t, "scoring", interaction["interaction_type"])
	}

	// ── Golden file assertions ──

	normalizer := NewNormalizer(sessionID)
	for _, rawStage := range traceStages {
		stg := rawStage.(map[string]interface{})
		normalizer.RegisterStageID(stg["stage_id"].(string))
		executions, _ := stg["executions"].([]interface{})
		for _, rawExec := range executions {
			exec := rawExec.(map[string]interface{})
			normalizer.RegisterExecutionID(exec["execution_id"].(string))
			llmInteractions, _ := exec["llm_interactions"].([]interface{})
			for _, rawLI := range llmInteractions {
				li, _ := rawLI.(map[string]interface{})
				if id, ok := li["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
			mcpInteractions, _ := exec["mcp_interactions"].([]interface{})
			for _, rawMI := range mcpInteractions {
				mi, _ := rawMI.(map[string]interface{})
				if id, ok := mi["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
		}
	}
	sessionInteractions, _ := traceResp["session_interactions"].([]interface{})
	for _, rawLI := range sessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		if id, ok := li["id"].(string); ok {
			normalizer.RegisterInteractionID(id)
		}
	}

	AssertGoldenJSON(t, GoldenPath("scoring", "score.golden"), scoreResp, normalizer)
	AssertGoldenJSON(t, GoldenPath("scoring", "trace_list.golden"), traceResp, normalizer)

	// ── Verify scoring LLM interaction detail via golden files ──
	// Fetch each scoring interaction detail and compare full response (metadata + conversation)
	// against golden files, exactly like pipeline_test.go does for all its interactions.
	scoringInteractionLabels := []string{"scoring_eval", "scoring_missing_tools"}
	for i, raw := range scoringLLMInteractions {
		interaction := raw.(map[string]interface{})
		interactionID := interaction["id"].(string)
		detail := app.GetLLMInteractionDetail(t, sessionID, interactionID)
		goldenPath := GoldenPath("scoring", filepath.Join("trace_interactions", fmt.Sprintf("%02d_scoring_llm_%s.golden", i+1, scoringInteractionLabels[i])))
		AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
	}

	// ── WS event structural assertions ──

	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "stage.status" &&
			e.Parsed["stage_name"] == "Scoring" &&
			e.Parsed["status"] == "completed"
	}, 5*time.Second, "expected scoring stage.status completed WS event")

	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)
	AssertEventsInOrder(t, wsEvents, testdata.ScoringExpectedEvents)

	// Verify total LLM call count: pipeline (7) + scoring (2) = 9.
	assert.Equal(t, 9, llm.CallCount())
}

func TestE2E_Scoring_Disabled_NoAutoTrigger(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Simple investigation (2) + exec summary (1) = 3 total. No scoring responses needed.
	scriptSimpleInvestigation(llm)
	scriptExecSummary(llm)

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":3}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "scoring")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-scoring-disabled", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Poll over a verification window to confirm scoring never fires.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		scoringStages, err := app.EntClient.Stage.Query().
			Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
			All(context.Background())
		require.NoError(t, err)
		require.Empty(t, scoringStages, "scoring stage must not be created when scoring is disabled")

		scores, err := app.EntClient.SessionScore.Query().
			Where(sessionscore.SessionIDEQ(sessionID)).
			All(context.Background())
		require.NoError(t, err)
		require.Empty(t, scores, "session_scores must not be created when scoring is disabled")

		time.Sleep(50 * time.Millisecond)
	}

	// ── Verify session detail has no scoring fields ──
	sessionDetail := app.GetSession(t, sessionID)
	assert.Nil(t, sessionDetail["latest_score"])
	assert.Nil(t, sessionDetail["scoring_status"])
	assert.Nil(t, sessionDetail["score_id"])

	// ── Verify GET score API returns 404 ──
	app.getJSON(t, "/api/v1/sessions/"+sessionID+"/score", http.StatusNotFound)

	// Only investigation + exec summary calls.
	assert.Equal(t, 3, llm.CallCount())
}

func TestE2E_Scoring_ReScoreAPI(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Rich pipeline (7) + auto scoring (2) + re-score (2) = 11 total.
	scriptRichPipeline(llm)
	scriptScoringSuccess(llm)

	// Re-score responses (different score).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Updated Score Analysis\n\nOn re-evaluation, the investigation was solid.\n\n82"},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 80, TotalTokens: 580},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Updated Missing Tools\n\nNo critical tools missing."},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 50, TotalTokens: 650},
		},
	})

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`
	restartResult := `{"pod":"pod-1","action":"restart","result":"success"}`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "scoring")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods":    StaticToolHandler(podsResult),
				"restart_pod": StaticToolHandler(restartResult),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-scoring", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for auto-scoring to complete.
	require.Eventually(t, func() bool {
		scores, err := app.EntClient.SessionScore.Query().
			Where(sessionscore.SessionIDEQ(sessionID), sessionscore.StatusEQ(sessionscore.StatusCompleted)).
			All(context.Background())
		return err == nil && len(scores) == 1
	}, 30*time.Second, 200*time.Millisecond, "auto-scoring did not complete")

	// Trigger re-score via API.
	scoreResp := app.postJSON(t, "/api/v1/sessions/"+sessionID+"/score", nil, http.StatusAccepted)
	scoreID := scoreResp["score_id"].(string)
	require.NotEmpty(t, scoreID)

	// Wait for re-score to complete.
	require.Eventually(t, func() bool {
		s, err := app.EntClient.SessionScore.Get(context.Background(), scoreID)
		return err == nil && s.Status == sessionscore.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "re-score did not complete")

	// Verify two scoring records exist.
	scores, err := app.EntClient.SessionScore.Query().
		Where(sessionscore.SessionIDEQ(sessionID)).
		All(context.Background())
	require.NoError(t, err)
	assert.Len(t, scores, 2, "should have both auto-score and re-score records")

	// Verify the re-scored record has the updated score.
	reScore, err := app.EntClient.SessionScore.Get(context.Background(), scoreID)
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusCompleted, reScore.Status)
	require.NotNil(t, reScore.TotalScore)
	assert.Equal(t, 82, *reScore.TotalScore)

	// Verify two scoring stages exist.
	scoringStages, err := app.EntClient.Stage.Query().
		Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
		All(context.Background())
	require.NoError(t, err)
	assert.Len(t, scoringStages, 2)

	// ── Verify GET score API returns the latest (re-score) ──
	scoreAPIResp := app.GetScore(t, sessionID)
	assert.Equal(t, scoreID, scoreAPIResp["score_id"])
	assert.Equal(t, float64(82), scoreAPIResp["total_score"])
	assert.Equal(t, "completed", scoreAPIResp["status"])

	// ── Verify session detail reflects the re-score ──
	sessionDetail := app.GetSession(t, sessionID)
	assert.Equal(t, float64(82), sessionDetail["latest_score"])
	assert.Equal(t, "completed", sessionDetail["scoring_status"])

	assert.Equal(t, 11, llm.CallCount())
}

func TestE2E_Scoring_API_DuplicatePrevention(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Simple investigation (2) + exec summary (1) = 3. No auto-scoring (disabled chain).
	scriptSimpleInvestigation(llm)
	scriptExecSummary(llm)

	// First re-score: score eval blocks in LLM to test concurrent rejection.
	blockCh := make(chan struct{})
	llm.AddSequential(LLMScriptEntry{WaitCh: blockCh, Text: "Analysis text.\n\n70"})
	llm.AddSequential(LLMScriptEntry{Text: "No missing tools."})

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":3}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "scoring")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	// Use disabled chain so auto-scoring doesn't fire.
	resp := app.SubmitAlert(t, "test-scoring-disabled", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	app.WaitForSessionStatus(t, sessionID, "completed")

	// First re-score — will block in the LLM call.
	scoreResp := app.postJSON(t, "/api/v1/sessions/"+sessionID+"/score", nil, http.StatusAccepted)
	require.NotEmpty(t, scoreResp["score_id"])

	// Wait for the scoring record to be in_progress.
	require.Eventually(t, func() bool {
		scores, err := app.EntClient.SessionScore.Query().
			Where(sessionscore.SessionIDEQ(sessionID), sessionscore.StatusEQ(sessionscore.StatusInProgress)).
			All(context.Background())
		return err == nil && len(scores) == 1
	}, 10*time.Second, 100*time.Millisecond, "scoring did not start")

	// Second re-score — should be rejected with 409 Conflict (partial unique index).
	app.postJSON(t, "/api/v1/sessions/"+sessionID+"/score", nil, http.StatusConflict)

	// Unblock the first scoring and wait for it to complete.
	close(blockCh)

	scoreID := scoreResp["score_id"].(string)
	require.Eventually(t, func() bool {
		s, err := app.EntClient.SessionScore.Get(context.Background(), scoreID)
		return err == nil && s.Status == sessionscore.StatusCompleted
	}, 10*time.Second, 100*time.Millisecond, "blocked scoring did not complete after unblock")
}

func TestE2E_Scoring_API_NonTerminalSession(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Block both parallel investigation agents so the session stays in_progress.
	blockCh := make(chan struct{})
	llm.AddRouted("Investigator", LLMScriptEntry{WaitCh: blockCh, Text: "Investigating..."})
	llm.AddRouted("MetricsChecker", LLMScriptEntry{WaitCh: blockCh, Text: "Checking metrics..."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "scoring")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods":    StaticToolHandler(`[]`),
				"restart_pod": StaticToolHandler(`{}`),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-scoring", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Wait for the session to be picked up (in_progress).
	app.WaitForSessionStatus(t, sessionID, "in_progress")

	// Attempt to score a non-terminal session — should be rejected.
	app.postJSON(t, "/api/v1/sessions/"+sessionID+"/score", nil, http.StatusConflict)

	close(blockCh)
}
