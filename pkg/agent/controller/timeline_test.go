package controller

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// parentExecID / parentExecIDPtr tests
// ============================================================================

func TestParentExecID(t *testing.T) {
	tests := []struct {
		name     string
		subAgent *agent.SubAgentContext
		want     string
	}{
		{
			name:     "nil SubAgent returns empty",
			subAgent: nil,
			want:     "",
		},
		{
			name:     "SubAgent with ParentExecID",
			subAgent: &agent.SubAgentContext{Task: "do stuff", ParentExecID: "exec-orch-123"},
			want:     "exec-orch-123",
		},
		{
			name:     "SubAgent with empty ParentExecID",
			subAgent: &agent.SubAgentContext{Task: "do stuff", ParentExecID: ""},
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCtx := &agent.ExecutionContext{SubAgent: tt.subAgent}
			assert.Equal(t, tt.want, parentExecID(execCtx))
		})
	}
}

func TestParentExecIDPtr(t *testing.T) {
	tests := []struct {
		name     string
		subAgent *agent.SubAgentContext
		wantNil  bool
		wantVal  string
	}{
		{
			name:     "nil SubAgent returns nil",
			subAgent: nil,
			wantNil:  true,
		},
		{
			name:     "SubAgent with ParentExecID returns pointer",
			subAgent: &agent.SubAgentContext{Task: "do stuff", ParentExecID: "exec-orch-123"},
			wantNil:  false,
			wantVal:  "exec-orch-123",
		},
		{
			name:     "SubAgent with empty ParentExecID returns nil",
			subAgent: &agent.SubAgentContext{Task: "do stuff", ParentExecID: ""},
			wantNil:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCtx := &agent.ExecutionContext{SubAgent: tt.subAgent}
			got := parentExecIDPtr(execCtx)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.wantVal, *got)
			}
		})
	}
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
