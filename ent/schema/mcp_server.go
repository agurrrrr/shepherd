package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// MCPServer holds the schema definition for the MCPServer entity.
// This represents an external MCP server that Shepherd connects to as a client.
type MCPServer struct {
	ent.Schema
}

// Fields of the MCPServer.
func (MCPServer) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("서버 고유 이름 (예: github, puppeteer)"),
		field.String("label").
			Optional().
			Comment("표시용 라벨"),
		field.Text("description").
			Optional().
			Comment("서버 설명"),
		field.Enum("transport").
			Values("stdio", "sse", "http").
			Default("stdio").
			Comment("연결 방식: stdio, sse, http"),
		field.String("command").
			Optional().
			Comment("stdio 명령어 (예: npx, uvx)"),
		field.String("args").
			Optional().
			Comment("stdio 인자 (JSON array, 예: [\"-y\", \"@modelcontextprotocol/server-github\"]"),
		field.String("url").
			Optional().
			Comment("SSE/HTTP URL (예: http://localhost:3001/mcp)"),
		field.String("env").
			Optional().
			Comment("환경변수 (JSON object, 예: {\"GITHUB_TOKEN\": \"ghp_xxx\"})"),
		field.Bool("enabled").
			Default(true).
			Comment("전역 활성화 여부"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}
