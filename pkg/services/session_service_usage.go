package services

import (
	"context"
	stdsql "database/sql"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/ent/llminteraction"
	"github.com/codeready-toolchain/tarsy/ent/predicate"
	"github.com/codeready-toolchain/tarsy/pkg/models"
)

const usageTopSessionsCap = 20

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
		InputSum     stdsql.NullInt64   `json:"input_sum"`
		OutputSum    stdsql.NullInt64   `json:"output_sum"`
		TotalSum     stdsql.NullInt64   `json:"total_sum"`
		CostSum      stdsql.NullFloat64 `json:"cost_sum"`
		TokenBearing int                `json:"token_bearing"`
		Priced       int                `json:"priced"`
	}

	aggs := []ent.AggregateFunc{
		ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
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
	totals.TotalTokens = r.TotalSum.Int64
	if s.costEstimationEnabled {
		cost := r.CostSum.Float64
		totals.EstimatedCostUsd = &cost
		totals.CostCompleteness = models.DeriveCostCompleteness(r.TokenBearing, r.Priced)
		unpriced := r.TokenBearing - r.Priced
		totals.UnpricedInteractionCount = &unpriced
	}
	return totals, nil
}

func (s *SessionService) usageByModel(ctx context.Context, interactionPred predicate.LLMInteraction) ([]models.UsageModelBreakdown, error) {
	var rows []struct {
		ModelName    string             `json:"model_name"`
		InputSum     stdsql.NullInt64   `json:"input_sum"`
		OutputSum    stdsql.NullInt64   `json:"output_sum"`
		TotalSum     stdsql.NullInt64   `json:"total_sum"`
		CostSum      stdsql.NullFloat64 `json:"cost_sum"`
		TokenBearing int                `json:"token_bearing"`
		Priced       int                `json:"priced"`
	}

	aggs := []ent.AggregateFunc{
		ent.As(ent.Sum(llminteraction.FieldInputTokens), "input_sum"),
		ent.As(ent.Sum(llminteraction.FieldOutputTokens), "output_sum"),
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
			ModelName:    row.ModelName,
			InputTokens:  row.InputSum.Int64,
			OutputTokens: row.OutputSum.Int64,
			TotalTokens:  row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
			priced := row.TokenBearing > 0 && row.Priced == row.TokenBearing
			item.Priced = &priced
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SessionService) usageByAlertType(ctx context.Context, sessionPreds []predicate.AlertSession) ([]models.UsageAlertBreakdown, error) {
	var rows []struct {
		AlertType stdsql.NullString  `json:"alert_type"`
		TotalSum  stdsql.NullInt64   `json:"total_sum"`
		CostSum   stdsql.NullFloat64 `json:"cost_sum"`
	}

	err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Modify(func(sel *sql.Selector) {
			li := sql.Table(llminteraction.Table).As("li")
			sel.LeftJoin(li).On(sel.C(alertsession.FieldID), li.C(llminteraction.FieldSessionID))
			sel.Select(sql.As(sel.C(alertsession.FieldAlertType), "alert_type"))
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
			AlertType:   row.AlertType.String,
			TotalTokens: row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *SessionService) usageByChain(ctx context.Context, sessionPreds []predicate.AlertSession) ([]models.UsageChainBreakdown, error) {
	var rows []struct {
		ChainID  string             `json:"chain_id"`
		TotalSum stdsql.NullInt64   `json:"total_sum"`
		CostSum  stdsql.NullFloat64 `json:"cost_sum"`
	}

	err := s.client.AlertSession.Query().
		Where(sessionPreds...).
		Modify(func(sel *sql.Selector) {
			li := sql.Table(llminteraction.Table).As("li")
			sel.LeftJoin(li).On(sel.C(alertsession.FieldID), li.C(llminteraction.FieldSessionID))
			sel.Select(sql.As(sel.C(alertsession.FieldChainID), "chain_id"))
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
			ChainID:     row.ChainID,
			TotalTokens: row.TotalSum.Int64,
		}
		if s.costEstimationEnabled {
			cost := row.CostSum.Float64
			item.EstimatedCostUsd = &cost
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
			sid := fmt.Sprintf("%q.%q", t, alertsession.FieldID)

			sel.Select(
				sql.As(sel.C(alertsession.FieldID), "session_id"),
				sel.C(alertsession.FieldAlertType),
				sel.C(alertsession.FieldChainID),
				sel.C(alertsession.FieldCreatedAt),
			)
			sel.AppendSelectAs(
				fmt.Sprintf("(SELECT COALESCE(SUM(total_tokens), 0) FROM llm_interactions WHERE session_id = %s)", sid),
				"total_sum",
			)
			if s.costEstimationEnabled {
				sel.AppendSelectAs(
					fmt.Sprintf("(SELECT COALESCE(SUM(estimated_cost_usd), 0) FROM llm_interactions WHERE session_id = %s)", sid),
					"cost_sum",
				)
				sel.AppendSelectAs(
					fmt.Sprintf(
						"(SELECT COUNT(*) FROM llm_interactions WHERE session_id = %s AND %s)",
						sid, tokenBearingPredicateSQL,
					),
					"token_bearing",
				)
				sel.AppendSelectAs(
					fmt.Sprintf(
						"(SELECT COUNT(*) FROM llm_interactions WHERE session_id = %s AND %s AND estimated_cost_usd IS NOT NULL)",
						sid, tokenBearingPredicateSQL,
					),
					"priced",
				)
			}

			switch rankBy {
			case models.UsageRankByCost:
				sel.OrderExpr(sql.Expr(fmt.Sprintf(
					"(SELECT COALESCE(SUM(estimated_cost_usd), 0) FROM llm_interactions WHERE session_id = %s) DESC",
					sid,
				)))
			default:
				sel.OrderExpr(sql.Expr(fmt.Sprintf(
					"(SELECT COALESCE(SUM(total_tokens), 0) FROM llm_interactions WHERE session_id = %s) DESC",
					sid,
				)))
			}
			sel.OrderExpr(sql.Expr(fmt.Sprintf("%q.%q DESC", t, alertsession.FieldCreatedAt)))
		}).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("failed to query top usage sessions: %w", err)
	}

	out := make([]models.UsageTopSession, 0, len(rows))
	for _, row := range rows {
		item := models.UsageTopSession{
			SessionID:   row.ID,
			AlertType:   row.AlertType,
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
