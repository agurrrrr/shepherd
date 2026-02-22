package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// BrowserSession holds the schema definition for the BrowserSession entity.
type BrowserSession struct {
	ent.Schema
}

// Fields of the BrowserSession.
func (BrowserSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("user_data_dir").
			NotEmpty().
			Comment("브라우저 프로필 경로"),
		field.Bool("headless").
			Default(true).
			Comment("헤드리스 모드"),
		field.String("proxy").
			Optional().
			Comment("프록시 URL"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the BrowserSession.
func (BrowserSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("sheep", Sheep.Type).
			Ref("browser_session").
			Unique().
			Required(),
	}
}
