package builtintools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlainToolKindsCoversExportedConsts(t *testing.T) {
	consts := []string{
		DispatchAgent,
		CancelAgent,
		ListAgents,
		LoadSkill,
		RecallPastInvestigations,
		SearchPastSessions,
	}
	require.Len(t, PlainToolKinds, len(consts), "each const must have a PlainToolKinds entry and vice versa")
	for _, c := range consts {
		_, ok := PlainToolKinds[c]
		assert.True(t, ok, "PlainToolKinds missing %q", c)
	}
}

func TestKindForPlainTool_categories(t *testing.T) {
	assert.Equal(t, KindOrchestration, PlainToolKinds[DispatchAgent])
	assert.Equal(t, KindOrchestration, PlainToolKinds[ListAgents])
	assert.Equal(t, KindSkill, PlainToolKinds[LoadSkill])
	assert.Equal(t, KindMemory, PlainToolKinds[RecallPastInvestigations])
	assert.Equal(t, KindMemory, PlainToolKinds[SearchPastSessions])
}

func TestKindForPlainTool_unknown(t *testing.T) {
	_, ok := KindForPlainTool("kubernetes.get_pods")
	assert.False(t, ok)
}
