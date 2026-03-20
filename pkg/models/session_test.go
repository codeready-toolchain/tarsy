package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseTriageGroupKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    TriageGroupKey
		wantErr bool
	}{
		{"investigating", "investigating", TriageGroupInvestigating, false},
		{"needs_review", "needs_review", TriageGroupNeedsReview, false},
		{"in_progress", "in_progress", TriageGroupInProgress, false},
		{"reviewed", "reviewed", TriageGroupReviewed, false},
		{"old resolved", "resolved", "", true},
		{"empty", "", "", true},
		{"unknown", "bogus", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTriageGroupKey(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestValidReviewAction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"claim", "claim", true},
		{"unclaim", "unclaim", true},
		{"complete", "complete", true},
		{"reopen", "reopen", true},
		{"update_feedback", "update_feedback", true},
		{"empty", "", false},
		{"unknown", "bogus", false},
		{"old resolve", "resolve", false},
		{"old update_note", "update_note", false},
		{"case sensitive", "Claim", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ValidReviewAction(tt.input))
		})
	}
}
