package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Sheep holds the schema definition for the Sheep entity.
type Sheep struct {
	ent.Schema
}

// Fields of the Sheep.
func (Sheep) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("양 이름 (양동이, 양말이 등)"),
		field.Enum("status").
			Values("idle", "working", "error").
			Default("idle").
			Comment("양 상태"),
		field.String("session_id").
			Optional().
			Comment("Claude Code 세션 ID"),
		field.Enum("provider").
			Values("claude", "opencode", "auto").
			Default("claude").
			Comment("AI provider (claude, opencode, auto)"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Sheep.
func (Sheep) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("sheep").
			Unique(),
		edge.To("tasks", Task.Type),
		edge.To("browser_session", BrowserSession.Type).
			Unique(),
	}
}
