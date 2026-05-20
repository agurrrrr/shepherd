package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// WikiPage holds the schema definition for the WikiPage entity.
type WikiPage struct {
	ent.Schema
}

// Fields of the WikiPage.
func (WikiPage) Fields() []ent.Field {
	return []ent.Field{
		field.String("slug").
			Unique().
			NotEmpty().
			Comment("페이지 고유 식별자 (예: architecture, patterns)"),
		field.String("title").
			NotEmpty().
			Comment("표시 제목"),
		field.Text("content").
			Default("").
			Comment("마크다운 내용"),
		field.Enum("category").
			Values("architecture", "patterns", "troubleshooting", "deployment", "lessons", "entity", "custom").
			Default("custom").
			Comment("카테고리"),
		field.Bool("auto_generated").
			Default(true).
			Comment("LLM 자동 생성 여부"),
		field.JSON("tags", []string{}).
			Optional().
			Comment("검색용 태그"),
		field.Time("last_updated_by_task").
			Optional().
			Comment("마지막 업데이트된 작업 시간"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the WikiPage.
func (WikiPage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("wiki_pages").
			Unique(),
	}
}
