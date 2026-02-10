package context

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/stretchr/testify/assert"
)

func TestFormatInvestigationContext_Empty(t *testing.T) {
	result := FormatInvestigationContext(nil)
	assert.Contains(t, result, "INVESTIGATION HISTORY")
	assert.Contains(t, result, "Original Investigation")
}

func TestFormatInvestigationContext_VariousEventTypes(t *testing.T) {
	events := []*ent.TimelineEvent{
		{EventType: timelineevent.EventTypeLlmThinking, Content: "Thinking about the problem."},
		{EventType: timelineevent.EventTypeLlmResponse, Content: "I need to check the pods."},
		{EventType: timelineevent.EventTypeLlmToolCall, Content: "k8s.pods_list(ns=default)"},
		{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Root cause: OOM."},
	}

	result := FormatInvestigationContext(events)

	assert.Contains(t, result, "**Internal Reasoning:**")
	assert.Contains(t, result, "Thinking about the problem.")
	assert.Contains(t, result, "**Agent Response:**")
	assert.Contains(t, result, "I need to check the pods.")
	assert.Contains(t, result, "**Tool Call:**")
	assert.Contains(t, result, "k8s.pods_list(ns=default)")
	assert.Contains(t, result, "**Final Analysis:**")
	assert.Contains(t, result, "Root cause: OOM.")
}

func TestFormatInvestigationContext_MCPToolSummary(t *testing.T) {
	events := []*ent.TimelineEvent{
		{EventType: timelineevent.EventTypeMcpToolSummary, Content: "Summary of tool output"},
	}

	result := FormatInvestigationContext(events)
	assert.Contains(t, result, "**Tool Result Summary:**")
	assert.Contains(t, result, "Summary of tool output")
}

func TestFormatInvestigationContext_UnknownEventType(t *testing.T) {
	events := []*ent.TimelineEvent{
		{EventType: timelineevent.EventTypeError, Content: "Something went wrong."},
	}

	result := FormatInvestigationContext(events)
	// Should use default formatting (replacing underscores with spaces)
	assert.Contains(t, result, "error")
	assert.Contains(t, result, "Something went wrong.")
}
