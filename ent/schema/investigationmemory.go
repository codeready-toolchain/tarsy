package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InvestigationMemory holds reusable learnings extracted from scored investigations.
type InvestigationMemory struct {
	ent.Schema
}

func (InvestigationMemory) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("memory_id").
			Unique().
			Immutable(),
		field.String("project").
			NotEmpty().
			Default("default"),
		field.Text("content").
			NotEmpty(),
		field.Enum("category").
			Values("semantic", "episodic", "procedural"),
		field.Enum("valence").
			Values("positive", "negative", "neutral"),
		field.Float("confidence").
			Default(0.5).
			Min(0).
			Max(1),
		field.Int("seen_count").
			Default(1).
			NonNegative(),
		field.String("source_session_id").
			NotEmpty(),

		// Scope metadata (soft signals for retrieval ranking)
		field.String("alert_type").
			Optional().
			Nillable(),
		field.String("chain_id").
			Optional().
			Nillable(),

		// Lifecycle
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Time("last_seen_at").
			Default(time.Now),
		field.Bool("deprecated").
			Default(false),
	}
}

func (InvestigationMemory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("source_session", AlertSession.Type).
			Ref("memories").
			Field("source_session_id").
			Unique().
			Required(),
		edge.From("injected_into_sessions", AlertSession.Type).
			Ref("injected_memories"),
	}
}

func (InvestigationMemory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("project"),
		index.Fields("project", "deprecated"),
		index.Fields("source_session_id"),
		index.Fields("category"),
	}
}
