package cost

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEstimate(t *testing.T) {
	tests := []struct {
		name     string
		rates    Rates
		input    int
		output   int
		thinking int
		want     float64
	}{
		{
			name:   "input and output only",
			rates:  Rates{Input: 1e-6, Output: 2e-6, Reasoning: 2e-6},
			input:  1_000_000,
			output: 500_000,
			want:   2.0, // 1.0 + 1.0
		},
		{
			name:     "thinking zero is ignored",
			rates:    Rates{Input: 1e-6, Output: 2e-6, Reasoning: 9e-6},
			input:    1000,
			output:   0,
			thinking: 0,
			want:     0.001,
		},
		{
			name:     "thinking uses reasoning rate",
			rates:    Rates{Input: 0, Output: 1e-6, Reasoning: 3e-6},
			output:   1000,
			thinking: 2000,
			want:     0.001 + 0.006,
		},
		{
			name: "all zero tokens",
			rates: Rates{Input: 1e-6, Output: 2e-6, Reasoning: 3e-6},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Estimate(tt.rates, tt.input, tt.output, tt.thinking)
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
	assert.InDelta(t, 12e-6, rates.Reasoning, 1e-15, "reasoning falls back to output for overrides")
}
