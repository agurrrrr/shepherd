package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// WikiPageVersion holds the schema definition for the WikiPageVersion entity.
type WikiPageVersion struct {
	ent.Schema
}

// Fields of the WikiPageVersion.
func (WikiPageVersion) Fields() []ent.Field {
	return []ent.Field{
		field.String("page_slug").
			NotEmpty().
			Comment("대상 위키 페이지 슬러그"),
		field.Text("content").
			Default("").
			Comment("페이지 내용 스냅샷"),
		field.String("summary").
			Default("내용 변경").
			Comment("변경 요약"),
		field.String("author").
			Default("").
			Comment("변경자"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// Edges of the WikiPageVersion.
func (WikiPageVersion) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("project", Project.Type).
			Ref("wiki_versions").
			Unique(),
	}
}
