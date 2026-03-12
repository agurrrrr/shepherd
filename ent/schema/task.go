package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Task holds the schema definition for the Task entity.
type Task struct {
	ent.Schema
}

// Fields of the Task.
func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.Text("prompt").
			NotEmpty().
			Comment("사용자 요청"),
		field.Text("summary").
			Optional().
			Comment("작업 요약 (AI 생성)"),
		field.JSON("files_modified", []string{}).
			Optional().
			Comment("수정한 파일 목록"),
		field.Enum("status").
			Values("pending", "running", "completed", "failed", "stopped").
			Default("pending").
			Comment("작업 상태"),
		field.Text("error").
			Optional().
			Comment("에러 메시지"),
		field.JSON("output", []string{}).
			Optional().
			Comment("작업 출력 로그"),
		field.Time("started_at").
			Optional().
			Comment("작업 시작 시간"),
		field.Time("completed_at").
			Optional().
			Comment("작업 완료 시간"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the Task.
func (Task) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("sheep", Sheep.Type).
			Ref("tasks").
			Unique(),
		edge.From("project", Project.Type).
			Ref("tasks").
			Unique(),
	}
}
