package e2e

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/ent/agentexecution"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/stage"
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
	_, err = dbClient.DB().ExecContext(ctx,
		`ALTER TABLE investigation_memories ADD COLUMN IF NOT EXISTS search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', content)) STORED`)
	require.NoError(t, err)
	_, err = dbClient.DB().ExecContext(ctx,
		`ALTER TABLE alert_sessions ADD COLUMN IF NOT EXISTS search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', alert_data)) STORED`)
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
	err = memSvc.ApplyReflectorActions(ctx, "default", sourceSession.ID, &alertType, &chainID,
		&memory.ReflectorResult{Create: []memory.ReflectorCreateAction{
			{Content: "Check PgBouncer connection pool health before investigating query latency", Category: "procedural", Valence: "positive"},
			{Content: "Normal error rate for batch-processor during 2-4am is ~200/hr", Category: "semantic", Valence: "neutral"},
		}})
	require.NoError(t, err)

	// Also seed a memory that won't be auto-injected (limit=5 but only 2 seeded)
	// but can be found via the recall tool.
	err = memSvc.ApplyReflectorActions(ctx, "default", sourceSession.ID, &alertType, &chainID,
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
		"[procedural, positive, score:",
		"seeded procedural memory should be in investigation prompt with score")
	assertSystemPromptContains(t, investigatorInput,
		"Check PgBouncer connection pool health",
		"seeded procedural memory content should be in investigation prompt")
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

// TestE2E_MemoryReflectorCreation verifies the full memory creation pipeline:
// investigation → scoring → reflector extracts learnings → memories stored in DB.
//
// LLM call sequence:
//  1. Investigation: tool call + final answer (2 calls)
//  2. Executive summary (1 call)
//  3. Scoring: score evaluation + tool improvement (2 calls)
//  4. Reflector: extracts learnings → returns JSON with create actions (1 call)
func TestE2E_MemoryReflectorCreation(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)
	ctx := t.Context()

	llm := NewScriptedLLMClient()

	// Investigation: tool call + final answer.
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
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOMKilled due to memory pressure. Recommend increasing memory limit."})

	// Scoring turn 1: score evaluation.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Dimension Assessments\n\n" +
				"**Investigation Outcome:** Correct conclusion.\n\n" +
				"**Evidence Gathering:** incomplete_evidence — no memory metrics checked.\n\n" +
				"## Overall Assessment\n\n" +
				"Correct conclusion with gaps in evidence.\n\n70"},
			&agent.UsageChunk{InputTokens: 400, OutputTokens: 80, TotalTokens: 480},
		},
	})
	// Scoring turn 2: tool improvement report.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Missing Tools\n\n1. **get_memory_metrics** — Fetch memory usage time series."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 60, TotalTokens: 560},
		},
	})

	// Reflector: returns JSON with memory create actions.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: `{"create":[` +
				`{"content":"When investigating OOMKilled pods, always check memory metrics and resource limits before concluding","category":"procedural","valence":"positive"},` +
				`{"content":"Pod restarts above 3 in a short window strongly correlate with OOM kills","category":"semantic","valence":"neutral"}` +
				`],"reinforce":[],"deprecate":[]}`},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 100, TotalTokens: 700},
		},
	})

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`
	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory-reflector")),
		WithDBClient(dbClient),
		WithLLMClient(llm),
		WithMemoryService(memSvc, memCfg),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-memory-reflector", "Pod OOMKilled in production")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for the scoring stage to complete (includes reflector extraction).
	require.Eventually(t, func() bool {
		stgs, qErr := app.EntClient.Stage.Query().
			Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
			All(context.Background())
		if qErr != nil || len(stgs) == 0 {
			return false
		}
		return stgs[0].Status == stage.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "scoring stage did not complete")

	// Verify: memories were created in the DB by the reflector.
	memories, err := app.EntClient.InvestigationMemory.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, memories, 2, "reflector should have created 2 memories")

	contents := make([]string, len(memories))
	for i, m := range memories {
		contents[i] = m.Content
	}
	sort.Strings(contents)
	assert.Equal(t, "Pod restarts above 3 in a short window strongly correlate with OOM kills", contents[0])
	assert.Equal(t, "When investigating OOMKilled pods, always check memory metrics and resource limits before concluding", contents[1])

	// Verify memory metadata.
	for _, m := range memories {
		assert.Equal(t, "default", m.Project)
		assert.Equal(t, sessionID, m.SourceSessionID, "memory source_session_id should point to the scored session")
		assert.False(t, m.Deprecated)
	}
}

// TestE2E_MemoryFeedbackReflector verifies the full review → memory feedback flow:
// investigation → scoring → reflector (initial memories) → human review with
// feedback → confidence adjustment + feedback reflector (new memories).
//
// LLM call sequence:
//  1. Investigation: tool call + final answer (2 calls)
//  2. Executive summary (1 call)
//  3. Scoring: score evaluation + tool improvement (2 calls)
//  4. Scoring reflector: initial memory extraction (1 call)
//  5. Feedback reflector: triggered by human review (1 call)
func TestE2E_MemoryFeedbackReflector(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)
	ctx := t.Context()

	llm := NewScriptedLLMClient()

	// Investigation: tool call + final answer.
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
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOMKilled due to memory pressure. Recommend increasing memory limit."})

	// Scoring turn 1: score evaluation.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Dimension Assessments\n\n" +
				"**Investigation Outcome:** Correct conclusion.\n\n" +
				"**Evidence Gathering:** incomplete_evidence — no memory metrics checked.\n\n" +
				"## Overall Assessment\n\nCorrect conclusion with gaps.\n\n70"},
			&agent.UsageChunk{InputTokens: 400, OutputTokens: 80, TotalTokens: 480},
		},
	})
	// Scoring turn 2: tool improvement report.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Missing Tools\n\n1. **get_memory_metrics** — Fetch memory usage."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 60, TotalTokens: 560},
		},
	})

	// Scoring reflector: initial memory extraction.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: `{"create":[` +
				`{"content":"OOMKilled pods need memory metrics verification","category":"procedural","valence":"positive"}` +
				`],"reinforce":[],"deprecate":[]}`},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 80, TotalTokens: 680},
		},
	})

	// Feedback reflector: triggered after human review. Creates a new memory
	// and reinforces the one from the scoring reflector.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: `{"create":[` +
				`{"content":"Always check Kubernetes resource limits before OOM diagnosis","category":"procedural","valence":"positive"}` +
				`],"reinforce":[],"deprecate":[]}`},
			&agent.UsageChunk{InputTokens: 700, OutputTokens: 90, TotalTokens: 790},
		},
	})

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`
	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory-reflector")),
		WithDBClient(dbClient),
		WithLLMClient(llm),
		WithMemoryService(memSvc, memCfg),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-memory-reflector", "Pod OOMKilled in production")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for scoring stage to complete (includes initial reflector extraction).
	require.Eventually(t, func() bool {
		stgs, qErr := app.EntClient.Stage.Query().
			Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
			All(context.Background())
		if qErr != nil || len(stgs) == 0 {
			return false
		}
		return stgs[0].Status == stage.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "scoring stage did not complete")

	// Snapshot initial memories created by the scoring reflector.
	initialMemories, err := memSvc.GetBySessionID(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, initialMemories, 1, "scoring reflector should have created 1 memory")
	initialMem := initialMemories[0]
	assert.Equal(t, "OOMKilled pods need memory metrics verification", initialMem.Content)
	initialConfidence := initialMem.Confidence

	// ── Submit human review with feedback ──
	app.PatchReview(t, sessionID, map[string]interface{}{
		"action":                 "complete",
		"quality_rating":         "partially_accurate",
		"action_taken":           "Needs more evidence on resource limits.",
		"investigation_feedback": "The investigation missed checking Kubernetes resource limits. Always verify limits before diagnosing OOM.",
	})

	// Wait for feedback reflector to complete (creates a FeedbackReflector execution).
	require.Eventually(t, func() bool {
		execs, qErr := app.EntClient.AgentExecution.Query().
			Where(
				agentexecution.SessionIDEQ(sessionID),
				agentexecution.AgentNameEQ("FeedbackReflector"),
			).
			All(context.Background())
		if qErr != nil || len(execs) == 0 {
			return false
		}
		return execs[0].Status == agentexecution.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "feedback reflector execution did not complete")

	// ── Assert: confidence was adjusted (partially_accurate → 0.6x) ──
	adjustedMem, err := memSvc.GetByID(ctx, initialMem.ID)
	require.NoError(t, err)
	assert.InDelta(t, initialConfidence*0.6, adjustedMem.Confidence, 0.01,
		"partially_accurate review should degrade confidence by 0.6x")

	// ── Assert: feedback reflector created a new memory at 0.9 confidence ──
	allMemories, err := memSvc.GetBySessionID(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, allMemories, 2, "should have 1 initial + 1 feedback memory")

	var feedbackMem *memory.Detail
	for _, m := range allMemories {
		if m.ID != initialMem.ID {
			feedbackMem = &m
			break
		}
	}
	require.NotNil(t, feedbackMem, "should find the feedback-created memory")
	assert.Equal(t, "Always check Kubernetes resource limits before OOM diagnosis", feedbackMem.Content)
	assert.InDelta(t, 0.9, feedbackMem.Confidence, 0.01, "feedback memories should have 0.9 confidence")

	// ── Assert: FeedbackReflector execution is on the scoring stage ──
	fbExecs, err := app.EntClient.AgentExecution.Query().
		Where(
			agentexecution.SessionIDEQ(sessionID),
			agentexecution.AgentNameEQ("FeedbackReflector"),
		).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, fbExecs, 1)

	scoringStages, err := app.EntClient.Stage.Query().
		Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, scoringStages, 1)
	assert.Equal(t, scoringStages[0].ID, fbExecs[0].StageID,
		"feedback reflector execution should be attached to the scoring stage")

	// ════════════════════════════════════════════════════════════
	// Golden files: timeline + trace list + per-interaction
	// ════════════════════════════════════════════════════════════

	const goldenScenario = "memory-feedback"

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

	// Timeline golden.
	execs := app.QueryExecutions(t, sessionID)
	agentIndex := BuildAgentNameIndex(execs)

	timeline := app.QueryTimeline(t, sessionID)
	projectedTimeline := make([]map[string]interface{}, len(timeline))
	for i, te := range timeline {
		projectedTimeline[i] = ProjectTimelineForGolden(te)
	}
	AnnotateTimelineWithAgent(projectedTimeline, timeline, agentIndex)
	SortTimelineProjection(projectedTimeline)
	AssertGoldenJSON(t, GoldenPath(goldenScenario, "timeline.golden"), projectedTimeline, normalizer)

	// Trace list golden.
	AssertGoldenJSON(t, GoldenPath(goldenScenario, "trace_list.golden"), traceList, normalizer)

	// Per-interaction golden files.
	type interactionEntry struct {
		Kind, ID, AgentName, CreatedAt, Label, ServerName string
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
					Kind: "llm", ID: li["id"].(string), AgentName: agentName,
					CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
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
					Kind: "mcp", ID: mi["id"].(string), AgentName: agentName,
					CreatedAt: mi["created_at"].(string), Label: label, ServerName: sn,
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
			Kind: "llm", ID: li["id"].(string), AgentName: "Session",
			CreatedAt: li["created_at"].(string), Label: li["interaction_type"].(string),
		})
	}

	iterationCounters := make(map[string]int)
	for idx, entry := range allInteractions {
		counterKey := entry.AgentName + "_" + entry.Label
		iterationCounters[counterKey]++
		count := iterationCounters[counterKey]

		label := strings.ReplaceAll(entry.Label, " ", "_")
		filename := fmt.Sprintf("%02d_%s_%s_%s_%d.golden", idx+1, entry.AgentName, entry.Kind, label, count)
		goldenPath := GoldenPath(goldenScenario, filepath.Join("trace_interactions", filename))

		if entry.Kind == "llm" {
			detail := app.GetLLMInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenLLMInteraction(t, goldenPath, detail, normalizer)
		} else {
			detail := app.GetMCPInteractionDetail(t, sessionID, entry.ID)
			AssertGoldenMCPInteraction(t, goldenPath, detail, normalizer)
		}
	}
}

// TestE2E_MemoryCRUDAPI verifies all memory HTTP endpoints through a full
// pipeline: investigation → scoring → reflector creates memories → exercise
// the GET/LIST/PATCH/DELETE API endpoints and session-scoped queries.
//
// Endpoints tested:
//   - GET  /api/v1/sessions/:id/memories         (session-extracted)
//   - GET  /api/v1/sessions/:id/injected-memories (session-injected)
//   - GET  /api/v1/memories                       (list with filters/pagination)
//   - GET  /api/v1/memories/:id                   (single)
//   - PATCH /api/v1/memories/:id                  (update)
//   - DELETE /api/v1/memories/:id                 (delete)
func TestE2E_MemoryCRUDAPI(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)

	llm := NewScriptedLLMClient()

	// Investigation: tool call + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Checking pods."},
			&agent.TextChunk{Content: "Checking pods."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete: pod-1 is OOMKilled."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "Pod-1 OOMKilled."})

	// Scoring turn 1 + turn 2.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Overall\nDecent investigation.\n\n75"},
			&agent.UsageChunk{InputTokens: 400, OutputTokens: 80, TotalTokens: 480},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "## Missing Tools\nNone."},
			&agent.UsageChunk{InputTokens: 500, OutputTokens: 40, TotalTokens: 540},
		},
	})

	// Reflector: creates two memories with different categories.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: `{"create":[` +
				`{"content":"Check resource limits when pods OOMKill","category":"procedural","valence":"positive"},` +
				`{"content":"Batch processor normal error rate is 200/hr","category":"semantic","valence":"neutral"}` +
				`],"reinforce":[],"deprecate":[]}`},
			&agent.UsageChunk{InputTokens: 600, OutputTokens: 100, TotalTokens: 700},
		},
	})

	podsResult := `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`
	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory-reflector")),
		WithDBClient(dbClient),
		WithLLMClient(llm),
		WithMemoryService(memSvc, memCfg),
		WithMCPServers(map[string]map[string]mcpsdk.ToolHandler{
			"test-mcp": {
				"get_pods": StaticToolHandler(podsResult),
			},
		}),
	)

	resp := app.SubmitAlert(t, "test-memory-reflector", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Wait for scoring + reflector.
	require.Eventually(t, func() bool {
		stgs, qErr := app.EntClient.Stage.Query().
			Where(stage.SessionIDEQ(sessionID), stage.StageTypeEQ(stage.StageTypeScoring)).
			All(context.Background())
		if qErr != nil || len(stgs) == 0 {
			return false
		}
		return stgs[0].Status == stage.StatusCompleted
	}, 30*time.Second, 200*time.Millisecond, "scoring stage did not complete")

	// ════════════════════════════════════════════════════════════
	// A. GET /sessions/:id/memories — extracted from session
	// ════════════════════════════════════════════════════════════

	sessionMemories := app.GetSessionMemories(t, sessionID)
	require.Len(t, sessionMemories, 2, "reflector should have created 2 memories")

	memoryIDs := make([]string, 2)
	for i, raw := range sessionMemories {
		m := raw.(map[string]interface{})
		memoryIDs[i] = m["id"].(string)
		assert.Equal(t, sessionID, m["source_session_id"])
		assert.Equal(t, "default", m["project"])
		assert.Equal(t, false, m["deprecated"])
	}

	// ════════════════════════════════════════════════════════════
	// B. GET /sessions/:id/injected-memories — empty for first session
	// ════════════════════════════════════════════════════════════

	injected := app.GetInjectedMemories(t, sessionID)
	assert.Empty(t, injected, "first session should have no injected memories")

	// ════════════════════════════════════════════════════════════
	// C. GET /memories/:id — single memory detail
	// ════════════════════════════════════════════════════════════

	mem := app.GetMemory(t, memoryIDs[0])
	assert.Equal(t, memoryIDs[0], mem["id"])
	assert.NotEmpty(t, mem["content"])
	assert.NotEmpty(t, mem["category"])
	assert.NotEmpty(t, mem["created_at"])

	// ════════════════════════════════════════════════════════════
	// D. GET /memories — list with pagination and filters
	// ════════════════════════════════════════════════════════════

	listResp := app.ListMemories(t, "")
	assert.Equal(t, float64(2), listResp["total"])
	assert.Equal(t, float64(1), listResp["page"])
	memories := listResp["memories"].([]interface{})
	assert.Len(t, memories, 2)

	// Filter by category.
	procedural := app.ListMemories(t, "category=procedural")
	assert.Equal(t, float64(1), procedural["total"])
	pm := procedural["memories"].([]interface{})[0].(map[string]interface{})
	assert.Equal(t, "procedural", pm["category"])

	// Filter by source_session_id.
	bySession := app.ListMemories(t, "source_session_id="+sessionID)
	assert.Equal(t, float64(2), bySession["total"])

	// Pagination: page_size=1.
	page1 := app.ListMemories(t, "page_size=1&page=1")
	assert.Equal(t, float64(2), page1["total"])
	assert.Equal(t, float64(2), page1["total_pages"])
	assert.Len(t, page1["memories"].([]interface{}), 1)

	// ════════════════════════════════════════════════════════════
	// E. PATCH /memories/:id — update content and deprecated flag
	// ════════════════════════════════════════════════════════════

	updated := app.UpdateMemory(t, memoryIDs[0], map[string]interface{}{
		"content":    "Updated memory content",
		"deprecated": true,
	})
	assert.Equal(t, "Updated memory content", updated["content"])
	assert.Equal(t, true, updated["deprecated"])

	// Verify via GET.
	fetched := app.GetMemory(t, memoryIDs[0])
	assert.Equal(t, "Updated memory content", fetched["content"])
	assert.Equal(t, true, fetched["deprecated"])

	// Filter deprecated=true returns only the updated one.
	depList := app.ListMemories(t, "deprecated=true")
	assert.Equal(t, float64(1), depList["total"])

	// ════════════════════════════════════════════════════════════
	// F. DELETE /memories/:id — remove and verify 404
	// ════════════════════════════════════════════════════════════

	app.DeleteMemory(t, memoryIDs[1])

	// Should be gone — GET returns 404.
	app.GetMemoryExpectStatus(t, memoryIDs[1], http.StatusNotFound)

	// List should now have 1 memory total.
	afterDelete := app.ListMemories(t, "")
	assert.Equal(t, float64(1), afterDelete["total"])
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
// TestE2E_SearchPastSessionsInChat — full search_past_sessions pipeline.
//
// Pre-seeds a completed+reviewed session, then runs:
//   1. Investigation: tool call + final answer (2 LLM calls)
//   2. Executive summary (1 LLM call)
//   3. Chat: agent calls search_past_sessions (1 LLM call)
//   4. Summarization: internal LLM call by search_past_sessions (1 LLM call)
//   5. Chat: final answer after seeing search result (1 LLM call)
//
// Verifies:
//   - search_past_sessions tool in investigation and chat tool lists
//   - Summarization LLM call receives correct system prompt and session data
//   - Chat receives the summary as tool result
//   - Golden files capture the full trace
// ────────────────────────────────────────────────────────────

func TestE2E_SearchPastSessionsInChat(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)
	ctx := t.Context()

	// Pre-seed a completed session with searchable alert_data, review metadata,
	// and a follow-up chat exchange. The search_vector column (GENERATED STORED)
	// auto-computes from alert_data.
	seedSessionID := uuid.New().String()
	finalAnalysis := "nginx-proxy was restarting due to a misconfigured liveness probe. Fixed by adjusting the probe threshold."
	feedback := "Good investigation. Probe fix was correct."
	_, err := dbClient.Client.AlertSession.Create().
		SetID(seedSessionID).
		SetAlertData("nginx-proxy pod restarting in namespace coolify with CrashLoopBackOff").
		SetAgentType("test").
		SetAlertType("pod-restart").
		SetChainID("memory-chain").
		SetStatus("completed").
		SetFinalAnalysis(finalAnalysis).
		SetQualityRating(alertsession.QualityRatingAccurate).
		SetInvestigationFeedback(feedback).
		Save(ctx)
	require.NoError(t, err)

	// Seed a follow-up chat stage on the previous session so
	// FetchChatExchanges includes it in the search results.
	seedChatStage, err := dbClient.Client.Stage.Create().
		SetID(uuid.New().String()).
		SetSessionID(seedSessionID).
		SetStageName("Chat").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStageType(stage.StageTypeChat).
		SetStatus(stage.StatusCompleted).
		SetStartedAt(time.Now()).
		Save(ctx)
	require.NoError(t, err)
	seedExec, err := dbClient.Client.AgentExecution.Create().
		SetID(uuid.New().String()).
		SetStage(seedChatStage).
		SetSessionID(seedSessionID).
		SetAgentName("ChatAgent").
		SetAgentIndex(1).
		SetStatus(agentexecution.StatusCompleted).
		SetLlmBackend("test").
		Save(ctx)
	require.NoError(t, err)
	now := time.Now()
	_, err = dbClient.Client.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(seedSessionID).
		SetStageID(seedChatStage.ID).
		SetExecutionID(seedExec.ID).
		SetAgentExecution(seedExec).
		SetSequenceNumber(1).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetEventType("user_question").
		SetStatus("completed").
		SetContent("Which namespace was the liveness probe misconfigured in?").
		Save(ctx)
	require.NoError(t, err)
	_, err = dbClient.Client.TimelineEvent.Create().
		SetID(uuid.New().String()).
		SetSessionID(seedSessionID).
		SetStageID(seedChatStage.ID).
		SetExecutionID(seedExec.ID).
		SetAgentExecution(seedExec).
		SetSequenceNumber(2).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		SetEventType("final_analysis").
		SetStatus("completed").
		SetContent("The liveness probe was misconfigured in namespace coolify on the nginx-proxy deployment.").
		Save(ctx)
	require.NoError(t, err)

	llm := NewScriptedLLMClient()

	// Investigation: tool call + final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me check pod status."},
			&agent.TextChunk{Content: "Checking pod status."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "test-mcp__get_pods", Arguments: `{"namespace":"default"}`},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Pods are healthy."},
			&agent.TextChunk{Content: "Investigation complete: all pods running normally."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 50, TotalTokens: 250},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "All pods healthy. No issues detected."})

	// Chat iteration 1: agent calls search_past_sessions.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me search for past investigations involving nginx-proxy."},
			&agent.TextChunk{Content: "Searching past sessions for nginx-proxy."},
			&agent.ToolCallChunk{
				CallID:    "search-1",
				Name:      "search_past_sessions",
				Arguments: `{"query":"nginx-proxy"}`,
			},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Summarization: internal LLM call triggered by search_past_sessions.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "nginx-proxy was previously investigated for CrashLoopBackOff in namespace coolify. The root cause was a misconfigured liveness probe, resolved by adjusting the threshold. Investigation rated accurate."},
			&agent.UsageChunk{InputTokens: 300, OutputTokens: 60, TotalTokens: 360},
		},
	})

	// Chat iteration 2: final answer after seeing search result.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Past investigation shows nginx-proxy had a liveness probe issue."},
			&agent.TextChunk{Content: "nginx-proxy was previously investigated. The issue was a misconfigured liveness probe in namespace coolify, fixed by adjusting the threshold."},
			&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
		},
	})

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

	// Submit alert and wait for completion.
	resp := app.SubmitAlert(t, "memory-test", "High latency alert for web service")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Chat follow-up: triggers search_past_sessions.
	chatResp := app.SendChatMessage(t, sessionID, "Has nginx-proxy been investigated before?")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	// ════════════════════════════════════════════════════════════
	// A. Behavioral assertions via CapturedInputs
	// ════════════════════════════════════════════════════════════

	captured := llm.CapturedInputs()
	// Investigation: 2 + exec_summary: 1 + chat: 1 + summarization: 1 + chat: 1 = 6
	require.Equal(t, 6, llm.CallCount(), "expected 6 LLM calls total")

	// A1. Investigation: both memory tools in tool list.
	investigatorInput := captured[0]
	assertHasTool(t, investigatorInput, "search_past_sessions")
	assertHasTool(t, investigatorInput, "recall_past_investigations")

	// A2. Chat: search_past_sessions in tool list.
	chatInput := captured[3]
	assertHasTool(t, chatInput, "search_past_sessions")

	// A3. Summarization call: correct system prompt.
	summarizationInput := captured[4]
	assertSystemPromptContains(t, summarizationInput,
		"summarization assistant for TARSy",
		"summarization call should have the correct system prompt")

	// A4. Summarization user prompt contains seeded session data.
	var summarizationUserPrompt string
	for _, m := range summarizationInput.Messages {
		if m.Role == agent.RoleUser {
			summarizationUserPrompt = m.Content
			break
		}
	}
	require.NotEmpty(t, summarizationUserPrompt, "summarization should have a user prompt")
	assert.Contains(t, summarizationUserPrompt, "nginx-proxy",
		"summarization prompt should contain the search query")
	assert.Contains(t, summarizationUserPrompt, "nginx-proxy pod restarting in namespace coolify",
		"summarization prompt should contain seeded alert_data")
	assert.Contains(t, summarizationUserPrompt, "misconfigured liveness probe",
		"summarization prompt should contain final_analysis")
	assert.Contains(t, summarizationUserPrompt, "accurate",
		"summarization prompt should contain quality_rating")
	assert.Contains(t, summarizationUserPrompt, "Good investigation",
		"summarization prompt should contain investigation_feedback")
	assert.Contains(t, summarizationUserPrompt, "Follow-up conversations (1):",
		"summarization prompt should contain follow-up chat exchange count")
	assert.Contains(t, summarizationUserPrompt, "Which namespace was the liveness probe misconfigured in?",
		"summarization prompt should contain the follow-up question")
	assert.Contains(t, summarizationUserPrompt, "misconfigured in namespace coolify on the nginx-proxy deployment",
		"summarization prompt should contain the follow-up answer")

	// A5. Chat iteration 2: search_past_sessions tool result in conversation.
	chatIter2 := captured[5]
	searchResult := findToolResultMessage(chatIter2, "search_past_sessions")
	require.NotNil(t, searchResult, "chat iter 2 should have search_past_sessions tool result")
	assert.Contains(t, searchResult.Content, "nginx-proxy was previously investigated",
		"search result should contain the LLM summary")

	// ════════════════════════════════════════════════════════════
	// B. Golden files: timeline + trace
	// ════════════════════════════════════════════════════════════

	AssertSessionTraceGoldens(t, app, sessionID, "memory-session-search")
}

// ────────────────────────────────────────────────────────────
// TestE2E_SearchPastSessionsNoMatches — search_past_sessions returns no hits.
//
// No matching sessions in the DB. Chat agent calls search_past_sessions,
// which returns "No matching sessions found" without triggering summarization.
//
// LLM call sequence:
//   1. Investigation: final answer (1 call)
//   2. Executive summary (1 call)
//   3. Chat: agent calls search_past_sessions (1 call)
//   4. Chat: final answer (1 call)
//   No summarization call — no matches to summarize.
// ────────────────────────────────────────────────────────────

func TestE2E_SearchPastSessionsNoMatches(t *testing.T) {
	dbClient := testdb.NewTestClient(t)
	memSvc, memCfg := setupMemoryService(t, dbClient, 3)

	llm := NewScriptedLLMClient()

	// Investigation: single iteration, no tool calls.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Quick investigation."},
			&agent.TextChunk{Content: "Investigation complete: no issues found."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Executive summary.
	llm.AddSequential(LLMScriptEntry{Text: "No issues found."})

	// Chat iteration 1: agent calls search_past_sessions with non-matching query.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Let me search for past investigations involving nonexistent-service."},
			&agent.TextChunk{Content: "Searching for nonexistent-service."},
			&agent.ToolCallChunk{
				CallID:    "search-1",
				Name:      "search_past_sessions",
				Arguments: `{"query":"nonexistent-service-xyz"}`,
			},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 30, TotalTokens: 130},
		},
	})

	// Chat iteration 2: agent responds after seeing "no matches" result.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "No past sessions found."},
			&agent.TextChunk{Content: "No previous investigations found for nonexistent-service-xyz."},
			&agent.UsageChunk{InputTokens: 150, OutputTokens: 30, TotalTokens: 180},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "memory")),
		WithDBClient(dbClient),
		WithLLMClient(llm),
		WithMemoryService(memSvc, memCfg),
	)

	resp := app.SubmitAlert(t, "memory-test", "Test alert for no-match scenario")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Chat follow-up: search_past_sessions with no matching data.
	chatResp := app.SendChatMessage(t, sessionID, "Was nonexistent-service-xyz investigated before?")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	// ════════════════════════════════════════════════════════════
	// A. Behavioral assertions via CapturedInputs
	// ════════════════════════════════════════════════════════════

	captured := llm.CapturedInputs()
	// Investigation: 1 + exec_summary: 1 + chat: 1 + chat: 1 = 4 (no summarization)
	require.Equal(t, 4, llm.CallCount(), "expected 4 LLM calls total (no summarization)")

	// Chat iteration 2 should have the "no matches" tool result.
	chatIter2 := captured[3]
	searchResult := findToolResultMessage(chatIter2, "search_past_sessions")
	require.NotNil(t, searchResult, "chat iter 2 should have search_past_sessions tool result")
	assert.Equal(t, "No matching sessions found for this query.", searchResult.Content,
		"should get the exact no-matches message")

	// ════════════════════════════════════════════════════════════
	// B. Golden files: timeline + trace
	// ════════════════════════════════════════════════════════════

	AssertSessionTraceGoldens(t, app, sessionID, "memory-session-search-no-match")
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
