package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
)

// newIssueApp mounts both list endpoints exactly like server.go so the tests
// also pin the route placement (#596: GET /api/issues next to the per-project
// routes, with /api/projects/:name/issues untouched).
func newIssueApp() *fiber.App {
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/issues", s.handleListAllIssues)
	app.Get("/api/projects/:name/issues", s.handleListIssues)
	return app
}

func getJSON(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode %s: %v", url, body)
	}
	return resp.StatusCode, payload
}

// TestHandleListAllIssuesCrossProject covers the #596 API contract: items from
// several projects, project_name / task_count / last-task fields on every row,
// per-status counts that ignore only the status filter, hidden-project default,
// include_hidden, multi-project filter, and sort=updated.
func TestHandleListAllIssuesCrossProject(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()

	alpha := client.Project.Create().SetName("alpha").SetPath("/ws/alpha").SaveX(ctx)
	beta := client.Project.Create().SetName("beta").SetPath("/ws/beta").SaveX(ctx)
	hiddenP := client.Project.Create().SetName("secret").SetPath("/ws/secret").SetHidden(true).SaveX(ctx)

	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	a1 := client.Issue.Create().SetTitle("alpha login bug").SetBody("long body should be omitted").
		SetType(entIssue.TypeBug).SetStatus(entIssue.StatusTodo).
		SetCreatedAt(base).SetUpdatedAt(base).SetProject(alpha).SaveX(ctx)
	a2 := client.Issue.Create().SetTitle("alpha feature").SetStatus(entIssue.StatusDone).
		SetCreatedAt(base.Add(time.Hour)).SetUpdatedAt(base.Add(48 * time.Hour)).SetProject(alpha).SaveX(ctx)
	client.Issue.Create().SetTitle("beta testing item").SetStatus(entIssue.StatusTesting).
		SetCreatedAt(base.Add(2 * time.Hour)).SetUpdatedAt(base.Add(time.Hour)).SetProject(beta).SaveX(ctx)
	client.Issue.Create().SetTitle("hidden secret item").
		SetCreatedAt(base.Add(3 * time.Hour)).SetUpdatedAt(base.Add(3 * time.Hour)).SetProject(hiddenP).SaveX(ctx)

	client.Task.Create().SetPrompt("older").SetStatus(entTask.StatusPending).
		SetCreatedAt(base).SetIssue(a1).SaveX(ctx)
	lastTask := client.Task.Create().SetPrompt("newest").SetStatus(entTask.StatusFailed).
		SetCreatedAt(base.Add(time.Hour)).SetIssue(a1).SaveX(ctx)
	client.Task.Create().SetPrompt("done task").SetStatus(entTask.StatusCompleted).
		SetCreatedAt(base.Add(9 * time.Hour)).SetIssue(a2).SaveX(ctx)

	app := newIssueApp()

	// --- default: visible projects only, counts for the board header ----------
	code, payload := getJSON(t, app, "/api/issues")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, payload)
	}
	if payload["total"].(float64) != 3 {
		t.Fatalf("total = %v, want 3 (hidden project excluded)", payload["total"])
	}
	counts := payload["counts"].(map[string]any)
	for status, want := range map[string]int{"todo": 1, "in_progress": 0, "testing": 1, "failed": 0, "done": 1} {
		if int(counts[status].(float64)) != want {
			t.Errorf("counts[%s] = %v, want %d", status, counts[status], want)
		}
	}

	items := payload["data"].([]any)
	var first map[string]any
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["title"] == "alpha login bug" {
			first = item
		}
	}
	if first == nil {
		t.Fatalf("alpha issue missing from: %v", items)
	}
	if first["project_name"] != "alpha" {
		t.Errorf("project_name = %v, want alpha", first["project_name"])
	}
	if int(first["task_count"].(float64)) != 2 {
		t.Errorf("task_count = %v, want 2", first["task_count"])
	}
	if int(first["last_task_id"].(float64)) != lastTask.ID || first["last_task_status"] != "failed" {
		t.Errorf("last task fields wrong: %v / %v", first["last_task_id"], first["last_task_status"])
	}
	if body, ok := first["body"].(string); !ok || body != "" {
		t.Errorf("body must be omitted by default, got %q", body)
	}

	// include_body brings the full body back.
	code, payload = getJSON(t, app, "/api/issues?q=login&include_body=true")
	item0 := payload["data"].([]any)[0].(map[string]any)
	if item0["body"] != "long body should be omitted" {
		t.Errorf("include_body must return body, got %v", item0["body"])
	}

	// --- status filter keeps full counts ---------------------------------------
	code, payload = getJSON(t, app, "/api/issues?status=todo")
	if payload["total"].(float64) != 1 {
		t.Fatalf("filtered total = %v, want 1", payload["total"])
	}
	counts = payload["counts"].(map[string]any)
	if int(counts["done"].(float64)) != 1 {
		t.Errorf("counts must ignore the status filter: %v", counts)
	}

	// --- multi-project + type + q + include_hidden ------------------------------
	code, payload = getJSON(t, app, "/api/issues?project=alpha,beta&type=bug")
	if payload["total"].(float64) != 1 {
		t.Fatalf("project+type filter total = %v, want 1", payload["total"])
	}
	code, payload = getJSON(t, app, "/api/issues?q=secret")
	if payload["total"].(float64) != 0 {
		t.Fatalf("hidden issue must not appear by default: %v", payload)
	}
	code, payload = getJSON(t, app, "/api/issues?q=secret&include_hidden=true")
	if payload["total"].(float64) != 1 {
		t.Fatalf("include_hidden must find the hidden issue: %v", payload)
	}

	// --- sort=updated puts recently touched issues first ------------------------
	code, payload = getJSON(t, app, "/api/issues?sort=updated")
	ordered := payload["data"].([]any)
	if ordered[0].(map[string]any)["title"] != "alpha feature" {
		t.Fatalf("sort=updated first row = %v", ordered[0])
	}

	// --- bad filter values map to 400 -------------------------------------------
	if code, _ = getJSON(t, app, "/api/issues?status=nope"); code != http.StatusBadRequest {
		t.Errorf("invalid status code = %d, want 400", code)
	}
}

// TestHandleListIssuesPerProjectUnchanged guards the goal "기존 프로젝트별 조회
// 결과는 바뀌지 않는다": the per-project endpoint keeps its shape (no counts, no
// project_name on rows, body present) and its error wording.
func TestHandleListIssuesPerProjectUnchanged(t *testing.T) {
	withServerTestDB(t)
	ctx := context.Background()
	client := db.Client()

	alpha := client.Project.Create().SetName("alpha").SetPath("/ws/alpha").SaveX(ctx)
	hiddenP := client.Project.Create().SetName("secret").SetPath("/ws/secret").SetHidden(true).SaveX(ctx)

	client.Issue.Create().SetTitle("visible one").SetBody("full body").SetProject(alpha).SaveX(ctx)
	client.Issue.Create().SetTitle("hidden one").SetProject(hiddenP).SaveX(ctx)

	app := newIssueApp()

	code, payload := getJSON(t, app, "/api/projects/alpha/issues")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, payload)
	}
	if payload["total"].(float64) != 1 {
		t.Fatalf("total = %v, want 1", payload["total"])
	}
	if _, ok := payload["counts"]; ok {
		t.Error("per-project response must not grow a counts field")
	}
	item := payload["data"].([]any)[0].(map[string]any)
	if item["body"] != "full body" {
		t.Errorf("per-project body must stay, got %v", item["body"])
	}
	if _, ok := item["project_name"]; ok {
		t.Error("per-project rows must not grow a project_name field")
	}

	// Hidden projects stay fully reachable through their own endpoint.
	if code, payload = getJSON(t, app, "/api/projects/secret/issues"); code != http.StatusOK ||
		payload["total"].(float64) != 1 {
		t.Errorf("hidden project endpoint must keep working: %d %v", code, payload)
	}

	// Unknown project keeps the original 404 wording ("project %q not found").
	if code, payload = getJSON(t, app, "/api/projects/nope/issues"); code != http.StatusNotFound {
		t.Errorf("unknown project code = %d, want 404 (%v)", code, payload)
	}
}

// TestSplitCSVQueryAndNormalizeSort covers the query-param helpers directly.
func TestSplitCSVQueryAndNormalizeSort(t *testing.T) {
	if got := splitCSVQuery(""); got != nil {
		t.Errorf("empty = %v", got)
	}
	if got := splitCSVQuery(" a , ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("csv split = %v", got)
	}
	if normalizeIssueSort("updated") != "updated" || normalizeIssueSort("updated_desc") != "updated" {
		t.Error("updated sort must map to SortBy=updated")
	}
	if normalizeIssueSort("asc") != "" || normalizeIssueSort("") != "" {
		t.Error("asc/empty must keep the default column")
	}
}
