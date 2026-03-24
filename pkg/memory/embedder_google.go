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

const googleEmbedBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GoogleEmbedder calls the Google Generative Language embedding API.
type GoogleEmbedder struct {
	model      string
	apiKeyEnv  string
	dimensions int
	baseURL    string
	client     *http.Client
}

func NewGoogleEmbedder(cfg config.EmbeddingConfig) (*GoogleEmbedder, error) {
	if cfg.APIKeyEnv == "" {
		return nil, fmt.Errorf("google embedder: api_key_env is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = googleEmbedBaseURL
	}
	return &GoogleEmbedder{
		model:      cfg.Model,
		apiKeyEnv:  cfg.APIKeyEnv,
		dimensions: cfg.Dimensions,
		baseURL:    base,
		client:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *GoogleEmbedder) Embed(ctx context.Context, text string, task EmbeddingTask) ([]float32, error) {
	apiKey := os.Getenv(e.apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("google embedder: env var %s is not set", e.apiKeyEnv)
	}

	taskType := "RETRIEVAL_DOCUMENT"
	if task == EmbeddingTaskQuery {
		taskType = "RETRIEVAL_QUERY"
	}

	reqBody := googleEmbedRequest{
		Model: "models/" + e.model,
		Content: googleContent{
			Parts: []googlePart{{Text: text}},
		},
		TaskType:             taskType,
		OutputDimensionality: e.dimensions,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("google embedder: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", e.baseURL, e.model, apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("google embedder: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google embedder: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("google embedder: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google embedder: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result googleEmbedResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("google embedder: unmarshal response: %w", err)
	}

	return result.Embedding.Values, nil
}

type googleEmbedRequest struct {
	Model                string        `json:"model"`
	Content              googleContent `json:"content"`
	TaskType             string        `json:"taskType"`
	OutputDimensionality int           `json:"outputDimensionality,omitempty"`
}

type googleContent struct {
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text string `json:"text"`
}

type googleEmbedResponse struct {
	Embedding struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
}
