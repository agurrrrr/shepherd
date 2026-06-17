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
		field.String("model").
			Optional().
			Comment("작업별 모델 오버라이드 (provider/model 형식, OpenCode 동시작업 그룹 집계에 사용)"),
		field.JSON("output", []string{}).
			Optional().
			Comment("작업 출력 로그"),
		field.Float("cost_usd").
			Optional().
			Default(0).
			Comment("실행 비용 (USD)"),
		field.Int64("prompt_tokens").
			Optional().
			Default(0).
			Comment("입력 토큰 수"),
		field.Int64("completion_tokens").
			Optional().
			Default(0).
			Comment("출력 토큰 수"),
		field.Int("owner_pid").
			Optional().
			Default(0).
			Comment("작업을 실행 중인 프로세스 PID (소유권/생존 판별용)"),
		field.Int("priority").
			Optional().
			Default(0).
			Comment("큐 실행 우선순위 (높을수록 먼저 실행). 일반작업=0, 컨텍스트 핸드오프 후속작업=1 → 대기 중인 일반작업보다 앞서 실행됨"),
		field.Int("handoff_depth").
			Optional().
			Default(0).
			Comment("컨텍스트 핸드오프 체인 깊이 (후속작업 = 부모 + 1). 진척 없는 무한 핸드오프 루프 감지/알람용"),
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
		edge.From("issue", Issue.Type).
			Ref("tasks").
			Unique(),
	}
}
