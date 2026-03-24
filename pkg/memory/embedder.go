package memory

import (
	"context"
	"fmt"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// EmbeddingTask distinguishes storage vs. search embeddings.
// Google's API uses different task types for each; OpenAI does not.
type EmbeddingTask string

const (
	EmbeddingTaskDocument EmbeddingTask = "document"
	EmbeddingTaskQuery    EmbeddingTask = "query"
)

// Embedder generates vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string, task EmbeddingTask) ([]float32, error)
}

// NewEmbedder creates an Embedder for the configured provider.
func NewEmbedder(cfg config.EmbeddingConfig) (Embedder, error) {
	switch cfg.Provider {
	case config.EmbeddingProviderGoogle:
		return NewGoogleEmbedder(cfg)
	case config.EmbeddingProviderOpenAI:
		return NewOpenAIEmbedder(cfg)
	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", cfg.Provider)
	}
}
