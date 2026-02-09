package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Stage holds the schema definition for the Stage entity (Layer 0a).
// Represents chain stage configuration and coordination.
type Stage struct {
	ent.Schema
}

// Fields of the Stage.
func (Stage) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("stage_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),

		// Stage Configuration
		field.String("stage_name").
			Comment("e.g., 'Initial Analysis', 'Deep Dive'"),
		field.Int("stage_index").
			Comment("Position in chain: 0, 1, 2..."),

		// Execution Mode
		field.Int("expected_agent_count").
			Comment("How many agents (1 for single, N for parallel)"),
		field.Enum("parallel_type").
			Values("multi_agent", "replica").
			Optional().
			Nillable().
			Comment("null if count=1, 'multi_agent'/'replica' if count>1"),
		field.Enum("success_policy").
			Values("all", "any").
			Optional().
			Nillable().
			Comment("null if count=1, 'all'/'any' if count>1"),

		// Stage-Level Status & Timing (aggregated from agent executions)
		field.Enum("status").
			Values("pending", "active", "completed", "failed", "timed_out", "cancelled").
			Default("pending"),
		field.Time("started_at").
			Optional().
			Nillable().
			Comment("When first agent started"),
		field.Time("completed_at").
			Optional().
			Nillable().
			Comment("When stage finished (any terminal state)"),
		field.Int("duration_ms").
			Optional().
			Nillable().
			Comment("Total stage duration"),
		field.String("error_message").
			Optional().
			Nillable().
			Comment("Aggregated error if stage failed/timed_out/cancelled"),

		// Chat Context (if applicable)
		field.String("chat_id").
			Optional().
			Nillable(),
		field.String("chat_user_message_id").
			Optional().
			Nillable(),
	}
}

// Edges of the Stage.
func (Stage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("stages").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.To("agent_executions", AgentExecution.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("timeline_events", TimelineEvent.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("messages", Message.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("llm_interactions", LLMInteraction.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("mcp_interactions", MCPInteraction.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("chat", Chat.Type).
			Ref("stages").
			Field("chat_id").
			Unique(),
		edge.From("chat_user_message", ChatUserMessage.Type).
			Ref("stage").
			Field("chat_user_message_id").
			Unique(),
	}
}

// Indexes of the Stage.
func (Stage) Indexes() []ent.Index {
	return []ent.Index{
		// Unique constraint for stage ordering within session
		index.Fields("session_id", "stage_index").
			Unique(),
	}
}
