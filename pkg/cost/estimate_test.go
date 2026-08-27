package cost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f64(v float64) *float64 { return &v }

func TestEstimate(t *testing.T) {
	tests := []struct {
		name  string
		rates Rates
		tok   Tokens
		want  float64
	}{
		{
			name:  "input and output only",
			rates: Rates{Input: 1e-6, Output: 2e-6, Reasoning: f64(2e-6)},
			tok:   Tokens{Input: 1_000_000, Output: 500_000},
			want:  2.0, // 1.0 + 1.0
		},
		{
			name:  "thinking zero is ignored",
			rates: Rates{Input: 1e-6, Output: 2e-6, Reasoning: f64(9e-6)},
			tok:   Tokens{Input: 1000},
			want:  0.001,
		},
		{
			name:  "thinking uses reasoning rate when set",
			rates: Rates{Input: 0, Output: 1e-6, Reasoning: f64(3e-6)},
			tok:   Tokens{Output: 1000, Thinking: 2000},
			want:  0.001 + 0.006,
		},
		{
			name:  "thinking falls back to output when reasoning unset",
			rates: Rates{Input: 0, Output: 2e-6},
			tok:   Tokens{Thinking: 1_000_000},
			want:  2.0,
		},
		{
			name:  "all zero tokens",
			rates: Rates{Input: 1e-6, Output: 2e-6, Reasoning: f64(3e-6)},
			want:  0,
		},
		{
			name: "cache read and create",
			rates: Rates{
				Input: 1e-6, Output: 2e-6,
				CacheRead: 1e-7, CacheCreation: 1.25e-6,
			},
			tok:  Tokens{Input: 100_000, CacheRead: 800_000, CacheCreation: 100_000, Output: 10_000},
			want: 0.1 + 0.08 + 0.125 + 0.02,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Estimate(tt.rates, tt.tok)
			assert.InDelta(t, tt.want, got, 1e-12)
		})
	}
}

func TestOverrideRates(t *testing.T) {
	rates := overrideRates(ModelRateOverride{
		InputPerMillion:  2.0,
		OutputPerMillion: 12.0,
	})
	assert.InDelta(t, 2e-6, rates.Input, 1e-15)
	assert.InDelta(t, 12e-6, rates.Output, 1e-15)
	assert.Nil(t, rates.Reasoning, "overrides leave reasoning unset")
	assert.InDelta(t, 12.0, Estimate(rates, Tokens{Thinking: 1_000_000}), 1e-12,
		"thinking should fall back to output rate")
}

func TestApplyCacheRates(t *testing.T) {
	base := Rates{Input: 1e-6, Output: 2e-6}

	t.Run("overlay non-claude derives 0.1 read and 1.25 create", func(t *testing.T) {
		got := applyCacheRates(base, "gpt-5.2", nil)
		assert.InDelta(t, 1e-7, got.CacheRead, 1e-15)
		assert.InDelta(t, 1.25e-6, got.CacheCreation, 1e-15)
	})

	t.Run("overlay claude derives 0.1 read and 2x create", func(t *testing.T) {
		got := applyCacheRates(base, "claude-sonnet-5", nil)
		assert.InDelta(t, 1e-7, got.CacheRead, 1e-15)
		assert.InDelta(t, 2e-6, got.CacheCreation, 1e-15)
	})

	t.Run("catalog read used when present", func(t *testing.T) {
		e := catalogEntry{HasCacheRead: true, CacheReadCost: 3e-7}
		got := applyCacheRates(base, "gemini-3.1-pro-preview", &e)
		assert.InDelta(t, 3e-7, got.CacheRead, 1e-15)
		assert.InDelta(t, 1.25e-6, got.CacheCreation, 1e-15)
	})

	t.Run("claude uses 1h create not 5m", func(t *testing.T) {
		e := catalogEntry{
			HasCacheCreate:         true,
			CacheCreateCost:        3e-6,
			HasCacheCreateAbove1hr: true,
			CacheCreateAbove1hr:    9e-6,
		}
		got := applyCacheRates(base, "vertexai/claude-sonnet-5", &e)
		assert.InDelta(t, 9e-6, got.CacheCreation, 1e-15)
	})

	t.Run("claude missing 1h derives 2x not 5m catalog create", func(t *testing.T) {
		e := catalogEntry{HasCacheCreate: true, CacheCreateCost: 3e-6}
		got := applyCacheRates(base, "claude-opus-5", &e)
		assert.InDelta(t, 2e-6, got.CacheCreation, 1e-15)
	})

	t.Run("non-claude uses catalog create when present", func(t *testing.T) {
		e := catalogEntry{HasCacheCreate: true, CacheCreateCost: 5e-6}
		got := applyCacheRates(base, "gpt-5.2", &e)
		assert.InDelta(t, 5e-6, got.CacheCreation, 1e-15)
	})
}

func TestIsClaudeModel(t *testing.T) {
	assert.True(t, isClaudeModel("claude-sonnet-5"))
	assert.True(t, isClaudeModel("Claude-Opus-5"))
	assert.False(t, isClaudeModel("gpt-5.2"))
	assert.False(t, isClaudeModel("gemini-3.1-pro-preview"))
}

func TestApplyCacheRates_DoesNotMarkUnpriced(t *testing.T) {
	// Missing catalog cache fields still derive; input/output remain usable.
	got := applyCacheRates(Rates{Input: 2e-6, Output: 1e-5}, "unknown-model", &catalogEntry{})
	require.Equal(t, 2e-6, got.Input)
	require.Equal(t, 1e-5, got.Output)
	assert.InDelta(t, 2e-7, got.CacheRead, 1e-15)
	assert.InDelta(t, 2.5e-6, got.CacheCreation, 1e-15)
}
