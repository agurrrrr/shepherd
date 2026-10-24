package manager

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entissue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/spf13/viper"
)

// countingClient builds an in-memory ent client whose driver counts every SQL
// query, so tests can pin that BuildScopeTable's query count does not grow with
// the project count (issue #595 goal, wiki: project-list-latency-analysis).
func countingClient(t *testing.T) (*ent.Client, *int32) {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	var queries int32
	drv := entsql.OpenDB("sqlite3", sqlDB)
	counted := &countingDriver{Driver: drv, queries: &queries}

	client := ent.NewClient(ent.Driver(counted))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	atomic.StoreInt32(&queries, 0)

	prev := db.ReplaceClient(client)
	t.Cleanup(func() {
		db.ReplaceClient(prev)
		client.Close()
	})
	return client, &queries
}

// countingDriver wraps an ent driver and increments a counter on each Query.
type countingDriver struct {
	dialect.Driver
	queries *int32
}

func (d *countingDriver) Query(ctx context.Context, query string, args, v interface{}) error {
	atomic.AddInt32(d.queries, 1)
	return d.Driver.Query(ctx, query, args, v)
}

// scopeDefaults pins the #593 scope config keys to their defaults for one test.
func scopeDefaults(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetDefault("shepherd_scope_include_hidden", false)
	viper.SetDefault("shepherd_scope_exclude", []string{})
}

func makeProject(t *testing.T, client *ent.Client, name string) *ent.Project {
	t.Helper()
	return client.Project.Create().SetName(name).SetPath("/ws/" + name).SaveX(context.Background())
}

// TestBuildShepherdContextSections checks every section the issue asks for:
// base instructions, workspace, scope table, autonomy notice, and the user's
// extra instructions at the end.
func TestBuildShepherdContextSections(t *testing.T) {
	client := newTestDB(t)
	scopeDefaults(t)

	p := makeProject(t, client, "bori")
	client.Sheep.Create().
		SetName("양순이").
		SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusWorking).
		SetProject(p).
		SaveX(context.Background())
	client.Task.Create().SetPrompt("do it").SetStatus(task.StatusPending).SetProject(p).SaveX(context.Background())
	client.Task.Create().SetPrompt("running now").SetStatus(task.StatusRunning).SetProject(p).SaveX(context.Background())
	client.Issue.Create().SetTitle("open one").SetStatus(entissue.StatusTodo).SetProject(p).SaveX(context.Background())
	client.Issue.Create().SetTitle("closed one").SetStatus(entissue.StatusDone).SetProject(p).SaveX(context.Background())

	viper.Set("workspace_path", "/home/user/code")
	viper.Set("shepherd_instructions", "이슈를 닫기 전에 테스트 통과를 확인하라.")

	out := BuildShepherdContext()

	for _, want := range []string{
		"[Shepherd Orchestrator — operating instructions]",
		"[Shepherd Workspace]\n/home/user/code",
		"[Managed Projects]",
		"bori | /ws/bori | 양순이(claude) working | pending=1 | running=#",
		"todo=1/in_progress=0/testing=0/failed=0",
		// #601: the [Autonomy] section is live — tier line, both caps, and the
		// request_user_action pointer replace the old flat AutonomyNotice.
		"[Autonomy]\ntier: execute — issue create/edit + enqueue allowed (caps apply); stopping tasks is refused",
		"delegation cap: at most 3 watched-issue tasks pending/running at once (currently 0)",
		"daily cost cap: none",
		"request_user_action(kind, project_name, target_id, reason)",
		"[User Shepherd Instructions]\n이슈를 닫기 전에 테스트 통과를 확인하라.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("context missing %q\n---\n%s", want, out)
		}
	}

	// done issues are not counted as open.
	if strings.Contains(out, "done=1") {
		t.Errorf("closed issue must not appear in the open-issue counts:\n%s", out)
	}

	// The user instructions must come after the autonomy notice.
	if strings.Index(out, "[Autonomy]") > strings.Index(out, "[User Shepherd Instructions]") {
		t.Errorf("[User Shepherd Instructions] must be the last section:\n%s", out)
	}
}

// TestBuildShepherdContextUnsetWorkspace covers a fresh daemon: no workspace,
// no projects. The prompt must still render every header instead of failing.
func TestBuildShepherdContextUnsetWorkspace(t *testing.T) {
	newTestDB(t)
	scopeDefaults(t)

	out := BuildShepherdContext()
	for _, want := range []string{
		"[Shepherd Orchestrator — operating instructions]",
		"[Shepherd Workspace]\n(unset",
		"[Managed Projects]\n(no managed projects)",
		"[Autonomy]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("context missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "[User Shepherd Instructions]") {
		t.Errorf("empty shepherd_instructions must not emit a section:\n%s", out)
	}
}

// TestBuildScopeTableSkipsHiddenAndExcluded re-checks the #593 scope rules at
// the table level: hidden projects and excluded names must not be rendered.
func TestBuildScopeTableSkipsHiddenAndExcluded(t *testing.T) {
	client := newTestDB(t)
	scopeDefaults(t)

	client.Project.Create().SetName("visible").SetPath("/ws/visible").SaveX(context.Background())
	client.Project.Create().SetName("secret").SetPath("/ws/secret").SetHidden(true).SaveX(context.Background())
	makeProject(t, client, "unmanaged")

	viper.Set("shepherd_scope_exclude", []string{"unmanaged"})

	table, err := BuildScopeTable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "visible | /ws/visible") {
		t.Errorf("visible project missing from table:\n%s", table)
	}
	for _, banned := range []string{"secret", "unmanaged"} {
		if strings.Contains(table, banned) {
			t.Errorf("%s must not appear in the scope table:\n%s", banned, table)
		}
	}

	viper.Set("shepherd_scope_include_hidden", true)
	table, err = BuildScopeTable()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table, "secret") {
		t.Errorf("hidden project must join when include_hidden is on:\n%s", table)
	}
}

// TestBuildScopeTableQueryCountIsFixed is the regression guard for the 50+
// project scale: three aggregate queries plus the project list, no matter how
// many projects are in scope.
func TestBuildScopeTableQueryCountIsFixed(t *testing.T) {
	client, queries := countingClient(t)
	scopeDefaults(t)

	for i := 0; i < 40; i++ {
		p := makeProject(t, client, "proj"+strings.Repeat("x", 1)+itoa(i))
		client.Task.Create().SetPrompt("t").SetStatus(task.StatusPending).SetProject(p).SaveX(context.Background())
		client.Issue.Create().SetTitle("i").SetStatus(entissue.StatusTodo).SetProject(p).SaveX(context.Background())
	}

	atomic.StoreInt32(queries, 0)
	if _, err := BuildScopeTable(); err != nil {
		t.Fatal(err)
	}
	few := atomic.LoadInt32(queries)

	// Grow the scope and re-measure: the count must not move.
	for i := 40; i < 80; i++ {
		makeProject(t, client, "proj"+itoa(i))
	}
	atomic.StoreInt32(queries, 0)
	if _, err := BuildScopeTable(); err != nil {
		t.Fatal(err)
	}
	many := atomic.LoadInt32(queries)

	if few != many {
		t.Fatalf("query count grew with project count: %d (40 projects) vs %d (80 projects)", few, many)
	}
	// Fixed shape regardless of project count: project list (1) + eager-loaded
	// sheep edge (1) + pending group-by (1) + running select (1) + issue
	// group-by (1).
	if many != 5 {
		t.Fatalf("expected a fixed 5 queries, got %d", many)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestReduceScopeTableCapsIdleProjectsFirst pins the length rule: when the
// table exceeds scopeTableMaxChars, projects with nothing in flight collapse to
// a bare name while active ones keep their detail.
func TestReduceScopeTableCapsIdleProjectsFirst(t *testing.T) {
	stats := make([]*scopeStat, 0, 60)
	for i := 0; i < 55; i++ {
		stats = append(stats, &scopeStat{
			name:       "idle-project-" + itoa(i),
			path:       "/workspace/with/a/fairly/long/path/to/every/project/deep/nested/dir/" + itoa(i),
			sheepName:  "양순이",
			openIssues: map[entissue.Status]int{},
		})
	}
	active := &scopeStat{
		name:       "active-project",
		path:       "/workspace/with/a/fairly/long/path/to/every/project/deep/nested/dir/active",
		sheepName:  "양동이",
		pending:    2,
		runningIDs: []int{101},
		openIssues: map[entissue.Status]int{entissue.StatusTodo: 3},
	}
	stats = append(stats, active)

	got := reduceScopeTable(stats)
	if utf8Len(got) > scopeTableMaxChars {
		t.Fatalf("reduced table still exceeds the cap: %d chars", utf8Len(got))
	}
	if !strings.Contains(got, "active-project | /workspace") {
		t.Errorf("active project must keep its full line:\n%s", got)
	}
	// Reduction starts with the longest idle line and works down until the
	// total fits, so the two-digit names (longest) collapse first.
	if !strings.Contains(got, "idle-project-10 | (idle)") {
		t.Errorf("idle projects must collapse to name + (idle):\n%s", got)
	}
	if strings.Contains(got, "idle-project-10 | /workspace") {
		t.Errorf("reduced idle project still carries a path:\n%s", got)
	}
}

// TestBuildScopeTableAppliesCap drives the same rule end-to-end: 60 wide
// projects in the DB, one of them busy. The rendered table stays under the cap
// and keeps the busy project's details.
func TestBuildScopeTableAppliesCap(t *testing.T) {
	client := newTestDB(t)
	scopeDefaults(t)

	// Paths are long on purpose: 60 short lines would fit under the 8000-char
	// cap and prove nothing. Real deployments register projects under deep
	// workspaces, which is what pushes the table past the limit.
	for i := 0; i < 59; i++ {
		client.Project.Create().
			SetName("wide-project-" + itoa(i)).
			SetPath("/home/agurrrrr/code/workspace/with/a/fairly/deep/nested/path/" + itoa(i)).
			SaveX(context.Background())
	}
	busy := client.Project.Create().
		SetName("busy-project").
		SetPath("/home/agurrrrr/code/workspace/with/a/fairly/deep/nested/path/busy").
		SaveX(context.Background())
	client.Task.Create().SetPrompt("t").SetStatus(task.StatusRunning).SetProject(busy).SaveX(context.Background())

	table, err := BuildScopeTable()
	if err != nil {
		t.Fatal(err)
	}
	if utf8Len(table) > scopeTableMaxChars {
		t.Fatalf("table exceeds the cap: %d chars", utf8Len(table))
	}
	if !strings.Contains(table, "busy-project | /home/agurrrrr/code/workspace") {
		t.Errorf("busy project must keep its full line:\n%s", table)
	}
	// Some idle projects must have been collapsed (largest line first), while
	// the cap holds. Not all of them need to go — only enough to fit.
	if !strings.Contains(table, " | (idle)") {
		t.Errorf("idle projects must be reduced to fit the cap:\n%s", table)
	}
}
