package manager

import (
	"context"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/spf13/viper"
)

// notifyConfig pins the #602 notification switches for one test. viper.Set,
// never config.Set (that would write the operator's config.yaml).
func notifyConfig(t *testing.T, notify, quietPatrol, muteDelegated bool) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("shepherd_notify", notify)
	viper.Set("shepherd_notify_quiet_patrol", quietPatrol)
	viper.Set("shepherd_mute_delegated_notifications", muteDelegated)
}

// createShepherdTask adds one completed shepherd task of the given kind, with
// optional run-window timestamps (zero time = leave the column unset).
func createShepherdTask(t *testing.T, client *ent.Client, kind string, started, completed time.Time) *ent.Task {
	t.Helper()
	ctx := context.Background()

	c := client.Task.Create().
		SetPrompt("shepherd work").
		SetStatus(entTask.StatusCompleted).
		SetSummary("report text").
		SetShepherdKind(kind)
	if !started.IsZero() {
		c = c.SetStartedAt(started)
	}
	if !completed.IsZero() {
		c = c.SetCompletedAt(completed)
	}
	return c.SaveX(ctx)
}

func TestBoolDefaultTrue_미설정시참(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	if !boolDefaultTrue("shepherd_notify") {
		t.Error("unset shepherd_notify should mean true")
	}
	viper.Set("shepherd_notify", false)
	if boolDefaultTrue("shepherd_notify") {
		t.Error("explicit false should win over the default")
	}
}

func TestQuietShepherdReport_Patrol(t *testing.T) {
	client := wakeTestDB(t)
	notifyConfig(t, true, true, false)
	ctx := context.Background()

	noNews := createShepherdTask(t, client, KindPatrol, time.Now(), time.Now())
	withNews := createShepherdTask(t, client, KindPatrol, time.Now(), time.Now())

	client.ShepherdPatrol.Create().
		SetCreatedAt(time.Now()).SetFindings("[]").SetNewCount(0).
		SetTaskID(noNews.ID).SaveX(ctx)
	client.ShepherdPatrol.Create().
		SetCreatedAt(time.Now()).SetFindings("[]").SetNewCount(3).
		SetTaskID(withNews.ID).SaveX(ctx)

	if got := quietShepherdReport(ctx, noNews.ID, KindPatrol); got == "" {
		t.Error("patrol with new_count=0 should be suppressed when quiet mode is on")
	}
	if got := quietShepherdReport(ctx, withNews.ID, KindPatrol); got != "" {
		t.Errorf("patrol with new findings should not be suppressed, got %q", got)
	}

	// A patrol task with no record (legacy or manual run) is not determinable:
	// the report goes out.
	orphan := createShepherdTask(t, client, KindPatrol, time.Now(), time.Now())
	if got := quietShepherdReport(ctx, orphan.ID, KindPatrol); got != "" {
		t.Errorf("patrol without a record should not be suppressed, got %q", got)
	}

	// Quiet mode off: even the no-news patrol reports.
	viper.Set("shepherd_notify_quiet_patrol", false)
	if got := quietShepherdReport(ctx, noNews.ID, KindPatrol); got != "" {
		t.Errorf("quiet mode off should not suppress, got %q", got)
	}
}

func TestQuietShepherdReport_Review(t *testing.T) {
	client := wakeTestDB(t)
	notifyConfig(t, true, true, false)
	ctx := context.Background()

	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)

	start := time.Now().Add(-time.Minute)
	end := time.Now().Add(-time.Second)

	// Review whose window contains an issue update → report.
	changedTask := createShepherdTask(t, client, KindReview, start, end)
	client.Issue.Create().
		SetTitle("touched").SetProject(p).
		SetUpdatedAt(start.Add(30 * time.Second)).
		SaveX(ctx)
	if got := quietShepherdReport(ctx, changedTask.ID, KindReview); got != "" {
		t.Errorf("review that changed an issue should not be suppressed, got %q", got)
	}

	// Review whose window holds no issue update → suppressed.
	quietTask := createShepherdTask(t, client, KindReview, start.Add(-2*time.Hour), start.Add(-time.Hour))
	if got := quietShepherdReport(ctx, quietTask.ID, KindReview); got == "" {
		t.Error("review that changed no issue should be suppressed when quiet mode is on")
	}

	// Missing timestamps → not determinable → report anyway.
	legacy := createShepherdTask(t, client, KindReview, time.Time{}, time.Time{})
	if got := quietShepherdReport(ctx, legacy.ID, KindReview); got != "" {
		t.Errorf("review without timestamps should not be suppressed, got %q", got)
	}

	// Command reports are never quiet.
	cmdTask := createShepherdTask(t, client, KindCommand, start.Add(-2*time.Hour), start.Add(-time.Hour))
	if got := quietShepherdReport(ctx, cmdTask.ID, KindCommand); got != "" {
		t.Errorf("command report should never be suppressed, got %q", got)
	}

	// Quiet mode off: the no-change review reports too.
	viper.Set("shepherd_notify_quiet_patrol", false)
	if got := quietShepherdReport(ctx, quietTask.ID, KindReview); got != "" {
		t.Errorf("quiet mode off should not suppress, got %q", got)
	}
}

func TestMutedDelegatedCompletion(t *testing.T) {
	f := newWakeFixture(t, true, entTask.StatusCompleted, "")
	ctx := context.Background()

	// Watched issue + mute on → muted.
	notifyConfig(t, true, true, true)
	if !MutedDelegatedCompletion(f.finished.ID) {
		t.Error("completion of a watched-issue task should be muted")
	}

	// Mute off → not muted.
	notifyConfig(t, true, true, false)
	if MutedDelegatedCompletion(f.finished.ID) {
		t.Error("mute off should keep the generic notification")
	}

	// Unwatched issue → not muted even with the switch on.
	notifyConfig(t, true, true, true)
	unwatched, err := f.client.Issue.Create().
		SetTitle("plain").SetStatus(entIssue.StatusTodo).
		SetShepherdWatch(false).SetProject(f.project).Save(ctx)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	plain := f.client.Task.Create().
		SetPrompt("x").SetStatus(entTask.StatusCompleted).
		SetSheep(f.workerSh).SetProject(f.project).SetIssueID(unwatched.ID).
		SaveX(ctx)
	if MutedDelegatedCompletion(plain.ID) {
		t.Error("unwatched-issue completion should not be muted")
	}

	// The shepherd's own task on a watched issue is never muted: it IS the
	// report. AnnounceTaskComplete routes it to the report path first, but the
	// guard stays for direct callers.
	mgrSheep, err := f.client.Sheep.Query().
		Where(sheep.NameEQ(names.ManagerName)).Only(ctx)
	if err != nil {
		t.Fatalf("manager sheep: %v", err)
	}
	own := f.client.Task.Create().
		SetPrompt("review").SetStatus(entTask.StatusCompleted).
		SetShepherdKind(KindReview).
		SetSheep(mgrSheep).SetProject(f.project).SetIssueID(f.issue.ID).
		SaveX(ctx)
	if MutedDelegatedCompletion(own.ID) {
		t.Error("shepherd's own task must not be muted")
	}

	// Task without an issue → not muted.
	orphan := f.client.Task.Create().
		SetPrompt("loose").SetStatus(entTask.StatusCompleted).
		SetSheep(f.workerSh).SetProject(f.project).
		SaveX(ctx)
	if MutedDelegatedCompletion(orphan.ID) {
		t.Error("task without an issue should not be muted")
	}
}
