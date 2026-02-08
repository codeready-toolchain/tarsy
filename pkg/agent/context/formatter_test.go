package context

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/ent"
	"github.com/codeready-toolchain/tarsy/ent/timelineevent"
	"github.com/stretchr/testify/assert"
)

func TestSimpleContextFormatter_Format(t *testing.T) {
	formatter := NewSimpleContextFormatter()

	t.Run("empty events", func(t *testing.T) {
		t.Run("nil slice", func(t *testing.T) {
			result := formatter.Format(nil)
			assert.Equal(t, "", result)
		})

		t.Run("empty non-nil slice", func(t *testing.T) {
			result := formatter.Format([]*ent.TimelineEvent{})
			assert.Equal(t, "", result)
		})
	})

	t.Run("formats events with type labels", func(t *testing.T) {
		events := []*ent.TimelineEvent{
			{EventType: timelineevent.EventTypeFinalAnalysis, Content: "Pod crash analysis"},
		}
		result := formatter.Format(events)
		assert.Contains(t, result, "<!-- STAGE_CONTEXT_START -->")
		assert.Contains(t, result, "### Analysis")
		assert.Contains(t, result, "Pod crash analysis")
		assert.Contains(t, result, "<!-- STAGE_CONTEXT_END -->")
	})

	t.Run("handles multiple events", func(t *testing.T) {
		events := []*ent.TimelineEvent{
			{EventType: timelineevent.EventTypeLlmThinking, Content: "thinking..."},
			{EventType: timelineevent.EventTypeFinalAnalysis, Content: "result"},
		}
		result := formatter.Format(events)
		assert.Contains(t, result, "### LLM Thinking")
		assert.Contains(t, result, "### Analysis")
	})
}
