package cost

import (
	"strings"
)

// knownProviderPrefixes are stripped as a last-resort heuristic.
var knownProviderPrefixes = []string{
	"gemini/",
	"openai/",
	"anthropic/",
	"vertex_ai/",
	"bedrock/",
	"azure/",
	"openrouter/",
	"xai/",
}

// findInCatalog looks up modelName in entries using exact match then conservative heuristics.
// Conflicting heuristic candidates (different rates) → not found.
func findInCatalog(entries map[string]catalogEntry, modelName string) (catalogEntry, string, bool) {
	if modelName == "" || len(entries) == 0 {
		return catalogEntry{}, "", false
	}

	if e, ok := entries[modelName]; ok {
		return e, modelName, true
	}

	type candidate struct {
		key   string
		entry catalogEntry
	}
	var candidates []candidate

	// Suffix match: catalog key ends with "/{model}"
	suffix := "/" + modelName
	for key, entry := range entries {
		if strings.HasSuffix(key, suffix) {
			candidates = append(candidates, candidate{key: key, entry: entry})
		}
	}

	// Prefix-strip: strip known provider prefix from catalog keys and compare.
	if len(candidates) == 0 {
		for key, entry := range entries {
			stripped := stripProviderPrefix(key)
			if stripped == modelName {
				candidates = append(candidates, candidate{key: key, entry: entry})
			}
		}
	}

	// Also try stripping prefix from the TARSy model name itself.
	if len(candidates) == 0 {
		strippedModel := stripProviderPrefix(modelName)
		if strippedModel != modelName {
			if e, ok := entries[strippedModel]; ok {
				return e, strippedModel, true
			}
			suffix = "/" + strippedModel
			for key, entry := range entries {
				if strings.HasSuffix(key, suffix) || stripProviderPrefix(key) == strippedModel {
					candidates = append(candidates, candidate{key: key, entry: entry})
				}
			}
		}
	}

	if len(candidates) == 0 {
		return catalogEntry{}, "", false
	}
	if len(candidates) == 1 {
		return candidates[0].entry, candidates[0].key, true
	}

	// Multiple candidates: accept only if rates agree (conflict → unpriced).
	first := candidates[0].entry
	for _, c := range candidates[1:] {
		if !ratesEqual(first, c.entry) {
			return catalogEntry{}, "", false
		}
	}
	return first, candidates[0].key, true
}

func stripProviderPrefix(key string) string {
	for _, p := range knownProviderPrefixes {
		if rest, ok := strings.CutPrefix(key, p); ok {
			return rest
		}
	}
	return key
}

func ratesEqual(a, b catalogEntry) bool {
	if a.InputCostPerToken != b.InputCostPerToken || a.OutputCostPerToken != b.OutputCostPerToken {
		return false
	}
	if (a.OutputCostPerReasoningToken == nil) != (b.OutputCostPerReasoningToken == nil) {
		return false
	}
	if a.OutputCostPerReasoningToken != nil && *a.OutputCostPerReasoningToken != *b.OutputCostPerReasoningToken {
		return false
	}
	return true
}
