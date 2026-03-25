package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseReflectorResponse(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOK     bool
		wantCreate int
		wantReinf  int
		wantDeprec int
	}{
		{
			name:       "strict JSON",
			input:      `{"create":[{"content":"test","category":"semantic","valence":"positive"}],"reinforce":[],"deprecate":[]}`,
			wantOK:     true,
			wantCreate: 1,
		},
		{
			name:       "markdown fenced with lang tag",
			input:      "Here is the result:\n```json\n{\"create\":[{\"content\":\"fenced\",\"category\":\"episodic\",\"valence\":\"negative\"}],\"reinforce\":[],\"deprecate\":[]}\n```\n",
			wantOK:     true,
			wantCreate: 1,
		},
		{
			name:       "markdown fenced without lang tag",
			input:      "```\n{\"create\":[],\"reinforce\":[],\"deprecate\":[]}\n```",
			wantOK:     true,
			wantCreate: 0,
		},
		{
			name:      "bracket extraction from prose",
			input:     "Let me analyze this.\n\n{\"create\":[],\"reinforce\":[{\"memory_id\":\"mem-123\"}],\"deprecate\":[]}\n\nDone.",
			wantOK:    true,
			wantReinf: 1,
		},
		{
			name:       "all action types",
			input:      `{"create":[{"content":"x","category":"procedural","valence":"neutral"}],"reinforce":[{"memory_id":"m1"},{"memory_id":"m2"}],"deprecate":[{"memory_id":"m3","reason":"old"}]}`,
			wantOK:     true,
			wantCreate: 1, wantReinf: 2, wantDeprec: 1,
		},
		{
			name:       "nested braces in content — balanced",
			input:      `Some text {  "create": [{"content": "handle {ns} properly", "category": "semantic", "valence": "positive"}], "reinforce": [], "deprecate": [] } more text`,
			wantOK:     true,
			wantCreate: 1,
		},
		{
			name:       "unbalanced brace in string value",
			input:      `Prose { "create": [{"content": "error } in logs", "category": "semantic", "valence": "negative"}], "reinforce": [], "deprecate": [] } end`,
			wantOK:     true,
			wantCreate: 1,
		},
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
		{
			name:   "plain prose — unparseable",
			input:  "I couldn't extract any memories from this investigation.",
			wantOK: false,
		},
		{
			name:   "empty arrays — parseable but empty",
			input:  `{"create":[],"reinforce":[],"deprecate":[]}`,
			wantOK: true,
		},
		{
			name:   "whitespace only",
			input:  "   \n\t  ",
			wantOK: false,
		},
		{
			name:   "incomplete JSON object",
			input:  `{"create":[{"content":"truncated`,
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := ParseReflectorResponse(tt.input)
			assert.Equal(t, tt.wantOK, ok)
			require.NotNil(t, result, "result should never be nil")

			assert.Len(t, result.Create, tt.wantCreate)
			assert.Len(t, result.Reinforce, tt.wantReinf)
			assert.Len(t, result.Deprecate, tt.wantDeprec)
		})
	}
}
