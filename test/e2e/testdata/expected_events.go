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
// Four stages + two synthesis stages + two chat messages:
//   1. investigation  (DataCollector, NativeThinking)
//   2. remediation    (Remediator, ReAct)
//   3. validation     (ConfigValidator react ∥ MetricsValidator native-thinking, forced conclusion)
//      → validation - Synthesis (synthesis-native-thinking)
//   4. scaling-review (ScalingReviewer x2 replicas, NativeThinking)
//      → scaling-review - Synthesis (plain synthesis)
//   + Chat 1: native-thinking with test-mcp tool call
//   + Chat 2: native-thinking with prometheus-mcp tool call
// Two MCP servers (test-mcp, prometheus-mcp), tool call summarization,
// parallel agents, replicas, both synthesis strategies, forced conclusion,
// executive summary, and follow-up chat with MCP tools.
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

	// ── Validation Synthesis (synthesis-native-thinking — includes thinking + Google Search) ──
	{Type: "stage.status", StageName: "validation - Synthesis", Status: "started"},
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "Combining ConfigValidator and MetricsValidator results.", Group: 11},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Combined validation confirms pod-1 has correct memory limit of 512Mi but violates 99.9% availability SLO.", Group: 11},
	// Google Search grounding — fire-and-forget event created after stream ends.
	{Type: "timeline_event.created", EventType: "google_search_result", Status: "completed",
		Content: "Google Search: 'kubernetes pod OOM memory limit best practices' → Sources: Resource Management for Pods and Containers (https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/)"},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Combined validation confirms pod-1 has correct memory limit of 512Mi but violates 99.9% availability SLO."},
	{Type: "stage.status", StageName: "validation - Synthesis", Status: "completed"},

	// ── Stage 4: scaling-review (ScalingReviewer x2 replicas, native-thinking) ──
	// Replicas run in parallel — events interleave non-deterministically.
	{Type: "stage.status", StageName: "scaling-review", Status: "started"},

	// ScalingReviewer-1 and ScalingReviewer-2 events (parallel — Group 20).
	// Both replicas produce identical output (interchangeable), so content matches
	// regardless of goroutine dispatch order.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 20},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 20},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 20,
		Content: "Evaluating horizontal scaling needs for pod-1."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 20,
		Content: "Current replicas=1 is insufficient. Recommend min=2 max=5 with 70% CPU target."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed", Group: 20,
		Content: "Current replicas=1 is insufficient. Recommend min=2 max=5 with 70% CPU target."},

	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 20},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 20},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 20,
		Content: "Evaluating horizontal scaling needs for pod-1."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 20,
		Content: "Current replicas=1 is insufficient. Recommend min=2 max=5 with 70% CPU target."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed", Group: 20,
		Content: "Current replicas=1 is insufficient. Recommend min=2 max=5 with 70% CPU target."},

	{Type: "stage.status", StageName: "scaling-review", Status: "completed"},

	// ── Scaling-review Synthesis (plain "synthesis" — no thinking) ──
	{Type: "stage.status", StageName: "scaling-review - Synthesis", Status: "started"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Both replicas confirm: set HPA to 70% CPU with min=2, max=5 replicas for pod-1."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Both replicas confirm: set HPA to 70% CPU with min=2, max=5 replicas for pod-1."},
	{Type: "stage.status", StageName: "scaling-review - Synthesis", Status: "completed"},

	{Type: "session.status", Status: "completed"},

	// ── Chat 1: "What caused the OOM?" (ChatAgent, native-thinking with test-mcp tool) ──
	{Type: "chat.created"},

	// user_question published via WS so the dashboard can render it.
	{Type: "timeline_event.created", EventType: "user_question", Status: "completed",
		Content: "What caused the OOM kill for pod-1?"},

	{Type: "stage.status", StageName: "Chat Response", Status: "started"},

	// Iteration 1: thinking + text + tool call to test-mcp/get_pods.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "The user wants to know the OOM root cause. Let me check current pod status.", Group: 30},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Let me check the current pod status to explain the OOM kill.", Group: 30},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
		"arguments":   `{"namespace":"default"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call"}, // content is large tool output
	// Tool result summarization for get_pods (triggered by size_threshold_tokens=100).
	{Type: "timeline_event.created", EventType: "mcp_tool_summary", Status: "streaming", Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "get_pods",
	}},
	{Type: "timeline_event.completed", EventType: "mcp_tool_summary",
		Content: "Pod pod-1 is OOMKilled with 5 restarts in default namespace."},

	// Iteration 2: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "The pod data confirms the OOM kill pattern.", Group: 31},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Pod-1 was OOM killed because it exceeded the 512Mi memory limit. The pod has restarted 5 times due to this issue.", Group: 31},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Pod-1 was OOM killed because it exceeded the 512Mi memory limit. The pod has restarted 5 times due to this issue."},

	{Type: "stage.status", StageName: "Chat Response", Status: "completed"},

	// ── Chat 2: "What are the current SLO metrics?" (ChatAgent, native-thinking with prometheus-mcp tool) ──
	// user_question published via WS so the dashboard can render it.
	{Type: "timeline_event.created", EventType: "user_question", Status: "completed",
		Content: "What are the current SLO metrics for pod-1?"},

	{Type: "stage.status", StageName: "Chat Response", Status: "started"},

	// Iteration 1: thinking + text + tool call to prometheus-mcp/query_slo.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "The user wants current SLO status. Let me query Prometheus.", Group: 32},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Let me check the current SLO metrics for pod-1.", Group: 32},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Metadata: map[string]string{
		"server_name": "prometheus-mcp",
		"tool_name":   "query_slo",
		"arguments":   `{"pod":"pod-1"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call",
		Content: `[{"slo":"availability","target":0.999,"current":0.95,"pod":"pod-1","violation":true}]`},

	// Iteration 2: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "The SLO data shows the availability target is not being met.", Group: 33},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "The current SLO metrics show pod-1 availability at 95%, well below the 99.9% target. This is a critical violation that needs immediate attention.", Group: 33},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "The current SLO metrics show pod-1 availability at 95%, well below the 99.9% target. This is a critical violation that needs immediate attention."},

	{Type: "stage.status", StageName: "Chat Response", Status: "completed"},
}

// ────────────────────────────────────────────────────────────
// Scenario: FailurePropagation
// Three-stage chain where stage 2 (policy=all) fails when one parallel
// agent's LLM returns an error. Fail-fast prevents stage 3 from starting.
//   1. preparation (Preparer, NativeThinking) — succeeds
//   2. parallel-check (CheckerA ∥ CheckerB, policy=all) — CheckerB errors → stage fails
//   3. final (Finalizer) — NEVER STARTS (fail-fast)
// ────────────────────────────────────────────────────────────

var FailurePropagationExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: preparation (Preparer, native-thinking) ──
	{Type: "stage.status", StageName: "preparation", Status: "started"},

	// Preparer: single iteration — thinking + response + final_analysis.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "Analyzing the alert data.", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Preparation complete: alert data reviewed and ready for parallel checks.", Group: 1},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Preparation complete: alert data reviewed and ready for parallel checks."},

	{Type: "stage.status", StageName: "preparation", Status: "completed"},

	// ── Stage 2: parallel-check (CheckerA succeeds ∥ CheckerB errors, policy=all) ──
	{Type: "stage.status", StageName: "parallel-check", Status: "started"},

	// CheckerA (native-thinking): succeeds — thinking + response + final_analysis.
	// CheckerB: LLM error on Generate() → no timeline events at all.
	// Since only CheckerA produces events, they don't need a Group.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "System status looks nominal.", Group: 2},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "CheckerA verification passed: all systems operational.", Group: 2},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "CheckerA verification passed: all systems operational."},

	{Type: "stage.status", StageName: "parallel-check", Status: "failed"},

	// ── Fail-fast: no stage 3 events, session fails ──
	{Type: "session.status", Status: "failed"},
}

// ────────────────────────────────────────────────────────────
// Scenario: FailureResilience
// Two-stage chain + exec summary failure exercising policy=any resilience
// and executive summary fail-open.
//   1. analysis (Analyzer ∥ Investigator, policy=any)
//      Analyzer: LLM error → fails (max_iterations=1)
//      Investigator: tool call + final answer → succeeds
//      → analysis - Synthesis (synthesis-native-thinking)
//   2. summary (Summarizer) — succeeds
//   Executive summary: LLM error → fail-open, session still completed
// ────────────────────────────────────────────────────────────

var FailureResilienceExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: analysis (Analyzer fails ∥ Investigator succeeds, policy=any) ──
	{Type: "stage.status", StageName: "analysis", Status: "started"},

	// Parallel agents — events interleave non-deterministically → Group 1.
	//
	// Analyzer: Generate() returns error → NativeThinking controller creates
	// a fire-and-forget "error" timeline event (no streaming phase).
	{Type: "timeline_event.created", EventType: "error", Status: "completed", Group: 1},

	// Investigator iteration 1: thinking + response + tool call.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 1},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 1,
		Content: "Let me check the system status."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 1,
		Content: "Checking system status to investigate the alert."},
	{Type: "timeline_event.created", EventType: "llm_tool_call", Status: "streaming", Group: 1, Metadata: map[string]string{
		"server_name": "test-mcp",
		"tool_name":   "check_status",
		"arguments":   `{"component":"api-server"}`,
	}},
	{Type: "timeline_event.completed", EventType: "llm_tool_call", Group: 1},

	// Investigator iteration 2: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming", Group: 1},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Group: 1,
		Content: "System check complete, API server is healthy."},
	{Type: "timeline_event.completed", EventType: "llm_response", Group: 1,
		Content: "Investigation complete: API server is healthy, alert was transient."},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed", Group: 1,
		Content: "Investigation complete: API server is healthy, alert was transient."},

	{Type: "stage.status", StageName: "analysis", Status: "completed"},

	// ── Synthesis: analysis - Synthesis (synthesis-native-thinking) ──
	{Type: "stage.status", StageName: "analysis - Synthesis", Status: "started"},

	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "One agent succeeded, one failed. Summarizing available results.", Group: 2},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Synthesis: Investigator confirmed API server is healthy. Analyzer failed due to LLM error.", Group: 2},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Synthesis: Investigator confirmed API server is healthy. Analyzer failed due to LLM error."},

	{Type: "stage.status", StageName: "analysis - Synthesis", Status: "completed"},

	// ── Stage 2: summary (Summarizer, native-thinking) ──
	{Type: "stage.status", StageName: "summary", Status: "started"},

	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "Creating final summary of the investigation.", Group: 3},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Summary: API server alert was transient. No action required.", Group: 3},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Summary: API server alert was transient. No action required."},

	{Type: "stage.status", StageName: "summary", Status: "completed"},

	// ── Executive summary: LLM error → fail-open ──
	// Executive summary produces NO WebSocket events (DB-only timeline event on success,
	// and nothing at all on failure). Session still completes.
	{Type: "session.status", Status: "completed"},
}

// ────────────────────────────────────────────────────────────
// Scenario: Cancellation — Session 1 (Investigation cancellation)
// Single stage with 2 parallel agents (policy=any), both BlockUntilCancelled.
// Test cancels the session while agents are blocked.
//   1. investigation (InvestigatorA ∥ InvestigatorB, policy=any)
//      Both agents block on BlockUntilCancelled → no streaming events created.
//      Cancel triggers context cancellation → agents + stage + session cancelled.
// ────────────────────────────────────────────────────────────

var CancellationInvestigationExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: investigation (InvestigatorA ∥ InvestigatorB, policy=any) ──
	{Type: "stage.status", StageName: "investigation", Status: "started"},

	// BlockUntilCancelled closes the channel without sending chunks, so
	// no streaming timeline events are created. The stage jumps straight
	// from started to cancelled once the cancel API is called.
	{Type: "stage.status", StageName: "investigation", Status: "cancelled"},
	{Type: "session.status", Status: "cancelled"},
}

// ────────────────────────────────────────────────────────────
// Scenario: Cancellation — Session 2 (Chat cancellation)
// Single-stage investigation completes normally, then chat is cancelled
// and a follow-up chat succeeds.
//   1. quick-check (QuickInvestigator, native-thinking) — succeeds
//   + Executive summary — succeeds
//   + Chat 1: BlockUntilCancelled → cancelled
//   + Chat 2: thinking + final answer → succeeds
// ────────────────────────────────────────────────────────────

var CancellationChatExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},

	// ── Stage 1: quick-check (QuickInvestigator, native-thinking) ──
	{Type: "stage.status", StageName: "quick-check", Status: "started"},

	// Single iteration: thinking + response + final_analysis.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "Quick check on the alert.", Group: 1},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Alert verified: system is stable, no action needed.", Group: 1},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Alert verified: system is stable, no action needed."},

	{Type: "stage.status", StageName: "quick-check", Status: "completed"},

	// Executive summary succeeds (no WS events — DB-only timeline event).
	{Type: "session.status", Status: "completed"},

	// ── Chat 1: BlockUntilCancelled → cancelled ──
	{Type: "chat.created"},
	{Type: "timeline_event.created", EventType: "user_question", Status: "completed",
		Content: "Ask a question"},
	{Type: "stage.status", StageName: "Chat Response", Status: "started"},
	// BlockUntilCancelled: no streaming events. Stage cancelled.
	{Type: "stage.status", StageName: "Chat Response", Status: "cancelled"},

	// ── Chat 2: follow-up succeeds ──
	{Type: "timeline_event.created", EventType: "user_question", Status: "completed",
		Content: "Follow-up question"},
	{Type: "stage.status", StageName: "Chat Response", Status: "started"},

	// Single iteration: thinking + response + final_analysis.
	{Type: "timeline_event.created", EventType: "llm_thinking", Status: "streaming"},
	{Type: "timeline_event.created", EventType: "llm_response", Status: "streaming"},
	{Type: "timeline_event.completed", EventType: "llm_thinking",
		Content: "Answering the follow-up.", Group: 2},
	{Type: "timeline_event.completed", EventType: "llm_response",
		Content: "Here is your follow-up answer: everything looks good.", Group: 2},
	{Type: "timeline_event.created", EventType: "final_analysis", Status: "completed",
		Content: "Here is your follow-up answer: everything looks good."},

	{Type: "stage.status", StageName: "Chat Response", Status: "completed"},
}
