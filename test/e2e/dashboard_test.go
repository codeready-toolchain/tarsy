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
// filter options, system info, and health endpoints after a
// completed single-stage pipeline run. Uses the concurrency
// config (simplest chain: one stage, one agent, max_iterations=1).
// ────────────────────────────────────────────────────────────

func TestDashboardEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// ── LLM script: one investigation turn + executive summary ──
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ThinkingChunk{Content: "Analyzing the dashboard test data."},
			&agent.TextChunk{Content: "Dashboard analysis complete: all systems nominal."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Brief summary: systems nominal."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "concurrency")),
		WithLLMClient(llm),
	)

	// Submit alert and wait for completion.
	result := app.SubmitAlert(t, "test-concurrency", "Dashboard test payload")
	sessionID := result["session_id"].(string)
	app.WaitForSessionStatus(t, sessionID, "completed")

	// ── Health ──
	t.Run("Health", func(t *testing.T) {
		health := app.GetHealth(t)
		assert.Equal(t, "healthy", health["status"])
		assert.NotEmpty(t, health["version"])

		// Database health.
		db, ok := health["database"].(map[string]interface{})
		require.True(t, ok, "health.database should be an object")
		assert.Equal(t, "healthy", db["status"])

		// Configuration stats.
		cfg, ok := health["configuration"].(map[string]interface{})
		require.True(t, ok, "health.configuration should be an object")
		assert.Equal(t, 4, toInt(cfg["agents"]))
		assert.Equal(t, 2, toInt(cfg["chains"]))
		assert.Equal(t, 1, toInt(cfg["mcp_servers"]))
		assert.Equal(t, 6, toInt(cfg["llm_providers"]))

		// Worker pool.
		wp, ok := health["worker_pool"].(map[string]interface{})
		require.True(t, ok, "health.worker_pool should be an object")
		assert.NotEmpty(t, wp["pod_id"])
	})

	// ── Session List ──
	t.Run("SessionList/Default", func(t *testing.T) {
		list := app.GetSessionList(t, "")
		sessions, ok := list["sessions"].([]interface{})
		require.True(t, ok, "sessions field should be an array")
		assert.GreaterOrEqual(t, len(sessions), 1)

		pagination, ok := list["pagination"].(map[string]interface{})
		require.True(t, ok, "pagination field should be present")
		assert.Equal(t, float64(1), pagination["page"])
		assert.GreaterOrEqual(t, pagination["total_items"], float64(1))

		// First session should have basic fields.
		first := sessions[0].(map[string]interface{})
		assert.NotEmpty(t, first["id"])
		assert.NotEmpty(t, first["status"])
		assert.NotEmpty(t, first["created_at"])
		assert.NotEmpty(t, first["chain_id"])
	})

	t.Run("SessionList/Pagination", func(t *testing.T) {
		list := app.GetSessionList(t, "page=1&page_size=1")
		sessions := list["sessions"].([]interface{})
		assert.LessOrEqual(t, len(sessions), 1)
		pagination := list["pagination"].(map[string]interface{})
		assert.Equal(t, float64(1), pagination["page_size"])
	})

	t.Run("SessionList/StatusFilter", func(t *testing.T) {
		list := app.GetSessionList(t, "status=completed")
		sessions := list["sessions"].([]interface{})
		for _, s := range sessions {
			sess := s.(map[string]interface{})
			assert.Equal(t, "completed", sess["status"])
		}
	})

	t.Run("SessionList/EmptyFilter", func(t *testing.T) {
		list := app.GetSessionList(t, "status=pending")
		sessions := list["sessions"].([]interface{})
		assert.Empty(t, sessions, "no pending sessions expected")
	})

	t.Run("SessionList/Sorting", func(t *testing.T) {
		list := app.GetSessionList(t, "sort_by=created_at&sort_order=desc")
		sessions := list["sessions"].([]interface{})
		assert.GreaterOrEqual(t, len(sessions), 1)
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

	// ── Session Detail (enriched DTO) ──
	t.Run("SessionDetail", func(t *testing.T) {
		detail := app.GetSession(t, sessionID)
		assert.Equal(t, sessionID, detail["id"])
		assert.Equal(t, "completed", detail["status"])
		assert.NotEmpty(t, detail["alert_data"])
		assert.NotEmpty(t, detail["chain_id"])

		// Computed fields from the enriched DTO.
		assert.NotNil(t, detail["total_stages"], "total_stages should be present")
		assert.NotNil(t, detail["completed_stages"], "completed_stages should be present")
		assert.NotNil(t, detail["llm_interaction_count"], "llm_interaction_count should be present")
		assert.GreaterOrEqual(t, detail["total_tokens"], float64(0))

		stages, ok := detail["stages"].([]interface{})
		require.True(t, ok, "stages should be an array")
		assert.GreaterOrEqual(t, len(stages), 1)

		// Verify stage structure.
		stage := stages[0].(map[string]interface{})
		assert.NotEmpty(t, stage["id"])
		assert.NotEmpty(t, stage["stage_name"])
		assert.NotEmpty(t, stage["status"])
	})

	// ── Session Summary ──
	t.Run("SessionSummary", func(t *testing.T) {
		summary := app.GetSessionSummary(t, sessionID)
		assert.Equal(t, sessionID, summary["session_id"])
		assert.NotNil(t, summary["total_interactions"])
		assert.NotNil(t, summary["llm_interactions"])
		assert.NotNil(t, summary["mcp_interactions"])

		chainStats, ok := summary["chain_statistics"].(map[string]interface{})
		require.True(t, ok, "chain_statistics should be present")
		assert.GreaterOrEqual(t, chainStats["total_stages"], float64(1))
	})

	// ── Session Summary 404 ──
	t.Run("SessionSummary/NotFound", func(t *testing.T) {
		resp := app.getJSON(t, "/api/v1/sessions/nonexistent-id/summary", 404)
		assert.NotNil(t, resp)
	})

	// ── Filter Options ──
	t.Run("FilterOptions", func(t *testing.T) {
		options := app.GetFilterOptions(t)
		statuses, ok := options["statuses"].([]interface{})
		require.True(t, ok, "statuses should be an array")
		assert.Equal(t, 7, len(statuses), "should have all 7 status enum values")

		alertTypes, ok := options["alert_types"].([]interface{})
		require.True(t, ok, "alert_types should be an array")
		assert.GreaterOrEqual(t, len(alertTypes), 1)

		chainIDs, ok := options["chain_ids"].([]interface{})
		require.True(t, ok, "chain_ids should be an array")
		assert.GreaterOrEqual(t, len(chainIDs), 1)
	})

	// ── System Warnings ──
	t.Run("SystemWarnings", func(t *testing.T) {
		warnings := app.GetSystemWarnings(t)
		warnList, ok := warnings["warnings"].([]interface{})
		require.True(t, ok, "warnings should be an array")
		// No MCP health monitor in tests — expect empty.
		assert.Empty(t, warnList)
	})

	// ── MCP Servers ──
	t.Run("MCPServers", func(t *testing.T) {
		servers := app.GetMCPServers(t)
		serverList, ok := servers["servers"].([]interface{})
		require.True(t, ok, "servers should be an array")
		// No MCP health monitor in tests — expect empty.
		assert.Empty(t, serverList)
	})

	// ── Default Tools ──
	t.Run("DefaultTools", func(t *testing.T) {
		tools := app.GetDefaultTools(t)
		nativeTools, ok := tools["native_tools"].(map[string]interface{})
		require.True(t, ok, "native_tools should be a map")
		assert.Contains(t, nativeTools, "google_search")
		assert.Contains(t, nativeTools, "code_execution")
		assert.Contains(t, nativeTools, "url_context")
	})

	// ── Alert Types ──
	t.Run("AlertTypes", func(t *testing.T) {
		types := app.GetAlertTypes(t)
		alertTypes, ok := types["alert_types"].([]interface{})
		require.True(t, ok, "alert_types should be an array")
		assert.GreaterOrEqual(t, len(alertTypes), 1)
		assert.NotEmpty(t, types["default_chain_id"])

		// The concurrency config registers "test-concurrency" alert type.
		found := false
		for _, at := range alertTypes {
			item := at.(map[string]interface{})
			if item["type"] == "test-concurrency" {
				found = true
				assert.Equal(t, "concurrency-chain", item["chain_id"])
			}
		}
		assert.True(t, found, "should find test-concurrency alert type")
	})
}
