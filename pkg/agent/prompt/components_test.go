package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatAlertSection_WithType(t *testing.T) {
	result := FormatAlertSection("kubernetes", "pod crash detected")
	assert.Contains(t, result, "## Alert Details")
	assert.Contains(t, result, "**Alert Type:** kubernetes")
	assert.Contains(t, result, "<!-- ALERT_DATA_START -->")
	assert.Contains(t, result, "pod crash detected")
	assert.Contains(t, result, "<!-- ALERT_DATA_END -->")
}

func TestFormatAlertSection_WithoutType(t *testing.T) {
	result := FormatAlertSection("", "pod crash detected")
	assert.Contains(t, result, "## Alert Details")
	assert.NotContains(t, result, "Alert Type")
	assert.Contains(t, result, "pod crash detected")
}

func TestFormatAlertSection_EmptyData(t *testing.T) {
	result := FormatAlertSection("kubernetes", "")
	assert.Contains(t, result, "No additional alert data provided")
	assert.NotContains(t, result, "ALERT_DATA_START")
}

func TestFormatAlertSection_BothEmpty(t *testing.T) {
	result := FormatAlertSection("", "")
	assert.Contains(t, result, "## Alert Details")
	assert.Contains(t, result, "No additional alert data provided")
}

func TestFormatRunbookSection_WithContent(t *testing.T) {
	result := FormatRunbookSection("# My Runbook\n\nStep 1: Check pods")
	assert.Contains(t, result, "## Runbook Content")
	assert.Contains(t, result, "<!-- RUNBOOK START -->")
	assert.Contains(t, result, "# My Runbook")
	assert.Contains(t, result, "<!-- RUNBOOK END -->")
	assert.Contains(t, result, "```markdown")
}

func TestFormatRunbookSection_Empty(t *testing.T) {
	result := FormatRunbookSection("")
	assert.Contains(t, result, "No runbook available")
	assert.NotContains(t, result, "RUNBOOK START")
}

func TestFormatChainContext_WithContent(t *testing.T) {
	result := FormatChainContext("Previous agent found OOM issues.")
	assert.Contains(t, result, "## Previous Stage Data")
	assert.Contains(t, result, "Previous agent found OOM issues.")
}

func TestFormatChainContext_Empty(t *testing.T) {
	result := FormatChainContext("")
	assert.Contains(t, result, "No previous stage data is available")
	assert.Contains(t, result, "first stage of analysis")
}

func TestFormatAlertSection_PreservesOpaqueContent(t *testing.T) {
	// Alert data could be JSON, YAML, or plain text — should be preserved as-is
	jsonData := `{"severity":"critical","namespace":"prod","pod":"web-1"}`
	result := FormatAlertSection("kubernetes", jsonData)
	assert.Contains(t, result, jsonData)

	yamlData := "severity: critical\nnamespace: prod\npod: web-1"
	result = FormatAlertSection("", yamlData)
	assert.Contains(t, result, yamlData)
}

func TestFormatRunbookSection_PreservesMarkdown(t *testing.T) {
	markdown := "# Runbook\n\n## Step 1\n\n- Check pods\n- Check logs\n\n```bash\nkubectl get pods\n```"
	result := FormatRunbookSection(markdown)
	// Content should be inside the boundaries
	idx1 := strings.Index(result, "<!-- RUNBOOK START -->")
	idx2 := strings.Index(result, "<!-- RUNBOOK END -->")
	assert.Greater(t, idx1, -1)
	assert.Greater(t, idx2, idx1)
	enclosed := result[idx1+len("<!-- RUNBOOK START -->\n") : idx2]
	assert.Equal(t, markdown, strings.TrimSuffix(enclosed, "\n"))
}
