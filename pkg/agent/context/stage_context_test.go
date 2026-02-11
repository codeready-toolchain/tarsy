package context

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildStageContext(t *testing.T) {
	t.Run("empty stages returns empty string", func(t *testing.T) {
		assert.Equal(t, "", BuildStageContext(nil))
		assert.Equal(t, "", BuildStageContext([]StageResult{}))
	})

	t.Run("single stage", func(t *testing.T) {
		result := BuildStageContext([]StageResult{
			{StageName: "data-collection", FinalAnalysis: "Found OOM in pod-1."},
		})
		expected := "<!-- CHAIN_CONTEXT_START -->\n\n### Stage 1: data-collection\n\nFound OOM in pod-1.\n\n<!-- CHAIN_CONTEXT_END -->"
		assert.Equal(t, expected, result)
	})

	t.Run("multiple stages", func(t *testing.T) {
		result := BuildStageContext([]StageResult{
			{StageName: "data-collection", FinalAnalysis: "Collected metrics."},
			{StageName: "diagnosis", FinalAnalysis: "Root cause: memory leak."},
		})
		expected := "<!-- CHAIN_CONTEXT_START -->\n\n### Stage 1: data-collection\n\nCollected metrics.\n\n### Stage 2: diagnosis\n\nRoot cause: memory leak.\n\n<!-- CHAIN_CONTEXT_END -->"
		assert.Equal(t, expected, result)
	})

	t.Run("missing final analysis", func(t *testing.T) {
		result := BuildStageContext([]StageResult{
			{StageName: "data-collection", FinalAnalysis: ""},
		})
		expected := "<!-- CHAIN_CONTEXT_START -->\n\n### Stage 1: data-collection\n\n(No final analysis produced)\n\n<!-- CHAIN_CONTEXT_END -->"
		assert.Equal(t, expected, result)
	})
}
