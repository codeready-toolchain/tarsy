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
// Two stages: investigation (NativeThinking) → remediation (ReAct).
// Two MCP servers (test-mcp, prometheus-mcp), tool call summarization,
// and executive summary.
// ────────────────────────────────────────────────────────────

var PipelineExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: investigation (DataCollector, native-thinking) ──
	{Type: "stage.status", StageName: "investigation", Status: "started"},

	// Iteration 1: thinking + intermediate response + two tool calls from test-mcp.
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
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
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary", Content: "Pod pod-1 is OOMKilled with 5 restarts."},

	// Iteration 2: thinking + tool call from prometheus-mcp.
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the memory metrics for pod-1.", Group: 3},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Querying Prometheus for memory usage.", Group: 3},

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

	// ── Stage 2: remediation (Remediator, react) ──
	// ReAct uses text-based tool calling: the full ReAct text (Thought + Action) streams
	// as llm_response. After parsing, a separate llm_thinking event is created (non-streaming).
	// Mirrors stage 1: tool call (no summary) → tool call (with summary) → final answer.
	{Type: "stage.status", StageName: "remediation", Status: "started"},

	// Iteration 1: test-mcp/get_pod_logs — small result, no summarization.
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: I should check the pod logs to understand the OOM pattern.\n" +
			"Action: test-mcp.get_pod_logs\n" +
			`Action Input: {"pod":"pod-1","namespace":"default"}`},
	{Type: "timeline_event.created", EventType: "llm_thinking",
		Content: "I should check the pod logs to understand the OOM pattern."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pod_logs",
		"arguments":   `{"pod":"pod-1","namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `{"pod":"pod-1","logs":"OOMKilled at 14:30:00 - memory usage exceeded 512Mi limit"}`},

	// Iteration 2: prometheus-mcp/query_alerts — large result, triggers summarization.
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: Let me check the Prometheus alert history for memory-related alerts.\n" +
			"Action: prometheus-mcp.query_alerts\n" +
			`Action Input: {"query":"ALERTS{alertname=\"OOMKilled\",pod=\"pod-1\"}"}`},
	{Type: "timeline_event.created", EventType: "llm_thinking",
		Content: "Let me check the Prometheus alert history for memory-related alerts."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_alerts",
		"arguments":   `{"query":"ALERTS{alertname=\"OOMKilled\",pod=\"pod-1\"}"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call"}, // content is large alert output, verified via golden file
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_alerts",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary",
		Content: "OOMKilled alert fired 3 times in the last hour for pod-1."},

	// Iteration 3: final answer.
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: The logs and alerts confirm repeated OOM kills due to memory pressure.\n" +
			"Final Answer: Recommend increasing memory limit to 1Gi and adding a HPA for pod-1."},
	{Type: "timeline_event.created", EventType: "llm_thinking",
		Content: "The logs and alerts confirm repeated OOM kills due to memory pressure."},
	{Type: "timeline_event.created", EventType: "final_analysis"},

	{Type: "stage.status", StageName: "remediation", Status: "completed"},
	{Type: "session.status", Status: "completed"},
}
