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

func (m *mockScoringPromptBuilder) BuildScoringToolImprovementReportPrompt() string {
	return "List tool improvements."
}

func newScoringExecCtx(t *testing.T, llm agent.LLMClient) *agent.ExecutionContext {
	t.Helper()
	execCtx := newTestExecCtx(t, llm, nil)
	execCtx.PromptBuilder = &mockScoringPromptBuilder{}
	return execCtx
}

func TestScoringController_Run(t *testing.T) {
	t.Run("happy path: score + tool improvement report", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1: Score evaluation
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Logical Flow: 20/25\nConsistency: 18/25\nTool Relevance: 15/25\nSynthesis: 14/25\n67"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Turn 2: Tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No critical tool improvements identified."},
					&agent.UsageChunk{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "session investigation data")
		require.NoError(t, err)
		require.NotNil(t, result)

		assert.Equal(t, agent.ExecutionStatusCompleted, result.Status)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 67, sr.TotalScore)
		assert.Equal(t, "Logical Flow: 20/25\nConsistency: 18/25\nTool Relevance: 15/25\nSynthesis: 14/25", sr.ScoreAnalysis)
		assert.Equal(t, "No critical tool improvements identified.", sr.ToolImprovementReport)
		assert.Empty(t, sr.FailureTags)

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
				// Turn 2: Tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No tool improvements."},
					&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 67, sr.TotalScore)

		// 3 LLM calls: initial + 1 extraction retry + tool improvement report
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
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to extract score after retries")
		assert.Equal(t, 6, mock.callCount) // 1 initial + maxExtractionRetries (5) retries
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
		_, err := ctrl.Run(ctx, newScoringExecCtx(t, mock), "data")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)

		// LLM may never be called — DB message storage fails first on cancelled ctx
		assert.LessOrEqual(t, mock.callCount, 1)
	})

	t.Run("context deadline propagates immediately", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: context.DeadlineExceeded},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)

		// Should not retry
		assert.Equal(t, 1, mock.callCount)
	})

	t.Run("nil PromptBuilder returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(t, &mockLLMClient{})
		execCtx.PromptBuilder = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "PromptBuilder is nil")
	})

	t.Run("nil LLMClient returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(t, nil)
		execCtx.LLMClient = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "LLMClient is nil")
	})

	t.Run("nil execCtx returns error", func(t *testing.T) {
		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), nil, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "execCtx is nil")
	})

	t.Run("nil execCtx.Config returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(t, &mockLLMClient{})
		execCtx.Config = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "execCtx.Config is nil")
	})

	t.Run("nil execCtx.Config.LLMProvider returns error", func(t *testing.T) {
		execCtx := newScoringExecCtx(t, &mockLLMClient{})
		execCtx.Config.LLMProvider = nil

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "execCtx.Config.LLMProvider is nil")
	})

	t.Run("LLM failure during extraction retry", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{&agent.TextChunk{Content: "no score here"}}},
				{err: fmt.Errorf("LLM unavailable")},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scoring extraction retry LLM call failed")
		assert.Equal(t, 2, mock.callCount)
	})

	t.Run("LLM failure during tool improvement report turn", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Analysis\n50"},
				}},
				{err: fmt.Errorf("LLM unavailable")},
			},
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tool improvement report LLM call failed")
		assert.Equal(t, 2, mock.callCount)
	})

	t.Run("fallback: Turn 1 fails, retries with fallback provider", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				{err: &PartialOutputError{
					Cause: fmt.Errorf("provider down"), Code: LLMErrorMaxRetries,
				}},
				// After fallback: Turn 1 succeeds
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Good analysis\n75"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Turn 2: tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No tool improvements."},
					&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
				}},
			},
		}

		execCtx := newScoringExecCtx(t, mock)
		execCtx.Config.LLMProviderName = "primary"
		execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
			makeFallbackEntry("fallback-scoring", config.LLMBackendLangChain, "fallback-model"),
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), execCtx, "session data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 75, sr.TotalScore)
		assert.Equal(t, 3, mock.callCount)
		assert.Equal(t, "fallback-scoring", execCtx.Config.LLMProviderName)
	})

	t.Run("fallback: Turn 2 fails, retries with fallback provider", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1 succeeds
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Score analysis\n60"},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Turn 2 fails
				{err: &PartialOutputError{
					Cause: fmt.Errorf("provider overloaded"), Code: LLMErrorMaxRetries,
				}},
				// After fallback: Turn 2 succeeds
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Improvement: tool-x"},
					&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
				}},
			},
		}

		execCtx := newScoringExecCtx(t, mock)
		execCtx.Config.LLMProviderName = "primary"
		execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
			makeFallbackEntry("fallback-scoring", config.LLMBackendLangChain, "fallback-model"),
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), execCtx, "session data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 60, sr.TotalScore)
		assert.Equal(t, "Improvement: tool-x", sr.ToolImprovementReport)
		assert.Equal(t, 3, mock.callCount)
	})

	t.Run("fallback exhausted: all providers fail", func(t *testing.T) {
		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: &PartialOutputError{
					Cause: fmt.Errorf("primary down"), Code: LLMErrorMaxRetries,
				}},
				{err: &PartialOutputError{
					Cause: fmt.Errorf("fallback down"), Code: LLMErrorMaxRetries,
				}},
			},
		}

		execCtx := newScoringExecCtx(t, mock)
		execCtx.Config.LLMProviderName = "primary"
		execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
			makeFallbackEntry("fallback-scoring", config.LLMBackendLangChain, "fallback-model"),
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(context.Background(), execCtx, "data")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scoring LLM call failed")
		assert.Equal(t, 2, mock.callCount)
	})

	t.Run("fallback: extraction retry fails, retries with fallback provider", func(t *testing.T) {
		mock := &mockLLMClient{
			capture: true,
			responses: []mockLLMResponse{
				// Turn 1: no valid score on last line
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "I think the score is seventy."},
					&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
				}},
				// Extraction retry: provider error
				{err: &PartialOutputError{
					Cause: fmt.Errorf("provider crashed"), Code: LLMErrorMaxRetries,
				}},
				// After fallback: extraction retry succeeds
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "70"},
					&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
				}},
				// Turn 2: tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "No tool improvements."},
					&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
				}},
			},
		}

		execCtx := newScoringExecCtx(t, mock)
		execCtx.Config.LLMProviderName = "primary"
		execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
			makeFallbackEntry("fallback-scoring", config.LLMBackendLangChain, "fallback-model"),
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), execCtx, "session data")
		require.NoError(t, err)

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 70, sr.TotalScore)
		assert.Equal(t, 4, mock.callCount)
		assert.Equal(t, "fallback-scoring", execCtx.Config.LLMProviderName)
	})

	t.Run("fallback: context cancelled during retry loop exits immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		mock := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: &PartialOutputError{
					Cause: fmt.Errorf("provider error"), Code: LLMErrorMaxRetries,
				}},
			},
			onGenerate: func(callIndex int) {
				if callIndex == 0 {
					cancel()
				}
			},
		}

		execCtx := newScoringExecCtx(t, mock)
		execCtx.Config.LLMProviderName = "primary"
		execCtx.Config.ResolvedFallbackProviders = []agent.ResolvedFallbackEntry{
			makeFallbackEntry("fallback-scoring", config.LLMBackendLangChain, "fallback-model"),
		}

		ctrl := NewScoringController()
		_, err := ctrl.Run(ctx, execCtx, "data")
		require.Error(t, err)
		assert.Equal(t, 1, mock.callCount)
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
				// Turn 2: Tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "None."},
					&agent.UsageChunk{InputTokens: 50, OutputTokens: 10, TotalTokens: 60},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "data")
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
				// Turn 2: Tool improvement report
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Improvement: tool-a, tool-b"},
					&agent.UsageChunk{InputTokens: 200, OutputTokens: 40, TotalTokens: 240},
				}},
			},
		}

		ctrl := NewScoringController()
		result, err := ctrl.Run(context.Background(), newScoringExecCtx(t, mock), "session data")
		require.NoError(t, err)

		// Turn 2 should have full conversation: system + user + assistant + user(tool improvement report)
		require.Len(t, mock.capturedInputs, 2)
		turn2Messages := mock.capturedInputs[1].Messages
		assert.Len(t, turn2Messages, 4)
		assert.Equal(t, agent.RoleSystem, turn2Messages[0].Role)
		assert.Equal(t, agent.RoleUser, turn2Messages[1].Role)
		assert.Equal(t, agent.RoleAssistant, turn2Messages[2].Role)
		assert.Equal(t, "Score analysis\n45", turn2Messages[2].Content)
		assert.Equal(t, agent.RoleUser, turn2Messages[3].Role)
		assert.Contains(t, turn2Messages[3].Content, "tool improvements")

		var sr ScoringResult
		require.NoError(t, json.Unmarshal([]byte(result.FinalAnalysis), &sr))
		assert.Equal(t, 45, sr.TotalScore)
		assert.Equal(t, "Improvement: tool-a, tool-b", sr.ToolImprovementReport)
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

	t.Run("score validation: negative value rejected", func(t *testing.T) {
		_, _, err := extractScore("Analysis\n-1")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "out of valid range")
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

	t.Run("score validation: trailing number in text rejected", func(t *testing.T) {
		_, _, err := extractScore("Analysis\nTotal: 67")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no numeric score found")
	})

	t.Run("score validation: empty response", func(t *testing.T) {
		_, _, err := extractScore("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty response")
	})
}

func TestScanFailureTags(t *testing.T) {
	tests := []struct {
		name     string
		analysis string
		expected []string
	}{
		{
			name:     "empty analysis",
			analysis: "",
			expected: []string{},
		},
		{
			name:     "no vocabulary terms",
			analysis: "The investigation was thorough and well-executed.",
			expected: []string{},
		},
		{
			name:     "single term",
			analysis: "The agent showed premature_conclusion by stopping after one tool call.",
			expected: []string{"premature_conclusion"},
		},
		{
			name:     "multiple terms in vocabulary order",
			analysis: "The agent showed incomplete_evidence and missed_available_tool — get_resource_limits was never called.",
			expected: []string{"missed_available_tool", "incomplete_evidence"},
		},
		{
			name:     "partial match not matched",
			analysis: "The conclusion was partially correct.",
			expected: []string{},
		},
		{
			name:     "all terms present",
			analysis: "premature_conclusion, missed_available_tool, unsupported_confidence, incomplete_evidence, hallucinated_evidence, wrong_conclusion",
			expected: []string{"premature_conclusion", "missed_available_tool", "unsupported_confidence", "incomplete_evidence", "hallucinated_evidence", "wrong_conclusion"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := scanFailureTags(tt.analysis)
			assert.Equal(t, tt.expected, tags)
		})
	}
}
