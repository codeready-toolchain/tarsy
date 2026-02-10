package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// LLMInteraction holds the schema definition for the LLMInteraction entity (Layer 3).
// Full technical details for LLM calls (Debug Tab - Observability).
type LLMInteraction struct {
	ent.Schema
}

// Fields of the LLMInteraction.
func (LLMInteraction) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("interaction_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),
		field.String("stage_id").
			Immutable(),
		field.String("execution_id").
			Immutable().
			Comment("Which agent"),

		// Timing
		field.Time("created_at").
			Default(time.Now).
			Immutable(),

		// Interaction Details
		field.Enum("interaction_type").
			Values("iteration", "final_analysis", "executive_summary", "chat_response", "summarization"),
		field.String("model_name").
			Comment("e.g., 'gemini-2.0-flash-thinking-exp'"),

		// Conversation Context (links to Message table)
		field.String("last_message_id").
			Optional().
			Nillable().
			Comment("Last message sent to LLM"),

		// Full API Details
		field.JSON("llm_request", map[string]interface{}{}).
			Comment("Full API request payload"),
		field.JSON("llm_response", map[string]interface{}{}).
			Comment("Full API response payload"),
		field.Text("thinking_content").
			Optional().
			Nillable().
			Comment("Native thinking (Gemini)"),
		field.JSON("response_metadata", map[string]interface{}{}).
			Optional().
			Comment("Grounding, tool usage, etc."),

		// Metrics & Result
		field.Int("input_tokens").
			Optional().
			Nillable(),
		field.Int("output_tokens").
			Optional().
			Nillable(),
		field.Int("total_tokens").
			Optional().
			Nillable(),
		field.Int("duration_ms").
			Optional().
			Nillable(),
		field.String("error_message").
			Optional().
			Nillable().
			Comment("null = success, not-null = failed"),
	}
}

// Edges of the LLMInteraction.
func (LLMInteraction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("llm_interactions").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.From("stage", Stage.Type).
			Ref("llm_interactions").
			Field("stage_id").
			Unique().
			Required().
			Immutable(),
		edge.From("agent_execution", AgentExecution.Type).
			Ref("llm_interactions").
			Field("execution_id").
			Unique().
			Required().
			Immutable(),
		edge.From("last_message", Message.Type).
			Ref("llm_interactions").
			Field("last_message_id").
			Unique(),
		edge.To("timeline_events", TimelineEvent.Type),
	}
}

// Indexes of the LLMInteraction.
func (LLMInteraction) Indexes() []ent.Index {
	return []ent.Index{
		// Agent's LLM calls chronologically
		index.Fields("execution_id", "created_at"),
		// Stage's LLM calls
		index.Fields("stage_id", "created_at"),
	}
}
