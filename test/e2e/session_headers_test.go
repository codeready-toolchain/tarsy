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

	"github.com/codeready-toolchain/tarsy/ent"
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

func setupSessionHeadersApp(t *testing.T, llm *ScriptedLLMClient) (*sessionMCPRecorder, *TestApp) {
	t.Helper()
	recorder := &sessionMCPRecorder{}
	mcpServer := startSessionScopedMCPServer(t, recorder)

	cfg := configs.Load(t, "session-headers")
	serverCfg, err := cfg.MCPServerRegistry.Get("session-mcp")
	require.NoError(t, err)
	serverCfg.Transport.URL = mcpServer.URL + "/mcp"
	serverCfg.Transport.SessionCleanupURL = mcpServer.URL + "/sessions/{{.SESSION_ID}}"

	factory := mcp.NewClientFactory(cfg.MCPServerRegistry, nil)
	app := NewTestApp(t,
		WithConfig(cfg),
		WithLLMClient(llm),
		WithMCPFactory(factory),
	)
	return recorder, app
}

func executionIDs(execs []*ent.AgentExecution) map[string]struct{} {
	ids := make(map[string]struct{}, len(execs))
	for _, e := range execs {
		ids[e.ID] = struct{}{}
	}
	return ids
}

func findExecByName(t *testing.T, execs []*ent.AgentExecution, name string) *ent.AgentExecution {
	t.Helper()
	for _, e := range execs {
		if e.AgentName == name {
			return e
		}
	}
	require.FailNow(t, "execution not found", "agent_name=%s", name)
	return nil
}

func assertHeadersAreExecutionIDs(t *testing.T, headers []string, execIDs map[string]struct{}, investigationID string) {
	t.Helper()
	require.NotEmpty(t, headers, "expected X-Session-ID on MCP requests")
	for _, h := range headers {
		assert.NotEqual(t, investigationID, h, "investigation session ID must not be used as X-Session-ID")
		assert.NotEmpty(t, h, "X-Session-ID must not be blank")
		_, ok := execIDs[h]
		assert.True(t, ok, "X-Session-ID %q must be an agent execution ID", h)
	}
}

func waitForDeletes(t *testing.T, recorder *sessionMCPRecorder, wantIDs ...string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got := recorder.deletes()
		for _, want := range wantIDs {
			found := false
			for _, id := range got {
				if id == want {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}, 10*time.Second, 50*time.Millisecond,
		"expected DELETE for execution IDs %v, got %v", wantIDs, recorder.deletes())
}

// TestE2E_SessionHeadersAndCleanup verifies per-agent-execution sandbox keys:
//   - X-Session-ID is the agent execution ID (not investigation session ID)
//   - DELETE /sessions/{executionID} runs when that execution's MCP client closes
//   - Chat uses a distinct chat execution ID
func TestE2E_SessionHeadersAndCleanup(t *testing.T) {
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
	// Executive summary (always runs; fail-open if missing — keep scripts deterministic).
	llm.AddSequential(LLMScriptEntry{Text: "Session MCP investigation summary."})
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

	recorder, app := setupSessionHeadersApp(t, llm)

	resp := app.SubmitAlert(t, "test-session-headers", "session header probe")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	invExecs := app.QueryExecutions(t, sessionID)
	require.NotEmpty(t, invExecs)
	invAgent := findExecByName(t, invExecs, "SessionAgent")
	waitForDeletes(t, recorder, invAgent.ID)

	headersAfterInvestigation := recorder.headers()
	assertHeadersAreExecutionIDs(t, headersAfterInvestigation, executionIDs(invExecs), sessionID)
	for _, h := range headersAfterInvestigation {
		assert.Equal(t, invAgent.ID, h, "investigation MCP traffic must use SessionAgent execution ID")
	}

	deletesAfterInvestigation := len(recorder.deletes())
	assert.NotContains(t, recorder.deletes(), sessionID)

	chatResp := app.SendChatMessage(t, sessionID, "Follow up with session MCP")
	chatStageID := chatResp["stage_id"].(string)
	require.NotEmpty(t, chatStageID)
	app.WaitForStageStatus(t, chatStageID, "completed")

	allExecs := app.QueryExecutions(t, sessionID)
	chatAgent := findExecByName(t, allExecs, "ChatAgent")
	assert.NotEqual(t, invAgent.ID, chatAgent.ID, "chat must create its own agent execution")

	waitForDeletes(t, recorder, chatAgent.ID)
	require.Greater(t, len(recorder.deletes()), deletesAfterInvestigation)

	assert.Contains(t, recorder.deletes(), invAgent.ID)
	assert.Contains(t, recorder.deletes(), chatAgent.ID)
	assert.NotContains(t, recorder.deletes(), sessionID)

	assertHeadersAreExecutionIDs(t, recorder.headers(), executionIDs(allExecs), sessionID)
}

// TestE2E_SessionHeaders_ParallelAgents verifies parallel stage agents get
// distinct sandbox session IDs and each is cleaned up independently.
func TestE2E_SessionHeaders_ParallelAgents(t *testing.T) {
	llm := NewScriptedLLMClient()

	llm.AddRouted("SessionAgentA", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "a-1", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	llm.AddRouted("SessionAgentA", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "AgentA done."},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	llm.AddRouted("SessionAgentB", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "b-1", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	llm.AddRouted("SessionAgentB", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "AgentB done."},
			&agent.UsageChunk{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	})
	// Mandatory synthesis after multi-agent stage.
	llm.AddSequential(LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Combined: both agents confirmed session MCP isolation."},
			&agent.UsageChunk{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "Parallel session MCP summary."})

	recorder, app := setupSessionHeadersApp(t, llm)

	resp := app.SubmitAlert(t, "test-session-headers-parallel", "parallel session header probe")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "completed")

	execs := app.QueryExecutions(t, sessionID)
	agentA := findExecByName(t, execs, "SessionAgentA")
	agentB := findExecByName(t, execs, "SessionAgentB")
	assert.NotEqual(t, agentA.ID, agentB.ID)

	waitForDeletes(t, recorder, agentA.ID, agentB.ID)

	uniqueHeaders := map[string]struct{}{}
	for _, h := range recorder.headers() {
		uniqueHeaders[h] = struct{}{}
	}
	assert.Contains(t, uniqueHeaders, agentA.ID)
	assert.Contains(t, uniqueHeaders, agentB.ID)
	assert.NotContains(t, uniqueHeaders, sessionID)
	assert.Len(t, uniqueHeaders, 2, "exactly two sandbox IDs for two parallel agents")

	assertHeadersAreExecutionIDs(t, recorder.headers(), executionIDs(execs), sessionID)
	deletes := recorder.deletes()
	assert.NotContains(t, deletes, sessionID)
	assert.ElementsMatch(t, []string{agentA.ID, agentB.ID}, deletes,
		"exactly one DELETE per parallel agent execution")
}

// TestE2E_SessionHeaders_SubAgents verifies orchestrator parent and sub-agent
// each get their own sandbox session ID and cleanup DELETE.
func TestE2E_SessionHeaders_SubAgents(t *testing.T) {
	llm := NewScriptedLLMClient()

	subAgentGate := make(chan struct{})
	orchIter2Gate := make(chan struct{})
	orchIter2Ready := make(chan struct{}, 1)

	// Iteration 1: parent uses session-mcp then dispatches SessionWorker.
	llm.AddRouted("SessionOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "orch-echo", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.ToolCallChunk{CallID: "orch-dispatch", Name: "dispatch_agent",
				Arguments: `{"name":"SessionWorker","task":"Probe session MCP in isolation"}`},
			&agent.UsageChunk{InputTokens: 50, OutputTokens: 20, TotalTokens: 70},
		},
	})
	// Iteration 2: wait for sub-agent.
	llm.AddRouted("SessionOrchestrator", LLMScriptEntry{
		WaitCh:  orchIter2Gate,
		OnBlock: orchIter2Ready,
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Waiting for SessionWorker."},
			&agent.UsageChunk{InputTokens: 60, OutputTokens: 10, TotalTokens: 70},
		},
	})
	// Iteration 3: final answer.
	llm.AddRouted("SessionOrchestrator", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "Investigation complete: parent and sub-agent used isolated sandboxes."},
			&agent.UsageChunk{InputTokens: 80, OutputTokens: 20, TotalTokens: 100},
		},
	})

	llm.AddRouted("SessionWorker", LLMScriptEntry{
		WaitCh: subAgentGate,
		Chunks: []agent.Chunk{
			&agent.ToolCallChunk{CallID: "worker-echo", Name: "session-mcp__echo", Arguments: `{}`},
			&agent.UsageChunk{InputTokens: 30, OutputTokens: 10, TotalTokens: 40},
		},
	})
	llm.AddRouted("SessionWorker", LLMScriptEntry{
		Chunks: []agent.Chunk{
			&agent.TextChunk{Content: "SessionWorker finished with isolated session MCP."},
			&agent.UsageChunk{InputTokens: 40, OutputTokens: 15, TotalTokens: 55},
		},
	})
	llm.AddSequential(LLMScriptEntry{Text: "Orchestrator session MCP summary."})

	recorder, app := setupSessionHeadersApp(t, llm)

	resp := app.SubmitAlert(t, "test-session-headers-orchestrator", "orchestrator session header probe")
	sessionID := resp["session_id"].(string)
	require.NotEmpty(t, sessionID)

	app.WaitForSessionStatus(t, sessionID, "in_progress")
	go func() {
		<-orchIter2Ready
		baseline, err := app.CountLLMInteractions(sessionID)
		if err != nil {
			t.Errorf("CountLLMInteractions failed: %v", err)
		}
		close(orchIter2Gate)
		if !app.AwaitLLMInteractionIncrease(sessionID, baseline) {
			t.Errorf("AwaitLLMInteractionIncrease timed out (baseline=%d)", baseline)
		}
		close(subAgentGate)
	}()

	app.WaitForSessionStatus(t, sessionID, "completed")

	execs := app.QueryExecutions(t, sessionID)
	orch := findExecByName(t, execs, "SessionOrchestrator")
	worker := findExecByName(t, execs, "SessionWorker")
	require.NotNil(t, worker.ParentExecutionID)
	assert.Equal(t, orch.ID, *worker.ParentExecutionID)
	assert.NotEqual(t, orch.ID, worker.ID)

	waitForDeletes(t, recorder, orch.ID, worker.ID)

	uniqueHeaders := map[string]struct{}{}
	for _, h := range recorder.headers() {
		uniqueHeaders[h] = struct{}{}
	}
	assert.Contains(t, uniqueHeaders, orch.ID, "parent must send its execution ID as X-Session-ID")
	assert.Contains(t, uniqueHeaders, worker.ID, "sub-agent must send its execution ID as X-Session-ID")
	assert.NotContains(t, uniqueHeaders, sessionID)

	assertHeadersAreExecutionIDs(t, recorder.headers(), executionIDs(execs), sessionID)
	assert.NotContains(t, recorder.deletes(), sessionID)
	assert.GreaterOrEqual(t, len(uniqueHeaders), 2, "expected at least parent + sub sandbox IDs")
}
