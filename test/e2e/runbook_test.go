package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// ────────────────────────────────────────────────────────────
// TestE2E_RunbookURL — submits an alert with a runbook URL
// pointing to a mock HTTP server. Verifies the full flow:
//
//	API submission → session stored with runbook_url →
//	executor resolves via RunbookService → content fetched →
//	session completes successfully.
//
// ────────────────────────────────────────────────────────────
func TestE2E_RunbookURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Start a mock HTTP server serving custom runbook content.
	const customRunbook = "# Custom Runbook\n\n1. Check pod logs\n2. Restart the deployment\n3. Verify metrics"
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(customRunbook))
	}))
	defer mockServer.Close()

	// LLM script: 1 investigation + 1 executive summary.
	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation with custom runbook complete."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Summary: issue resolved per custom runbook."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})

	// Load concurrency config (simplest chain: one stage, one agent, max_iterations=1)
	// and widen AllowedDomains to include the test server.
	cfg := configs.Load(t, "concurrency")
	cfg.Runbooks = &config.RunbookConfig{
		AllowedDomains: []string{"127.0.0.1"},
	}

	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
	)

	// Submit alert with runbook URL pointing to mock server.
	runbookURL := mockServer.URL + "/runbook.md"
	resp := app.SubmitAlertWithRunbook(t, "test-concurrency", "Pod OOMKilled in production", runbookURL)
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	// Wait for session to complete.
	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify the session was stored with the runbook URL.
	session, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, session.RunbookURL, "session should have runbook_url set")
	assert.Equal(t, runbookURL, *session.RunbookURL)

	// Verify session completed successfully via API.
	apiSession := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", apiSession["status"])
	assert.Equal(t, runbookURL, apiSession["runbook_url"])
	assert.NotEmpty(t, apiSession["final_analysis"])

	// Verify the LLM was called exactly twice (investigation + summary).
	assert.Equal(t, 2, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_RunbookURL_InvalidDomain — submits an alert with a
// runbook URL on a disallowed domain and verifies the API
// returns a 400 Bad Request before any processing starts.
// ────────────────────────────────────────────────────────────
func TestE2E_RunbookURL_InvalidDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// No LLM calls expected — the request is rejected at the API layer.
	llm := NewScriptedLLMClient()

	cfg := configs.Load(t, "concurrency")

	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
	)

	// Submit alert with a runbook URL on a disallowed domain.
	body := map[string]interface{}{
		"alert_type": "test-concurrency",
		"data":       "Pod CrashLoopBackOff",
		"runbook":    "https://evil.com/malicious-runbook.md",
	}

	// Expect 400 Bad Request.
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, app.BaseURL+"/api/v1/alerts", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Verify no LLM calls were made.
	assert.Equal(t, 0, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_RunbookListEndpoint — verifies GET /api/v1/runbooks
// returns an empty array when no repo URL is configured (the
// default for all e2e tests).
// ────────────────────────────────────────────────────────────
func TestE2E_RunbookListEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	llm := NewScriptedLLMClient()
	cfg := configs.Load(t, "concurrency")

	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
	)

	// GET /api/v1/runbooks should return an empty array (no repo_url configured).
	runbooks := app.GetRunbooks(t)
	assert.Equal(t, 0, len(runbooks))

	// Verify no LLM calls were triggered.
	assert.Equal(t, 0, llm.CallCount())
}

// ────────────────────────────────────────────────────────────
// TestE2E_RunbookFallback — submits an alert without a runbook
// URL and verifies the session still completes using the
// default runbook content (existing behavior preserved).
// ────────────────────────────────────────────────────────────
func TestE2E_RunbookFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Default runbook investigation complete."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Summary with default runbook."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})

	cfg := configs.Load(t, "concurrency")

	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
	)

	// Submit alert without runbook URL.
	resp := app.SubmitAlert(t, "test-concurrency", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify session has no runbook_url.
	session, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
	require.NoError(t, err)
	assert.Nil(t, session.RunbookURL, "session should not have runbook_url set")

	// Verify via API.
	apiSession := app.GetSession(t, sessionID)
	assert.Equal(t, "completed", apiSession["status"])
	assert.Nil(t, apiSession["runbook_url"])
	assert.Equal(t, 2, llm.CallCount())
}
