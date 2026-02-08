// Package context provides formatters for passing information between stages.
package context

import (
	"fmt"
	"strings"

	"github.com/codeready-toolchain/tarsy/ent"
)

// ContextFormatter transforms the output of one stage into text
// for consumption by the next stage.
type ContextFormatter interface {
	// Format converts a list of timeline events into a context string.
	Format(events []*ent.TimelineEvent) string
}

// SimpleContextFormatter produces a human-readable summary with
// type-aware labels and HTML comment boundaries.
type SimpleContextFormatter struct{}

// NewSimpleContextFormatter creates a new simple formatter.
func NewSimpleContextFormatter() *SimpleContextFormatter {
	return &SimpleContextFormatter{}
}

// Format converts timeline events into a formatted context string.
func (f *SimpleContextFormatter) Format(events []*ent.TimelineEvent) string {
	if len(events) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("<!-- STAGE_CONTEXT_START -->\n")

	for _, event := range events {
		label := eventTypeLabel(string(event.EventType))
		sb.WriteString(fmt.Sprintf("### %s\n\n", label))
		sb.WriteString(event.Content)
		sb.WriteString("\n\n")
	}

	sb.WriteString("<!-- STAGE_CONTEXT_END -->")
	return sb.String()
}

func eventTypeLabel(eventType string) string {
	switch eventType {
	case "llm_thinking":
		return "LLM Thinking"
	case "llm_response":
		return "LLM Response"
	case "tool_call":
		return "Tool Call"
	case "tool_result":
		return "Tool Result"
	case "final_analysis":
		return "Analysis"
	case "executive_summary":
		return "Executive Summary"
	default:
		return strings.ReplaceAll(eventType, "_", " ")
	}
}
