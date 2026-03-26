package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Skill holds the schema definition for the Skill entity.
type Skill struct {
	ent.Schema
}

// Fields of the Skill.
func (Skill) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			NotEmpty().
			Comment("스킬 이름"),
		field.Text("description").
			Optional().
			Comment("스킬 설명"),
		field.Text("content").
			NotEmpty().
			Comment("스킬 내용 (마크다운)"),
		field.Enum("scope").
			Values("global", "project").
			Default("project").
			Comment("스킬 범위 (global 또는 project)"),
		field.Bool("enabled").
			Default(true).
			Comment("활성화 여부"),
		field.JSON("tags", []string{}).
			Optional().
			Comment("태그 목록"),
		field.Bool("bundled").
			Default(false).
			Comment("번들(내장) 스킬 여부"),
		field.String("effort").
			Optional().
			Default("").
			Comment("모델 추론 노력 수준 (low/medium/high)"),
		field.Int("max_turns").
			Optional().
			Default(0).
			Comment("최대 에이전트 턴 수 (0이면 제한 없음)"),
		field.JSON("disallowed_tools", []string{}).
			Optional().
			Comment("사용 금지 도구 목록"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Skill.
func (Skill) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("skills").
			Unique(),
	}
}
