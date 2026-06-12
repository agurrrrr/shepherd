package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Issue holds the schema definition for the Issue entity.
type Issue struct {
	ent.Schema
}

// Fields of the Issue.
func (Issue) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").
			NotEmpty().
			Comment("이슈 제목"),
		field.Enum("type").
			Values("design", "feature", "bug").
			Default("feature").
			Comment("이슈 타입"),
		field.Enum("status").
			Values("todo", "in_progress", "testing", "failed", "done").
			Default("todo").
			Comment("이슈 상태"),
		field.Text("body").
			Optional().
			Comment("이슈 상세 내용"),
		field.Text("goal").
			Optional().
			Comment("성공 여부 판단 기준"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Time("started_at").
			Optional().
			Nillable().
			Comment("최초 수행 시각"),
		field.Time("completed_at").
			Optional().
			Nillable().
			Comment("마감(성공/실패 확정) 시각"),
	}
}

// Edges of the Issue.
func (Issue) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("issues").
			Unique().
			Required(),
		edge.To("tasks", Task.Type).
			Comment("이슈로 수행된 Task 목록"),
	}
}
