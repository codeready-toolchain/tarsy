package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// ChatUserMessage holds the schema definition for the ChatUserMessage entity.
// User messages in chat conversations.
type ChatUserMessage struct {
	ent.Schema
}

// Fields of the ChatUserMessage.
func (ChatUserMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("message_id").
			Unique().
			Immutable(),
		field.String("chat_id").
			Immutable(),
		field.Text("content").
			Comment("Question text"),
		field.String("author").
			Comment("User email"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the ChatUserMessage.
func (ChatUserMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("chat", Chat.Type).
			Ref("user_messages").
			Field("chat_id").
			Unique().
			Required().
			Immutable(),
		edge.To("stage", Stage.Type).
			Unique().
			Comment("Response stage"),
	}
}

// Indexes of the ChatUserMessage.
func (ChatUserMessage) Indexes() []ent.Index {
	return []ent.Index{
		// Chat lookup
		index.Fields("chat_id"),
		// Message ordering
		index.Fields("created_at"),
	}
}
