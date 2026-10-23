package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/spf13/viper"
)

// Patrol API tests (#600). The wake-rule details live in
// internal/manager/patrol_test.go; here we pin the HTTP surface: config keys
// round-trip through GET/PATCH, "run now" stores a record even with nothing
// found, the list endpoint paginates newest-first with the woken task's first
// summary line, and the status card reports the next patrol time.

func newPatrolApp(t *testing.T) (*fiber.App, *Server) {
	t.Helper()
	app, s := newShepherdApp(t)
	app.Get("/api/shepherd/patrols", s.handleListPatrols)
	app.Post("/api/shepherd/patrols/run", s.handleRunPatrol)
	return app, s
}

// withPatrolRawDB points the raw *sql.DB handle at the same in-memory DB as
// the ent client. CollectAttention's pending_backlog kind runs raw SQL, and
// withServerTestDB only swaps the client — without this, a full pass through
// the HTTP handler records 점검 실패 instead of real findings.
func withPatrolRawDB(t *testing.T) {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"-raw?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	client := ent.NewClient(ent.Driver(entsql.OpenDB("sqlite3", sqlDB)))
	if err := client.Schema.Create(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prevClient := db.ReplaceClient(client)
	prevRaw := db.ReplaceRawDB(sqlDB)
	t.Cleanup(func() {
		db.ReplaceClient(prevClient)
		db.ReplaceRawDB(prevRaw)
		client.Close()
	})
}

func getPatrols(t *testing.T, app *fiber.App, query string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/shepherd/patrols"+query, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	body := mustRead(resp)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp.StatusCode, out
}

func postPatrolRun(t *testing.T, app *fiber.App) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/shepherd/patrols/run", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	body := mustRead(resp)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return resp.StatusCode, out
}

func mustRead(resp *http.Response) []byte {
	buf := make([]byte, 0, 4096)
	b := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(b)
		buf = append(buf, b[:n]...)
		if err != nil {
			return buf
		}
	}
}

// TestPatrolConfigRoundTrip pins the two new shepherd config keys.
func TestPatrolConfigRoundTrip(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)

	// Defaults: interval "30m", wake on (unset-means-true).
	code, cfg := getShepherdConfig(t, app)
	if code != http.StatusOK {
		t.Fatalf("GET config: %d %v", code, cfg)
	}
	if cfg["data"].(map[string]any)["patrol_interval"] != "30m" {
		t.Fatalf("default patrol_interval: %v", cfg["data"].(map[string]any)["patrol_interval"])
	}
	if cfg["data"].(map[string]any)["patrol_wake"] != true {
		t.Fatalf("default patrol_wake must be true: %v", cfg["data"].(map[string]any)["patrol_wake"])
	}

	code, cfg = patchShepherdConfig(t, app, map[string]any{
		"patrol_interval": "0",
		"patrol_wake":     false,
	})
	if code != http.StatusOK {
		t.Fatalf("PATCH: %d %v", code, cfg)
	}
	data := cfg["data"].(map[string]any)
	if data["patrol_interval"] != "0" || data["patrol_wake"] != false {
		t.Fatalf("saved values must echo back: %v / %v", data["patrol_interval"], data["patrol_wake"])
	}

	// Garbage intervals are rejected so the loop can't silently fall back.
	code, _ = patchShepherdConfig(t, app, map[string]any{"patrol_interval": "every-30"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad interval must 400, got %d", code)
	}
	code, _ = patchShepherdConfig(t, app, map[string]any{"patrol_interval": "-10m"})
	if code != http.StatusBadRequest {
		t.Fatalf("negative interval must 400, got %d", code)
	}
}

// TestPatrolRunAndListAPI pins the run/list shape: a run with nothing found
// stores one record (new_count 0, no task), and GET lists it newest-first.
func TestPatrolRunAndListAPI(t *testing.T) {
	withShepherdTestEnv(t)
	app, _ := newPatrolApp(t)

	code, out := getPatrols(t, app, "")
	if code != http.StatusOK {
		t.Fatalf("GET patrols: %d %v", code, out)
	}
	if out["data"].(map[string]any)["count"] != float64(0) {
		t.Fatalf("empty list expected: %v", out)
	}

	code, out = postPatrolRun(t, app)
	if code != http.StatusOK {
		t.Fatalf("run: %d %v", code, out)
	}
	rec := out["data"].(map[string]any)["patrol"].(map[string]any)
	if rec["new_count"] != float64(0) || rec["task_id"] != nil {
		t.Fatalf("no findings → record only: %v", rec)
	}

	code, out = getPatrols(t, app, "?limit=5")
	if code != http.StatusOK {
		t.Fatalf("GET patrols after run: %d %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["count"] != float64(1) {
		t.Fatalf("one stored record expected: %v", data)
	}

	// Bad limits are rejected (same convention as /api/shepherd/tasks).
	if code, _ = getPatrols(t, app, "?limit=0"); code != http.StatusBadRequest {
		t.Fatalf("limit=0 must 400, got %d", code)
	}
	if code, _ = getPatrols(t, app, "?limit=abc"); code != http.StatusBadRequest {
		t.Fatalf("limit=abc must 400, got %d", code)
	}
}

// TestPatrolListAttachesWakeSummary pins the woken-task link in the list:
// seeded by running one pass with a failing task present, so the whole chain
// (CollectAttention → wake → record → list) runs through real queries.
func TestPatrolListAttachesWakeSummary(t *testing.T) {
	withShepherdTestEnv(t)
	withPatrolRawDB(t) // CollectAttention needs the raw handle on the same DB
	app, _ := newPatrolApp(t)
	ctx := context.Background()

	// A failed task inside the scope is an attention finding (failed_task).
	proj := db.Client().Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	failed := db.Client().Task.Create().SetPrompt("a").
		SetStatus(entTask.StatusFailed).SetError("boom").SetProject(proj).
		SetCompletedAt(time.Now().Add(-time.Hour)).SaveX(ctx)

	code, out := postPatrolRun(t, app)
	if code != http.StatusOK {
		t.Fatalf("run: %d %v", code, out)
	}
	rec := out["data"].(map[string]any)["patrol"].(map[string]any)
	if rec["new_count"] != float64(1) {
		t.Fatalf("the failure must count as new: %v", rec)
	}
	if rec["skipped_reason"] != nil {
		t.Fatalf("wake should succeed (workspace unset only matters for task RUN, not enqueue): %v", rec)
	}
	taskID := int(rec["task_id"].(float64))

	// Give the woken task a summary so the list can attach its first line.
	if _, err := db.Client().Task.UpdateOneID(taskID).
		SetSummary("순찰 보고 첫 줄\n두 번째 줄").Save(ctx); err != nil {
		t.Fatal(err)
	}

	code, out = getPatrols(t, app, "")
	if code != http.StatusOK {
		t.Fatalf("GET patrols: %d %v", code, out)
	}
	patrols := out["data"].(map[string]any)["patrols"].([]any)
	row := patrols[0].(map[string]any)
	if row["task_id"] != float64(taskID) || row["task_summary"] != "순찰 보고 첫 줄" {
		t.Fatalf("list must link the woken task and show its first line: %v", row)
	}
	findings := row["findings"].([]any)
	if len(findings) != 1 {
		t.Fatalf("findings JSON must decode: %v", row)
	}
	f := findings[0].(map[string]any)
	if f["fingerprint"] != fmt.Sprintf("failed_task:%d", failed.ID) {
		t.Fatalf("bad finding: %v", f)
	}

	// The daily cap now sees this patrol wake; with the cap at 1 a second
	// pass with new findings must skip with the cap reason.
	viper.Set("shepherd_max_wakeups_per_day", 1)
	if _, err := db.Client().Task.Create().SetPrompt("b").
		SetStatus(entTask.StatusFailed).SetError("boom2").SetProject(proj).
		SetCompletedAt(time.Now().Add(-time.Minute)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	// Finish the pending patrol task so the busy guard doesn't fire first.
	if _, err := db.Client().Task.UpdateOneID(taskID).
		SetStatus(entTask.StatusCompleted).Save(ctx); err != nil {
		t.Fatal(err)
	}

	code, out = postPatrolRun(t, app)
	if code != http.StatusOK {
		t.Fatalf("second run: %d %v", code, out)
	}
	rec2 := out["data"].(map[string]any)["patrol"].(map[string]any)
	if rec2["task_id"] != nil || rec2["skipped_reason"] != manager.SkipDailyCap {
		t.Fatalf("daily cap must skip the second wake: %v", rec2)
	}
}

// TestShepherdStatusPatrolFields pins the status card's next-patrol cell.
func TestShepherdStatusPatrolFields(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)

	req := httptest.NewRequest(http.MethodGet, "/api/shepherd/status", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	body := mustRead(resp)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	data := out["data"].(map[string]any)

	if data["patrol_enabled"] != true {
		t.Fatalf("default interval is on: %v", data)
	}
	if _, ok := data["next_patrol_at"]; !ok {
		t.Fatalf("next_patrol_at must be present when patrol is on: %v", data)
	}

	patchShepherdConfig(t, app, map[string]any{"patrol_interval": "0"})
	resp2, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/shepherd/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var out2 map[string]any
	body2 := mustRead(resp2)
	if err := json.Unmarshal(body2, &out2); err != nil {
		t.Fatalf("decode %s: %v", body2, err)
	}
	data2 := out2["data"].(map[string]any)
	if data2["patrol_enabled"] != false {
		t.Fatalf("interval 0 must report patrol off: %v", data2)
	}
	if _, ok := data2["next_patrol_at"]; ok {
		t.Fatalf("off patrol must not advertise a next time: %v", data2)
	}
}
