package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/mcpinteraction"
	"github.com/codeready-toolchain/tarsy/ent/sessionscore"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Post-investigation e2e tests — agents run after auto-scoring:
//   - Gated entries: passing entry runs, gate miss creates no stage
//   - MCP tool call, memo persisted, YES marker → actions_executed
//   - Sub-agents dispatched from a post-investigation agent
//   - API re-score does not start post-investigation agents
// ────────────────────────────────────────────────────────────

// scriptScoredSession scripts investigation (2) + executive summary (1,
// labels the session "action") + scoring (2) as sequential entries.
func scriptScoredSession(llm *ScriptedLLMClient) {
	scriptSimpleInvestigation(llm)
	scriptExecSummary(llm)
	scriptScoringSuccess(llm)
}

func postInvestigationMCPServers() map[string]map[string]mcpsdk.ToolHandler {
	return map[string]map[string]mcpsdk.ToolHandler{
		"test-mcp": {
			"get_pods": StaticToolHandler(`[{"name":"pod-1","status":"OOMKilled","restarts":5}]`),
		},
		"ticket-mcp": {
			"search_tickets": StaticToolHandler(`[]`),
			"create_ticket":  StaticToolHandler(`{"id":"TCK-1","url":"https://tracker.example.com/TCK-1"}`),
		},
	}
}

// waitForPostInvestigationStage waits until the named post-investigation
// stage reaches a terminal status and returns it with its executions.
func (app *TestApp) waitForPostInvestigationStage(t *testing.T, sessionID, name string) *ent.Stage {
	t.Helper()
	var found *ent.Stage
	require.Eventually(t, func() bool {
		stg, err := app.EntClient.Stage.Query().
			Where(
				stage.SessionIDEQ(sessionID),
				stage.StageTypeEQ(stage.StageTypePostInvestigation),
				stage.StageNameEQ(name),
			).
			WithAgentExecutions().
			Only(context.Background())
		if err != nil {
			return false
		}
		found = stg
		return stg.Status != stage.StatusPending && stg.Status != stage.StatusActive
	}, 30*time.Second, 200*time.Millisecond, "post-investigation stage %q did not finish", name)
	return found
}

func TestE2E_PostInvestigation_AutoScoringRunsGatedEntries(t *testing.T) {
	llm := NewScriptedLLMClient()
	scriptScoredSession(llm)

	// PostInvestigator: create a ticket, then report it with the YES marker.
	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "The investigation confirmed an OOM; filing a ticket for the owning team."},
			&agent.ToolCallChunk{CallID: "pi-call-1", Name: "ticket-mcp__create_ticket",
				Arguments: `{"title":"pod-1 OOMKilled","body":"Raise the memory limit"}`},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 40, TotalTokens: 340},
		},
	})
	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		Text: "Decision: filed a ticket. Evidence: pod-1 OOMKilled with 5 restarts. Created https://tracker.example.com/TCK-1\n\nYES",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "post-investigation")),
		WithLLMClient(llm),
		WithMCPServers(postInvestigationMCPServers()),
	)
	require.NotNil(t, app.PostInvestigationExecutor)

	resp := app.SubmitAlert(t, "test-post-investigation", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	app.WaitForSessionStatus(t, sessionID, "completed")

	stg := app.waitForPostInvestigationStage(t, sessionID, "file-ticket")

	// ── Stage and execution ──
	assert.Equal(t, stage.StatusCompleted, stg.Status)
	require.NotNil(t, stg.ActionsExecuted)
	assert.True(t, *stg.ActionsExecuted)
	require.Len(t, stg.Edges.AgentExecutions, 1)
	exec := stg.Edges.AgentExecutions[0]
	assert.Equal(t, "PostInvestigator", exec.AgentName)
	assert.Equal(t, agentexecution.StatusCompleted, exec.Status)

	stages := app.QueryStages(t, sessionID)
	var scoringIndex int
	for _, s := range stages {
		if s.StageType == stage.StageTypeScoring {
			scoringIndex = s.StageIndex
		}
	}
	assert.Greater(t, stg.StageIndex, scoringIndex, "post-investigation runs after the scoring stage")

	// The gate-missed entry created no stage.
	piCount, err := app.EntClient.Stage.Query().
		Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypePostInvestigation)).
		Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, piCount)

	// ── Memo persisted without the marker ──
	memo, err := app.EntClient.TimelineEvent.Query().
		Where(timelineevent.ExecutionIDEQ(exec.ID), timelineevent.EventTypeEQ(timelineevent.EventTypeFinalAnalysis)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Contains(t, memo.Content, "https://tracker.example.com/TCK-1")
	assert.False(t, strings.HasSuffix(strings.TrimSpace(memo.Content), "YES"), "YES marker must be stripped from the memo")

	// ── MCP tool call recorded ──
	calls, err := app.EntClient.MCPInteraction.Query().
		Where(mcpinteraction.ExecutionIDEQ(exec.ID), mcpinteraction.InteractionTypeEQ(mcpinteraction.InteractionTypeToolCall)).
		All(context.Background())
	require.NoError(t, err)
	var toolNames []string
	for _, c := range calls {
		if c.ToolName != nil {
			toolNames = append(toolNames, *c.ToolName)
		}
	}
	assert.Contains(t, toolNames, "create_ticket")

	// ── The session itself is unchanged ──
	session, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.StatusCompleted, session.Status)
	assert.Equal(t, "Investigation complete: pod-1 is OOMKilled.", strings.TrimSpace(*session.FinalAnalysis))

	// ── Session API: per-stage outcome, list flags and filter ──
	detail := app.GetSession(t, sessionID)
	var piStage map[string]interface{}
	for _, raw := range detail["stages"].([]interface{}) {
		s := raw.(map[string]interface{})
		if s["stage_type"] == "post_investigation" {
			piStage = s
		}
	}
	require.NotNil(t, piStage, "session detail should list the post-investigation stage")
	assert.Equal(t, true, piStage["actions_executed"])
	assert.Equal(t, false, detail["has_action_stages"], "post-investigation is not an action stage")

	acted := app.GetSessionList(t, "post_investigation=acted")
	items := acted["sessions"].([]interface{})
	require.Len(t, items, 1)
	item := items[0].(map[string]interface{})
	assert.Equal(t, sessionID, item["id"])
	assert.Equal(t, true, item["has_post_investigation"])
	assert.Equal(t, true, item["post_investigation_acted"])

	none := app.GetSessionList(t, "post_investigation=none")
	assert.Empty(t, none["sessions"])

	// ── API re-score does not start post-investigation agents ──
	scriptScoringSuccess(llm)
	scoreResp := app.postJSON(t, "/api/v1/sessions/"+sessionID+"/score", nil, http.StatusAccepted)
	scoreID := scoreResp["score_id"].(string)
	require.Eventually(t, func() bool {
		s, err := app.EntClient.SessionScore.Get(context.Background(), scoreID)
		return err == nil && s.Status == sessionscore.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "re-score did not complete")

	// Drain both executors so a (wrongly) triggered run would have created its stage.
	app.ScoringExecutor.Stop(time.Minute)
	app.PostInvestigationExecutor.Stop(time.Minute)

	piCount, err = app.EntClient.Stage.Query().
		Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypePostInvestigation)).
		Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, piCount, "API re-score must not start post-investigation agents")
}

func TestE2E_PostInvestigation_SubAgent(t *testing.T) {
	llm := NewScriptedLLMClient()
	scriptScoredSession(llm)

	// Same gating pattern as TestE2E_Orchestrator: release the sub-agent only
	// after the post-investigation agent recorded iteration 2 and is waiting.
	subAgentGate := make(chan struct{})
	piIter2Gate := make(chan struct{})
	piIter2Ready := make(chan struct{}, 1)

	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "pi-d1", Name: "dispatch_agent",
				Arguments: `{"name":"Researcher","task":"Check for an existing ticket about pod-1 OOMKilled"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		WaitCh:  piIter2Gate,
		OnBlock: piIter2Ready,
		Text:    "Waiting for the Researcher result.",
	})
	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		Text: "Decision: no action. An equivalent ticket already exists.\n\nNO",
	})

	llm.AddRouted("Researcher", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "r-1", Name: "ticket-mcp__search_tickets",
				Arguments: `{"query":"pod-1 OOMKilled"}`},
		},
	})
	llm.AddRouted("Researcher", LLMScriptEntry{
		Text: "Found an existing open ticket for pod-1 memory limits.",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "post-investigation")),
		WithLLMClient(llm),
		WithMCPServers(postInvestigationMCPServers()),
	)

	resp := app.SubmitAlert(t, "test-post-investigation-subagent", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)

	go func() {
		<-piIter2Ready
		baseline, err := app.CountLLMInteractions(sessionID)
		if err != nil {
			t.Errorf("CountLLMInteractions failed: %v", err)
		}
		close(piIter2Gate)
		if !app.AwaitLLMInteractionIncrease(sessionID, baseline) {
			t.Errorf("AwaitLLMInteractionIncrease timed out (baseline=%d)", baseline)
		}
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")
	stg := app.waitForPostInvestigationStage(t, sessionID, "research-and-file")

	assert.Equal(t, stage.StatusCompleted, stg.Status)
	require.NotNil(t, stg.ActionsExecuted)
	assert.False(t, *stg.ActionsExecuted)

	execs, err := app.EntClient.AgentExecution.Query().
		Where(agentexecution.StageIDEQ(stg.ID)).
		All(context.Background())
	require.NoError(t, err)
	require.Len(t, execs, 2, "post-investigation agent + one sub-agent")

	var parentID string
	var sub *ent.AgentExecution
	for _, e := range execs {
		if e.ParentExecutionID == nil {
			parentID = e.ID
			assert.Equal(t, "PostInvestigator", e.AgentName)
		} else {
			sub = e
		}
	}
	require.NotNil(t, sub)
	assert.Equal(t, "Researcher", sub.AgentName)
	assert.Equal(t, parentID, *sub.ParentExecutionID)
	assert.Equal(t, agentexecution.StatusCompleted, sub.Status)

	// ── Timeline API: one top-level memo for the stage, marker stripped;
	// sub-agent final answers are tagged with the parent execution ──
	var topLevelMemos []string
	subAgentAnswers := 0
	for _, raw := range app.GetTimeline(t, sessionID) {
		ev := raw.(map[string]interface{})
		if ev["stage_id"] != stg.ID || ev["event_type"] != string(timelineevent.EventTypeFinalAnalysis) {
			continue
		}
		if parent, ok := ev["parent_execution_id"].(string); ok {
			assert.Equal(t, parentID, parent)
			assert.Equal(t, sub.ID, ev["execution_id"])
			subAgentAnswers++
			continue
		}
		topLevelMemos = append(topLevelMemos, ev["content"].(string))
	}
	require.Len(t, topLevelMemos, 1, "post-investigation stage should have one top-level memo")
	assert.Equal(t, 1, subAgentAnswers, "sub-agent final answer should be tagged with the parent execution")
	assert.Contains(t, topLevelMemos[0], "An equivalent ticket already exists.")
	assert.False(t, strings.HasSuffix(strings.TrimSpace(topLevelMemos[0]), "NO"), "NO marker must be stripped from the memo")

	// ── Session list: ran without acting ──
	ran := app.GetSessionList(t, "post_investigation=ran")
	items := ran["sessions"].([]interface{})
	require.Len(t, items, 1)
	item := items[0].(map[string]interface{})
	assert.Equal(t, sessionID, item["id"])
	assert.Equal(t, true, item["has_post_investigation"])
	assert.Equal(t, false, item["post_investigation_acted"])

	acted := app.GetSessionList(t, "post_investigation=acted")
	assert.Empty(t, acted["sessions"])

	// The orchestrator sections were injected into the post-investigation prompt.
	var piSystem string
	for _, input := range llm.CapturedInputs() {
		for _, m := range input.Messages {
			if m.Role == agent.RoleSystem && strings.Contains(m.Content, "You are PostInvestigator") {
				piSystem = m.Content
			}
		}
	}
	require.NotEmpty(t, piSystem)
	assert.Contains(t, piSystem, "Post-Investigation Agent Guidelines")
	assert.Contains(t, piSystem, "Available Sub-Agents")
	assert.Contains(t, piSystem, "Researcher")
}
