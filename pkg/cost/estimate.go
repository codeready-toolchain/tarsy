package cost

// Estimate computes USD cost from token counts and per-token rates.
// Thinking tokens are priced at the reasoning rate when thinkingTokens > 0.
func Estimate(rates Rates, inputTokens, outputTokens, thinkingTokens int) float64 {
	cost := float64(inputTokens)*rates.Input + float64(outputTokens)*rates.Output
	if thinkingTokens > 0 {
		cost += float64(thinkingTokens) * rates.Reasoning
	}
	return cost
}

// overrideRates converts per-million USD overrides to per-token rates.
func overrideRates(o ModelRateOverride) Rates {
	in := o.InputPerMillion / 1_000_000
	out := o.OutputPerMillion / 1_000_000
	return Rates{Input: in, Output: out, Reasoning: out}
}
