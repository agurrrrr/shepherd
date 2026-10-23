package issue

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

// newTestDB swaps in an in-memory database for one test, mirroring the
// server/manager helpers: modernc's driver registers as "sqlite" while ent
// needs the "sqlite3" dialect name.
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

func mustProject(t *testing.T, client *ent.Client, name string, hidden bool) *ent.Project {
	t.Helper()
	return client.Project.Create().
		SetName(name).
		SetPath("/ws/" + name).
		SetHidden(hidden).
		SaveX(context.Background())
}

func mustIssue(t *testing.T, client *ent.Client, p *ent.Project, title string, mods ...func(*ent.IssueCreate)) *ent.Issue {
	t.Helper()
	c := client.Issue.Create().SetTitle(title).SetBody("body of " + title).SetProject(p)
	for _, m := range mods {
		m(c)
	}
	return c.SaveX(context.Background())
}

func mustTask(t *testing.T, client *ent.Client, iss *ent.Issue, status entTask.Status, createdAt time.Time) *ent.Task {
	t.Helper()
	return client.Task.Create().
		SetPrompt("task for " + iss.Title).
		SetStatus(status).
		SetCreatedAt(createdAt).
		SetIssue(iss).
		SaveX(context.Background())
}

// TestListCrossProjectScope covers the #596 goal set for issue.List: all-projects
// listing, multi-project filter, hidden exclusion + opt-in, OmitBody, the
// `updated` sort, per-status counts, and the batched last-task lookup.
func TestListCrossProjectScope(t *testing.T) {
	client := newTestDB(t)

	alpha := mustProject(t, client, "alpha", false)
	beta := mustProject(t, client, "beta", false)
	gamma := mustProject(t, client, "gamma", true) // hidden

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	a1 := mustIssue(t, client, alpha, "alpha todo", func(c *ent.IssueCreate) {
		c.SetCreatedAt(base).SetUpdatedAt(base)
	})
	a2 := mustIssue(t, client, alpha, "alpha done", func(c *ent.IssueCreate) {
		c.SetStatus(entIssue.StatusDone).SetCreatedAt(base.Add(time.Hour)).SetUpdatedAt(base.Add(48 * time.Hour))
	})
	b1 := mustIssue(t, client, beta, "beta testing", func(c *ent.IssueCreate) {
		c.SetStatus(entIssue.StatusTesting).SetCreatedAt(base.Add(2 * time.Hour)).SetUpdatedAt(base.Add(time.Hour))
	})
	mustIssue(t, client, gamma, "gamma hidden", func(c *ent.IssueCreate) {
		c.SetCreatedAt(base.Add(3 * time.Hour)).SetUpdatedAt(base.Add(3 * time.Hour))
	})

	mustTask(t, client, a1, entTask.StatusPending, base)
	lastA1 := mustTask(t, client, a1, entTask.StatusFailed, base.Add(time.Hour))
	mustTask(t, client, a2, entTask.StatusCompleted, base.Add(10*time.Hour))

	// --- all projects: hidden excluded by default --------------------------------
	res, err := List(ListFilter{AllProjects: true})
	if err != nil {
		t.Fatalf("all projects: %v", err)
	}
	if res.Total != 3 {
		t.Fatalf("visible total = %d, want 3", res.Total)
	}
	for _, iss := range res.Items {
		if iss.Edges.Project == nil {
			t.Fatal("cross-project items must carry Edges.Project")
		}
		if iss.Edges.Project.Name == "gamma" {
			t.Fatal("hidden project must be excluded by default")
		}
	}

	// counts ignore the status filter but keep the rest (here: none).
	wantCounts := StatusCounts{"todo": 1, "in_progress": 0, "testing": 1, "failed": 0, "done": 1}
	for k, v := range wantCounts {
		if res.StatusCount[k] != v {
			t.Errorf("counts[%s] = %d, want %d (all: %v)", k, res.StatusCount[k], v, res.StatusCount)
		}
	}

	// last task per issue: newest by created_at, resolved in one batch query.
	lt := res.LastTasks[a1.ID]
	if lt == nil || lt.ID != lastA1.ID || lt.Status != "failed" {
		t.Errorf("last task for a1 = %+v, want #%d/failed", lt, lastA1.ID)
	}
	if res.TaskCounts[a1.ID] != 2 {
		t.Errorf("task count a1 = %d, want 2", res.TaskCounts[a1.ID])
	}
	if res.TaskCounts[b1.ID] != 0 {
		t.Errorf("task count b1 = %d, want 0", res.TaskCounts[b1.ID])
	}

	// --- status filter narrows items but NOT counts ------------------------------
	res, err = List(ListFilter{AllProjects: true, Status: "todo"})
	if err != nil {
		t.Fatalf("status filter: %v", err)
	}
	if res.Total != 1 || len(res.Items) != 1 || res.Items[0].Title != "alpha todo" {
		t.Fatalf("status filter items wrong: %+v", res.Items)
	}
	if res.StatusCount["done"] != 1 {
		t.Errorf("counts must ignore the status filter: %v", res.StatusCount)
	}

	// --- include_hidden brings hidden projects in --------------------------------
	res, err = List(ListFilter{AllProjects: true, IncludeHidden: true})
	if err != nil {
		t.Fatalf("include_hidden: %v", err)
	}
	if res.Total != 4 {
		t.Fatalf("include_hidden total = %d, want 4", res.Total)
	}

	// --- Projects filter ----------------------------------------------------------
	res, err = List(ListFilter{Projects: []string{"beta", "missing"}})
	if err != nil {
		t.Fatalf("projects filter: %v", err)
	}
	if res.Total != 1 || res.Items[0].Title != "beta testing" {
		t.Fatalf("projects filter wrong: %+v", res.Items)
	}
	// Hidden projects stay out of an explicit name list unless asked for, and
	// IncludeHidden only lifts that guard — it must not narrow the list to the
	// hidden names.
	if got := ListMust(t, ListFilter{Projects: []string{"alpha", "gamma"}}); titles(got)[0] != "alpha done" || len(got) != 2 {
		t.Fatalf("Projects without IncludeHidden must skip hidden rows: %v", titles(got))
	}
	gotBoth := ListMust(t, ListFilter{Projects: []string{"alpha", "gamma"}, IncludeHidden: true})
	if len(gotBoth) != 3 {
		t.Fatalf("Projects + IncludeHidden = %v, want alpha(2)+gamma(1)", titles(gotBoth))
	}
	if len(ListMust(t, ListFilter{Projects: []string{"gamma"}, IncludeHidden: true})) != 1 {
		t.Error("Projects + IncludeHidden must return the hidden project's issue")
	}

	// --- OmitBody -----------------------------------------------------------------
	res, err = List(ListFilter{AllProjects: true, OmitBody: true})
	if err != nil {
		t.Fatalf("omit body: %v", err)
	}
	for _, iss := range res.Items {
		if iss.Body != "" {
			t.Errorf("OmitBody must blank body, got %q", iss.Body)
		}
	}
	res, err = List(ListFilter{AllProjects: true})
	if err != nil {
		t.Fatalf("default body: %v", err)
	}
	if res.Items[0].Body == "" {
		t.Error("body must be present without OmitBody")
	}

	// --- sort=updated --------------------------------------------------------------
	// updated_at: a2=48h, b1=1h, a1=0 → desc = alpha done, beta testing, alpha todo.
	res, err = List(ListFilter{AllProjects: true, SortBy: "updated"})
	if err != nil {
		t.Fatalf("sort updated: %v", err)
	}
	if got := titles(res.Items); strings.Join(got, ",") != "alpha done,beta testing,alpha todo" {
		t.Fatalf("updated desc = %v, want [alpha done, beta testing, alpha todo]", got)
	}
	res, err = List(ListFilter{AllProjects: true, SortBy: "updated", SortAsc: true})
	if err != nil {
		t.Fatalf("sort updated asc: %v", err)
	}
	if got := titles(res.Items); strings.Join(got, ",") != "alpha todo,beta testing,alpha done" {
		t.Fatalf("updated asc = %v", got)
	}
	res, err = List(ListFilter{AllProjects: true}) // default created desc
	if err != nil {
		t.Fatalf("default sort: %v", err)
	}
	if got := titles(res.Items); strings.Join(got, ",") != "beta testing,alpha done,alpha todo" {
		t.Fatalf("created desc = %v", got)
	}

	// single-project mode keeps its shape: no StatusCount/LastTasks filled.
	res, err = List(ListFilter{Project: "alpha"})
	if err != nil {
		t.Fatalf("single project: %v", err)
	}
	if res.StatusCount != nil || res.LastTasks != nil {
		t.Error("single-project results must keep the original shape")
	}
	if res.TaskCounts[a1.ID] != 2 {
		t.Errorf("single-project task counts broken: %v", res.TaskCounts)
	}
}

func ListMust(t *testing.T, f ListFilter) []*ent.Issue {
	t.Helper()
	res, err := List(f)
	if err != nil {
		t.Fatalf("list %v: %v", f, err)
	}
	return res.Items
}

func titles(items []*ent.Issue) []string {
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.Title)
	}
	return out
}

// TestListFilterValidation pins the filter-mode rules: project stays required
// outside cross-project mode (original wording), and mixing modes is refused.
func TestListFilterValidation(t *testing.T) {
	newTestDB(t)

	if _, err := List(ListFilter{}); err == nil || !strings.Contains(err.Error(), "project is required") {
		t.Fatalf("expected project required, got %v", err)
	}
	if _, err := List(ListFilter{Project: "p", AllProjects: true}); err == nil ||
		!strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("expected mixed-mode error, got %v", err)
	}
	if _, err := List(ListFilter{AllProjects: true, Status: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("expected invalid status error, got %v", err)
	}
	if _, err := List(ListFilter{AllProjects: true, Type: "nope"}); err == nil ||
		!strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("expected invalid type error, got %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	if _, err := Create(CreateInput{Title: "x"}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	if _, err := Create(CreateInput{Project: "p"}); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("expected title required, got %v", err)
	}
	if _, err := Create(CreateInput{Project: "p", Title: "t", Type: "nope"}); err == nil || !strings.Contains(err.Error(), "invalid type") {
		t.Fatalf("expected invalid type, got %v", err)
	}
}

func TestUpdateValidation(t *testing.T) {
	// Empty project fails before DB lookup.
	if _, err := Update("", 1, UpdateInput{}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	if _, err := Update("p", 0, UpdateInput{}); err == nil || !strings.Contains(err.Error(), "invalid issue id") {
		t.Fatalf("expected invalid id, got %v", err)
	}
}

func TestListValidation(t *testing.T) {
	if _, err := List(ListFilter{}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("expected project required, got %v", err)
	}
	// Invalid status/type fail before DB when project is set — but project existence
	// is checked first via DB. Invalid enums are checked after project exists, so
	// validate helpers directly.
	if err := validateType("nope"); err == nil {
		t.Fatal("expected invalid type")
	}
	if err := validateStatus("nope"); err == nil {
		t.Fatal("expected invalid status")
	}
	if err := validateType("feature"); err != nil {
		t.Fatal(err)
	}
	if err := validateStatus("in_progress"); err != nil {
		t.Fatal(err)
	}
}

func TestFormatTime(t *testing.T) {
	if FormatTime(time.Time{}) != "" {
		t.Fatal("zero time should format empty")
	}
	ts := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	if got := FormatTime(ts); got != "2026-07-22 13:00:00" {
		t.Fatalf("got %q", got)
	}
	if FormatTimePtr(nil) != "" {
		t.Fatal("nil ptr should be empty")
	}
	if got := FormatTimePtr(&ts); got != "2026-07-22 13:00:00" {
		t.Fatalf("got %q", got)
	}
}

func TestValidEnumsDocumented(t *testing.T) {
	// Keep CLI help and package constants in sync.
	if len(ValidTypes) != 3 {
		t.Fatalf("types: %v", ValidTypes)
	}
	if len(ValidStatuses) != 5 {
		t.Fatalf("statuses: %v", ValidStatuses)
	}
}

func TestChildrenChecklist(t *testing.T) {
	if ChildrenChecklist(nil) != "" {
		t.Fatal("nil issue should yield empty checklist")
	}
	root := &ent.Issue{ID: 3, Title: "parent"}
	if ChildrenChecklist(root) != "" {
		t.Fatal("no children should yield empty")
	}

	root.Edges.Children = []*ent.Issue{
		{ID: 5, Title: "[API-1] setup", Status: entIssue.StatusDone},
		{ID: 6, Title: "[API-2] schema", Status: entIssue.StatusTodo},
		{ID: 7, Title: "[API-3] auth", Status: entIssue.StatusInProgress},
	}
	got := ChildrenChecklist(root)
	if !strings.Contains(got, "## 하위 이슈") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "- [x] #5 [API-1] setup (성공)") {
		t.Fatalf("done child missing: %q", got)
	}
	if !strings.Contains(got, "- [ ] #6 [API-2] schema (작업전)") {
		t.Fatalf("todo child missing: %q", got)
	}
	if !strings.Contains(got, "- [ ] #7 [API-3] auth (작업중)") {
		t.Fatalf("in_progress child missing: %q", got)
	}
}

func TestStatusLabel(t *testing.T) {
	cases := map[string]string{
		"todo":        "작업전",
		"in_progress": "작업중",
		"testing":     "테스트",
		"failed":      "실패",
		"done":        "성공",
		"other":       "other",
	}
	for in, want := range cases {
		if got := statusLabel(in); got != want {
			t.Fatalf("statusLabel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCascadeEnqueueDelayPositive(t *testing.T) {
	// Guard against accidental zero/negative that reintroduces cascade races.
	if cascadeEnqueueDelay < time.Second {
		t.Fatalf("cascadeEnqueueDelay=%v; want at least 1s to space bulk enqueue", cascadeEnqueueDelay)
	}
}
