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
		{EventType: timelineevent.EventTypeToolResult, Content: "Pod web-1 is CrashLoopBackOff"},
		{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Root cause: OOM."},
	}

	result := FormatInvestigationContext(events)

	assert.Contains(t, result, "**Internal Reasoning:**")
	assert.Contains(t, result, "Thinking about the problem.")
	assert.Contains(t, result, "**Agent Response:**")
	assert.Contains(t, result, "I need to check the pods.")
	assert.Contains(t, result, "**Tool Call:**")
	assert.Contains(t, result, "k8s.pods_list(ns=default)")
	assert.Contains(t, result, "**Observation:**")
	assert.Contains(t, result, "Pod web-1 is CrashLoopBackOff")
	assert.Contains(t, result, "**Final Analysis:**")
	assert.Contains(t, result, "Root cause: OOM.")
}

func TestFormatInvestigationContext_MCPToolCall(t *testing.T) {
	events := []*ent.TimelineEvent{
		{EventType: timelineevent.EventTypeMcpToolCall, Content: "mcp_call(param=value)"},
	}

	result := FormatInvestigationContext(events)
	assert.Contains(t, result, "**Tool Call:**")
	assert.Contains(t, result, "mcp_call(param=value)")
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
