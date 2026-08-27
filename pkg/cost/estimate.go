package cost

import "strings"

// Estimate computes USD cost from token counts and per-token rates.
// Thinking tokens are priced at rates.Reasoning when set, otherwise rates.Output.
func Estimate(rates Rates, t Tokens) float64 {
	cost := float64(t.Input)*rates.Input +
		float64(t.Output)*rates.Output +
		float64(t.CacheRead)*rates.CacheRead +
		float64(t.CacheCreation)*rates.CacheCreation
	if t.Thinking > 0 {
		reasoning := rates.Output
		if rates.Reasoning != nil {
			reasoning = *rates.Reasoning
		}
		cost += float64(t.Thinking) * reasoning
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

func isClaudeModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "claude")
}

func derivedCacheRead(inputRate float64) float64 {
	return 0.1 * inputRate
}

func derivedCacheCreate(modelName string, inputRate float64) float64 {
	if isClaudeModel(modelName) {
		return 2 * inputRate
	}
	return 1.25 * inputRate
}

// applyCacheRates fills CacheRead / CacheCreation on already-resolved input/output rates.
// Overlay (e == nil) always derives. Catalog/snapshot uses explicit cache fields when
// present; Claude 1h writes never use the 5m cache_creation_input_token_cost.
func applyCacheRates(rates Rates, modelName string, e *catalogEntry) Rates {
	read := derivedCacheRead(rates.Input)
	create := derivedCacheCreate(modelName, rates.Input)
	if e != nil {
		if e.HasCacheRead {
			read = e.CacheReadCost
		}
		if isClaudeModel(modelName) {
			if e.HasCacheCreateAbove1hr {
				create = e.CacheCreateAbove1hr
			}
		} else if e.HasCacheCreate {
			create = e.CacheCreateCost
		}
	}
	rates.CacheRead = read
	rates.CacheCreation = create
	return rates
}
