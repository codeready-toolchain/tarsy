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
	Type      string // required: "session.status", "stage.status", etc.
	Status    string // optional: match if non-empty
	StageName string // optional: match if non-empty (for stage.status events)
	EventType string // optional: match if non-empty (for timeline_event.created)
	Content   string // optional: exact match on "content" field if non-empty
}

// ────────────────────────────────────────────────────────────
// Scenario: SingleStage
// Single NativeThinking agent: thinking + tool call + thinking + final answer + executive summary.
// ────────────────────────────────────────────────────────────

var SingleStageExpectedEvents = []ExpectedEvent{
	{Type: "session.status", Status: "in_progress"},
	{Type: "stage.status", StageName: "investigation", Status: "started"},

	// Iteration 1: thinking + intermediate response + tool call.
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "Let me check the pod status."},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "I'll look up the pods."},
	{Type: "timeline_event.created", EventType: "llm_tool_call"},
	{Type: "timeline_event.completed", EventType: "llm_tool_call", Content: `[{"name":"pod-1","status":"OOMKilled","restarts":5}]`},

	// Iteration 2: thinking + final answer.
	{Type: "timeline_event.created", EventType: "llm_thinking"},
	{Type: "timeline_event.created", EventType: "llm_response"},
	{Type: "timeline_event.completed", EventType: "llm_thinking", Content: "The pod is clearly OOMKilled."},
	{Type: "timeline_event.completed", EventType: "llm_response", Content: "Investigation complete: pod-1 is OOMKilled with 5 restarts."},
	{Type: "timeline_event.created", EventType: "final_analysis"},

	{Type: "stage.status", StageName: "investigation", Status: "completed"},
	{Type: "session.status", Status: "completed"},
}
