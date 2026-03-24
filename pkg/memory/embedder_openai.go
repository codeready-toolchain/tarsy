package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

const openaiEmbedBaseURL = "https://api.openai.com/v1"

// OpenAIEmbedder calls the OpenAI embeddings API.
type OpenAIEmbedder struct {
	model      string
	apiKeyEnv  string
	dimensions int
	baseURL    string
	client     *http.Client
}

func NewOpenAIEmbedder(cfg config.EmbeddingConfig) (*OpenAIEmbedder, error) {
	if cfg.APIKeyEnv == "" {
		return nil, fmt.Errorf("openai embedder: api_key_env is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = openaiEmbedBaseURL
	}
	return &OpenAIEmbedder{
		model:      cfg.Model,
		apiKeyEnv:  cfg.APIKeyEnv,
		dimensions: cfg.Dimensions,
		baseURL:    base,
		client:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, text string, _ EmbeddingTask) ([]float32, error) {
	apiKey := os.Getenv(e.apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("openai embedder: env var %s is not set", e.apiKeyEnv)
	}

	reqBody := openaiEmbedRequest{
		Input:      text,
		Model:      e.model,
		Dimensions: e.dimensions,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: marshal request: %w", err)
	}

	url := e.baseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openai embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embedder: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embedder: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result openaiEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("openai embedder: unmarshal response: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai embedder: empty response data")
	}

	return result.Data[0].Embedding, nil
}

type openaiEmbedRequest struct {
	Input      string `json:"input"`
	Model      string `json:"model"`
	Dimensions int    `json:"dimensions,omitempty"`
}

type openaiEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}
