package queue

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestMemoryExcludeIDs(t *testing.T) {
	tests := []struct {
		name     string
		briefing *agent.MemoryBriefing
		want     map[string]struct{}
	}{
		{
			name:     "nil briefing",
			briefing: nil,
			want:     nil,
		},
		{
			name:     "empty IDs",
			briefing: &agent.MemoryBriefing{InjectedIDs: nil},
			want:     nil,
		},
		{
			name: "multiple IDs",
			briefing: &agent.MemoryBriefing{
				InjectedIDs: []string{"mem-1", "mem-2", "mem-3"},
			},
			want: map[string]struct{}{
				"mem-1": {},
				"mem-2": {},
				"mem-3": {},
			},
		},
		{
			name: "single ID",
			briefing: &agent.MemoryBriefing{
				InjectedIDs: []string{"mem-1"},
			},
			want: map[string]struct{}{
				"mem-1": {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memoryExcludeIDs(tt.briefing)
			assert.Equal(t, tt.want, got)
		})
	}
}
