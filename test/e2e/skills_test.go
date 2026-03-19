package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// TestE2E_SkillsRequiredAndOnDemand — comprehensive happy path.
//
// Two-stage chain:
//  1. investigation (SkillInvestigator) — load_skill + MCP tool + final answer
//  2. remediation   (SkillRemediator)   — final answer only (no load_skill)
//  + Executive summary
//  + Chat follow-up with load_skill (exercises chat_executor.go wiring)
//
// SkillInvestigator: explicit allowlist + required_skills
//   - kubernetes-basics body injected into prompt (Tier 2.5)
//   - networking available on-demand via load_skill
//
// SkillRemediator: nil allowlist (all registry skills on-demand)
//
// Verifies: prompt content, timeline events, trace interactions (golden files),
// WS events, DB state, API responses, chat with skills.
// ────────────────────────────────────────────────────────────

func TestE2E_SkillsRequiredAndOnDemand(t *testing.T) {
	llm := NewScriptedLLMClient()

	// ── Stage 1: investigation (SkillInvestigator) ──

	// Iteration 1: thinking + response + load_skill tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me load the networking skill for this investigation."},
			&agent.TextChunk{Content: "Loading networking knowledge."},
			&agent.ToolCallChunk{CallID: "skill-call-1", Name: "load_skill", Arguments: `{"names":["networking"]}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	// Iteration 2: thinking + response + MCP tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Now let me check the pod status."},
			&agent.TextChunk{Content: "Checking pods."},
			&agent.ToolCallChunk{CallID: "mcp-call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	// Iteration 3: thinking + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Pod is OOMKilled. The networking skill confirms connectivity is fine."},
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled. Network connectivity verified."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 40, TotalTokens: 190},
		},
	})

	// ── Stage 2: remediation (SkillRemediator) — single iteration, no tool calls ──
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "The investigation found OOM issues. Recommending memory increase."},
			&agent.TextChunk{Content: "Remediation: increase memory limit for pod-1 to 1Gi."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Pod-1 OOM killed. Memory limit increase to 1Gi recommended.",
	})

	// ── Chat: load_skill for kubernetes-basics + final answer ──
	// Iteration 1: load_skill tool call.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me load the kubernetes-basics skill to answer this question."},
			&agent.TextChunk{Content: "Loading Kubernetes knowledge."},
			&agent.ToolCallChunk{CallID: "chat-skill-1", Name: "load_skill", Arguments: `{"names":["kubernetes-basics"]}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 25, TotalTokens: 225},
		},
	})
	// Iteration 2: final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Based on the skill content, pods are the smallest unit."},
			&agent.TextChunk{Content: "A pod is the smallest deployable unit in Kubernetes. The OOMKilled pod needs its memory limit increased."},
			&agent.UsageChunk{InputTokens: 250, OutputTokens: 40, TotalTokens: 290},
		},
	})

	podsResult := `[{"name":"pod-1","namespace":"default","status":"OOMKilled","restarts":5}]`

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "skills")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	// Connect WS and subscribe.
	ctx := context.Background()
	ws, err := WSConnect(ctx, app.WSURL)
	require.NoError(t, err)
	defer ws.Close()

	// Submit alert.
	resp := app.SubmitAlert(t, "test-skills", "Pod OOMKilled in production")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	require.NoError(t, ws.Subscribe("session:"+sessionID))

	// Wait for session completion.
	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Chat follow-up ──
	chatResp := app.SendChatMessage(t, sessionID, "What is a Kubernetes pod?")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	// Wait for the chat WS event before asserting.
	ws.WaitForEvent(t, func(e WSEvent) bool {
		return e.Type == "stage.status" &&
			e.Parsed["stage_id"] == chatStageID &&
			e.Parsed["status"] == "completed"
	}, 5*time.Second, "expected chat stage.status completed WS event")

	// ════════════════════════════════════════════════════════════
	// A. Prompt content (via CapturedInputs)
	// ════════════════════════════════════════════════════════════

	captured := llm.CapturedInputs()
	// Pipeline: 3 (investigation) + 1 (remediation) + 1 (exec_summary) + 2 (chat) = 7
	require.Equal(t, 7, llm.CallCount(), "expected 7 LLM calls total")

	// A1. SkillInvestigator's first call: required skill in prompt.
	investigatorInput := captured[0]
	assertSystemPromptContains(t, investigatorInput, "## Pre-loaded Skills",
		"required skills section header should be in system prompt")
	assertSystemPromptContains(t, investigatorInput, "### kubernetes-basics",
		"required skill heading should be in system prompt")
	assertSystemPromptContains(t, investigatorInput, "A pod is the smallest deployable unit",
		"required skill body should be in system prompt")

	// A2. On-demand catalog in prompt.
	assertSystemPromptContains(t, investigatorInput, "## Available Skills",
		"on-demand catalog header should be in system prompt")
	assertSystemPromptContains(t, investigatorInput, "networking",
		"networking skill should appear in on-demand catalog")
	assertSystemPromptContains(t, investigatorInput, "load_skill",
		"load_skill nudge should be in system prompt")

	// A3. load_skill tool in tool list.
	assertHasTool(t, investigatorInput, "load_skill")

	// A4. load_skill tool result in conversation (iteration 2 input).
	investigatorIter2 := captured[1]
	assertToolResultContains(t, investigatorIter2, "load_skill",
		"DNS Resolution", "networking skill body should be in tool result")
	assertToolResultContains(t, investigatorIter2, "load_skill",
		"TCP Connectivity", "networking skill body should be in tool result")

	// A5. SkillRemediator's first call: nil-allowlist → both skills in catalog.
	remediatorInput := captured[3]
	assertSystemPromptContains(t, remediatorInput, "## Available Skills",
		"SkillRemediator should have on-demand catalog")
	assertSystemPromptContains(t, remediatorInput, "kubernetes-basics",
		"kubernetes-basics should be in SkillRemediator catalog (nil allowlist)")
	assertSystemPromptContains(t, remediatorInput, "networking",
		"networking should be in SkillRemediator catalog (nil allowlist)")
	assertHasTool(t, remediatorInput, "load_skill")

	// A6. Chat agent's first call: skills in prompt + tool list.
	chatInput := captured[5]
	assertSystemPromptContains(t, chatInput, "## Available Skills",
		"chat agent should have on-demand catalog")
	assertHasTool(t, chatInput, "load_skill")

	// A7. Chat load_skill result in iteration 2.
	chatIter2 := captured[6]
	assertToolResultContains(t, chatIter2, "load_skill",
		"A pod is the smallest deployable unit", "kubernetes-basics body in chat tool result")

	// ════════════════════════════════════════════════════════════
	// B. Timeline events (DB + API)
	// ════════════════════════════════════════════════════════════

	timeline := app.QueryTimeline(t, sessionID)
	assert.NotEmpty(t, timeline)

	// B1. Find load_skill timeline event.
	loadSkillEvent := findTimelineEvent(t, timeline, "load_skill")
	require.NotNil(t, loadSkillEvent, "should have a load_skill timeline event")
	assert.Equal(t, timelineevent.EventTypeLlmToolCall, loadSkillEvent.EventType)
	assert.Equal(t, timelineevent.StatusCompleted, loadSkillEvent.Status)
	assert.Contains(t, loadSkillEvent.Content, "DNS Resolution",
		"load_skill event content should contain skill body")
	toolName, _ := loadSkillEvent.Metadata["tool_name"].(string)
	assert.Equal(t, "load_skill", toolName)

	// B2. Find MCP tool call timeline event.
	mcpToolEvent := findTimelineEvent(t, timeline, "get_pods")
	require.NotNil(t, mcpToolEvent, "should have a get_pods timeline event")
	serverName, _ := mcpToolEvent.Metadata["server_name"].(string)
	assert.Equal(t, "test-mcp", serverName)

	// B3. API timeline count matches DB.
	apiTimeline := app.GetTimeline(t, sessionID)
	assert.Equal(t, len(timeline), len(apiTimeline),
		"API timeline event count must match DB query")

	// ════════════════════════════════════════════════════════════
	// C. Trace interactions (via Trace API + golden files)
	// ════════════════════════════════════════════════════════════

	traceList := app.GetTraceList(t, sessionID)
	traceStages, ok := traceList["stages"].([]interface{})
	require.True(t, ok, "stages should be an array")
	require.NotEmpty(t, traceStages)

	normalizer := NewNormalizer(sessionID)
	for _, rawStage := range traceStages {
		stg, _ := rawStage.(map[string]interface{})
		stageID, _ := stg["stage_id"].(string)
		normalizer.RegisterStageID(stageID)

		executions, _ := stg["executions"].([]interface{})
		for _, rawExec := range executions {
			exec, _ := rawExec.(map[string]interface{})
			execID, _ := exec["execution_id"].(string)
			normalizer.RegisterExecutionID(execID)

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

	// Session-level interactions (exec summary).
	traceSessionInteractions, _ := traceList["session_interactions"].([]interface{})
	for _, rawLI := range traceSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
	}

	// Golden: trace_list.
	AssertGoldenJSON(t, GoldenPath("skills", "trace_list.golden"), traceList, normalizer)

	// Golden: per-interaction trace details.
	type interactionEntry struct {
		Kind       string
		ID         string
		AgentName  string
		CreatedAt  string
		Label      string
		ServerName string
	}

	var allInteractions []interactionEntry
	for _, rawStage := range traceStages {
		stg, _ := rawStage.(map[string]interface{})
		for _, rawExec := range stg["executions"].([]interface{}) {
			exec, _ := rawExec.(map[string]interface{})
			agentName, _ := exec["agent_name"].(string)

			var execInteractions []interactionEntry
			for _, rawLI := range exec["llm_interactions"].([]interface{}) {
				li, _ := rawLI.(map[string]interface{})
				execInteractions = append(execInteractions, interactionEntry{
					Kind:      "llm",
					ID:        li["id"].(string),
					AgentName: agentName,
					CreatedAt: li["created_at"].(string),
					Label:     li["interaction_type"].(string),
				})
			}
			for _, rawMI := range exec["mcp_interactions"].([]interface{}) {
				mi, _ := rawMI.(map[string]interface{})
				label := mi["interaction_type"].(string)
				if tn, ok := mi["tool_name"].(string); ok && tn != "" {
					label = tn
				}
				sn, _ := mi["server_name"].(string)
				execInteractions = append(execInteractions, interactionEntry{
					Kind:       "mcp",
					ID:         mi["id"].(string),
					AgentName:  agentName,
					CreatedAt:  mi["created_at"].(string),
					Label:      label,
					ServerName: sn,
				})
			}
			sort.SliceStable(execInteractions, func(i, j int) bool {
				a, b := execInteractions[i], execInteractions[j]
				if a.CreatedAt != b.CreatedAt {
					return a.CreatedAt < b.CreatedAt
				}
				return a.ServerName < b.ServerName
			})
			allInteractions = append(allInteractions, execInteractions...)
		}
	}

	for _, rawLI := range traceSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		allInteractions = append(allInteractions, interactionEntry{
			Kind:      "llm",
			ID:        li["id"].(string),
			AgentName: "Session",
			CreatedAt: li["created_at"].(string),
			Label:     li["interaction_type"].(string),
		})
	}

	iterationCounters := make(map[string]int)
	for idx, entry := range allInteractions {
		counterKey := entry.AgentName + "_" + entry.Label
		iterationCounters[counterKey]++
		count := iterationCounters[counterKey]

		label := strings.ReplaceAll(entry.Label, " ", "_")
		filename := fmt.Sprintf("%02d_%s_%s_%s_%d.golden", idx+1, entry.AgentName, entry.Kind, label, count)
		goldenPath := GoldenPath("skills", filepath.Join("trace_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}

	// ════════════════════════════════════════════════════════════
	// D. WS expected events
	// ════════════════════════════════════════════════════════════

	wsEvents := ws.Events()
	AssertAllEventsHaveSessionID(t, wsEvents, sessionID)
	AssertEventsInOrder(t, wsEvents, testdata.SkillsExpectedEvents)

	// ════════════════════════════════════════════════════════════
	// E. DB + API state
	// ════════════════════════════════════════════════════════════

	// DB: stages — 3 pipeline + 1 chat = 4.
	stages := app.QueryStages(t, sessionID)
	require.Len(t, stages, 4)
	assert.Equal(t, "investigation", stages[0].StageName)
	assert.Equal(t, "remediation", stages[1].StageName)
	assert.Equal(t, "Executive Summary", stages[2].StageName)
	assert.Equal(t, "Chat", stages[3].StageName)

	// DB: executions — 3 pipeline + 1 chat = 4.
	execs := app.QueryExecutions(t, sessionID)
	require.Len(t, execs, 4)
	assert.Equal(t, "SkillInvestigator", execs[0].AgentName)
	assert.Equal(t, "SkillRemediator", execs[1].AgentName)
	assert.Equal(t, config.AgentNameExecSummary, execs[2].AgentName)
	assert.Equal(t, config.AgentNameChat, execs[3].AgentName)

	// API: session.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
	assert.NotEmpty(t, session["final_analysis"])

	// Golden: session.
	AssertGoldenJSON(t, GoldenPath("skills", "session.golden"), session, normalizer)
}

// ────────────────────────────────────────────────────────────
// TestE2E_SkillsPartialFailure — load_skill with mix of valid
// and invalid skill names returns partial success.
// ────────────────────────────────────────────────────────────

func TestE2E_SkillsPartialFailure(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Iteration 1: load_skill with one valid + one invalid name.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Loading skills."},
			&agent.ToolCallChunk{CallID: "skill-call-1", Name: "load_skill",
				Arguments: `{"names":["networking","nonexistent-skill"]}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Iteration 2: final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Got partial results."},
			&agent.TextChunk{Content: "Partial skill load completed. Proceeding with networking knowledge."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	// Remediation stage (2-stage chain — needs an entry).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "No remediation needed for partial skill test."},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation used partial skill loading."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "skills")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[]`)},
		}),
	)

	resp := app.SubmitAlert(t, "test-skills", "Test partial failure")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify tool result in conversation: contains skill body + partial failure note.
	captured := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(captured), 2)
	toolResult := findToolResultMessage(captured[1], "load_skill")
	require.NotNil(t, toolResult, "should have load_skill tool result in iteration 2")
	assert.Contains(t, toolResult.Content, "DNS Resolution",
		"partial success should include valid skill body")
	assert.Contains(t, toolResult.Content, "the following skill names were not found: nonexistent-skill",
		"partial success should note invalid names")

	// Verify timeline event.
	timeline := app.QueryTimeline(t, sessionID)
	loadSkillEvent := findTimelineEvent(t, timeline, "load_skill")
	require.NotNil(t, loadSkillEvent)
	assert.Contains(t, loadSkillEvent.Content, "DNS Resolution")
	assert.Contains(t, loadSkillEvent.Content, "nonexistent-skill")

	// is_error should be false (partial success, not full error).
	isErr, _ := loadSkillEvent.Metadata["is_error"].(bool)
	assert.False(t, isErr, "partial success should not be marked as error")

	// Session completed.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
}

// ────────────────────────────────────────────────────────────
// TestE2E_SkillsAllInvalid — load_skill when all requested
// skill names are invalid returns an error result.
// ────────────────────────────────────────────────────────────

func TestE2E_SkillsAllInvalid(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Iteration 1: load_skill with invalid name.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Loading skill."},
			&agent.ToolCallChunk{CallID: "skill-call-1", Name: "load_skill",
				Arguments: `{"names":["ghost"]}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Iteration 2: final answer despite error.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Skill not found. Proceeding without it."},
			&agent.TextChunk{Content: "Skill loading failed. Proceeding with available knowledge."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})
	// Remediation stage (2-stage chain — needs an entry).
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "No remediation needed for all-invalid skill test."},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
		},
	})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Investigation completed without requested skill."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "skills")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {"get_pods": StaticToolHandler(`[]`)},
		}),
	)

	resp := app.SubmitAlert(t, "test-skills", "Test all-invalid skills")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify tool result contains error message.
	captured := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(captured), 2)
	toolResult := findToolResultMessage(captured[1], "load_skill")
	require.NotNil(t, toolResult, "should have load_skill tool result in iteration 2")
	assert.Contains(t, toolResult.Content, "no valid skills found",
		"all-invalid should report no valid skills")

	// Verify timeline event: is_error should be true.
	timeline := app.QueryTimeline(t, sessionID)
	loadSkillEvent := findTimelineEvent(t, timeline, "load_skill")
	require.NotNil(t, loadSkillEvent)
	assert.Contains(t, loadSkillEvent.Content, "no valid skills found")
	isErr, _ := loadSkillEvent.Metadata["is_error"].(bool)
	assert.True(t, isErr, "all-invalid should be marked as error")

	// Session still completed.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
}

// ────────────────────────────────────────────────────────────
// TestE2E_SkillsOrchestratorSubAgent — skills propagate
// to orchestrator sub-agents via createSubAgentToolExecutor.
// ────────────────────────────────────────────────────────────

func TestE2E_SkillsOrchestratorSubAgent(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

	// ── SREOrchestrator LLM entries ──

	// Iteration 1: dispatch SkillWorker.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Dispatching SkillWorker."},
			&agent.ToolCallChunk{CallID: "orch-call-1", Name: "dispatch_agent",
				Arguments: `{"name":"SkillWorker","task":"Investigate network connectivity"}`},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})
	// Iteration 2: waiting for results.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Waiting for SkillWorker results."},
			&agent.TextChunk{Content: "Waiting for sub-agent."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 30, TotalTokens: 330},
		},
	})
	// Iteration 3: final answer.
	llm.AddRouted("SREOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "SkillWorker confirmed network is fine. OOM is the root cause."},
			&agent.TextChunk{Content: "Investigation complete: network connectivity verified. OOM is the root cause."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 50, TotalTokens: 550},
		},
	})

	// ── SkillWorker LLM entries ──

	// Iteration 1: load_skill for networking.
	llm.AddRouted("SkillWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Loading networking skill."},
			&agent.ToolCallChunk{CallID: "sw-skill-1", Name: "load_skill",
				Arguments: `{"names":["networking"]}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
		},
	})
	// Iteration 2: final answer.
	llm.AddRouted("SkillWorker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Network diagnostics look good based on skill knowledge."},
			&agent.TextChunk{Content: "Network connectivity verified. DNS and TCP checks pass."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
		},
	})

	// ── Executive summary ──
	llm.AddSequential(LLMScriptEntry{
		Text: "Network verified healthy. OOM is root cause.",
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "skills-orchestrator")),
		WithLLMClient(llm),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(`[{"name":"pod-1","status":"OOMKilled"}]`),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-skills-orchestrator", "Network connectivity check needed")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		<-orchIter2Ready
		baseline, err := app.CountLLMInteractions(sessionID)
		if err != nil {
			t.Errorf("CountLLMInteractions failed: %v", err)
		}
		close(orchIter2Gate)
		if !app.AwaitLLMInteractionIncrease(sessionID, baseline) {
			t.Errorf("AwaitLLMInteractionIncrease timed out (baseline=%d)", baseline)
		}
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify sub-agent had load_skill in its tool list and skill catalog in prompt.
	captured := llm.CapturedInputs()
	var skillWorkerInput *agent.GenerateInput
	for _, input := range captured {
		if hasAgentInPrompt(input, "SkillWorker") {
			skillWorkerInput = input
			break
		}
	}
	require.NotNil(t, skillWorkerInput, "should find SkillWorker's LLM input")
	assertSystemPromptContains(t, skillWorkerInput, "## Available Skills",
		"sub-agent should have skill catalog")
	assertSystemPromptContains(t, skillWorkerInput, "networking",
		"networking should appear in sub-agent catalog")
	assertHasTool(t, skillWorkerInput, "load_skill")

	// Verify load_skill tool result was received by SkillWorker.
	var skillWorkerIter2 *agent.GenerateInput
	for _, input := range captured {
		if hasToolResult(input, "load_skill") {
			skillWorkerIter2 = input
			break
		}
	}
	require.NotNil(t, skillWorkerIter2, "should find SkillWorker iteration with load_skill result")
	assertToolResultContains(t, skillWorkerIter2, "load_skill",
		"DNS Resolution", "sub-agent should receive skill body")

	// Verify timeline has load_skill event.
	timeline := app.QueryTimeline(t, sessionID)
	loadSkillEvent := findTimelineEvent(t, timeline, "load_skill")
	require.NotNil(t, loadSkillEvent, "should have load_skill timeline event from sub-agent")
	assert.Contains(t, loadSkillEvent.Content, "DNS Resolution")

	// Verify trace API shows the sub-agent's interactions.
	// In orchestrator traces, sub-agents are nested under the orchestrator
	// execution's "sub_agents" field, not as top-level executions.
	traceList := app.GetTraceList(t, sessionID)
	traceStages, ok := traceList["stages"].([]interface{})
	require.True(t, ok)
	require.NotEmpty(t, traceStages)

	found := false
	for _, rawStage := range traceStages {
		stg, _ := rawStage.(map[string]interface{})
		executions, _ := stg["executions"].([]interface{})
		for _, rawExec := range executions {
			exec, _ := rawExec.(map[string]interface{})
			// Check sub_agents nested under orchestrator execution.
			subAgents, _ := exec["sub_agents"].([]interface{})
			for _, rawSub := range subAgents {
				sub, _ := rawSub.(map[string]interface{})
				mcpInteractions, _ := sub["mcp_interactions"].([]interface{})
				for _, rawMI := range mcpInteractions {
					mi, _ := rawMI.(map[string]interface{})
					if tn, _ := mi["tool_name"].(string); tn == "load_skill" {
						found = true
					}
				}
			}
		}
	}
	assert.True(t, found, "trace should contain load_skill MCP interaction from sub-agent")

	// Session completed.
	session := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", session["status"])
}

// ────────────────────────────────────────────────────────────
// Test helpers
// ────────────────────────────────────────────────────────────

// assertSystemPromptContains checks that any system message in the input contains substr.
func assertSystemPromptContains(t *testing.T, input *agent.GenerateInput, substr, msg string) {
	t.Helper()
	for _, m := range input.Messages {
		if m.Role == agent.RoleSystem && strings.Contains(m.Content, substr) {
			return
		}
	}
	t.Errorf("%s: no system message contains %q", msg, substr)
}

// assertHasTool checks that the input's tool list contains a tool with the given name.
func assertHasTool(t *testing.T, input *agent.GenerateInput, toolName string) {
	t.Helper()
	for _, tool := range input.Tools {
		if tool.Name == toolName {
			return
		}
	}
	t.Errorf("tool list should contain %q, got %d tools", toolName, len(input.Tools))
}

// assertToolResultContains checks that the input has a tool result for toolName containing substr.
func assertToolResultContains(t *testing.T, input *agent.GenerateInput, toolName, substr, msg string) {
	t.Helper()
	result := findToolResultMessage(input, toolName)
	if result == nil {
		t.Errorf("%s: no tool result message for %q found", msg, toolName)
		return
	}
	assert.Contains(t, result.Content, substr, msg)
}

// findToolResultMessage finds the first tool result message for the given tool name.
func findToolResultMessage(input *agent.GenerateInput, toolName string) *agent.ConversationMessage {
	for i := range input.Messages {
		if input.Messages[i].Role == agent.RoleTool && input.Messages[i].ToolName == toolName {
			return &input.Messages[i]
		}
	}
	return nil
}

// findTimelineEvent finds the first completed llm_tool_call event with the given tool_name in metadata.
func findTimelineEvent(t *testing.T, events []*ent.TimelineEvent, toolName string) *ent.TimelineEvent {
	t.Helper()
	for _, e := range events {
		if e.EventType != timelineevent.EventTypeLlmToolCall {
			continue
		}
		if e.Status != timelineevent.StatusCompleted {
			continue
		}
		if tn, ok := e.Metadata["tool_name"].(string); ok && tn == toolName {
			return e
		}
	}
	return nil
}

// hasAgentInPrompt checks if any system message contains the agent name.
func hasAgentInPrompt(input *agent.GenerateInput, agentName string) bool {
	for _, m := range input.Messages {
		if m.Role == agent.RoleSystem && strings.Contains(m.Content, agentName) {
			return true
		}
	}
	return false
}

// hasToolResult checks if the input has a tool result for the given tool name.
func hasToolResult(input *agent.GenerateInput, toolName string) bool {
	return findToolResultMessage(input, toolName) != nil
}
