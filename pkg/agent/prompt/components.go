package prompt

import (
	"bytes"
	"encoding/json"
	"strings"
)

// alertTextEnvelope is the prompt-only wrapper for alert text that is not
// already JSON. Field order is the struct order so the same alert encodes
// to the same bytes on every build.
type alertTextEnvelope struct {
	AlertDataType string `json:"alert_data_type"`
	RawAlertText  string `json:"raw_alert_text"`
}

// FormatAlertSection builds the alert details section.
// alertType may be empty. alertData is opaque text from the session.
// Valid JSON is pasted unchanged. Other text is JSON-encoded in this
// prompt copy only; the stored alert is not modified.
func FormatAlertSection(alertType, alertData string) string {
	var sb strings.Builder
	sb.WriteString("## Alert Details\n\n")

	// Alert metadata (if available)
	if alertType != "" {
		sb.WriteString("### Alert Metadata\n")
		sb.WriteString("**Alert Type:** ")
		sb.WriteString(alertType)
		sb.WriteString("\n\n")
	}

	// Alert data. Valid JSON stays byte-for-byte. Other text is encoded
	// so a line in the alert cannot impersonate the next section.
	sb.WriteString("### Alert Data\n")
	if alertData == "" {
		sb.WriteString("No additional alert data provided.\n")
		return sb.String()
	}

	sb.WriteString("<!-- ALERT_DATA_START -->\n")
	sb.WriteString(promptAlertData(alertData))
	sb.WriteString("\n<!-- ALERT_DATA_END -->\n")

	return sb.String()
}

// promptAlertData returns alert text for the user message. Valid JSON,
// including whitespace around one value, is returned unchanged. Anything
// else becomes one JSON object. Encoding this struct into a bytes.Buffer
// does not fail; there is no fallback to the raw alert text.
func promptAlertData(alertData string) string {
	if json.Valid([]byte(alertData)) {
		return alertData
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(alertTextEnvelope{
		AlertDataType: "text",
		RawAlertText:  alertData,
	})
	return strings.TrimSuffix(buf.String(), "\n")
}

// FormatRunbookSection builds the runbook section.
// runbookContent is the raw runbook text (markdown typically).
func FormatRunbookSection(runbookContent string) string {
	if runbookContent == "" {
		return "## Runbook Content\nNo runbook available.\n"
	}

	var sb strings.Builder
	sb.WriteString("## Runbook Content\n")
	sb.WriteString("````markdown\n")
	sb.WriteString("<!-- RUNBOOK START -->\n")
	sb.WriteString(runbookContent)
	sb.WriteString("\n<!-- RUNBOOK END -->\n")
	sb.WriteString("````\n")
	return sb.String()
}

// FormatChainContext wraps pre-formatted previous stage context into a section.
// prevStageContext is the output of ContextFormatter.Format() — already formatted.
func FormatChainContext(prevStageContext string) string {
	if prevStageContext == "" {
		return "## Previous Stage Data\nNo previous stage data is available for this alert. This is the first stage of analysis.\n"
	}

	var sb strings.Builder
	sb.WriteString("## Previous Stage Data\n")
	sb.WriteString(prevStageContext)
	sb.WriteString("\n")
	return sb.String()
}
