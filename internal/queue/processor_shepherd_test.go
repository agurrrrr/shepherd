package queue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// shepherdDispatchTestEnv gives the processor an in-memory DB, a workspace
// directory, and provider defaults matching a running daemon. The manager
// sheep's provider is set to "embedded": no embedded executor is registered in
// tests, so a dispatched run fails fast ("embedded executor not initialized")
// instead of invoking a real CLI. That error also proves the run reached the
// provider stage — i.e. it got past the project/WorkDir check.
func shepherdDispatchTestEnv(t *testing.T) (*ent.Client, string) {
	t.Helper()

	client := withTestDB(t)

	viper.Set("workspace_path", t.TempDir())
	viper.Set("max_concurrent_tasks", 0)
	viper.Set("concurrency_limits", map[string]int{})
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.Set("provider_enabled_"+p, true)
	}
	t.Cleanup(func() {
		viper.Set("workspace_path", "")
		viper.Set("max_concurrent_tasks", 0)
		viper.Set("concurrency_limits", map[string]int{})
	})

	ctx := context.Background()
	mgr, err := client.Sheep.Create().
		SetName(names.ManagerName).
		SetStatus(sheep.StatusIdle).
		SetProvider(sheep.ProviderEmbedded).
		Save(ctx)
	if err != nil {
		t.Fatalf("create manager sheep: %v", err)
	}

	return client, mgr.Name
}

func createShepherdTask(t *testing.T, client *ent.Client, sheepID int, kind string, priority int) *ent.Task {
	t.Helper()
	ctx := context.Background()
	return client.Task.Create().
		SetPrompt("list the subdirectories of the workspace").
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		SetShepherdKind(kind).
		SetPriority(priority).
		SaveX(ctx)
}

// waitTaskSettled waits until the task row reaches a terminal status and the
// in-memory claim placeholder is released (executeTask's deferred
// ReleaseDispatch), then returns the refreshed task row. The helper waits on
// the queue's own dispatch state rather than on the sheep's DB status column:
// with no embedded executor registered, executeWithEmbedded returns before any
// registerRunningTask call, so ExecuteInteractive's restore-status step still
// sees the ClaimDispatch placeholder and skips its write. That quirk belongs to
// the stub-provider path itself — TestDispatchShepherdDoesNotAffectOrdinarySheep
// shows the ordinary-sheep loop behaves the same way — and is unrelated to #594.
func waitTaskSettled(t *testing.T, client *ent.Client, sheepName string, taskID int) *ent.Task {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		row := client.Task.GetX(ctx, taskID)
		settled := row.Status == task.StatusFailed || row.Status == task.StatusCompleted || row.Status == task.StatusStopped
		if settled && !worker.IsTaskRunning(sheepName) {
			return row
		}
		time.Sleep(20 * time.Millisecond)
	}
	row := client.Task.GetX(ctx, taskID)
	s, _ := client.Sheep.Query().Where(sheep.Name(sheepName)).Only(ctx)
	t.Fatalf("task #%d did not settle within 10s: task{status:%s err:%q} sheep=%+v running=%v",
		taskID, row.Status, row.Error, s, worker.IsTaskRunning(sheepName))
	return nil
}

// Workspace unset: the shepherd task must stay pending and the manager must
// not be claimed. This is the core "no workspace → no dispatch" guarantee.
func TestDispatchShepherdKeepsTaskPendingWithoutWorkspace(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)
	viper.Set("workspace_path", "")

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	created := createShepherdTask(t, client, mgr.ID, "command", 1)

	p := NewProcessor(time.Second)
	p.checkAndExecutePendingTasks()

	got := client.Task.GetX(ctx, created.ID)
	if got.Status != task.StatusPending {
		t.Errorf("task status = %s, want pending (no workspace configured)", got.Status)
	}
	mgr = client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	if mgr.Status != sheep.StatusIdle {
		t.Errorf("manager status = %s, want idle", mgr.Status)
	}
	if worker.IsTaskRunning(names.ManagerName) {
		t.Errorf("manager sheep must not be claimed without a workspace")
	}

	// Invalid (non-existent) workspace: same behavior.
	viper.Set("workspace_path", "/nonexistent/shepherd-workspace-xyz")
	p.checkAndExecutePendingTasks()
	got = client.Task.GetX(ctx, created.ID)
	if got.Status != task.StatusPending {
		t.Errorf("task status = %s after invalid workspace, want pending", got.Status)
	}
}

// Workspace configured: the task dispatches to the manager. The run fails fast
// (no embedded executor in tests), which proves it passed the workDir check —
// a project-check failure would have produced "no project assigned" instead —
// and records that error on the task row.
func TestDispatchShepherdDispatchesWithWorkspace(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	created := createShepherdTask(t, client, mgr.ID, "command", 1)

	p := NewProcessor(time.Second)
	p.checkAndExecutePendingTasks()

	// Claim happened synchronously inside dispatchShepherd.
	if !worker.IsTaskRunning(names.ManagerName) {
		t.Fatalf("manager sheep was not claimed for dispatch")
	}

	got := waitTaskSettled(t, client, names.ManagerName, created.ID)
	if got.Status != task.StatusFailed {
		t.Fatalf("task status = %s, want failed (embedded executor missing in test env)", got.Status)
	}
	if !strings.Contains(got.Error, "embedded executor not initialized") {
		t.Errorf("task error = %q; want the embedded-executor error, which proves the run passed the workDir check (not 'no project assigned')", got.Error)
	}
	// The run must skip the git checkpoint entirely (#594): the emitted line
	// carries the shepherd-specific reason, not a repo-probe skip like the
	// ordinary-sheep path produces.
	if !strings.Contains(strings.Join(got.Output, "\n"), "checkpoint skipped: shepherd task") {
		t.Errorf("shepherd task output lacks the checkpoint-skip line: %q", got.Output)
	}
}

// Legacy project-less manager tasks (shepherd_kind empty) are NOT dispatched —
// their pre-#594 behavior (never runnable) is preserved.
func TestDispatchShepherdIgnoresTasksWithoutKind(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	created := client.Task.Create().
		SetPrompt("legacy manager task without kind").
		SetStatus(task.StatusPending).
		SetSheepID(mgr.ID).
		SaveX(ctx)

	p := NewProcessor(time.Second)
	p.checkAndExecutePendingTasks()

	got := client.Task.GetX(ctx, created.ID)
	if got.Status != task.StatusPending {
		t.Errorf("legacy task status = %s, want pending (must not be dispatched)", got.Status)
	}
}

// Ordinary sheep dispatch keeps working unchanged: a project-assigned sheep
// with a pending task still dispatches through the normal loop.
func TestDispatchShepherdDoesNotAffectOrdinarySheep(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	proj := client.Project.Create().
		SetName("ordinary-proj").
		SetPath(t.TempDir()).
		SaveX(ctx)
	workerSheep := client.Sheep.Create().
		SetName("양순이").
		SetStatus(sheep.StatusIdle).
		SetProvider(sheep.ProviderEmbedded).
		SetProjectID(proj.ID).
		SaveX(ctx)
	created := client.Task.Create().
		SetPrompt("ordinary work").
		SetStatus(task.StatusPending).
		SetSheepID(workerSheep.ID).
		SetProjectID(proj.ID).
		SaveX(ctx)

	p := NewProcessor(time.Second)
	p.checkAndExecutePendingTasks()

	got := waitTaskSettled(t, client, "양순이", created.ID)
	if got.Status != task.StatusFailed {
		t.Fatalf("ordinary task status = %s, want failed (embedded executor missing in test env)", got.Status)
	}
	if !strings.Contains(got.Error, "embedded executor not initialized") {
		t.Errorf("ordinary task error = %q, want the embedded-executor error", got.Error)
	}
}

// Global ceiling: a running task already at max_concurrent_tasks blocks the
// shepherd dispatch too (counted in totalRunning via CountRunningByGroup).
func TestDispatchShepherdRespectsGlobalCeiling(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)

	// One running task on the manager's own group already fills the ceiling.
	client.Task.Create().
		SetPrompt("running work").
		SetStatus(task.StatusRunning).
		SetSheepID(mgr.ID).
		SaveX(ctx)

	viper.Set("max_concurrent_tasks", 1)
	defer viper.Set("max_concurrent_tasks", 0)

	shepherdTask := createShepherdTask(t, client, mgr.ID, "command", 1)

	p := NewProcessor(time.Second)
	p.checkAndExecutePendingTasks()

	got := client.Task.GetX(ctx, shepherdTask.ID)
	if got.Status != task.StatusPending {
		t.Errorf("shepherd task status = %s, want pending under the global ceiling", got.Status)
	}
	if worker.IsTaskRunning(names.ManagerName) {
		t.Errorf("manager sheep must not be claimed at the ceiling")
	}
}

// A user command (priority 1) is picked before a queued review task
// (priority 0).
func TestGetPendingShepherdTaskBySheepPriorityOrder(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)

	review := createShepherdTask(t, client, mgr.ID, "review", 0)
	command := createShepherdTask(t, client, mgr.ID, "command", 1)

	first, err := GetPendingShepherdTaskBySheep(mgr.ID)
	if err != nil {
		t.Fatalf("GetPendingShepherdTaskBySheep: %v", err)
	}
	if first == nil || first.ID != command.ID {
		t.Errorf("first pending = %v, want the priority-1 command task #%d", first, command.ID)
	}

	_ = client.Task.UpdateOneID(command.ID).SetStatus(task.StatusCompleted).SaveX(ctx)
	second, err := GetPendingShepherdTaskBySheep(mgr.ID)
	if err != nil {
		t.Fatalf("GetPendingShepherdTaskBySheep: %v", err)
	}
	if second == nil || second.ID != review.ID {
		t.Errorf("second pending = %v, want the review task #%d", second, review.ID)
	}
}

// ListShepherdTasks filters by kind/status and excludes non-shepherd rows.
func TestListShepherdTasks(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)

	command := createShepherdTask(t, client, mgr.ID, "command", 1)
	review := createShepherdTask(t, client, mgr.ID, "review", 0)
	legacy := client.Task.Create().
		SetPrompt("not a shepherd task").
		SetStatus(task.StatusPending).
		SetSheepID(mgr.ID).
		SaveX(ctx)

	all, err := ListShepherdTasks("", "", 0)
	if err != nil {
		t.Fatalf("ListShepherdTasks: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2 (legacy task excluded)", len(all))
	}
	// Newest first.
	if all[0].ID != review.ID {
		t.Errorf("newest-first order broken: got #%d want #%d", all[0].ID, review.ID)
	}

	byKind, err := ListShepherdTasks("command", "", 0)
	if err != nil {
		t.Fatalf("ListShepherdTasks(kind): %v", err)
	}
	if len(byKind) != 1 || byKind[0].ID != command.ID {
		t.Errorf("kind filter broken: %+v", byKind)
	}

	byStatus, err := ListShepherdTasks("", "completed", 0)
	if err != nil {
		t.Fatalf("ListShepherdTasks(status): %v", err)
	}
	if len(byStatus) != 0 {
		t.Errorf("status filter broken: %+v", byStatus)
	}

	_ = legacy

	// shepherd_kind is persisted and readable.
	if got := GetTaskShepherdKind(command.ID); got != "command" {
		t.Errorf("GetTaskShepherdKind = %q, want command", got)
	}
	if got := GetTaskShepherdKind(legacy.ID); got != "" {
		t.Errorf("GetTaskShepherdKind(legacy) = %q, want empty", got)
	}
}

// ListShepherdTasksFiltered adds offset paging + prompt/summary search, and
// CountShepherdTasks counts the same filter (#623). Created timestamps are
// pinned so the newest-first order is deterministic.
func TestListShepherdTasksFilterPagingSearch(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)

	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)

	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	mk := func(kind, prompt, summary string, minute int) *ent.Task {
		row := client.Task.Create().
			SetPrompt(prompt).
			SetStatus(task.StatusPending).
			SetSheepID(mgr.ID).
			SetShepherdKind(kind).
			SetCreatedAt(base.Add(time.Minute * time.Duration(minute))).
			SaveX(ctx)
		if summary != "" {
			row = client.Task.UpdateOneID(row.ID).SetSummary(summary).SaveX(ctx)
		}
		return row
	}

	first := mk("command", "check the workspace flock", "", 0)
	second := mk("review", "unrelated prompt", "report mentions workspace health", 1)
	mk("patrol", "patrol sweep", "", 2) // newest, no match for "workspace"

	// Count matches the same filter regardless of Limit/Offset.
	total, err := CountShepherdTasks(ShepherdTaskFilter{})
	if err != nil {
		t.Fatalf("CountShepherdTasks: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}

	// Offset paging: page 2 with limit 2 returns the oldest row.
	page2, err := ListShepherdTasksFiltered(ShepherdTaskFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("ListShepherdTasksFiltered(offset): %v", err)
	}
	if len(page2) != 1 || page2[0].ID != first.ID {
		t.Errorf("offset page = %+v, want only #%d", page2, first.ID)
	}

	// Search hits the prompt field.
	byPrompt, err := ListShepherdTasksFiltered(ShepherdTaskFilter{Query: "flock"})
	if err != nil {
		t.Fatalf("ListShepherdTasksFiltered(query prompt): %v", err)
	}
	if len(byPrompt) != 1 || byPrompt[0].ID != first.ID {
		t.Errorf("prompt search = %+v, want only #%d", byPrompt, first.ID)
	}

	// Search hits the summary field (same Or() shape as GET /api/tasks).
	bySummary, err := ListShepherdTasksFiltered(ShepherdTaskFilter{Query: "workspace health"})
	if err != nil {
		t.Fatalf("ListShepherdTasksFiltered(query summary): %v", err)
	}
	if len(bySummary) != 1 || bySummary[0].ID != second.ID {
		t.Errorf("summary search = %+v, want only #%d", bySummary, second.ID)
	}

	// Count honors the search term too, so total_pages is right.
	matched, err := CountShepherdTasks(ShepherdTaskFilter{Query: "workspace"})
	if err != nil {
		t.Fatalf("CountShepherdTasks(query): %v", err)
	}
	if matched != 2 {
		t.Errorf("count(query=workspace) = %d, want 2 (prompt + summary rows)", matched)
	}

	// Filters compose: kind + query.
	combo, err := ListShepherdTasksFiltered(ShepherdTaskFilter{Kind: "review", Query: "workspace"})
	if err != nil {
		t.Fatalf("ListShepherdTasksFiltered(kind+query): %v", err)
	}
	if len(combo) != 1 || combo[0].ID != second.ID {
		t.Errorf("kind+query = %+v, want only #%d", combo, second.ID)
	}

	// The thin wrapper keeps working unchanged.
	wrapped, err := ListShepherdTasks("patrol", "", 0)
	if err != nil {
		t.Fatalf("ListShepherdTasks wrapper: %v", err)
	}
	if len(wrapped) != 1 {
		t.Errorf("wrapper = %+v, want the single patrol row", wrapped)
	}
}

// CreateManagerTask keeps its old shape (priority 0, no kind).
func TestCreateManagerTaskUnchangedShape(t *testing.T) {
	client, _ := shepherdDispatchTestEnv(t)
	ctx := context.Background()
	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)

	taskRow, err := CreateManagerTask("plain manager task", mgr.ID)
	if err != nil {
		t.Fatalf("CreateManagerTask: %v", err)
	}
	if taskRow.Priority != 0 || taskRow.ShepherdKind != "" {
		t.Errorf("priority=%d kind=%q, want 0/empty", taskRow.Priority, taskRow.ShepherdKind)
	}

	withKind, err := CreateManagerTaskWithKind("shepherd command", mgr.ID, 1, "command")
	if err != nil {
		t.Fatalf("CreateManagerTaskWithKind: %v", err)
	}
	if withKind.Priority != 1 || withKind.ShepherdKind != "command" {
		t.Errorf("priority=%d kind=%q, want 1/command", withKind.Priority, withKind.ShepherdKind)
	}
}

// TestDispatchShepherdAfterCircuitReset (#599) pins the round trip the panel's
// "서킷 초기화" button relies on: a tripped manager refuses to dispatch, and
// after worker.Reset(untrip) the very next dispatch pass claims the pending
// shepherd task again. The run itself fails fast (no embedded executor in
// tests), which is exactly the proof that dispatch happened.
func TestDispatchShepherdAfterCircuitReset(t *testing.T) {
	client, mgrName := shepherdDispatchTestEnv(t)
	ctx := context.Background()

	mgr := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx)
	client.Sheep.UpdateOneID(mgr.ID).
		SetConsecutiveFailures(CircuitBreakerThreshold).ExecX(ctx)
	created := createShepherdTask(t, client, mgr.ID, "command", 1)

	p := NewProcessor(time.Second)

	// Tripped: nothing dispatches; the task stays pending.
	p.checkAndExecutePendingTasks()
	if got := client.Task.GetX(ctx, created.ID); got.Status != task.StatusPending {
		t.Fatalf("tripped breaker must block dispatch, task status = %s", got.Status)
	}

	// Reset (same call POST /api/sheep/:name/reset makes) untrips the circuit.
	if _, err := worker.Reset(mgrName, worker.ResetOptions{ResetCircuit: true}); err != nil {
		t.Fatalf("worker.Reset: %v", err)
	}
	if got := client.Sheep.Query().Where(sheep.Name(names.ManagerName)).OnlyX(ctx); got.ConsecutiveFailures != 0 {
		t.Fatalf("reset must zero consecutive_failures, got %d", got.ConsecutiveFailures)
	}

	// The processor dispatches again; with no embedded executor registered the
	// run fails fast — pending → failed proves the claim happened.
	p.checkAndExecutePendingTasks()
	settled := waitTaskSettled(t, client, names.ManagerName, created.ID)
	if settled.Status == task.StatusPending {
		t.Fatal("after reset the processor must dispatch the pending shepherd task")
	}
}
