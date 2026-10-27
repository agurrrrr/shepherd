package manager

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/spf13/viper"
)

// patrolConfig pins the patrol settings. wake=false / interval="" mean
// "explicitly set"; pass nil-ish defaults by NOT setting the key when you
// want the unset-means-default behavior under test.
func patrolConfig(t *testing.T, interval string, wake *bool, maxWakeups int) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	if interval != "" {
		viper.Set("shepherd_patrol_interval", interval)
	}
	if wake != nil {
		viper.Set("shepherd_patrol_wake", *wake)
	}
	if maxWakeups > 0 {
		viper.Set("shepherd_max_wakeups_per_day", maxWakeups)
	}
	// Keep shepherd_blocked quiet unless a test wants it.
	viper.Set("workspace_path", t.TempDir())
	for _, p := range []string{"claude", "opencode", "pi", "grok", "embedded", "magi"} {
		viper.SetDefault("provider_enabled_"+p, true)
	}
}

// fakeItems builds AttentionItems by fingerprint for the collect seam.
func fakeItems(fingerprints ...string) func(time.Time) ([]AttentionItem, error) {
	return func(time.Time) ([]AttentionItem, error) {
		items := make([]AttentionItem, 0, len(fingerprints))
		for _, fp := range fingerprints {
			items = append(items, AttentionItem{
				Kind:        AttentionFailedTask,
				Severity:    6,
				Fingerprint: fp,
				Title:       "작업 실패 " + fp,
				Message:     "boom",
				Project:     "bori",
			})
		}
		return items, nil
	}
}

// failingCollect is the collect seam error case.
func failingCollect(time.Time) ([]AttentionItem, error) {
	return nil, context.DeadlineExceeded
}

func countPatrolRows(t *testing.T) int {
	t.Helper()
	n, err := db.Client().ShepherdPatrol.Query().Count(context.Background())
	if err != nil {
		t.Fatalf("count patrols: %v", err)
	}
	return n
}

func shepherdTasksByKind(t *testing.T, kind string) []*ent.Task {
	t.Helper()
	tasks, err := db.Client().Task.Query().
		Where(entTask.ShepherdKindEQ(kind)).
		All(context.Background())
	if err != nil {
		t.Fatalf("query shepherd tasks: %v", err)
	}
	return tasks
}

// TestPatrolIntervalPins the setting rules: unset → 30m, "0" → off,
// nonzero below the floor → 5m, typo → default (mirrors GetTaskTimeout).
func TestPatrolInterval(t *testing.T) {
	cases := []struct {
		raw  string
		set  bool
		want time.Duration
	}{
		{raw: "", set: false, want: 30 * time.Minute},
		{raw: "30m", want: 30 * time.Minute},
		{raw: "0", want: 0},
		{raw: "-5m", want: 0},
		{raw: "1m", want: 5 * time.Minute},
		{raw: "2h", want: 2 * time.Hour},
		{raw: "30x", want: 30 * time.Minute},
	}
	for _, tc := range cases {
		viper.Reset()
		if tc.set || tc.raw != "" {
			viper.Set("shepherd_patrol_interval", tc.raw)
		}
		if got := PatrolInterval(); got != tc.want {
			t.Errorf("interval %q → %v, want %v", tc.raw, got, tc.want)
		}
	}
	viper.Reset()
}

// TestPatrolWakesOnlyOnNew is the core rule: the first pass treats every
// finding as new and wakes once; an identical second pass has no new
// fingerprints and must NOT wake; a third pass with one extra fingerprint
// wakes again. Every pass stores a record.
func TestPatrolWakesOnlyOnNew(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	now := time.Now()

	rec1, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:1"))
	if err != nil {
		t.Fatal(err)
	}
	if rec1.NewCount != 1 || rec1.TaskID == 0 || rec1.SkippedReason != "" {
		t.Fatalf("first pass must wake: %+v", rec1)
	}
	patrolTasks := shepherdTasksByKind(t, KindPatrol)
	if len(patrolTasks) != 1 {
		t.Fatalf("want 1 patrol task, got %d", len(patrolTasks))
	}
	if !strings.Contains(patrolTasks[0].Prompt, "New problems since the previous patrol (1)") {
		t.Fatalf("prompt lacks new-problems header:\n%s", patrolTasks[0].Prompt)
	}

	// Same fingerprints + the pending patrol task from pass 1 → two reasons
	// to skip, but "no new findings" comes first: no wake, no skipped_reason.
	rec2, err := RunPatrolPassWithCollect(now.Add(30*time.Minute), fakeItems("failed_task:1"))
	if err != nil {
		t.Fatal(err)
	}
	if rec2.NewCount != 0 || rec2.TaskID != 0 || rec2.SkippedReason != "" {
		t.Fatalf("second identical pass must record without waking: %+v", rec2)
	}

	// A genuinely new fingerprint wakes again only once the previous patrol
	// task is out of the way (pending guard). Finish it first.
	ctx := context.Background()
	finished := shepherdTasksByKind(t, KindPatrol)[0]
	if _, err := db.Client().Task.UpdateOneID(finished.ID).
		SetStatus(entTask.StatusCompleted).Save(ctx); err != nil {
		t.Fatal(err)
	}

	rec3, err := RunPatrolPassWithCollect(now.Add(time.Hour), fakeItems("failed_task:1", "failed_task:2"))
	if err != nil {
		t.Fatal(err)
	}
	if rec3.NewCount != 1 || rec3.TaskID == 0 {
		t.Fatalf("new fingerprint must wake with new_count 1: %+v", rec3)
	}
	if n := countPatrolRows(t); n != 3 {
		t.Fatalf("every pass stores a record, got %d rows", n)
	}

	// The wake prompt carries the per-kind action candidate and the
	// do-yourself / request_user_action instruction (#600 as revised by #601).
	woken := shepherdTasksByKind(t, KindPatrol)[1]
	for _, needle := range []string{"Candidate:", "issue_execute", "request_user_action"} {
		if !strings.Contains(woken.Prompt, needle) {
			t.Fatalf("patrol prompt missing %q:\n%s", needle, woken.Prompt)
		}
	}
	if woken.Priority != 0 {
		t.Fatalf("patrol tasks run at priority 0, got %d", woken.Priority)
	}
}

// TestPatrolSkipsAckedFingerprints pins the 확인함 rule end-to-end: an acked
// attention item is absent from findings, and when it later resurfaces with
// the SAME fingerprint (impossible for well-formed fingerprints, but the
// rule is about the diff) it must not count as new. The realistic case —
// a NEW fingerprint after acking — still wakes.
func TestPatrolSkipsAckedFingerprints(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()
	now := time.Now()

	proj := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	failed := client.Task.Create().SetPrompt("a").SetStatus(entTask.StatusFailed).
		SetError("boom").SetProject(proj).
		SetCompletedAt(now.Add(-time.Hour)).SetCreatedAt(now.Add(-2 * time.Hour)).SaveX(ctx)

	// Pass 1: the failure is new → wake.
	rec1, err := RunPatrolPass(now)
	if err != nil {
		t.Fatal(err)
	}
	if rec1.NewCount != 1 || rec1.TaskID == 0 {
		t.Fatalf("first pass should wake on the failure: %+v", rec1)
	}

	// Ack it and finish the patrol task.
	if err := AckAttention(fmt.Sprintf("failed_task:%d", failed.ID)); err != nil {
		t.Fatal(err)
	}
	finishPatrolTasks(t)

	// Pass 2: CollectAttention drops the acked fingerprint → no findings at
	// all, and — crucially — the record's findings list is empty, so a later
	// un-ack would count as new again only via a NEW fingerprint.
	rec2, err := RunPatrolPass(now.Add(30 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec2.Findings) != 0 || rec2.NewCount != 0 || rec2.TaskID != 0 {
		t.Fatalf("acked item must vanish from findings: %+v", rec2)
	}

	// A second failure (new fingerprint) surfaces and wakes even though the
	// first one stays acked.
	client.Task.Create().SetPrompt("b").SetStatus(entTask.StatusFailed).
		SetError("boom again").SetProject(proj).
		SetCompletedAt(now.Add(-time.Minute)).SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)

	rec3, err := RunPatrolPass(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rec3.NewCount != 1 || rec3.TaskID == 0 {
		t.Fatalf("new failure after ack must wake: %+v", rec3)
	}
	if len(rec3.Findings) != 1 {
		t.Fatalf("findings must hold only the un-acked failure: %+v", rec3.Findings)
	}
}

func finishPatrolTasks(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	for _, tk := range shepherdTasksByKind(t, KindPatrol) {
		if tk.Status == entTask.StatusPending || tk.Status == entTask.StatusRunning {
			if _, err := db.Client().Task.UpdateOneID(tk.ID).
				SetStatus(entTask.StatusCompleted).Save(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestPatrolPendingBacklogDrainFingerprint pins the #631 patrol behaviour: a
// backlog caused by normal FIFO drain (assigned sheep running) is still a
// finding — recorded under the same pending_backlog:<project>:<oldest>
// fingerprint at lowered severity — and the wake happens exactly once. When
// the drain state flips (the running task finishes, the sheep goes idle),
// the fingerprint does NOT rotate: the item re-reports at full severity but
// counts as neither new nor released, so a flapping drain state cannot spam
// patrol wakes. Only a change of the oldest task id rotates it.
func TestPatrolPendingBacklogDrainFingerprint(t *testing.T) {
	client := attentionTestDB(t)
	attentionConfig(t, 120, 60, 3)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()
	now := time.Now()

	bori := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)
	hamchi := client.Sheep.Create().SetName("햄찌").SetStatus(sheep.StatusIdle).SaveX(ctx)

	old := client.Task.Create().SetPrompt("queued").SetStatus(entTask.StatusPending).
		SetProject(bori).SetSheep(hamchi).SetCreatedAt(now.Add(-61 * time.Minute)).SaveX(ctx)
	client.Task.Create().SetPrompt("busy").SetStatus(entTask.StatusRunning).
		SetProject(bori).SetSheep(hamchi).
		SetCreatedAt(now.Add(-40 * time.Minute)).SetStartedAt(now.Add(-30 * time.Minute)).SaveX(ctx)

	fp := fmt.Sprintf("pending_backlog:%d:%d", bori.ID, old.ID)

	// Pass 1: drain-state backlog is a finding at severity 2 and wakes once.
	rec1, err := RunPatrolPass(now)
	if err != nil {
		t.Fatal(err)
	}
	if rec1.NewCount != 1 || rec1.TaskID == 0 {
		t.Fatalf("drain backlog must still be reported and wake once: %+v", rec1)
	}
	var sev int
	for _, f := range rec1.Findings {
		if f.Fingerprint == fp {
			sev = f.Severity
		}
	}
	if sev != 2 {
		t.Fatalf("drain-state finding severity = %d, want 2 (findings: %+v)", sev, rec1.Findings)
	}

	finishPatrolTasks(t)

	// Pass 2 with the same state: identical fingerprint → recorded again but
	// not new, no wake.
	rec2, err := RunPatrolPass(now.Add(30 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rec2.NewCount != 0 || rec2.TaskID != 0 {
		t.Fatalf("unchanged backlog must not re-wake: %+v", rec2)
	}

	// Drain flips to a real stall (running task finishes → assigned sheep
	// idle). The item comes back at severity 4 under the SAME fingerprint,
	// so the patrol records it without a second wake.
	if _, err := db.Client().Task.Update().
		Where(entTask.StatusEQ(entTask.StatusRunning)).
		SetStatus(entTask.StatusCompleted).Save(ctx); err != nil {
		t.Fatal(err)
	}
	rec3, err := RunPatrolPass(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rec3.NewCount != 0 || rec3.TaskID != 0 {
		t.Fatalf("drain→stall flip must keep the fingerprint and not re-wake: %+v", rec3)
	}
	sev = 0
	for _, f := range rec3.Findings {
		if f.Fingerprint == fp {
			sev = f.Severity
		}
	}
	if sev != 4 {
		t.Fatalf("stall-state finding severity = %d, want 4 (findings: %+v)", sev, rec3.Findings)
	}

	// Only the oldest task changing rotates the fingerprint and wakes again.
	if _, err := db.Client().Task.UpdateOneID(old.ID).
		SetStatus(entTask.StatusRunning).SetStartedAt(now.Add(time.Hour)).Save(ctx); err != nil {
		t.Fatal(err)
	}
	next := client.Task.Create().SetPrompt("next").SetStatus(entTask.StatusPending).
		SetProject(bori).SetSheep(hamchi).SetCreatedAt(now.Add(-61 * time.Minute)).SaveX(ctx)
	rec4, err := RunPatrolPass(now.Add(90 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rec4.NewCount != 1 || rec4.TaskID == 0 {
		t.Fatalf("new oldest task must rotate the fingerprint and wake: %+v", rec4)
	}
	want := fmt.Sprintf("pending_backlog:%d:%d", bori.ID, next.ID)
	found := false
	for _, f := range rec4.Findings {
		if f.Fingerprint == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("rotated fingerprint %q missing from findings: %+v", want, rec4.Findings)
	}
}

// TestPatrolSkipsWhenPatrolTaskActive pins the pending/running guard: with a
// patrol task still in flight, new findings are recorded (new_count > 0) but
// no second wake stacks up.
func TestPatrolSkipsWhenPatrolTaskActive(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	now := time.Now()

	if _, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:1")); err != nil {
		t.Fatal(err)
	}

	rec, err := RunPatrolPassWithCollect(now.Add(30*time.Minute), fakeItems("failed_task:2"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.NewCount != 1 {
		t.Fatalf("the new finding must still be counted: %+v", rec)
	}
	if rec.TaskID != 0 || rec.SkippedReason != SkipPatrolBusy {
		t.Fatalf("pending patrol task must block the wake: %+v", rec)
	}
	// #665: the stored column holds the language-neutral code, not a sentence.
	row := db.Client().ShepherdPatrol.GetX(context.Background(), rec.ID)
	if row.SkippedReason != "patrol_busy" {
		t.Fatalf("stored skipped_reason = %q, want the code patrol_busy", row.SkippedReason)
	}
	if n := len(shepherdTasksByKind(t, KindPatrol)); n != 1 {
		t.Fatalf("no second patrol task may stack, got %d", n)
	}
}

// TestPatrolSkipsWhenWakeDisabled pins shepherd_patrol_wake=false: findings
// are recorded with their new count, but the wake switch keeps the 목자 asleep.
func TestPatrolSkipsWhenWakeDisabled(t *testing.T) {
	attentionTestDB(t)
	off := false
	patrolConfig(t, "30m", &off, 0)

	rec, err := RunPatrolPassWithCollect(time.Now(), fakeItems("failed_task:1"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.NewCount != 1 || rec.TaskID != 0 || rec.SkippedReason != SkipWakeDisabled {
		t.Fatalf("wake off must record-only: %+v", rec)
	}
	if n := len(shepherdTasksByKind(t, KindPatrol)); n != 0 {
		t.Fatalf("wake off must enqueue nothing, got %d", n)
	}
}

// TestPatrolDailyCap pins the shared shepherd_max_wakeups_per_day budget:
// yesterday's wakes do not count, today's review AND patrol wakes do.
func TestPatrolDailyCap(t *testing.T) {
	client := attentionTestDB(t)
	patrolConfig(t, "30m", nil, 2)
	ctx := context.Background()
	now := time.Now()

	// Two automatic wakes already used today: one review, one patrol. Both
	// completed, so the pending-patrol guard does not interfere.
	client.Task.Create().SetPrompt("r").SetShepherdKind(KindReview).
		SetStatus(entTask.StatusCompleted).SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)
	client.Task.Create().SetPrompt("p").SetShepherdKind(KindPatrol).
		SetStatus(entTask.StatusCompleted).SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)
	// Yesterday's wake must not count.
	yesterday := now.Add(-36 * time.Hour)
	client.Task.Create().SetPrompt("old").SetShepherdKind(KindReview).
		SetStatus(entTask.StatusCompleted).SetCreatedAt(yesterday).SaveX(ctx)

	rec, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:1"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.NewCount != 1 || rec.TaskID != 0 || rec.SkippedReason != SkipDailyCap {
		t.Fatalf("daily cap must skip the wake: %+v", rec)
	}

	// A user `command` task never consumes the budget — drop the automatic
	// ones and add commands, and the wake goes through.
	tasks, err := db.Client().Task.Query().Where(entTask.ShepherdKindNEQ("")).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		if tk.ShepherdKind == KindReview || tk.ShepherdKind == KindPatrol {
			if err := db.Client().Task.DeleteOneID(tk.ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	client.Task.Create().SetPrompt("c").SetShepherdKind(KindCommand).
		SetStatus(entTask.StatusCompleted).SetCreatedAt(now.Add(-time.Hour)).SaveX(ctx)

	rec2, err := RunPatrolPassWithCollect(now.Add(time.Hour), fakeItems("failed_task:1", "failed_task:9"))
	if err != nil {
		t.Fatal(err)
	}
	if rec2.TaskID == 0 {
		t.Fatalf("command tasks must not eat the wake budget: %+v", rec2)
	}
}

// TestPatrolZeroIntervalBeatsDoNothing pins "주기 0이면 루프가 돌지 않는다":
// with the interval off, beat() never reaches the pass — no records, no
// collects, even hours after the last pass.
func TestPatrolZeroIntervalBeatsDoNothing(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "0", nil, 0)

	called := 0
	p := NewPatrol()
	p.clock = time.Now
	p.collect = func(now time.Time) ([]AttentionItem, error) {
		called++
		return fakeItems("failed_task:1")(now)
	}

	now := time.Now()
	for i := 0; i < 60; i++ { // an hour of simulated beats
		p.beat(now.Add(time.Duration(i) * time.Minute))
	}

	if called != 0 {
		t.Fatalf("interval 0 must never run a pass, collect called %d times", called)
	}
	if n := countPatrolRows(t); n != 0 {
		t.Fatalf("interval 0 must store nothing, got %d rows", n)
	}
}

// TestPatrolBeatHonorsInterval drives the loop's gate through injected beats:
// a pass runs when due, not before, and a SHORTENED setting applies from the
// next beat (no restart).
func TestPatrolBeatHonorsInterval(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)

	calls := []time.Time{}
	p := NewPatrol()
	p.collect = func(now time.Time) ([]AttentionItem, error) {
		calls = append(calls, now)
		return nil, nil // no findings — only the gate is under test
	}

	t0 := time.Now()
	p.beat(t0)                      // no previous pass → due now
	p.beat(t0.Add(29 * time.Minute)) // not due (29m < 30m)
	p.beat(t0.Add(31 * time.Minute)) // due (31m ≥ 30m)
	if len(calls) != 2 {
		t.Fatalf("want passes at t0 and t0+31m, got %v", calls)
	}

	// Shorten the interval: the very next beat uses the new period. The last
	// pass was at t0+31m, so t0+36m is due under 5m but not under 30m.
	viper.Set("shepherd_patrol_interval", "5m")
	p.beat(t0.Add(34 * time.Minute)) // 3m since last → still not due even at 5m floor
	if len(calls) != 2 {
		t.Fatalf("beat before the new interval elapsed must skip: %v", calls)
	}
	p.beat(t0.Add(36 * time.Minute)) // 5m since last → due under the new setting
	if len(calls) != 3 {
		t.Fatalf("new interval must apply from the next beat: %v", calls)
	}

	// The 5-minute floor: "1m" behaves like 5m.
	viper.Set("shepherd_patrol_interval", "1m")
	p.beat(t0.Add(39 * time.Minute)) // 3m after the last pass → floor says not due
	if len(calls) != 3 {
		t.Fatalf("1m must clamp to the 5-minute floor: %v", calls)
	}
	p.beat(t0.Add(41 * time.Minute)) // 5m after → due
	if len(calls) != 4 {
		t.Fatalf("floor-clamped beat missing: %v", calls)
	}
}

// TestPatrolCollectionErrorRecordsOnly pins that a broken collection leaves a
// record with the reason and never wakes.
func TestPatrolCollectionErrorRecordsOnly(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)

	rec, err := RunPatrolPassWithCollect(time.Now(), failingCollect)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.SkippedReason, SkipCollectionFail) || rec.TaskID != 0 {
		t.Fatalf("collection error must record-only: %+v", rec)
	}
	if n := len(shepherdTasksByKind(t, KindPatrol)); n != 0 {
		t.Fatalf("collection error must never wake, got %d tasks", n)
	}
}

// TestTranslateSkippedReason pins the #665 API rendering: stored codes become
// current-language labels (detail tail kept), unknown/legacy Korean sentences
// pass through untouched.
func TestTranslateSkippedReason(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	i18n.Init("ko")
	if got := TranslateSkippedReason(SkipDailyCap); got != "하루 깨우기 상한 도달" {
		t.Errorf("ko daily_cap = %q", got)
	}
	if got := TranslateSkippedReason(SkipCollectionFail + ": boom"); got != "점검 실패: boom" {
		t.Errorf("ko collection_failed detail = %q", got)
	}

	i18n.Init("en")
	defer i18n.Init("ko")
	if got := TranslateSkippedReason(SkipWakeDisabled); got != "wake disabled" {
		t.Errorf("en wake_disabled = %q", got)
	}
	if got := TranslateSkippedReason(SkipEnqueueFailed + ": db down"); got != "shepherd task enqueue failed: db down" {
		t.Errorf("en enqueue_failed detail = %q", got)
	}
	for _, code := range []string{SkipWakeDisabled, SkipPatrolBusy, SkipDailyCap, SkipCostCap, SkipCollectionFail, SkipEnqueueFailed} {
		if got := TranslateSkippedReason(code); got == code || got == "" {
			t.Errorf("code %q must render an English label, got %q", code, got)
		}
	}

	// Legacy Korean rows (pre-#665) come back verbatim in any language.
	legacy := "하루 깨우기 상한 도달"
	if got := TranslateSkippedReason(legacy); got != legacy {
		t.Errorf("legacy row = %q, want verbatim", got)
	}
	if got := TranslateSkippedReason(""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
}

// TestPatrolRetention pins the 30-day prune riding on every pass.
func TestPatrolRetention(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()
	now := time.Now()

	old := now.Add(-31 * 24 * time.Hour)
	if _, err := db.Client().ShepherdPatrol.Create().
		SetCreatedAt(old).SetFindings("[]").Save(ctx); err != nil {
		t.Fatal(err)
	}
	recent := now.Add(-29 * 24 * time.Hour)
	if _, err := db.Client().ShepherdPatrol.Create().
		SetCreatedAt(recent).SetFindings("[]").Save(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:1")); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Client().ShepherdPatrol.Query().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 { // the 29-day row and the fresh pass; the 31-day row is gone
		t.Fatalf("30-day retention failed, rows=%d", len(rows))
	}
	for _, r := range rows {
		if r.CreatedAt.Equal(old) {
			t.Fatal("31-day-old patrol record survived the prune")
		}
	}
}

// TestPatrolListAttachesTaskSummary pins the GET payload shape: newest first,
// findings decoded, and the woken task's first summary line attached.
func TestPatrolListAttachesTaskSummary(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()
	now := time.Now()

	rec, err := RunPatrolPassWithCollect(now, fakeItems("failed_task:7"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Client().Task.UpdateOneID(rec.TaskID).
		SetSummary("순찰 결과\n두 번째 줄").Save(ctx); err != nil {
		t.Fatal(err)
	}

	finishPatrolTasks(t)
	if _, err := RunPatrolPassWithCollect(now.Add(time.Hour), fakeItems("failed_task:7")); err != nil {
		t.Fatal(err)
	}

	records, err := ListPatrols(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("want 2 records, got %d", len(records))
	}
	newest, older := records[0], records[1]
	if newest.CreatedAt.Before(older.CreatedAt) {
		t.Fatalf("records must be newest first: %+v / %+v", newest, older)
	}
	if older.TaskID != rec.TaskID || older.TaskSummary != "순찰 결과" {
		t.Fatalf("woken task link + first summary line expected: %+v", older)
	}
	if newest.NewCount != 0 || len(newest.Findings) != 1 || newest.Findings[0].Fingerprint != "failed_task:7" {
		t.Fatalf("findings must decode from the record JSON: %+v", newest)
	}
	if newest.TaskID != 0 || newest.TaskSummary != "" {
		t.Fatalf("non-wake record carries no task: %+v", newest)
	}
}

// TestNextPatrolAt pins the status-card countdown: off → not ok; no records →
// due now; otherwise last + interval.
func TestNextPatrolAt(t *testing.T) {
	attentionTestDB(t)
	patrolConfig(t, "30m", nil, 0)
	ctx := context.Background()
	now := time.Now()

	next, ok := NextPatrolAt(now)
	if !ok || !next.Equal(now) {
		t.Fatalf("with no records yet the next pass is due now: %v %v", next, ok)
	}

	last := now.Add(-10 * time.Minute)
	if _, err := db.Client().ShepherdPatrol.Create().
		SetCreatedAt(last).SetFindings("[]").Save(ctx); err != nil {
		t.Fatal(err)
	}
	next, ok = NextPatrolAt(now)
	if !ok || !next.Equal(last.Add(30 * time.Minute)) {
		t.Fatalf("next patrol must be last+interval: %v", next)
	}

	viper.Set("shepherd_patrol_interval", "0")
	if _, ok := NextPatrolAt(now); ok {
		t.Fatal("interval 0 must report patrol off")
	}
}
