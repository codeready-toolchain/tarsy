package controller_test

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/builtintools"
	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/stretchr/testify/assert"
)

// TestMemoryToolNameConsistency ensures memory package exports match pkg/builtintools.
func TestMemoryToolNameConsistency(t *testing.T) {
	assert.Equal(t, builtintools.RecallPastInvestigations, memory.ToolRecallPastInvestigations)
	assert.Equal(t, builtintools.SearchPastSessions, memory.ToolSearchPastSessions)
}
