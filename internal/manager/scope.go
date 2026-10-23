package manager

import (
	"path/filepath"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/project"
)

// ScopeProjects returns the projects the shepherd manager manages (#593).
//
// The scope is "every project that is not hidden, minus the exclusion list" —
// deliberately NOT tied to workspace_path. A new project enters the scope the
// moment it is registered, and projects that live outside the workspace (e.g.
// the `home` project pointing at ~) stay in scope too; the UI only marks them
// "outside workspace". Later stages (prompts, issue board, needs-attention,
// patrol) must all read scope through this function.
//
//   - shepherd_scope_exclude: project names to leave out even when visible.
//   - shepherd_scope_include_hidden: when true, hidden projects join as well.
func ScopeProjects() ([]*ent.Project, error) {
	all, err := project.List()
	if err != nil {
		return nil, err
	}

	excluded := make(map[string]bool)
	for _, name := range config.GetSheepScopeExclude() {
		excluded[name] = true
	}
	includeHidden := config.GetBool("shepherd_scope_include_hidden")

	inScope := make([]*ent.Project, 0, len(all))
	for _, p := range all {
		if excluded[p.Name] {
			continue
		}
		if p.Hidden && !includeHidden {
			continue
		}
		inScope = append(inScope, p)
	}
	return inScope, nil
}

// InWorkspace reports whether projectPath is the workspace itself or lives
// inside it. Both sides are normalized with filepath.Clean so trailing slashes
// and "." segments don't matter. An unset workspace contains nothing: callers
// treat that as "we can't tell", and the UI only shows the marker when a
// workspace is set. This is display metadata only — it never changes scope.
func InWorkspace(workspace, projectPath string) bool {
	ws := strings.TrimSpace(workspace)
	if ws == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(ws), filepath.Clean(projectPath))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
