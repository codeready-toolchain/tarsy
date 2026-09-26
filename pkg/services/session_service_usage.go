package services

import (
	"cmp"
	"context"
	stdsql "database/sql"
	"fmt"
	"maps"
	"slices"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/predicate"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

const (
	usageTopSessionsCap  = 20
	usageSeriesTopModels = 6
)

// GetUsageSummary returns fleet token/cost aggregates for sessions created in the
// given window (soft-deleted sessions excluded).
func (s *SessionService) GetUsageSummary(ctx context.Context, params models.UsageSummaryParams) (*models.UsageSummaryResponse, error) {
	rankBy := params.RankBy
	if rankBy == "" {
		if s.costEstimationEnabled {
			rankBy = models.UsageRankByCost
		} else {
			rankBy = models.UsageRankByTokens
		}
	}

	sessionPreds := usageSessionPreds(params)
	interactionPred := llminteraction.HasSessionWith(sessionPreds...)

	totals, err := s.usageTotals(ctx, interactionPred)
	if err != nil {
		return nil, err
	}
	sessionCount, err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count usage sessions: %w", err)
	}
	totals.SessionCount = int64(sessionCount)
	totals.AverageCostUsd = usageAverageCostUSD(totals.EstimatedCostUsd, totals.SessionCount)
	byModel, err := s.usageByModel(ctx, interactionPred)
	if err != nil {
		return nil, err
	}
	byAlert, err := s.usageByAlertType(ctx, sessionPreds)
	if err != nil {
		return nil, err
	}
	byChain, err := s.usageByChain(ctx, sessionPreds)
	if err != nil {
		return nil, err
	}
	top, err := s.usageTopSessions(ctx, sessionPreds, rankBy)
	if err != nil {
		return nil, err
	}

	resp := &models.UsageSummaryResponse{
		CostEstimationEnabled: s.costEstimationEnabled,
		Window: models.UsageWindow{
			Start: params.StartDate,
			End:   params.EndDate,
		},
		RankBy:      rankBy,
		Totals:      totals,
		ByModel:     byModel,
		ByAlertType: byAlert,
		ByChain:     byChain,
		TopSessions: top,
	}
	return resp, nil
}

func usageSessionPreds(params models.UsageSummaryParams) []predicate.AlertSession {
	preds := []predicate.AlertSession{
		alertsession.DeletedAtIsNil(),
		alertsession.CreatedAtGTE(params.StartDate),
		alertsession.CreatedAtLT(params.EndDate),
	}
	if params.AlertType != "" {
		preds = append(preds, alertsession.AlertTypeEQ(params.AlertType))
	}
	if params.ChainID != "" {
		preds = append(preds, alertsession.ChainIDEQ(params.ChainID))
	}
	return preds
}

func (s *SessionService) usageTotals(ctx context.Context, interactionPred predicate.LLMInteraction) (models.UsageTotals, error) {
	var results []struct {
		InputSum       stdsql.NullInt64   `json:"input_sum"`
		OutputSum      stdsql.NullInt64   `json:"output_sum"`
		CacheReadSum   stdsql.NullInt64   `json:"cache_read_sum"`
		CacheCreateSum stdsql.NullInt64   `json:"cache_create_sum"`
		TotalSum       stdsql.NullInt64   `json:"total_sum"`
		CostSum        stdsql.NullFloat64 `json:"cost_sum"`
		TokenBearing   int                `json:"token_bearing"`
		Priced         int                `json:"priced"`
		UnpricedTokens stdsql.NullInt64   `json:"unpriced_tokens"`
	}

	aggs := []ent.AggregateFunc{
		ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
		ent.As(ent.Sum(llminteraction.FieldCacheReadTokens), "cache_read_sum"),
		ent.As(ent.Sum(llminteraction.FieldCacheCreationTokens), "cache_create_sum"),
		ent.As(ent.Sum(llminteraction.FieldTotalTokens), "total_sum"),
		ent.As(func(_ *sql.Selector) string {
			return "COUNT(*) FILTER (WHERE " + tokenBearingPredicateSQL + ")"
		}, "token_bearing"),
	}
	if s.costEstimationEnabled {
		aggs = append(aggs,
			ent.As(ent.Sum(llminteraction.FieldEstimatedCostUsd), "cost_sum"),
			ent.As(func(_ *sql.Selector) string {
				return "COUNT(*) FILTER (WHERE " + tokenBearingPredicateSQL + " AND estimated_cost_usd IS NOT NULL)"
			}, "priced"),
			ent.As(func(_ *sql.Selector) string {
				return "COALESCE(SUM(COALESCE(" + llminteraction.FieldTotalTokens + ", 0) + COALESCE(" +
					llminteraction.FieldCacheReadTokens + ", 0) + COALESCE(" +
					llminteraction.FieldCacheCreationTokens + ", 0)) FILTER (WHERE " +
					tokenBearingPredicateSQL + " AND estimated_cost_usd IS NULL), 0)"
			}, "unpriced_tokens"),
		)
	}

	err := s.client.LLMInteraction.Query().
		Where(interactionPred).
		Aggregate(aggs...).
		Scan(ctx, &results)
	if err != nil {
		return models.UsageTotals{}, fmt.Errorf("failed to aggregate usage totals: %w", err)
	}

	totals := models.UsageTotals{}
	if len(results) == 0 {
		return totals, nil
	}
	r := results[0]
	totals.InputTokens = r.InputSum.Int64
	totals.OutputTokens = r.OutputSum.Int64
	totals.CacheReadTokens = r.CacheReadSum.Int64
	totals.CacheCreationTokens = r.CacheCreateSum.Int64
	totals.TotalTokens = r.TotalSum.Int64
	if s.costEstimationEnabled {
		cost := r.CostSum.Float64
		totals.EstimatedCostUsd = &cost
		totals.CostCompleteness = models.DeriveCostCompleteness(r.TokenBearing, r.Priced)
		unpriced := r.TokenBearing - r.Priced
		totals.UnpricedInteractionCount = &unpriced
		unpricedTokens := r.UnpricedTokens.Int64
		totals.UnpricedTokenCount = &unpricedTokens
	}
	return totals, nil
}

func (s *SessionService) usageByModel(ctx context.Context, interactionPred predicate.LLMInteraction) ([]models.UsageModelBreakdown, error) {
	var rows []struct {
		ModelName      string             `json:"model_name"`
		SessionCount   stdsql.NullInt64   `json:"session_count"`
		InputSum       stdsql.NullInt64   `json:"input_sum"`
		OutputSum      stdsql.NullInt64   `json:"output_sum"`
		CacheReadSum   stdsql.NullInt64   `json:"cache_read_sum"`
		CacheCreateSum stdsql.NullInt64   `json:"cache_create_sum"`
		TotalSum       stdsql.NullInt64   `json:"total_sum"`
		CostSum        stdsql.NullFloat64 `json:"cost_sum"`
		TokenBearing   int                `json:"token_bearing"`
		Priced         int                `json:"priced"`
	}

	aggs := []ent.AggregateFunc{
		ent.As(func(_ *sql.Selector) string {
			return "COUNT(DISTINCT " + llminteraction.FieldSessionID + ")"
		}, "session_count"),
		ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
		ent.As(ent.Sum(llminteraction.FieldCacheReadTokens), "cache_read_sum"),
		ent.As(ent.Sum(llminteraction.FieldCacheCreationTokens), "cache_create_sum"),
		ent.As(ent.Sum(llminteraction.FieldTotalTokens), "total_sum"),
		ent.As(func(_ *sql.Selector) string {
			return "COUNT(*) FILTER (WHERE " + tokenBearingPredicateSQL + ")"
		}, "token_bearing"),
	}
	if s.costEstimationEnabled {
		aggs = append(aggs,
			ent.As(ent.Sum(llminteraction.FieldEstimatedCostUsd), "cost_sum"),
			ent.As(func(_ *sql.Selector) string {
				return "COUNT(*) FILTER (WHERE " + tokenBearingPredicateSQL + " AND estimated_cost_usd IS NOT NULL)"
			}, "priced"),
		)
	}

	err := s.client.LLMInteraction.Query().
		Where(interactionPred).
		GroupBy(llminteraction.FieldModelName).
		Aggregate(aggs...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate usage by model: %w", err)
	}

	out := make([]models.UsageModelBreakdown, 0, len(rows))
	for _, row := range rows {
		item := models.UsageModelBreakdown{
			ModelName:           row.ModelName,
			SessionCount:        row.SessionCount.Int64,
			InputTokens:         row.InputSum.Int64,
			OutputTokens:        row.OutputSum.Int64,
			CacheReadTokens:     row.CacheReadSum.Int64,
			CacheCreationTokens: row.CacheCreateSum.Int64,
			TotalTokens:         row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
			item.AverageCostUsd = usageAverageCostUSD(item.EstimatedCostUsd, item.SessionCount)
			priced := row.TokenBearing > 0 && row.Priced == row.TokenBearing
			item.Priced = &priced
			unpriced := row.TokenBearing - row.Priced
			item.UnpricedInteractionCount = &unpriced
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SessionService) usageByAlertType(ctx context.Context, sessionPreds []predicate.AlertSession) ([]models.UsageAlertBreakdown, error) {
	var rows []struct {
		AlertType    stdsql.NullString  `json:"alert_type"`
		SessionCount stdsql.NullInt64   `json:"session_count"`
		TotalSum     stdsql.NullInt64   `json:"total_sum"`
		CostSum      stdsql.NullFloat64 `json:"cost_sum"`
	}

	err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Modify(func(sel *sql.Selector) {
			li := sql.Table(llminteraction.Table).As("li")
			sel.LeftJoin(li).On(sel.C(alertsession.FieldID), li.C(llminteraction.FieldSessionID))
			sel.Select(sql.As(sel.C(alertsession.FieldAlertType), "alert_type"))
			sel.AppendSelectAs(
				fmt.Sprintf("COUNT(DISTINCT %s)", sel.C(alertsession.FieldID)),
				"session_count",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("COALESCE(SUM(%s), 0)", li.C(llminteraction.FieldTotalTokens)),
				"total_sum",
			)
			if s.costEstimationEnabled {
				sel.AppendSelectAs(
					fmt.Sprintf("COALESCE(SUM(%s), 0)", li.C(llminteraction.FieldEstimatedCostUsd)),
					"cost_sum",
				)
			}
			sel.GroupBy(sel.C(alertsession.FieldAlertType))
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate usage by alert type: %w", err)
	}

	out := make([]models.UsageAlertBreakdown, 0, len(rows))
	for _, row := range rows {
		item := models.UsageAlertBreakdown{
			AlertType:    row.AlertType.String,
			SessionCount: row.SessionCount.Int64,
			TotalTokens:  row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
			item.AverageCostUsd = usageAverageCostUSD(item.EstimatedCostUsd, item.SessionCount)
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SessionService) usageByChain(ctx context.Context, sessionPreds []predicate.AlertSession) ([]models.UsageChainBreakdown, error) {
	var rows []struct {
		ChainID      string             `json:"chain_id"`
		SessionCount stdsql.NullInt64   `json:"session_count"`
		TotalSum     stdsql.NullInt64   `json:"total_sum"`
		CostSum      stdsql.NullFloat64 `json:"cost_sum"`
	}

	err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Modify(func(sel *sql.Selector) {
			li := sql.Table(llminteraction.Table).As("li")
			sel.LeftJoin(li).On(sel.C(alertsession.FieldID), li.C(llminteraction.FieldSessionID))
			sel.Select(sql.As(sel.C(alertsession.FieldChainID), "chain_id"))
			sel.AppendSelectAs(
				fmt.Sprintf("COUNT(DISTINCT %s)", sel.C(alertsession.FieldID)),
				"session_count",
			)
			sel.AppendSelectAs(
				fmt.Sprintf("COALESCE(SUM(%s), 0)", li.C(llminteraction.FieldTotalTokens)),
				"total_sum",
			)
			if s.costEstimationEnabled {
				sel.AppendSelectAs(
					fmt.Sprintf("COALESCE(SUM(%s), 0)", li.C(llminteraction.FieldEstimatedCostUsd)),
					"cost_sum",
				)
			}
			sel.GroupBy(sel.C(alertsession.FieldChainID))
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate usage by chain: %w", err)
	}

	out := make([]models.UsageChainBreakdown, 0, len(rows))
	for _, row := range rows {
		item := models.UsageChainBreakdown{
			ChainID:      row.ChainID,
			SessionCount: row.SessionCount.Int64,
			TotalTokens:  row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
			item.AverageCostUsd = usageAverageCostUSD(item.EstimatedCostUsd, item.SessionCount)
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SessionService) usageTopSessions(
	ctx context.Context,
	sessionPreds []predicate.AlertSession,
	rankBy models.UsageRankBy,
) ([]models.UsageTopSession, error) {
	var rows []struct {
		ID           string             `json:"session_id"`
		AlertType    *string            `json:"alert_type"`
		ChainID      string             `json:"chain_id"`
		CreatedAt    time.Time          `json:"created_at"`
		TotalSum     stdsql.NullInt64   `json:"total_sum"`
		CostSum      stdsql.NullFloat64 `json:"cost_sum"`
		TokenBearing int                `json:"token_bearing"`
		Priced       int                `json:"priced"`
	}

	err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Limit(usageTopSessionsCap).
		Modify(func(sel *sql.Selector) {
			t := sel.TableName()
			agg := sql.Table("agg")

			// CTE aggregates llm_interactions once per session; SELECT and ORDER BY
			// both read from those columns (no repeated correlated SUM/COUNT).
			subq := sql.Select(llminteraction.FieldSessionID).
				From(sql.Table(llminteraction.Table)).
				GroupBy(llminteraction.FieldSessionID)
			subq.AppendSelectExprAs(sql.Expr("COALESCE(SUM(total_tokens), 0)"), "total_sum")
			if s.costEstimationEnabled {
				subq.AppendSelectExprAs(sql.Expr("COALESCE(SUM(estimated_cost_usd), 0)"), "cost_sum")
				subq.AppendSelectExprAs(
					sql.Expr(fmt.Sprintf("COUNT(*) FILTER (WHERE %s)", tokenBearingPredicateSQL)),
					"token_bearing",
				)
				subq.AppendSelectExprAs(
					sql.Expr(fmt.Sprintf(
						"COUNT(*) FILTER (WHERE %s AND estimated_cost_usd IS NOT NULL)",
						tokenBearingPredicateSQL,
					)),
					"priced",
				)
			}
			sel.Prefix(sql.With("agg").As(subq))
			sel.LeftJoin(agg).On(sel.C(alertsession.FieldID), agg.C(llminteraction.FieldSessionID))

			sel.Select(
				sql.As(sel.C(alertsession.FieldID), "session_id"),
				sel.C(alertsession.FieldAlertType),
				sel.C(alertsession.FieldChainID),
				sel.C(alertsession.FieldCreatedAt),
			)
			sel.AppendSelectExprAs(sql.Expr(fmt.Sprintf("COALESCE(%s, 0)", agg.C("total_sum"))), "total_sum")
			if s.costEstimationEnabled {
				sel.AppendSelectExprAs(sql.Expr(fmt.Sprintf("COALESCE(%s, 0)", agg.C("cost_sum"))), "cost_sum")
				sel.AppendSelectExprAs(sql.Expr(fmt.Sprintf("COALESCE(%s, 0)", agg.C("token_bearing"))), "token_bearing")
				sel.AppendSelectExprAs(sql.Expr(fmt.Sprintf("COALESCE(%s, 0)", agg.C("priced"))), "priced")
			}

			switch rankBy {
			case models.UsageRankByCost:
				sel.OrderExpr(sql.Expr(fmt.Sprintf("COALESCE(%s, 0) DESC", agg.C("cost_sum"))))
			default:
				sel.OrderExpr(sql.Expr(fmt.Sprintf("COALESCE(%s, 0) DESC", agg.C("total_sum"))))
			}
			sel.OrderExpr(sql.Expr(fmt.Sprintf("%q.%q DESC", t, alertsession.FieldCreatedAt)))
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to query top usage sessions: %w", err)
	}

	out := make([]models.UsageTopSession, 0, len(rows))
	for _, row := range rows {
		alertType := row.AlertType
		if alertType != nil && *alertType == "" {
			alertType = nil
		}
		item := models.UsageTopSession{
			SessionID:   row.ID,
			AlertType:   alertType,
			ChainID:     row.ChainID,
			TotalTokens: row.TotalSum.Int64,
			CreatedAt:   row.CreatedAt,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
			item.CostCompleteness = models.DeriveCostCompleteness(row.TokenBearing, row.Priced)
		}
		out = append(out, item)
	}
	return out, nil
}

func usageAverageCostUSD(cost *float64, sessionCount int64) *float64 {
	if cost == nil || sessionCount <= 0 {
		return nil
	}
	return new(*cost / float64(sessionCount))
}

// resolveUsageTimezone returns a PostgreSQL-usable IANA name.
// Missing, unknown, and Go's Local zone fall back to UTC.
func resolveUsageTimezone(name string) string {
	if name == "" || name == "Local" {
		return "UTC"
	}
	loc, err := time.LoadLocation(name)
	if err != nil || loc.String() == "Local" {
		return "UTC"
	}
	return loc.String()
}

// GetUsageSeries returns per-day estimated cost and session counts for the same
// population as GetUsageSummary. Days are calendar days in the resolved timezone.
func (s *SessionService) GetUsageSeries(ctx context.Context, params models.UsageSeriesParams) (*models.UsageSeriesResponse, error) {
	if !s.costEstimationEnabled {
		return &models.UsageSeriesResponse{CostEstimationEnabled: false}, nil
	}

	zone := resolveUsageTimezone(params.Timezone)
	params.Timezone = zone
	sessionPreds := usageSessionPreds(models.UsageSummaryParams{
		StartDate: params.StartDate,
		EndDate:   params.EndDate,
		AlertType: params.AlertType,
		ChainID:   params.ChainID,
	})

	rows, err := s.queryUsageSeriesRows(ctx, params, sessionPreds)
	if err != nil {
		return nil, err
	}
	totals, err := s.usageTotals(ctx, llminteraction.HasSessionWith(sessionPreds...))
	if err != nil {
		return nil, err
	}

	points, modelNames := assembleUsageSeries(rows)
	return &models.UsageSeriesResponse{
		CostEstimationEnabled:    true,
		Window:                   models.UsageWindow{Start: params.StartDate, End: params.EndDate},
		Timezone:                 zone,
		Bucket:                   models.UsageBucketDay,
		CostCompleteness:         totals.CostCompleteness,
		UnpricedInteractionCount: totals.UnpricedInteractionCount,
		UnpricedTokenCount:       totals.UnpricedTokenCount,
		Models:                   modelNames,
		Points:                   points,
	}, nil
}

type usageSeriesScanRow struct {
	BucketStart  time.Time          `json:"bucket_start"`
	BucketEnd    time.Time          `json:"bucket_end"`
	SessionCount int64              `json:"session_count"`
	ModelName    stdsql.NullString  `json:"model_name"`
	Cost         stdsql.NullFloat64 `json:"cost"`
}

type usageSeriesDay struct {
	start    time.Time
	end      time.Time
	sessions int64
	costs    map[string]float64
}

func (s *SessionService) queryUsageSeriesRows(
	ctx context.Context,
	params models.UsageSeriesParams,
	preds []predicate.AlertSession,
) ([]usageSeriesScanRow, error) {
	var rows []usageSeriesScanRow
	err := s.client.AlertSession.Query().Modify(func(sel *sql.Selector) {
		d := sql.Dialect(sel.Dialect())
		sessions := usageSeriesSessions(d, params.Timezone, preds)
		days := usageSeriesDays(d, params)
		counts := usageSeriesCounts(d)
		costs := usageSeriesModelCosts(d)

		daysT := d.Table("days").As("d")
		countsT := d.Table("session_counts").As("sc")
		costsT := d.Table("model_costs").As("mc")

		sel.SetP(nil)
		sel.From(daysT)
		sel.LeftJoin(countsT).On(daysT.C("local_day"), countsT.C("local_day"))
		sel.LeftJoin(costsT).On(daysT.C("local_day"), costsT.C("local_day"))
		// Clear the entity column list, then select the series shape.
		sel.Select()
		sel.AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
			b.WriteString("GREATEST(" + daysT.C("local_day") + " AT TIME ZONE ")
			b.Arg(params.Timezone)
			b.WriteString("::text, ")
			b.Arg(params.StartDate)
			b.WriteString("::timestamptz)")
		}), "bucket_start")
		sel.AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
			b.WriteString("LEAST((" + daysT.C("local_day") + " + interval '1 day') AT TIME ZONE ")
			b.Arg(params.Timezone)
			b.WriteString("::text, ")
			b.Arg(params.EndDate)
			b.WriteString("::timestamptz)")
		}), "bucket_end")
		sel.AppendSelectExprAs(
			sql.Expr("COALESCE("+countsT.C("session_count")+", 0)"),
			"session_count",
		)
		sel.AppendSelectAs(costsT.C("model_name"), "model_name")
		sel.AppendSelectAs(costsT.C("cost"), "cost")
		sel.ClearOrder()
		sel.OrderExpr(
			sql.Expr(daysT.C("local_day")),
			sql.Expr(costsT.C("cost")+" DESC NULLS LAST"),
			sql.Expr(costsT.C("model_name")+" ASC NULLS LAST"),
		)
		sel.Prefix(d.With("sessions").As(sessions).
			With("days").As(days).
			With("session_counts").As(counts).
			With("model_costs").As(costs))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to query usage series: %w", err)
	}
	return rows, nil
}

func usageSeriesSessions(d *sql.DialectBuilder, zone string, preds []predicate.AlertSession) *sql.Selector {
	sessions := d.Select().From(d.Table(alertsession.Table))
	for _, p := range preds {
		p(sessions)
	}
	createdAt := sessions.C(alertsession.FieldCreatedAt)
	sessions.AppendSelectAs(sessions.C(alertsession.FieldID), "session_id")
	sessions.AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("date_trunc('day', " + createdAt + " AT TIME ZONE ")
		b.Arg(zone)
		b.WriteString("::text)")
	}), "local_day")
	return sessions
}

func usageSeriesDays(d *sql.DialectBuilder, params models.UsageSeriesParams) *sql.Selector {
	days := d.Select()
	// Step timestamp (not timestamptz) by one day so a DST day does not drift.
	// Subtract one microsecond so a window that ends on local midnight excludes that day.
	days.AppendSelectExprAs(sql.ExprFunc(func(b *sql.Builder) {
		b.WriteString("generate_series(date_trunc('day', ")
		b.Arg(params.StartDate)
		b.WriteString("::timestamptz AT TIME ZONE ")
		b.Arg(params.Timezone)
		b.WriteString("::text), date_trunc('day', (")
		b.Arg(params.EndDate)
		b.WriteString("::timestamptz - interval '1 microsecond') AT TIME ZONE ")
		b.Arg(params.Timezone)
		b.WriteString("::text), interval '1 day')")
	}), "local_day")
	return days
}

func usageSeriesCounts(d *sql.DialectBuilder) *sql.Selector {
	counts := d.Select().From(d.Table("sessions"))
	counts.AppendSelectAs("local_day", "local_day")
	counts.AppendSelectExprAs(sql.Expr("COUNT(*)"), "session_count")
	counts.GroupBy("local_day")
	return counts
}

func usageSeriesModelCosts(d *sql.DialectBuilder) *sql.Selector {
	sessionsT := d.Table("sessions").As("s")
	li := d.Table(llminteraction.Table).As("li")
	costExpr := "SUM(" + li.C(llminteraction.FieldEstimatedCostUsd) + ")"
	costs := d.Select().From(sessionsT)
	costs.Join(li).On(sessionsT.C("session_id"), li.C(llminteraction.FieldSessionID))
	costs.AppendSelectAs(sessionsT.C("local_day"), "local_day")
	costs.AppendSelectAs(li.C(llminteraction.FieldModelName), "model_name")
	costs.AppendSelectExprAs(sql.Expr(costExpr), "cost")
	costs.GroupBy(sessionsT.C("local_day"), li.C(llminteraction.FieldModelName))
	costs.Having(sql.ExprP(costExpr + " > 0"))
	return costs
}

func assembleUsageSeries(rows []usageSeriesScanRow) ([]models.UsageSeriesPoint, []string) {
	var days []usageSeriesDay
	for _, row := range rows {
		if len(days) == 0 || !days[len(days)-1].start.Equal(row.BucketStart) {
			days = append(days, usageSeriesDay{
				start:    row.BucketStart,
				end:      row.BucketEnd,
				sessions: row.SessionCount,
				costs:    map[string]float64{},
			})
		}
		day := &days[len(days)-1]
		if row.ModelName.Valid && row.Cost.Valid && row.Cost.Float64 > 0 {
			day.costs[row.ModelName.String] = row.Cost.Float64
		}
	}

	windowCost := map[string]float64{}
	for _, day := range days {
		for name, cost := range day.costs {
			windowCost[name] += cost
		}
	}
	ranked := slices.Collect(maps.Keys(windowCost))
	slices.SortFunc(ranked, func(a, b string) int {
		if c := cmp.Compare(windowCost[b], windowCost[a]); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	named := ranked
	var collapsed []string
	if len(ranked) > usageSeriesTopModels {
		named = slices.Clone(ranked[:usageSeriesTopModels])
		collapsed = ranked[usageSeriesTopModels:]
	}
	collapsedSet := map[string]struct{}{}
	for _, name := range collapsed {
		collapsedSet[name] = struct{}{}
	}

	points := make([]models.UsageSeriesPoint, 0, len(days))
	for _, day := range days {
		byModel := map[string]float64{}
		var namedSum float64
		for _, name := range named {
			cost, ok := day.costs[name]
			if !ok || cost <= 0 {
				continue
			}
			byModel[name] = cost
			namedSum += cost
		}
		other := make([]models.UsageSeriesOtherModel, 0, len(collapsed))
		var otherSum float64
		for _, name := range collapsed {
			cost, ok := day.costs[name]
			if !ok || cost <= 0 {
				continue
			}
			other = append(other, models.UsageSeriesOtherModel{
				ModelName:        name,
				EstimatedCostUsd: cost,
			})
			otherSum += cost
		}
		slices.SortFunc(other, func(a, b models.UsageSeriesOtherModel) int {
			if c := cmp.Compare(b.EstimatedCostUsd, a.EstimatedCostUsd); c != 0 {
				return c
			}
			return cmp.Compare(a.ModelName, b.ModelName)
		})
		total := namedSum + otherSum
		point := models.UsageSeriesPoint{
			Start:            day.start,
			End:              day.end,
			SessionCount:     day.sessions,
			EstimatedCostUsd: total,
			AverageCostUsd:   usageAverageCostUSD(&total, day.sessions),
		}
		if len(byModel) > 0 {
			point.ByModel = byModel
		}
		if len(other) > 0 {
			point.OtherModels = other
		}
		points = append(points, point)
	}
	if len(named) == 0 {
		named = nil
	}
	return points, named
}
