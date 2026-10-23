package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ShepherdPatrol records one patrol pass of the shepherd loop (#600). Every
// pass writes a row — including passes that found nothing — so the Shepherd
// screen can show when the code last looked and what it saw. `findings` is a
// JSON array of {fingerprint, kind, title} for every un-acked attention item
// present at that moment; the next pass diffs against it to decide which
// fingerprints are new. `task_id` is the woken shepherd patrol task (0 when
// nobody was woken), and `skipped_reason` explains why a wake was skipped
// (daily cap reached, a patrol task still running, wake disabled).
//
// Rows older than 30 days are pruned by the patrol pass itself.
type ShepherdPatrol struct {
	ent.Schema
}

// Fields of the ShepherdPatrol.
func (ShepherdPatrol) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Default(time.Now).
			Comment("순찰 실행 시각"),
		field.Text("findings").
			Default("").
			Comment("그 순간의 확인 필요 항목 JSON ([{fingerprint,kind,title,message}])"),
		field.Int("new_count").
			Default(0).
			Comment("직전 순찰 이후 새로 생긴 지문 수"),
		field.Int("task_id").
			Default(0).
			Comment("깨운 목자 patrol 작업 ID (0 = 깨우지 않음)"),
		field.String("skipped_reason").
			Default("").
			Comment("깨우기를 건너뛴 이유 (빈 문자열 = 건너뛰지 않음)"),
	}
}

// Indexes lets the 30-day prune and the "latest patrol" diff query scan by
// time without a full table walk.
func (ShepherdPatrol) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("created_at"),
	}
}

// Edges of the ShepherdPatrol.
func (ShepherdPatrol) Edges() []ent.Edge {
	return nil
}
