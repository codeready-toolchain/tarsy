package mcp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseActionInput parses a raw ActionInput string into structured parameters.
//
// Parsing cascade (first successful parse wins):
//  1. JSON object → map[string]any
//  2. JSON non-object (string, number, array) → {"input": value}
//  3. YAML with complex structures (arrays, nested maps) → map[string]any
//  4. Key-value pairs (key: value or key=value, comma/newline separated)
//  5. Single raw string → {"input": string}
//
// Empty input returns empty map (for no-parameter tools).
func ParseActionInput(input string) (map[string]any, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return map[string]any{}, nil
	}

	// 1. Try JSON
	if result, ok := tryParseJSON(input); ok {
		return result, nil
	}

	// 2. Try YAML (only if it has structure — arrays or nested maps)
	if result, ok := tryParseYAML(input); ok {
		return result, nil
	}

	// 3. Try key-value pairs
	if result, ok := tryParseKeyValue(input); ok {
		return result, nil
	}

	// 4. Fallback: raw string
	return map[string]any{"input": input}, nil
}

// tryParseJSON attempts to parse input as JSON.
// Returns (result, true) on success. Handles objects, arrays, strings,
// numbers, booleans, and null. Non-object values are wrapped as {"input": value}.
func tryParseJSON(input string) (map[string]any, bool) {
	// Quick-reject: first non-space byte must be a JSON-compatible character
	trimmed := strings.TrimSpace(input)
	if len(trimmed) == 0 {
		return nil, false
	}
	b := trimmed[0]
	isJSONStart := b == '{' || b == '[' || b == '"' ||
		(b >= '0' && b <= '9') || b == '-' ||
		b == 't' || b == 'f' || b == 'n'
	if !isJSONStart {
		return nil, false
	}

	var raw any
	if err := json.Unmarshal([]byte(input), &raw); err != nil {
		return nil, false
	}

	// If it's already a map, use directly
	if m, ok := raw.(map[string]any); ok {
		return m, true
	}

	// Non-object JSON (array, string, number, bool, null): wrap in {"input": value}
	return map[string]any{"input": raw}, true
}

// tryParseYAML attempts to parse input as YAML.
// Only accepts if result is a map with complex values (arrays, nested maps).
// Simple key: value pairs are handled by tryParseKeyValue instead, to avoid
// false positives on plain text that happens to look like YAML.
func tryParseYAML(input string) (map[string]any, bool) {
	var result map[string]any
	if err := yaml.Unmarshal([]byte(input), &result); err != nil {
		return nil, false
	}
	if len(result) == 0 {
		return nil, false
	}

	// Only accept YAML if it contains complex structures.
	// Plain "key: value" lines are handled by key-value parser.
	if hasComplexValues(result) {
		return result, true
	}
	return nil, false
}

// hasComplexValues returns true if any value in the map is a slice or nested map.
func hasComplexValues(m map[string]any) bool {
	for _, v := range m {
		switch v.(type) {
		case []any:
			return true
		case map[string]any:
			return true
		}
	}
	return false
}

// tryParseKeyValue attempts to parse "key: value" or "key=value" pairs
// separated by commas or newlines.
func tryParseKeyValue(input string) (map[string]any, bool) {
	parts := splitKeyValueParts(input)
	if len(parts) == 0 {
		return nil, false
	}

	result := make(map[string]any)
	for _, part := range parts {
		key, value, ok := parseKeyValuePair(part)
		if !ok {
			return nil, false // If any part fails, reject the whole thing
		}
		result[key] = coerceValue(value)
	}

	if len(result) == 0 {
		return nil, false
	}
	return result, true
}

// splitKeyValueParts splits input on commas and newlines, trimming whitespace.
// Known limitation: values containing commas (e.g., "tags: a,b,c, name: foo")
// will be mis-split. The input falls through to the raw-string fallback in that
// case, which is safe but loses structured parsing.
func splitKeyValueParts(input string) []string {
	// Normalize newlines to commas for uniform splitting
	normalized := strings.ReplaceAll(input, "\n", ",")
	raw := strings.Split(normalized, ",")

	var parts []string
	for _, p := range raw {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// parseKeyValuePair parses a single "key: value" or "key=value" pair.
func parseKeyValuePair(part string) (key, value string, ok bool) {
	// Try colon separator first (key: value)
	if idx := strings.Index(part, ":"); idx > 0 {
		k := strings.TrimSpace(part[:idx])
		v := strings.TrimSpace(part[idx+1:])
		if isValidKey(k) {
			return k, v, true
		}
	}

	// Try equals separator (key=value)
	if idx := strings.Index(part, "="); idx > 0 {
		k := strings.TrimSpace(part[:idx])
		v := strings.TrimSpace(part[idx+1:])
		if isValidKey(k) {
			return k, v, true
		}
	}

	return "", "", false
}

// isValidKey checks if a string looks like a parameter key (no spaces, not empty).
func isValidKey(k string) bool {
	if k == "" {
		return false
	}
	// Keys should be simple identifiers — no spaces
	return !strings.Contains(k, " ")
}

// coerceValue converts string values to appropriate Go types.
// Matches old TARSy's _convert_parameter_value().
func coerceValue(s string) any {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)

	// Booleans
	if lower == "true" {
		return true
	}
	if lower == "false" {
		return false
	}

	// Null
	if lower == "null" || lower == "none" {
		return nil
	}

	// Integer
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}

	// Float (reject NaN/Inf — not valid in JSON)
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return s
		}
		return f
	}

	return s
}
