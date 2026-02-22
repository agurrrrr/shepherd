package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// SheepName holds the schema definition for the SheepName entity.
type SheepName struct {
	ent.Schema
}

// Fields of the SheepName.
func (SheepName) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("양 이름"),
		field.Int("priority").
			Default(0).
			Comment("우선순위 (낮을수록 먼저 사용)"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the SheepName.
func (SheepName) Edges() []ent.Edge {
	return nil
}
