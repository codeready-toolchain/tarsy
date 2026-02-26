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
			Optional().
			Nillable().
			Immutable().
			Comment("Stage grouping — nil for session-level events (e.g. executive_summary)"),
		field.String("execution_id").
			Optional().
			Nillable().
			Immutable().
			Comment("Which agent — nil for session-level events (e.g. executive_summary)"),
		field.String("parent_execution_id").
			Optional().
			Nillable().
			Immutable().
			Comment("For sub-agent events: the parent orchestrator's execution ID"),

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
		//                        (b) parsed thoughts from text format ("Thought: ...") — metadata.source = "langchain"
		//                        Streamed to frontend (rendered differently per source).
		//                        NOT included in cross-stage context for sequential stages;
		//                        included for synthesis strategies.
		//   llm_response       — Regular LLM text during intermediate iterations. The LLM may produce
		//                        text alongside tool calls (native thinking) or as an intermediate step.
		//   llm_tool_call      — Tool call lifecycle event. Created with status "streaming" when the
		//                        LLM requests a tool call (metadata: server_name, tool_name, arguments).
		//                        Completed with the storage-truncated raw result in content and
		//                        is_error in metadata after ToolExecutor.Execute() returns.
		//   mcp_tool_summary   — MCP tool result summary. Created with status "streaming"
		//                        when summarization starts. Completed with the LLM-generated summary.
		//                        Metadata: server_name, tool_name, original_tokens, summarization_model.
		//   error              — Error during iteration (LLM failure, tool failure, etc.).
		//   user_question      — User question in chat mode.
		//   executive_summary  — High-level session summary.
		//   final_analysis     — Agent's final conclusion (no more iterations/tool calls).
		//   task_assigned      — Task assigned to a sub-agent by an orchestrator.
		field.Enum("event_type").
			Values(
				"llm_thinking",
				"llm_response",
				"llm_tool_call",
				"mcp_tool_summary",
				"error",
				"user_question",
				"executive_summary",
				"final_analysis",
				"code_execution",
				"google_search_result",
				"url_context_result",
				"task_assigned",
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
			Comment("Link to trace details"),
		field.String("mcp_interaction_id").
			Optional().
			Nillable().
			Comment("Link to trace details"),
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
			Immutable(),
		edge.From("agent_execution", AgentExecution.Type).
			Ref("timeline_events").
			Field("execution_id").
			Unique().
			Immutable(),
		edge.From("parent_execution", AgentExecution.Type).
			Ref("sub_agent_timeline_events").
			Field("parent_execution_id").
			Unique().
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
		// Stage timeline grouping (stage_id is nullable; EQ predicates naturally exclude NULLs)
		index.Fields("stage_id", "sequence_number"),
		// Agent timeline filtering (execution_id is nullable; EQ predicates naturally exclude NULLs)
		index.Fields("execution_id", "sequence_number"),
		// Sub-agent event lookups by parent orchestrator
		index.Fields("parent_execution_id"),
		// Chronological queries
		index.Fields("created_at"),
	}
}
