package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLLMInteractionCacheTokensJSON(t *testing.T) {
	read := 40
	create := 10

	t.Run("list item includes cache when set", func(t *testing.T) {
		item := LLMInteractionListItem{
			ID:                  "int-1",
			InteractionType:     "iteration",
			ModelName:           "gemini-2.0-flash",
			CacheReadTokens:     &read,
			CacheCreationTokens: &create,
			CreatedAt:           "2026-08-26T10:00:00Z",
		}
		raw, err := json.Marshal(item)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, float64(40), got["cache_read_tokens"])
		assert.Equal(t, float64(10), got["cache_creation_tokens"])
	})

	t.Run("list item omits cache when unset", func(t *testing.T) {
		item := LLMInteractionListItem{
			ID:              "int-1",
			InteractionType: "iteration",
			ModelName:       "gemini-2.0-flash",
			CreatedAt:       "2026-08-26T10:00:00Z",
		}
		raw, err := json.Marshal(item)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "cache_read")
		assert.NotContains(t, string(raw), "cache_creation")
	})

	t.Run("detail includes cache when set", func(t *testing.T) {
		detail := LLMInteractionDetailResponse{
			ID:                  "int-1",
			InteractionType:     "iteration",
			ModelName:           "gemini-2.0-flash",
			CacheReadTokens:     &read,
			CacheCreationTokens: &create,
			LLMRequest:          map[string]any{},
			LLMResponse:         map[string]any{},
			CreatedAt:           "2026-08-26T10:00:00Z",
		}
		raw, err := json.Marshal(detail)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, float64(40), got["cache_read_tokens"])
		assert.Equal(t, float64(10), got["cache_creation_tokens"])
	})

	t.Run("detail omits cache when unset", func(t *testing.T) {
		detail := LLMInteractionDetailResponse{
			ID:              "int-1",
			InteractionType: "iteration",
			ModelName:       "gemini-2.0-flash",
			LLMRequest:      map[string]any{},
			LLMResponse:     map[string]any{},
			CreatedAt:       "2026-08-26T10:00:00Z",
		}
		raw, err := json.Marshal(detail)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "cache_read")
		assert.NotContains(t, string(raw), "cache_creation")
	})
}
