package prompt

import "fmt"

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

// FormatVocabularyForHash produces a deterministic string from the vocabulary
// slice for inclusion in prompt hash computation. Any change to terms or
// descriptions changes the hash.
func FormatVocabularyForHash(vocab []FailureTag) string {
	var s string
	for _, ft := range vocab {
		s += fmt.Sprintf("%s:%s;", ft.Term, ft.Description)
	}
	return s
}
