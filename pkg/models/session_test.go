package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveCostCompleteness(t *testing.T) {
	tests := []struct {
		name         string
		tokenBearing int
		priced       int
		want         CostCompleteness
	}{
		{"none when no priced", 3, 0, CostCompletenessNone},
		{"none when empty", 0, 0, CostCompletenessNone},
		{"complete when all priced", 2, 2, CostCompletenessComplete},
		{"partial when some priced", 3, 1, CostCompletenessPartial},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, DeriveCostCompleteness(tt.tokenBearing, tt.priced))
		})
	}
}

func TestSessionCostFieldsJSON(t *testing.T) {
	t.Run("zero estimated cost is serialized", func(t *testing.T) {
		zero := 0.0
		item := DashboardSessionItem{
			ID:               "s1",
			EstimatedCostUsd: &zero,
			CostCompleteness: CostCompletenessComplete,
		}
		raw, err := json.Marshal(item)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"estimated_cost_usd":0`)
		assert.Contains(t, string(raw), `"cost_completeness":"complete"`)
	})

	t.Run("nil cost fields omitted", func(t *testing.T) {
		item := DashboardSessionItem{ID: "s1"}
		raw, err := json.Marshal(item)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "estimated_cost_usd")
		assert.NotContains(t, string(raw), "cost_completeness")
	})
}

func TestParseTriageGroupKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TriageGroupKey
		wantErr bool
	}{
		{"investigating", "investigating", TriageGroupInvestigating, false},
		{"needs_review", "needs_review", TriageGroupNeedsReview, false},
		{"in_progress", "in_progress", TriageGroupInProgress, false},
		{"reviewed", "reviewed", TriageGroupReviewed, false},
		{"old resolved", "resolved", "", true},
		{"empty", "", "", true},
		{"unknown", "bogus", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTriageGroupKey(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestValidReviewAction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"claim", "claim", true},
		{"unclaim", "unclaim", true},
		{"complete", "complete", true},
		{"reopen", "reopen", true},
		{"update_feedback", "update_feedback", true},
		{"acknowledge", "acknowledge", true},
		{"empty", "", false},
		{"unknown", "bogus", false},
		{"old resolve", "resolve", false},
		{"old update_note", "update_note", false},
		{"case sensitive", "Claim", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidReviewAction(tt.input))
		})
	}
}

func TestValidUsageRankBy(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"cost", "cost", true},
		{"tokens", "tokens", true},
		{"empty", "", false},
		{"unknown", "sessions", false},
		{"case sensitive", "Cost", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidUsageRankBy(tt.input))
		})
	}
}

func TestUsageSummaryResponseJSON(t *testing.T) {
	t.Run("zero cost and priced false are serialized", func(t *testing.T) {
		zero := 0.0
		priced := false
		resp := UsageSummaryResponse{
			CostEstimationEnabled: true,
			RankBy:                UsageRankByCost,
			Totals: UsageTotals{
				EstimatedCostUsd: &zero,
				CostCompleteness: CostCompletenessNone,
			},
			ByModel: []UsageModelBreakdown{{
				ModelName:        "m",
				EstimatedCostUsd: &zero,
				Priced:           &priced,
			}},
		}
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"estimated_cost_usd":0`)
		assert.Contains(t, string(raw), `"cost_completeness":"none"`)
		assert.Contains(t, string(raw), `"priced":false`)
	})

	t.Run("nil cost fields omitted when estimation disabled shape", func(t *testing.T) {
		resp := UsageSummaryResponse{
			CostEstimationEnabled: false,
			RankBy:                UsageRankByTokens,
			Totals:                UsageTotals{TotalTokens: 10},
			ByModel:               []UsageModelBreakdown{{ModelName: "m", TotalTokens: 10}},
			TopSessions:           []UsageTopSession{{SessionID: "s1", TotalTokens: 10}},
		}
		raw, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "estimated_cost_usd")
		assert.NotContains(t, string(raw), "cost_completeness")
		assert.NotContains(t, string(raw), `"priced"`)
		assert.NotContains(t, string(raw), "unpriced_interaction_count")
	})
}
