package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// MCPInteraction holds the schema definition for the MCPInteraction entity (Layer 4).
// Full technical details for MCP tool calls (Debug Tab - Observability).
type MCPInteraction struct {
	ent.Schema
}

// Fields of the MCPInteraction.
func (MCPInteraction) Fields() []ent.Field {
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
			Values("tool_call", "tool_list"),
		field.String("server_name").
			Comment("e.g., 'kubernetes', 'argocd'"),
		field.String("tool_name").
			Optional().
			Nillable().
			Comment("e.g., 'kubectl_get_pods'"),
		
		// Full Details
		field.JSON("tool_arguments", map[string]interface{}{}).
			Optional().
			Comment("Input parameters"),
		field.JSON("tool_result", map[string]interface{}{}).
			Optional().
			Comment("Tool output"),
		field.JSON("available_tools", []interface{}{}).
			Optional().
			Comment("For tool_list type"),
		
		// Result & Timing
		field.Int("duration_ms").
			Optional().
			Nillable(),
		field.String("error_message").
			Optional().
			Nillable().
			Comment("null = success, not-null = failed"),
	}
}

// Edges of the MCPInteraction.
func (MCPInteraction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("mcp_interactions").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.From("stage", Stage.Type).
			Ref("mcp_interactions").
			Field("stage_id").
			Unique().
			Required().
			Immutable(),
		edge.From("agent_execution", AgentExecution.Type).
			Ref("mcp_interactions").
			Field("execution_id").
			Unique().
			Required().
			Immutable(),
		edge.To("timeline_events", TimelineEvent.Type),
	}
}

// Indexes of the MCPInteraction.
func (MCPInteraction) Indexes() []ent.Index {
	return []ent.Index{
		// Agent's MCP calls chronologically
		index.Fields("execution_id", "created_at"),
		// Stage's MCP calls
		index.Fields("stage_id", "created_at"),
		// Primary lookups on id field (stored as interaction_id)
		index.Fields("id"),
	}
}
