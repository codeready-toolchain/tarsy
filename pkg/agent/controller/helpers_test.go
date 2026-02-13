package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/stretchr/testify/assert"
)

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
// accumulateTokenUsage tests
// ============================================================================

func TestAccumulateTokenUsage(t *testing.T) {
	t.Run("adds usage to total", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
		usage := &agent.TokenUsage{InputTokens: 20, OutputTokens: 30, TotalTokens: 50, ThinkingTokens: 8}

		accumulateTokenUsage(total, usage)
		assert.Equal(t, 30, total.InputTokens)
		assert.Equal(t, 35, total.OutputTokens)
		assert.Equal(t, 65, total.TotalTokens)
		assert.Equal(t, 8, total.ThinkingTokens)
	})

	t.Run("nil usage is no-op", func(t *testing.T) {
		total := &agent.TokenUsage{InputTokens: 42}
		accumulateTokenUsage(total, nil)
		assert.Equal(t, 42, total.InputTokens)
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
			name: "timeout in message only (no wrapped sentinel)",
			err:  errors.New("request timeout after 30s"),
			want: false,
		},
		{
			name: "timed out in message only (no wrapped sentinel)",
			err:  errors.New("connection timed out"),
			want: false,
		},
		{
			name: "TIMEOUT uppercase in message only (no wrapped sentinel)",
			err:  errors.New("TIMEOUT occurred"),
			want: false,
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
// buildResponseMetadata tests
// ============================================================================

func TestBuildResponseMetadata(t *testing.T) {
	t.Run("nil resp returns nil", func(t *testing.T) {
		result := buildResponseMetadata(nil)
		assert.Nil(t, result)
	})

	t.Run("no groundings returns nil", func(t *testing.T) {
		resp := &LLMResponse{Text: "some text"}
		result := buildResponseMetadata(resp)
		assert.Nil(t, result)
	})

	t.Run("empty groundings slice returns nil", func(t *testing.T) {
		resp := &LLMResponse{Groundings: []agent.GroundingChunk{}}
		result := buildResponseMetadata(resp)
		assert.Nil(t, result)
	})

	t.Run("google search grounding", func(t *testing.T) {
		resp := &LLMResponse{
			Groundings: []agent.GroundingChunk{
				{
					WebSearchQueries: []string{"kubernetes OOM best practices", "pod memory limits"},
					Sources: []agent.GroundingSource{
						{URI: "https://k8s.io/docs/memory", Title: "Memory Management"},
						{URI: "https://example.com/oom", Title: "OOM Guide"},
					},
				},
			},
		}

		result := buildResponseMetadata(resp)
		assert.NotNil(t, result)

		groundings, ok := result["groundings"].([]map[string]any)
		assert.True(t, ok)
		assert.Len(t, groundings, 1)

		entry := groundings[0]
		assert.Equal(t, "google_search", entry["type"])
		assert.Equal(t, []string{"kubernetes OOM best practices", "pod memory limits"}, entry["queries"])

		sources, ok := entry["sources"].([]map[string]string)
		assert.True(t, ok)
		assert.Len(t, sources, 2)
		assert.Equal(t, "https://k8s.io/docs/memory", sources[0]["uri"])
		assert.Equal(t, "Memory Management", sources[0]["title"])
		assert.Equal(t, "https://example.com/oom", sources[1]["uri"])
	})

	t.Run("url context grounding (no web search queries)", func(t *testing.T) {
		resp := &LLMResponse{
			Groundings: []agent.GroundingChunk{
				{
					Sources: []agent.GroundingSource{
						{URI: "https://docs.example.com/api", Title: "API Reference"},
					},
				},
			},
		}

		result := buildResponseMetadata(resp)
		assert.NotNil(t, result)

		groundings := result["groundings"].([]map[string]any)
		assert.Len(t, groundings, 1)
		assert.Equal(t, "url_context", groundings[0]["type"])
		assert.Nil(t, groundings[0]["queries"]) // no queries for url_context
	})

	t.Run("grounding with supports", func(t *testing.T) {
		resp := &LLMResponse{
			Groundings: []agent.GroundingChunk{
				{
					WebSearchQueries: []string{"query"},
					Sources: []agent.GroundingSource{
						{URI: "https://example.com", Title: "Example"},
					},
					Supports: []agent.GroundingSupport{
						{
							StartIndex:            0,
							EndIndex:              50,
							Text:                  "Supported text segment",
							GroundingChunkIndices: []int{0},
						},
						{
							StartIndex:            60,
							EndIndex:              100,
							Text:                  "Another segment",
							GroundingChunkIndices: []int{0},
						},
					},
				},
			},
		}

		result := buildResponseMetadata(resp)
		groundings := result["groundings"].([]map[string]any)
		entry := groundings[0]

		supports, ok := entry["supports"].([]map[string]any)
		assert.True(t, ok)
		assert.Len(t, supports, 2)

		assert.Equal(t, 0, supports[0]["start_index"])
		assert.Equal(t, 50, supports[0]["end_index"])
		assert.Equal(t, "Supported text segment", supports[0]["text"])
		assert.Equal(t, []int{0}, supports[0]["source_indices"])

		assert.Equal(t, 60, supports[1]["start_index"])
		assert.Equal(t, 100, supports[1]["end_index"])
	})

	t.Run("multiple groundings (google search + url context)", func(t *testing.T) {
		resp := &LLMResponse{
			Groundings: []agent.GroundingChunk{
				{
					WebSearchQueries: []string{"search query"},
					Sources: []agent.GroundingSource{
						{URI: "https://search-result.com", Title: "Search Result"},
					},
				},
				{
					Sources: []agent.GroundingSource{
						{URI: "https://fetched-url.com/doc", Title: "Fetched Doc"},
					},
				},
			},
		}

		result := buildResponseMetadata(resp)
		groundings := result["groundings"].([]map[string]any)
		assert.Len(t, groundings, 2)

		assert.Equal(t, "google_search", groundings[0]["type"])
		assert.Equal(t, "url_context", groundings[1]["type"])
	})

	t.Run("grounding without sources or supports", func(t *testing.T) {
		resp := &LLMResponse{
			Groundings: []agent.GroundingChunk{
				{
					WebSearchQueries: []string{"query with no sources yet"},
				},
			},
		}

		result := buildResponseMetadata(resp)
		groundings := result["groundings"].([]map[string]any)
		assert.Len(t, groundings, 1)

		entry := groundings[0]
		assert.Equal(t, "google_search", entry["type"])
		assert.Equal(t, []string{"query with no sources yet"}, entry["queries"])
		assert.Nil(t, entry["sources"])  // no sources key when empty
		assert.Nil(t, entry["supports"]) // no supports key when empty
	})
}
