package cost

// Estimate computes USD cost from token counts and per-token rates.
// Thinking tokens are priced at rates.Reasoning when set, otherwise rates.Output.
func Estimate(rates Rates, inputTokens, outputTokens, thinkingTokens int) float64 {
	cost := float64(inputTokens)*rates.Input + float64(outputTokens)*rates.Output
	if thinkingTokens > 0 {
		reasoning := rates.Output
		if rates.Reasoning != nil {
			reasoning = *rates.Reasoning
		}
		cost += float64(thinkingTokens) * reasoning
	}
	return cost
}

// overrideRates converts per-million USD overrides to per-token rates.
// Reasoning is left unset so Estimate falls back to the output rate.
func overrideRates(o ModelRateOverride) Rates {
	in := o.InputPerMillion / 1_000_000
	out := o.OutputPerMillion / 1_000_000
	return Rates{Input: in, Output: out}
}
