package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockScoringPromptBuilder implements agent.PromptBuilder for scoring tests.
// Only the scoring methods are needed; others panic if called.
type mockScoringPromptBuilder struct{}

func (m *mockScoringPromptBuilder) BuildFunctionCallingMessages(_ *agent.ExecutionContext, _ string) []agent.ConversationMessage {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildSynthesisMessages(_ *agent.ExecutionContext, _ string) []agent.ConversationMessage {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildForcedConclusionPrompt(_ int) string {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildMCPSummarizationSystemPrompt(_, _ string, _ int) string {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildMCPSummarizationUserPrompt(_, _, _, _ string) string {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildExecutiveSummarySystemPrompt() string {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildExecutiveSummaryUserPrompt(_ string) string {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) MCPServerRegistry() *config.MCPServerRegistry {
	panic("unexpected call")
}

func (m *mockScoringPromptBuilder) BuildScoringSystemPrompt() string {
	return "You are a scoring judge."
}

func (m *mockScoringPromptBuilder) BuildScoringInitialPrompt(sessionCtx, schema string) string {
	return fmt.Sprintf("Evaluate this session:\n%s\n%s", sessionCtx, schema)
}

func (m *mockScoringPromptBuilder) BuildScoringOutputSchemaReminderPrompt(schema string) string {
	return fmt.Sprintf("Reminder: %s", schema)
}

func (m *mockScoringPromptBuilder) BuildScoringMissingToolsReportPrompt() string {
	return "List missing tools."
}

func newScoringExecCtx(llm agent.LLMClient) *agent.ExecutionContext {
	return &agent.ExecutionContext{
		SessionID:   "score-session",
		ExecutionID: "score-exec",
		AgentName:   "scorer",
		AgentIndex:  1,
		Config: &agent.ResolvedAgentConfig{
			AgentName:   "scorer",
			LLMProvider: &config.LLMProviderConfig{Model: "test-model"},
		},
		LLMClient:     llm,
		PromptBuilder: &mockScoringPromptBuilder{},
		// Services intentionally nil — ScoringController doesn't use them
	}
}

func TestScoringController_Run(t *testing.T) {
	t.Run("happy path: score + missing tools", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1: Score evaluation
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Logical Flow: 20/25\nConsistency: 18/25\nTool Relevance: 15/25\nSynthesis: 14/25\n67"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Turn 2: Missing tools
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No critical missing tools identified."},
					&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "session investigation data")
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, agent.ExecutionStatusCompleted, result.Status)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 67, sr.TotalScore)
		assert.Equal(t, "Logical Flow: 20/25\nConsistency: 18/25\nTool Relevance: 15/25\nSynthesis: 14/25", sr.ScoreAnalysis)
		assert.Equal(t, "No critical missing tools identified.", sr.MissingToolsAnalysis)

		// Verify token accumulation
		assert.Equal(t, 300, result.TokensUsed.InputTokens)
		assert.Equal(t, 80, result.TokensUsed.OutputTokens)
		assert.Equal(t, 380, result.TokensUsed.TotalTokens)

		// Verify 2 LLM calls
		assert.Equal(t, 2, mock.callCount)
	})

	t.Run("extraction retry: first response lacks score, second succeeds", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1: No valid score
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "I think the score is around sixty-seven."},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Extraction retry 1: Valid score
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "67"},
					&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
				}},
				// Turn 2: Missing tools
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No missing tools."},
					&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 67, sr.TotalScore)

		// 3 LLM calls: initial + 1 extraction retry + missing tools
		assert.Equal(t, 3, mock.callCount)

		// Verify conversation grew: extraction retry should have 4 messages
		// (system + user + assistant + reminder)
		assert.Len(t, mock.capturedInputs[1].Messages, 4)
	})

	t.Run("extraction retry exhaustion: all retries fail", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "no score"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "still no score"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "zero"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "zip"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "zilch"}}},
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "nada"}}},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to extract score after retries")
	})

	t.Run("context cancellation propagates immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel before running

		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: context.Canceled},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(ctx, newScoringExecCtx(mock), "data")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)

		// Should not retry
		assert.Equal(t, 1, mock.callCount)
	})

	t.Run("context deadline propagates immediately", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: context.DeadlineExceeded},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "data")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)

		// Should not retry
		assert.Equal(t, 1, mock.callCount)
	})

	t.Run("nil PromptBuilder returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(&mockLLMClient{})
		execCtx.PromptBuilder = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PromptBuilder is nil")
	})

	t.Run("nil LLMClient returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(nil)
		execCtx.LLMClient = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LLMClient is nil")
	})

	t.Run("thinking chunks are collected but don't affect score extraction", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				// Turn 1: Thinking + text with score
				{chunks: []agent.Chunk{
					&agent.ThinkingChunk{Content: "Let me think about this score..."},
					&agent.TextChunk{Content: "My analysis\n80"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150, ThinkingTokens: 30},
				}},
				// Turn 2: Missing tools
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "None."},
					&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 80, sr.TotalScore)
		assert.Equal(t, "My analysis", sr.ScoreAnalysis)

		// Thinking tokens accumulated
		assert.Equal(t, 30, result.TokensUsed.ThinkingTokens)
	})

	t.Run("multi-turn conversation integrity", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1: Score
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Score analysis\n45"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Turn 2: Missing tools
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Missing: tool-a, tool-b"},
					&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(mock), "session data")
		require.NoError(t, err)

		// Turn 2 should have full conversation: system + user + assistant + user(missing tools)
		require.Len(t, mock.capturedInputs, 2)
		turn2Messages := mock.capturedInputs[1].Messages
		assert.Len(t, turn2Messages, 4)
		assert.Equal(t, agent.RoleSystem, turn2Messages[0].Role)
		assert.Equal(t, agent.RoleUser, turn2Messages[1].Role)
		assert.Equal(t, agent.RoleAssistant, turn2Messages[2].Role)
		assert.Equal(t, "Score analysis\n45", turn2Messages[2].Content)
		assert.Equal(t, agent.RoleUser, turn2Messages[3].Role)
		assert.Contains(t, turn2Messages[3].Content, "missing tools")

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 45, sr.TotalScore)
		assert.Equal(t, "Missing: tool-a, tool-b", sr.MissingToolsAnalysis)
	})
}

func TestScoringController_extractScore(t *testing.T) {
	t.Run("score extraction: clean number", func(t *testing.T) {
		score, analysis, err := extractScore("Some analysis text\n42")
		require.NoError(t, err)
		assert.Equal(t, 42, score)
		assert.Equal(t, "Some analysis text", analysis)
	})

	t.Run("score extraction: trailing whitespace", func(t *testing.T) {
		score, analysis, err := extractScore("Analysis\n100   ")
		require.NoError(t, err)
		assert.Equal(t, 100, score)
		assert.Equal(t, "Analysis", analysis)
	})

	t.Run("score extraction: zero score", func(t *testing.T) {
		score, _, err := extractScore("Bad work\n0")
		require.NoError(t, err)
		assert.Equal(t, 0, score)
	})

	t.Run("score extraction: multi-line analysis", func(t *testing.T) {
		score, analysis, err := extractScore("Line 1\nLine 2\nLine 3\n55")
		require.NoError(t, err)
		assert.Equal(t, 55, score)
		assert.Equal(t, "Line 1\nLine 2\nLine 3", analysis)
	})

	t.Run("score extraction: single line (score only)", func(t *testing.T) {
		score, analysis, err := extractScore("75")
		require.NoError(t, err)
		assert.Equal(t, 75, score)
		assert.Equal(t, "", analysis)
	})

	t.Run("score validation: out of range 101", func(t *testing.T) {
		_, _, err := extractScore("Analysis\n101")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of valid range")
	})

	t.Run("score validation: negative sign supported", func(t *testing.T) {
		score, _, err := extractScore("Analysis\n-1")
		require.NoError(t, err)
		assert.Equal(t, -1, score)
	})

	t.Run("score validation: explicit positive sign supported", func(t *testing.T) {
		score, _, err := extractScore("Analysis\n+1")
		require.NoError(t, err)
		assert.Equal(t, 1, score)
	})

	t.Run("score validation: too large 999", func(t *testing.T) {
		_, _, err := extractScore("Analysis\n999")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of valid range")
	})

	t.Run("score validation: non-numeric last line", func(t *testing.T) {
		_, _, err := extractScore("Analysis\nno score here")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no numeric score found")
	})

	t.Run("score validation: empty response", func(t *testing.T) {
		_, _, err := extractScore("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty response")
	})
}
