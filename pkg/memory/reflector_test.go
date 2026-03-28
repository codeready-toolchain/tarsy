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
	assert.Contains(t, prompt, "Extraction Boundaries")
	assert.Contains(t, prompt, "Skill content")
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

// ─────────────────────────────────────────────────────────────
// Feedback Reflector prompt tests
// ─────────────────────────────────────────────────────────────

func TestBuildFeedbackReflectorUserPrompt(t *testing.T) {
	input := FeedbackReflectorInput{
		FeedbackText:         "The investigation missed the PgBouncer saturation.",
		QualityRating:        "partially_accurate",
		InvestigationContext: "## ORIGINAL ALERT\nHigh latency on web-01\n## INVESTIGATION TIMELINE\nAgent checked pods.",
		ExistingMemories: []Memory{
			{ID: "mem-1", Content: "Check PgBouncer first", Category: "procedural", Valence: "positive", Confidence: 0.8, SeenCount: 3},
		},
		AlertType: "latency",
		ChainID:   "infra-chain",
	}

	prompt := buildFeedbackReflectorUserPrompt(input)

	// Investigation context appears before human review
	ctxIdx := strings.Index(prompt, "investigation_context")
	reviewIdx := strings.Index(prompt, "Human Review")
	assert.NotEqual(t, -1, ctxIdx, "prompt must contain investigation_context")
	assert.NotEqual(t, -1, reviewIdx, "prompt must contain Human Review")
	assert.Greater(t, reviewIdx, ctxIdx, "investigation context should appear before human review")

	assert.Contains(t, prompt, "High latency on web-01")
	assert.Contains(t, prompt, "Agent checked pods")
	assert.Contains(t, prompt, "partially_accurate")
	assert.Contains(t, prompt, "PgBouncer saturation")
	assert.Contains(t, prompt, "mem-1")
	assert.Contains(t, prompt, "Check PgBouncer first")
	assert.Contains(t, prompt, "latency")
	assert.Contains(t, prompt, "infra-chain")
	assert.Contains(t, prompt, "CREATE")
	assert.Contains(t, prompt, "REINFORCE")
	assert.Contains(t, prompt, "DEPRECATE")
}

func TestBuildFeedbackReflectorUserPrompt_EmptyMemories(t *testing.T) {
	input := FeedbackReflectorInput{
		FeedbackText:         "Looks good.",
		QualityRating:        "accurate",
		InvestigationContext: "test context",
	}

	prompt := buildFeedbackReflectorUserPrompt(input)
	assert.Contains(t, prompt, "[]")
}

func TestBuildFeedbackReflectorUserPrompt_ContainsOutputSchema(t *testing.T) {
	prompt := buildFeedbackReflectorUserPrompt(FeedbackReflectorInput{
		FeedbackText:         "test",
		InvestigationContext: "test",
	})

	assert.Contains(t, prompt, `"create"`)
	assert.Contains(t, prompt, `"reinforce"`)
	assert.Contains(t, prompt, `"deprecate"`)
}

func TestNewFeedbackReflectorController_NotNil(t *testing.T) {
	ctrl := NewFeedbackReflectorController(FeedbackReflectorInput{
		FeedbackText:         "test",
		InvestigationContext: "test",
	})
	assert.NotNil(t, ctrl)
}

func TestFeedbackReflectorSystemPrompt(t *testing.T) {
	assert.Contains(t, feedbackReflectorSystemPrompt, "memory extraction specialist")
	assert.Contains(t, feedbackReflectorSystemPrompt, "Human feedback is the strongest signal")
	assert.Contains(t, feedbackReflectorSystemPrompt, "semantic")
	assert.Contains(t, feedbackReflectorSystemPrompt, "episodic")
	assert.Contains(t, feedbackReflectorSystemPrompt, "procedural")
	assert.Contains(t, feedbackReflectorSystemPrompt, "Extraction Boundaries")
}
