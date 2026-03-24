package memory

import (
	"encoding/json"
	"strings"
)

// ParseReflectorResponse extracts a ReflectorResult from potentially messy LLM output.
// Strategy: try strict JSON, strip markdown fences, extract by bracket depth.
// On total failure, returns an empty result (not an error) — extraction never blocks scoring.
func ParseReflectorResponse(raw string) (*ReflectorResult, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return &ReflectorResult{}, false
	}

	// 1. Try strict JSON.
	if result, ok := tryUnmarshal(raw); ok {
		return result, true
	}

	// 2. Strip markdown fences and retry.
	stripped := stripMarkdownFences(raw)
	if stripped != raw {
		if result, ok := tryUnmarshal(stripped); ok {
			return result, true
		}
	}

	// 3. Extract JSON object by bracket depth.
	if extracted := extractJSONObject(raw); extracted != "" {
		if result, ok := tryUnmarshal(extracted); ok {
			return result, true
		}
	}

	return &ReflectorResult{}, false
}

func tryUnmarshal(s string) (*ReflectorResult, bool) {
	var result ReflectorResult
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return nil, false
	}
	return &result, true
}

func stripMarkdownFences(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	inside := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inside && (trimmed == "```json" || trimmed == "```") {
			inside = true
			continue
		}
		if inside && trimmed == "```" {
			inside = false
			continue
		}
		if inside {
			out = append(out, line)
		}
	}

	if len(out) == 0 {
		return s
	}
	return strings.Join(out, "\n")
}

func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start == -1 {
		return ""
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
