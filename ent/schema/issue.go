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
		// Explicit FK so APIs can read parent_id without edge load.
		field.Int("parent_id").
			Optional().
			Nillable().
			Comment("상위 이슈 ID (없으면 루트)"),
		// Shepherd (목자) delegation tracking (#597).
		field.Bool("shepherd_watch").
			Default(false).
			Comment("목자가 위임하고 결과를 지켜보는 이슈인지 표시"),
		field.Int("shepherd_rounds").
			Default(0).
			Comment("이 이슈 때문에 목자가 깨어난 횟수"),
		field.Text("shepherd_last_review").
			Optional().
			Comment("목자의 마지막 검토 판단 요약 (7단계 보드에 표시)"),
		field.Time("shepherd_watched_at").
			Optional().
			Nillable().
			Comment("감시를 시작한 시각"),
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
		// Self-referential parent/children for epic → sub-issue hierarchy.
		edge.To("children", Issue.Type).
			From("parent").
			Unique().
			Field("parent_id").
			Comment("상위 이슈 (없으면 루트)"),
	}
}
