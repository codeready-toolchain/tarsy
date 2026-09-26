package services

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// usageSeriesRejectedZone is an IANA-shaped name this test installs for Go
// only. PostgreSQL does not ship it, so AT TIME ZONE raises SQLSTATE 22023.
const usageSeriesRejectedZone = "Test/NotAZone"

func TestMain(m *testing.M) {
	// LoadLocation reads ZONEINFO once, on the first call in the process.
	// Install the extra zone before any test loads a location. Names that are
	// not in this directory still fall through to the system zoneinfo.
	dir, err := os.MkdirTemp("", "tarsy-zoneinfo")
	if err != nil {
		fmt.Fprintf(os.Stderr, "zoneinfo temp dir: %v\n", err)
		os.Exit(1)
	}
	if err := installUsageSeriesRejectedZone(dir); err != nil {
		fmt.Fprintf(os.Stderr, "install test zone: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := os.Setenv("ZONEINFO", dir); err != nil {
		fmt.Fprintf(os.Stderr, "set ZONEINFO: %v\n", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func installUsageSeriesRejectedZone(dir string) error {
	src, err := readZoneinfoUTC()
	if err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.FromSlash(usageSeriesRejectedZone))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, src, 0o644)
}

func readZoneinfoUTC() ([]byte, error) {
	for _, path := range []string{
		"/usr/share/zoneinfo/UTC",
		"/usr/share/zoneinfo/Etc/UTC",
		"/usr/share/lib/zoneinfo/UTC",
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("UTC zoneinfo file not found")
}

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
		assert.Equal(t, int64(1), summary.Totals.SessionCount)
		assert.Equal(t, int64(150), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.01, *summary.Totals.EstimatedCostUsd, 1e-9)
		require.NotNil(t, summary.Totals.AverageCostUsd)
		assert.InDelta(t, 0.01, *summary.Totals.AverageCostUsd, 1e-9)
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
		assert.Equal(t, int64(0), summary.Totals.SessionCount)
		assert.Nil(t, summary.Totals.AverageCostUsd)
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
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(300), *summary.Totals.UnpricedTokenCount)

		byModel := map[string]models.UsageModelBreakdown{}
		for _, m := range summary.ByModel {
			byModel[m.ModelName] = m
		}
		require.Contains(t, byModel, "priced-model")
		require.Contains(t, byModel, "unpriced-model")
		assert.Equal(t, int64(1), byModel["priced-model"].SessionCount)
		require.NotNil(t, byModel["priced-model"].AverageCostUsd)
		assert.InDelta(t, 0.012, *byModel["priced-model"].AverageCostUsd, 1e-9)
		assert.Equal(t, int64(1), byModel["unpriced-model"].SessionCount)
		require.NotNil(t, byModel["unpriced-model"].AverageCostUsd)
		assert.InDelta(t, 0.0, *byModel["unpriced-model"].AverageCostUsd, 1e-9)
		require.NotNil(t, byModel["priced-model"].Priced)
		assert.True(t, *byModel["priced-model"].Priced)
		require.NotNil(t, byModel["priced-model"].UnpricedInteractionCount)
		assert.Equal(t, 0, *byModel["priced-model"].UnpricedInteractionCount)
		require.NotNil(t, byModel["unpriced-model"].Priced)
		assert.False(t, *byModel["unpriced-model"].Priced)
		require.NotNil(t, byModel["unpriced-model"].UnpricedInteractionCount)
		assert.Equal(t, 1, *byModel["unpriced-model"].UnpricedInteractionCount)

		require.Len(t, summary.TopSessions, 1)
		assert.Equal(t, models.CostCompletenessPartial, summary.TopSessions[0].CostCompleteness)
		require.NotNil(t, summary.TopSessions[0].EstimatedCostUsd)
		assert.InDelta(t, 0.012, *summary.TopSessions[0].EstimatedCostUsd, 1e-9)
	})

	t.Run("unpriced_token_count sums unpriced token-bearing rows only", func(t *testing.T) {
		uc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, uc.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, uc.Client, usageSeed{
			AlertData: "unpriced-token-sum",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, uc.Client, sid, stageID, execID, "priced-model", 100, 50, 150, floatPtr(0.01), 0)
		seedLLMInteraction(t, uc.Client, sid, stageID, execID, "unpriced-a", 80, 20, 100, nil, 0)
		seedLLMInteraction(t, uc.Client, sid, stageID, execID, "unpriced-b", 150, 50, 200, nil, 0)
		seedLLMInteraction(t, uc.Client, sid, stageID, execID, "zero-token", 0, 0, 0, nil, 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(450), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 2, *summary.Totals.UnpricedInteractionCount)
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(300), *summary.Totals.UnpricedTokenCount)
	})

	t.Run("sums cache tokens on totals and by_model only", func(t *testing.T) {
		uc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, uc.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, uc.Client, usageSeed{
			AlertData: "cache-sum",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		uc.Client.LLMInteraction.Create().
			SetID(uuid.New().String()).
			SetSessionID(sid).
			SetStageID(stageID).
			SetExecutionID(execID).
			SetInteractionType(llminteraction.InteractionTypeIteration).
			SetModelName("gemini-flash").
			SetLlmRequest(map[string]any{}).
			SetLlmResponse(map[string]any{}).
			SetInputTokens(60).
			SetOutputTokens(20).
			SetTotalTokens(80).
			SetCacheReadTokens(40).
			SetCacheCreationTokens(10).
			SetEstimatedCostUsd(0.01).
			SaveX(ctx)
		seedLLMInteraction(t, uc.Client, sid, stageID, execID, "gemini-flash", 10, 5, 15, floatPtr(0.001), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(70), summary.Totals.InputTokens)
		assert.Equal(t, int64(25), summary.Totals.OutputTokens)
		assert.Equal(t, int64(40), summary.Totals.CacheReadTokens)
		assert.Equal(t, int64(10), summary.Totals.CacheCreationTokens)
		assert.Equal(t, int64(95), summary.Totals.TotalTokens)

		require.Len(t, summary.ByModel, 1)
		assert.Equal(t, int64(70), summary.ByModel[0].InputTokens)
		assert.Equal(t, int64(25), summary.ByModel[0].OutputTokens)
		assert.Equal(t, int64(40), summary.ByModel[0].CacheReadTokens)
		assert.Equal(t, int64(10), summary.ByModel[0].CacheCreationTokens)
		require.Len(t, summary.ByAlertType, 1)
		assert.Equal(t, "pod-crash", summary.ByAlertType[0].AlertType)
		assert.Equal(t, int64(95), summary.ByAlertType[0].TotalTokens)
	})

	t.Run("cache-only rows count as token-bearing", func(t *testing.T) {
		uc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, uc.Client)
		ctx := t.Context()

		unpricedID, uStage, uExec := seedUsageSession(t, uc.Client, usageSeed{
			AlertData: "cache-only-unpriced",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		uc.Client.LLMInteraction.Create().
			SetID(uuid.New().String()).
			SetSessionID(unpricedID).
			SetStageID(uStage).
			SetExecutionID(uExec).
			SetInteractionType(llminteraction.InteractionTypeIteration).
			SetModelName("cache-unpriced").
			SetLlmRequest(map[string]any{}).
			SetLlmResponse(map[string]any{}).
			SetInputTokens(0).
			SetOutputTokens(0).
			SetTotalTokens(0).
			SetCacheReadTokens(40).
			SaveX(ctx)

		pricedID, pStage, pExec := seedUsageSession(t, uc.Client, usageSeed{
			AlertData: "cache-only-priced",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow.Add(time.Minute),
		})
		uc.Client.LLMInteraction.Create().
			SetID(uuid.New().String()).
			SetSessionID(pricedID).
			SetStageID(pStage).
			SetExecutionID(pExec).
			SetInteractionType(llminteraction.InteractionTypeIteration).
			SetModelName("cache-priced").
			SetLlmRequest(map[string]any{}).
			SetLlmResponse(map[string]any{}).
			SetInputTokens(0).
			SetOutputTokens(0).
			SetTotalTokens(0).
			SetCacheCreationTokens(10).
			SetEstimatedCostUsd(0.01).
			SaveX(ctx)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(0), summary.Totals.InputTokens)
		assert.Equal(t, int64(40), summary.Totals.CacheReadTokens)
		assert.Equal(t, int64(10), summary.Totals.CacheCreationTokens)
		assert.Equal(t, int64(0), summary.Totals.TotalTokens)
		assert.Equal(t, models.CostCompletenessPartial, summary.Totals.CostCompleteness)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 1, *summary.Totals.UnpricedInteractionCount)
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(40), *summary.Totals.UnpricedTokenCount)

		byModel := map[string]models.UsageModelBreakdown{}
		for _, m := range summary.ByModel {
			byModel[m.ModelName] = m
		}
		require.Contains(t, byModel, "cache-unpriced")
		require.Contains(t, byModel, "cache-priced")
		require.NotNil(t, byModel["cache-unpriced"].Priced)
		assert.False(t, *byModel["cache-unpriced"].Priced)
		assert.Equal(t, int64(40), byModel["cache-unpriced"].CacheReadTokens)
		require.NotNil(t, byModel["cache-priced"].Priced)
		assert.True(t, *byModel["cache-priced"].Priced)
		assert.Equal(t, int64(10), byModel["cache-priced"].CacheCreationTokens)

		topByID := map[string]models.UsageTopSession{}
		for _, row := range summary.TopSessions {
			topByID[row.SessionID] = row
		}
		require.Contains(t, topByID, unpricedID)
		require.Contains(t, topByID, pricedID)
		assert.Equal(t, models.CostCompletenessNone, topByID[unpricedID].CostCompleteness)
		assert.Equal(t, models.CostCompletenessComplete, topByID[pricedID].CostCompleteness)
	})

	t.Run("model average counts distinct sessions that used that model", func(t *testing.T) {
		oc := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, oc.Client)
		ctx := t.Context()

		aID, aStage, aExec := seedUsageSession(t, oc.Client, usageSeed{
			AlertData: "shared-a",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, oc.Client, aID, aStage, aExec, "shared-model", 10, 10, 20, floatPtr(0.10), 0)
		seedLLMInteraction(t, oc.Client, aID, aStage, aExec, "solo-model", 20, 20, 40, floatPtr(0.40), 0)

		bID, bStage, bExec := seedUsageSession(t, oc.Client, usageSeed{
			AlertData: "shared-b",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow.Add(time.Minute),
		})
		seedLLMInteraction(t, oc.Client, bID, bStage, bExec, "shared-model", 30, 30, 60, floatPtr(0.30), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(2), summary.Totals.SessionCount)
		require.NotNil(t, summary.Totals.AverageCostUsd)
		assert.InDelta(t, 0.40, *summary.Totals.AverageCostUsd, 1e-9)

		byModel := map[string]models.UsageModelBreakdown{}
		for _, m := range summary.ByModel {
			byModel[m.ModelName] = m
		}
		require.Contains(t, byModel, "shared-model")
		require.Contains(t, byModel, "solo-model")

		assert.Equal(t, int64(2), byModel["shared-model"].SessionCount)
		require.NotNil(t, byModel["shared-model"].EstimatedCostUsd)
		assert.InDelta(t, 0.40, *byModel["shared-model"].EstimatedCostUsd, 1e-9)
		require.NotNil(t, byModel["shared-model"].AverageCostUsd)
		assert.InDelta(t, 0.20, *byModel["shared-model"].AverageCostUsd, 1e-9)

		assert.Equal(t, int64(1), byModel["solo-model"].SessionCount)
		require.NotNil(t, byModel["solo-model"].EstimatedCostUsd)
		assert.InDelta(t, 0.40, *byModel["solo-model"].EstimatedCostUsd, 1e-9)
		require.NotNil(t, byModel["solo-model"].AverageCostUsd)
		assert.InDelta(t, 0.40, *byModel["solo-model"].AverageCostUsd, 1e-9)
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
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(15), *summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(0), summary.Totals.CacheReadTokens)
		assert.Equal(t, int64(0), summary.Totals.CacheCreationTokens)
		require.Len(t, summary.ByModel, 1)
		assert.Equal(t, int64(0), summary.ByModel[0].CacheReadTokens)
		assert.Equal(t, int64(0), summary.ByModel[0].CacheCreationTokens)
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
		require.Len(t, byCost.ByModel, 1)
		assert.Equal(t, int64(2), byCost.ByModel[0].SessionCount)
		require.NotNil(t, byCost.ByModel[0].AverageCostUsd)
		assert.InDelta(t, (0.001+5.0)/2, *byCost.ByModel[0].AverageCostUsd, 1e-9)

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
		assert.Equal(t, int64(1), filtered.Totals.SessionCount)
		assert.Equal(t, int64(100), filtered.Totals.TotalTokens)
		require.NotNil(t, filtered.Totals.AverageCostUsd)
		assert.InDelta(t, 0.1, *filtered.Totals.AverageCostUsd, 1e-9)
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
		assert.Equal(t, int64(3), summary.Totals.SessionCount)
		require.NotNil(t, summary.Totals.AverageCostUsd)
		assert.InDelta(t, 0.25, *summary.Totals.AverageCostUsd, 1e-9)

		alertMap := map[string]models.UsageAlertBreakdown{}
		for _, row := range summary.ByAlertType {
			alertMap[row.AlertType] = row
		}
		require.Contains(t, alertMap, "oom")
		assert.Equal(t, int64(2), alertMap["oom"].SessionCount)
		assert.Equal(t, int64(150), alertMap["oom"].TotalTokens)
		require.NotNil(t, alertMap["oom"].EstimatedCostUsd)
		assert.InDelta(t, 0.75, *alertMap["oom"].EstimatedCostUsd, 1e-9)
		require.NotNil(t, alertMap["oom"].AverageCostUsd)
		assert.InDelta(t, 0.375, *alertMap["oom"].AverageCostUsd, 1e-9)
		require.Contains(t, alertMap, "idle")
		assert.Equal(t, int64(1), alertMap["idle"].SessionCount)
		assert.Equal(t, int64(0), alertMap["idle"].TotalTokens)
		require.NotNil(t, alertMap["idle"].AverageCostUsd)
		assert.InDelta(t, 0.0, *alertMap["idle"].AverageCostUsd, 1e-9)

		chainMap := map[string]models.UsageChainBreakdown{}
		for _, row := range summary.ByChain {
			chainMap[row.ChainID] = row
		}
		require.Contains(t, chainMap, "chain-x")
		assert.Equal(t, int64(1), chainMap["chain-x"].SessionCount)
		assert.Equal(t, int64(100), chainMap["chain-x"].TotalTokens)
		require.NotNil(t, chainMap["chain-x"].AverageCostUsd)
		assert.InDelta(t, 0.5, *chainMap["chain-x"].AverageCostUsd, 1e-9)
		require.Contains(t, chainMap, "chain-z")
		assert.Equal(t, int64(1), chainMap["chain-z"].SessionCount)
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
		assert.Nil(t, summary.Totals.AverageCostUsd)
		assert.Empty(t, summary.Totals.CostCompleteness)
		assert.Nil(t, summary.Totals.UnpricedInteractionCount)
		assert.Nil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(1), summary.Totals.SessionCount)
		assert.Equal(t, int64(150), summary.Totals.TotalTokens)

		require.Len(t, summary.ByModel, 1)
		assert.Equal(t, int64(1), summary.ByModel[0].SessionCount)
		assert.Nil(t, summary.ByModel[0].EstimatedCostUsd)
		assert.Nil(t, summary.ByModel[0].AverageCostUsd)
		assert.Nil(t, summary.ByModel[0].Priced)
		require.Len(t, summary.ByAlertType, 1)
		assert.Equal(t, int64(1), summary.ByAlertType[0].SessionCount)
		assert.Nil(t, summary.ByAlertType[0].AverageCostUsd)
		require.Len(t, summary.ByChain, 1)
		assert.Equal(t, int64(1), summary.ByChain[0].SessionCount)
		assert.Nil(t, summary.ByChain[0].AverageCostUsd)

		require.Len(t, summary.TopSessions, 1)
		assert.Nil(t, summary.TopSessions[0].EstimatedCostUsd)
		assert.Empty(t, summary.TopSessions[0].CostCompleteness)
	})

	t.Run("empty window returns zero totals and empty sections", func(t *testing.T) {
		ec := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, ec.Client)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, int64(0), summary.Totals.SessionCount)
		assert.Nil(t, summary.Totals.AverageCostUsd)
		assert.Equal(t, int64(0), summary.Totals.TotalTokens)
		require.NotNil(t, summary.Totals.EstimatedCostUsd)
		assert.InDelta(t, 0.0, *summary.Totals.EstimatedCostUsd, 1e-9)
		assert.Equal(t, models.CostCompletenessNone, summary.Totals.CostCompleteness)
		require.NotNil(t, summary.Totals.UnpricedInteractionCount)
		assert.Equal(t, 0, *summary.Totals.UnpricedInteractionCount)
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(0), *summary.Totals.UnpricedTokenCount)
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
		require.NotNil(t, summary.Totals.UnpricedTokenCount)
		assert.Equal(t, int64(0), *summary.Totals.UnpricedTokenCount)
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
		require.Len(t, summary.ByModel, 1)
		assert.Equal(t, int64(1), summary.ByModel[0].SessionCount)
		require.NotNil(t, summary.ByModel[0].AverageCostUsd)
		assert.InDelta(t, 0.35, *summary.ByModel[0].AverageCostUsd, 1e-9)
		require.Len(t, summary.ByAlertType, 1)
		assert.Equal(t, int64(1), summary.ByAlertType[0].SessionCount)
	})

	t.Run("empty alert_type normalized to nil in top_sessions", func(t *testing.T) {
		ac := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, ac.Client)

		sid, stageID, execID := seedUsageSession(t, ac.Client, usageSeed{
			AlertData: "empty-alert-type",
			AlertType: "",
			ChainID:   "k8s-analysis",
			CreatedAt: inWindow,
		})
		seedLLMInteraction(t, ac.Client, sid, stageID, execID, "m", 10, 5, 15, floatPtr(0.01), 0)

		summary, err := svc.GetUsageSummary(ctx, params)
		require.NoError(t, err)
		require.Len(t, summary.TopSessions, 1)
		assert.Nil(t, summary.TopSessions[0].AlertType)
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
		assert.Equal(t, int64(21), summary.Totals.SessionCount)
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

func TestPostgresUnrecognizedTimezone(t *testing.T) {
	t.Parallel()
	zoneErr := &pgconn.PgError{
		Code:    sqlstateInvalidParameterValue,
		Message: `time zone "right/UTC" not recognized`,
	}
	assert.True(t, postgresUnrecognizedTimezone(zoneErr))
	assert.True(t, postgresUnrecognizedTimezone(fmt.Errorf("failed to query usage series: %w", zoneErr)))
	assert.False(t, postgresUnrecognizedTimezone(&pgconn.PgError{
		Code:    sqlstateInvalidParameterValue,
		Message: `invalid value for parameter "TimeZone"`,
	}))
	assert.False(t, postgresUnrecognizedTimezone(&pgconn.PgError{
		Code:    "23505",
		Message: `time zone "right/UTC" not recognized`,
	}))
	assert.False(t, postgresUnrecognizedTimezone(fmt.Errorf("time zone not recognized (SQLSTATE 22023)")))
}

func TestUsageAverageCostUSD(t *testing.T) {
	t.Parallel()
	cost := 1.5
	zero := 0.0
	tests := []struct {
		name         string
		cost         *float64
		sessionCount int64
		wantNil      bool
		want         float64
	}{
		{name: "nil cost", cost: nil, sessionCount: 3, wantNil: true},
		{name: "zero sessions", cost: &cost, sessionCount: 0, wantNil: true},
		{name: "divides cost by count", cost: &cost, sessionCount: 3, want: 0.5},
		{name: "zero cost with sessions", cost: &zero, sessionCount: 2, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usageAverageCostUSD(tt.cost, tt.sessionCount)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.InDelta(t, tt.want, *got, 1e-9)
		})
	}
}

func TestSessionService_GetUsageSeries(t *testing.T) {
	windowStart := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC)
	dayOne := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	dayTwo := time.Date(2024, 6, 2, 12, 0, 0, 0, time.UTC)

	params := models.UsageSeriesParams{
		StartDate: windowStart,
		EndDate:   windowEnd,
		Timezone:  "UTC",
	}
	summaryParams := models.UsageSummaryParams{
		StartDate: windowStart,
		EndDate:   windowEnd,
	}

	t.Run("splits sessions and models across days and matches the summary", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		aID, aStage, aExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "day-one-a",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, aID, aStage, aExec, "model-a", 100, 50, 150, floatPtr(1.0), 0)
		seedLLMInteraction(t, client.Client, aID, aStage, aExec, "model-b", 10, 10, 20, floatPtr(0.5), 0)
		// A session with no LLM rows still counts toward that day's session_count.
		seedUsageSession(t, client.Client, usageSeed{
			AlertData: "day-one-empty",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne.Add(time.Hour),
		})

		bID, bStage, bExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "day-two",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayTwo,
		})
		seedLLMInteraction(t, client.Client, bID, bStage, bExec, "model-a", 20, 10, 30, floatPtr(0.25), 0)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		assert.True(t, series.CostEstimationEnabled)
		assert.Equal(t, "UTC", series.Timezone)
		assert.Equal(t, models.UsageBucketDay, series.Bucket)
		assert.Equal(t, []string{"model-a", "model-b"}, series.Models)
		require.Len(t, series.Points, 2)

		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		assert.Equal(t, int64(2), june1.SessionCount)
		assert.InDelta(t, 1.5, june1.EstimatedCostUsd, 1e-9)
		require.NotNil(t, june1.AverageCostUsd)
		assert.InDelta(t, 0.75, *june1.AverageCostUsd, 1e-9)
		assert.InDelta(t, 1.0, june1.ByModel["model-a"], 1e-9)
		assert.InDelta(t, 0.5, june1.ByModel["model-b"], 1e-9)
		assert.Empty(t, june1.OtherModels)

		june2 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 2)
		assert.Equal(t, int64(1), june2.SessionCount)
		assert.InDelta(t, 0.25, june2.EstimatedCostUsd, 1e-9)
		assert.InDelta(t, 0.25, june2.ByModel["model-a"], 1e-9)
		_, hasB := june2.ByModel["model-b"]
		assert.False(t, hasB)

		summary, err := svc.GetUsageSummary(ctx, summaryParams)
		require.NoError(t, err)
		assertSeriesMatchesSummary(t, summary, series)
	})

	t.Run("zero-fills an empty day and omits its average", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "only-day-one",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(0.2), 0)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		require.Len(t, series.Points, 2)

		empty := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 2)
		assert.Equal(t, int64(0), empty.SessionCount)
		assert.InDelta(t, 0, empty.EstimatedCostUsd, 1e-9)
		assert.Nil(t, empty.AverageCostUsd)
		assert.Empty(t, empty.ByModel)

		raw, err := json.Marshal(empty)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "average_cost_usd")
	})

	t.Run("filters match the summary", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		keepID, keepStage, keepExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "kept",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, keepID, keepStage, keepExec, "model-a", 10, 10, 20, floatPtr(0.4), 0)

		otherID, otherStage, otherExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "other",
			AlertType: "oom",
			ChainID:   "other-chain",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, otherID, otherStage, otherExec, "model-b", 10, 10, 20, floatPtr(9), 0)

		deletedID, deletedStage, deletedExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "deleted",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, deletedID, deletedStage, deletedExec, "model-a", 10, 10, 20, floatPtr(8), 0)
		require.NoError(t, client.Client.AlertSession.UpdateOneID(deletedID).SetDeletedAt(time.Now()).Exec(ctx))

		filtered := params
		filtered.AlertType = "pod-crash"
		filtered.ChainID = "k8s-analysis"
		series, err := svc.GetUsageSeries(ctx, filtered)
		require.NoError(t, err)
		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		assert.Equal(t, int64(1), june1.SessionCount)
		assert.InDelta(t, 0.4, june1.EstimatedCostUsd, 1e-9)

		summary, err := svc.GetUsageSummary(ctx, models.UsageSummaryParams{
			StartDate: windowStart,
			EndDate:   windowEnd,
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
		})
		require.NoError(t, err)
		assertSeriesMatchesSummary(t, summary, series)
	})

	t.Run("excludes a session created before the window", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		before := time.Date(2024, 5, 15, 12, 0, 0, 0, time.UTC)
		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "before",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: before,
		})
		seedLLMInteractionAt(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(4), dayOne)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		for _, point := range series.Points {
			assert.Equal(t, int64(0), point.SessionCount)
			assert.InDelta(t, 0, point.EstimatedCostUsd, 1e-9)
		}
	})

	t.Run("attributes later interactions to the session start day", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		monday := time.Date(2024, 6, 3, 15, 0, 0, 0, time.UTC)
		wednesday := time.Date(2024, 6, 5, 15, 0, 0, 0, time.UTC)
		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "monday-session",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: monday,
		})
		seedLLMInteractionAt(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(1.25), wednesday)

		series, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, 6, 6, 0, 0, 0, 0, time.UTC),
			Timezone:  "UTC",
		})
		require.NoError(t, err)
		require.Len(t, series.Points, 3)
		mon := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 3)
		assert.InDelta(t, 1.25, mon.EstimatedCostUsd, 1e-9)
		assert.Equal(t, int64(1), mon.SessionCount)
		wed := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 5)
		assert.Equal(t, int64(0), wed.SessionCount)
		assert.InDelta(t, 0, wed.EstimatedCostUsd, 1e-9)
	})

	t.Run("unpriced rows do not create a series", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "partial",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "priced", 100, 50, 150, floatPtr(0.3), 0)
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "unpriced", 200, 100, 300, nil, 0)
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "explicit-zero", 10, 10, 20, floatPtr(0), 0)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"priced"}, series.Models)
		assert.Equal(t, models.CostCompletenessPartial, series.CostCompleteness)
		require.NotNil(t, series.UnpricedInteractionCount)
		assert.Equal(t, 1, *series.UnpricedInteractionCount)
		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		assert.InDelta(t, 0.3, june1.EstimatedCostUsd, 1e-9)
		_, hasUnpriced := june1.ByModel["unpriced"]
		assert.False(t, hasUnpriced)
		assert.Empty(t, june1.OtherModels)

		summary, err := svc.GetUsageSummary(ctx, summaryParams)
		require.NoError(t, err)
		assertSeriesMatchesSummary(t, summary, series)
	})

	t.Run("collapses models past the top six", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "many-models",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		// Five distinct costs, then a tie on the sixth slot (a-tie before z-tie by name).
		costs := []struct {
			name string
			cost float64
		}{
			{"m7", 7},
			{"m6", 6},
			{"m5", 5},
			{"m4", 4},
			{"m3", 3},
			{"z-tie", 2},
			{"a-tie", 2},
		}
		for _, model := range costs {
			seedLLMInteraction(t, client.Client, sid, stageID, execID, model.name, 10, 10, 20, floatPtr(model.cost), 0)
		}
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "unpriced", 40, 10, 50, nil, 0)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"m7", "m6", "m5", "m4", "m3", "a-tie"}, series.Models)
		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		require.Len(t, june1.OtherModels, 1)
		assert.Equal(t, "z-tie", june1.OtherModels[0].ModelName)
		assert.InDelta(t, 2, june1.OtherModels[0].EstimatedCostUsd, 1e-9)
		assert.InDelta(t, 29, june1.EstimatedCostUsd, 1e-9)
		_, hasUnpriced := june1.ByModel["unpriced"]
		assert.False(t, hasUnpriced)
	})

	t.Run("buckets on the requested local calendar day", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		// 2024-06-02 06:30 UTC is 2024-06-01 23:30 in America/Los_Angeles (PDT, UTC-7).
		created := time.Date(2024, 6, 2, 6, 30, 0, 0, time.UTC)
		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "tz-boundary",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: created,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(1), 0)

		wide := models.UsageSeriesParams{
			StartDate: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC),
			Timezone:  "America/Los_Angeles",
		}
		series, err := svc.GetUsageSeries(ctx, wide)
		require.NoError(t, err)
		assert.Equal(t, "America/Los_Angeles", series.Timezone)
		local := seriesPointOn(t, series.Points, "America/Los_Angeles", 2024, time.June, 1)
		assert.Equal(t, int64(1), local.SessionCount)
		assert.InDelta(t, 1, local.EstimatedCostUsd, 1e-9)
		next := seriesPointOn(t, series.Points, "America/Los_Angeles", 2024, time.June, 2)
		assert.Equal(t, int64(0), next.SessionCount)

		utc, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: wide.StartDate,
			EndDate:   wide.EndDate,
			Timezone:  "Not/AZone",
		})
		require.NoError(t, err)
		assert.Equal(t, "UTC", utc.Timezone)
		utcPoint := seriesPointOn(t, utc.Points, "UTC", 2024, time.June, 2)
		assert.Equal(t, int64(1), utcPoint.SessionCount)
		may31 := seriesPointOn(t, utc.Points, "UTC", 2024, time.June, 1)
		assert.Equal(t, int64(0), may31.SessionCount)
	})

	t.Run("falls back to UTC when PostgreSQL rejects the zone", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		loc, loadErr := time.LoadLocation(usageSeriesRejectedZone)
		require.NoError(t, loadErr)
		require.Equal(t, usageSeriesRejectedZone, loc.String())
		zone := usageSeriesRejectedZone
		created := time.Date(2024, 6, 2, 6, 30, 0, 0, time.UTC)
		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "pg-rejects-zone",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: created,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(1), 0)

		series, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, 6, 3, 0, 0, 0, 0, time.UTC),
			Timezone:  zone,
		})
		require.NoError(t, err)
		assert.Equal(t, "UTC", series.Timezone)
		june2 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 2)
		assert.Equal(t, int64(1), june2.SessionCount)
	})

	t.Run("clips partial days and excludes an end on local midnight", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		// 16:00 UTC is 09:00 PDT on June 1. 18:00 UTC is 11:00 PDT on June 2.
		for _, created := range []time.Time{
			time.Date(2024, 6, 1, 16, 0, 0, 0, time.UTC),
			time.Date(2024, 6, 2, 18, 0, 0, 0, time.UTC),
		} {
			sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
				AlertData: created.Format(time.RFC3339),
				AlertType: "pod-crash",
				ChainID:   "k8s-analysis",
				CreatedAt: created,
			})
			seedLLMInteraction(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(1), 0)
		}

		// 08:30 PDT June 1 through 13:00 PDT June 2. Both edge days are shorter than a calendar day.
		start := time.Date(2024, 6, 1, 15, 30, 0, 0, time.UTC)
		end := time.Date(2024, 6, 2, 20, 0, 0, 0, time.UTC)
		series, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: start,
			EndDate:   end,
			Timezone:  "America/Los_Angeles",
		})
		require.NoError(t, err)
		require.Len(t, series.Points, 2)
		assert.Equal(t, []string{"2024-06-01", "2024-06-02"}, localSeriesDates(t, series.Points, "America/Los_Angeles"))
		assert.True(t, series.Points[0].Start.Equal(start), "first day starts at the window, not local midnight")
		assert.True(t, series.Points[0].End.Equal(time.Date(2024, 6, 2, 7, 0, 0, 0, time.UTC)))
		assert.True(t, series.Points[1].Start.Equal(time.Date(2024, 6, 2, 7, 0, 0, 0, time.UTC)))
		assert.True(t, series.Points[1].End.Equal(end), "last day ends at the window, not the next local midnight")
		assert.Equal(t, int64(1), series.Points[0].SessionCount)
		assert.Equal(t, int64(1), series.Points[1].SessionCount)

		// Ends exactly at June 3 00:00 PDT, so that day is not a bucket.
		onMidnight, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: time.Date(2024, 6, 1, 7, 0, 0, 0, time.UTC),
			EndDate:   time.Date(2024, 6, 3, 7, 0, 0, 0, time.UTC),
			Timezone:  "America/Los_Angeles",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"2024-06-01", "2024-06-02"}, localSeriesDates(t, onMidnight.Points, "America/Los_Angeles"))
	})

	t.Run("returns one point per local day across DST", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		springAt := time.Date(2024, 3, 10, 18, 0, 0, 0, time.UTC) // 11:00 PDT, after the spring-forward
		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "spring-forward",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: springAt,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "model-a", 10, 10, 20, floatPtr(1), 0)

		spring, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: time.Date(2024, 3, 9, 8, 0, 0, 0, time.UTC),  // March 9 00:00 PST
			EndDate:   time.Date(2024, 3, 12, 7, 0, 0, 0, time.UTC), // March 12 00:00 PDT
			Timezone:  "America/Los_Angeles",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"2024-03-09", "2024-03-10", "2024-03-11"}, localSeriesDates(t, spring.Points, "America/Los_Angeles"))
		assert.Equal(t, int64(1), seriesPointOn(t, spring.Points, "America/Los_Angeles", 2024, time.March, 10).SessionCount)
		assert.Equal(t, int64(0), seriesPointOn(t, spring.Points, "America/Los_Angeles", 2024, time.March, 9).SessionCount)

		fallAt := time.Date(2024, 11, 3, 18, 0, 0, 0, time.UTC) // 10:00 PST, after the fall-back
		fid, fStage, fExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "fall-back",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: fallAt,
		})
		seedLLMInteraction(t, client.Client, fid, fStage, fExec, "model-a", 10, 10, 20, floatPtr(1), 0)

		fall, err := svc.GetUsageSeries(ctx, models.UsageSeriesParams{
			StartDate: time.Date(2024, 11, 2, 7, 0, 0, 0, time.UTC), // November 2 00:00 PDT
			EndDate:   time.Date(2024, 11, 5, 8, 0, 0, 0, time.UTC), // November 5 00:00 PST
			Timezone:  "America/Los_Angeles",
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"2024-11-02", "2024-11-03", "2024-11-04"}, localSeriesDates(t, fall.Points, "America/Los_Angeles"))
		assert.Equal(t, int64(1), seriesPointOn(t, fall.Points, "America/Los_Angeles", 2024, time.November, 3).SessionCount)
		assert.Equal(t, int64(0), seriesPointOn(t, fall.Points, "America/Los_Angeles", 2024, time.November, 4).SessionCount)
	})

	t.Run("orders other_models by that day's cost", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		dayOneID, dayOneStage, dayOneExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "other-day-one",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		for i, name := range []string{"a", "b", "c", "d", "e", "f"} {
			seedLLMInteraction(t, client.Client, dayOneID, dayOneStage, dayOneExec, name, 10, 10, 20, floatPtr(float64(10-i)), 0)
		}
		// Window rank is m-high (4) then m-low (3). Day two spends the other way.
		seedLLMInteraction(t, client.Client, dayOneID, dayOneStage, dayOneExec, "m-high", 10, 10, 20, floatPtr(3), 0)
		seedLLMInteraction(t, client.Client, dayOneID, dayOneStage, dayOneExec, "m-low", 10, 10, 20, floatPtr(0.5), 0)

		dayTwoID, dayTwoStage, dayTwoExec := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "other-day-two",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayTwo,
		})
		seedLLMInteraction(t, client.Client, dayTwoID, dayTwoStage, dayTwoExec, "m-high", 10, 10, 20, floatPtr(1), 0)
		seedLLMInteraction(t, client.Client, dayTwoID, dayTwoStage, dayTwoExec, "m-low", 10, 10, 20, floatPtr(2.5), 0)

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c", "d", "e", "f"}, series.Models)

		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		require.Len(t, june1.OtherModels, 2)
		assert.Equal(t, "m-high", june1.OtherModels[0].ModelName)
		assert.InDelta(t, 3, june1.OtherModels[0].EstimatedCostUsd, 1e-9)
		assert.Equal(t, "m-low", june1.OtherModels[1].ModelName)
		assert.InDelta(t, 0.5, june1.OtherModels[1].EstimatedCostUsd, 1e-9)

		june2 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 2)
		require.Len(t, june2.OtherModels, 2)
		assert.Equal(t, "m-low", june2.OtherModels[0].ModelName)
		assert.InDelta(t, 2.5, june2.OtherModels[0].EstimatedCostUsd, 1e-9)
		assert.Equal(t, "m-high", june2.OtherModels[1].ModelName)
		assert.InDelta(t, 1, june2.OtherModels[1].EstimatedCostUsd, 1e-9)
		assert.Empty(t, june2.ByModel)
	})

	t.Run("counts every interaction type", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		ctx := t.Context()

		sid, stageID, execID := seedUsageSession(t, client.Client, usageSeed{
			AlertData: "all-types",
			AlertType: "pod-crash",
			ChainID:   "k8s-analysis",
			CreatedAt: dayOne,
		})
		seedLLMInteraction(t, client.Client, sid, stageID, execID, "m", 10, 10, 20, floatPtr(0.1), 0)
		seedUsageLLMInteractionType(t, client.Client, sid, stageID, execID,
			llminteraction.InteractionTypeSummarization, "m", 30, 20, 50, floatPtr(0.2))
		seedUsageLLMInteractionType(t, client.Client, sid, stageID, execID,
			llminteraction.InteractionTypeScoring, "m", 5, 5, 10, floatPtr(0.05))

		series, err := svc.GetUsageSeries(ctx, params)
		require.NoError(t, err)
		june1 := seriesPointOn(t, series.Points, "UTC", 2024, time.June, 1)
		assert.InDelta(t, 0.35, june1.EstimatedCostUsd, 1e-9)
		assert.InDelta(t, 0.35, june1.ByModel["m"], 1e-9)

		summary, err := svc.GetUsageSummary(ctx, summaryParams)
		require.NoError(t, err)
		assertSeriesMatchesSummary(t, summary, series)
	})

	t.Run("estimation disabled returns no points", func(t *testing.T) {
		client := testdb.NewTestClient(t)
		svc := setupTestSessionService(t, client.Client)
		svc.SetCostEstimationEnabled(false)

		series, err := svc.GetUsageSeries(t.Context(), params)
		require.NoError(t, err)
		assert.False(t, series.CostEstimationEnabled)
		assert.Empty(t, series.Points)
		assert.Empty(t, series.Timezone)

		raw, err := json.Marshal(series)
		require.NoError(t, err)
		assert.JSONEq(t, `{"cost_estimation_enabled":false}`, string(raw))
	})
}

func seedLLMInteractionAt(
	t *testing.T,
	client *ent.Client,
	sessionID, stageID, execID, modelName string,
	inputTokens, outputTokens, totalTokens int,
	costUSD *float64,
	createdAt time.Time,
) {
	t.Helper()
	create := client.LLMInteraction.Create().
		SetID(uuid.New().String()).
		SetSessionID(sessionID).
		SetStageID(stageID).
		SetExecutionID(execID).
		SetInteractionType(llminteraction.InteractionTypeIteration).
		SetModelName(modelName).
		SetLlmRequest(map[string]any{}).
		SetLlmResponse(map[string]any{}).
		SetInputTokens(inputTokens).
		SetOutputTokens(outputTokens).
		SetTotalTokens(totalTokens).
		SetCreatedAt(createdAt)
	if costUSD != nil {
		create = create.SetEstimatedCostUsd(*costUSD)
	}
	create.SaveX(context.Background())
}

func assertSeriesMatchesSummary(t *testing.T, summary *models.UsageSummaryResponse, series *models.UsageSeriesResponse) {
	t.Helper()
	var costSum float64
	var sessions int64
	var weighted float64
	for _, point := range series.Points {
		costSum += point.EstimatedCostUsd
		sessions += point.SessionCount
		if point.AverageCostUsd != nil {
			weighted += *point.AverageCostUsd * float64(point.SessionCount)
		}
	}
	require.NotNil(t, summary.Totals.EstimatedCostUsd)
	assert.InDelta(t, *summary.Totals.EstimatedCostUsd, costSum, 1e-9)
	assert.Equal(t, summary.Totals.SessionCount, sessions)
	if sessions == 0 {
		assert.Nil(t, summary.Totals.AverageCostUsd)
		return
	}
	require.NotNil(t, summary.Totals.AverageCostUsd)
	assert.InDelta(t, *summary.Totals.AverageCostUsd, weighted/float64(sessions), 1e-9)
}

func localSeriesDates(t *testing.T, points []models.UsageSeriesPoint, zone string) []string {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	dates := make([]string, len(points))
	for i, point := range points {
		dates[i] = point.Start.In(loc).Format(time.DateOnly)
	}
	return dates
}

func seriesPointOn(t *testing.T, points []models.UsageSeriesPoint, zone string, year int, month time.Month, day int) models.UsageSeriesPoint {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	for _, point := range points {
		local := point.Start.In(loc)
		if local.Year() == year && local.Month() == month && local.Day() == day {
			return point
		}
	}
	t.Fatalf("no series point on %04d-%02d-%02d in %s", year, month, day, zone)
	return models.UsageSeriesPoint{}
}
