package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SessionScore holds the schema definition for the SessionScore entity.
// Represents an LLM-judged quality score for an alert session.
type SessionScore struct {
	ent.Schema
}

// Fields of the SessionScore.
func (SessionScore) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("score_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),
		field.String("prompt_hash").
			Optional().
			Nillable().
			Comment("SHA256 hex of judge prompts used"),
		field.Int("total_score").
			Optional().
			Nillable().
			Comment("0-100, extracted from LLM response"),
		field.Text("score_analysis").
			Optional().
			Nillable(),
		field.Text("missing_tools_analysis").
			Optional().
			Nillable(),
		field.String("score_triggered_by").
			Comment("Who triggered scoring (from extractAuthor)"),
		field.Enum("status").
			Values("pending", "in_progress", "completed", "failed", "timed_out", "cancelled").
			Default("pending"),
		field.Time("started_at").
			Default(time.Now).
			Immutable().
			Comment("When scoring was triggered"),
		field.Time("completed_at").
			Optional().
			Nillable(),
		field.Text("error_message").
			Optional().
			Nillable(),
	}
}

// Edges of the SessionScore.
func (SessionScore) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("session_scores").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
	}
}

// Indexes of the SessionScore.
func (SessionScore) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("prompt_hash"),
		index.Fields("total_score"),
		index.Fields("status"),
		index.Fields("session_id", "status"),
		index.Fields("status", "started_at"),
		// Prevent duplicate in-progress scorings per session
		index.Fields("session_id").
			Unique().
			Annotations(entsql.IndexWhere("status IN ('pending', 'in_progress')")),
	}
}
