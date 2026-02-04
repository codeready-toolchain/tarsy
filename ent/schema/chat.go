package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// Chat holds the schema definition for the Chat entity.
// Chat metadata for follow-up conversations.
type Chat struct {
	ent.Schema
}

// Fields of the Chat.
func (Chat) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("chat_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Unique().
			Immutable(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.String("created_by").
			Optional().
			Nillable().
			Comment("User email"),
		field.String("chain_id").
			Comment("From original session (live lookup, no snapshot)"),
		field.String("pod_id").
			Optional().
			Nillable().
			Comment("For multi-replica"),
		field.Time("last_interaction_at").
			Optional().
			Nillable().
			Comment("For orphan detection"),
	}
}

// Edges of the Chat.
func (Chat) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("chat").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.To("user_messages", ChatUserMessage.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("stages", Stage.Type),
	}
}

// Indexes of the Chat.
func (Chat) Indexes() []ent.Index {
	return []ent.Index{
		// Session lookup (unique)
		index.Fields("session_id").
			Unique(),
		// Listing
		index.Fields("created_at"),
		// Orphan detection
		index.Fields("pod_id", "last_interaction_at"),
	}
}
