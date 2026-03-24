package memory

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent/controller"
	"github.com/stretchr/testify/assert"
)

func TestBuildReflectorSystemPrompt(t *testing.T) {
	prompt := buildReflectorSystemPrompt()

	assert.Contains(t, prompt, "memory extraction specialist")
	assert.Contains(t, prompt, "semantic")
	assert.Contains(t, prompt, "episodic")
	assert.Contains(t, prompt, "procedural")
	assert.Contains(t, prompt, "positive")
	assert.Contains(t, prompt, "negative")
	assert.Contains(t, prompt, "neutral")
	assert.Contains(t, prompt, "Do not duplicate skill content")
}

func TestBuildReflectorUserPrompt(t *testing.T) {
	input := ReflectorInput{
		InvestigationContext: "## Alert\nHigh CPU on web-01\n## Timeline\nAgent checked metrics.",
		ScoringResult: controller.ScoringResult{
			TotalScore:            75,
			ScoreAnalysis:         "Good investigation overall.",
			ToolImprovementReport: "Consider using more targeted queries.",
			FailureTags:           []string{"incomplete_evidence"},
		},
		ExistingMemories: []Memory{
			{ID: "mem-1", Content: "Check PgBouncer first", Category: "procedural", Valence: "positive", Confidence: 0.8, SeenCount: 3},
		},
		AlertType: "cpu_high",
		ChainID:   "infra-chain",
	}

	prompt := buildReflectorUserPrompt(input)

	assert.Contains(t, prompt, "High CPU on web-01")
	assert.Contains(t, prompt, "Score: 75/100")
	assert.Contains(t, prompt, "incomplete_evidence")
	assert.Contains(t, prompt, "Good investigation overall.")
	assert.Contains(t, prompt, "Consider using more targeted queries.")
	assert.Contains(t, prompt, "mem-1")
	assert.Contains(t, prompt, "Check PgBouncer first")
	assert.Contains(t, prompt, "cpu_high")
	assert.Contains(t, prompt, "infra-chain")
	assert.Contains(t, prompt, "CREATE")
	assert.Contains(t, prompt, "REINFORCE")
	assert.Contains(t, prompt, "DEPRECATE")
}

func TestBuildReflectorUserPrompt_EmptyMemories(t *testing.T) {
	input := ReflectorInput{
		InvestigationContext: "test context",
		ScoringResult: controller.ScoringResult{
			TotalScore:    50,
			ScoreAnalysis: "test analysis",
		},
	}

	prompt := buildReflectorUserPrompt(input)

	assert.Contains(t, prompt, "[]")
	assert.Contains(t, prompt, "Failure tags: none")
}

func TestBuildReflectorUserPrompt_ContainsOutputSchema(t *testing.T) {
	input := ReflectorInput{
		InvestigationContext: "test",
		ScoringResult: controller.ScoringResult{
			ScoreAnalysis: "test",
		},
	}

	prompt := buildReflectorUserPrompt(input)

	assert.True(t, strings.Contains(prompt, `"create"`))
	assert.True(t, strings.Contains(prompt, `"reinforce"`))
	assert.True(t, strings.Contains(prompt, `"deprecate"`))
	assert.True(t, strings.Contains(prompt, `"memory_id"`))
}
