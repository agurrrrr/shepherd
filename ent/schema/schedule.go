package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Schedule holds the schema definition for the Schedule entity.
type Schedule struct {
	ent.Schema
}

// Fields of the Schedule.
func (Schedule) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			Comment("스케줄 이름"),
		field.Text("prompt").
			NotEmpty().
			Comment("실행할 프롬프트"),
		field.Enum("schedule_type").
			Values("cron", "interval").
			Comment("스케줄 타입 (cron 또는 interval)"),
		field.String("cron_expr").
			Optional().
			Comment("크론 표현식 (예: 0 9 * * *)"),
		field.Int("interval_seconds").
			Optional().
			Comment("반복 간격 (초)"),
		field.Bool("enabled").
			Default(true).
			Comment("활성화 여부"),
		field.Time("last_run").
			Optional().
			Nillable().
			Comment("마지막 실행 시각"),
		field.Time("next_run").
			Optional().
			Nillable().
			Comment("다음 실행 예정 시각"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Schedule.
func (Schedule) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("schedules").
			Unique().
			Required(),
	}
}
