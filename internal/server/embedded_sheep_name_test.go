package server

import (
	"testing"

	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/mcp"
)

// TestEmbeddedSheepNameInjection_RealSchemas pins the schema contract that
// embedded.WantsSheepName relies on, using the defs the daemon really hands to
// the embedded loop. Every browser_* tool that declares sheep_name must require
// it (caller identity, injected). task_start and issue_execute must keep it
// optional (target sheep, never injected) — otherwise a shepherd task's
// enqueue lands on the manager sheep instead of the project's sheep.
func TestEmbeddedSheepNameInjection_RealSchemas(t *testing.T) {
	var defs []embedded.MCPToolDef
	browser := map[string]bool{}
	for _, tool := range mcp.ListCoreToolDefs() {
		defs = append(defs, toEmbeddedMCPDef(tool))
	}
	for _, tool := range mcp.ListBrowserToolDefs() {
		defs = append(defs, toEmbeddedMCPDef(tool))
		if _, ok := tool.InputSchema.Properties["sheep_name"]; ok {
			browser[tool.Name] = true
		}
	}
	tr := embedded.NewToolRegistry(t.TempDir(), "shepherd", defs, nil)

	if len(browser) == 0 {
		t.Fatal("no browser tool declares sheep_name; test setup is wrong")
	}
	for name := range browser {
		if !tr.WantsSheepName(name) {
			t.Errorf("%s: browser tool must get the caller's sheep_name injected", name)
		}
	}
	for _, name := range []string{"task_start", "issue_execute"} {
		if tr.WantsSheepName(name) {
			t.Errorf("%s: sheep_name selects the target sheep and must not be injected", name)
		}
	}
}
