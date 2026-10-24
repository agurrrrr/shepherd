package manager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/spf13/viper"
)

// ---- permission table (#601 §2) --------------------------------------------

// autonomyCallerFixture seeds an identifiable shepherd caller: a task owned by
// the manager sheep. IsShepherdCaller reads it from the DB, so every tier row
// needs a real row, not just config.
func autonomyCallerFixture(t *testing.T) (client *ent.Client, shepherdCallerTask int) {
	t.Helper()
	ctx := context.Background()
	client = wakeTestDB(t)

	mgr := client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)
	workerSh := client.Sheep.Create().SetName("양순이").SetStatus(sheep.StatusIdle).SaveX(ctx)

	shepherdTask := client.Task.Create().SetPrompt("shepherd run").
		SetStatus(entTask.StatusRunning).SetSheepID(mgr.ID).SaveX(ctx)
	client.Task.Create().SetPrompt("flock run").
		SetStatus(entTask.StatusRunning).SetSheepID(workerSh.ID).SaveX(ctx)

	return client, shepherdTask.ID
}

// workerCallerTask returns the ordinary-sheep task seeded by the fixture.
func workerCallerTask(t *testing.T) int {
	t.Helper()
	ctx := context.Background()
	wSh, err := db.Client().Sheep.Query().Where(sheep.Name("양순이")).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	taskRow := db.Client().Task.Query().Where(entTask.HasSheepWith(sheep.ID(wSh.ID))).OnlyX(ctx)
	return taskRow.ID
}

// TestCheckShepherdToolPermissionTable pins every cell of the #601 §2 table:
//
//	issue_upsert:  report refuse, plan/execute/full allow
//	issue_execute: report/plan refuse, execute/full allow
//	task_stop:     refuse until full
//
// plus the exemptions: a non-shepherd (ordinary sheep) caller and an
// unidentified caller (task id 0) are never checked, and tools outside the
// table are unrestricted even for shepherds.
func TestCheckShepherdToolPermissionTable(t *testing.T) {
	_, shepherdCaller := autonomyCallerFixture(t)

	cases := []struct {
		tool   string
		tier   string
		refuse bool
	}{
		{"issue_upsert", AutonomyReport, true},
		{"issue_upsert", AutonomyPlan, false},
		{"issue_upsert", AutonomyExecute, false},
		{"issue_upsert", AutonomyFull, false},

		{"issue_execute", AutonomyReport, true},
		{"issue_execute", AutonomyPlan, true},
		{"issue_execute", AutonomyExecute, false},
		{"issue_execute", AutonomyFull, false},

		{"task_stop", AutonomyReport, true},
		{"task_stop", AutonomyPlan, true},
		{"task_stop", AutonomyExecute, true},
		{"task_stop", AutonomyFull, false},
	}

	for _, tc := range cases {
		viper.Reset()
		viper.Set("shepherd_autonomy", tc.tier)
		err := CheckShepherdToolPermission(tc.tool, shepherdCaller)
		if tc.refuse {
			if err == nil {
				t.Errorf("%s at tier %q: expected refusal, got nil", tc.tool, tc.tier)
				continue
			}
			// Every refusal must point the shepherd at request_user_action.
			if !strings.Contains(err.Error(), "request_user_action") {
				t.Errorf("%s at tier %q: refusal lacks the request_user_action hint: %v", tc.tool, tc.tier, err)
			}
		} else if err != nil {
			t.Errorf("%s at tier %q: expected allow, got %v", tc.tool, tc.tier, err)
		}
	}
	viper.Reset()

	t.Run("unknown tier normalizes to execute", func(t *testing.T) {
		viper.Reset()
		viper.Set("shepherd_autonomy", "yolo")
		if got := AutonomyTier(); got != AutonomyExecute {
			t.Errorf("AutonomyTier = %q, want execute", got)
		}
		// execute behaviour: issue_execute allowed, task_stop refused.
		if err := CheckShepherdToolPermission("issue_execute", shepherdCaller); err != nil {
			t.Errorf("issue_execute under unknown tier: %v", err)
		}
		if err := CheckShepherdToolPermission("task_stop", shepherdCaller); err == nil {
			t.Error("task_stop under unknown tier must be refused")
		}
		viper.Reset()
	})

	t.Run("unidentified caller (id 0) is exempt", func(t *testing.T) {
		viper.Reset()
		viper.Set("shepherd_autonomy", AutonomyReport) // most restrictive tier
		for _, tool := range []string{"issue_upsert", "issue_execute", "task_stop"} {
			if err := CheckShepherdToolPermission(tool, 0); err != nil {
				t.Errorf("caller 0 must not be checked for %s: %v", tool, err)
			}
		}
		viper.Reset()
	})

	t.Run("ordinary sheep caller is exempt", func(t *testing.T) {
		workerCaller := workerCallerTask(t)

		viper.Reset()
		viper.Set("shepherd_autonomy", AutonomyReport)
		for _, tool := range []string{"issue_upsert", "issue_execute", "task_stop"} {
			if err := CheckShepherdToolPermission(tool, workerCaller); err != nil {
				t.Errorf("non-shepherd caller must not be checked for %s: %v", tool, err)
			}
		}
		viper.Reset()
	})

	t.Run("unrestricted tools pass at every tier", func(t *testing.T) {
		viper.Reset()
		viper.Set("shepherd_autonomy", AutonomyReport)
		for _, tool := range []string{"issue_list", "issue_get", "wiki_read_page", "get_status"} {
			if err := CheckShepherdToolPermission(tool, shepherdCaller); err != nil {
				t.Errorf("unrestricted tool %s refused: %v", tool, err)
			}
		}
		viper.Reset()
	})

	// Issue deletion has no MCP tool at all — it only runs through an approved
	// request — so the table must never contain it.
	if _, ok := toolMinTier["issue_delete"]; ok {
		t.Error("issue_delete must not be an MCP-permissioned tool")
	}
	if _, ok := toolMinTier["delete_issue"]; ok {
		t.Error("delete_issue must not be an MCP-permissioned tool")
	}
}

// ---- delegation cap --------------------------------------------------------

func TestActiveDelegationsAndCap(t *testing.T) {
	client, shepherdCaller := autonomyCallerFixture(t)
	ctx := context.Background()

	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("shepherd_max_active_delegations", 2)

	// Two watched issues: one pending + one running task = 2 active. Finished
	// tasks and unwatched issues never count.
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	w1 := client.Issue.Create().SetTitle("w1").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	w2 := client.Issue.Create().SetTitle("w2").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	unwatched := client.Issue.Create().SetTitle("plain").SetStatus("todo").SetShepherdWatch(false).SetProject(proj).SaveX(ctx)

	p1 := client.Task.Create().SetPrompt("p1").SetStatus(entTask.StatusPending).
		SetIssueID(w1.ID).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("r1").SetStatus(entTask.StatusRunning).
		SetIssueID(w2.ID).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("done").SetStatus(entTask.StatusCompleted).
		SetIssueID(unwatched.ID).SetProject(proj).SaveX(ctx)

	active, err := ActiveDelegations()
	if err != nil {
		t.Fatal(err)
	}
	if active != 2 {
		t.Fatalf("ActiveDelegations = %d, want 2", active)
	}

	// At the cap (2/2), issue_execute is refused for the shepherd caller.
	if err := CheckShepherdExecuteLimits(shepherdCaller); err == nil {
		t.Fatal("expected delegation-cap refusal at 2/2")
	} else if !strings.Contains(err.Error(), "wait") || !strings.Contains(err.Error(), "shepherd_max_active_delegations") {
		t.Errorf("cap refusal should tell the shepherd to wait for the wake: %v", err)
	}

	// Finishing one task frees a slot (1/2) → allowed again.
	client.Task.UpdateOneID(p1.ID).SetStatus(entTask.StatusCompleted).SaveX(ctx)
	if active, err := ActiveDelegations(); err != nil || active != 1 {
		t.Fatalf("ActiveDelegations after finishing p1 = %d (err %v), want 1", active, err)
	}
	if err := CheckShepherdExecuteLimits(shepherdCaller); err != nil {
		t.Errorf("under the cap → execute must pass: %v", err)
	}

	// Non-shepherd and unidentified callers skip the cap entirely.
	viper.Set("shepherd_max_active_delegations", 1) // back at/over cap: 1 active >= 1
	if err := CheckShepherdExecuteLimits(0); err != nil {
		t.Errorf("caller 0 must skip caps: %v", err)
	}
	if err := CheckShepherdExecuteLimits(workerCallerTask(t)); err != nil {
		t.Errorf("non-shepherd caller must skip caps: %v", err)
	}
}

// ---- daily cost cap + midnight window (#601 §1/§3) -------------------------

// TestDailyCostTodayMidnightBoundary uses a FIXED reference day so the ent
// CreatedAtGTE(local-midnight) window is deterministic even when the test
// process itself runs just after midnight. SQLite stores created_at as TEXT;
// this is the comparison wakeupsToday already relies on.
func TestDailyCostTodayMidnightBoundary(t *testing.T) {
	client, _ := autonomyCallerFixture(t)
	ctx := context.Background()

	ref := time.Date(2026, 10, 24, 12, 0, 0, 0, time.Local) // local noon
	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("w").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)

	// In-window (2026-10-24): a shepherd task ($1.50) + a watched-issue task ($2.00).
	client.Task.Create().SetPrompt("shep").SetStatus(entTask.StatusCompleted).
		SetShepherdKind(KindReview).SetCostUsd(1.50).SetCreatedAt(ref.Add(-2*time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("watched").SetStatus(entTask.StatusCompleted).
		SetIssueID(watched.ID).SetProject(proj).SetCostUsd(2.00).SetCreatedAt(ref.Add(-1*time.Hour)).SaveX(ctx)

	// Out-of-window (2026-10-23): must NOT count even though watched + shepherd.
	client.Task.Create().SetPrompt("old shep").SetStatus(entTask.StatusCompleted).
		SetShepherdKind(KindReview).SetCostUsd(9.00).SetCreatedAt(ref.Add(-26*time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("old watched").SetStatus(entTask.StatusCompleted).
		SetIssueID(watched.ID).SetProject(proj).SetCostUsd(7.00).SetCreatedAt(ref.Add(-26*time.Hour)).SaveX(ctx)

	// Excluded kind: an ordinary flock task on the ref day, issue-unlinked.
	client.Task.Create().SetPrompt("flock").SetStatus(entTask.StatusCompleted).
		SetProject(proj).SetCostUsd(5.00).SetCreatedAt(ref.Add(-3*time.Hour)).SaveX(ctx)

	spent, err := DailyCostToday(ref)
	if err != nil {
		t.Fatal(err)
	}
	if diff := spent - 3.50; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("DailyCostToday(ref) = %v, want 3.50 (yesterday + unwatched excluded)", spent)
	}

	// The next day's window is empty: crossing local midnight resets the cap.
	next, err := DailyCostToday(ref.Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if next > 1e-9 {
		t.Errorf("window one day later must be empty (cap auto-released after midnight), got %v", next)
	}

	// The window is "since local midnight" with no upper bound (same shape as
	// wakeupsToday): querying one day earlier widens it to include the older
	// rows AND today's — 9+7+1.5+2 = 19.5. The unwatched flock task ($5) never
	// counts at any window.
	prev, err := DailyCostToday(ref.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if diff := prev - 19.5; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("previous-midnight window = %v, want 19.50 (9+7+1.5+2; unwatched 5 excluded)", prev)
	}
}

// TestCostLimitStatusAndGate pins the gate itself. Rows use the DEFAULT
// created_at (= now), so the window always contains them regardless of when
// this test runs; CheckShepherdExecuteLimits reads time.Now() internally.
func TestCostLimitStatusAndGate(t *testing.T) {
	client, shepherdCaller := autonomyCallerFixture(t)
	ctx := context.Background()

	viper.Reset()
	t.Cleanup(viper.Reset)

	// limit 0 → no gate, no query.
	viper.Set("shepherd_daily_cost_limit_usd", 0)
	exceeded, _, limit, err := CostLimitStatus(time.Now())
	if err != nil || exceeded || limit != 0 {
		t.Fatalf("limit 0 must disable the gate: exceeded=%v limit=%v err=%v", exceeded, limit, err)
	}

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("w").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("watched").SetStatus(entTask.StatusCompleted).
		SetIssueID(watched.ID).SetProject(proj).SetCostUsd(4.00).SaveX(ctx)

	// Under the cap: allowed, not exceeded.
	viper.Set("shepherd_daily_cost_limit_usd", 10.0)
	if exceeded, spent, limit, err := CostLimitStatus(time.Now()); err != nil || exceeded || spent < 3.99 || limit != 10 {
		t.Fatalf("under cap: exceeded=%v spent=%v limit=%v err=%v", exceeded, spent, limit, err)
	}
	if err := CheckShepherdExecuteLimits(shepherdCaller); err != nil {
		t.Errorf("under cost cap → execute must pass: %v", err)
	}

	// At the cap: refused, and the refusal mentions request_user_action.
	viper.Set("shepherd_daily_cost_limit_usd", 4.0)
	if exceeded, _, _, err := CostLimitStatus(time.Now()); err != nil || !exceeded {
		t.Fatalf("at cap: exceeded=%v err=%v", exceeded, err)
	}
	if err := CheckShepherdExecuteLimits(shepherdCaller); err == nil {
		t.Fatal("expected cost-cap refusal")
	} else if !strings.Contains(err.Error(), "request_user_action") {
		t.Errorf("cost refusal lacks the hint: %v", err)
	}

	// Negative stored values fall back to 0 (no limit), never to a negative.
	viper.Set("shepherd_daily_cost_limit_usd", -5.0)
	if got := DailyCostLimitUSD(); got != 0 {
		t.Errorf("DailyCostLimitUSD with -5 stored = %v, want 0", got)
	}

	// Non-positive delegations fall back to the documented default (3).
	viper.Set("shepherd_max_active_delegations", 0)
	if got := MaxActiveDelegations(); got != 3 {
		t.Errorf("MaxActiveDelegations with 0 stored = %d, want 3", got)
	}
}

// ---- attention: cost_limit item (#601 §3) ----------------------------------

func TestAttentionCostLimitItem(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	ctx := context.Background()

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("w").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("watched").SetStatus(entTask.StatusCompleted).
		SetIssueID(watched.ID).SetProject(proj).SetCostUsd(6.00).SaveX(ctx)

	viper.Set("shepherd_daily_cost_limit_usd", 5.0)

	now := time.Now()
	items, err := attentionCostLimit(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("over the cap: want one cost_limit item, got %d", len(items))
	}
	it := items[0]
	if it.Kind != AttentionCostLimit || it.Severity != 6 {
		t.Errorf("item = %+v, want kind cost_limit severity 6", it)
	}
	// The fingerprint embeds the LOCAL date: yesterday's ack never suppresses
	// today's item, and the item disappears by itself after midnight.
	wantFP := "cost_limit:" + now.Format("2006-01-02")
	if it.Fingerprint != wantFP {
		t.Errorf("fingerprint = %q, want %q", it.Fingerprint, wantFP)
	}

	// Under the cap → nothing.
	viper.Set("shepherd_daily_cost_limit_usd", 100.0)
	items, err = attentionCostLimit(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("under the cap: want no items, got %+v", items)
	}

	// CollectAttention surfaces the kind end-to-end.
	viper.Set("shepherd_daily_cost_limit_usd", 5.0)
	all, err := CollectAttention(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(itemsByKind(all)[AttentionCostLimit]) != 1 {
		t.Errorf("CollectAttention must include the cost_limit item: %+v", all)
	}
}

// ---- patrol: SkipCostCap (#601 §3) -----------------------------------------

func TestPatrolSkipsWakeOnCostCap(t *testing.T) {
	client := wakeTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	watched := client.Issue.Create().SetTitle("w").SetStatus("todo").SetShepherdWatch(true).SetProject(proj).SaveX(ctx)
	client.Task.Create().SetPrompt("watched").SetStatus(entTask.StatusCompleted).
		SetIssueID(watched.ID).SetProject(proj).SetCostUsd(9.00).SaveX(ctx)

	viper.Set("shepherd_daily_cost_limit_usd", 5.0)

	now := time.Now()
	rec, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:1"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.NewCount != 1 {
		t.Fatalf("the finding is still new: %+v", rec)
	}
	if rec.SkippedReason != SkipCostCap {
		t.Fatalf("skipped_reason = %q, want %q", rec.SkippedReason, SkipCostCap)
	}
	if rec.TaskID != 0 {
		t.Errorf("no patrol task may be enqueued over the cap: %+v", rec)
	}
	if n := len(shepherdTasksByKind(t, KindPatrol)); n != 0 {
		t.Errorf("patrol tasks over the cap = %d, want 0", n)
	}

	// Under the cap again → the same pass wakes normally.
	viper.Set("shepherd_daily_cost_limit_usd", 0)
	rec2, err := RunPatrolPassWithCollect(now.Add(time.Minute), fakeItems("failed_task:1", "failed_task:2"))
	if err != nil {
		t.Fatal(err)
	}
	if rec2.SkippedReason != "" || rec2.TaskID == 0 {
		t.Fatalf("under the cap the new finding must wake: %+v", rec2)
	}
}

// ---- wake rule 7 (#601 §3) --------------------------------------------------

func TestWakeRule7CostCapBlocksWake(t *testing.T) {
	f := newWakeFixture(t, true, entTask.StatusCompleted, "")
	wakeConfig(t, 3, 30)

	viper.Set("shepherd_daily_cost_limit_usd", 5.0)
	ctx := context.Background()
	// Today's spend (default created_at = now) crosses the cap.
	f.client.Task.Create().SetPrompt("watched spend").SetStatus(entTask.StatusCompleted).
		SetIssueID(f.issue.ID).SetProject(f.project).SetCostUsd(6.00).SaveX(ctx)

	ResetCostLimitNotifyDay()
	var sends int
	origSend := costLimitSend
	defer func() { costLimitSend = origSend }()
	costLimitSend = func(spent, limit float64) error { sends++; return nil }

	handleIssueTaskFinished(finishedEvent(f))

	if got := len(reviewTasks(t)); got != 0 {
		t.Fatalf("rule 7: no review may be enqueued over the cap, got %d", got)
	}
	if iss, err := f.client.Issue.Get(ctx, f.issue.ID); err != nil {
		t.Fatal(err)
	} else if iss.ShepherdRounds != 0 {
		t.Errorf("shepherd_rounds must not increment over the cap: %d", iss.ShepherdRounds)
	}
	if sends != 1 {
		t.Errorf("the one-time cost notice must fire once: sends=%d", sends)
	}

	// Under the cap → the same event wakes normally.
	viper.Set("shepherd_daily_cost_limit_usd", 0)
	handleIssueTaskFinished(finishedEvent(f))
	if got := len(reviewTasks(t)); got != 1 {
		t.Fatalf("under the cap the wake must happen, got %d review tasks", got)
	}
}

// finishedEvent builds the wake event for a fixture's completed task.
func finishedEvent(f *wakeFixture) queue.IssueTaskEvent {
	return queue.IssueTaskEvent{
		TaskID:    f.finished.ID,
		IssueID:   f.issue.ID,
		Project:   "bori",
		TaskState: "completed",
		SheepName: "양순이",
	}
}

// ---- one-per-day cost notification -----------------------------------------

func TestMaybeNotifyCostLimitOncePerDay(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	ResetCostLimitNotifyDay()

	var sends int
	var hookSpent, hookLimit float64
	origSend := costLimitSend
	origHook := costLimitHook
	defer func() { costLimitSend = origSend; costLimitHook = origHook }()
	costLimitSend = func(spent, limit float64) error { sends++; return nil }

	now := time.Now()
	if !MaybeNotifyCostLimit(now, 5.0, 4.0) {
		t.Fatal("first hit today must notify")
	}
	if sends != 1 {
		t.Fatalf("sends = %d, want 1", sends)
	}
	if MaybeNotifyCostLimit(now.Add(time.Minute), 6.0, 4.0) {
		t.Error("second hit same day must not notify")
	}
	if sends != 1 {
		t.Errorf("sends after second same-day call = %d, want 1", sends)
	}

	// Next local day → the once-guard resets by date key.
	if !MaybeNotifyCostLimit(now.Add(48*time.Hour), 3.0, 2.0) {
		t.Error("next day must notify again")
	}
	if sends != 2 {
		t.Errorf("sends after next-day call = %d, want 2", sends)
	}

	// The SSE hook fires on a newly-tripped cap.
	costLimitHook = func(spent, limit float64) { hookSpent, hookLimit = spent, limit }
	ResetCostLimitNotifyDay()
	MaybeNotifyCostLimit(now, 9.0, 8.0)
	if hookSpent != 9.0 || hookLimit != 8.0 {
		t.Errorf("cost hook got (%v,%v), want (9,8)", hookSpent, hookLimit)
	}
}
