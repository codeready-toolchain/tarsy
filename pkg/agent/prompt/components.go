package prompt

import "strings"

// FormatAlertSection builds the alert details section.
// alertType may be empty; alertData is opaque text from the session.
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

	// Alert data (opaque text, passed as-is)
	sb.WriteString("### Alert Data\n")
	if alertData == "" {
		sb.WriteString("No additional alert data provided.\n")
		return sb.String()
	}

	sb.WriteString("<!-- ALERT_DATA_START -->\n")
	sb.WriteString(alertData)
	sb.WriteString("\n<!-- ALERT_DATA_END -->\n")

	return sb.String()
}

// FormatRunbookSection builds the runbook section.
// runbookContent is the raw runbook text (markdown typically).
func FormatRunbookSection(runbookContent string) string {
	if runbookContent == "" {
		return "## Runbook Content\nNo runbook available.\n"
	}

	var sb strings.Builder
	sb.WriteString("## Runbook Content\n")
	sb.WriteString("```markdown\n")
	sb.WriteString("<!-- RUNBOOK START -->\n")
	sb.WriteString(runbookContent)
	sb.WriteString("\n<!-- RUNBOOK END -->\n")
	sb.WriteString("```\n")
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
