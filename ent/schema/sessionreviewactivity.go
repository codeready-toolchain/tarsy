package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SessionReviewActivity records workflow transitions for session review.
type SessionReviewActivity struct {
	ent.Schema
}

func (SessionReviewActivity) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("activity_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),
		field.String("actor").
			Comment("User who performed the action (X-Forwarded-User)"),
		field.Enum("action").
			Values("claim", "unclaim", "resolve", "reopen").
			Comment("What happened"),
		field.Enum("from_status").
			Values("needs_review", "in_progress", "resolved").
			Optional().
			Nillable().
			Comment("Review status before transition"),
		field.Enum("to_status").
			Values("needs_review", "in_progress", "resolved").
			Comment("Review status after transition"),
		field.Enum("resolution_reason").
			Values("actioned", "dismissed").
			Optional().
			Nillable().
			Comment("Set when action is resolve"),
		field.Text("note").
			Optional().
			Nillable().
			Comment("Free-text context"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

func (SessionReviewActivity) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("review_activities").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
	}
}

func (SessionReviewActivity) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("session_id", "created_at"),
	}
}
