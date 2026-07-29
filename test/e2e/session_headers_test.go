package e2e

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/mcp"
	"github.com/codeready-toolchain/tarsy/test/e2e/testdata/configs"
)

// sessionMCPRecorder captures X-Session-ID on MCP traffic and DELETE /sessions/{id}.
type sessionMCPRecorder struct {
	mu              sync.Mutex
	sessionHeaders  []string
	deletedSessions []string
}

func (r *sessionMCPRecorder) recordHeader(v string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessionHeaders = append(r.sessionHeaders, v)
}

func (r *sessionMCPRecorder) recordDelete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletedSessions = append(r.deletedSessions, id)
}

func (r *sessionMCPRecorder) headers() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.sessionHeaders))
	copy(out, r.sessionHeaders)
	return out
}

func (r *sessionMCPRecorder) deletes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.deletedSessions))
	copy(out, r.deletedSessions)
	return out
}

func startSessionScopedMCPServer(t *testing.T, recorder *sessionMCPRecorder) *httptest.Server {
	t.Helper()

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "session-mcp", Version: "test"}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "echo",
		Description: "echo tool for session header e2e",
		InputSchema: emptySchema,
	}, StaticToolHandler(`{"ok":true}`))

	mcpHandler := mcpsdk.NewStreamableHTTPHandler(func(_ *http.Request) *mcpsdk.Server {
		return server
	}, &mcpsdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true})

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, req *http.Request) {
		recorder.recordHeader(req.Header.Get("X-Session-ID"))
		mcpHandler.ServeHTTP(w, req)
	})
	mux.HandleFunc("/sessions/", func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodDelete {
			http.NotFound(w, req)
			return
		}
		id := strings.TrimPrefix(req.URL.Path, "/sessions/")
		recorder.recordDelete(id)
		w.WriteHeader(http.StatusNoContent)
	})

	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return httpServer
}

// TestE2E_SessionHeadersAndCleanup verifies that when configured:
//   - X-Session-ID custom header is set to the investigation/chat session ID
//   - session_cleanup_url DELETE is called after investigation and chat
func TestE2E_SessionHeadersAndCleanup(t *testing.T) {
	recorder := &sessionMCPRecorder{}
	mcpServer := startSessionScopedMCPServer(t, recorder)

	cfg := configs.Load(t, "session-headers")
	serverCfg, err := cfg.MCPServerRegistry.Get("session-mcp")
	require.NoError(t, err)
	serverCfg.Transport.URL = mcpServer.URL + "/mcp"
	serverCfg.Transport.SessionCleanupURL = mcpServer.URL + "/sessions/{{.SESSION_ID}}"

	llm := NewScriptedLLMClient()
	// Investigation: tool call then final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Checking via session MCP."},
			&agent.ToolCallChunk{CallID: "call-1", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete: session MCP ok."},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	// Chat: tool call then final answer.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Following up via session MCP."},
			&agent.ToolCallChunk{CallID: "call-chat-1", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Chat complete: session MCP ok."},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})

	factory := mcp.NewClientFactory(cfg.MCPServerRegistry, nil)
	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
		WithMCPFactory(factory),
	)

	resp := app.SubmitAlert(t, "test-session-headers", "session header probe")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	require.Eventually(t, func() bool {
		for _, id := range recorder.deletes() {
			if id == sessionID {
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "expected investigation cleanup DELETE for %s", sessionID)

	headersAfterInvestigation := recorder.headers()
	require.NotEmpty(t, headersAfterInvestigation, "expected X-Session-ID on MCP requests")
	for _, h := range headersAfterInvestigation {
		assert.Equal(t, sessionID, h, "investigation MCP requests must carry session ID")
	}

	deletesAfterInvestigation := len(recorder.deletes())

	chatResp := app.SendChatMessage(t, sessionID, "Follow up with session MCP")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	require.Eventually(t, func() bool {
		return len(recorder.deletes()) > deletesAfterInvestigation
	}, 10*time.Second, 50*time.Millisecond, "expected chat cleanup DELETE")

	deletes := recorder.deletes()
	assert.Contains(t, deletes, sessionID)
	assert.GreaterOrEqual(t, len(deletes), 2, "cleanup should run after investigation and chat")

	for _, h := range recorder.headers() {
		assert.Equal(t, sessionID, h, "all MCP requests must carry session ID")
	}
}
