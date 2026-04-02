package orchestrator

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/stretchr/testify/assert"
)

func TestOrchestrationToolAliasesMatchBuiltintools(t *testing.T) {
	assert.Equal(t, builtintools.DispatchAgent, ToolDispatchAgent)
	assert.Equal(t, builtintools.CancelAgent, ToolCancelAgent)
	assert.Equal(t, builtintools.ListAgents, ToolListAgents)
}
