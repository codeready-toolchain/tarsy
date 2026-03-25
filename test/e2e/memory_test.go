package e2e

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// TestE2E_MemoryInjectionAndRecall — comprehensive memory E2E test.
//
// Single-stage investigation chain + chat follow-up:
//   1. investigation (MemoryInvestigator) — tool call + final answer
//   + Chat: recall_past_investigations tool call
//
// Pre-seeds memories in the DB, then verifies:
//   - Tier 4 memory hints auto-injected into investigation prompt
//   - recall_past_investigations tool in tool list
//   - Injected memory IDs recorded in DB (Ent edge)
//   - Chat prompt does NOT get Tier 4 auto-injection
//   - Chat CAN use recall_past_investigations tool
//   - recall tool result is formatted correctly
//
// Designed for Phase 3 extensibility: scoring + reflector extraction
// can be layered on by enabling scoring in the config and adding
// reflector LLM script entries after the investigation completes.
// ────────────────────────────────────────────────────────────

// fakeEmbedder returns a fixed vector for all embedding calls.
// Dimension 3 keeps the test database setup lightweight.
type fakeEmbedder struct {
	vec []float32
}

func (f *fakeEmbedder) Embed(_ context.Context, _ string, _ memory.EmbeddingTask) ([]float32, error) {
	return f.vec, nil
}

// setupMemoryService creates a memory.Service backed by the test DB with a
// fixed-vector embedder. Adds the pgvector column that Ent doesn't manage.
func setupMemoryService(t *testing.T, dbClient *database.Client, dims int) (*memory.Service, *config.MemoryConfig) {
	t.Helper()
	ctx := t.Context()

	vec := make([]float32, dims)
	vec[0] = 1 // deterministic unit vector along first axis

	_, err := dbClient.DB().ExecContext(ctx,
		`ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS embedding vector(3)`)
	require.NoError(t, err)

	memCfg := &config.MemoryConfig{
		Enabled:   true,
		MaxInject: 5,
		Embedding: config.EmbeddingConfig{Dimensions: dims},
	}
	svc := memory.NewService(dbClient.Client, dbClient.DB(), &fakeEmbedder{vec: vec}, memCfg)
	return svc, memCfg
}

func TestE2E_MemoryInjectionAndRecall(t *testing.T) {
	// ── Setup: DB + memory service + seed memories ──

	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)
	ctx := t.Context()

	// Create a source session for FK references (memories need a source_session_id).
	sourceSession, err := dbClient.Client.AlertSession.Create().
		SetID(uuid.New().String()).
		SetAlertData("historical alert").
		SetAgentType("test").
		SetChainID("memory-chain").
		SetStatus("completed").
		Save(ctx)
	require.NoError(t, err)

	alertType := "memory-test"
	chainID := "memory-chain"

	// Seed memories that will be found by the investigation's similarity search.
	err = memSvc.ApplyReflectorActions(ctx, "default", sourceSession.ID, &alertType, &chainID, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Check PgBouncer connection pool health before investigating query latency", Category: "procedural", Valence: "positive"},
			{Content: "Normal error rate for batch-processor during 2-4am is ~200/hr", Category: "semantic", Valence: "neutral"},
		}})
	require.NoError(t, err)

	// Also seed a memory that won't be auto-injected (limit=5 but only 2 seeded)
	// but can be found via the recall tool.
	err = memSvc.ApplyReflectorActions(ctx, "default", sourceSession.ID, &alertType, &chainID, 80,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "container_memory_rss metric does not exist in this setup", Category: "procedural", Valence: "negative"},
		}})
	require.NoError(t, err)

	// ── Script LLM responses ──

	llm := NewScriptedLLMClient()

	// Stage 1 — investigation: tool call → final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check the pods."},
			&agent.TextChunk{Content: "Checking pod status."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Pods look fine. Investigation complete."},
			&agent.TextChunk{Content: "Investigation complete: all pods healthy."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 50, TotalTokens: 250},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "All pods healthy. No issues found."})

	// Chat — iteration 1: agent calls recall_past_investigations.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check past investigations for similar patterns."},
			&agent.TextChunk{Content: "Searching past investigations."},
			&agent.ToolCallChunk{
				CallID:    "recall-1",
				Name:      "recall_past_investigations",
				Arguments: `{"query":"pod health check patterns","limit":10}`,
			},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	// Chat — iteration 2: final answer after seeing recall results.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Past investigations suggest checking PgBouncer."},
			&agent.TextChunk{Content: "Based on past investigations, check PgBouncer connection pool health."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

	// ── Boot TestApp ──

	podsResult := `[{"name":"pod-1","status":"Running","restarts":0}]`
	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory")),
		WithDBClient(dbClient),
		WithLLMClient(llm),
		WithMemoryService(memSvc, memCfg),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	// ── Submit alert and wait for completion ──

	resp := app.SubmitAlert(t, "memory-test", "Pod latency alert in production")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Chat follow-up ──

	chatResp := app.SendChatMessage(t, sessionID, "What patterns have we seen before?")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	// ════════════════════════════════════════════════════════════
	// A. Quick behavioral assertions (via CapturedInputs)
	// ════════════════════════════════════════════════════════════

	captured := llm.CapturedInputs()
	// Investigation: 2 (tool call + answer) + exec_summary: 1 + chat: 2 (recall + answer) = 5
	require.Equal(t, 5, llm.CallCount(), "expected 5 LLM calls total")

	// A1. Investigation first call: Tier 4 memory hints in system prompt.
	investigatorInput := captured[0]
	assertSystemPromptContains(t, investigatorInput,
		"Lessons from Past Investigations",
		"Tier 4 memory section should be in investigation system prompt")
	assertSystemPromptContains(t, investigatorInput,
		"[procedural, positive] Check PgBouncer connection pool health",
		"seeded procedural memory should be in investigation prompt")
	assertHasTool(t, investigatorInput, "recall_past_investigations")

	// A2. Chat first call: NO Tier 4 auto-injection, but recall tool available.
	chatInput := captured[3]
	assertSystemPromptNotContains(t, chatInput,
		"Lessons from Past Investigations",
		"Tier 4 memory section should NOT be in chat system prompt")
	assertHasTool(t, chatInput, "recall_past_investigations")

	// A3. Chat iteration 2: recall tool result in conversation.
	chatIter2 := captured[4]
	recallResult := findToolResultMessage(chatIter2, "recall_past_investigations")
	require.NotNil(t, recallResult, "chat iter 2 should have recall_past_investigations tool result")
	assert.Contains(t, recallResult.Content, "relevant memories",
		"recall result should contain formatted memories")

	// ════════════════════════════════════════════════════════════
	// B. DB state: injected memory IDs recorded via Ent edge
	// ════════════════════════════════════════════════════════════

	session, err := app.EntClient.AlertSession.Get(ctx, sessionID)
	require.NoError(t, err)
	injectedMemories, err := session.QueryInjectedMemories().All(ctx)
	require.NoError(t, err)
	assert.Len(t, injectedMemories, 3, "all 3 seeded memories should be recorded as injected")

	var injectedContents []string
	for _, m := range injectedMemories {
		injectedContents = append(injectedContents, m.Content)
	}
	assert.Contains(t, injectedContents, "Check PgBouncer connection pool health before investigating query latency")
	assert.Contains(t, injectedContents, "Normal error rate for batch-processor during 2-4am is ~200/hr")

	// ════════════════════════════════════════════════════════════
	// C. Normalizer setup (shared by timeline + trace golden assertions)
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

			for _, rawLI := range exec["llm_interactions"].([]interface{}) {
				li, _ := rawLI.(map[string]interface{})
				if id, ok := li["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
			for _, rawMI := range exec["mcp_interactions"].([]interface{}) {
				mi, _ := rawMI.(map[string]interface{})
				if id, ok := mi["id"].(string); ok {
					normalizer.RegisterInteractionID(id)
				}
			}
		}
	}

	traceSessionInteractions, _ := traceList["session_interactions"].([]interface{})
	for _, rawLI := range traceSessionInteractions {
		li, _ := rawLI.(map[string]interface{})
		normalizer.RegisterInteractionID(li["id"].(string))
	}

	// ════════════════════════════════════════════════════════════
	// D. Timeline golden file
	// ════════════════════════════════════════════════════════════

	execs := app.QueryExecutions(t, sessionID)
	agentIndex := BuildAgentNameIndex(execs)

	timeline := app.QueryTimeline(t, sessionID)
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AnnotateTimelineWithAgent(projectedTimeline, timeline, agentIndex)
	SortTimelineProjection(projectedTimeline)
	AssertGoldenJSON(t, GoldenPath("memory", "timeline.golden"), projectedTimeline, normalizer)

	// ════════════════════════════════════════════════════════════
	// E. Trace golden files: exact LLM + MCP interaction verification
	// ════════════════════════════════════════════════════════════

	AssertGoldenJSON(t, GoldenPath("memory", "trace_list.golden"), traceList, normalizer)

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
		goldenPath := GoldenPath("memory", filepath.Join("trace_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}
}

// TestE2E_MemoryDisabled verifies that when memory is not configured,
// sessions complete normally without memory injection or the recall tool.
func TestE2E_MemoryDisabled(t *testing.T) {
	llm := NewScriptedLLMClient()

	// Single-iteration investigation.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Quick check."},
			&agent.TextChunk{Content: "Investigation complete."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "No issues found."})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory")),
		WithLLMClient(llm),
		// No WithMemoryService — memory is disabled despite config saying enabled.
	)

	resp := app.SubmitAlert(t, "memory-test", "Test alert")
	sessionID := resp["session_id"].(string)
	app.WaitForSessionStatus(t, sessionID, "completed")

	captured := llm.CapturedInputs()
	require.GreaterOrEqual(t, len(captured), 1)

	// No Tier 4 section.
	assertSystemPromptNotContains(t, captured[0],
		"Lessons from Past Investigations",
		"should not have memory section when memory service is nil")

	// recall_past_investigations tool should NOT be in the tool list.
	assertDoesNotHaveTool(t, captured[0], "recall_past_investigations")
}

// ────────────────────────────────────────────────────────────
// Test helpers (memory-specific)
// ────────────────────────────────────────────────────────────

func assertSystemPromptNotContains(t *testing.T, input *agent.GenerateInput, substr, msg string) {
	t.Helper()
	for _, m := range input.Messages {
		if m.Role == agent.RoleSystem && strings.Contains(m.Content, substr) {
			t.Errorf("%s: system message should NOT contain %q", msg, substr)
			return
		}
	}
}

func assertDoesNotHaveTool(t *testing.T, input *agent.GenerateInput, toolName string) {
	t.Helper()
	for _, tool := range input.Tools {
		if tool.Name == toolName {
			t.Errorf("tool list should NOT contain %q", toolName)
			return
		}
	}
}
