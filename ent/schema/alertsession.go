package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AlertSession holds the schema definition for the AlertSession entity.
type AlertSession struct {
	ent.Schema
}

// Mixin for custom ID field.
func (AlertSession) Mixin() []ent.Mixin {
	return []ent.Mixin{}
}

// Fields of the AlertSession.
func (AlertSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("session_id").
			Unique().
			Immutable(),
		field.Text("alert_data").
			Comment("Original alert payload (full-text searchable)"),
		field.String("agent_type").
			Comment("Agent type (e.g., 'kubernetes')"),
		field.String("alert_type").
			Optional().
			Comment("Alert classification"),
		field.Enum("status").
			Values("pending", "in_progress", "cancelling", "completed", "failed", "cancelled", "timed_out").
			Default("pending"),
		field.Time("created_at").
			Default(time.Now).
			Comment("When the session was submitted/created"),
		field.Time("started_at").
			Optional().
			Nillable().
			Comment("When the worker started processing (transitioned from pending to in_progress)"),
		field.Time("completed_at").
			Optional().
			Nillable(),
		field.String("error_message").
			Optional().
			Nillable(),
		field.Text("final_analysis").
			Optional().
			Nillable().
			Comment("Investigation summary (full-text searchable)"),
		field.Text("executive_summary").
			Optional().
			Nillable().
			Comment("Brief summary of investigation"),
		field.String("executive_summary_error").
			Optional().
			Nillable(),
		field.JSON("session_metadata", map[string]interface{}{}).
			Optional(),
		field.String("author").
			Optional().
			Nillable().
			Comment("From oauth2-proxy"),
		field.String("runbook_url").
			Optional().
			Nillable(),
		field.JSON("mcp_selection", map[string]interface{}{}).
			Optional().
			Comment("MCP override config"),
		field.String("chain_id").
			Comment("Chain identifier (live lookup, no snapshot)"),
		field.Int("current_stage_index").
			Optional().
			Nillable(),
		field.String("current_stage_id").
			Optional().
			Nillable(),
		field.String("pod_id").
			Optional().
			Nillable().
			Comment("For multi-replica coordination"),
		field.Time("last_interaction_at").
			Optional().
			Nillable().
			Comment("For orphan detection"),
		field.String("slack_message_fingerprint").
			Optional().
			Nillable().
			Comment("For Slack threading"),
		field.Time("deleted_at").
			Optional().
			Nillable().
			Comment("Soft delete for retention policy"),
	}
}

// Edges of the AlertSession.
func (AlertSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("stages", Stage.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
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
		edge.To("events", Event.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("chat", Chat.Type).
			Unique().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("session_scores", SessionScore.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

// Indexes of the AlertSession.
func (AlertSession) Indexes() []ent.Index {
	return []ent.Index{
		// Single field indexes
		index.Fields("status"),
		index.Fields("agent_type"),
		index.Fields("alert_type"),
		index.Fields("chain_id"),

		// Composite indexes
		index.Fields("status", "created_at"),
		index.Fields("status", "started_at"),
		index.Fields("status", "last_interaction_at"),

		// Partial index for soft deletes
		index.Fields("deleted_at").
			Annotations(entsql.IndexWhere("deleted_at IS NOT NULL")),
	}
}

// Annotations for PostgreSQL-specific features.
// Note: GIN indexes for full-text search are created via migration hooks
// in pkg/database/migrations.go
func (AlertSession) Annotations() []schema.Annotation {
	return []schema.Annotation{}
}
