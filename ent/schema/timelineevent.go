package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TimelineEvent holds the schema definition for the TimelineEvent entity (Layer 1).
// User-facing investigation timeline (UX-focused, streamed in real-time).
type TimelineEvent struct {
	ent.Schema
}

// Fields of the TimelineEvent.
func (TimelineEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("event_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),
		field.String("stage_id").
			Immutable().
			Comment("Stage grouping"),
		field.String("execution_id").
			Immutable().
			Comment("Which agent"),

		// Timeline Ordering
		field.Int("sequence_number").
			Comment("Order in timeline"),

		// Timestamps
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Creation timestamp"),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("Last update (for streaming)"),

		// Event Details
		//
		// Event types and their semantics:
		//   llm_thinking       — LLM reasoning/thought content. Covers both:
		//                        (a) Native model thinking (Gemini thinking feature) — metadata.source = "native"
		//                        (b) ReAct parsed thoughts ("Thought: ...") — metadata.source = "react"
		//                        Streamed to frontend (rendered differently per source).
		//                        NOT included in cross-stage context for sequential stages;
		//                        included for synthesis strategies.
		//   llm_response       — Regular LLM text during intermediate iterations. The LLM may produce
		//                        text alongside tool calls (native thinking) or as an intermediate step.
		//                        Maps to old TARSy's INTERMEDIATE_RESPONSE.
		//   llm_tool_call      — LLM requested a tool call (native function calling).
		//                        Metadata: tool_name, server_name, arguments.
		//   mcp_tool_call      — MCP tool execution (tool was invoked).
		//                        Metadata: tool_name, server_name.
		//   mcp_tool_summary   — MCP tool result summary for the timeline.
		//   user_question      — User question in chat mode.
		//   executive_summary  — High-level session summary.
		//   final_analysis     — Agent's final conclusion (no more iterations/tool calls).
		//                        Maps to old TARSy's FINAL_ANSWER. Used as primary context
		//                        for the next stage in sequential chains.
		field.Enum("event_type").
			Values(
				"llm_thinking",
				"llm_response",
				"llm_tool_call",
				"mcp_tool_call",
				"mcp_tool_summary",
				"user_question",
				"executive_summary",
				"final_analysis",
			),
		field.Enum("status").
			Values("streaming", "completed", "failed", "cancelled", "timed_out").
			Default("streaming"),
		field.Text("content").
			Comment("Event content (grows during streaming, updateable on completion)"),
		field.JSON("metadata", map[string]interface{}{}).
			Optional().
			Comment("Type-specific data (tool_name, server_name, etc.)"),

		// Debug Links (set on completion)
		field.String("llm_interaction_id").
			Optional().
			Nillable().
			Comment("Link to debug details"),
		field.String("mcp_interaction_id").
			Optional().
			Nillable().
			Comment("Link to debug details"),
	}
}

// Edges of the TimelineEvent.
func (TimelineEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("timeline_events").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.From("stage", Stage.Type).
			Ref("timeline_events").
			Field("stage_id").
			Unique().
			Required().
			Immutable(),
		edge.From("agent_execution", AgentExecution.Type).
			Ref("timeline_events").
			Field("execution_id").
			Unique().
			Required().
			Immutable(),
		edge.From("llm_interaction", LLMInteraction.Type).
			Ref("timeline_events").
			Field("llm_interaction_id").
			Unique(),
		edge.From("mcp_interaction", MCPInteraction.Type).
			Ref("timeline_events").
			Field("mcp_interaction_id").
			Unique(),
	}
}

// Indexes of the TimelineEvent.
func (TimelineEvent) Indexes() []ent.Index {
	return []ent.Index{
		// Timeline ordering
		index.Fields("session_id", "sequence_number"),
		// Stage timeline grouping
		index.Fields("stage_id", "sequence_number"),
		// Agent timeline filtering
		index.Fields("execution_id", "sequence_number"),
		// Updates by ID (stored as event_id)
		index.Fields("id"),
		// Chronological queries
		index.Fields("created_at"),
	}
}
