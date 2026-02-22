package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Project holds the schema definition for the Project entity.
type Project struct {
	ent.Schema
}

// Fields of the Project.
func (Project) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("프로젝트 이름"),
		field.String("path").
			NotEmpty().
			Comment("프로젝트 경로"),
		field.String("description").
			Optional().
			Comment("프로젝트 설명"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Project.
func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sheep", Sheep.Type).
			Unique(),
		edge.To("tasks", Task.Type),
		edge.To("schedules", Schedule.Type),
		edge.To("skills", Skill.Type),
	}
}
