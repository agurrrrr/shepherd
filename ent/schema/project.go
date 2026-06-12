package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Project holds the schema definition for the Project entity.
type Project struct {
	ent.Schema
}

// Fields of the Project.
func (Project) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("프로젝트 이름"),
		field.String("path").
			NotEmpty().
			Comment("프로젝트 경로"),
		field.String("description").
			Optional().
			Comment("프로젝트 설명"),
		field.String("repo_url").
			Optional().
			Comment("git origin에서 추출한 저장소 HTTPS URL (거의 변하지 않으므로 캐시)"),
		field.JSON("mcp_servers", map[string]interface{}{}).
			Optional().
			Comment("프로젝트별 MCP 서버 활성화 설정: {server_name: {enabled: bool}}"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Project.
func (Project) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sheep", Sheep.Type).
			Unique(),
		edge.To("tasks", Task.Type),
		edge.To("schedules", Schedule.Type),
		edge.To("skills", Skill.Type),
		edge.To("wiki_pages", WikiPage.Type),
		edge.To("wiki_versions", WikiPageVersion.Type),
		edge.To("issues", Issue.Type),
	}
}
