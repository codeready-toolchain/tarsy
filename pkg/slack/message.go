package slack

import (
	"fmt"

	goslack "github.com/slack-go/slack"
)

const maxBlockTextLength = 2900

var statusEmoji = map[string]string{
	"completed": ":white_check_mark:",
	"failed":    ":x:",
	"timed_out": ":hourglass:",
	"cancelled": ":no_entry_sign:",
}

var statusLabel = map[string]string{
	"completed": "Analysis Complete",
	"failed":    "Analysis Failed",
	"timed_out": "Analysis Timed Out",
	"cancelled": "Analysis Cancelled",
}

func sessionURL(sessionID, dashboardURL string) string {
	return fmt.Sprintf("%s/sessions/%s", dashboardURL, sessionID)
}

// BuildStartedMessage creates Block Kit blocks for a session start notification.
func BuildStartedMessage(sessionID, dashboardURL string) []goslack.Block {
	url := sessionURL(sessionID, dashboardURL)
	text := fmt.Sprintf(":arrows_counterclockwise: *Processing started* — this may take a few minutes.\n<%s|View in Dashboard>", url)

	return []goslack.Block{
		goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, text, false, false),
			nil, nil,
		),
	}
}

// BuildTerminalMessage creates Block Kit blocks for a terminal session notification.
func BuildTerminalMessage(input SessionCompletedInput, dashboardURL string) []goslack.Block {
	emoji := statusEmoji[input.Status]
	if emoji == "" {
		emoji = ":question:"
	}
	label := statusLabel[input.Status]
	if label == "" {
		label = "Analysis " + input.Status
	}

	var blocks []goslack.Block

	if input.Status == "completed" {
		content := input.ExecutiveSummary
		if content == "" {
			content = input.FinalAnalysis
		}

		if content != "" {
			headerText := fmt.Sprintf("%s *%s*", emoji, label)
			blocks = append(blocks, goslack.NewSectionBlock(
				goslack.NewTextBlockObject(goslack.MarkdownType, headerText, false, false),
				nil, nil,
			))
			blocks = append(blocks, goslack.NewSectionBlock(
				goslack.NewTextBlockObject(goslack.MarkdownType, truncateForSlack(content), false, false),
				nil, nil,
			))
		} else {
			headerText := fmt.Sprintf("%s *%s*", emoji, label)
			blocks = append(blocks, goslack.NewSectionBlock(
				goslack.NewTextBlockObject(goslack.MarkdownType, headerText, false, false),
				nil, nil,
			))
		}
	} else {
		headerText := fmt.Sprintf("%s *%s*", emoji, label)
		if input.ErrorMessage != "" {
			headerText += fmt.Sprintf("\n\n*Error:*\n%s", truncateForSlack(input.ErrorMessage))
		}
		blocks = append(blocks, goslack.NewSectionBlock(
			goslack.NewTextBlockObject(goslack.MarkdownType, headerText, false, false),
			nil, nil,
		))
	}

	url := sessionURL(input.SessionID, dashboardURL)
	buttonText := "View Full Analysis"
	if input.Status != "completed" {
		buttonText = "View Details"
	}

	btn := goslack.NewButtonBlockElement("", "", goslack.NewTextBlockObject(goslack.PlainTextType, buttonText, false, false))
	btn.URL = url
	blocks = append(blocks, goslack.NewActionBlock("", btn))

	return blocks
}

func truncateForSlack(text string) string {
	if len(text) <= maxBlockTextLength {
		return text
	}
	return text[:maxBlockTextLength] + "\n\n_... (truncated — view full analysis in dashboard)_"
}
