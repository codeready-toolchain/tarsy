package prompt

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCurrentPromptHash_Deterministic(t *testing.T) {
	h1 := GetCurrentPromptHash()
	h2 := GetCurrentPromptHash()
	assert.Equal(t, h1, h2, "same prompts must produce the same hash across calls")
}

func TestGetCurrentPromptHash_MatchesExpected(t *testing.T) {
	vocabStr := FormatVocabularyForHash(FailureVocabulary)
	expected := sha256.Sum256([]byte(
		judgeSystemPrompt + judgePromptScore + judgePromptScoreReminder +
			judgePromptFollowupMissingTools + vocabStr,
	))
	assert.Equal(t, expected, GetCurrentPromptHash(), "hash must match SHA256 of concatenated prompts + vocabulary")
}

func TestGetCurrentPromptHash_ChangesWithPrompts(t *testing.T) {
	different := sha256.Sum256([]byte("different prompt content"))
	assert.NotEqual(t, different, GetCurrentPromptHash(), "different prompts must produce a different hash")
}

func TestGetCurrentPromptHash_NonZero(t *testing.T) {
	var zero [32]byte
	assert.NotEqual(t, zero, GetCurrentPromptHash(), "hash must not be the zero value")
}

func TestBuildScoringSystemPrompt(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringSystemPrompt()
	assert.Equal(t, judgeSystemPrompt, result)
	assert.Contains(t, result, "investigation quality evaluator")
	assert.Contains(t, result, "TARSy")
}

func TestBuildScoringInitialPrompt(t *testing.T) {
	builder := newBuilderForTest()

	context := "session investigation context here"
	schema := "output schema instructions here"
	result := builder.BuildScoringInitialPrompt(context, schema)

	assert.Contains(t, result, context, "must include session investigation context")
	assert.Contains(t, result, schema, "must include output schema")
	assert.NotContains(t, result, "%[1]s", "no unresolved positional verbs")
	assert.NotContains(t, result, "%[2]s", "no unresolved positional verbs")
	assert.NotContains(t, result, "%[3]s", "no unresolved positional verbs")
}

func TestBuildScoringInitialPrompt_UsesInvestigationTerminology(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringInitialPrompt("context", "schema")

	assert.Contains(t, result, "investigation")
	assert.NotContains(t, result, "evaluate evaluation tasks")
	assert.NotContains(t, result, "the evaluator")
}

func TestBuildScoringInitialPrompt_NoMissingToolsSection(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringInitialPrompt("context", "schema")

	assert.NotContains(t, result, "IDENTIFYING MISSING TOOLS")
}

func TestBuildScoringInitialPrompt_InjectsVocabulary(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringInitialPrompt("context", "schema")

	for _, ft := range FailureVocabulary {
		assert.Contains(t, result, ft.Term, "rendered prompt must include vocabulary term %q", ft.Term)
		assert.Contains(t, result, ft.Description, "rendered prompt must include vocabulary description for %q", ft.Term)
	}
}

func TestBuildScoringOutputSchemaReminderPrompt(t *testing.T) {
	builder := newBuilderForTest()

	schema := "End your response with the total score"
	result := builder.BuildScoringOutputSchemaReminderPrompt(schema)

	assert.Contains(t, result, schema, "must include output schema")
	assert.Contains(t, result, "could not parse", "must include the retry instruction")
	assert.NotContains(t, result, "%[1]s", "no unresolved positional verbs")
}

func TestBuildScoringMissingToolsReportPrompt(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringMissingToolsReportPrompt()

	assert.Equal(t, judgePromptFollowupMissingTools, result)
	assert.Contains(t, result, "missing tool")
}

func TestBuildScoringMissingToolsReportPrompt_UsesInvestigationTerminology(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringMissingToolsReportPrompt()

	assert.Contains(t, result, "investigation")
}

func TestBuildScoringMissingToolsReportPrompt_HasExistingToolImprovements(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringMissingToolsReportPrompt()

	assert.Contains(t, result, "Existing Tool Improvements")
	assert.Contains(t, result, "Argument clarity")
	assert.Contains(t, result, "Response format")
}

func TestJudgePromptScore_HasPlaceholders(t *testing.T) {
	require.Contains(t, judgePromptScore, "%[1]s", "must have session context placeholder")
	require.Contains(t, judgePromptScore, "%[2]s", "must have output schema placeholder")
	require.Contains(t, judgePromptScore, "%[3]s", "must have vocabulary placeholder")
}

func TestJudgePromptScoreReminder_HasPlaceholder(t *testing.T) {
	require.Contains(t, judgePromptScoreReminder, "%[1]s", "must have output schema placeholder")
}

func TestJudgePromptFollowupMissingTools_NoPlaceholders(t *testing.T) {
	assert.NotContains(t, judgePromptFollowupMissingTools, "%[1]s", "must have no placeholders")
	assert.NotContains(t, judgePromptFollowupMissingTools, "%[2]s", "must have no placeholders")
}

func TestFormatVocabularyForHash_Deterministic(t *testing.T) {
	h1 := FormatVocabularyForHash(FailureVocabulary)
	h2 := FormatVocabularyForHash(FailureVocabulary)
	assert.Equal(t, h1, h2, "same vocabulary must produce the same hash string")
}

func TestFormatVocabularyForHash_ChangesWithVocabulary(t *testing.T) {
	original := FormatVocabularyForHash(FailureVocabulary)
	modified := FormatVocabularyForHash([]FailureTag{
		{"different_tag", "different description"},
	})
	assert.NotEqual(t, original, modified, "different vocabulary must produce different hash string")
}
