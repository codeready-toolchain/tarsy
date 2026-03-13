package prompt

import (
	"fmt"
	"strings"
)

// FailureTag defines a failure pattern term and its description for the scoring vocabulary.
type FailureTag struct {
	Term        string
	Description string
}

// FailureVocabulary is the single source of truth for failure pattern terms.
// Used by BuildScoringInitialPrompt() for prompt injection and by
// controller.scanFailureTags() for post-analysis tag extraction.
var FailureVocabulary = []FailureTag{
	{"premature_conclusion", "reached a diagnosis without gathering sufficient evidence"},
	{"missed_available_tool", "a relevant tool was available but not used"},
	{"unsupported_confidence", "stated high confidence without comprehensive evidence"},
	{"incomplete_evidence", "stopped gathering evidence before covering all relevant dimensions"},
	{"hallucinated_evidence", "cited or assumed evidence not present in the investigation data"},
	{"wrong_conclusion", "the final diagnosis is incorrect or contradicted by gathered evidence"},
}

// RenderFailureVocabularySection produces the prompt section injected into the
// scoring prompt via %[3]s. This same output is included in the prompt hash,
// so any change to terms, descriptions, or preamble wording changes the hash.
func RenderFailureVocabularySection(vocab []FailureTag) string {
	var sb strings.Builder
	sb.WriteString("Common failure patterns to watch for (use these terms when applicable, but describe any problems you identify even if they don't match these patterns):\n\n")
	for _, ft := range vocab {
		fmt.Fprintf(&sb, "- %s — %s\n", ft.Term, ft.Description)
	}
	return sb.String()
}
