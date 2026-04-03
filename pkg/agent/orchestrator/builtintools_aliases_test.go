package orchestrator

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrchestrationToolAliasesMatchBuiltintools(t *testing.T) {
	assert.Equal(t, builtintools.DispatchAgent, ToolDispatchAgent)
	assert.Equal(t, builtintools.CancelAgent, ToolCancelAgent)
	assert.Equal(t, builtintools.ListAgents, ToolListAgents)
}

// orchestrationToolNames must match orchestrationTools (used by CompositeToolExecutor.Execute);
// each advertised name must also be KindOrchestration in pkg/builtintools for MCP normalization/errors.
func TestOrchestrationToolNamesMatchAdvertisedToolsAndBuiltintools(t *testing.T) {
	fromTools := make(map[string]bool, len(orchestrationTools))
	for _, def := range orchestrationTools {
		fromTools[def.Name] = true
	}
	assert.Equal(t, fromTools, orchestrationToolNames)

	for name := range orchestrationToolNames {
		k, ok := builtintools.KindForPlainTool(name)
		require.True(t, ok, "%q must exist in builtintools.PlainToolKinds", name)
		assert.Equal(t, builtintools.KindOrchestration, k,
			"%q must be KindOrchestration in builtintools for mcp.NormalizeBuiltinPlainToolName / SplitToolName hints", name)
	}
}
