package cost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCatalogJSON(t *testing.T) {
	raw := `{
		"flat-model": {
			"input_cost_per_token": 1e-6,
			"output_cost_per_token": 2e-6,
			"output_cost_per_reasoning_token": 3e-6,
			"input_cost_per_token_above_200k_tokens": 4e-6,
			"output_cost_per_token_above_200k_tokens": 5e-6,
			"cache_read_input_token_cost": 1e-7,
			"cache_creation_input_token_cost": 1.25e-6,
			"cache_creation_input_token_cost_above_1hr": 2e-6
		},
		"tiered-model": {
			"tiered_pricing": [
				{"input_cost_per_token": 1e-8, "output_cost_per_token": 2e-8, "range": [0, 1000]},
				{"input_cost_per_token": 9e-8, "output_cost_per_token": 8e-8, "range": [1000, 10000]}
			]
		},
		"input-only": {
			"input_cost_per_token": 1e-6
		},
		"explicit-zero-output": {
			"input_cost_per_token": 1e-6,
			"output_cost_per_token": 0
		},
		"promo-until": {
			"input_cost_per_token": 1e-6,
			"output_cost_per_token": 2e-6,
			"tarsy_rates_valid_until": "2026-11-22"
		},
		"bad-expiry": {
			"input_cost_per_token": 1e-6,
			"output_cost_per_token": 2e-6,
			"tarsy_rates_valid_until": "not-a-date"
		},
		"sample_spec": {"max_tokens": 100},
		"no-prices": {"litellm_provider": "x"}
	}`

	entries, err := parseCatalogJSON([]byte(raw))
	require.NoError(t, err)
	require.Contains(t, entries, "flat-model")
	require.Contains(t, entries, "tiered-model")
	require.Contains(t, entries, "input-only")
	require.Contains(t, entries, "explicit-zero-output")
	require.Contains(t, entries, "promo-until")
	assert.NotContains(t, entries, "no-prices")
	assert.NotContains(t, entries, "bad-expiry")

	flat := entries["flat-model"]
	assert.True(t, flat.HasInput)
	assert.True(t, flat.HasOutput)
	assert.Equal(t, 1e-6, flat.InputCostPerToken)
	assert.Equal(t, 2e-6, flat.OutputCostPerToken)
	require.NotNil(t, flat.OutputCostPerReasoningToken)
	assert.Equal(t, 3e-6, *flat.OutputCostPerReasoningToken)
	assert.Equal(t, 4e-6, flat.InputCostAbove[200_000])
	assert.Equal(t, 5e-6, flat.OutputCostAbove[200_000])
	assert.True(t, flat.HasCacheRead)
	assert.Equal(t, 1e-7, flat.CacheReadCost)
	assert.True(t, flat.HasCacheCreate)
	assert.Equal(t, 1.25e-6, flat.CacheCreateCost)
	assert.True(t, flat.HasCacheCreateAbove1hr)
	assert.Equal(t, 2e-6, flat.CacheCreateAbove1hr)

	tiered := entries["tiered-model"]
	require.Len(t, tiered.TieredPricing, 2)
	assert.False(t, tiered.HasInput)
	assert.False(t, tiered.HasOutput)

	inputOnly := entries["input-only"]
	assert.True(t, inputOnly.HasInput)
	assert.False(t, inputOnly.HasOutput)

	zeroOut := entries["explicit-zero-output"]
	assert.True(t, zeroOut.HasOutput)
	assert.Equal(t, 0.0, zeroOut.OutputCostPerToken)

	promo := entries["promo-until"]
	require.NotNil(t, promo.RatesValidUntil)
	assert.True(t, promo.RatesValidUntil.Equal(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)))
	assert.True(t, promo.ratesValidAt(time.Date(2026, 11, 21, 23, 59, 59, 0, time.UTC)))
	assert.False(t, promo.ratesValidAt(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)))
	assert.True(t, flat.ratesValidAt(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)))
}

func TestRatesForInput(t *testing.T) {
	reasoning := 7e-6
	entry := catalogEntry{
		HasInput:                    true,
		HasOutput:                   true,
		InputCostPerToken:           1e-6,
		OutputCostPerToken:          2e-6,
		OutputCostPerReasoningToken: &reasoning,
		InputCostAbove:              map[int]float64{200_000: 4e-6, 100_000: 3e-6},
		OutputCostAbove:             map[int]float64{200_000: 5e-6, 100_000: 3.5e-6},
		TieredPricing: []tierRange{
			{InputCostPerToken: 9e-9, OutputCostPerToken: 8e-9, RangeStart: 0, RangeEnd: 50},
		},
	}

	t.Run("flat below thresholds", func(t *testing.T) {
		r, ok := entry.ratesForInput(50_000)
		require.True(t, ok)
		assert.Equal(t, 1e-6, r.Input)
		assert.Equal(t, 2e-6, r.Output)
		require.NotNil(t, r.Reasoning)
		assert.Equal(t, 7e-6, *r.Reasoning)
	})

	t.Run("highest matching above_Nk wins", func(t *testing.T) {
		r, ok := entry.ratesForInput(250_000)
		require.True(t, ok)
		assert.Equal(t, 4e-6, r.Input)
		assert.Equal(t, 5e-6, r.Output)
	})

	t.Run("above_Nk preferred over tiered_pricing", func(t *testing.T) {
		r, ok := entry.ratesForInput(150_000)
		require.True(t, ok)
		assert.Equal(t, 3e-6, r.Input)
		assert.Equal(t, 3.5e-6, r.Output)
	})

	t.Run("tiered when no above threshold applies", func(t *testing.T) {
		tierOnly := catalogEntry{
			HasInput:           true,
			HasOutput:          true,
			InputCostPerToken:  1e-6,
			OutputCostPerToken: 2e-6,
			TieredPricing: []tierRange{
				{InputCostPerToken: 1e-8, OutputCostPerToken: 2e-8, RangeStart: 0, RangeEnd: 100},
				{InputCostPerToken: 9e-8, OutputCostPerToken: 8e-8, RangeStart: 100, RangeEnd: 1000},
			},
		}
		low, ok := tierOnly.ratesForInput(50)
		require.True(t, ok)
		high, ok := tierOnly.ratesForInput(100)
		require.True(t, ok)
		assert.Equal(t, 1e-8, low.Input)
		assert.Equal(t, 9e-8, high.Input)
		assert.Equal(t, 8e-8, high.Output)
	})

	t.Run("tiered-only entry prices when tier matches", func(t *testing.T) {
		tierOnly := catalogEntry{
			TieredPricing: []tierRange{
				{InputCostPerToken: 1e-8, OutputCostPerToken: 2e-8, RangeStart: 0, RangeEnd: 100},
			},
		}
		r, ok := tierOnly.ratesForInput(50)
		require.True(t, ok)
		assert.Equal(t, 1e-8, r.Input)
		assert.Nil(t, r.Reasoning)
	})

	t.Run("missing output side is unpriced", func(t *testing.T) {
		partial := catalogEntry{HasInput: true, InputCostPerToken: 1e-6}
		_, ok := partial.ratesForInput(10)
		assert.False(t, ok)
	})

	t.Run("explicit zero output remains priced", func(t *testing.T) {
		zeroOut := catalogEntry{
			HasInput: true, HasOutput: true,
			InputCostPerToken: 1e-6, OutputCostPerToken: 0,
		}
		r, ok := zeroOut.ratesForInput(10)
		require.True(t, ok)
		assert.Equal(t, 0.0, r.Output)
	})

	t.Run("reasoning unset when catalog omits it", func(t *testing.T) {
		noReasoning := catalogEntry{
			HasInput: true, HasOutput: true,
			InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6,
		}
		r, ok := noReasoning.ratesForInput(10)
		require.True(t, ok)
		assert.Nil(t, r.Reasoning)
	})
}

func TestFetchCatalog_MaxBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	t.Cleanup(srv.Close)

	_, err := fetchCatalog(t.Context(), srv.Client(), srv.URL, 50)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds max size")
}

func TestFetchCatalog_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"m":{"input_cost_per_token":1e-6,"output_cost_per_token":2e-6}}`))
	}))
	t.Cleanup(srv.Close)

	entries, err := fetchCatalog(t.Context(), srv.Client(), srv.URL, 1<<20)
	require.NoError(t, err)
	require.Contains(t, entries, "m")
	assert.True(t, entries["m"].HasInput)
	assert.True(t, entries["m"].HasOutput)
}

func TestLoadSnapshot_ShippedKeysAndCacheRates(t *testing.T) {
	entries, err := loadSnapshot()
	require.NoError(t, err)

	require.Contains(t, entries, "gpt-5")
	require.Contains(t, entries, "gpt-5.2")
	require.Contains(t, entries, "gpt-5.6")
	require.Contains(t, entries, "gpt-5.6-sol")
	require.Contains(t, entries, "gpt-5.6-terra")
	require.Contains(t, entries, "gpt-5.6-luna")
	gpt5 := entries["gpt-5"]
	assert.True(t, gpt5.HasCacheRead)
	assert.InDelta(t, 1.25e-07, gpt5.CacheReadCost, 1e-15)
	assert.False(t, gpt5.HasCacheCreate)
	assert.False(t, gpt5.HasCacheCreateAbove1hr)

	gpt56 := entries["gpt-5.6"]
	assert.True(t, gpt56.HasCacheRead)
	assert.True(t, gpt56.HasCacheCreate)
	assert.False(t, gpt56.HasCacheCreateAbove1hr)
	assert.InDelta(t, 4e-07, gpt56.CacheReadCost, 1e-15)
	assert.InDelta(t, 5e-06, gpt56.CacheCreateCost, 1e-15)
	assert.InDelta(t, 8e-06, gpt56.InputCostAbove[272_000], 1e-15)
	assert.InDelta(t, 3e-05, gpt56.OutputCostAbove[272_000], 1e-15)
	wantUntil := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	require.NotNil(t, gpt56.RatesValidUntil)
	assert.True(t, gpt56.RatesValidUntil.Equal(wantUntil))
	sol := entries["gpt-5.6-sol"]
	require.NotNil(t, sol.RatesValidUntil)
	assert.True(t, sol.RatesValidUntil.Equal(wantUntil))
	assert.Nil(t, entries["gpt-5.6-terra"].RatesValidUntil)

	require.Contains(t, entries, "gemini-3.8-flash")
	require.Contains(t, entries, "gemini/gemini-3.8-flash")
	flash := entries["gemini-3.8-flash"]
	assert.InDelta(t, 7.5e-07, flash.InputCostPerToken, 1e-15)
	assert.InDelta(t, 3.75e-06, flash.OutputCostPerToken, 1e-15)
	assert.True(t, flash.HasCacheRead)
	assert.InDelta(t, 7.5e-08, flash.CacheReadCost, 1e-15)
	flash37 := entries["gemini-3.7-flash"]
	assert.InDelta(t, 7.5e-07, flash37.InputCostPerToken, 1e-15)
	assert.InDelta(t, 3.75e-06, flash37.OutputCostPerToken, 1e-15)

	claude := entries["claude-sonnet-5"]
	assert.True(t, claude.HasCacheRead)
	assert.True(t, claude.HasCacheCreate)
	assert.True(t, claude.HasCacheCreateAbove1hr)
	assert.InDelta(t, 2.5e-06, claude.CacheCreateCost, 1e-15)
	assert.InDelta(t, 4e-06, claude.CacheCreateAbove1hr, 1e-15)

	qwen := entries["dashscope/qwen-flash"]
	assert.False(t, qwen.HasCacheRead)
	assert.False(t, qwen.HasCacheCreate)
	assert.False(t, qwen.HasCacheCreateAbove1hr)
}
