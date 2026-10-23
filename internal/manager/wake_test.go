package manager

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/spf13/viper"
)

// wakeTestDB injects an in-memory DB for both the ent client and the raw
// handle — AppendToPendingReviewTaskPrompt runs raw SQL, so both must point at
// the same database (wiki: viper-writeconfigas-clobbers-config family of test
// hygiene traps).
func wakeTestDB(t *testing.T) *ent.Client {
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

// wakeConfig pins the wake-cap config keys for one test. viper.Set (never
// config.Set — that writes ~/.shepherd/config.yaml from tests).
func wakeConfig(t *testing.T, maxRounds, maxWakeups int) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("shepherd_max_review_rounds", maxRounds)
	viper.Set("shepherd_max_wakeups_per_day", maxWakeups)
}

// wakeSeeds creates: manager sheep + worker sheep + one project + one issue
// (watch configurable) + a finished task of the given state on that issue.
type wakeFixture struct {
	client   *ent.Client
	issue    *ent.Issue
	finished *ent.Task
	workerSh *ent.Sheep
	project  *ent.Project
}

func newWakeFixture(t *testing.T, watch bool, state task.Status, stopReason string) *wakeFixture {
	t.Helper()
	ctx := context.Background()
	client := wakeTestDB(t)

	// Manager sheep row (id/name = shepherd) so EnqueueShepherdTask works.
	client.Sheep.Create().SetName(names.ManagerName).SetStatus(sheep.StatusIdle).SaveX(ctx)
	workerSh := client.Sheep.Create().SetName("양순이").SetStatus(sheep.StatusIdle).SaveX(ctx)
	p := client.Project.Create().SetName("bori").SetPath("/ws/bori").SaveX(ctx)

	issCreate := client.Issue.Create().
		SetTitle("delegated work").
		SetStatus(entIssue.StatusTesting).
		SetShepherdWatch(watch).
		SetProject(p)
	if watch {
		now := time.Now()
		issCreate = issCreate.SetShepherdWatchedAt(now)
	}
	iss := issCreate.SaveX(ctx)

	finished := client.Task.Create().
		SetPrompt("do it").
		SetStatus(state).
		SetError(stopReason).
		SetSheepID(workerSh.ID).
		SetProject(p).
		SetIssueID(iss.ID).
		SaveX(ctx)

	return &wakeFixture{client: client, issue: iss, finished: finished, workerSh: workerSh, project: p}
}

// reviewTasks returns the shepherd review tasks currently in the DB.
func reviewTasks(t *testing.T) []*ent.Task {
	t.Helper()
	ctx := context.Background()
	ts, err := db.Client().Task.Query().Where(task.ShepherdKindEQ(KindReview)).All(ctx)
	if err != nil {
		t.Fatalf("query review tasks: %v", err)
	}
	return ts
}

// TestWakeRules covers the six wake rules from wake.go as a table. Every case
// drives handleIssueTaskFinished directly (the hook body), so no processor or
// provider is involved. Default-cap tests use explicit config values via viper.
func TestWakeRules(t *testing.T) {
	tests := []struct {
		name       string
		watch      bool
		state      task.Status
		stopReason string
		sheepName  string // sheep that ran the finished task ("" = worker)
		maxRounds  int
		maxWakeups int
		rounds     int // pre-existing shepherd_rounds on the issue
		preReviews int // pre-seeded (completed) review tasks today, for the day cap
		wantReview int // expected review tasks after the wake
		wantRounds int // expected shepherd_rounds after the wake
	}{
		{
			name:      "rule1 unwatched issue never wakes",
			watch:     false,
			state:     task.StatusCompleted,
			maxRounds: 3, maxWakeups: 30,
			wantReview: 0, wantRounds: 0,
		},
		{
			name:      "rule2 shepherd's own task never wakes",
			watch:     true,
			state:     task.StatusCompleted,
			sheepName: names.ManagerName,
			maxRounds: 3, maxWakeups: 30,
			wantReview: 0, wantRounds: 0,
		},
		{
			name:       "rule3 user stop does not wake",
			watch:      true,
			state:      task.StatusStopped,
			stopReason: "stopped by user",
			maxRounds:  3, maxWakeups: 30,
			wantReview: 0, wantRounds: 0,
		},
		{
			name:       "rule3 question stop wakes",
			watch:      true,
			state:      task.StatusStopped,
			stopReason: "question: which branch?",
			maxRounds:  3, maxWakeups: 30,
			wantReview: 1, wantRounds: 1,
		},
		{
			name:      "completed wakes",
			watch:     true,
			state:     task.StatusCompleted,
			maxRounds: 3, maxWakeups: 30,
			wantReview: 1, wantRounds: 1,
		},
		{
			name:       "failed wakes",
			watch:      true,
			state:      task.StatusFailed,
			stopReason: "boom",
			maxRounds:  3, maxWakeups: 30,
			wantReview: 1, wantRounds: 1,
		},
		{
			name:      "rule5 round cap reached does not wake",
			watch:     true,
			state:     task.StatusCompleted,
			maxRounds: 3, maxWakeups: 30,
			rounds:     3,
			wantReview: 0, wantRounds: 3,
		},
		{
			name:      "rule5 under round cap wakes",
			watch:     true,
			state:     task.StatusCompleted,
			maxRounds: 3, maxWakeups: 30,
			rounds:     2,
			wantReview: 1, wantRounds: 3,
		},
		{
			name:      "rule6 daily wake cap reached does not wake",
			watch:     true,
			state:     task.StatusCompleted,
			maxRounds: 3, maxWakeups: 2,
			preReviews: 2,
			wantReview: 0, wantRounds: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newWakeFixture(t, tt.watch, tt.state, tt.stopReason)
			wakeConfig(t, tt.maxRounds, tt.maxWakeups)

			ctx := context.Background()
			if tt.rounds > 0 {
				f.client.Issue.UpdateOneID(f.issue.ID).SetShepherdRounds(tt.rounds).SaveX(ctx)
			}
			// rule6 setup: pre-seed completed review tasks to fill the day cap.
			if tt.preReviews > 0 {
				mgr := mustManagerSheep(t)
				for i := 0; i < tt.preReviews; i++ {
					f.client.Task.Create().SetPrompt("seed review").SetStatus(task.StatusCompleted).
						SetShepherdKind(KindReview).SetSheepID(mgr.ID).SaveX(ctx)
				}
			}

			sheepName := tt.sheepName
			if sheepName == "" {
				sheepName = "양순이"
			}

			handleIssueTaskFinished(queue.IssueTaskEvent{
				TaskID:       f.finished.ID,
				IssueID:      f.issue.ID,
				Project:      "bori",
				TaskState:    string(tt.state),
				StopReason:   tt.stopReason,
				QuestionStop: strings.HasPrefix(tt.stopReason, "question: "),
				UserStop:     isUserStopMarker(tt.stopReason),
				SheepName:    sheepName,
			})

			got := len(reviewTasks(t))
			if got != tt.preReviews+tt.wantReview {
				t.Fatalf("review tasks = %d, want %d", got, tt.preReviews+tt.wantReview)
			}

			iss, err := f.client.Issue.Get(ctx, f.issue.ID)
			if err != nil {
				t.Fatalf("reload issue: %v", err)
			}
			if iss.ShepherdRounds != tt.wantRounds {
				t.Errorf("shepherd_rounds = %d, want %d", iss.ShepherdRounds, tt.wantRounds)
			}
		})
	}
}

// isUserStopMarker mirrors queue.isUserStopReason for building test events.
func isUserStopMarker(reason string) bool {
	switch reason {
	case "stopped by user", "cancelled by user", "task was cancelled",
		"cancelled: stale task from previous session":
		return true
	}
	return false
}

func mustManagerSheep(t *testing.T) *ent.Sheep {
	t.Helper()
	ctx := context.Background()
	s, err := db.Client().Sheep.Query().Where(sheep.Name(names.ManagerName)).Only(ctx)
	if err != nil {
		t.Fatalf("manager sheep: %v", err)
	}
	return s
}

// TestWakeBundleGate pins rule 4: a completed task defers the wake while the
// cascade bundle still has pending/running watched work, but failures and
// question stops bypass the gate.
func TestWakeBundleGate(t *testing.T) {
	ctx := context.Background()

	t.Run("success defers while sibling is running", func(t *testing.T) {
		f := newWakeFixture(t, true, task.StatusCompleted, "")
		wakeConfig(t, 3, 30)

		// A watched sibling child with a running task in the same bundle.
		child := f.client.Issue.Create().SetTitle("child").SetStatus(entIssue.StatusInProgress).
			SetShepherdWatch(true).SetProject(f.project).SetParentID(f.issue.ID).SaveX(ctx)
		f.client.Task.Create().SetPrompt("sibling").SetStatus(task.StatusRunning).
			SetSheepID(f.workerSh.ID).SetProject(f.project).SetIssueID(child.ID).SaveX(ctx)

		handleIssueTaskFinished(eventFor(f, "양순이"))
		if n := len(reviewTasks(t)); n != 0 {
			t.Fatalf("bundle still active: expected deferred wake, got %d review tasks", n)
		}
	})

	t.Run("success wakes when no sibling work remains", func(t *testing.T) {
		f := newWakeFixture(t, true, task.StatusCompleted, "")
		wakeConfig(t, 3, 30)

		child := f.client.Issue.Create().SetTitle("child").SetStatus(entIssue.StatusTesting).
			SetShepherdWatch(true).SetProject(f.project).SetParentID(f.issue.ID).SaveX(ctx)
		// Sibling already finished (terminal) — does not block.
		f.client.Task.Create().SetPrompt("sibling").SetStatus(task.StatusCompleted).
			SetSheepID(f.workerSh.ID).SetProject(f.project).SetIssueID(child.ID).SaveX(ctx)

		handleIssueTaskFinished(eventFor(f, "양순이"))
		if n := len(reviewTasks(t)); n != 1 {
			t.Fatalf("bundle drained: expected one wake, got %d", n)
		}
	})

	t.Run("failure wakes even with active sibling", func(t *testing.T) {
		f := newWakeFixture(t, true, task.StatusFailed, "boom")
		wakeConfig(t, 3, 30)

		child := f.client.Issue.Create().SetTitle("child").SetStatus(entIssue.StatusInProgress).
			SetShepherdWatch(true).SetProject(f.project).SetParentID(f.issue.ID).SaveX(ctx)
		f.client.Task.Create().SetPrompt("sibling").SetStatus(task.StatusRunning).
			SetSheepID(f.workerSh.ID).SetProject(f.project).SetIssueID(child.ID).SaveX(ctx)

		handleIssueTaskFinished(eventFor(f, "양순이"))
		if n := len(reviewTasks(t)); n != 1 {
			t.Fatalf("failure must bypass the bundle gate, got %d review tasks", n)
		}
	})
}

func eventFor(f *wakeFixture, sheepName string) queue.IssueTaskEvent {
	return queue.IssueTaskEvent{
		TaskID:    f.finished.ID,
		IssueID:   f.issue.ID,
		Project:   "bori",
		TaskState: string(f.finished.Status),
		SheepName: sheepName,
	}
}

// TestWakeCoalescing pins that a second wake while a review task is still
// pending appends to that task's prompt instead of stacking a new one, and the
// round counter still advances once per event.
func TestWakeCoalescing(t *testing.T) {
	ctx := context.Background()
	f := newWakeFixture(t, true, task.StatusCompleted, "")
	wakeConfig(t, 3, 30)

	// Two finished tasks on the same watched issue.
	second := f.client.Task.Create().SetPrompt("do it 2").SetStatus(task.StatusCompleted).
		SetSheepID(f.workerSh.ID).SetProject(f.project).SetIssueID(f.issue.ID).SaveX(ctx)

	handleIssueTaskFinished(eventFor(f, "양순이"))
	first := reviewTasks(t)
	if len(first) != 1 {
		t.Fatalf("expected one review task, got %d", len(first))
	}

	ev2 := eventFor(f, "양순이")
	ev2.TaskID = second.ID
	handleIssueTaskFinished(ev2)

	after := reviewTasks(t)
	if len(after) != 1 {
		t.Fatalf("coalescing expected: still one pending review task, got %d", len(after))
	}
	if strings.Count(after[0].Prompt, "issue #") < 2 {
		t.Fatalf("second event was not appended to the pending review prompt:\n%s", after[0].Prompt)
	}

	iss, _ := f.client.Issue.Get(ctx, f.issue.ID)
	if iss.ShepherdRounds != 2 {
		t.Errorf("each event should advance rounds once: got %d want 2", iss.ShepherdRounds)
	}
}

// TestWakeCoalescingRunningTaskNotAppended pins that when the pending review
// task has already been claimed (running), AppendToPendingReviewTaskPrompt's
// status guard fails and a fresh review task is created instead.
func TestWakeCoalescingRunningTaskNotAppended(t *testing.T) {
	f := newWakeFixture(t, true, task.StatusCompleted, "")
	wakeConfig(t, 3, 30)

	handleIssueTaskFinished(eventFor(f, "양순이"))
	first := reviewTasks(t)
	if len(first) != 1 {
		t.Fatalf("expected one review task, got %d", len(first))
	}

	// Processor claimed the review row → running.
	ctx := context.Background()
	f.client.Task.UpdateOneID(first[0].ID).SetStatus(task.StatusRunning).SaveX(ctx)

	second := f.client.Task.Create().SetPrompt("do it 2").SetStatus(task.StatusCompleted).
		SetSheepID(f.workerSh.ID).SetProject(f.project).SetIssueID(f.issue.ID).SaveX(ctx)
	ev2 := eventFor(f, "양순이")
	ev2.TaskID = second.ID
	handleIssueTaskFinished(ev2)

	if n := len(reviewTasks(t)); n != 2 {
		t.Fatalf("running review must not absorb the event: expected 2 tasks, got %d", n)
	}
}
