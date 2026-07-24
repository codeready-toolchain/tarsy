package e2e

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// usageCostRates match system.cost_estimation.model_rates in the usage-cost config.
const (
	usageCostInputPerMillion  = 1.0
	usageCostOutputPerMillion = 2.0
)

// estimateUSD mirrors pkg/cost.Estimate for YAML overrides (thinking uses output rate).
func estimateUSD(inputTokens, outputTokens, thinkingTokens int) float64 {
	return float64(inputTokens)*usageCostInputPerMillion/1_000_000 +
		float64(outputTokens)*usageCostOutputPerMillion/1_000_000 +
		float64(thinkingTokens)*usageCostOutputPerMillion/1_000_000
}

// TestUsageCost_PipelinePersistsAndExposesCost runs a minimal single-stage chain
// with scripted token usage (including thinking tokens) and YAML rate overrides.
// It verifies the write path → DB → session APIs → usage summary → config viewer.
func TestUsageCost_PipelinePersistsAndExposesCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Session A: lower tokens, higher cost (thinking tokens priced at output rate).
	// Session B: higher tokens, lower cost — separates rank_by=cost vs rank_by=tokens.
	type sessionSpec struct {
		alertData                                 string
		investText, summaryText                   string
		invIn, invOut, invTotal, invThinking      int
		sumIn, sumOut, sumTotal                   int
	}
	specs := []sessionSpec{
		{
			alertData:   "Usage session A",
			investText:  "A investigation complete.",
			summaryText: "A executive summary.",
			invIn:       100, invOut: 50, invTotal: 150, invThinking: 1000,
			sumIn: 30, sumOut: 10, sumTotal: 40,
		},
		{
			alertData:   "Usage session B",
			investText:  "B investigation complete.",
			summaryText: "B executive summary.",
			invIn:       500, invOut: 250, invTotal: 750, invThinking: 0,
			sumIn: 100, sumOut: 50, sumTotal: 150,
		},
	}

	llm := NewScriptedLLMClient()
	for _, s := range specs {
		llm.AddSequential(LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: s.investText},
				&agent.UsageChunk{
					InputTokens:    s.invIn,
					OutputTokens:   s.invOut,
					TotalTokens:    s.invTotal,
					ThinkingTokens: s.invThinking,
				},
			},
		})
		llm.AddSequential(LLMScriptEntry{
			Chunks: []agent.Chunk{
				&agent.TextChunk{Content: s.summaryText},
				&agent.UsageChunk{
					InputTokens:  s.sumIn,
					OutputTokens: s.sumOut,
					TotalTokens:  s.sumTotal,
				},
			},
		})
	}

	app := NewTestApp(t,
		WithConfig(configs.Load(t, "usage-cost")),
		WithLLMClient(llm),
	)

	ids := make([]string, len(specs))
	for i, s := range specs {
		result := app.SubmitAlert(t, "test-usage-cost", s.alertData)
		ids[i] = result["session_id"].(string)
		app.WaitForSessionStatus(t, ids[i], "completed")
	}

	type sessionExpected struct {
		inputTokens, outputTokens, totalTokens int
		estimatedCost                          float64
	}
	expectedByID := make(map[string]sessionExpected, len(specs))
	var totalIn, totalOut, totalTok int
	var totalCost float64
	for i, s := range specs {
		in := s.invIn + s.sumIn
		out := s.invOut + s.sumOut
		tok := s.invTotal + s.sumTotal
		cost := estimateUSD(s.invIn, s.invOut, s.invThinking) +
			estimateUSD(s.sumIn, s.sumOut, 0)
		expectedByID[ids[i]] = sessionExpected{
			inputTokens: in, outputTokens: out, totalTokens: tok,
			estimatedCost: cost,
		}
		totalIn += in
		totalOut += out
		totalTok += tok
		totalCost += cost
	}

	t.Run("SessionListCost", func(t *testing.T) {
		list := app.GetSessionList(t, "")
		assert.Equal(t, true, list["cost_estimation_enabled"])

		items, ok := list["sessions"].([]interface{})
		require.True(t, ok)
		require.Len(t, items, 2)

		for _, item := range items {
			sess := item.(map[string]interface{})
			id := sess["id"].(string)
			exp := expectedByID[id]

			assert.Equal(t, exp.inputTokens, toInt(sess["input_tokens"]), "session %s input_tokens", id)
			assert.Equal(t, exp.outputTokens, toInt(sess["output_tokens"]), "session %s output_tokens", id)
			assert.Equal(t, exp.totalTokens, toInt(sess["total_tokens"]), "session %s total_tokens", id)
			assert.InDelta(t, exp.estimatedCost, toFloat(sess["estimated_cost_usd"]), 1e-12, "session %s cost", id)
			assert.Equal(t, "complete", sess["cost_completeness"], "session %s completeness", id)
		}
	})

	t.Run("SessionDetailAndSummaryCost", func(t *testing.T) {
		id := ids[0]
		exp := expectedByID[id]

		detail := app.GetSession(t, id)
		assert.Equal(t, true, detail["cost_estimation_enabled"])
		assert.Equal(t, exp.inputTokens, toInt(detail["input_tokens"]))
		assert.Equal(t, exp.outputTokens, toInt(detail["output_tokens"]))
		assert.Equal(t, exp.totalTokens, toInt(detail["total_tokens"]))
		assert.InDelta(t, exp.estimatedCost, toFloat(detail["estimated_cost_usd"]), 1e-12)
		assert.Equal(t, "complete", detail["cost_completeness"])
		assert.Equal(t, 0, toInt(detail["unpriced_interaction_count"]))

		summary := app.GetSessionSummary(t, id)
		assert.Equal(t, true, summary["cost_estimation_enabled"])
		assert.InDelta(t, exp.estimatedCost, toFloat(summary["estimated_cost_usd"]), 1e-12)
		assert.Equal(t, "complete", summary["cost_completeness"])
	})

	t.Run("UsageSummary", func(t *testing.T) {
		start := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
		end := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
		q := url.Values{
			"start_date": {start},
			"end_date":   {end},
			"rank_by":    {"cost"},
		}

		usage := app.GetUsageSummary(t, q.Encode())
		assert.Equal(t, true, usage["cost_estimation_enabled"])
		assert.Equal(t, "cost", usage["rank_by"])

		totals, ok := usage["totals"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, totalIn, toInt(totals["input_tokens"]))
		assert.Equal(t, totalOut, toInt(totals["output_tokens"]))
		assert.Equal(t, totalTok, toInt(totals["total_tokens"]))
		assert.InDelta(t, totalCost, toFloat(totals["estimated_cost_usd"]), 1e-12)
		assert.Equal(t, "complete", totals["cost_completeness"])

		byModel, ok := usage["by_model"].([]interface{})
		require.True(t, ok)
		require.Len(t, byModel, 1)
		model := byModel[0].(map[string]interface{})
		assert.Equal(t, "test-model", model["model_name"])
		assert.Equal(t, true, model["priced"])
		assert.Equal(t, 0, toInt(model["unpriced_interaction_count"]))
		assert.InDelta(t, totalCost, toFloat(model["estimated_cost_usd"]), 1e-12)

		byAlert, ok := usage["by_alert_type"].([]interface{})
		require.True(t, ok)
		require.Len(t, byAlert, 1)
		assert.Equal(t, "test-usage-cost", byAlert[0].(map[string]interface{})["alert_type"])

		byChain, ok := usage["by_chain"].([]interface{})
		require.True(t, ok)
		require.Len(t, byChain, 1)
		assert.Equal(t, "usage-cost-chain", byChain[0].(map[string]interface{})["chain_id"])

		top, ok := usage["top_sessions"].([]interface{})
		require.True(t, ok)
		require.Len(t, top, 2)
		// rank_by=cost: A (thinking-heavy) before B
		assert.Equal(t, ids[0], top[0].(map[string]interface{})["session_id"])
		assert.Equal(t, ids[1], top[1].(map[string]interface{})["session_id"])
		assert.InDelta(t, expectedByID[ids[0]].estimatedCost,
			toFloat(top[0].(map[string]interface{})["estimated_cost_usd"]), 1e-12)

		q.Set("rank_by", "tokens")
		byTokens := app.GetUsageSummary(t, q.Encode())
		topTok, ok := byTokens["top_sessions"].([]interface{})
		require.True(t, ok)
		require.Len(t, topTok, 2)
		// rank_by=tokens: B (more tokens) before A
		assert.Equal(t, ids[1], topTok[0].(map[string]interface{})["session_id"])
		assert.Equal(t, ids[0], topTok[1].(map[string]interface{})["session_id"])
	})

	t.Run("SystemConfigCostEstimation", func(t *testing.T) {
		cfg := app.GetSystemConfig(t)
		system, ok := cfg["system"].(map[string]interface{})
		require.True(t, ok, "system should be present")
		ce, ok := system["cost_estimation"].(map[string]interface{})
		require.True(t, ok, "system.cost_estimation should be present")
		assert.Equal(t, true, ce["enabled"])

		rates, ok := ce["model_rates"].(map[string]interface{})
		require.True(t, ok)
		tm, ok := rates["test-model"].(map[string]interface{})
		require.True(t, ok)
		assert.InDelta(t, usageCostInputPerMillion, toFloat(tm["input_per_million"]), 1e-12)
		assert.InDelta(t, usageCostOutputPerMillion, toFloat(tm["output_per_million"]), 1e-12)

		catalog, ok := ce["catalog"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "snapshot", catalog["source"])
		assert.Greater(t, toInt(catalog["entry_count"]), 0)
	})
}

// TestUsageCost_EstimationDisabled verifies tokens still flow while cost fields
// are omitted and rank_by=cost is rejected.
func TestUsageCost_EstimationDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	cfg := configs.Load(t, "usage-cost")
	require.NotNil(t, cfg.CostEstimation)
	cfg.CostEstimation.Enabled = false

	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Disabled-cost investigation."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 40, TotalTokens: 120},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Disabled-cost summary."},
			&agent.UsageChunk{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		},
	})

	app := NewTestApp(t, WithConfig(cfg), WithLLMClient(llm))
	result := app.SubmitAlert(t, "test-usage-cost", "disabled cost payload")
	sessionID := result["session_id"].(string)
	app.WaitForSessionStatus(t, sessionID, "completed")

	list := app.GetSessionList(t, "")
	assert.Equal(t, false, list["cost_estimation_enabled"])
	items := list["sessions"].([]interface{})
	require.Len(t, items, 1)
	sess := items[0].(map[string]interface{})
	assert.Equal(t, 100, toInt(sess["input_tokens"]))
	assert.Equal(t, 50, toInt(sess["output_tokens"]))
	assert.Equal(t, 150, toInt(sess["total_tokens"]))
	_, hasCost := sess["estimated_cost_usd"]
	assert.False(t, hasCost, "estimated_cost_usd must be omitted when estimation is disabled")
	_, hasCompleteness := sess["cost_completeness"]
	assert.False(t, hasCompleteness, "cost_completeness must be omitted when estimation is disabled")

	detail := app.GetSession(t, sessionID)
	assert.Equal(t, false, detail["cost_estimation_enabled"])
	_, hasDetailCost := detail["estimated_cost_usd"]
	assert.False(t, hasDetailCost)

	start := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	end := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)

	usage := app.GetUsageSummary(t, fmt.Sprintf(
		"start_date=%s&end_date=%s&rank_by=tokens",
		url.QueryEscape(start), url.QueryEscape(end),
	))
	assert.Equal(t, false, usage["cost_estimation_enabled"])
	totals := usage["totals"].(map[string]interface{})
	assert.Equal(t, 150, toInt(totals["total_tokens"]))
	_, hasUsageCost := totals["estimated_cost_usd"]
	assert.False(t, hasUsageCost)

	// rank_by=cost is rejected when estimation is disabled.
	app.getJSON(t, fmt.Sprintf(
		"/api/v1/usage/summary?start_date=%s&end_date=%s&rank_by=cost",
		url.QueryEscape(start), url.QueryEscape(end),
	), 400)

	sysCfg := app.GetSystemConfig(t)
	system := sysCfg["system"].(map[string]interface{})
	ce := system["cost_estimation"].(map[string]interface{})
	assert.Equal(t, false, ce["enabled"])
}
