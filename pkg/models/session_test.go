package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidReviewAction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"claim", "claim", true},
		{"unclaim", "unclaim", true},
		{"resolve", "resolve", true},
		{"reopen", "reopen", true},
		{"empty", "", false},
		{"unknown", "bogus", false},
		{"case sensitive", "Claim", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidReviewAction(tt.input))
		})
	}
}
