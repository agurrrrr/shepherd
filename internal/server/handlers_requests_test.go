package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/spf13/viper"
)

// Shepherd request API (#601): the panel's list plus approve/reject, and the
// autonomy fields on GET /api/shepherd/config and /api/shepherd/status.

// withRequestTestEnv is withShepherdTestEnv + the raw-handle swap (same DB as
// the ent client): ApproveShepherdRequest wakes the shepherd through
// queue.AppendToPendingReviewTaskPrompt, which runs raw SQL.
func withRequestTestEnv(t *testing.T) *ent.Client {
	t.Helper()

	sqlDB, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
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

	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}

	orig := persistConfig
	persistConfig = func(values map[string]interface{}) error {
		for k, v := range values {
			viper.Set(k, v)
		}
		return nil
	}
	t.Cleanup(func() { persistConfig = orig })

	return client
}

func requestsApp(t *testing.T) *fiber.App {
	t.Helper()
	s := &Server{hub: NewSSEHub()}
	app := fiber.New()
	app.Get("/api/shepherd/requests", s.handleListShepherdRequests)
	app.Post("/api/shepherd/requests/:id/approve", s.handleApproveShepherdRequest)
	app.Post("/api/shepherd/requests/:id/reject", s.handleRejectShepherdRequest)
	return app
}

func shepherdReviewTasks(t *testing.T) []*ent.Task {
	t.Helper()
	ts, err := db.Client().Task.Query().
		Where(entTask.ShepherdKindEQ(manager.KindReview)).
		All(context.Background())
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	return ts
}

// ---- list ------------------------------------------------------------------

func TestListShepherdRequestsDefaultPending(t *testing.T) {
	withRequestTestEnv(t)
	app := requestsApp(t)

	// Two pending + one decided (rejected directly through the manager layer).
	r1, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "승인 대기 1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "승인 대기 2", 0); err != nil {
		t.Fatal(err)
	}
	r3, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "이미 거절됨", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RejectShepherdRequest(r3.ID, "동의할 수 없습니다"); err != nil {
		t.Fatal(err)
	}

	code, out := getJSON(t, app, "/api/shepherd/requests")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %v", code, out)
	}
	data := out["data"].(map[string]any)
	reqs := data["requests"].([]any)
	if data["count"] != 2.0 || len(reqs) != 2 {
		t.Fatalf("default must list only pending: %v", data)
	}
	// Newest first: r1 is the OLDEST pending row → last in the list.
	first := reqs[0].(map[string]any)
	if int(first["id"].(float64)) == r1.ID {
		t.Errorf("list must be newest-first, got %v", reqs)
	}

	code, out = getJSON(t, app, "/api/shepherd/requests?status=all")
	if code != http.StatusOK || out["data"].(map[string]any)["count"] != 3.0 {
		t.Fatalf("status=all must include decided rows: %d %v", code, out)
	}

	code, out = getJSON(t, app, "/api/shepherd/requests?status=rejected")
	if code != http.StatusOK {
		t.Fatalf("status=rejected: %d %v", code, out)
	}
	rej := out["data"].(map[string]any)["requests"].([]any)
	if len(rej) != 1 {
		t.Fatalf("want one rejected row: %v", rej)
	}
	row := rej[0].(map[string]any)
	if row["result"] != "동의할 수 없습니다" {
		t.Errorf("rejection note must be stored in result: %v", row["result"])
	}
}

// ---- approve: kind actions --------------------------------------------------

func TestApproveOtherRequestRecordsOnly(t *testing.T) {
	withRequestTestEnv(t)
	app := requestsApp(t)

	rec, err := manager.CreateShepherdRequest(manager.RequestOther, "bori", 0, "확인해 주세요", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if row["status"] != manager.RequestApproved {
		t.Errorf("status = %v, want approved", row["status"])
	}
	if row["result"] != "승인 기록만 남겼습니다 (실행할 조치 없음)." {
		t.Errorf("other must record a no-action result: %v", row["result"])
	}
	if row["decided_at"] == nil {
		t.Error("decided_at must be set")
	}

	// The decision wakes the shepherd with a review task.
	revs := shepherdReviewTasks(t)
	if len(revs) != 1 {
		t.Fatalf("approval must enqueue one review task, got %d", len(revs))
	}
	prompt := revs[0].Prompt
	for _, needle := range []string{"[Shepherd request decision]", "기타", "approved", "확인해 주세요"} {
		if !strings.Contains(prompt, needle) {
			t.Errorf("review prompt missing %q:\n%s", needle, prompt)
		}
	}

	// A second approve is a conflict — the row is already decided.
	code, out = postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusConflict {
		t.Fatalf("re-approve: status = %d, want 409 (%v)", code, out)
	}

	// Unknown ids are 404.
	code, _ = postJSON(t, app, "/api/shepherd/requests/99999/approve", nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404", code)
	}
}

func TestApproveStopTaskFailureLandsInResult(t *testing.T) {
	withRequestTestEnv(t)
	app := requestsApp(t)

	// A nonexistent task: the stop action fails softly into `result`, the
	// approval still counts as decided (a stale row must not wedge the panel).
	rec, err := manager.CreateShepherdRequest(manager.RequestStopTask, "", 999999, "멈춰 주세요", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if row["status"] != manager.RequestApproved {
		t.Fatalf("status = %v, want approved even when the target vanished", row["status"])
	}
	if !strings.Contains(row["result"].(string), "작업 중지 실패") || !strings.Contains(row["result"].(string), "not found") {
		t.Errorf("stop failure must land in result: %v", row["result"])
	}
	if len(shepherdReviewTasks(t)) != 1 {
		t.Error("the shepherd must still be woken with the failure outcome")
	}
}

// TestApproveStopTaskPendingCancels pins #626 cause 1: a stop_task approval
// whose target is still queued must go through the daemon's cancel path
// (queue.CancelPendingTask, the same one POST /api/tasks/:id/cancel uses) and
// record a success phrase. Before #626 this failed with
// "task #N is not running (status: pending)" and the row stayed queued.
func TestApproveStopTaskPendingCancels(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	workerSh := client.Sheep.Create().SetName("양순이").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusIdle).SaveX(ctx)
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SetSheep(workerSh).SaveX(ctx)
	queued := client.Task.Create().SetPrompt("duplicate in queue").SetStatus(entTask.StatusPending).
		SetSheep(workerSh).SetProject(proj).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestStopTask, "", queued.ID, "중복 대기 작업이라 취소합니다", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if row["status"] != manager.RequestApproved {
		t.Fatalf("status = %v, want approved", row["status"])
	}
	if !strings.Contains(row["result"].(string), "대기 취소 완료") {
		t.Errorf("pending target must record the cancel success phrase: %v", row["result"])
	}

	got := client.Task.GetX(ctx, queued.ID)
	if got.Status != entTask.StatusStopped {
		t.Errorf("task status = %s, want stopped (cancelled)", got.Status)
	}
	if got.Error != "cancelled by user" {
		t.Errorf("task error = %q, want the same marker POST /api/tasks/:id/cancel writes", got.Error)
	}
	if len(shepherdReviewTasks(t)) != 1 {
		t.Error("the shepherd must be woken with the cancel outcome")
	}
}

// TestApproveStopTaskPendingKeepsIssueWithLiveSibling pins #626 cause 3 through
// the approval path: cancelling one of two queued duplicates must not mark the
// issue failed while a live sibling remains.
func TestApproveStopTaskPendingKeepsIssueWithLiveSibling(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	iss := client.Issue.Create().SetTitle("duplicate queue").SetStatus("in_progress").
		SetProject(proj).SaveX(ctx)
	first := client.Task.Create().SetPrompt("drop me").SetStatus(entTask.StatusPending).
		SetIssueID(iss.ID).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("will run").SetStatus(entTask.StatusPending).
		SetIssueID(iss.ID).SetProject(proj).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestStopTask, "", first.ID, "중복 취소", 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}

	got := client.Issue.GetX(ctx, iss.ID)
	if got.Status != entIssue.StatusInProgress {
		t.Errorf("issue status = %s, want in_progress while a queued sibling remains", got.Status)
	}
}

// TestApproveStopTaskRunningStillUsesStopPath shows the running branch is
// unchanged: it still calls queue.StopTaskByID (the worker kill), so a running
// row with no live provider process fails softly instead of being cancelled.
func TestApproveStopTaskRunningStillUsesStopPath(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	workerSh := client.Sheep.Create().SetName("양순이").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusIdle).SaveX(ctx)
	live := client.Task.Create().SetPrompt("actually running").SetStatus(entTask.StatusRunning).
		SetSheep(workerSh).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestStopTask, "", live.ID, "멈춰 주세요", 0)
	if err != nil {
		t.Fatal(err)
	}
	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	result := row["result"].(string)
	// No provider process is registered for this sheep in tests, so the stop
	// path fails at the worker layer — proof it took the STOP branch and never
	// touched the pending-cancel branch.
	if !strings.Contains(result, "작업 중지 실패") || !strings.Contains(result, "no running task") {
		t.Errorf("running target must go through StopTaskByID: %v", result)
	}
	if got := client.Task.GetX(ctx, live.ID); got.Status != entTask.StatusRunning {
		t.Errorf("running task status = %s, want untouched running row", got.Status)
	}
}

func TestApproveExecuteIssueEnqueuesWatchedWork(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	workerSh := client.Sheep.Create().SetName("양순이").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusIdle).SaveX(ctx)
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SetSheep(workerSh).SaveX(ctx)
	iss := client.Issue.Create().SetTitle("approved work").SetStatus("todo").
		SetProject(proj).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestExecuteIssue, "bori", iss.ID, "이 이슈를 실행해도 됩니다", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if !strings.Contains(row["result"].(string), "실행 적재 완료") {
		t.Fatalf("execute result: %v", row["result"])
	}

	// The enqueued task exists AND the issue is watched (approved delegation =
	// watched work, #597): its completion wakes the shepherd review path.
	n, err := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(iss.ID))).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Error("no task was enqueued for the approved issue")
	}
	reloaded, err := client.Issue.Get(ctx, iss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ShepherdWatch {
		t.Error("approved execute must mark the issue shepherd-watched")
	}
}

func TestApproveDeleteIssueRemovesIt(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	iss := client.Issue.Create().SetTitle("duplicate").SetStatus("todo").SetProject(proj).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestDeleteIssue, "bori", iss.ID, "중복 이슈", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil)
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if !strings.Contains(row["result"].(string), "삭제 완료") {
		t.Fatalf("delete result: %v", row["result"])
	}

	if _, err := client.Issue.Get(ctx, iss.ID); err == nil {
		t.Error("the approved issue must be gone")
	}
}

func TestRejectRunsNoActionButWakes(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	iss := client.Issue.Create().SetTitle("keep me").SetStatus("todo").SetProject(proj).SaveX(ctx)

	rec, err := manager.CreateShepherdRequest(manager.RequestDeleteIssue, "bori", iss.ID, "지워도 될까요?", 0)
	if err != nil {
		t.Fatal(err)
	}

	code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/reject", map[string]any{"note": "삭제하지 마세요"})
	if code != http.StatusOK {
		t.Fatalf("reject: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["request"].(map[string]any)
	if row["status"] != manager.RequestRejected || row["result"] != "삭제하지 마세요" {
		t.Errorf("rejection record = %v", row)
	}

	// NO action ran — the issue is still there.
	if _, err := client.Issue.Get(ctx, iss.ID); err != nil {
		t.Errorf("reject must not delete anything: %v", err)
	}

	// The shepherd still wakes (knowing "no" is what lets it change course).
	revs := shepherdReviewTasks(t)
	if len(revs) != 1 || !strings.Contains(revs[0].Prompt, "rejected") {
		t.Fatalf("rejection must wake the shepherd: %v", revs)
	}

	// Empty note falls back to the default text.
	rec2, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "물음", 0)
	if err != nil {
		t.Fatal(err)
	}
	code, out = postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec2.ID)+"/reject", map[string]any{})
	if code != http.StatusOK {
		t.Fatalf("reject empty note: %d %v", code, out)
	}
	if out["data"].(map[string]any)["request"].(map[string]any)["result"] != "사용자가 거절했습니다." {
		t.Errorf("default rejection note missing: %v", out)
	}
}

// TestDecisionCoalescesIntoPendingReview pins #597's merge rule for request
// decisions: with a review task already pending, the decision is APPENDED to
// its prompt instead of stacking a second wake.
func TestDecisionCoalescesIntoPendingReview(t *testing.T) {
	withRequestTestEnv(t)
	app := requestsApp(t)

	pending, err := manager.EnqueueShepherdTask(manager.KindReview, "original review", 0)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "coalesce me", 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := postJSON(t, app, "/api/shepherd/requests/"+strconv.Itoa(rec.ID)+"/approve", nil); code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}

	revs := shepherdReviewTasks(t)
	if len(revs) != 1 {
		t.Fatalf("decision must merge into the pending review task, got %d tasks", len(revs))
	}
	if revs[0].ID != pending.ID {
		t.Errorf("the pre-existing pending review must be the one extended")
	}
	if !strings.Contains(revs[0].Prompt, "original review") || !strings.Contains(revs[0].Prompt, "[Shepherd request decision]") {
		t.Errorf("merged prompt missing either block:\n%s", revs[0].Prompt)
	}
}

// ---- requester identity -----------------------------------------------------

func TestRequestListShowsRequester(t *testing.T) {
	ctx := context.Background()
	client := withRequestTestEnv(t)
	app := requestsApp(t)

	mgrSheep := client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)
	caller := client.Task.Create().SetPrompt("shepherd run").SetStatus(entTask.StatusRunning).
		SetSheepID(mgrSheep.ID).SaveX(ctx)

	if _, err := manager.CreateShepherdRequest(manager.RequestOther, "", 0, "목자의 요청", caller.ID); err != nil {
		t.Fatal(err)
	}

	code, out := getJSON(t, app, "/api/shepherd/requests")
	if code != http.StatusOK {
		t.Fatalf("list: %d %v", code, out)
	}
	row := out["data"].(map[string]any)["requests"].([]any)[0].(map[string]any)
	if row["requester_sheep"] != names.ManagerName {
		t.Errorf("requester_sheep = %v, want %s", row["requester_sheep"], names.ManagerName)
	}
	if int(row["requested_by_task_id"].(float64)) != caller.ID {
		t.Errorf("requested_by_task_id = %v, want %d", row["requested_by_task_id"], caller.ID)
	}
}

// ---- autonomy settings + status card ----------------------------------------

func TestShepherdConfigAutonomyFields(t *testing.T) {
	withShepherdTestEnv(t)
	app := shepherdApp(t)

	// Defaults: execute / 3 / 0 (no limit) — effective values, not raw keys.
	code, payload := getShepherdConfig(t, app)
	if code != http.StatusOK {
		t.Fatalf("GET config: %d %v", code, payload)
	}
	data := payload["data"].(map[string]any)
	if data["autonomy"] != "execute" {
		t.Errorf("default autonomy = %v, want execute", data["autonomy"])
	}
	if data["max_active_delegations"] != 3.0 {
		t.Errorf("default max_active_delegations = %v, want 3", data["max_active_delegations"])
	}
	if data["daily_cost_limit_usd"] != 0.0 {
		t.Errorf("default daily_cost_limit_usd = %v, want 0", data["daily_cost_limit_usd"])
	}

	for _, tier := range []string{"report", "plan", "execute", "full"} {
		code, payload = patchShepherdConfig(t, app, map[string]any{"autonomy": tier})
		if code != http.StatusOK {
			t.Fatalf("PATCH autonomy=%s: %d %v", tier, code, payload)
		}
		if got := payload["data"].(map[string]any)["autonomy"]; got != tier {
			t.Errorf("stored autonomy = %v, want %s", got, tier)
		}
	}

	// Unknown tiers are rejected (AutonomyTier would silently re-map them).
	code, payload = patchShepherdConfig(t, app, map[string]any{"autonomy": "yolo"})
	if code != http.StatusBadRequest {
		t.Fatalf("bad tier: %d %v", code, payload)
	}

	// Caps: 0 is legal (default / no limit), negatives are rejected.
	code, payload = patchShepherdConfig(t, app, map[string]any{
		"max_active_delegations": 5, "daily_cost_limit_usd": 12.5,
	})
	if code != http.StatusOK {
		t.Fatalf("caps: %d %v", code, payload)
	}
	data = payload["data"].(map[string]any)
	if data["max_active_delegations"] != 5.0 || data["daily_cost_limit_usd"] != 12.5 {
		t.Errorf("caps round-trip failed: %v", data)
	}
	if code, payload = patchShepherdConfig(t, app, map[string]any{"max_active_delegations": -1}); code != http.StatusBadRequest {
		t.Fatalf("negative delegations: %d %v", code, payload)
	}
	if code, payload = patchShepherdConfig(t, app, map[string]any{"daily_cost_limit_usd": -0.5}); code != http.StatusBadRequest {
		t.Fatalf("negative cost limit: %d %v", code, payload)
	}

	// 0 on delegations falls back to the effective default 3 in the payload.
	if code, payload = patchShepherdConfig(t, app, map[string]any{"max_active_delegations": 0}); code != http.StatusOK {
		t.Fatalf("zero delegations: %d %v", code, payload)
	}
	if got := payload["data"].(map[string]any)["max_active_delegations"]; got != 3.0 {
		t.Errorf("0 stored must report the effective default 3, got %v", got)
	}
}

func TestShepherdStatusAutonomyCard(t *testing.T) {
	ctx := context.Background()
	client := withShepherdTestEnvForStatus(t)
	app := shepherdApp(t)

	viper.Set("shepherd_autonomy", "plan")
	viper.Set("shepherd_daily_cost_limit_usd", 5.0)

	// One active delegation on a watched issue + today's spend over the cap.
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("w").SetStatus("todo").
		SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("delegated").SetStatus(entTask.StatusPending).
		SetIssueID(watched.ID).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("spent").SetStatus(entTask.StatusCompleted).
		SetShepherdKind(manager.KindReview).SetCostUsd(6.0).SaveX(ctx)

	data := shepherdStatus(t, app)
	if data["autonomy_tier"] != "plan" {
		t.Errorf("autonomy_tier = %v, want plan", data["autonomy_tier"])
	}
	if data["max_active_delegations"] != 3.0 || data["active_delegations"] != 1.0 {
		t.Errorf("delegations = %v/%v, want max 3 active 1", data["max_active_delegations"], data["active_delegations"])
	}
	if data["daily_cost_limit_usd"] != 5.0 {
		t.Errorf("daily_cost_limit_usd = %v, want 5", data["daily_cost_limit_usd"])
	}
	spent, _ := data["today_cost"].(float64)
	if spent < 5.99 {
		t.Errorf("today_cost (combined window) = %v, want ≥ 6", spent)
	}
	if data["cost_limit_exceeded"] != true {
		t.Errorf("over the cap must flip cost_limit_exceeded: %v", data["cost_limit_exceeded"])
	}

	// Under the cap → exceeded false.
	viper.Set("shepherd_daily_cost_limit_usd", 100.0)
	data = shepherdStatus(t, app)
	if data["cost_limit_exceeded"] != false {
		t.Errorf("under the cap: %v", data["cost_limit_exceeded"])
	}
}

// withShepherdTestEnvForStatus is withShepherdTestEnv but returns the client
// and shares one in-memory DB (the default fixture works for status reads).
func withShepherdTestEnvForStatus(t *testing.T) *ent.Client {
	t.Helper()
	withShepherdTestEnv(t)
	return db.Client()
}

func shepherdStatus(t *testing.T, app *fiber.App) map[string]any {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/shepherd/status", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	body := mustRead(resp)
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out["data"].(map[string]any)
}

