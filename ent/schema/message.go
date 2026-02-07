package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Message holds the schema definition for the Message entity (Layer 2).
// LLM conversation history (LLM context building).
type Message struct {
	ent.Schema
}

// Fields of the Message.
func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			StorageKey("message_id").
			Unique().
			Immutable(),
		field.String("session_id").
			Immutable(),
		field.String("stage_id").
			Immutable().
			Comment("Stage scoping"),
		field.String("execution_id").
			Immutable().
			Comment("Agent conversation"),

		// Message Details
		field.Int("sequence_number").
			Comment("Execution-scoped order"),
		field.Enum("role").
			Values("system", "user", "assistant", "tool"),
		field.Text("content").
			Comment("Message text"),

		// Tool-related fields for native function calling (Phase 3.1)
		field.JSON("tool_calls", []map[string]interface{}{}).
			Optional().
			Comment("For assistant messages: tool calls requested by LLM [{id, name, arguments}]"),
		field.String("tool_call_id").
			Optional().
			Nillable().
			Comment("For tool messages: links result to the original tool call"),
		field.String("tool_name").
			Optional().
			Nillable().
			Comment("For tool messages: name of the tool that was called"),

		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the Message.
func (Message) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("session", AlertSession.Type).
			Ref("messages").
			Field("session_id").
			Unique().
			Required().
			Immutable(),
		edge.From("stage", Stage.Type).
			Ref("messages").
			Field("stage_id").
			Unique().
			Required().
			Immutable(),
		edge.From("agent_execution", AgentExecution.Type).
			Ref("messages").
			Field("execution_id").
			Unique().
			Required().
			Immutable(),
		edge.To("llm_interactions", LLMInteraction.Type),
	}
}

// Indexes of the Message.
func (Message) Indexes() []ent.Index {
	return []ent.Index{
		// Agent conversation order
		index.Fields("execution_id", "sequence_number"),
		// Stage + agent scoping
		index.Fields("stage_id", "execution_id"),
	}
}
