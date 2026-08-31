package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/mcpserver"
	"github.com/agurrrrr/shepherd/internal/db"
)

// seedMCPServer registers a global MCP server with the given master switch.
func seedMCPServer(t *testing.T, client *ent.Client, name string, enabled bool) *ent.MCPServer {
	t.Helper()
	return client.MCPServer.Create().
		SetName(name).
		SetTransport(mcpserver.TransportStdio).
		SetCommand("echo").
		SetEnabled(enabled).
		SaveX(context.Background())
}

// seedProject registers a project (optionally with per-project MCP settings).
func seedProject(t *testing.T, client *ent.Client, name string, settings map[string]interface{}) {
	t.Helper()
	create := client.Project.Create().SetName(name).SetPath("/tmp/" + name)
	if settings != nil {
		create = create.SetMcpServers(settings)
	}
	create.SaveX(context.Background())
}

func newProjectMCPApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{}
	app := fiber.New()
	app.Get("/api/projects/:name/mcp-servers", s.handleGetProjectMCPServers)
	app.Put("/api/projects/:name/mcp-servers", s.handleUpdateProjectMCPServers)
	return app
}

type projectMCPEntry struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	ProjectEnabled bool   `json:"project_enabled"`
}

func getProjectMCPServers(t *testing.T, app *fiber.App, project string) []projectMCPEntry {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+project+"/mcp-servers", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	var payload struct {
		Success bool               `json:"success"`
		Data    []projectMCPEntry  `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if !payload.Success {
		t.Fatalf("expected success=true: %s", body)
	}
	return payload.Data
}

func findEntry(entries []projectMCPEntry, name string) (projectMCPEntry, bool) {
	for _, e := range entries {
		if e.Name == name {
			return e, true
		}
	}
	return projectMCPEntry{}, false
}

// A globally enabled server must be OFF for a project that has no per-project
// settings — the Settings tab no longer shows every switch ON by default.
func TestProjectMCPDefaultsToOff(t *testing.T) {
	withServerTestDB(t)
	client := db.Client()

	seedProject(t, client, "demo", nil)
	srv := seedMCPServer(t, client, "github", true)

	if projectMCPEnabled(srv, nil) {
		t.Errorf("server without project settings should default to off")
	}
	if projectMCPEnabled(srv, map[string]interface{}{}) {
		t.Errorf("server with empty project settings should default to off")
	}

	// The API the Settings tab reads must agree: project_enabled=false.
	app := newProjectMCPApp(t)
	entries := getProjectMCPServers(t, app, "demo")
	entry, ok := findEntry(entries, "github")
	if !ok {
		t.Fatalf("github server missing from %v", entries)
	}
	if entry.ProjectEnabled {
		t.Errorf("project_enabled should default to false, got true")
	}

	// And the worker-side lookup must not inject tools for it either.
	active, err := getProjectActiveMCPServers("demo")
	if err != nil {
		t.Fatalf("getProjectActiveMCPServers: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected no active MCP servers by default, got %d", len(active))
	}
}

// Explicitly enabling a server for a project turns it on — in both the API
// response and the worker-side active list.
func TestProjectMCPExplicitEnable(t *testing.T) {
	withServerTestDB(t)
	client := db.Client()

	seedProject(t, client, "demo", map[string]interface{}{
		"github": map[string]interface{}{"enabled": true},
	})
	srv := seedMCPServer(t, client, "github", true)
	seedMCPServer(t, client, "puppeteer", true) // left untouched → stays off

	if !projectMCPEnabled(srv, map[string]interface{}{
		"github": map[string]interface{}{"enabled": true},
	}) {
		t.Errorf("explicitly enabled server should be reported active by the helper")
	}

	app := newProjectMCPApp(t)
	entries := getProjectMCPServers(t, app, "demo")
	if e, _ := findEntry(entries, "github"); !e.ProjectEnabled {
		t.Errorf("explicitly enabled server should report project_enabled=true")
	}
	if e, _ := findEntry(entries, "puppeteer"); e.ProjectEnabled {
		t.Errorf("untouched server should stay off")
	}

	active, err := getProjectActiveMCPServers("demo")
	if err != nil {
		t.Fatalf("getProjectActiveMCPServers: %v", err)
	}
	if len(active) != 1 || active[0].Name != "github" {
		t.Errorf("expected only github active, got %v", active)
	}
}

// The PUT handler is what the Settings tab Save button calls: toggling a
// server on must persist and flip the reported state.
func TestProjectMCPPutToggleRoundTrip(t *testing.T) {
	withServerTestDB(t)
	client := db.Client()

	seedProject(t, client, "demo", nil)
	seedMCPServer(t, client, "github", true)

	app := newProjectMCPApp(t)
	if e, _ := findEntry(getProjectMCPServers(t, app, "demo"), "github"); e.ProjectEnabled {
		t.Fatalf("precondition failed: server should start off")
	}

	body := `{"github": {"enabled": true}}`
	req := httptest.NewRequest(http.MethodPut, "/api/projects/demo/mcp-servers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on PUT, got %d", resp.StatusCode)
	}

	if e, _ := findEntry(getProjectMCPServers(t, app, "demo"), "github"); !e.ProjectEnabled {
		t.Errorf("server should be on after PUT toggle")
	}

	active, _ := getProjectActiveMCPServers("demo")
	if len(active) != 1 || active[0].Name != "github" {
		t.Errorf("expected github active after toggle, got %v", active)
	}
}

// The global enabled flag is a master switch: a globally disabled server stays
// off even when a project setting says enabled=true.
func TestProjectMCPGlobalMasterSwitchWins(t *testing.T) {
	withServerTestDB(t)
	client := db.Client()

	seedProject(t, client, "demo", map[string]interface{}{
		"github": map[string]interface{}{"enabled": true},
	})
	srv := seedMCPServer(t, client, "github", false)

	if projectMCPEnabled(srv, map[string]interface{}{
		"github": map[string]interface{}{"enabled": true},
	}) {
		t.Errorf("globally disabled server must stay off regardless of project setting")
	}

	app := newProjectMCPApp(t)
	if e, _ := findEntry(getProjectMCPServers(t, app, "demo"), "github"); e.ProjectEnabled {
		t.Errorf("project_enabled should be false while the global master switch is off")
	}

	active, _ := getProjectActiveMCPServers("demo")
	if len(active) != 0 {
		t.Errorf("expected no active servers, got %v", active)
	}
}