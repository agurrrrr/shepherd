package manager

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/spf13/viper"
)

// TestMain pins the language for the whole manager package: attention items
// and request results render through i18n (#665), so assertions must not
// depend on test execution order. Config writes are also redirected to a
// throwaway file/dir, so no test here can touch the operator's real
// ~/.shepherd/config.yaml.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "shepherd-manager-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testmain: mkdir temp config:", err)
		os.Exit(1)
	}
	config.UseTempConfigFile(filepath.Join(dir, "config.yaml"))
	config.UseTempConfigDir(dir)

	i18n.Init("ko")

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

// attentionTestDB injects an in-memory DB for BOTH the ent client and the raw
// handle: the pending_backlog query runs raw SQL, so both must point at the
// same database (same rule as wakeTestDB).
func attentionTestDB(t *testing.T) *ent.Client {
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
	return client
}

// attentionConfig pins the threshold settings for one test. Defaults mirror a
// running daemon (workspace set to a real dir so shepherd_blocked stays quiet
// unless the test wants it).
func attentionConfig(t *testing.T, stuck, stale, maxRounds int) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("shepherd_stuck_minutes", stuck)
	viper.Set("shepherd_pending_stale_minutes", stale)
	viper.Set("shepherd_max_review_rounds", maxRounds)
	viper.Set("workspace_path", t.TempDir())
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
}

// itemsByKind groups collected items by kind for assertions.
func itemsByKind(items []AttentionItem) map[string][]AttentionItem {
	m := map[string][]AttentionItem{}
	for _, it := range items {
		m[it.Kind] = append(m[it.Kind], it)
	}
	return m
}

// TestAttentionFailedTasks pins the 24h window edges and the scope rule.
func TestAttentionFailedTasks(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	inScope := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	hidden := client.Project.Create().SetName("secret").SetPath("/ws/secret").SetHidden(true).SaveX(ctx)

	// 1h ago → inside the window.
	client.Task.Create().SetPrompt("a").SetStatus(entTask.StatusFailed).
		SetError("boom").SetProject(inScope).
		SetCompletedAt(now.Add(-1 * time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)
	// 23h ago → inside.
	client.Task.Create().SetPrompt("b").SetStatus(entTask.StatusFailed).
		SetError("older but still visible").SetProject(inScope).
		SetCompletedAt(now.Add(-23 * time.Hour)).SetCreatedAt(now.Add(-24 * time.Hour)).SaveX(ctx)
	// 25h ago → outside the 24h window.
	client.Task.Create().SetPrompt("c").SetStatus(entTask.StatusFailed).
		SetError("too old").SetProject(inScope).
		SetCompletedAt(now.Add(-25 * time.Hour)).SetCreatedAt(now.Add(-26 * time.Hour)).SaveX(ctx)
	// Hidden project → out of scope.
	client.Task.Create().SetPrompt("d").SetStatus(entTask.StatusFailed).
		SetError("hidden project failure").SetProject(hidden).
		SetCompletedAt(now.Add(-1 * time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)
	// Failed shepherd review task → suppressed (console covers it).
	client.Task.Create().SetPrompt("e").SetStatus(entTask.StatusFailed).
		SetError("patrol failure").SetShepherdKind(KindReview).
		SetCompletedAt(now.Add(-1 * time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	failed := itemsByKind(items)[AttentionFailedTask]
	if len(failed) != 2 {
		t.Fatalf("want 2 failed_task items (window + scope rules), got %d: %+v", len(failed), failed)
	}
	for _, it := range failed {
		if it.Fingerprint == "" || it.Kind != AttentionFailedTask {
			t.Fatalf("bad item: %+v", it)
		}
		if it.Project == "secret" {
			t.Fatalf("hidden project leaked: %+v", it)
		}
	}

	// Acking one hides only that task's fingerprint.
	if err := AckAttention(failed[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	failed2 := itemsByKind(items)[AttentionFailedTask]
	if len(failed2) != 1 || failed2[0].Fingerprint == failed[0].Fingerprint {
		t.Fatalf("ack must hide exactly the acked failure: %+v", failed2)
	}
}

// TestAttentionLongRunning pins the stuck-minutes boundary and the bucket in
// the fingerprint (acking at one bucket reappears at the next).
func TestAttentionLongRunning(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)

	// 119 minutes → below the threshold.
	client.Task.Create().SetPrompt("fast").SetStatus(entTask.StatusRunning).
		SetProject(p).SetStartedAt(now.Add(-119 * time.Minute)).SaveX(ctx)
	// 121 minutes → over the threshold, bucket 1.
	stuck := client.Task.Create().SetPrompt("slow").SetStatus(entTask.StatusRunning).
		SetProject(p).SetStartedAt(now.Add(-121 * time.Minute)).SaveX(ctx)
	// started_at missing → falls back to created_at (3 hours old).
	client.Task.Create().SetPrompt("nostart").SetStatus(entTask.StatusRunning).
		SetProject(p).SetCreatedAt(now.Add(-3 * time.Hour)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	long := itemsByKind(items)[AttentionLongRunning]
	if len(long) != 2 {
		t.Fatalf("want 2 long_running items, got %d: %+v", len(long), long)
	}

	var stuckFP string
	for _, it := range long {
		if it.TargetID == stuck.ID {
			stuckFP = it.Fingerprint
		}
	}
	if stuckFP != "long_running:"+strconv.Itoa(stuck.ID)+":1" {
		t.Fatalf("fingerprint should embed the bucket, got %q", stuckFP)
	}

	if err := AckAttention(stuckFP); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	long2 := itemsByKind(items)[AttentionLongRunning]
	if len(long2) != 1 {
		t.Fatalf("acked bucket must disappear, others stay: %+v", long2)
	}

	// Two hours later the same task crosses bucket 2 → new item again.
	later := now.Add(2 * time.Hour)
	items, err = CollectAttention(later)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range itemsByKind(items)[AttentionLongRunning] {
		if it.TargetID == stuck.ID && it.Fingerprint != stuckFP {
			found = true
		}
	}
	if !found {
		t.Fatalf("still-stuck task must resurface with a new bucket: %+v", items)
	}
}

// TestAttentionCircuitOpen pins the threshold (= CircuitBreakerThreshold) and
// that the manager sheep is reported too.
func TestAttentionCircuitOpen(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	client.Sheep.Create().SetName("양순이").SetStatus(sheep.StatusIdle).
		SetConsecutiveFailures(queue.CircuitBreakerThreshold - 1).SaveX(ctx)
	client.Sheep.Create().SetName("깜순이").SetStatus(sheep.StatusError).
		SetConsecutiveFailures(queue.CircuitBreakerThreshold).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	circuits := itemsByKind(items)[AttentionCircuitOpen]
	if len(circuits) != 1 || circuits[0].TargetName != "깜순이" {
		t.Fatalf("only the tripped sheep must show: %+v", circuits)
	}
	if circuits[0].ActionKind != "reset_sheep" {
		t.Fatalf("action must be reset_sheep, got %q", circuits[0].ActionKind)
	}

	// Another failure changes the fingerprint (new trip count → new item).
	if err := AckAttention(circuits[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionCircuitOpen]) != 0 {
		t.Fatalf("acked trip must stay hidden")
	}

	db.Client().Sheep.Update().Where(sheep.Name("깜순이")).
		AddConsecutiveFailures(1).ExecX(ctx)
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionCircuitOpen]) != 1 {
		t.Fatalf("a further failure must resurface the breaker: %+v", items)
	}
}

// TestAttentionPendingBacklog pins the stale-minute boundary, the per-project
// aggregation (one item per project), and the scope exclusion.
func TestAttentionPendingBacklog(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	backed := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	fresh := client.Project.Create().SetName("fresh").SetPath("/ws/fresh").SaveX(ctx)
	hidden := client.Project.Create().SetName("secret").SetPath("/ws/secret").SetHidden(true).SaveX(ctx)

	// bori: two pending, oldest 61 minutes → backlog.
	client.Task.Create().SetPrompt("old").SetStatus(entTask.StatusPending).
		SetProject(backed).SetCreatedAt(now.Add(-61 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("newer").SetStatus(entTask.StatusPending).
		SetProject(backed).SetCreatedAt(now.Add(-5 * time.Minute)).SaveX(ctx)
	// fresh: oldest 59 minutes → below the threshold.
	client.Task.Create().SetPrompt("ok").SetStatus(entTask.StatusPending).
		SetProject(fresh).SetCreatedAt(now.Add(-59 * time.Minute)).SaveX(ctx)
	// hidden project: old pending but out of scope.
	client.Task.Create().SetPrompt("hidden-old").SetStatus(entTask.StatusPending).
		SetProject(hidden).SetCreatedAt(now.Add(-24 * time.Hour)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	backlogs := itemsByKind(items)[AttentionPendingBacklog]
	if len(backlogs) != 1 || backlogs[0].Project != "bori" {
		t.Fatalf("want exactly the stale in-scope project, got %+v", backlogs)
	}
	if cnt, _ := backlogs[0].Detail["count"].(int); cnt != 2 {
		t.Fatalf("pending count = %v, want 2", backlogs[0].Detail["count"])
	}

	// The fingerprint keys on the oldest task: once it dispatches, the item
	// changes to the new oldest task instead of vanishing silently. (The
	// "fresh" project crosses the stale line at now+70m too — filter it out.)
	started := db.Client().Task.Query().Where(entTask.Prompt("old")).OnlyX(ctx)
	db.Client().Task.UpdateOneID(started.ID).
		SetStatus(entTask.StatusRunning).SetStartedAt(now).ExecX(ctx)

	items, err = CollectAttention(now.Add(70 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var bori []AttentionItem
	for _, it := range itemsByKind(items)[AttentionPendingBacklog] {
		if it.Project == "bori" {
			bori = append(bori, it)
		}
	}
	if len(bori) != 1 || bori[0].Fingerprint == backlogs[0].Fingerprint {
		t.Fatalf("oldest-task change must rotate the fingerprint: %+v", bori)
	}
}

// TestAttentionPendingBacklogDrainEvidence pins the #631 rule: the item keeps
// firing, but its severity and message carry the judgement material. Scenario
// 1 — the assigned sheep is running a task (queue draining in normal FIFO):
// severity drops to 2 and the message names the running task and its start
// time. Scenario 2 — the assigned sheep is idle: severity stays 4 and the
// message calls out the real-stall possibility. The fingerprint is identical
// in both shapes (project + oldest task only), so a drain-state flip never
// rotates it.
func TestAttentionPendingBacklogDrainEvidence(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	bori := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	hamchi := client.Sheep.Create().SetName("햄찌").SetStatus(sheep.StatusIdle).SaveX(ctx)

	// Oldest pending: 61 minutes, assigned to 햄찌.
	old := client.Task.Create().SetPrompt("old").SetStatus(entTask.StatusPending).
		SetProject(bori).SetSheep(hamchi).SetCreatedAt(now.Add(-61 * time.Minute)).SaveX(ctx)

	// Scenario 2 (idle assigned sheep): real stall → severity 4, idle hint.
	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	idleItems := itemsByKind(items)[AttentionPendingBacklog]
	if len(idleItems) != 1 {
		t.Fatalf("idle sheep with stale pending must fire: %+v", idleItems)
	}
	if idleItems[0].Severity != 4 {
		t.Fatalf("idle-sheep stall severity = %d, want 4", idleItems[0].Severity)
	}
	if !strings.Contains(idleItems[0].Message, "idle") {
		t.Fatalf("idle-sheep message must state the stall possibility: %q", idleItems[0].Message)
	}

	// Scenario 1 (assigned sheep running): serial-wait evidence, severity 2.
	running := client.Task.Create().SetPrompt("busy").SetStatus(entTask.StatusRunning).
		SetProject(bori).SetSheep(hamchi).
		SetCreatedAt(now.Add(-40 * time.Minute)).SetStartedAt(now.Add(-30 * time.Minute)).SaveX(ctx)

	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	drainItems := itemsByKind(items)[AttentionPendingBacklog]
	if len(drainItems) != 1 {
		t.Fatalf("draining queue must still report (evidence, not silence): %+v", drainItems)
	}
	it := drainItems[0]
	if it.Severity != 2 {
		t.Fatalf("drain-state severity = %d, want 2", it.Severity)
	}
	if !strings.Contains(it.Message, "#"+strconv.Itoa(running.ID)) || !strings.Contains(it.Message, "햄찌") {
		t.Fatalf("message must name the running task and sheep: %q", it.Message)
	}
	if behind, _ := it.Detail["draining_behind_task"].(int); behind != running.ID {
		t.Fatalf("detail draining_behind_task = %v, want %d", it.Detail["draining_behind_task"], running.ID)
	}
	if _, ok := it.Detail["draining_started_at"].(string); !ok {
		t.Fatalf("detail must carry the running task's start time: %+v", it.Detail)
	}

	// The fingerprint survives the drain-state flip unchanged.
	if it.Fingerprint != idleItems[0].Fingerprint {
		t.Fatalf("fingerprint must be stable across drain flip: %q vs %q", it.Fingerprint, idleItems[0].Fingerprint)
	}
	if want := fmt.Sprintf("pending_backlog:%d:%d", bori.ID, old.ID); it.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", it.Fingerprint, want)
	}
}

// TestAttentionPendingBacklogDeadzone pins the mixed shape from the 4th
// false positive (#11047): a normal oldest row plus manager-assigned
// pending rows with an empty shepherd_kind in the same project. The item
// must report the deadzone count so the real stall cannot hide behind the
// normal FIFO row, and must NOT drop to severity 2 even when the oldest's
// assigned sheep is running.
func TestAttentionPendingBacklogDeadzone(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	bori := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	hamchi := client.Sheep.Create().SetName("햄찌").SetStatus(sheep.StatusIdle).SaveX(ctx)
	mgrSheep := client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)

	// Oldest pending: normal FIFO row assigned to 햄찌 (running, so the
	// oldest itself looks like a serial wait).
	client.Task.Create().SetPrompt("old").SetStatus(entTask.StatusPending).
		SetProject(bori).SetSheep(hamchi).SetCreatedAt(now.Add(-61 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("busy").SetStatus(entTask.StatusRunning).
		SetProject(bori).SetSheep(hamchi).
		SetCreatedAt(now.Add(-40 * time.Minute)).SetStartedAt(now.Add(-30 * time.Minute)).SaveX(ctx)

	// The hidden real stall: manager-assigned, empty kind (dispatch deadzone
	// on a pre-#629 daemon). Created later, so it is not the oldest row.
	client.Task.Create().SetPrompt("stuck-mgr").SetStatus(entTask.StatusPending).
		SetProject(bori).SetSheep(mgrSheep).SetShepherdKind("").
		SetCreatedAt(now.Add(-65 * time.Minute)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	backlogs := itemsByKind(items)[AttentionPendingBacklog]
	if len(backlogs) != 1 {
		t.Fatalf("want one backlog item, got %+v", backlogs)
	}
	it := backlogs[0]
	if it.Severity != 4 {
		t.Fatalf("deadzone present → severity must stay 4, got %d", it.Severity)
	}
	if n, _ := it.Detail["manager_pending_no_kind"].(int); n != 1 {
		t.Fatalf("detail manager_pending_no_kind = %v, want 1", it.Detail["manager_pending_no_kind"])
	}
	if !strings.Contains(it.Message, "shepherd_kind") {
		t.Fatalf("message must surface the deadzone count: %q", it.Message)
	}
}

// TestAttentionReviewLimit pins the round-cap boundary (= max rounds trips,
// below does not) and the round-rotation of the fingerprint.
func TestAttentionReviewLimit(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)

	client.Issue.Create().SetTitle("below cap").SetProject(p).
		SetShepherdWatch(true).SetShepherdRounds(2).SaveX(ctx)
	capped := client.Issue.Create().SetTitle("at cap").SetProject(p).
		SetShepherdWatch(true).SetShepherdRounds(3).SaveX(ctx)
	client.Issue.Create().SetTitle("unwatched at cap").SetProject(p).
		SetShepherdWatch(false).SetShepherdRounds(9).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	limits := itemsByKind(items)[AttentionReviewLimit]
	if len(limits) != 1 || limits[0].TargetID != capped.ID {
		t.Fatalf("only the watched issue at the cap must show: %+v", limits)
	}

	// Round reset clears the item; climbing back to the cap reappears (round
	// is part of the fingerprint).
	if err := AckAttention(limits[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionReviewLimit]) != 0 {
		t.Fatal("acked review_limit item must hide")
	}

	db.Client().Issue.UpdateOneID(capped.ID).SetShepherdRounds(4).ExecX(ctx)
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionReviewLimit]) != 1 {
		t.Fatalf("a new round must resurface the cap item: %+v", items)
	}
}

// TestAttentionUserStopped pins wake rule 3's counterpart: user-stopped tasks
// on watched issues show up; question stops and unwatched issues do not.
func TestAttentionUserStopped(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("watched").SetProject(p).
		SetShepherdWatch(true).SaveX(ctx)
	unwatched := client.Issue.Create().SetTitle("plain").SetProject(p).SaveX(ctx)

	client.Task.Create().SetPrompt("stopped").SetStatus(entTask.StatusStopped).
		SetError("stopped by user").SetProject(p).SetIssueID(watched.ID).
		SetCompletedAt(now.Add(-10 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("question-stop").SetStatus(entTask.StatusStopped).
		SetError("question: which branch?").SetProject(p).SetIssueID(watched.ID).
		SetCompletedAt(now.Add(-10 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("unwatched-stop").SetStatus(entTask.StatusStopped).
		SetError("cancelled by user").SetProject(p).SetIssueID(unwatched.ID).
		SetCompletedAt(now.Add(-10 * time.Minute)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	stopped := itemsByKind(items)[AttentionUserStopped]
	if len(stopped) != 1 || stopped[0].TargetID != watched.ID {
		t.Fatalf("only the user-stopped watched-issue task must show: %+v", stopped)
	}
}

// TestAttentionShepherdBlocked pins the workspace-blocked state: pending
// shepherd tasks + an unset workspace surface one item pointing at settings.
func TestAttentionShepherdBlocked(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)
	client.Task.Create().SetPrompt("user command").SetStatus(entTask.StatusPending).
		SetShepherdKind(KindCommand).SetPriority(1).SaveX(ctx)

	// Workspace valid (attentionConfig pointed it at a temp dir): no block.
	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionShepherdBlocked]) != 0 {
		t.Fatalf("no block expected with a valid workspace: %+v", items)
	}

	// Workspace missing → one item, settings action, state-scoped fingerprint.
	viper.Set("workspace_path", "/nonexistent/shepherd-workspace-xyz")
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	blocked := itemsByKind(items)[AttentionShepherdBlocked]
	if len(blocked) != 1 || blocked[0].ActionKind != "open_settings" {
		t.Fatalf("workspace block must surface one settings item: %+v", items)
	}

	// Acking hides the nag until the reason itself changes.
	if err := AckAttention(blocked[0].Fingerprint); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionShepherdBlocked]) != 0 {
		t.Fatal("acked workspace block must hide")
	}

	// Provider disabled is a different reason → new item even though the old
	// one was acked.
	viper.Set("workspace_path", t.TempDir())
	viper.Set("provider_enabled_claude", false)
	db.Client().Sheep.Update().Where(sheep.Name(names.ManagerName)).
		SetProvider(sheep.ProviderClaude).ExecX(ctx)
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionShepherdBlocked]) != 1 {
		t.Fatalf("provider block must surface as a new item: %+v", items)
	}
}

// TestAttentionScopeExclusion verifies that excluding a project via config
// hides every project-scoped kind for it (failed/long_running/backlog/review).
func TestAttentionScopeExclusion(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	p := client.Project.Create().SetName("quiet").SetPath("/ws/quiet").SaveX(ctx)
	client.Task.Create().SetPrompt("f").SetStatus(entTask.StatusFailed).
		SetError("boom").SetProject(p).
		SetCompletedAt(now.Add(-time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)
	client.Issue.Create().SetTitle("capped").SetProject(p).
		SetShepherdWatch(true).SetShepherdRounds(5).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("baseline: excluded project items expected before exclusion")
	}

	viper.Set("shepherd_scope_exclude", []string{"quiet"})
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Project == "quiet" {
			t.Fatalf("excluded project leaked kind %s: %+v", it.Kind, it)
		}
	}
}

// TestAttentionQueryCountFixed pins the "one query per kind, not per project"
// rule: doubling the number of in-scope projects must not change any kind's
// query count.
func TestAttentionQueryCountFixed(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 4; i++ {
		p := client.Project.Create().SetName("proj" + string(rune('a'+i))).
			SetPath("/ws/proj" + string(rune('a'+i))).SaveX(ctx)
		client.Task.Create().SetPrompt("f").SetStatus(entTask.StatusFailed).
			SetError("boom").SetProject(p).
			SetCompletedAt(now.Add(-time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)
	}

	startQueryCounting()
	if _, err := CollectAttention(now); err != nil {
		t.Fatal(err)
	}
	first := stopQueryCounting()

	for i := 0; i < 3; i++ {
		client.Project.Create().SetName("more" + string(rune('0'+i))).SetPath("/ws/more").SaveX(ctx)
	}

	startQueryCounting()
	if _, err := CollectAttention(now); err != nil {
		t.Fatal(err)
	}
	second := stopQueryCounting()

	for kind, n := range first {
		if n != 1 {
			t.Errorf("kind %s issued %d queries in run 1, want exactly 1", kind, n)
		}
		if second[kind] != n {
			t.Errorf("kind %s issued %d queries after adding projects, want %d", kind, second[kind], n)
		}
	}
	// Every kind must be queried exactly once — a missing entry means a kind
	// never ran (silently skipped).
	for _, kind := range []string{
		AttentionFailedTask, AttentionLongRunning, AttentionCircuitOpen,
		AttentionPendingBacklog, AttentionReviewLimit, AttentionUserStopped,
		AttentionShepherdBlocked,
	} {
		if first[kind] != 1 {
			t.Errorf("kind %s query count = %d, want 1", kind, first[kind])
		}
	}
}

// TestAttentionAckPrune verifies acks older than 30 days stop suppressing.
func TestAttentionAckPrune(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()
	now := time.Now()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	task := client.Task.Create().SetPrompt("f").SetStatus(entTask.StatusFailed).
		SetError("boom").SetProject(p).
		SetCompletedAt(now.Add(-time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	failed := itemsByKind(items)[AttentionFailedTask]
	if len(failed) != 1 {
		t.Fatalf("want one failed item, got %+v", failed)
	}
	fp := "failed_task:" + strconv.Itoa(task.ID)
	if failed[0].Fingerprint != fp {
		t.Fatalf("fingerprint = %q, want %q", failed[0].Fingerprint, fp)
	}

	if err := AckAttention(fp); err != nil {
		t.Fatal(err)
	}
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionFailedTask]) != 0 {
		t.Fatal("acked item must hide")
	}

	// Rewind the ack's timestamp past retention and collect again.
	db.Client().ShepherdAck.Update().SetAckedAt(now.Add(-31 * 24 * time.Hour)).ExecX(ctx)
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(items)[AttentionFailedTask]) != 1 {
		t.Fatalf("expired ack must stop suppressing: %+v", items)
	}

	// The prune actually deleted the row.
	n, err := db.Client().ShepherdAck.Query().Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expired ack rows must be pruned, got %d", n)
	}
}

// TestAckAttentionEmpty guards the API's validation path.
func TestAckAttentionEmpty(t *testing.T) {
	attentionTestDB(t)
	if err := AckAttention("   "); err == nil {
		t.Fatal("empty fingerprint must error")
	}
}

// TestAttentionRendersInEnglish pins #665 §1: with language=en, every title
// and message the panel shows comes from the en dictionary — no Korean leaks
// through, and the minute/hour/day helper switches too. Data-driven text (task
// prompts, project names) is fed in ASCII so only our own labels are checked.
func TestAttentionRendersInEnglish(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	i18n.Init("en")
	defer i18n.Init("ko")

	ctx := context.Background()
	now := time.Now()

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("watched").SetStatus("in_progress").
		SetShepherdWatch(true).SetShepherdRounds(3).SetProject(proj).SaveX(ctx)

	client.Task.Create().SetPrompt("boom prompt").SetStatus(entTask.StatusFailed).
		SetError("provider died").SetProject(proj).
		SetCompletedAt(now.Add(-1 * time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("still going").SetStatus(entTask.StatusRunning).
		SetProject(proj).SetCreatedAt(now.Add(-3 * time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("queued long ago").SetStatus(entTask.StatusPending).
		SetProject(proj).SetCreatedAt(now.Add(-90 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("user killed").SetStatus(entTask.StatusStopped).
		SetError("stopped by user").SetIssue(watched).SetProject(proj).
		SetCompletedAt(now.Add(-1 * time.Hour)).SaveX(ctx)

	shep := client.Sheep.Create().SetName("worker").SetProvider(sheep.ProviderClaude).
		SetStatus(sheep.StatusIdle).SetConsecutiveFailures(4).SaveX(ctx)
	_ = shep

	items, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	kinds := itemsByKind(items)
	for _, kind := range []string{
		AttentionFailedTask, AttentionLongRunning, AttentionPendingBacklog,
		AttentionReviewLimit, AttentionUserStopped,
	} {
		if len(kinds[kind]) == 0 {
			t.Fatalf("kind %s produced no item — the en assertions below prove nothing", kind)
		}
	}

	for _, it := range items {
		for _, s := range []string{it.Title, it.Message} {
			if hasHangul(s) {
				t.Errorf("kind %s leaked Korean in en: %q", it.Kind, s)
			}
		}
	}

	// The duration helper follows the language too (90 minutes → 1h).
	if got := formatMinutes(90); got != "1h" {
		t.Errorf("formatMinutes(90) = %q, want 1h", got)
	}
	if got := formatMinutes(45); got != "45m" {
		t.Errorf("formatMinutes(45) = %q, want 45m", got)
	}
	if got := formatMinutes(48 * 60); got != "2d" {
		t.Errorf("formatMinutes(2 days) = %q, want 2d", got)
	}

	// The same rows render in ko with the pre-#665 wording.
	i18n.Init("ko")
	items, err = CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	kinds = itemsByKind(items)
	failed := kinds[AttentionFailedTask][0]
	if failed.Title != fmt.Sprintf(i18n.T().Attention.FailedTaskTitleFmt, failed.TargetID) {
		t.Errorf("ko failed title = %q", failed.Title)
	}
	// The task error text is data and rides through; the user_stopped message
	// is ours, so it must be Korean again.
	stopped := kinds[AttentionUserStopped][0]
	if stopped.Message != i18n.T().Attention.UserStoppedMessage || !hasHangul(stopped.Message) {
		t.Errorf("ko user_stopped message = %q", stopped.Message)
	}
	if got := formatMinutes(90); got != "1시간" {
		t.Errorf("ko formatMinutes(90) = %q", got)
	}
}

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
		if r >= 0x1100 && r <= 0x11FF {
			return true
		}
		if r >= 0x3130 && r <= 0x318F {
			return true
		}
	}
	return false
}
