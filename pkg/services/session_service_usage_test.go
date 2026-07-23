package services

import (
	"context"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/stage"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionService_GetUsageSummary(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestSessionService(t, client.Client)
	ctx := context.Background()

	windowStart := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	inWindow := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	beforeWindow := time.Date(2024, 5, 15, 12, 0, 0, 0, time.UTC)
	onEndBoundary := windowEnd // half-open: excluded

	params := models.UsageSummaryParams{
		StartDate: windowStart,
		EndDate:   windowEnd,
	}

	t.Run("window includes only sessions by created_at", func(t *testing.T) {
		inID, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "in-window",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, client.Client, inID, stageID, execID, "model-a", 100, 50, 150, floatPtr(0.01), 0)

		outID, outStage, outExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "before-window",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: beforeWindow,
		})
		seedLLMInteraction(t, client.Client, outID, outStage, outExec, "model-a", 999, 999, 1998, floatPtr(9.99), 0)

		boundaryID, bStage, bExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "on-end-boundary",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: onEndBoundary,
		})
		seedLLMInteraction(t, client.Client, boundaryID, bStage, bExec, "model-a", 500, 500, 1000, floatPtr(1.0), 0)

		summary, err := service.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(150), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.01, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.UsageRankByCost, summary.RankBy)
		assert.Equal(t, windowStart, summary.Window.Start)
		assert.Equal(t, windowEnd, summary.Window.End)

		require.Len(t, summary.TopSessions, 1)
		assert.Equal(t, inID, summary.TopSessions[0].SessionID)
	})

	t.Run("excludes soft-deleted sessions", func(t *testing.T) {
		delClient := testdb.NewTestClient(t)
		delSvc := setupTestSessionService(t, delClient.Client)

		sid, stageID, execID := seedUsageSession(t, delClient.Client, usageSeed{
			AlertData: "soft-deleted",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, delClient.Client, sid, stageID, execID, "model-a", 100, 50, 150, floatPtr(0.01), 0)
		require.NoError(t, delClient.Client.AlertSession.UpdateOneID(sid).SetDeletedAt(time.Now()).Exec(ctx))

		summary, err := delSvc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(0), summary.Totals.TotalTokens)
		assert.Empty(t, summary.TopSessions)
		assert.Empty(t, summary.ByModel)
	})

	t.Run("multi-model partial completeness and by_model priced flags", func(t *testing.T) {
		mc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, mc.Client)

		sid, stageID, execID := seedUsageSession(t, mc.Client, usageSeed{
			AlertData: "multi-model",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, mc.Client, sid, stageID, execID, "priced-model", 100, 50, 150, floatPtr(0.012), 0)
		seedLLMInteraction(t, mc.Client, sid, stageID, execID, "unpriced-model", 200, 100, 300, nil, 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.True(t, summary.CostEstimationEnabled)
		assert.Equal(t, int64(450), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.012, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.CostCompletenessPartial, summary.Totals.CostCompleteness)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 1, *summary.Totals.UnpricedInteractionCount)

		byModel := map[string]models.UsageModelBreakdown{}
		for _, m := range summary.ByModel {
			byModel[m.ModelName] = m
		}
		require.Contains(t, byModel, "priced-model")
		require.Contains(t, byModel, "unpriced-model")
		require.NotNil(t, byModel["priced-model"].Priced)
		assert.True(t, *byModel["priced-model"].Priced)
		require.NotNil(t, byModel["unpriced-model"].Priced)
		assert.False(t, *byModel["unpriced-model"].Priced)

		require.Len(t, summary.TopSessions, 1)
		assert.Equal(t, models.CostCompletenessPartial, summary.TopSessions[0].CostCompleteness)
		require.NotNil(t, summary.TopSessions[0].EstimatedCostUsd)
		assert.InDelta(t, 0.012, *summary.TopSessions[0].EstimatedCostUsd, 1e-9)
	})

	t.Run("null costs treated as zero with none completeness", func(t *testing.T) {
		nc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, nc.Client)

		sid, stageID, execID := seedUsageSession(t, nc.Client, usageSeed{
			AlertData: "all-null-cost",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, nc.Client, sid, stageID, execID, "old-model", 10, 5, 15, nil, 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.0, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.CostCompletenessNone, summary.Totals.CostCompleteness)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 1, *summary.Totals.UnpricedInteractionCount)
	})

	t.Run("rank_by cost vs tokens", func(t *testing.T) {
		rc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, rc.Client)

		cheapID, cheapStage, cheapExec := seedUsageSession(t, rc.Client, usageSeed{
			AlertData: "cheap-high-tokens",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow.Add(time.Hour),
		})
		seedLLMInteraction(t, rc.Client, cheapID, cheapStage, cheapExec, "m", 1000, 1000, 2000, floatPtr(0.001), 0)

		priceyID, priceyStage, priceyExec := seedUsageSession(t, rc.Client, usageSeed{
			AlertData: "pricey-low-tokens",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, rc.Client, priceyID, priceyStage, priceyExec, "m", 10, 10, 20, floatPtr(5.0), 0)

		byCost, err := svc.GetUsageSummary(ctx, models.UsageSummaryParams{
			StartDate: windowStart,
			EndDate:   windowEnd,
			RankBy:    models.UsageRankByCost,
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(byCost.TopSessions), 2)
		assert.Equal(t, models.UsageRankByCost, byCost.RankBy)
		assert.Equal(t, priceyID, byCost.TopSessions[0].SessionID)
		assert.Equal(t, cheapID, byCost.TopSessions[1].SessionID)

		byTokens, err := svc.GetUsageSummary(ctx, models.UsageSummaryParams{
			StartDate: windowStart,
			EndDate:   windowEnd,
			RankBy:    models.UsageRankByTokens,
		})
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(byTokens.TopSessions), 2)
		assert.Equal(t, models.UsageRankByTokens, byTokens.RankBy)
		assert.Equal(t, cheapID, byTokens.TopSessions[0].SessionID)
		assert.Equal(t, priceyID, byTokens.TopSessions[1].SessionID)
	})

	t.Run("alert_type and chain_id filters", func(t *testing.T) {
		fc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, fc.Client)

		aID, aStage, aExec := seedUsageSession(t, fc.Client, usageSeed{
			AlertData: "type-a",
			AlertType: "type-a",
			ChainID:   "chain-a",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, fc.Client, aID, aStage, aExec, "m", 100, 0, 100, floatPtr(0.1), 0)

		bID, bStage, bExec := seedUsageSession(t, fc.Client, usageSeed{
			AlertData: "type-b",
			AlertType: "type-b",
			ChainID:   "chain-b",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, fc.Client, bID, bStage, bExec, "m", 200, 0, 200, floatPtr(0.2), 0)

		filtered, err := svc.GetUsageSummary(ctx, models.UsageSummaryParams{
			StartDate: windowStart,
			EndDate:   windowEnd,
			AlertType: "type-a",
			ChainID:   "chain-a",
		})
		require.NoError(t, err)
		assert.Equal(t, int64(100), filtered.Totals.TotalTokens)
		require.Len(t, filtered.ByAlertType, 1)
		assert.Equal(t, "type-a", filtered.ByAlertType[0].AlertType)
		require.Len(t, filtered.ByChain, 1)
		assert.Equal(t, "chain-a", filtered.ByChain[0].ChainID)
		require.Len(t, filtered.TopSessions, 1)
		assert.Equal(t, aID, filtered.TopSessions[0].SessionID)
		_ = bID // ensured seeded but filtered out
	})

	t.Run("by_alert_type and by_chain rollups", func(t *testing.T) {
		bc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, bc.Client)

		s1, st1, e1 := seedUsageSession(t, bc.Client, usageSeed{
			AlertData: "rollup-1",
			AlertType: "oom",
			ChainID:   "chain-x",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, bc.Client, s1, st1, e1, "m", 50, 50, 100, floatPtr(0.5), 0)

		s2, st2, e2 := seedUsageSession(t, bc.Client, usageSeed{
			AlertData: "rollup-2",
			AlertType: "oom",
			ChainID:   "chain-y",
			CreatedAt: inWindow.Add(time.Minute),
		})
		seedLLMInteraction(t, bc.Client, s2, st2, e2, "m", 25, 25, 50, floatPtr(0.25), 0)

		// Session with no LLM rows still appears in breakdowns via LEFT JOIN.
		seedUsageSession(t, bc.Client, usageSeed{
			AlertData: "no-llm",
			AlertType: "idle",
			ChainID:   "chain-z",
			CreatedAt: inWindow.Add(2 * time.Minute),
		})

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)

		alertMap := map[string]models.UsageAlertBreakdown{}
		for _, row := range summary.ByAlertType {
			alertMap[row.AlertType] = row
		}
		require.Contains(t, alertMap, "oom")
		assert.Equal(t, int64(150), alertMap["oom"].TotalTokens)
		require.NotNil(t, alertMap["oom"].EstimatedCostUsd)
		assert.InDelta(t, 0.75, *alertMap["oom"].EstimatedCostUsd, 1e-9)
		require.Contains(t, alertMap, "idle")
		assert.Equal(t, int64(0), alertMap["idle"].TotalTokens)

		chainMap := map[string]models.UsageChainBreakdown{}
		for _, row := range summary.ByChain {
			chainMap[row.ChainID] = row
		}
		require.Contains(t, chainMap, "chain-x")
		assert.Equal(t, int64(100), chainMap["chain-x"].TotalTokens)
		require.Contains(t, chainMap, "chain-z")
		assert.Equal(t, int64(0), chainMap["chain-z"].TotalTokens)
	})

	t.Run("estimation disabled omits cost fields and defaults rank_by tokens", func(t *testing.T) {
		dc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, dc.Client)
		svc.SetCostEstimationEnabled(false)

		sid, stageID, execID := seedUsageSession(t, dc.Client, usageSeed{
			AlertData: "disabled-cost",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, dc.Client, sid, stageID, execID, "model-a", 100, 50, 150, floatPtr(0.01), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.False(t, summary.CostEstimationEnabled)
		assert.Equal(t, models.UsageRankByTokens, summary.RankBy)
		assert.Nil(t, summary.Totals.EstimatedCostUsd)
		assert.Empty(t, summary.Totals.CostCompleteness)
		assert.Nil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, int64(150), summary.Totals.TotalTokens)

		require.Len(t, summary.ByModel, 1)
		assert.Nil(t, summary.ByModel[0].EstimatedCostUsd)
		assert.Nil(t, summary.ByModel[0].Priced)

		require.Len(t, summary.TopSessions, 1)
		assert.Nil(t, summary.TopSessions[0].EstimatedCostUsd)
		assert.Empty(t, summary.TopSessions[0].CostCompleteness)
	})

	t.Run("empty window returns zero totals and empty sections", func(t *testing.T) {
		ec := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, ec.Client)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(0), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.0, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.CostCompletenessNone, summary.Totals.CostCompleteness)
		assert.Empty(t, summary.ByModel)
		assert.Empty(t, summary.ByAlertType)
		assert.Empty(t, summary.ByChain)
		assert.Empty(t, summary.TopSessions)
	})

	t.Run("start boundary is inclusive", func(t *testing.T) {
		sc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, sc.Client)

		sid, stageID, execID := seedUsageSession(t, sc.Client, usageSeed{
			AlertData: "on-start",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: windowStart,
		})
		seedLLMInteraction(t, sc.Client, sid, stageID, execID, "model-a", 40, 10, 50, floatPtr(0.02), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(50), summary.Totals.TotalTokens)
		require.Len(t, summary.TopSessions, 1)
		assert.Equal(t, sid, summary.TopSessions[0].SessionID)
	})

	t.Run("explicit zero cost is priced complete", func(t *testing.T) {
		zc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, zc.Client)

		sid, stageID, execID := seedUsageSession(t, zc.Client, usageSeed{
			AlertData: "zero-cost",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, zc.Client, sid, stageID, execID, "free-model", 10, 5, 15, floatPtr(0.0), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.0, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.CostCompletenessComplete, summary.Totals.CostCompleteness)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 0, *summary.Totals.UnpricedInteractionCount)
		require.Len(t, summary.ByModel, 1)
		require.NotNil(t, summary.ByModel[0].Priced)
		assert.True(t, *summary.ByModel[0].Priced)
	})

	t.Run("all interaction types count in totals", func(t *testing.T) {
		ic := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, ic.Client)

		sid, stageID, execID := seedUsageSession(t, ic.Client, usageSeed{
			AlertData: "multi-type",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, ic.Client, sid, stageID, execID, "m", 10, 10, 20, floatPtr(0.1), 0)
		seedUsageLLMInteractionType(t, ic.Client, sid, stageID, execID,
			llminteraction.InteractionTypeSummarization, "m", 30, 20, 50, floatPtr(0.2))
		seedUsageLLMInteractionType(t, ic.Client, sid, stageID, execID,
			llminteraction.InteractionTypeScoring, "m", 5, 5, 10, floatPtr(0.05))

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(80), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.35, *summary.Totals.EstimatedCostUsd, 1e-9)
	})

	t.Run("top_sessions capped at 20", func(t *testing.T) {
		tc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, tc.Client)

		var lowestID string
		for i := range 21 {
			tokens := (21 - i) * 10 // 210, 200, ..., 10
			sid, stageID, execID := seedUsageSession(t, tc.Client, usageSeed{
				AlertData: "top-cap",
				AlertType: "pod-crash",
				ChainID:   "k8s-analysis",
				CreatedAt: inWindow.Add(time.Duration(i) * time.Minute),
			})
			seedLLMInteraction(t, tc.Client, sid, stageID, execID, "m", tokens, 0, tokens, floatPtr(float64(tokens)*0.001), 0)
			if i == 20 {
				lowestID = sid
			}
		}

		summary, err := svc.GetUsageSummary(ctx, models.UsageSummaryParams{
			StartDate: windowStart,
			EndDate:   windowEnd,
			RankBy:    models.UsageRankByTokens,
		})
		require.NoError(t, err)
		require.Len(t, summary.TopSessions, 20)
		assert.Equal(t, int64(210), summary.TopSessions[0].TotalTokens)
		assert.Equal(t, int64(20), summary.TopSessions[19].TotalTokens)
		for _, item := range summary.TopSessions {
			assert.NotEqual(t, lowestID, item.SessionID)
		}
	})
}

type usageSeed struct {
	AlertData string
	AlertType string
	ChainID   string
	CreatedAt time.Time
}

func seedUsageSession(t *testing.T, client *ent.Client, seed usageSeed) (sessionID, stageID, execID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	started := now.Add(-5 * time.Second)
	completed := now
	sessionID = uuid.New().String()

	sess := client.AlertSession.Create().
		SetID(sessionID).
		SetAlertData(seed.AlertData).
		SetAlertType(seed.AlertType).
		SetChainID(seed.ChainID).
		SetAgentType("kubernetes").
		SetStatus(alertsession.StatusCompleted).
		SetCreatedAt(seed.CreatedAt).
		SetStartedAt(started).
		SetCompletedAt(completed).
		SaveX(ctx)

	stg := client.Stage.Create().
		SetID(uuid.New().String()).
		SetSessionID(sess.ID).
		SetStageName("analysis").
		SetStageIndex(1).
		SetExpectedAgentCount(1).
		SetStatus(stage.StatusCompleted).
		SetStartedAt(started).
		SetCompletedAt(completed).
		SaveX(ctx)

	exec := client.AgentExecution.Create().
		SetID(uuid.New().String()).
		SetSessionID(sess.ID).
		SetStageID(stg.ID).
		SetAgentName("TestAgent").
		SetAgentIndex(1).
		SetLlmBackend(string(config.LLMBackendLangChain)).
		SetStartedAt(started).
		SetStatus("completed").
		SaveX(ctx)

	return sess.ID, stg.ID, exec.ID
}

func seedUsageLLMInteractionType(
	t *testing.T,
	client *ent.Client,
	sessionID, stageID, execID string,
	interactionType llminteraction.InteractionType,
	modelName string,
	inputTokens, outputTokens, totalTokens int,
	costUSD *float64,
) {
	t.Helper()
	create := client.LLMInteraction.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetExecutionID(execID).
		SetInteractionType(interactionType).
		SetModelName(modelName).
		SetLlmRequest(map[string]any{}).
		SetLlmResponse(map[string]any{}).
		SetInputTokens(inputTokens).
		SetOutputTokens(outputTokens).
		SetTotalTokens(totalTokens)
	if costUSD != nil {
		create = create.SetEstimatedCostUsd(*costUSD)
	}
	create.SaveX(context.Background())
}
