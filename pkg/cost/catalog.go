package cost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
)

// rawCatalogEntry is a flexible parse of LiteLLM catalog entries.
// Unknown fields are ignored; we only extract pricing-relevant ones.
type rawCatalogEntry map[string]any

// catalogEntry is a normalized pricing entry.
type catalogEntry struct {
	InputCostPerToken           float64
	OutputCostPerToken          float64
	OutputCostPerReasoningToken *float64
	InputCostAbove              map[int]float64 // threshold tokens → rate
	OutputCostAbove             map[int]float64
	TieredPricing               []tierRange
}

type tierRange struct {
	InputCostPerToken  float64
	OutputCostPerToken float64
	RangeStart         float64
	RangeEnd           float64
}

var aboveNkRE = regexp.MustCompile(`^(input|output)_cost_per_token_above_(\d+)k_tokens$`)

func parseCatalogJSON(data []byte) (map[string]catalogEntry, error) {
	var raw map[string]rawCatalogEntry
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse catalog JSON: %w", err)
	}

	out := make(map[string]catalogEntry, len(raw))
	for key, entry := range raw {
		parsed, ok := parseEntry(entry)
		if !ok {
			continue
		}
		out[key] = parsed
	}
	return out, nil
}

func parseEntry(raw rawCatalogEntry) (catalogEntry, bool) {
	input, hasInput := asFloat(raw["input_cost_per_token"])
	output, hasOutput := asFloat(raw["output_cost_per_token"])

	var tiers []tierRange
	if tp, ok := raw["tiered_pricing"].([]any); ok {
		tiers = parseTier(tp)
	}

	// Need base rates or at least tiered pricing to be usable.
	if !hasInput && !hasOutput && len(tiers) == 0 {
		return catalogEntry{}, false
	}

	e := catalogEntry{
		InputCostPerToken:  input,
		OutputCostPerToken: output,
		InputCostAbove:     map[int]float64{},
		OutputCostAbove:    map[int]float64{},
		TieredPricing:      tiers,
	}

	if r, ok := asFloat(raw["output_cost_per_reasoning_token"]); ok {
		e.OutputCostPerReasoningToken = &r
	}

	for k, v := range raw {
		m := aboveNkRE.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		rate, ok := asFloat(v)
		if !ok {
			continue
		}
		threshold := n * 1000
		switch m[1] {
		case "input":
			e.InputCostAbove[threshold] = rate
		case "output":
			e.OutputCostAbove[threshold] = rate
		}
	}

	return e, true
}

func parseTier(raw []any) []tierRange {
	var tiers []tierRange
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		in, _ := asFloat(m["input_cost_per_token"])
		out, _ := asFloat(m["output_cost_per_token"])
		rng, ok := m["range"].([]any)
		if !ok || len(rng) < 2 {
			continue
		}
		start, _ := asFloat(rng[0])
		end, _ := asFloat(rng[1])
		tiers = append(tiers, tierRange{
			InputCostPerToken:  in,
			OutputCostPerToken: out,
			RangeStart:         start,
			RangeEnd:           end,
		})
	}
	return tiers
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// fetchCatalog downloads and parses the remote LiteLLM catalog.
func fetchCatalog(ctx context.Context, client *http.Client, url string, maxBody int64) (map[string]catalogEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create catalog request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch catalog: unexpected status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, maxBody+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read catalog body: %w", err)
	}
	if int64(len(data)) > maxBody {
		return nil, fmt.Errorf("catalog body exceeds max size %d bytes", maxBody)
	}

	entries, err := parseCatalogJSON(data)
	if err != nil {
		return nil, err
	}
	slog.Info("Loaded LiteLLM price catalog", "entries", len(entries), "url", url)
	return entries, nil
}

// ratesForInput selects flat / above_Nk / tiered rates for the given input token count.
func (e catalogEntry) ratesForInput(inputTokens int) Rates {
	in := e.InputCostPerToken
	out := e.OutputCostPerToken

	// 1. above_Nk thresholds (highest matching threshold wins).
	bestThreshold := -1
	for threshold, rate := range e.InputCostAbove {
		if inputTokens >= threshold && threshold > bestThreshold {
			bestThreshold = threshold
			in = rate
		}
	}
	if bestThreshold >= 0 {
		if rate, ok := e.OutputCostAbove[bestThreshold]; ok {
			out = rate
		}
	} else if len(e.TieredPricing) > 0 {
		// 2. tiered_pricing single-tier pick (no blending).
		for _, t := range e.TieredPricing {
			if float64(inputTokens) >= t.RangeStart && float64(inputTokens) < t.RangeEnd {
				in = t.InputCostPerToken
				out = t.OutputCostPerToken
				break
			}
		}
	}

	reasoning := out
	if e.OutputCostPerReasoningToken != nil {
		reasoning = *e.OutputCostPerReasoningToken
	}

	return Rates{Input: in, Output: out, Reasoning: reasoning}
}
