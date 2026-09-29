package config

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyModel(t *testing.T) {
	tests := []struct {
		model        string
		recognized   bool
		eligible     bool
		major        int
		minor        int
		checkVersion bool
	}{
		{model: "gpt-5.6-sol", recognized: true, eligible: true, major: 5, minor: 6, checkVersion: true},
		{model: "gpt-6", recognized: true, eligible: true, major: 6, checkVersion: true},
		{model: "gpt-5.10", recognized: true, eligible: true, major: 5, minor: 10, checkVersion: true},
		{model: "gpt-5.5", recognized: true, eligible: false, major: 5, minor: 5, checkVersion: true},
		{model: "GPT-5.6", recognized: true, eligible: true},
		{model: "GPT-5.6-Chat", recognized: true, eligible: false},
		{model: "gpt-5.2", recognized: true, eligible: false},
		{model: "gpt-5.6-chat", recognized: true, eligible: false},
		{model: "gpt-6-chat", recognized: true, eligible: false},
		{model: "gpt-5-main-mini", recognized: true, eligible: false},
		{model: "grok-4.6", recognized: true, eligible: true, major: 4, minor: 6, checkVersion: true},
		{model: "grok-4.7", recognized: true, eligible: true},
		{model: "grok-4.5", recognized: true, eligible: false},
		{model: "grok-4", recognized: true, eligible: false, major: 4, checkVersion: true},
		{model: "gemini-3.8-flash", recognized: true, eligible: true, major: 3, minor: 8, checkVersion: true},
		{model: "gemini-3.7-flash", recognized: true, eligible: false},
		{model: "gemini-3.8-flash-image", recognized: true, eligible: false},
		{model: "gemini-3.8-flash-IMAGE", recognized: true, eligible: false},
		{model: "claude-opus-4-8", recognized: true, eligible: true, major: 4, minor: 8, checkVersion: true},
		{model: "claude-opus-4.8", recognized: true, eligible: true, major: 4, minor: 8, checkVersion: true},
		{model: "Claude-Opus-4-8", recognized: true, eligible: true},
		{model: "claude-sonnet-5", recognized: true, eligible: true, major: 5, checkVersion: true},
		{model: "claude-sonnet-5-5", recognized: true, eligible: true, major: 5, minor: 5, checkVersion: true},
		{model: "claude-opus-5-5", recognized: true, eligible: true, major: 5, minor: 5, checkVersion: true},
		{model: "claude-sonnet-5-20260101", recognized: true, eligible: true, major: 5, checkVersion: true},
		{model: "claude-fable-5", recognized: true, eligible: true},
		{model: "claude-sonnet-4-6", recognized: true, eligible: false, major: 4, minor: 6, checkVersion: true},
		{model: "claude-opus-4.6", recognized: true, eligible: false},
		{model: "claude-opus-4-7", recognized: true, eligible: false, major: 4, minor: 7, checkVersion: true},
		{model: "claude-sonnet-4-6-20260217", recognized: true, eligible: false, major: 4, minor: 6, checkVersion: true},
		{model: "company-reasoner", recognized: false, eligible: false},
		{model: "claude-sonnet-latest", recognized: false, eligible: false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got := classifyModel(tt.model)
			assert.Equal(t, tt.recognized, got.recognized)
			assert.Equal(t, tt.eligible, got.eligible)
			if tt.checkVersion {
				assert.Equal(t, tt.major, got.major)
				assert.Equal(t, tt.minor, got.minor)
			}
		})
	}
}

func TestDocumentedReasoningEfforts(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		level  string
		inSet  bool
		hasSet bool
	}{
		{name: "xhigh on opus 4.8", model: "claude-opus-4-8", level: ReasoningEffortXHigh, inSet: true, hasSet: true},
		{name: "xhigh on sonnet 5.5", model: "claude-sonnet-5-5", level: ReasoningEffortXHigh, inSet: true, hasSet: true},
		{name: "xhigh on fable 5", model: "claude-fable-5", level: ReasoningEffortXHigh, inSet: true, hasSet: true},
		{name: "xhigh on sonnet 4.8", model: "claude-sonnet-4-8", level: ReasoningEffortXHigh, inSet: false, hasSet: true},
		{name: "max on sonnet 4.8", model: "claude-sonnet-4-8", level: ReasoningEffortMax, inSet: true, hasSet: true},
		{name: "xhigh on gemini 3.8", model: "gemini-3.8-flash", level: ReasoningEffortXHigh, inSet: false, hasSet: true},
		{name: "high on gemini 3.8", model: "gemini-3.8-flash", level: ReasoningEffortHigh, inSet: true, hasSet: true},
		{name: "max on gpt 5.6", model: "gpt-5.6-sol", level: ReasoningEffortMax, inSet: true, hasSet: true},
		{name: "max on grok 4.6", model: "grok-4.6", level: ReasoningEffortMax, inSet: false, hasSet: true},
		{name: "xhigh on grok 4.6", model: "grok-4.6", level: ReasoningEffortXHigh, inSet: true, hasSet: true},
		{name: "high below claude floor", model: "claude-sonnet-4-6", level: ReasoningEffortHigh, hasSet: false},
		{name: "high on gpt chat", model: "gpt-5.6-chat", level: ReasoningEffortHigh, hasSet: false},
		{name: "high on gemini image", model: "gemini-3.8-flash-image", level: ReasoningEffortHigh, hasSet: false},
		{name: "unrecognized", model: "company-reasoner", level: ReasoningEffortMedium, hasSet: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			levels := documentedReasoningEfforts(classifyModel(tt.model))
			if !tt.hasSet {
				assert.Nil(t, levels)
				return
			}
			assert.Equal(t, tt.inSet, slices.Contains(levels, tt.level))
		})
	}
}

func TestEffectiveReasoningEffort(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		configured string
		want       string
	}{
		{name: "verbatim medium", model: "gemini-2.5-pro", configured: "medium", want: "medium"},
		{name: "untrimmed", model: "gpt-5.6", configured: " high ", want: " high "},
		{name: "unnormalized case", model: "gpt-5.6", configured: "High", want: "High"},
		{name: "explicit below floor", model: "claude-sonnet-4-6", configured: "max", want: "max"},
		{name: "omitted eligible gpt", model: "gpt-5.6", want: ReasoningEffortHigh},
		{name: "omitted eligible gpt-6", model: "gpt-6", want: ReasoningEffortHigh},
		{name: "omitted eligible grok", model: "grok-4.6", want: ReasoningEffortHigh},
		{name: "omitted eligible gemini", model: "gemini-3.8-flash", want: ReasoningEffortHigh},
		{name: "omitted eligible claude 4.8", model: "claude-opus-4-8", want: ReasoningEffortHigh},
		{name: "omitted eligible sonnet 5", model: "claude-sonnet-5", want: ReasoningEffortHigh},
		{name: "omitted eligible sonnet 5.5", model: "claude-sonnet-5-5", want: ReasoningEffortHigh},
		{name: "omitted eligible opus 5.5", model: "claude-opus-5-5", want: ReasoningEffortHigh},
		{name: "omitted legacy gpt", model: "gpt-5.2", want: ""},
		{name: "omitted gpt chat", model: "gpt-5.6-chat", want: ""},
		{name: "omitted gemini image", model: "gemini-3.8-flash-image", want: ""},
		{name: "omitted legacy claude", model: "claude-sonnet-4-6", want: ""},
		{name: "omitted unrecognized", model: "company-reasoner", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EffectiveReasoningEffort(tt.model, tt.configured))
		})
	}
}
