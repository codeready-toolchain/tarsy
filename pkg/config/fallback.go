package config

import (
	"fmt"
	"slices"
)

// FallbackLayer is one YAML node's named-list selector plus its deprecated
// inline fallback_providers slice.
type FallbackLayer struct {
	ListName string
	Inline   []FallbackProviderEntry
}

// ExpandFallbackLayer expands one YAML node to a slice-or-nil.
//
// A non-empty ListName looks up the catalog and always returns a non-nil
// slice (an empty catalog entry means "no fallback", not inherit). An empty
// ListName returns Inline as-is (nil inherits; a non-nil empty slice clears).
// Unknown names return an error.
func ExpandFallbackLayer(lists map[string][]FallbackProviderEntry, layer FallbackLayer) ([]FallbackProviderEntry, error) {
	if layer.ListName == "" {
		return layer.Inline, nil
	}
	entries, ok := lists[layer.ListName]
	if !ok {
		return nil, fmt.Errorf("unknown fallback list %q", layer.ListName)
	}
	if entries == nil {
		return []FallbackProviderEntry{}, nil
	}
	return slices.Clone(entries), nil
}

// LastNonNilFallback returns the last non-nil fallback list from layers listed
// in lowest-to-highest precedence order. A non-nil empty slice is an explicit
// override that clears inherited values. Returns nil when no layer provides a
// value. The returned slice is a copy.
func LastNonNilFallback(layers ...[]FallbackProviderEntry) []FallbackProviderEntry {
	var result []FallbackProviderEntry
	found := false
	for _, layer := range layers {
		if layer != nil {
			result = layer
			found = true
		}
	}
	if !found {
		return nil
	}
	if len(result) == 0 {
		return []FallbackProviderEntry{}
	}
	return slices.Clone(result)
}

// ResolveFallbackLayers expands each layer then applies last-non-nil.
func ResolveFallbackLayers(lists map[string][]FallbackProviderEntry, layers ...FallbackLayer) ([]FallbackProviderEntry, error) {
	expanded := make([][]FallbackProviderEntry, len(layers))
	for i, layer := range layers {
		got, err := ExpandFallbackLayer(lists, layer)
		if err != nil {
			return nil, err
		}
		expanded[i] = got
	}
	return LastNonNilFallback(expanded...), nil
}
