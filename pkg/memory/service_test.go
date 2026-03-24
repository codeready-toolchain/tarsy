package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitialConfidence(t *testing.T) {
	tests := []struct {
		name  string
		score int
		want  float64
	}{
		{"excellent score", 95, 0.8},
		{"high score", 80, 0.8},
		{"good score", 70, 0.6},
		{"medium score", 60, 0.6},
		{"mediocre score", 50, 0.4},
		{"low score", 40, 0.4},
		{"poor score", 30, 0.3},
		{"zero score", 0, 0.3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := initialConfidence(tt.score)
			assert.Equal(t, tt.want, got)
		})
	}
}

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
