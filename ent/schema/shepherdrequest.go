package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShepherdRequest is one user-confirmation request raised by the shepherd or
// any other caller (#601). The shepherd acts autonomously only up to its
// autonomy tier; anything beyond it (stopping a task, deleting an issue) must
// go through a pending row here that the user approves or rejects on the
// Shepherd screen. Approving runs the action server-side and wakes a shepherd
// review task with the decision.
type ShepherdRequest struct {
	ent.Schema
}

// Fields of the ShepherdRequest.
func (ShepherdRequest) Fields() []ent.Field {
	return []ent.Field{
		field.String("kind").
			NotEmpty().
			Comment("조치 종류 (stop_task / execute_issue / delete_issue / other)"),
		field.String("project_name").
			Optional().
			Comment("대상 프로젝트 이름 (없으면 빈 문자열)"),
		field.Int("target_id").
			Default(0).
			Comment("대상 id (kind가 task면 작업 id, issue면 이슈 id, 없으면 0)"),
		field.Text("reason").
			Optional().
			Comment("목자가 요청한 이유"),
		field.String("status").
			Default("pending").
			Comment("pending / approved / rejected"),
		field.Int("requested_by_task_id").
			Default(0).
			Comment("요청을 만든 작업 id (0이면 호출자 미상)"),
		field.Time("created_at").
			Default(time.Now).
			Comment("요청이 만들어진 시각"),
		field.Time("decided_at").
			Optional().
			Nillable().
			Comment("승인/거절된 시각 (아직 미결정이면 NULL)"),
		field.Text("result").
			Optional().
			Comment("조치 실행 결과 또는 거절 사유"),
	}
}

// Indexes lets the panel's status=pending query and the per-day cost window
// avoid full table walks.
func (ShepherdRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("created_at"),
	}
}

// Edges of the ShepherdRequest. Kept edge-free on purpose: the request points
// at tasks/issues by plain ids so deleting a target never cascades here.
func (ShepherdRequest) Edges() []ent.Edge {
	return nil
}
