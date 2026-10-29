package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
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
		field.String("provider_override").
			Optional().
			Comment("작업별 프로바이더 오버라이드 (#675). rate limit 핸드오프로 만들어진 후속작업이 양의 기본 프로바이더 대신 다른 프로바이더로 실행되게 한다. 비어 있으면 양의 provider를 그대로 사용"),
		field.String("shepherd_kind").
			Optional().
			Comment("목자 작업의 종류 (command=사용자 지시, review=위임 작업 검토, patrol=순찰). 목자 작업이 아니면 비어 있음 (#594)"),
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
		field.String("checkpoint_dir").
			Optional().
			Comment("체크포인트 스냅샷을 찍은 디렉터리의 절대 경로"),
		field.String("checkpoint_start").
			Optional().
			Comment("작업 시작 스냅샷 커밋 SHA"),
		field.String("checkpoint_end").
			Optional().
			Comment("작업 종료 스냅샷 커밋 SHA"),
		field.Text("checkpoint_error").
			Optional().
			Comment("체크포인트를 건너뛴 이유나 실패 메시지"),
		field.Time("reverted_at").
			Optional().
			Comment("작업 되돌리기 시각"),
		field.String("revert_pre").
			Optional().
			Comment("되돌리기 직전 스냅샷 SHA (되돌리기 취소용)"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Indexes of the Task.
//
// status is used by the dashboard/status counters (GROUP BY status) and
// created_at by the time-window scans; the composite index serves queries that
// filter on both (e.g. today's completed/failed counts).
//
// The partial unique index on the issue edge (#627) is the DB-level guard
// against double-enqueue: at most one pending/running task may be linked to an
// issue at any time. Every enqueue path (issue.Execute cascade, web execute,
// approved execute_issue request, retry / retry-from inheritance) inserts the
// issue link in the same INSERT, so the check-then-insert race between
// hasActiveTask and task creation is closed by this constraint instead of by
// application-level checks. NULL issue_tasks rows (plain queue work) are not
// covered — SQLite treats NULLs as distinct in unique indexes.
func (Task) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("created_at"),
		index.Fields("status", "created_at"),
		index.Edges("issue").
			Unique().
			Annotations(entsql.IndexWhere("status IN ('pending', 'running')")),
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
