package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AgentExecution holds the schema definition for the AgentExecution entity (Layer 0b).
// Represents individual agent work within a stage.
type AgentExecution struct {
	ent.Schema
}

// Fields of the AgentExecution.
func (AgentExecution) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("execution_id").
			Unique().
			Immutable(),
		field.String("stage_id").
			Immutable(),
		field.String("session_id").
			Immutable().
			Comment("Denormalized for performance"),
		
		// Agent Details
		field.String("agent_name").
			Comment("e.g., 'KubernetesAgent', 'ArgoCDAgent'"),
		field.Int("agent_index").
			Comment("1 for single, 1-N for parallel"),
		
		// Execution Status & Timing
		field.Enum("status").
			Values("pending", "active", "completed", "failed", "cancelled", "timed_out").
			Default("pending"),
		field.Time("started_at").
			Optional().
			Nillable(),
		field.Time("completed_at").
			Optional().
			Nillable(),
		field.Int("duration_ms").
			Optional().
			Nillable(),
		field.String("error_message").
			Optional().
			Nillable().
			Comment("Error details if failed"),
		
		// Agent Configuration
		field.String("iteration_strategy").
			Comment("e.g., 'react', 'native_thinking' (for observability)"),
	}
}

// Edges of the AgentExecution.
func (AgentExecution) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("stage", Stage.Type).
			Ref("agent_executions").
			Field("stage_id").
			Unique().
			Required().
			Immutable(),
		edge.From("session", AlertSession.Type).
			Ref("agent_executions").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.To("timeline_events", TimelineEvent.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("messages", Message.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("llm_interactions", LLMInteraction.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("mcp_interactions", MCPInteraction.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the AgentExecution.
func (AgentExecution) Indexes() []ent.Index {
	return []ent.Index{
		// Unique constraint for agent ordering within stage
		index.Fields("stage_id", "agent_index").
			Unique(),
		// Primary lookups on id field (stored as execution_id)
		index.Fields("id"),
		// Session-wide queries
		index.Fields("session_id"),
	}
}
