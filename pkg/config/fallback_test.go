package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpandFallbackLayer(t *testing.T) {
	t.Parallel()

	catalog := map[string][]FallbackProviderEntry{
		"premium": {{Provider: "opus"}, {Provider: "sonnet"}},
		"empty":   {},
		"null":    nil,
	}

	tests := []struct {
		name    string
		layer   FallbackLayer
		want    []FallbackProviderEntry
		wantNil bool
		wantErr string
	}{
		{
			name:  "named list clones catalog slice",
			layer: FallbackLayer{ListName: "premium"},
			want:  []FallbackProviderEntry{{Provider: "opus"}, {Provider: "sonnet"}},
		},
		{
			name:  "empty catalog entry returns non-nil empty",
			layer: FallbackLayer{ListName: "empty"},
			want:  []FallbackProviderEntry{},
		},
		{
			name:  "nil catalog entry returns non-nil empty",
			layer: FallbackLayer{ListName: "null"},
			want:  []FallbackProviderEntry{},
		},
		{
			name:    "omitted selector returns nil inline",
			layer:   FallbackLayer{},
			wantNil: true,
		},
		{
			name:  "omitted selector returns inline as-is",
			layer: FallbackLayer{Inline: []FallbackProviderEntry{{Provider: "inline"}}},
			want:  []FallbackProviderEntry{{Provider: "inline"}},
		},
		{
			name:  "empty-string selector inherits inline",
			layer: FallbackLayer{ListName: "", Inline: []FallbackProviderEntry{{Provider: "inline"}}},
			want:  []FallbackProviderEntry{{Provider: "inline"}},
		},
		{
			name:    "unknown name",
			layer:   FallbackLayer{ListName: "nope"},
			wantErr: `unknown fallback list "nope"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ExpandFallbackLayer(catalog, tt.layer)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
			assert.NotNil(t, got)
		})
	}

	t.Run("clone is independent of catalog", func(t *testing.T) {
		t.Parallel()
		got, err := ExpandFallbackLayer(catalog, FallbackLayer{ListName: "premium"})
		require.NoError(t, err)
		require.NotEmpty(t, got)
		got[0].Provider = "mutated"
		assert.Equal(t, "opus", catalog["premium"][0].Provider)
	})

	t.Run("nil catalog with named selector", func(t *testing.T) {
		t.Parallel()
		_, err := ExpandFallbackLayer(nil, FallbackLayer{ListName: "premium"})
		require.Error(t, err)
		assert.Equal(t, `unknown fallback list "premium"`, err.Error())
	})
}

func TestLastNonNilFallback(t *testing.T) {
	t.Parallel()

	lower := []FallbackProviderEntry{{Provider: "defaults"}}
	higher := []FallbackProviderEntry{{Provider: "chain"}}
	empty := []FallbackProviderEntry{}

	assert.Nil(t, LastNonNilFallback(nil, nil))
	assert.Equal(t, lower, LastNonNilFallback(lower, nil))
	assert.Equal(t, higher, LastNonNilFallback(lower, higher))
	assert.NotNil(t, LastNonNilFallback(lower, empty))
	assert.Empty(t, LastNonNilFallback(lower, empty))

	got := LastNonNilFallback(lower)
	require.NotEmpty(t, got)
	got[0].Provider = "mutated"
	assert.Equal(t, "defaults", lower[0].Provider)
}

func TestResolveFallbackLayers(t *testing.T) {
	t.Parallel()

	catalog := map[string][]FallbackProviderEntry{
		"premium": {{Provider: "opus"}},
		"mid":     {{Provider: "sonnet"}},
		"empty":   {},
	}

	t.Run("last expanded named list wins", func(t *testing.T) {
		t.Parallel()
		got, err := ResolveFallbackLayers(catalog,
			FallbackLayer{ListName: "premium"},
			FallbackLayer{ListName: "mid"},
		)
		require.NoError(t, err)
		assert.Equal(t, []FallbackProviderEntry{{Provider: "sonnet"}}, got)
	})

	t.Run("named empty clears inherited", func(t *testing.T) {
		t.Parallel()
		got, err := ResolveFallbackLayers(catalog,
			FallbackLayer{ListName: "premium"},
			FallbackLayer{ListName: "empty"},
		)
		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("inline still works when selector unset", func(t *testing.T) {
		t.Parallel()
		inline := []FallbackProviderEntry{{Provider: "inline"}}
		got, err := ResolveFallbackLayers(catalog,
			FallbackLayer{Inline: inline},
			FallbackLayer{},
		)
		require.NoError(t, err)
		assert.Equal(t, inline, got)
	})

	t.Run("unknown name errors", func(t *testing.T) {
		t.Parallel()
		_, err := ResolveFallbackLayers(catalog, FallbackLayer{ListName: "ghost"})
		require.Error(t, err)
		assert.Equal(t, `unknown fallback list "ghost"`, err.Error())
	})

	t.Run("inline at higher layer beats named lower layer", func(t *testing.T) {
		t.Parallel()
		inline := []FallbackProviderEntry{{Provider: "inline"}}
		got, err := ResolveFallbackLayers(catalog,
			FallbackLayer{ListName: "premium"},
			FallbackLayer{Inline: inline},
		)
		require.NoError(t, err)
		assert.Equal(t, inline, got)
	})

	t.Run("named at higher layer beats inline lower layer", func(t *testing.T) {
		t.Parallel()
		got, err := ResolveFallbackLayers(catalog,
			FallbackLayer{Inline: []FallbackProviderEntry{{Provider: "inline"}}},
			FallbackLayer{ListName: "mid"},
		)
		require.NoError(t, err)
		assert.Equal(t, []FallbackProviderEntry{{Provider: "sonnet"}}, got)
	})
}
