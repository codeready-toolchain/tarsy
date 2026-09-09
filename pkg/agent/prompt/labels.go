package prompt

import (
	"strings"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// Layer 1 is TARSy-owned: encode from the closed list; do not invent a verdict.
const sessionLabelLayer1 = `Encode the session using only labels from the map below. Do not invent labels or a new verdict. The 1-4 line executive summary body stays facts-only.`

// Used when the active map's Instructions is empty (including a YAML builtin
// override that omitted instructions). Not the Go builtin attention essay.
const sessionLabelLayer2Generic = `Apply the label(s) whose descriptions best match the analysis. If none match, omit the LABELS line.`

const sessionLabelLayer4Exclusive = `If one label applies, last line exactly:
LABELS: <one exact label from the list>
Copy the label as written. Nothing after that line.
If none apply, do not add a LABELS line.`

const sessionLabelLayer4Multi = `If any labels apply, last line exactly:
LABELS: <comma-separated unique labels from the list>
Only labels from the list. Nothing after that line.
If none apply, do not add a LABELS line.`

// FormatSessionLabelLayers renders LABELS prompt layers 1–4 for the active map.
// Not wired into BuildExecutiveSummaryUserPrompt yet (PR 2).
func FormatSessionLabelLayers(m config.LabelMap) string {
	var sb strings.Builder
	sb.WriteString(sessionLabelLayer1)
	sb.WriteString("\n\n")
	if strings.TrimSpace(m.Instructions) != "" {
		sb.WriteString(m.Instructions)
	} else {
		sb.WriteString(sessionLabelLayer2Generic)
	}
	sb.WriteString("\n\n")
	sb.WriteString("Labels (in order):\n")
	for _, spec := range m.Labels {
		sb.WriteString("- ")
		sb.WriteString(spec.Label)
		sb.WriteString(":\n")
		for line := range strings.SplitSeq(spec.Description, "\n") {
			if line != "" {
				sb.WriteString("  ")
				sb.WriteString(line)
			}
			sb.WriteByte('\n')
		}
	}
	sb.WriteByte('\n')
	if m.Multi {
		sb.WriteString(sessionLabelLayer4Multi)
	} else {
		sb.WriteString(sessionLabelLayer4Exclusive)
	}
	return sb.String()
}
