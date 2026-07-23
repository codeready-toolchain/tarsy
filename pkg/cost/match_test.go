package cost

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindInCatalog(t *testing.T) {
	entries := map[string]catalogEntry{
		"gemini-3.6-flash": {
			InputCostPerToken:  1e-6,
			OutputCostPerToken: 2e-6,
		},
		"gemini/gemini-2.5-pro": {
			InputCostPerToken:  3e-6,
			OutputCostPerToken: 4e-6,
		},
		"openai/gpt-4o": {
			InputCostPerToken:  5e-6,
			OutputCostPerToken: 6e-6,
		},
	}

	tests := []struct {
		name      string
		model     string
		wantKey   string
		wantFound bool
	}{
		{name: "exact", model: "gemini-3.6-flash", wantKey: "gemini-3.6-flash", wantFound: true},
		{name: "suffix heuristic", model: "gemini-2.5-pro", wantKey: "gemini/gemini-2.5-pro", wantFound: true},
		{name: "strip catalog prefix", model: "gpt-4o", wantKey: "openai/gpt-4o", wantFound: true},
		{name: "strip model prefix then exact", model: "gemini/gemini-3.6-flash", wantKey: "gemini-3.6-flash", wantFound: true},
		{name: "empty model", model: "", wantFound: false},
		{name: "unknown", model: "no-such-model", wantFound: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, key, ok := findInCatalog(entries, tt.model)
			assert.Equal(t, tt.wantFound, ok)
			if tt.wantFound {
				assert.Equal(t, tt.wantKey, key)
			}
		})
	}
}

func TestFindInCatalog_EmptyMap(t *testing.T) {
	_, _, ok := findInCatalog(nil, "gpt-4o")
	assert.False(t, ok)
	_, _, ok = findInCatalog(map[string]catalogEntry{}, "gpt-4o")
	assert.False(t, ok)
}

func TestStripProviderPrefix(t *testing.T) {
	assert.Equal(t, "gemini-2.5-pro", stripProviderPrefix("gemini/gemini-2.5-pro"))
	assert.Equal(t, "gpt-4o", stripProviderPrefix("openai/gpt-4o"))
	assert.Equal(t, "bare", stripProviderPrefix("bare"))
}

func TestRatesEqual(t *testing.T) {
	r := 1e-6
	a := catalogEntry{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, OutputCostPerReasoningToken: &r}
	b := catalogEntry{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, OutputCostPerReasoningToken: &r}
	require.True(t, ratesEqual(a, b))

	c := catalogEntry{InputCostPerToken: 1e-6, OutputCostPerToken: 9e-6}
	assert.False(t, ratesEqual(a, c))
}
