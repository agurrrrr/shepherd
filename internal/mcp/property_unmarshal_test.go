package mcp

import (
	"encoding/json"
	"testing"
)

// External MCP servers send JSON Schema forms that a plain struct decode
// rejects. One bad property used to fail the whole tools/list, so the server
// was silently dropped from the embedded session.
func TestToolsListLenientPropertySchema(t *testing.T) {
	raw := `{"tools":[{"name":"search","description":"d","inputSchema":{"type":"object","properties":{
		"q":{"type":"string","description":"query"},
		"note":{"type":["string","null"]},
		"opt":{"anyOf":[{"type":"null"},{"type":"integer"}]},
		"tags":{"type":"array","items":{"type":"string"}},
		"level":{"type":"integer","enum":[1,2,3]},
		"mode":{"type":"string","enum":["a","b"]}
	},"required":["q"]}}]}`

	var res ToolsListResult
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatalf("tools/list decode failed: %v", err)
	}
	if len(res.Tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(res.Tools))
	}
	props := res.Tools[0].InputSchema.Properties

	cases := map[string]string{"q": "string", "note": "string", "opt": "integer", "tags": "array", "level": "integer", "mode": "string"}
	for name, want := range cases {
		if got := props[name].Type; got != want {
			t.Errorf("%s: type = %q, want %q", name, got, want)
		}
	}
	if string(props["tags"].Items) != `{"type":"string"}` {
		t.Errorf("tags items = %s", props["tags"].Items)
	}
	if props["level"].Enum != nil {
		t.Errorf("non-string enum should be dropped, got %v", props["level"].Enum)
	}
	if len(props["mode"].Enum) != 2 {
		t.Errorf("string enum lost: %v", props["mode"].Enum)
	}
	if props["q"].Description != "query" {
		t.Errorf("description lost: %q", props["q"].Description)
	}
}
