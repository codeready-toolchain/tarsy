package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// collectStream tests
// ============================================================================

func TestCollectStream(t *testing.T) {
	t.Run("text chunks concatenated", func(t *testing.T) {
		ch := make(chan agent.Chunk, 3)
		ch <- &agent.TextChunk{Content: "Hello "}
		ch <- &agent.TextChunk{Content: "world"}
		ch <- &agent.TextChunk{Content: "!"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Hello world!", resp.Text)
	})

	t.Run("thinking chunks concatenated", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.ThinkingChunk{Content: "Let me think "}
		ch <- &agent.ThinkingChunk{Content: "about this."}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Let me think about this.", resp.ThinkingText)
	})

	t.Run("tool call chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.ToolCallChunk{CallID: "c1", Name: "k8s.pods", Arguments: "{}"}
		ch <- &agent.ToolCallChunk{CallID: "c2", Name: "k8s.logs", Arguments: "{\"pod\": \"web\"}"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.ToolCalls, 2)
		assert.Equal(t, "c1", resp.ToolCalls[0].ID)
		assert.Equal(t, "k8s.pods", resp.ToolCalls[0].Name)
		assert.Equal(t, "c2", resp.ToolCalls[1].ID)
	})

	t.Run("usage chunk captured", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.UsageChunk{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ThinkingTokens: 5}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.NotNil(t, resp.Usage)
		assert.Equal(t, 10, resp.Usage.InputTokens)
		assert.Equal(t, 20, resp.Usage.OutputTokens)
		assert.Equal(t, 30, resp.Usage.TotalTokens)
		assert.Equal(t, 5, resp.Usage.ThinkingTokens)
	})

	t.Run("code execution chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.CodeExecutionChunk{Code: "print('hi')", Result: "hi"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.CodeExecutions, 1)
		assert.Equal(t, "print('hi')", resp.CodeExecutions[0].Code)
		assert.Equal(t, "hi", resp.CodeExecutions[0].Result)
	})

	t.Run("grounding chunks collected", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.GroundingChunk{
			WebSearchQueries: []string{"query1"},
			Sources: []agent.GroundingSource{
				{URI: "https://example.com", Title: "Example"},
			},
		}
		ch <- &agent.GroundingChunk{
			Sources: []agent.GroundingSource{
				{URI: "https://docs.k8s.io", Title: "K8s Docs"},
			},
		}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		require.Len(t, resp.Groundings, 2)
		assert.Equal(t, []string{"query1"}, resp.Groundings[0].WebSearchQueries)
		assert.Equal(t, "https://example.com", resp.Groundings[0].Sources[0].URI)
		assert.Empty(t, resp.Groundings[1].WebSearchQueries)
		assert.Equal(t, "https://docs.k8s.io", resp.Groundings[1].Sources[0].URI)
	})

	t.Run("empty stream has no groundings", func(t *testing.T) {
		ch := make(chan agent.Chunk, 1)
		ch <- &agent.TextChunk{Content: "hello"}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Nil(t, resp.Groundings)
	})

	t.Run("error chunk returns error", func(t *testing.T) {
		ch := make(chan agent.Chunk, 2)
		ch <- &agent.TextChunk{Content: "partial"}
		ch <- &agent.ErrorChunk{Message: "rate limited", Code: "429", Retryable: true}
		close(ch)

		resp, err := collectStream(ch)
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "rate limited")
		assert.Contains(t, err.Error(), "429")
		assert.Contains(t, err.Error(), "retryable: true")
	})

	t.Run("empty stream returns empty response", func(t *testing.T) {
		ch := make(chan agent.Chunk)
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Empty(t, resp.Text)
		assert.Empty(t, resp.ThinkingText)
		assert.Empty(t, resp.ToolCalls)
		assert.Nil(t, resp.Usage)
	})

	t.Run("mixed chunks collected correctly", func(t *testing.T) {
		ch := make(chan agent.Chunk, 6)
		ch <- &agent.ThinkingChunk{Content: "Thinking..."}
		ch <- &agent.TextChunk{Content: "I'll check pods."}
		ch <- &agent.ToolCallChunk{CallID: "c1", Name: "k8s.pods", Arguments: "{}"}
		ch <- &agent.UsageChunk{InputTokens: 50, OutputTokens: 100, TotalTokens: 150}
		close(ch)

		resp, err := collectStream(ch)
		require.NoError(t, err)
		assert.Equal(t, "Thinking...", resp.ThinkingText)
		assert.Equal(t, "I'll check pods.", resp.Text)
		require.Len(t, resp.ToolCalls, 1)
		require.NotNil(t, resp.Usage)
		assert.Equal(t, 150, resp.Usage.TotalTokens)
	})
}

// ============================================================================
// callLLM tests
// ============================================================================

func TestCallLLM(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		llm := &mockLLMClient{
			responses: []mockLLMResponse{
				{chunks: []agent.Chunk{
					&agent.TextChunk{Content: "Hello"},
					&agent.UsageChunk{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
				}},
			},
		}

		resp, err := callLLM(context.Background(), llm, &agent.GenerateInput{})
		require.NoError(t, err)
		assert.Equal(t, "Hello", resp.Text)
		assert.Equal(t, 15, resp.Usage.TotalTokens)
	})

	t.Run("generate error", func(t *testing.T) {
		llm := &mockLLMClient{
			responses: []mockLLMResponse{
				{err: fmt.Errorf("connection refused")},
			},
		}

		resp, err := callLLM(context.Background(), llm, &agent.GenerateInput{})
		require.Error(t, err)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "LLM Generate failed")
	})
}

// ============================================================================
// accumulateUsage tests
// ============================================================================

func TestAccumulateUsage(t *testing.T) {
	t.Run("accumulates from response with usage", func(t *testing.T) {
		total := &agent.TokenUsage{}
		resp := &LLMResponse{Usage: &agent.TokenUsage{
			InputTokens: 10, OutputTokens: 20, TotalTokens: 30, ThinkingTokens: 5,
		}}

		accumulateUsage(total, resp)
		assert.Equal(t, 10, total.InputTokens)
		assert.Equal(t, 20, total.OutputTokens)
		assert.Equal(t, 30, total.TotalTokens)
		assert.Equal(t, 5, total.ThinkingTokens)

		// Accumulate again
		accumulateUsage(total, resp)
		assert.Equal(t, 20, total.InputTokens)
		assert.Equal(t, 60, total.TotalTokens)
	})

	t.Run("nil usage is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 100}
		resp := &LLMResponse{Usage: nil}

		accumulateUsage(total, resp)
		assert.Equal(t, 100, total.InputTokens)
	})

	t.Run("nil resp is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 100}
		accumulateUsage(total, nil)
		assert.Equal(t, 100, total.InputTokens)
	})
}

// ============================================================================
// isTimeoutError tests
// ============================================================================

func TestIsTimeoutError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "context.DeadlineExceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped DeadlineExceeded",
			err:  fmt.Errorf("operation failed: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "timeout in message",
			err:  errors.New("request timeout after 30s"),
			want: true,
		},
		{
			name: "timed out in message",
			err:  errors.New("connection timed out"),
			want: true,
		},
		{
			name: "TIMEOUT uppercase in message",
			err:  errors.New("TIMEOUT occurred"),
			want: true,
		},
		{
			name: "regular error",
			err:  errors.New("connection refused"),
			want: false,
		},
		{
			name: "context.Canceled is not timeout",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isTimeoutError(tt.err))
		})
	}
}

// ============================================================================
// buildToolNameSet tests
// ============================================================================

func TestBuildToolNameSet(t *testing.T) {
	t.Run("builds set from tools", func(t *testing.T) {
		tools := []agent.ToolDefinition{
			{Name: "k8s.get_pods"},
			{Name: "k8s.get_logs"},
			{Name: "prom.query"},
		}
		set := buildToolNameSet(tools)
		assert.True(t, set["k8s.get_pods"])
		assert.True(t, set["k8s.get_logs"])
		assert.True(t, set["prom.query"])
		assert.False(t, set["nonexistent"])
	})

	t.Run("empty tools returns empty set", func(t *testing.T) {
		set := buildToolNameSet(nil)
		assert.Empty(t, set)
	})
}

// ============================================================================
// tokenUsageFromResp tests
// ============================================================================

func TestTokenUsageFromResp(t *testing.T) {
	t.Run("with usage", func(t *testing.T) {
		resp := &LLMResponse{Usage: &agent.TokenUsage{
			InputTokens: 10, OutputTokens: 20, TotalTokens: 30,
		}}
		usage := tokenUsageFromResp(resp)
		assert.Equal(t, 30, usage.TotalTokens)
	})

	t.Run("nil usage returns zero", func(t *testing.T) {
		resp := &LLMResponse{}
		usage := tokenUsageFromResp(resp)
		assert.Equal(t, 0, usage.TotalTokens)
	})

	t.Run("nil resp returns zero", func(t *testing.T) {
		usage := tokenUsageFromResp(nil)
		assert.Equal(t, 0, usage.TotalTokens)
	})
}

// ============================================================================
// formatCodeExecution tests
// ============================================================================

func TestFormatCodeExecution(t *testing.T) {
	t.Run("code and result", func(t *testing.T) {
		content := formatCodeExecution("print(2 + 2)", "4")
		assert.Contains(t, content, "```python\nprint(2 + 2)\n```")
		assert.Contains(t, content, "Output:\n```\n4\n```")
	})

	t.Run("code only", func(t *testing.T) {
		content := formatCodeExecution("print('hi')", "")
		assert.Contains(t, content, "```python\nprint('hi')\n```")
		assert.NotContains(t, content, "Output:")
	})

	t.Run("result only", func(t *testing.T) {
		content := formatCodeExecution("", "hello")
		assert.NotContains(t, content, "```python")
		assert.Contains(t, content, "Output:\n```\nhello\n```")
	})

	t.Run("empty both", func(t *testing.T) {
		content := formatCodeExecution("", "")
		assert.Empty(t, content)
	})
}

// ============================================================================
// formatGoogleSearchContent tests
// ============================================================================

func TestFormatGoogleSearchContent(t *testing.T) {
	t.Run("single query and source", func(t *testing.T) {
		content := formatGoogleSearchContent(
			[]string{"Euro 2024 winner"},
			[]agent.GroundingSource{{URI: "https://uefa.com", Title: "UEFA"}},
		)
		assert.Equal(t, "Google Search: 'Euro 2024 winner' → Sources: UEFA (https://uefa.com)", content)
	})

	t.Run("multiple queries and sources", func(t *testing.T) {
		content := formatGoogleSearchContent(
			[]string{"query1", "query2"},
			[]agent.GroundingSource{
				{URI: "https://a.com", Title: "Site A"},
				{URI: "https://b.com", Title: ""},
			},
		)
		assert.Contains(t, content, "'query1', 'query2'")
		assert.Contains(t, content, "Site A (https://a.com)")
		assert.Contains(t, content, "https://b.com")
	})
}

// ============================================================================
// formatUrlContextContent tests
// ============================================================================

func TestFormatUrlContextContent(t *testing.T) {
	t.Run("with titles", func(t *testing.T) {
		content := formatUrlContextContent([]agent.GroundingSource{
			{URI: "https://docs.k8s.io", Title: "K8s Docs"},
		})
		assert.Equal(t, "URL Context → Sources: K8s Docs (https://docs.k8s.io)", content)
	})

	t.Run("without titles", func(t *testing.T) {
		content := formatUrlContextContent([]agent.GroundingSource{
			{URI: "https://example.com", Title: ""},
		})
		assert.Equal(t, "URL Context → Sources: https://example.com", content)
	})
}

// ============================================================================
// formatGroundingSources tests
// ============================================================================

func TestFormatGroundingSources(t *testing.T) {
	sources := formatGroundingSources([]agent.GroundingSource{
		{URI: "https://a.com", Title: "A"},
		{URI: "https://b.com", Title: ""},
	})
	require.Len(t, sources, 2)
	assert.Equal(t, "https://a.com", sources[0]["uri"])
	assert.Equal(t, "A", sources[0]["title"])
	assert.Equal(t, "https://b.com", sources[1]["uri"])
	assert.Equal(t, "", sources[1]["title"])
}

// ============================================================================
// formatGroundingSupports tests
// ============================================================================

func TestFormatGroundingSupports(t *testing.T) {
	supports := formatGroundingSupports([]agent.GroundingSupport{
		{StartIndex: 0, EndIndex: 10, Text: "hello", GroundingChunkIndices: []int{0, 1}},
	})
	require.Len(t, supports, 1)
	assert.Equal(t, 0, supports[0]["start_index"])
	assert.Equal(t, 10, supports[0]["end_index"])
	assert.Equal(t, "hello", supports[0]["text"])
	assert.Equal(t, []int{0, 1}, supports[0]["grounding_chunk_indices"])
}

// ============================================================================
// createCodeExecutionEvents tests
// ============================================================================

func TestCreateCodeExecutionEvents(t *testing.T) {
	t.Run("pairs code and result", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, []agent.CodeExecutionChunk{
			{Code: "print(2 + 2)", Result: ""},
			{Code: "", Result: "4"},
		}, &eventSeq)

		assert.Equal(t, 1, created)
		assert.Equal(t, 1, eventSeq)
	})

	t.Run("code only emitted at end", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, []agent.CodeExecutionChunk{
			{Code: "print('hi')", Result: ""},
		}, &eventSeq)

		assert.Equal(t, 1, created)
	})

	t.Run("code_only then self_contained", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, []agent.CodeExecutionChunk{
			{Code: "x = 1", Result: ""},
			{Code: "print(x)", Result: "1"},
		}, &eventSeq)

		assert.Equal(t, 2, created)
	})

	t.Run("multiple executions", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, []agent.CodeExecutionChunk{
			{Code: "print(1)", Result: ""},
			{Code: "", Result: "1"},
			{Code: "print(2)", Result: ""},
			{Code: "", Result: "2"},
		}, &eventSeq)

		assert.Equal(t, 2, created)
		assert.Equal(t, 2, eventSeq)
	})

	t.Run("empty input", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, nil, &eventSeq)

		assert.Equal(t, 0, created)
		assert.Equal(t, 0, eventSeq)
	})

	t.Run("consecutive code without result emits previous", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createCodeExecutionEvents(context.Background(), execCtx, []agent.CodeExecutionChunk{
			{Code: "first_code", Result: ""},
			{Code: "second_code", Result: ""},
			{Code: "", Result: "result"},
		}, &eventSeq)

		// first_code emitted alone, second_code paired with result
		assert.Equal(t, 2, created)
	})
}

// ============================================================================
// createGroundingEvents tests
// ============================================================================

func TestCreateGroundingEvents(t *testing.T) {
	t.Run("google search result", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createGroundingEvents(context.Background(), execCtx, []agent.GroundingChunk{
			{
				WebSearchQueries: []string{"test query"},
				Sources:          []agent.GroundingSource{{URI: "https://a.com", Title: "A"}},
			},
		}, &eventSeq)

		assert.Equal(t, 1, created)
		assert.Equal(t, 1, eventSeq)
	})

	t.Run("url context result", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createGroundingEvents(context.Background(), execCtx, []agent.GroundingChunk{
			{
				Sources: []agent.GroundingSource{{URI: "https://docs.k8s.io", Title: "K8s"}},
			},
		}, &eventSeq)

		assert.Equal(t, 1, created)
	})

	t.Run("skips empty sources", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createGroundingEvents(context.Background(), execCtx, []agent.GroundingChunk{
			{WebSearchQueries: []string{"query"}, Sources: nil},
		}, &eventSeq)

		assert.Equal(t, 0, created)
		assert.Equal(t, 0, eventSeq)
	})

	t.Run("empty input", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createGroundingEvents(context.Background(), execCtx, nil, &eventSeq)

		assert.Equal(t, 0, created)
	})

	t.Run("multiple groundings", func(t *testing.T) {
		execCtx := newTestExecCtx(t, &mockLLMClient{}, &mockToolExecutor{})
		eventSeq := 0

		created := createGroundingEvents(context.Background(), execCtx, []agent.GroundingChunk{
			{
				WebSearchQueries: []string{"q1"},
				Sources:          []agent.GroundingSource{{URI: "https://a.com", Title: "A"}},
			},
			{
				Sources: []agent.GroundingSource{{URI: "https://b.com", Title: "B"}},
			},
		}, &eventSeq)

		assert.Equal(t, 2, created)
		assert.Equal(t, 2, eventSeq)
	})
}
