// Package testdata defines expected WebSocket event sequences for e2e tests.
//
// WS events are verified with structural assertions (not golden files) because
// the catchup/NOTIFY race makes exact event ordering non-deterministic.
// AssertEventsInOrder checks that each expected event appears in the actual
// events in the correct relative order, tolerating extra or duplicate events.
package testdata

// ExpectedEvent defines a single expected WebSocket event for structural matching.
// Only non-empty fields are matched against actual events.
type ExpectedEvent struct {
	Type      string            // required: "session.status", "stage.status", etc.
	Status    string            // optional: match if non-empty
	StageName string            // optional: match if non-empty (for stage.status events)
	EventType string            // optional: match if non-empty (for timeline_event.created)
	Content   string            // optional: exact match on "content" field if non-empty
	Metadata  map[string]string // optional: partial match on metadata — only specified keys are checked
	Group     int               // optional: non-zero = events with same Group can match in any order
}

// ────────────────────────────────────────────────────────────
// Scenario: Pipeline
// Grows incrementally into the full pipeline test.
// Currently: single NativeThinking agent, two MCP servers (test-mcp,
// prometheus-mcp), three tool calls across two iterations, summarization,
// final answer, and executive summary.
// ────────────────────────────────────────────────────────────

var PipelineExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},
	{Type: "stage.status", StageName: "investigation", Status: "started"},

	// Iteration 1: thinking + intermediate response + two tool calls from test-mcp.
	// Created events are sequential (streaming callback order is deterministic).
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	// Completed events for thinking/response can arrive in either order (catchup/NOTIFY race).
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the cluster nodes and pod status.", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "I'll look up the nodes and pods.", Group: 1},

	// Tool call 1: test-mcp/get_nodes — small result, no summarization.
	{Type: "timeline_event.created", EventType: "llm_tool_call", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_nodes",
		"arguments":   `{}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `[{"name":"worker-1","status":"Ready","cpu":"4","memory":"16Gi"}]`},

	// Tool call 2: test-mcp/get_pods — large result, triggers summarization.
	{Type: "timeline_event.created", EventType: "llm_tool_call", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
		"arguments":   `{"namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call"}, // content is large tool output, verified via golden file
	// Tool result summarization (triggered by size_threshold_tokens=100 in config).
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary", Content: "Pod pod-1 is OOMKilled with 5 restarts."},

	// Iteration 2: thinking + tool call from prometheus-mcp (second MCP server).
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the memory metrics for pod-1.", Group: 3},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Querying Prometheus for memory usage.", Group: 3},

	// Tool call 3: prometheus-mcp/query_metrics — small result, no summarization.
	{Type: "timeline_event.created", EventType: "llm_tool_call", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_metrics",
		"arguments":   `{"query":"container_memory_usage_bytes{pod=\"pod-1\"}"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `[{"metric":"container_memory_usage_bytes","pod":"pod-1","value":"524288000","timestamp":"2026-01-15T14:29:00Z"}]`},

	// Iteration 3: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "The pod is clearly OOMKilled.", Group: 4},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts.", Group: 4},
	{Type: "timeline_event.created", EventType: "final_analysis"},

	{Type: "stage.status", StageName: "investigation", Status: "completed"},
	{Type: "session.status", Status: "completed"},
}
