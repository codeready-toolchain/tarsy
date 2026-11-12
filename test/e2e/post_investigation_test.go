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
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Post-investigation e2e tests — agents run after auto-scoring:
//   - Gated entries: passing entry runs, gate miss creates no stage
//   - MCP tool call, memo persisted, YES marker → actions_executed
//   - Sub-agents dispatched from a post-investigation agent, with that
//     sub-agent's skills and tools
//   - Two entries run together, each with its own tools; skills load when
//     the agent defines them; chain MCP servers and sub-agents are not inherited
//   - API re-score does not start post-investigation agents
//   - Shutdown: scoring that completes during its drain still runs the agents
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

	skips, _ := detail["post_investigation_skips"].([]interface{})
	require.Len(t, skips, 1, "the gated-out entry should explain why it did not run")
	skip := skips[0].(map[string]interface{})
	assert.Equal(t, "noise-follow-up", skip["name"])
	assert.Contains(t, skip["detail"].(string), "noise")
	assert.Contains(t, skip["detail"].(string), "action")

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
			&agent.ToolCallChunk{CallID: "r-skill", Name: "load_skill",
				Arguments: `{"names":["networking"]}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddRouted("Researcher", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "r-1", Name: "ticket-mcp__search_tickets",
				Arguments: `{"query":"pod-1 OOMKilled"}`},
			&agent.UsageChunk{InputTokens: 120, OutputTokens: 20, TotalTokens: 140},
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
	// This agent opts out of skills. dispatch_agent comes from its own sub_agents.
	parentInputs := agentInputs(llm, "You are PostInvestigator")
	require.NotEmpty(t, parentInputs)
	parentSystem := systemPrompt(t, parentInputs[0])
	assert.Contains(t, parentSystem, "Post-Investigation Agent Guidelines")
	assert.Contains(t, parentSystem, "Available Sub-Agents")
	assert.Contains(t, parentSystem, "Researcher")
	assert.NotContains(t, parentSystem, "## Pre-loaded Skills")
	assertHasTool(t, parentInputs[0], "dispatch_agent")
	assertNoTool(t, parentInputs[0], "load_skill")

	// The sub-agent was created with its own skill catalog and ticket tools.
	researcherInputs := agentInputs(llm, "You are Researcher")
	require.Len(t, researcherInputs, 3)
	assertSystemPromptContains(t, researcherInputs[0], "## Pre-loaded Skills",
		"required skill should be injected into the sub-agent prompt")
	assertSystemPromptContains(t, researcherInputs[0], "A pod is the smallest deployable unit",
		"required skill body should be in the sub-agent prompt")
	assertSystemPromptContains(t, researcherInputs[0], "## Available Skills",
		"on-demand catalog should be in the sub-agent prompt")
	assertSystemPromptContains(t, researcherInputs[0], "networking",
		"networking should be offered to the sub-agent")
	assertHasTool(t, researcherInputs[0], "load_skill")
	assertHasTool(t, researcherInputs[0], "ticket-mcp.search_tickets")
	assertToolResultContains(t, researcherInputs[1], "load_skill",
		"DNS Resolution", "sub-agent should receive the skill body")
	assertToolResultContains(t, researcherInputs[2], "ticket-mcp__search_tickets",
		"[]", "sub-agent should receive the ticket search result")

	timeline := app.QueryTimeline(t, sessionID)
	loaded := findSkillLoaded(timeline, sub.ID, "kubernetes-basics")
	require.NotNil(t, loaded, "required skill should be recorded on the sub-agent execution")
	assert.Contains(t, loaded.Content, "smallest deployable unit")

	assert.ElementsMatch(t, []string{"load_skill", "search_tickets"}, mcpToolNames(t, app, sub.ID))
}

func TestE2E_PostInvestigation_MultipleAgentsSkillsAndTools(t *testing.T) {
	llm := NewScriptedLLMClient()
	scriptScoredSession(llm)

	// SkillFiler: required skill is already in the prompt; load the on-demand
	// skill, then file a ticket with the agent-level MCP server.
	llm.AddRouted("SkillFiler", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "sf-skill", Name: "load_skill",
				Arguments: `{"names":["networking"]}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	llm.AddRouted("SkillFiler", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "sf-ticket", Name: "ticket-mcp__create_ticket",
				Arguments: `{"title":"pod-1 OOMKilled","body":"Raise the memory limit"}`},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 30, TotalTokens: 180},
		},
	})
	llm.AddRouted("SkillFiler", LLMScriptEntry{
		Text: "Decision: filed a ticket. Evidence: pod-1 OOMKilled. Created https://tracker.example.com/TCK-1\n\nYES",
	})

	// PodChecker: entry MCP override is test-mcp, not the agent's ticket-mcp.
	llm.AddRouted("PodChecker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "pc-pods", Name: "test-mcp__get_pods",
				Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddRouted("PodChecker", LLMScriptEntry{
		Text: "Decision: no action. The pod is already OOMKilled and no change is warranted.\n\nNO",
	})

	servers := postInvestigationMCPServers()
	servers["chain-only-mcp"] = map[string]mcpsdk.ToolHandler{
		"chain_only_tool": StaticToolHandler(`{"scope":"chain"}`),
	}
	app := NewTestApp(t,
		WithConfig(configs.Load(t, "post-investigation")),
		WithLLMClient(llm),
		WithMCPServers(servers),
	)

	resp := app.SubmitAlert(t, "test-post-investigation-capabilities", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	app.WaitForSessionStatus(t, sessionID, "completed")

	filerStage := app.waitForPostInvestigationStage(t, sessionID, "file-from-skill")
	checkerStage := app.waitForPostInvestigationStage(t, sessionID, "check-pods")

	assert.Equal(t, stage.StatusCompleted, filerStage.Status)
	assert.Equal(t, stage.StatusCompleted, checkerStage.Status)
	require.NotNil(t, filerStage.ActionsExecuted)
	assert.True(t, *filerStage.ActionsExecuted)
	require.NotNil(t, checkerStage.ActionsExecuted)
	assert.False(t, *checkerStage.ActionsExecuted)
	require.Len(t, filerStage.Edges.AgentExecutions, 1)
	require.Len(t, checkerStage.Edges.AgentExecutions, 1)
	filerExec := filerStage.Edges.AgentExecutions[0]
	checkerExec := checkerStage.Edges.AgentExecutions[0]
	assert.Equal(t, "SkillFiler", filerExec.AgentName)
	assert.Equal(t, "PodChecker", checkerExec.AgentName)

	stages := app.QueryStages(t, sessionID)
	var scoringIndex int
	var piNames []string
	for _, s := range stages {
		if s.StageType == stage.StageTypeScoring {
			scoringIndex = s.StageIndex
		}
		if s.StageType == stage.StageTypePostInvestigation {
			piNames = append(piNames, s.StageName)
			assert.Greater(t, s.StageIndex, scoringIndex)
		}
	}
	assert.ElementsMatch(t, []string{"file-from-skill", "check-pods"}, piNames)

	// ── Each agent was offered only its own tools ──
	filerInputs := agentInputs(llm, "You are SkillFiler")
	checkerInputs := agentInputs(llm, "You are PodChecker")
	require.Len(t, filerInputs, 3)
	require.Len(t, checkerInputs, 2)

	assertHasTool(t, filerInputs[0], "load_skill")
	assertHasTool(t, filerInputs[0], "ticket-mcp.create_ticket")
	assertHasTool(t, filerInputs[0], "ticket-mcp.search_tickets")
	assertNoTool(t, filerInputs[0], "test-mcp.get_pods", "chain-only-mcp.chain_only_tool", "dispatch_agent")

	assertHasTool(t, checkerInputs[0], "test-mcp.get_pods")
	assertNoTool(t, checkerInputs[0], "load_skill", "ticket-mcp.create_ticket", "ticket-mcp.search_tickets", "chain-only-mcp.chain_only_tool", "dispatch_agent")

	// The investigation did inherit the chain's sub-agent, so the absence
	// above is the post-investigation rule and not a missing config.
	investigatorInputs := agentInputs(llm, "You are Investigator")
	require.NotEmpty(t, investigatorInputs)
	assertHasTool(t, investigatorInputs[0], "dispatch_agent")
	assertHasTool(t, investigatorInputs[0], "test-mcp.get_pods")
	assertNoTool(t, investigatorInputs[0], "chain-only-mcp.chain_only_tool")

	// ── Skills: required body in the prompt, on-demand body from load_skill ──
	assertSystemPromptContains(t, filerInputs[0], "## Pre-loaded Skills",
		"required skill section should be in the post-investigation prompt")
	assertSystemPromptContains(t, filerInputs[0], "A pod is the smallest deployable unit",
		"required skill body should be in the post-investigation prompt")
	assertSystemPromptContains(t, filerInputs[0], "## Available Skills",
		"on-demand catalog should be in the post-investigation prompt")
	assertSystemPromptContains(t, filerInputs[0], "networking",
		"networking should be offered on demand")
	assert.NotContains(t, systemPrompt(t, checkerInputs[0]), "## Pre-loaded Skills")
	assert.NotContains(t, systemPrompt(t, checkerInputs[0]), "## Available Skills")

	assertToolResultContains(t, filerInputs[1], "load_skill",
		"DNS Resolution", "SkillFiler should receive the skill body")
	assertToolResultContains(t, filerInputs[2], "ticket-mcp__create_ticket",
		"https://tracker.example.com/TCK-1", "SkillFiler should receive the ticket it created")
	assertToolResultContains(t, checkerInputs[1], "test-mcp__get_pods",
		"OOMKilled", "PodChecker should receive the pod listing")

	timeline := app.QueryTimeline(t, sessionID)
	loaded := findSkillLoaded(timeline, filerExec.ID, "kubernetes-basics")
	require.NotNil(t, loaded, "required skill should be recorded on the post-investigation execution")
	assert.Contains(t, loaded.Content, "smallest deployable unit")
	assert.ElementsMatch(t, []string{"load_skill", "create_ticket"}, mcpToolNames(t, app, filerExec.ID))
	assert.ElementsMatch(t, []string{"get_pods"}, mcpToolNames(t, app, checkerExec.ID))

	// ── Context: both agents see the score; only the opted-in agent sees the timeline ──
	filerUser := userPrompt(t, filerInputs[0])
	checkerUser := userPrompt(t, checkerInputs[0])
	assert.Contains(t, filerUser, "## Completed Investigation")
	assert.Contains(t, filerUser, "Total score: 75/100")
	assert.Contains(t, filerUser, "Labels: action")
	assert.Contains(t, filerUser, "### Full Investigation")
	assert.Contains(t, filerUser, "Investigation complete: pod-1 is OOMKilled.")
	assert.Contains(t, checkerUser, "Total score: 75/100")
	assert.Contains(t, checkerUser, "Labels: action")
	assert.NotContains(t, checkerUser, "### Full Investigation")

	// ── Memos, marker stripped, session itself unchanged ──
	filerMemo := finalAnalysisContent(t, app, filerExec.ID)
	checkerMemo := finalAnalysisContent(t, app, checkerExec.ID)
	assert.Contains(t, filerMemo, "https://tracker.example.com/TCK-1")
	assert.False(t, strings.HasSuffix(strings.TrimSpace(filerMemo), "YES"))
	assert.Contains(t, checkerMemo, "no action")
	assert.False(t, strings.HasSuffix(strings.TrimSpace(checkerMemo), "NO"))

	session, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Equal(t, alertsession.StatusCompleted, session.Status)
	assert.Equal(t, "Investigation complete: pod-1 is OOMKilled.", strings.TrimSpace(*session.FinalAnalysis))
	assert.Empty(t, session.PostInvestigationSkips)

	detail := app.GetSession(t, sessionID)
	assert.Nil(t, detail["post_investigation_skips"])
	assert.Equal(t, false, detail["has_action_stages"])
	byName := map[string]map[string]interface{}{}
	for _, raw := range detail["stages"].([]interface{}) {
		s := raw.(map[string]interface{})
		if s["stage_type"] == "post_investigation" {
			byName[s["stage_name"].(string)] = s
		}
	}
	require.Len(t, byName, 2)
	assert.Equal(t, true, byName["file-from-skill"]["actions_executed"])
	assert.Equal(t, false, byName["check-pods"]["actions_executed"])

	acted := app.GetSessionList(t, "post_investigation=acted")
	items := acted["sessions"].([]interface{})
	require.Len(t, items, 1)
	item := items[0].(map[string]interface{})
	assert.Equal(t, sessionID, item["id"])
	assert.Equal(t, true, item["has_post_investigation"])
	assert.Equal(t, true, item["post_investigation_acted"])
}

// agentInputs returns captured LLM calls whose system prompt identifies the agent.
func agentInputs(llm *ScriptedLLMClient, identity string) []*agent.GenerateInput {
	var out []*agent.GenerateInput
	for _, input := range llm.CapturedInputs() {
		if hasAgentInPrompt(input, identity) {
			out = append(out, input)
		}
	}
	return out
}

func systemPrompt(t *testing.T, input *agent.GenerateInput) string {
	t.Helper()
	for _, m := range input.Messages {
		if m.Role == agent.RoleSystem {
			return m.Content
		}
	}
	t.Fatal("no system message")
	return ""
}

func userPrompt(t *testing.T, input *agent.GenerateInput) string {
	t.Helper()
	for _, m := range input.Messages {
		if m.Role == agent.RoleUser {
			return m.Content
		}
	}
	t.Fatal("no user message")
	return ""
}

func assertNoTool(t *testing.T, input *agent.GenerateInput, names ...string) {
	t.Helper()
	have := make([]string, 0, len(input.Tools))
	for _, tool := range input.Tools {
		have = append(have, tool.Name)
	}
	for _, name := range names {
		assert.NotContains(t, have, name, "tool list should not contain %q (have %v)", name, have)
	}
}

func findSkillLoaded(events []*ent.TimelineEvent, executionID, skillName string) *ent.TimelineEvent {
	for _, e := range events {
		if e.EventType != timelineevent.EventTypeSkillLoaded || e.ExecutionID == nil || *e.ExecutionID != executionID {
			continue
		}
		if name, _ := e.Metadata["skill_name"].(string); name == skillName {
			return e
		}
	}
	return nil
}

func mcpToolNames(t *testing.T, app *TestApp, executionID string) []string {
	t.Helper()
	calls, err := app.EntClient.MCPInteraction.Query().
		Where(mcpinteraction.ExecutionIDEQ(executionID), mcpinteraction.InteractionTypeEQ(mcpinteraction.InteractionTypeToolCall)).
		All(context.Background())
	require.NoError(t, err)
	var names []string
	for _, c := range calls {
		if c.ToolName != nil {
			names = append(names, *c.ToolName)
		}
	}
	return names
}

func finalAnalysisContent(t *testing.T, app *TestApp, executionID string) string {
	t.Helper()
	memo, err := app.EntClient.TimelineEvent.Query().
		Where(timelineevent.ExecutionIDEQ(executionID), timelineevent.EventTypeEQ(timelineevent.EventTypeFinalAnalysis)).
		Only(context.Background())
	require.NoError(t, err)
	return memo.Content
}

func TestE2E_PostInvestigation_RunsWhenScoringCompletesDuringShutdown(t *testing.T) {
	llm := NewScriptedLLMClient()
	scriptScoredSession(llm)
	// Hold the scoring evaluation (first of the two scoring calls) until shutdown has started.
	release := make(chan struct{})
	scoringBlocked := make(chan struct{}, 1)
	scoringEval := &llm.sequential[len(llm.sequential)-2]
	scoringEval.WaitCh = release
	scoringEval.OnBlock = scoringBlocked
	llm.AddRouted("PostInvestigator", LLMScriptEntry{
		Text: "Decision: no ticket needed. Evidence: the OOM is already tracked.\n\nNO",
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
	select {
	case <-scoringBlocked:
	case <-time.After(30 * time.Second):
		t.Fatal("scoring did not reach the LLM call")
	}

	// Shut down as cmd/tarsy does: worker pool first, then the scoring and
	// post-investigation drains.
	app.WorkerPool.Stop()
	queueCfg := *app.Config.Queue
	queueCfg.ScoringShutdownTimeout = 20 * time.Second
	queueCfg.PostInvestigationShutdownTimeout = 20 * time.Second
	drained := make(chan struct{})
	go func() {
		queue.DrainScoringAndPostInvestigation(context.Background(), &queueCfg, app.ScoringExecutor, app.PostInvestigationExecutor)
		close(drained)
	}()

	// Let the drain start, then let scoring finish inside its drain window.
	time.Sleep(500 * time.Millisecond)
	close(release)

	select {
	case <-drained:
	case <-time.After(60 * time.Second):
		t.Fatal("drain did not finish")
	}

	score, err := app.EntClient.SessionScore.Query().
		Where(sessionscore.SessionIDEQ(sessionID)).
		Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, sessionscore.StatusCompleted, score.Status)

	stg := app.waitForPostInvestigationStage(t, sessionID, "file-ticket")
	assert.Equal(t, stage.StatusCompleted, stg.Status)
	require.Len(t, stg.Edges.AgentExecutions, 1)
	assert.Equal(t, agentexecution.StatusCompleted, stg.Edges.AgentExecutions[0].Status)
}
