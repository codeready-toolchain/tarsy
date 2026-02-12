// Package testdata defines expected WebSocket event sequences for e2e tests.
//
// WS events are verified with structural assertions (not golden files) because
// the catchup/NOTIFY race makes exact event ordering non-deterministic.
// AssertEventsInOrder checks that each expected event appears in the actual
// events in the correct relative order, tolerating extra or duplicate events.
//
// Timeline events follow two lifecycle patterns (Streaming vs Fire-and-Forget).
// See pkg/events/types.go for the full protocol reference. Inline annotations
// in the event lists below mark which pattern each event follows.
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
// Three stages + synthesis:
//   1. investigation (DataCollector, NativeThinking)
//   2. remediation   (Remediator, ReAct)
//   3. validation    (ConfigValidator react ∥ MetricsValidator native-thinking, forced conclusion)
//      → validation - Synthesis (automatic)
// Two MCP servers (test-mcp, prometheus-mcp), tool call summarization,
// parallel agents, synthesis, forced conclusion, and executive summary.
// ────────────────────────────────────────────────────────────

var PipelineExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: investigation (DataCollector, native-thinking) ──
	{Type: "stage.status", StageName: "investigation", Status: "started"},

	// Iteration 1: thinking + intermediate response + two tool calls from test-mcp.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the cluster nodes and pod status.", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "I'll look up the nodes and pods.", Group: 1},

	// Tool call 1: test-mcp/get_nodes — small result, no summarization.
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_nodes",
		"arguments":   `{}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `[{"name":"worker-1","status":"Ready","cpu":"4","memory":"16Gi"}]`},

	// Tool call 2: test-mcp/get_pods — large result, triggers summarization.
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
		"arguments":   `{"namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call"}, // content is large tool output, verified via golden file
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary", Content: "Pod pod-1 is OOMKilled with 5 restarts."},

	// Iteration 2: thinking + tool call from prometheus-mcp.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the memory metrics for pod-1.", Group: 3},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Querying Prometheus for memory usage.", Group: 3},

	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_metrics",
		"arguments":   `{"query":"container_memory_usage_bytes{pod=\"pod-1\"}"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `[{"metric":"container_memory_usage_bytes","pod":"pod-1","value":"524288000","timestamp":"2026-01-15T14:29:00Z"}]`},

	// Iteration 3: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "The pod is clearly OOMKilled.", Group: 4},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts.", Group: 4},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},

	{Type: "stage.status", StageName: "investigation", Status: "completed"},

	// ── Stage 2: remediation (Remediator, react) ──
	// ReAct uses text-based tool calling: the full ReAct text (Thought + Action) streams
	// as llm_response. After parsing, a separate llm_thinking event is created (non-streaming).
	// Mirrors stage 1: tool call (no summary) → tool call (with summary) → final answer.
	{Type: "stage.status", StageName: "remediation", Status: "started"},

	// Iteration 1: test-mcp/get_pod_logs — small result, no summarization.
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: I should check the pod logs to understand the OOM pattern.\n" +
			"Action: test-mcp.get_pod_logs\n" +
			`Action Input: {"pod":"pod-1","namespace":"default"}`},
	// ReAct llm_thinking: completed (fire-and-forget) — parsed from streamed text, not itself streamed.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "completed",
		Content: "I should check the pod logs to understand the OOM pattern."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pod_logs",
		"arguments":   `{"pod":"pod-1","namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `{"pod":"pod-1","logs":"OOMKilled at 14:30:00 - memory usage exceeded 512Mi limit"}`},

	// Iteration 2: prometheus-mcp/query_alerts — large result, triggers summarization.
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: Let me check the Prometheus alert history for memory-related alerts.\n" +
			"Action: prometheus-mcp.query_alerts\n" +
			`Action Input: {"query":"ALERTS{alertname=\"OOMKilled\",pod=\"pod-1\"}"}`},
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "completed",
		Content: "Let me check the Prometheus alert history for memory-related alerts."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_alerts",
		"arguments":   `{"query":"ALERTS{alertname=\"OOMKilled\",pod=\"pod-1\"}"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call"}, // content is large alert output, verified via golden file
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Status: "streaming", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_alerts",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary",
		Content: "OOMKilled alert fired 3 times in the last hour for pod-1."},

	// Iteration 3: final answer.
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Thought: The logs and alerts confirm repeated OOM kills due to memory pressure.\n" +
			"Final Answer: Recommend increasing memory limit to 1Gi and adding a HPA for pod-1."},
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "completed",
		Content: "The logs and alerts confirm repeated OOM kills due to memory pressure."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Recommend increasing memory limit to 1Gi and adding a HPA for pod-1."},

	{Type: "stage.status", StageName: "remediation", Status: "completed"},

	// ── Stage 3: validation (ConfigValidator react ∥ MetricsValidator native-thinking) ──
	// Two agents run in parallel. Events from both agents interleave non-deterministically,
	// so all timeline events are in a single Group (matched in any order).
	{Type: "stage.status", StageName: "validation", Status: "started"},

	// --- ConfigValidator (react): iteration 1 — tool call ---
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 10},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 10,
		Content: "Thought: I should verify the pod memory limits are properly configured.\n" +
			"Action: test-mcp.get_resource_config\n" +
			`Action Input: {"pod":"pod-1","namespace":"default"}`},
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "completed", Group: 10,
		Content: "I should verify the pod memory limits are properly configured."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Group: 10, Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_resource_config",
		"arguments":   `{"pod":"pod-1","namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call", Group: 10,
		Content: `{"pod":"pod-1","limits":{"memory":"512Mi","cpu":"250m"},"requests":{"memory":"256Mi","cpu":"100m"}}`},
	// --- ConfigValidator (react): iteration 2 — final answer ---
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 10},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 10,
		Content: "Thought: The memory limit of 512Mi matches the alert threshold.\n" +
			"Final Answer: Config validated: pod-1 memory limit is 512Mi, matching the OOM threshold."},
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "completed", Group: 10,
		Content: "The memory limit of 512Mi matches the alert threshold."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed", Group: 10,
		Content: "Config validated: pod-1 memory limit is 512Mi, matching the OOM threshold."},

	// --- MetricsValidator (native-thinking, forced conclusion): iteration 1 — tool call ---
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 10},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 10},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 10,
		Content: "Let me verify the SLO metrics for pod-1."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 10,
		Content: "Checking SLO compliance."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Group: 10, Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_slo",
		"arguments":   `{"pod":"pod-1"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call", Group: 10,
		Content: `[{"slo":"availability","target":0.999,"current":0.95,"pod":"pod-1","violation":true}]`},
	// --- MetricsValidator (native-thinking): forced conclusion (max_iterations=1 exhausted) ---
	// All events from the forced conclusion path carry forced_conclusion metadata.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 10, Metadata: map[string]string{
		"forced_conclusion": "true",
	}},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 10, Metadata: map[string]string{
		"forced_conclusion": "true",
	}},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 10,
		Content: "SLO is being violated."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 10,
		Content: "Metrics confirm SLO violation for pod-1 availability."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed", Group: 10,
		Content: "Metrics confirm SLO violation for pod-1 availability.", Metadata: map[string]string{
			"forced_conclusion": "true",
			"iterations_used":   "1",
			"max_iterations":    "1",
		}},

	{Type: "stage.status", StageName: "validation", Status: "completed"},

	// ── Synthesis (automatic after parallel stage) ──
	{Type: "stage.status", StageName: "validation - Synthesis", Status: "started"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Combined validation confirms pod-1 has correct memory limit of 512Mi but violates 99.9% availability SLO."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Combined validation confirms pod-1 has correct memory limit of 512Mi but violates 99.9% availability SLO."},
	{Type: "stage.status", StageName: "validation - Synthesis", Status: "completed"},

	{Type: "session.status", Status: "completed"},
}
