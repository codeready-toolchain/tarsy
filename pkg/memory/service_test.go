package memory

import (
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/investigationmemory"
	"github.com/stretchr/testify/assert"
)

func TestFormatVector(t *testing.T) {
	tests := []struct {
		name string
		vec  []float32
		want string
	}{
		{"empty", nil, "[]"},
		{"single", []float32{1.5}, "[1.5]"},
		{"multiple", []float32{0.1, 0.2, 0.3}, "[0.1,0.2,0.3]"},
		{"integers", []float32{1, 2, 3}, "[1,2,3]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatVector(tt.vec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "short", truncate("short", 10))
	assert.Equal(t, "exactly10!", truncate("exactly10!", 10))
	assert.Equal(t, "this is lo...", truncate("this is longer than 10", 10))
}

func TestEntToDetail(t *testing.T) {
	now := time.Now()
	alertType := "cpu_spike"
	chainID := "chain-1"

	m := &ent.InvestigationMemory{
		ID:              "mem-1",
		Project:         "default",
		Content:         "test content",
		Category:        investigationmemory.CategorySemantic,
		Valence:         investigationmemory.ValencePositive,
		Confidence:      0.75,
		SeenCount:       3,
		SourceSessionID: "sess-1",
		AlertType:       &alertType,
		ChainID:         &chainID,
		Deprecated:      false,
		CreatedAt:       now,
		UpdatedAt:       now,
		LastSeenAt:      now,
	}

	detail := entToDetail(m)
	assert.Equal(t, m.ID, detail.ID)
	assert.Equal(t, m.Project, detail.Project)
	assert.Equal(t, m.Content, detail.Content)
	assert.Equal(t, string(m.Category), detail.Category)
	assert.Equal(t, string(m.Valence), detail.Valence)
	assert.Equal(t, m.Confidence, detail.Confidence)
	assert.Equal(t, m.SeenCount, detail.SeenCount)
	assert.Equal(t, m.SourceSessionID, detail.SourceSessionID)
	assert.Equal(t, m.AlertType, detail.AlertType)
	assert.Equal(t, m.ChainID, detail.ChainID)
	assert.Equal(t, m.Deprecated, detail.Deprecated)
	assert.True(t, m.CreatedAt.Equal(detail.CreatedAt), "CreatedAt mismatch")
	assert.True(t, m.UpdatedAt.Equal(detail.UpdatedAt), "UpdatedAt mismatch")
	assert.True(t, m.LastSeenAt.Equal(detail.LastSeenAt), "LastSeenAt mismatch")
}
