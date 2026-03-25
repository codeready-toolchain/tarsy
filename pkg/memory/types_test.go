package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHumanizeAge(t *testing.T) {
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"just now", now.Add(-30 * time.Second), "just now"},
		{"1 minute", now.Add(-90 * time.Second), "1 minute ago"},
		{"5 minutes", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"1 hour", now.Add(-90 * time.Minute), "1 hour ago"},
		{"3 hours", now.Add(-3 * time.Hour), "3 hours ago"},
		{"1 day", now.Add(-36 * time.Hour), "1 day ago"},
		{"5 days", now.Add(-5 * 24 * time.Hour), "5 days ago"},
		{"1 week", now.Add(-10 * 24 * time.Hour), "1 week ago"},
		{"3 weeks", now.Add(-21 * 24 * time.Hour), "3 weeks ago"},
		{"1 month", now.Add(-45 * 24 * time.Hour), "1 month ago"},
		{"6 months", now.Add(-180 * 24 * time.Hour), "6 months ago"},
		{"1 year", now.Add(-400 * 24 * time.Hour), "1 year ago"},
		{"2 years", now.Add(-800 * 24 * time.Hour), "2 years ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, humanizeAge(tt.t, now))
		})
	}
}

func TestFormatMemoryAgeSince(t *testing.T) {
	now := time.Date(2026, 3, 25, 14, 0, 0, 0, time.UTC)

	t.Run("created only (updated close to created)", func(t *testing.T) {
		created := now.Add(-3 * 24 * time.Hour)
		updated := created.Add(10 * time.Minute) // within threshold
		assert.Equal(t, "learned 3 days ago", FormatMemoryAgeSince(created, updated, now))
	})

	t.Run("created and updated (updated differs meaningfully)", func(t *testing.T) {
		created := now.Add(-180 * 24 * time.Hour)
		updated := now.Add(-24 * time.Hour)
		assert.Equal(t, "learned 6 months ago, updated 1 day ago", FormatMemoryAgeSince(created, updated, now))
	})

	t.Run("same timestamps", func(t *testing.T) {
		created := now.Add(-2 * time.Hour)
		assert.Equal(t, "learned 2 hours ago", FormatMemoryAgeSince(created, created, now))
	})

	t.Run("updated exactly at threshold", func(t *testing.T) {
		created := now.Add(-48 * time.Hour)
		updated := created.Add(time.Hour) // exactly at threshold, not >
		assert.Equal(t, "learned 2 days ago", FormatMemoryAgeSince(created, updated, now))
	})

	t.Run("updated just past threshold", func(t *testing.T) {
		created := now.Add(-48 * time.Hour)
		updated := created.Add(time.Hour + time.Second)
		assert.Contains(t, FormatMemoryAgeSince(created, updated, now), "updated")
	})
}
