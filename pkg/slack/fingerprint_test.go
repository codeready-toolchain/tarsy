package slack

import (
	"testing"

	goslack "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase",
			input:    "Pod CRASHED in namespace",
			expected: "pod crashed in namespace",
		},
		{
			name:     "collapse whitespace",
			input:    "pod   crashed\t\tin\n\nnamespace",
			expected: "pod crashed in namespace",
		},
		{
			name:     "trim",
			input:    "  hello  ",
			expected: "hello",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "mixed case and whitespace",
			input:    "  ALERT:   Pod   nginx-abc   OOMKilled  ",
			expected: "alert: pod nginx-abc oomkilled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeText(tt.input))
		})
	}
}

func TestCollectMessageText(t *testing.T) {
	tests := []struct {
		name     string
		msg      goslack.Message
		expected string
	}{
		{
			name: "text only",
			msg: goslack.Message{
				Msg: goslack.Msg{Text: "hello world"},
			},
			expected: "hello world",
		},
		{
			name: "text with attachment text",
			msg: goslack.Message{
				Msg: goslack.Msg{
					Text: "alert",
					Attachments: []goslack.Attachment{
						{Text: "pod crashed"},
					},
				},
			},
			expected: "alert pod crashed",
		},
		{
			name: "text with attachment fallback",
			msg: goslack.Message{
				Msg: goslack.Msg{
					Text: "alert",
					Attachments: []goslack.Attachment{
						{Fallback: "pod crashed fallback"},
					},
				},
			},
			expected: "alert pod crashed fallback",
		},
		{
			name: "attachment with both text and fallback",
			msg: goslack.Message{
				Msg: goslack.Msg{
					Attachments: []goslack.Attachment{
						{Text: "att text", Fallback: "att fallback"},
					},
				},
			},
			expected: "att text att fallback",
		},
		{
			name:     "empty message",
			msg:      goslack.Message{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collectMessageText(tt.msg))
		})
	}
}
