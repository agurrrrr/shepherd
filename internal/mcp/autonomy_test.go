package mcp

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/names"
)

// autonomyFixture seeds a manager-sheep caller task, an ordinary-sheep caller
// task, and one project with an assigned sheep so the gated handlers can run
// their real bodies. Caller identity arrives through the reserved arg key —
// exactly what the daemon proxy and the embedded dispatcher inject.
//
// Call it ONCE per test: withMCPTestDB's in-memory DSN is keyed by the test
// name, so a second call would collide with the first fixture's rows.
type autonomyFixture struct {
	client       *ent.Client
	shepherdTask int
	workerTask   int
	projectID    int
}

func newAutonomyFixture(t *testing.T) *autonomyFixture {
	t.Helper()
	ctx := context.Background()
	client := withMCPTestDB(t)

	mgr := client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)
	workerSh := client.Sheep.Create().SetName("양순이").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusIdle).SaveX(ctx)

	st := client.Task.Create().SetPrompt("shepherd run").SetStatus(entTask.StatusRunning).
		SetSheepID(mgr.ID).SaveX(ctx)
	wt := client.Task.Create().SetPrompt("flock run").SetStatus(entTask.StatusRunning).
		SetSheepID(workerSh.ID).SaveX(ctx)

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SetSheep(workerSh).SaveX(ctx)
	return &autonomyFixture{client: client, shepherdTask: st.ID, workerTask: wt.ID, projectID: proj.ID}
}

// setTier pins the autonomy tier for one case (viper only — never config.Set).
// It also restores the provider_enabled_* defaults viper.Reset() drops, so an
// allowed issue_execute can actually enqueue (issue.Execute gates on them).
func setTier(t *testing.T, tier string) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
	if tier != "" {
		viper.Set("shepherd_autonomy", tier)
	}
}

// TestShepherdPermissionGateThroughHandlers drives the #601 §2 table through
// the real MCP handlers: a refusal must be a tool error naming the tier and
// pointing at request_user_action, and it must happen BEFORE any work — a
// refused issue_upsert must not create an issue, a refused task_stop must not
// reach the queue layer.
func TestShepherdPermissionGateThroughHandlers(t *testing.T) {
	f := newAutonomyFixture(t)
	ctx := context.Background()

	iss := f.client.Issue.Create().SetTitle("delegated").SetStatus("todo").
		SetProjectID(f.projectID).SaveX(ctx)

	cases := []struct {
		tool   string
		tier   string
		refuse bool
		call   func(caller int) (string, error)
	}{
		{
			tool: "issue_upsert", tier: manager.AutonomyReport, refuse: true,
			call: func(caller int) (string, error) {
				return handleIssueUpsert(SetCallerTaskID(map[string]interface{}{
					"project_name": "bori", "title": "new work",
				}, caller))
			},
		},
		{
			tool: "issue_upsert", tier: manager.AutonomyPlan, refuse: false,
			call: func(caller int) (string, error) {
				return handleIssueUpsert(SetCallerTaskID(map[string]interface{}{
					"project_name": "bori", "title": "plan-tier work",
				}, caller))
			},
		},
		{
			tool: "issue_execute", tier: manager.AutonomyPlan, refuse: true,
			call: func(caller int) (string, error) {
				return handleIssueExecute(SetCallerTaskID(map[string]interface{}{
					"project_name": "bori", "id": iss.ID,
				}, caller))
			},
		},
		{
			tool: "issue_execute", tier: manager.AutonomyExecute, refuse: false,
			call: func(caller int) (string, error) {
				return handleIssueExecute(SetCallerTaskID(map[string]interface{}{
					"project_name": "bori", "id": iss.ID,
				}, caller))
			},
		},
		{
			tool: "task_stop", tier: manager.AutonomyExecute, refuse: true,
			call: func(caller int) (string, error) {
				return handleTaskStop(SetCallerTaskID(map[string]interface{}{
					"task_id": 999999,
				}, caller))
			},
		},
	}

	for _, tc := range cases {
		setTier(t, tc.tier)
		before := issueCount(t, f.client)
		out, err := tc.call(f.shepherdTask)

		if tc.refuse {
			if err == nil {
				t.Fatalf("%s at tier %q: expected refusal, got output %q", tc.tool, tc.tier, out)
			}
			if !strings.Contains(err.Error(), "request_user_action") || !strings.Contains(err.Error(), tc.tier) {
				t.Errorf("%s at tier %q: refusal text = %q, want tier + request_user_action hint", tc.tool, tc.tier, err)
			}
			if after := issueCount(t, f.client); after != before {
				t.Errorf("%s refused at %q must not change anything: %d → %d", tc.tool, tc.tier, before, after)
			}
			continue
		}

		if err != nil {
			t.Errorf("%s at tier %q: expected allow, got %v", tc.tool, tc.tier, err)
		}
	}

	// task_stop at `full` clears the gate and reaches the queue layer — proven
	// by the queue's own "not found" error instead of an autonomy refusal.
	setTier(t, manager.AutonomyFull)
	_, err := handleTaskStop(SetCallerTaskID(map[string]interface{}{"task_id": 999999}, f.shepherdTask))
	if err == nil {
		t.Fatal("stopping a nonexistent task must fail")
	}
	if strings.Contains(err.Error(), "autonomy tier") {
		t.Errorf("full tier must not be refused by the gate: %v", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected the queue-layer not-found error, got: %v", err)
	}

	// Ordinary sheep callers are untouched even at the most restrictive tier.
	setTier(t, manager.AutonomyReport)
	for _, call := range []func(caller int) (string, error){
		func(caller int) (string, error) {
			return handleTaskStop(SetCallerTaskID(map[string]interface{}{"task_id": 999999}, caller))
		},
		func(caller int) (string, error) {
			return handleIssueUpsert(SetCallerTaskID(map[string]interface{}{
				"project_name": "bori", "title": "flock work",
			}, caller))
		},
	} {
		if _, err := call(f.workerTask); err != nil && strings.Contains(err.Error(), "autonomy tier") {
			t.Errorf("ordinary sheep must not be permission-checked: %v", err)
		}
	}

	// Unidentified callers (no arg key, no env) are exempt too.
	t.Setenv("SHEPHERD_TASK_ID", "")
	if _, err := handleTaskStop(map[string]interface{}{"task_id": 999999}); err != nil &&
		strings.Contains(err.Error(), "autonomy tier") {
		t.Errorf("caller 0 must not be permission-checked: %v", err)
	}
	_ = ctx
}

// TestCallerTaskIDFromEnvFallback pins the env path: a `shepherd mcp` child has
// SHEPHERD_TASK_ID in its environment, so the gate must still see the caller.
func TestCallerTaskIDFromEnvFallback(t *testing.T) {
	f := newAutonomyFixture(t)
	setTier(t, manager.AutonomyReport)

	t.Setenv("SHEPHERD_TASK_ID", strconv.Itoa(f.shepherdTask))
	if got := callerTaskIDOrEnv(map[string]interface{}{}); got != f.shepherdTask {
		t.Fatalf("callerTaskIDOrEnv env fallback = %d, want %d", got, f.shepherdTask)
	}
	if _, err := handleTaskStop(map[string]interface{}{"task_id": 999999}); err == nil ||
		!strings.Contains(err.Error(), "request_user_action") {
		t.Fatalf("env-identified shepherd must be gated: %v", err)
	}

	// args win over env when both are present.
	t.Setenv("SHEPHERD_TASK_ID", "1")
	if got := callerTaskIDOrEnv(SetCallerTaskID(map[string]interface{}{}, f.shepherdTask)); got != f.shepherdTask {
		t.Errorf("args must take precedence over env: got %d, want %d", got, f.shepherdTask)
	}
}

// TestTaskStopCancelsPendingTask pins #626 cause 2: the MCP task_stop tool now
// covers queued rows too, through the daemon's cancel path (the same one
// POST /api/tasks/:id/cancel uses). The autonomy gate is unchanged — a shepherd
// below `full` is still refused for a pending target, and `full` cancels.
func TestTaskStopCancelsPendingTask(t *testing.T) {
	f := newAutonomyFixture(t)
	ctx := context.Background()

	queued := f.client.Task.Create().SetPrompt("queued duplicate").
		SetStatus(entTask.StatusPending).SaveX(ctx)

	// Below `full` a shepherd must be refused for the pending target as well:
	// the cancel branch must not become a way around the tier limit.
	setTier(t, manager.AutonomyExecute)
	if _, err := handleTaskStop(SetCallerTaskID(map[string]interface{}{
		"task_id": queued.ID,
	}, f.shepherdTask)); err == nil || !strings.Contains(err.Error(), "request_user_action") {
		t.Fatalf("pending cancel below full must be refused by the gate: %v", err)
	}
	if got := f.client.Task.GetX(ctx, queued.ID); got.Status != entTask.StatusPending {
		t.Errorf("refused call must not touch the row: status = %s", got.Status)
	}

	// At `full` the same tool cancels the queued row.
	setTier(t, manager.AutonomyFull)
	out, err := handleTaskStop(SetCallerTaskID(map[string]interface{}{
		"task_id": queued.ID, "reason": "중복 대기 작업",
	}, f.shepherdTask))
	if err != nil {
		t.Fatalf("task_stop at full on a pending task: %v", err)
	}
	if !strings.Contains(out, "취소") {
		t.Errorf("output should name the cancel: %q", out)
	}

	got := f.client.Task.GetX(ctx, queued.ID)
	if got.Status != entTask.StatusStopped {
		t.Errorf("task status = %s, want stopped (cancelled)", got.Status)
	}
	if got.Error != "cancelled by user" {
		t.Errorf("task error = %q, want the same marker the web cancel path writes", got.Error)
	}

	// Ordinary sheep keep today's unrestricted behaviour on the cancel branch.
	queued2 := f.client.Task.Create().SetPrompt("sheep-visible duplicate").
		SetStatus(entTask.StatusPending).SaveX(ctx)
	setTier(t, manager.AutonomyReport)
	if _, err := handleTaskStop(SetCallerTaskID(map[string]interface{}{
		"task_id": queued2.ID,
	}, f.workerTask)); err != nil {
		t.Errorf("ordinary sheep must not be permission-checked on cancel: %v", err)
	}
	if got := f.client.Task.GetX(ctx, queued2.ID); got.Status != entTask.StatusStopped {
		t.Errorf("sheep cancel left status = %s, want stopped", got.Status)
	}
}

// TestRequestUserActionHandlerCreatesRow pins the tool → manager wiring: valid
// kinds land a pending row carrying the caller; bad kind/reason/target are
// refused before anything is stored. request_user_action itself is never
// tier-gated — every caller may raise one (#601 §4).
func TestRequestUserActionHandlerCreatesRow(t *testing.T) {
	f := newAutonomyFixture(t)

	iss := f.client.Issue.Create().SetTitle("to delete").SetStatus("todo").
		SetProjectID(f.projectID).SaveX(ctx0())

	// Even the most restrictive tier may raise a request.
	setTier(t, manager.AutonomyReport)

	out, err := handleRequestUserAction(SetCallerTaskID(map[string]interface{}{
		"kind":         manager.RequestDeleteIssue,
		"project_name": "bori",
		"target_id":    iss.ID,
		"reason":       "중복 이슈라 정리하고 싶습니다",
	}, f.shepherdTask))
	if err != nil {
		t.Fatalf("request_user_action: %v", err)
	}
	if !strings.Contains(out, "요청 #1") {
		t.Errorf("output = %q, want the request id echoed", out)
	}

	row := firstRequest(t, f.client)
	if row.Kind != manager.RequestDeleteIssue || row.Status != manager.RequestPending {
		t.Errorf("row = %s/%s, want delete_issue/pending", row.Kind, row.Status)
	}
	if row.TargetID != iss.ID || row.ProjectName != "bori" {
		t.Errorf("row target/project = %d/%q, want %d/bori", row.TargetID, row.ProjectName, iss.ID)
	}
	if row.RequestedByTaskID != f.shepherdTask {
		t.Errorf("requested_by_task_id = %d, want %d", row.RequestedByTaskID, f.shepherdTask)
	}

	// A non-shepherd caller may raise one too, and it records no caller.
	if _, err := handleRequestUserAction(map[string]interface{}{
		"kind":   manager.RequestOther,
		"reason": "사용자 확인이 필요합니다",
	}); err != nil {
		t.Fatalf("non-shepherd request_user_action: %v", err)
	}

	for _, bad := range []map[string]interface{}{
		{"kind": "nuke_project", "reason": "x"},                                  // unknown kind
		{"kind": manager.RequestStopTask, "reason": "x"},                         // missing target_id
		{"kind": manager.RequestExecuteIssue, "target_id": 1, "reason": "x"},     // missing project
		{"kind": manager.RequestOther},                                           // missing reason
	} {
		if _, err := handleRequestUserAction(bad); err == nil {
			t.Errorf("bad args %v must be refused", bad)
		}
	}

	if n := requestCount(t, f.client); n != 2 {
		t.Errorf("requests stored = %d, want 2 (bad calls must not store)", n)
	}
}

func ctx0() context.Context { return context.Background() }

func issueCount(t *testing.T, client *ent.Client) int {
	t.Helper()
	n, err := client.Issue.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("count issues: %v", err)
	}
	return n
}

func firstRequest(t *testing.T, client *ent.Client) *ent.ShepherdRequest {
	t.Helper()
	row, err := client.ShepherdRequest.Query().Order(ent.Asc("id")).First(context.Background())
	if err != nil {
		t.Fatalf("first request: %v", err)
	}
	return row
}

func requestCount(t *testing.T, client *ent.Client) int {
	t.Helper()
	n, err := client.ShepherdRequest.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("count requests: %v", err)
	}
	return n
}
