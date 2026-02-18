package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	tarsyslack "github.com/codeready-toolchain/tarsy/pkg/slack"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// slackCall captures a single chat.postMessage request to the mock.
type slackCall struct {
	Channel  string
	ThreadTS string
	Blocks   string // raw JSON blocks payload
}

// mockSlackServer provides an httptest server that mimics the Slack API,
// recording chat.postMessage calls and responding to conversations.history
// with an optional canned message that matches a fingerprint.
type mockSlackServer struct {
	mu    sync.Mutex
	calls []slackCall

	server      *httptest.Server
	channelID   string
	fingerprint string // fingerprint text to match in conversations.history
	matchTS     string // timestamp returned for matching message
}

func newMockSlackServer(channelID, fingerprint, matchTS string) *mockSlackServer {
	m := &mockSlackServer{
		channelID:   channelID,
		fingerprint: fingerprint,
		matchTS:     matchTS,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/chat.postMessage", m.handlePostMessage)
	mux.HandleFunc("/conversations.history", m.handleConversationsHistory)

	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockSlackServer) handlePostMessage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	call := slackCall{
		Channel:  r.FormValue("channel"),
		ThreadTS: r.FormValue("thread_ts"),
		Blocks:   r.FormValue("blocks"),
	}

	m.mu.Lock()
	m.calls = append(m.calls, call)
	m.mu.Unlock()

	resp := map[string]interface{}{
		"ok":      true,
		"channel": call.Channel,
		"ts":      fmt.Sprintf("1234567890.%06d", len(m.calls)),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockSlackServer) handleConversationsHistory(w http.ResponseWriter, _ *http.Request) {
	var messages []map[string]interface{}
	if m.fingerprint != "" {
		messages = append(messages, map[string]interface{}{
			"type": "message",
			"text": m.fingerprint,
			"ts":   m.matchTS,
		})
	}

	resp := map[string]interface{}{
		"ok":       true,
		"messages": messages,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockSlackServer) getCalls() []slackCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]slackCall, len(m.calls))
	copy(out, m.calls)
	return out
}

func (m *mockSlackServer) close() {
	m.server.Close()
}

// TestE2E_SlackNotifications verifies that the worker sends exactly two
// Slack messages (start + terminal) when processing a session with a
// fingerprint, and that both messages are threaded to the original message.
func TestE2E_SlackNotifications(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	const (
		channelID   = "C99TEST"
		fingerprint = "Pod nginx-xyz OOMKilled in namespace production"
		threadTS    = "1700000000.000001"
	)

	mock := newMockSlackServer(channelID, fingerprint, threadTS)
	defer mock.close()

	apiURL := mock.server.URL + "/"
	client := tarsyslack.NewClientWithAPIURL("xoxb-test-token", channelID, apiURL)
	slackSvc := tarsyslack.NewServiceWithClient(client, "http://test-dashboard:8080")

	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Executive summary of investigation."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})

	cfg := configs.Load(t, "concurrency")
	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
		WithSlackService(slackSvc),
	)

	resp := app.SubmitAlertWithFingerprint(t, "test-concurrency", "Pod OOMKilled", fingerprint)
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Verify fingerprint stored on session.
	session, err := app.EntClient.AlertSession.Get(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, session.SlackMessageFingerprint, "fingerprint should be stored on session")
	assert.Equal(t, fingerprint, *session.SlackMessageFingerprint)

	// Verify exactly 2 Slack messages were sent: start + terminal.
	calls := mock.getCalls()
	require.Len(t, calls, 2, "expected exactly 2 chat.postMessage calls (start + terminal)")

	// Both messages should target the correct channel.
	assert.Equal(t, channelID, calls[0].Channel, "start message: wrong channel")
	assert.Equal(t, channelID, calls[1].Channel, "terminal message: wrong channel")

	// Both messages should be threaded to the original message.
	assert.Equal(t, threadTS, calls[0].ThreadTS, "start message should be threaded")
	assert.Equal(t, threadTS, calls[1].ThreadTS, "terminal message should be threaded")

	// Start message should contain "Processing started".
	startBlocks := decodeBlocks(t, calls[0].Blocks)
	assert.Contains(t, startBlocks, "Processing started", "start message should mention processing started")

	// Terminal message should contain the executive summary or completion status.
	termBlocks := decodeBlocks(t, calls[1].Blocks)
	assert.Contains(t, termBlocks, "Analysis Complete", "terminal message should mention analysis complete")

	// Dashboard links should be present in both messages.
	assert.Contains(t, startBlocks, "test-dashboard", "start message should contain dashboard link")
	assert.Contains(t, termBlocks, "test-dashboard", "terminal message should contain dashboard link")
}

// TestE2E_SlackNoFingerprint verifies that when an alert is submitted
// without a fingerprint, only the terminal notification is sent (no start
// notification), and it is not threaded.
func TestE2E_SlackNoFingerprint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	const channelID = "C99TEST"

	mock := newMockSlackServer(channelID, "", "")
	defer mock.close()

	apiURL := mock.server.URL + "/"
	client := tarsyslack.NewClientWithAPIURL("xoxb-test-token", channelID, apiURL)
	slackSvc := tarsyslack.NewServiceWithClient(client, "http://test-dashboard:8080")

	llm := NewScriptedLLMClient()
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete."},
			&agent.UsageChunk{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Executive summary."},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})

	cfg := configs.Load(t, "concurrency")
	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
		WithSlackService(slackSvc),
	)

	resp := app.SubmitAlert(t, "test-concurrency", "Pod OOMKilled")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	// Only the terminal notification should be sent (start is skipped without fingerprint).
	calls := mock.getCalls()
	require.Len(t, calls, 1, "expected exactly 1 chat.postMessage call (terminal only, no start)")

	assert.Equal(t, channelID, calls[0].Channel, "terminal message: wrong channel")
	assert.Empty(t, calls[0].ThreadTS, "terminal message should NOT be threaded without fingerprint")

	termBlocks := decodeBlocks(t, calls[0].Blocks)
	assert.Contains(t, termBlocks, "Analysis Complete", "terminal message should mention analysis complete")
}

// decodeBlocks extracts the raw JSON blocks string into a flat text
// representation for simple substring assertions.
func decodeBlocks(t *testing.T, raw string) string {
	t.Helper()
	if raw == "" {
		return ""
	}
	decoded, err := url.QueryUnescape(raw)
	if err != nil {
		decoded = raw
	}
	var blocks []map[string]interface{}
	if err := json.Unmarshal([]byte(decoded), &blocks); err != nil {
		return decoded
	}
	out, _ := json.Marshal(blocks)
	return string(out)
}
