package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCostTime(t *testing.T) {
	t.Parallel()

	t.Run("date only UTC midnight", func(t *testing.T) {
		t.Parallel()
		got, err := ParseCostTime("2026-10-01")
		require.NoError(t, err)
		assert.True(t, got.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("RFC3339 normalized to UTC", func(t *testing.T) {
		t.Parallel()
		got, err := ParseCostTime("2026-10-01T12:00:00-07:00")
		require.NoError(t, err)
		assert.True(t, got.Equal(time.Date(2026, 10, 1, 19, 0, 0, 0, time.UTC)))
	})

	t.Run("empty fails", func(t *testing.T) {
		t.Parallel()
		_, err := ParseCostTime("")
		require.Error(t, err)
	})

	t.Run("invalid fails", func(t *testing.T) {
		t.Parallel()
		_, err := ParseCostTime("not-a-date")
		require.Error(t, err)
	})
}

func TestParsePromotionWindow(t *testing.T) {
	t.Parallel()

	t.Run("omitted start", func(t *testing.T) {
		t.Parallel()
		start, end, err := ParsePromotionWindow("", "2026-10-01")
		require.NoError(t, err)
		assert.Nil(t, start)
		assert.True(t, end.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("both bounds", func(t *testing.T) {
		t.Parallel()
		start, end, err := ParsePromotionWindow("2026-08-01", "2026-10-01T12:00:00Z")
		require.NoError(t, err)
		require.NotNil(t, start)
		assert.True(t, start.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)))
		assert.True(t, end.Equal(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)))
	})

	t.Run("missing end", func(t *testing.T) {
		t.Parallel()
		_, _, err := ParsePromotionWindow("2026-08-01", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "end is required")
	})

	t.Run("invalid start", func(t *testing.T) {
		t.Parallel()
		_, _, err := ParsePromotionWindow("bad-start", "2026-10-01")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "start:")
	})
}
