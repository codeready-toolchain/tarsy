package controller_test

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/memory"
	"github.com/stretchr/testify/assert"
)

// TestMemoryToolNameConsistency guards against silent divergence between
// memory.ToolRecallPastInvestigations and the local copy in tool_execution.go
// (toolNameRecallPastInvestigations). The internal test
// TestExecuteToolCall_ToolTypeClassification asserts the literal
// "recall_past_investigations" maps to ToolTypeMemory; this test asserts the
// memory package exports the same literal. Together they ensure the two
// constants stay in sync even though a direct import is blocked by an import
// cycle.
func TestMemoryToolNameConsistency(t *testing.T) {
	assert.Equal(t, "recall_past_investigations", memory.ToolRecallPastInvestigations,
		"memory.ToolRecallPastInvestigations changed — update toolNameRecallPastInvestigations in tool_execution.go to match")
}
