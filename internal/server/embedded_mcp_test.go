package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/mcp"
)

// The guide must name each project-enabled external MCP server. Without the
// name the model could call the tools but told the user the server "is not
// registered in this session".
func TestBuildMCPGuideNamesExternalServers(t *testing.T) {
	defs := []embedded.MCPToolDef{
		{Name: "get_status", Description: "shepherd status"},
		{Name: "wiki_read_page", Description: "read wiki"},
		{Name: "get_issue", Description: "YouTrack issue"},
		{Name: "create_issue", Description: "create YouTrack issue"},
	}
	external := []externalMCPServerStatus{
		{Name: "atsel", Label: "ATSEL", Description: "YOUTRACK MCP", Tools: []string{"get_issue", "create_issue"}},
		{Name: "dtc", Err: "MCP server \"dtc\" start: exec: \"dtc\": executable file not found in $PATH"},
	}
	g := buildMCPGuide(defs, "bori-api", external)

	for _, want := range []string{
		"[Connected External MCP Servers]",
		`MCP server "atsel" (ATSEL) — YOUTRACK MCP:`,
		"- get_issue: YouTrack issue",
		"- create_issue: create YouTrack issue",
		`MCP server "dtc":`,
		"failed to connect",
		"executable file not found",
		"Project: bori-api",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("guide missing %q\n%s", want, g)
		}
	}

	// get_issue belongs to atsel, not to Shepherd's task management.
	taskSection := g[strings.Index(g, "Task management:"):strings.Index(g, "[Connected External MCP Servers]")]
	if strings.Contains(taskSection, "get_issue") {
		t.Errorf("external get_issue listed under Shepherd task management:\n%s", taskSection)
	}
	if !strings.Contains(taskSection, "get_status") {
		t.Errorf("built-in get_status missing from task management:\n%s", taskSection)
	}
}

// A filtered tool set (MAGI proposers, sub-agents) lists only its tools.
func TestBuildMCPGuideFilteredExternalTools(t *testing.T) {
	defs := []embedded.MCPToolDef{{Name: "get_issue", Description: "read"}}
	external := []externalMCPServerStatus{
		{Name: "atsel", Tools: []string{"get_issue", "create_issue"}},
		{Name: "writer", Tools: []string{"delete_all"}},
	}
	g := buildMCPGuide(defs, "", external)
	if strings.Contains(g, "create_issue") || strings.Contains(g, "delete_all") {
		t.Errorf("filtered-out tools must not be listed:\n%s", g)
	}
	if !strings.Contains(g, `MCP server "writer":`) || !strings.Contains(g, "No tools of this server are available") {
		t.Errorf("server with no visible tools should say so:\n%s", g)
	}
}

func TestBuildMCPGuideNoExternal(t *testing.T) {
	g := buildMCPGuide([]embedded.MCPToolDef{{Name: "get_status"}}, "p", nil)
	if strings.Contains(g, "[Connected External MCP Servers]") {
		t.Errorf("no external section expected:\n%s", g)
	}
}

// A server that fails to start is recorded with its error, so the task log
// and the guide show why an ON server is missing from the session.
func TestAddServersRecordsFailure(t *testing.T) {
	set := externalMCPSet{servers: map[string]*mcp.ExternalMCPServer{}, toolOwner: map[string]string{}}
	fail := func(*ent.MCPServer) (*mcp.ExternalMCPServer, error) { return nil, errors.New("boom") }
	set.addServers([]*ent.MCPServer{{Name: "dtc"}}, fail, "[test]")

	if len(set.status) != 1 || set.status[0].Err != "boom" {
		t.Fatalf("status = %+v", set.status)
	}
	if set.owns("anything") {
		t.Error("failed server must not own tools")
	}
	if line := set.summaryLine(); !strings.Contains(line, "dtc (failed: boom)") {
		t.Errorf("summary = %q", line)
	}
	empty := externalMCPSet{}
	if empty.summaryLine() != "" {
		t.Error("no servers → no summary line")
	}
}

// Copilot (OpenAI-style validation) rejects an array schema without items.
func TestToEmbeddedMCPDefArrayItemsAndEnum(t *testing.T) {
	var tool mcp.Tool
	raw := `{"name":"t","inputSchema":{"type":"object","properties":{
		"tags":{"type":"array","items":{"type":"string"}},
		"ids":{"type":"array"},
		"mode":{"type":"string","enum":["a","b"]},
		"any":{}
	}}}`
	if err := json.Unmarshal([]byte(raw), &tool); err != nil {
		t.Fatal(err)
	}
	def := toEmbeddedMCPDef(tool)
	props := def.Parameters["properties"].(map[string]interface{})

	tags := props["tags"].(map[string]interface{})
	if items, _ := tags["items"].(map[string]interface{}); items["type"] != "string" {
		t.Errorf("tags items = %v", tags["items"])
	}
	ids := props["ids"].(map[string]interface{})
	if _, ok := ids["items"].(map[string]interface{}); !ok {
		t.Errorf("array without items must get items {}: %v", ids)
	}
	if mode := props["mode"].(map[string]interface{}); len(mode["enum"].([]string)) != 2 {
		t.Errorf("enum lost: %v", mode)
	}
	if _, ok := props["any"].(map[string]interface{})["type"]; ok {
		t.Error("empty type must be omitted")
	}
	if def.Parameters["required"] == nil {
		t.Error("required must be [] not nil")
	}
}
