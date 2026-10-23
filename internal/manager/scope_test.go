package manager

import (
	"context"
	"database/sql"
	"testing"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/spf13/viper"
)

// newTestDB swaps in an in-memory database for one test, mirroring the server
// package's helper: modernc's driver registers as "sqlite" while ent needs the
// "sqlite3" dialect name.
func newTestDB(t *testing.T) *ent.Client {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := db.ReplaceClient(client)
	t.Cleanup(func() {
		db.ReplaceClient(prev)
		client.Close()
	})

	return client
}

func createProject(t *testing.T, client *ent.Client, name, path string, hidden bool) {
	t.Helper()
	ctx := context.Background()
	client.Project.Create().
		SetName(name).
		SetPath(path).
		SetHidden(hidden).
		SaveX(ctx)
}

func scopeNames(projects []*ent.Project) []string {
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		out = append(out, p.Name)
	}
	return out
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// TestScopeProjectsCoversHiddenAndExclusions pins the three scope rules from
// #593: visible projects are in, hidden projects are out unless the include
// toggle is on, and the exclusion list removes named projects even when they
// are visible.
func TestScopeProjectsCoversHiddenAndExclusions(t *testing.T) {
	client := newTestDB(t)
	createProject(t, client, "alpha", "/ws/alpha", false)
	createProject(t, client, "beta", "/ws/beta", false)
	createProject(t, client, "secret", "/ws/secret", true)

	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("shepherd_scope_include_hidden", false)
	viper.SetDefault("shepherd_scope_exclude", []string{})

	got, err := ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	names := scopeNames(got)
	if !contains(names, "alpha") || !contains(names, "beta") {
		t.Fatalf("visible projects must be in scope: %v", names)
	}
	if contains(names, "secret") {
		t.Fatalf("hidden project must be out of scope by default: %v", names)
	}

	viper.Set("shepherd_scope_exclude", []string{"beta"})
	got, err = ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	names = scopeNames(got)
	if contains(names, "beta") {
		t.Fatalf("excluded project must be out of scope: %v", names)
	}
	if !contains(names, "alpha") {
		t.Fatalf("non-excluded project must stay in scope: %v", names)
	}

	viper.Set("shepherd_scope_include_hidden", true)
	got, err = ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	names = scopeNames(got)
	if !contains(names, "secret") {
		t.Fatalf("hidden project must join when include_hidden is on: %v", names)
	}
	if contains(names, "beta") {
		t.Fatalf("exclusion must still apply with include_hidden: %v", names)
	}
}

// TestScopeProjectsIncludesNewlyRegisteredProject is the reason scope is
// computed from the project table instead of a stored allow-list: a project
// registered after the settings were saved enters the scope automatically.
func TestScopeProjectsIncludesNewlyRegisteredProject(t *testing.T) {
	client := newTestDB(t)
	createProject(t, client, "alpha", "/ws/alpha", false)

	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("shepherd_scope_include_hidden", false)
	viper.SetDefault("shepherd_scope_exclude", []string{})

	before, err := ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 {
		t.Fatalf("want 1 project, got %v", scopeNames(before))
	}

	createProject(t, client, "fresh", "/ws/fresh", false)

	after, err := ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	names := scopeNames(after)
	if !contains(names, "fresh") {
		t.Fatalf("newly registered project must enter scope automatically: %v", names)
	}
}

// TestScopeProjectsIgnoresWorkspacePath guards the design decision that the
// workspace is only the manager's working directory. The `home` project lives
// outside /home/x/code but must stay in scope; the UI marks it instead.
func TestScopeProjectsIgnoresWorkspacePath(t *testing.T) {
	client := newTestDB(t)
	createProject(t, client, "alpha", "/ws/alpha", false)
	createProject(t, client, "home", "/home/agurrrrr", false)

	viper.Reset()
	defer viper.Reset()
	viper.SetDefault("shepherd_scope_include_hidden", false)
	viper.SetDefault("shepherd_scope_exclude", []string{})
	viper.Set("workspace_path", "/ws")

	got, err := ScopeProjects()
	if err != nil {
		t.Fatal(err)
	}
	names := scopeNames(got)
	if !contains(names, "home") {
		t.Fatalf("project outside the workspace must stay in scope: %v", names)
	}
}

func TestInWorkspace(t *testing.T) {
	cases := []struct {
		workspace, project string
		want               bool
	}{
		{"/home/u/code", "/home/u/code/shepherd", true},
		{"/home/u/code", "/home/u/code", true},
		{"/home/u/code/", "/home/u/code/shepherd/", true},
		{"/home/u/code", "/home/u", false},
		{"/home/u/code", "/home/other/code", false},
		{"/home/u/code", "/home/u/codeback", false},
		{"", "/anything", false},
	}
	for _, tc := range cases {
		if got := InWorkspace(tc.workspace, tc.project); got != tc.want {
			t.Errorf("InWorkspace(%q, %q) = %v, want %v", tc.workspace, tc.project, got, tc.want)
		}
	}
}
