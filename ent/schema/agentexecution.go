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
		field.String("llm_backend").
			Comment("LLM backend used: 'google-native' or 'langchain' (for observability)"),
		field.String("llm_provider").
			Optional().
			Nillable().
			Comment("Resolved LLM provider name (for observability, e.g. 'gemini-2.5-pro')"),

		// Orchestrator sub-agent fields
		field.String("parent_execution_id").
			Optional().
			Nillable().
			Comment("For orchestrator sub-agents: links to the parent orchestrator execution"),
		field.Text("task").
			Optional().
			Nillable().
			Comment("Task description from orchestrator dispatch"),
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
		// Orchestrator sub-agent hierarchy (self-referential)
		edge.To("sub_agents", AgentExecution.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("parent", AgentExecution.Type).
			Ref("sub_agents").
			Field("parent_execution_id").
			Unique(),
	}
}

// Indexes of the AgentExecution.
func (AgentExecution) Indexes() []ent.Index {
	return []ent.Index{
		// NOTE: The unique constraint for agent ordering is enforced via two partial
		// indexes in PostgreSQL (see 20260225235224_add_orchestrator_sub_agent_fields.up.sql).
		// Ent/Atlas cannot express WHERE clauses, so the uniqueness is not declared here.
		//   UNIQUE(stage_id, agent_index) WHERE parent_execution_id IS NULL
		//   UNIQUE(parent_execution_id, agent_index) WHERE parent_execution_id IS NOT NULL

		// Session-wide queries
		index.Fields("session_id"),
		// Sub-agent lookups by parent
		index.Fields("parent_execution_id"),
	}
}
