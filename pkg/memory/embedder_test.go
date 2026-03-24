package memory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogleEmbedder_RequestConstruction(t *testing.T) {
	var capturedReq googleEmbedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		assert.Contains(t, r.URL.Path, "/models/test-model:embedContent")
		assert.Contains(t, r.URL.RawQuery, "key=test-key")

		if err := json.NewDecoder(r.Body).Decode(&capturedReq); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		resp := googleEmbedResponse{}
		resp.Embedding.Values = []float32{0.1, 0.2, 0.3}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("TEST_GOOGLE_KEY", "test-key")

	embedder, err := NewGoogleEmbedder(config.EmbeddingConfig{
		Provider:   config.EmbeddingProviderGoogle,
		Model:      "test-model",
		APIKeyEnv:  "TEST_GOOGLE_KEY",
		Dimensions: 768,
		BaseURL:    srv.URL,
	})
	require.NoError(t, err)

	vec, err := embedder.Embed(t.Context(), "test input", EmbeddingTaskDocument)
	require.NoError(t, err)
	assert.Equal(t, []float32{0.1, 0.2, 0.3}, vec)

	assert.Equal(t, "RETRIEVAL_DOCUMENT", capturedReq.TaskType)
	assert.Equal(t, 768, capturedReq.OutputDimensionality)
	assert.Equal(t, "models/test-model", capturedReq.Model)
	require.Len(t, capturedReq.Content.Parts, 1)
	assert.Equal(t, "test input", capturedReq.Content.Parts[0].Text)
}

func TestGoogleEmbedder_QueryTask(t *testing.T) {
	var capturedReq googleEmbedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedReq)
		resp := googleEmbedResponse{}
		resp.Embedding.Values = []float32{0.5}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("TEST_GOOGLE_KEY", "test-key")

	embedder, err := NewGoogleEmbedder(config.EmbeddingConfig{
		Model:     "test-model",
		APIKeyEnv: "TEST_GOOGLE_KEY",
		BaseURL:   srv.URL,
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "query text", EmbeddingTaskQuery)
	require.NoError(t, err)

	assert.Equal(t, "RETRIEVAL_QUERY", capturedReq.TaskType)
}

func TestOpenAIEmbedder_RequestConstruction(t *testing.T) {
	var capturedReq openaiEmbedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/embeddings", r.URL.Path)
		assert.Equal(t, "Bearer test-openai-key", r.Header.Get("Authorization"))

		json.NewDecoder(r.Body).Decode(&capturedReq)

		resp := openaiEmbedResponse{
			Data: []struct {
				Embedding []float32 `json:"embedding"`
			}{
				{Embedding: []float32{0.4, 0.5, 0.6}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	t.Setenv("TEST_OPENAI_KEY", "test-openai-key")

	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Provider:   config.EmbeddingProviderOpenAI,
		Model:      "text-embedding-3-small",
		APIKeyEnv:  "TEST_OPENAI_KEY",
		Dimensions: 256,
		BaseURL:    srv.URL,
	})
	require.NoError(t, err)

	vec, err := embedder.Embed(t.Context(), "test input", EmbeddingTaskDocument)
	require.NoError(t, err)
	assert.Equal(t, []float32{0.4, 0.5, 0.6}, vec)

	assert.Equal(t, "text-embedding-3-small", capturedReq.Model)
	assert.Equal(t, "test input", capturedReq.Input)
	assert.Equal(t, 256, capturedReq.Dimensions)
}

func TestNewEmbedder_Factory(t *testing.T) {
	t.Run("google", func(t *testing.T) {
		e, err := NewEmbedder(config.EmbeddingConfig{
			Provider:  config.EmbeddingProviderGoogle,
			Model:     "test-model",
			APIKeyEnv: "TEST_KEY",
		})
		require.NoError(t, err)
		assert.IsType(t, &GoogleEmbedder{}, e)
	})

	t.Run("openai", func(t *testing.T) {
		e, err := NewEmbedder(config.EmbeddingConfig{
			Provider:  config.EmbeddingProviderOpenAI,
			Model:     "test-model",
			APIKeyEnv: "TEST_KEY",
		})
		require.NoError(t, err)
		assert.IsType(t, &OpenAIEmbedder{}, e)
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := NewEmbedder(config.EmbeddingConfig{
			Provider: "unknown",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown embedding provider")
	})
}

func TestGoogleEmbedder_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded"}}`))
	}))
	defer srv.Close()

	t.Setenv("TEST_GOOGLE_KEY", "test-key")
	embedder, err := NewGoogleEmbedder(config.EmbeddingConfig{
		Model: "test-model", APIKeyEnv: "TEST_GOOGLE_KEY", BaseURL: srv.URL,
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "test", EmbeddingTaskDocument)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 429")
}

func TestOpenAIEmbedder_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"message":"internal server error"}}`))
	}))
	defer srv.Close()

	t.Setenv("TEST_OPENAI_KEY", "test-key")
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Model: "test-model", APIKeyEnv: "TEST_OPENAI_KEY", BaseURL: srv.URL,
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "test", EmbeddingTaskDocument)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "status 500")
}

func TestOpenAIEmbedder_EmptyResponseData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	t.Setenv("TEST_OPENAI_KEY", "test-key")
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Model: "test-model", APIKeyEnv: "TEST_OPENAI_KEY", BaseURL: srv.URL,
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "test", EmbeddingTaskDocument)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty response data")
}

func TestGoogleEmbedder_MissingAPIKey(t *testing.T) {
	embedder, err := NewGoogleEmbedder(config.EmbeddingConfig{
		Model:     "test",
		APIKeyEnv: "NONEXISTENT_KEY_FOR_TEST",
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "test", EmbeddingTaskDocument)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not set")
}

func TestOpenAIEmbedder_MissingAPIKey(t *testing.T) {
	embedder, err := NewOpenAIEmbedder(config.EmbeddingConfig{
		Model:     "test",
		APIKeyEnv: "NONEXISTENT_KEY_FOR_TEST",
	})
	require.NoError(t, err)

	_, err = embedder.Embed(t.Context(), "test", EmbeddingTaskDocument)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not set")
}
