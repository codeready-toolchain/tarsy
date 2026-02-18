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
	expected := sha256.Sum256([]byte(judgeSystemPrompt + judgePromptScore + judgePromptScoreReminder + judgePromptFollowupMissingTools))
	assert.Equal(t, expected, GetCurrentPromptHash(), "hash must match SHA256 of concatenated prompts")
}

func TestGetCurrentPromptHash_ChangesWithPrompts(t *testing.T) {
	// Verify that different prompt content would produce a different hash.
	// We can't mutate the constants, so we compute what a different set of prompts
	// would hash to and confirm it differs from the current hash.
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
	assert.Contains(t, result, "expert evaluator")
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
}

func TestBuildScoringOutputSchemaReminderPrompt(t *testing.T) {
	builder := newBuilderForTest()

	schema := "You MUST end your response with a single line containing ONLY the total score"
	result := builder.BuildScoringOutputSchemaReminderPrompt(schema)

	assert.Contains(t, result, schema, "must include output schema")
	assert.Contains(t, result, "failed to parse", "must include the retry instruction")
	assert.NotContains(t, result, "%[1]s", "no unresolved positional verbs")
}

func TestBuildScoringMissingToolsReportPrompt(t *testing.T) {
	builder := newBuilderForTest()
	result := builder.BuildScoringMissingToolsReportPrompt()

	assert.Equal(t, judgePromptFollowupMissingTools, result)
	assert.Contains(t, result, "missing tool")
}

func TestJudgePromptScore_HasPlaceholders(t *testing.T) {
	require.Contains(t, judgePromptScore, "%[1]s", "must have session context placeholder")
	require.Contains(t, judgePromptScore, "%[2]s", "must have output schema placeholder")
}

func TestJudgePromptScoreReminder_HasPlaceholder(t *testing.T) {
	require.Contains(t, judgePromptScoreReminder, "%[1]s", "must have output schema placeholder")
}

func TestJudgePromptFollowupMissingTools_NoPlaceholders(t *testing.T) {
	assert.NotContains(t, judgePromptFollowupMissingTools, "%[1]s", "must have no placeholders")
	assert.NotContains(t, judgePromptFollowupMissingTools, "%[2]s", "must have no placeholders")
}

