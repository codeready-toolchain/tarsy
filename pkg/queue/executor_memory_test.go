package queue

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestAgentTypeSupportsMemory(t *testing.T) {
	supported := []config.AgentType{
		config.AgentTypeDefault,
		config.AgentTypeAction,
		config.AgentTypeOrchestrator,
	}
	for _, at := range supported {
		name := string(at)
		if name == "" {
			name = "(default)"
		}
		t.Run(name+"_supported", func(t *testing.T) {
			assert.True(t, agentTypeSupportsMemory(at))
		})
	}

	unsupported := []config.AgentType{
		config.AgentTypeSynthesis,
		config.AgentTypeExecSummary,
		config.AgentTypeScoring,
	}
	for _, at := range unsupported {
		t.Run(string(at)+"_unsupported", func(t *testing.T) {
			assert.False(t, agentTypeSupportsMemory(at))
		})
	}
}

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
