package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// Dashboard API test — exercises session list, detail, summary,
// filter options, system info, and health endpoints after three
// completed single-stage pipeline runs with distinct payloads
// and token profiles. Uses the concurrency config (simplest
// chain: one stage, one agent, max_iterations=1).
// ────────────────────────────────────────────────────────────

func TestDashboardEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// ── Per-session expected data ──
	type sessionSpec struct {
		alertData               string
		investText              string // investigation LLM response → final_analysis
		summaryText             string // executive summary LLM response → executive_summary
		invIn, invOut, invTotal int
		sumIn, sumOut, sumTotal int
	}

	specs := []sessionSpec{
		{
			alertData:   "Session Alpha payload",
			investText:  "Alpha investigation: CPU spike detected on node-1.",
			summaryText: "Alpha summary: CPU spike resolved.",
			invIn:       100, invOut: 50, invTotal: 150,
			sumIn: 30, sumOut: 10, sumTotal: 40,
		},
		{
			alertData:   "Session Beta payload",
			investText:  "Beta investigation: memory pressure on pod-xyz.",
			summaryText: "Beta summary: OOM risk mitigated.",
			invIn:       200, invOut: 100, invTotal: 300,
			sumIn: 40, sumOut: 20, sumTotal: 60,
		},
		{
			alertData:   "Session Charlie payload",
			investText:  "Charlie investigation: network latency anomaly.",
			summaryText: "Charlie summary: latency normalized.",
			invIn:       150, invOut: 75, invTotal: 225,
			sumIn: 35, sumOut: 15, sumTotal: 50,
		},
	}

	// ── LLM script: 2 entries per session (investigation + executive summary) ──
	llm := NewScriptedLLMClient()
	for _, s := range specs {
		llm.AddSequential(LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: s.investText},
				&agent.UsageChunk{InputTokens: s.invIn, OutputTokens: s.invOut, TotalTokens: s.invTotal},
			},
		})
		llm.AddSequential(LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: s.summaryText},
				&agent.UsageChunk{InputTokens: s.sumIn, OutputTokens: s.sumOut, TotalTokens: s.sumTotal},
			},
		})
	}

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "concurrency")),
		WithLLMClient(llm),
	)

	// ── Submit 3 sessions sequentially (workerCount=1) ──
	ids := make([]string, len(specs))
	for i, s := range specs {
		result := app.SubmitAlert(t, "test-concurrency", s.alertData)
		ids[i] = result["session_id"].(string)
		app.WaitForSessionStatus(t, ids[i], "completed")
	}
	// Submission order: ids[0]=Alpha (earliest), ids[1]=Beta, ids[2]=Charlie (latest).

	// Pre-compute per-session expected values.
	// Token counts include both investigation and executive summary interactions.
	type sessionExpected struct {
		alertData    string
		investText   string
		summaryText  string
		inputTokens  int
		outputTokens int
		totalTokens  int
	}
	expectedByID := make(map[string]sessionExpected, len(specs))
	for i, s := range specs {
		expectedByID[ids[i]] = sessionExpected{
			alertData:    s.alertData,
			investText:   s.investText,
			summaryText:  s.summaryText,
			inputTokens:  s.invIn + s.sumIn,
			outputTokens: s.invOut + s.sumOut,
			totalTokens:  s.invTotal + s.sumTotal,
		}
	}

	// Local helper: find a session in a JSON list by ID.
	findSession := func(t *testing.T, items []interface{}, id string) map[string]interface{} {
		t.Helper()
		for _, item := range items {
			sess := item.(map[string]interface{})
			if sess["id"] == id {
				return sess
			}
		}
		t.Fatalf("session %s not found in list of %d items", id, len(items))
		return nil
	}

	// ── Health ──
	t.Run("Health", func(t *testing.T) {
		health := app.GetHealth(t)
		assert.Equal(t, "healthy", health["status"])
		assert.NotEmpty(t, health["version"])

		checks, ok := health["checks"].(map[string]interface{})
		require.True(t, ok, "health.checks should be an object")

		// Database health.
		db, ok := checks["database"].(map[string]interface{})
		require.True(t, ok, "checks.database should be an object")
		assert.Equal(t, "healthy", db["status"])

		// Worker pool.
		wp, ok := checks["worker_pool"].(map[string]interface{})
		require.True(t, ok, "checks.worker_pool should be an object")
		assert.Equal(t, "healthy", wp["status"])
	})

	// ── Session List (default) ──
	t.Run("SessionList/Default", func(t *testing.T) {
		list := app.GetSessionList(t, "")
		items, ok := list["sessions"].([]interface{})
		require.True(t, ok, "sessions should be an array")
		require.Len(t, items, 3)

		// Pagination metadata.
		pg := list["pagination"].(map[string]interface{})
		assert.Equal(t, 1, toInt(pg["page"]))
		assert.Equal(t, 25, toInt(pg["page_size"]))
		assert.Equal(t, 3, toInt(pg["total_items"]))
		assert.Equal(t, 1, toInt(pg["total_pages"]))

		// Assert exact deterministic fields for each session.
		for _, id := range ids {
			exp := expectedByID[id]
			sess := findSession(t, items, id)

			assert.Equal(t, "test-concurrency", sess["alert_type"], "session %s alert_type", id)
			assert.Equal(t, "concurrency-chain", sess["chain_id"], "session %s chain_id", id)
			assert.Equal(t, "completed", sess["status"], "session %s status", id)
			assert.Equal(t, exp.summaryText, sess["executive_summary"], "session %s executive_summary", id)
			assert.Nil(t, sess["error_message"], "session %s error_message", id)
			assert.Equal(t, "api-client", sess["author"], "session %s author", id)

			// Interaction counts.
			assert.Equal(t, 2, toInt(sess["llm_interaction_count"]), "session %s llm_interaction_count", id)
			assert.Equal(t, 0, toInt(sess["mcp_interaction_count"]), "session %s mcp_interaction_count", id)

			// Token counts (deterministic from LLM script).
			assert.Equal(t, exp.inputTokens, toInt(sess["input_tokens"]), "session %s input_tokens", id)
			assert.Equal(t, exp.outputTokens, toInt(sess["output_tokens"]), "session %s output_tokens", id)
			assert.Equal(t, exp.totalTokens, toInt(sess["total_tokens"]), "session %s total_tokens", id)

			// Stage stats.
			assert.Equal(t, 1, toInt(sess["total_stages"]), "session %s total_stages", id)
			assert.Equal(t, 1, toInt(sess["completed_stages"]), "session %s completed_stages", id)
			assert.Equal(t, false, sess["has_parallel_stages"], "session %s has_parallel_stages", id)
			assert.Equal(t, 0, toInt(sess["chat_message_count"]), "session %s chat_message_count", id)

			// Timestamps (non-deterministic values, but must be present).
			assert.NotNil(t, sess["started_at"], "session %s started_at", id)
			assert.NotNil(t, sess["completed_at"], "session %s completed_at", id)
			assert.NotNil(t, sess["duration_ms"], "session %s duration_ms", id)
		}
	})

	// ── Pagination (real multi-page traversal) ──
	t.Run("SessionList/Pagination", func(t *testing.T) {
		// Page 1 of 2.
		p1 := app.GetSessionList(t, "page=1&page_size=2")
		items1, ok := p1["sessions"].([]interface{})
		require.True(t, ok)
		require.Len(t, items1, 2)

		pg1 := p1["pagination"].(map[string]interface{})
		assert.Equal(t, 1, toInt(pg1["page"]))
		assert.Equal(t, 2, toInt(pg1["page_size"]))
		assert.Equal(t, 3, toInt(pg1["total_items"]))
		assert.Equal(t, 2, toInt(pg1["total_pages"]))

		// Page 2 of 2.
		p2 := app.GetSessionList(t, "page=2&page_size=2")
		items2, ok := p2["sessions"].([]interface{})
		require.True(t, ok)
		require.Len(t, items2, 1)

		pg2 := p2["pagination"].(map[string]interface{})
		assert.Equal(t, 2, toInt(pg2["page"]))
		assert.Equal(t, 2, toInt(pg2["page_size"]))
		assert.Equal(t, 3, toInt(pg2["total_items"]))
		assert.Equal(t, 2, toInt(pg2["total_pages"]))

		// All 3 session IDs must appear across both pages exactly once.
		allIDs := make(map[string]bool)
		for _, item := range items1 {
			allIDs[item.(map[string]interface{})["id"].(string)] = true
		}
		for _, item := range items2 {
			allIDs[item.(map[string]interface{})["id"].(string)] = true
		}
		assert.Len(t, allIDs, 3, "3 unique IDs across 2 pages")
		for _, id := range ids {
			assert.True(t, allIDs[id], "session %s should appear in paginated results", id)
		}
	})

	// ── Status filter ──
	t.Run("SessionList/StatusFilter", func(t *testing.T) {
		// All 3 sessions are completed.
		list := app.GetSessionList(t, "status=completed")
		items := list["sessions"].([]interface{})
		require.Len(t, items, 3)
		for _, item := range items {
			assert.Equal(t, "completed", item.(map[string]interface{})["status"])
		}

		// No pending sessions.
		list = app.GetSessionList(t, "status=pending")
		items = list["sessions"].([]interface{})
		assert.Empty(t, items)

		// Multi-status filter with no matches.
		list = app.GetSessionList(t, "status=failed,cancelled")
		items = list["sessions"].([]interface{})
		assert.Empty(t, items)
	})

	// ── Search ──
	t.Run("SessionList/Search", func(t *testing.T) {
		// Search by unique keyword in alert_data → exactly 1 match.
		list := app.GetSessionList(t, "search=Alpha")
		items := list["sessions"].([]interface{})
		require.Len(t, items, 1)
		assert.Equal(t, ids[0], items[0].(map[string]interface{})["id"])

		// Search by keyword common to all alert_data → 3 matches.
		list = app.GetSessionList(t, "search=payload")
		items = list["sessions"].([]interface{})
		assert.Len(t, items, 3)

		// Search with no matches.
		list = app.GetSessionList(t, "search=nonexistent")
		items = list["sessions"].([]interface{})
		assert.Empty(t, items)
	})

	// ── Chain ID filter ──
	t.Run("SessionList/ChainFilter", func(t *testing.T) {
		list := app.GetSessionList(t, "chain_id=concurrency-chain")
		items := list["sessions"].([]interface{})
		assert.Len(t, items, 3)

		list = app.GetSessionList(t, "chain_id=nonexistent-chain")
		items = list["sessions"].([]interface{})
		assert.Empty(t, items)
	})

	// ── Alert type filter ──
	t.Run("SessionList/AlertTypeFilter", func(t *testing.T) {
		list := app.GetSessionList(t, "alert_type=test-concurrency")
		items := list["sessions"].([]interface{})
		assert.Len(t, items, 3)

		list = app.GetSessionList(t, "alert_type=nonexistent")
		items = list["sessions"].([]interface{})
		assert.Empty(t, items)
	})

	// ── Sorting ──
	t.Run("SessionList/Sorting", func(t *testing.T) {
		// created_at ascending: Alpha (earliest) first, Charlie (latest) last.
		list := app.GetSessionList(t, "sort_by=created_at&sort_order=asc")
		items := list["sessions"].([]interface{})
		require.Len(t, items, 3)
		assert.Equal(t, ids[0], items[0].(map[string]interface{})["id"], "asc: first should be Alpha")
		assert.Equal(t, ids[1], items[1].(map[string]interface{})["id"], "asc: second should be Beta")
		assert.Equal(t, ids[2], items[2].(map[string]interface{})["id"], "asc: third should be Charlie")

		// created_at descending: Charlie first, Alpha last.
		list = app.GetSessionList(t, "sort_by=created_at&sort_order=desc")
		items = list["sessions"].([]interface{})
		require.Len(t, items, 3)
		assert.Equal(t, ids[2], items[0].(map[string]interface{})["id"], "desc: first should be Charlie")
		assert.Equal(t, ids[1], items[1].(map[string]interface{})["id"], "desc: second should be Beta")
		assert.Equal(t, ids[0], items[2].(map[string]interface{})["id"], "desc: third should be Alpha")
	})

	// ── Active Sessions ──
	t.Run("ActiveSessions", func(t *testing.T) {
		active := app.GetActiveSessions(t)

		activeList, ok := active["active"].([]interface{})
		require.True(t, ok, "active should be an array")
		assert.Empty(t, activeList, "no sessions should be active after completion")

		queuedList, ok := active["queued"].([]interface{})
		require.True(t, ok, "queued should be an array")
		assert.Empty(t, queuedList, "no sessions should be queued after completion")
	})

	// ── Session Detail (session B — middle session) ──
	t.Run("SessionDetail", func(t *testing.T) {
		detail := app.GetSession(t, ids[1])
		expB := expectedByID[ids[1]]

		// Core fields.
		assert.Equal(t, ids[1], detail["id"])
		assert.Equal(t, "Session Beta payload", detail["alert_data"])
		assert.Equal(t, "test-concurrency", detail["alert_type"])
		assert.Equal(t, "concurrency-chain", detail["chain_id"])
		assert.Equal(t, "completed", detail["status"])
		assert.Nil(t, detail["error_message"])
		assert.Equal(t, "api-client", detail["author"])

		// Analysis results from LLM script.
		assert.Equal(t, expB.investText, detail["final_analysis"])
		assert.Equal(t, expB.summaryText, detail["executive_summary"])

		// Computed stats (exact).
		assert.Equal(t, 1, toInt(detail["total_stages"]))
		assert.Equal(t, 1, toInt(detail["completed_stages"]))
		assert.Equal(t, 0, toInt(detail["failed_stages"]))
		assert.Equal(t, false, detail["has_parallel_stages"])
		assert.Equal(t, 2, toInt(detail["llm_interaction_count"]))
		assert.Equal(t, 0, toInt(detail["mcp_interaction_count"]))
		assert.Equal(t, 0, toInt(detail["chat_message_count"]))
		assert.Equal(t, expB.inputTokens, toInt(detail["input_tokens"]))
		assert.Equal(t, expB.outputTokens, toInt(detail["output_tokens"]))
		assert.Equal(t, expB.totalTokens, toInt(detail["total_tokens"]))

		// Timestamps.
		assert.NotNil(t, detail["started_at"])
		assert.NotNil(t, detail["completed_at"])
		assert.NotNil(t, detail["duration_ms"])

		// Stage list — exactly 1 stage.
		stages, ok := detail["stages"].([]interface{})
		require.True(t, ok, "stages should be an array")
		require.Len(t, stages, 1)

		stage := stages[0].(map[string]interface{})
		assert.NotEmpty(t, stage["id"])
		assert.Equal(t, "analysis", stage["stage_name"])
		assert.Equal(t, 1, toInt(stage["stage_index"]))
		assert.Equal(t, "completed", stage["status"])
		assert.Equal(t, 1, toInt(stage["expected_agent_count"]))
	})

	// ── Session Summary (session A — verify exact token math) ──
	t.Run("SessionSummary", func(t *testing.T) {
		summary := app.GetSessionSummary(t, ids[0])
		expA := expectedByID[ids[0]]

		assert.Equal(t, ids[0], summary["session_id"])
		assert.Equal(t, 2, toInt(summary["total_interactions"]))
		assert.Equal(t, 2, toInt(summary["llm_interactions"]))
		assert.Equal(t, 0, toInt(summary["mcp_interactions"]))
		assert.Equal(t, expA.inputTokens, toInt(summary["input_tokens"]))
		assert.Equal(t, expA.outputTokens, toInt(summary["output_tokens"]))
		assert.Equal(t, expA.totalTokens, toInt(summary["total_tokens"]))
		assert.NotNil(t, summary["total_duration_ms"])

		chainStats, ok := summary["chain_statistics"].(map[string]interface{})
		require.True(t, ok, "chain_statistics should be present")
		assert.Equal(t, 1, toInt(chainStats["total_stages"]))
		assert.Equal(t, 1, toInt(chainStats["completed_stages"]))
		assert.Equal(t, 0, toInt(chainStats["failed_stages"]))
	})

	// ── Session Summary 404 ──
	t.Run("SessionSummary/NotFound", func(t *testing.T) {
		resp := app.getJSON(t, "/api/v1/sessions/nonexistent-id/summary", 404)
		assert.NotNil(t, resp)
	})

	// ── Session Status (lightweight polling endpoint) ──
	t.Run("SessionStatus", func(t *testing.T) {
		status := app.GetSessionStatus(t, ids[0])
		expA := expectedByID[ids[0]]

		assert.Equal(t, ids[0], status["id"])
		assert.Equal(t, "completed", status["status"])
		assert.Equal(t, expA.investText, status["final_analysis"])
		assert.Equal(t, expA.summaryText, status["executive_summary"])
		assert.Nil(t, status["error_message"])
	})

	// ── Session Status 404 ──
	t.Run("SessionStatus/NotFound", func(t *testing.T) {
		resp := app.getJSON(t, "/api/v1/sessions/nonexistent-id/status", 404)
		assert.NotNil(t, resp)
	})

	// ── Filter Options (reflect actual DB data) ──
	t.Run("FilterOptions", func(t *testing.T) {
		options := app.GetFilterOptions(t)

		statuses, ok := options["statuses"].([]interface{})
		require.True(t, ok, "statuses should be an array")
		assert.Equal(t, 7, len(statuses), "should have all 7 status enum values")

		// Only one alert type and one chain in the DB.
		alertTypes, ok := options["alert_types"].([]interface{})
		require.True(t, ok, "alert_types should be an array")
		require.Len(t, alertTypes, 1)
		assert.Equal(t, "test-concurrency", alertTypes[0])

		chainIDs, ok := options["chain_ids"].([]interface{})
		require.True(t, ok, "chain_ids should be an array")
		require.Len(t, chainIDs, 1)
		assert.Equal(t, "concurrency-chain", chainIDs[0])
	})

	// ── System Warnings ──
	t.Run("SystemWarnings", func(t *testing.T) {
		warnings := app.GetSystemWarnings(t)
		warnList, ok := warnings["warnings"].([]interface{})
		require.True(t, ok, "warnings should be an array")
		assert.Empty(t, warnList)
	})

	// ── MCP Servers ──
	t.Run("MCPServers", func(t *testing.T) {
		servers := app.GetMCPServers(t)
		serverList, ok := servers["servers"].([]interface{})
		require.True(t, ok, "servers should be an array")
		assert.Empty(t, serverList)
	})

	// ── Default Tools (test-provider has no native tools → all false) ──
	t.Run("DefaultTools", func(t *testing.T) {
		tools := app.GetDefaultTools(t)
		nativeTools, ok := tools["native_tools"].(map[string]interface{})
		require.True(t, ok, "native_tools should be a map")
		assert.Len(t, nativeTools, 3)
		assert.Equal(t, false, nativeTools["google_search"])
		assert.Equal(t, false, nativeTools["code_execution"])
		assert.Equal(t, false, nativeTools["url_context"])
	})

	// ── Alert Types (from config, not DB: concurrency-chain + kubernetes) ──
	t.Run("AlertTypes", func(t *testing.T) {
		types := app.GetAlertTypes(t)

		// Chains sorted alphabetically: concurrency-chain then kubernetes.
		assert.Equal(t, "concurrency-chain", types["default_chain_id"])

		alertTypes, ok := types["alert_types"].([]interface{})
		require.True(t, ok, "alert_types should be an array")
		require.Len(t, alertTypes, 2)

		at0 := alertTypes[0].(map[string]interface{})
		assert.Equal(t, "test-concurrency", at0["type"])
		assert.Equal(t, "concurrency-chain", at0["chain_id"])

		at1 := alertTypes[1].(map[string]interface{})
		assert.Equal(t, "kubernetes", at1["type"])
		assert.Equal(t, "kubernetes", at1["chain_id"])
		assert.Equal(t, "Single-stage Kubernetes analysis", at1["description"])
	})
}
