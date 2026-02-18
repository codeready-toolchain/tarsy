package slack

import (
	"regexp"
	"strings"

	goslack "github.com/slack-go/slack"
)

var whitespaceRe = regexp.MustCompile(`\s+`)

func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = whitespaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func collectMessageText(msg goslack.Message) string {
	var parts []string
	if msg.Text != "" {
		parts = append(parts, msg.Text)
	}
	for _, att := range msg.Attachments {
		if att.Text != "" {
			parts = append(parts, att.Text)
		}
		if att.Fallback != "" {
			parts = append(parts, att.Fallback)
		}
	}
	return strings.Join(parts, " ")
}
