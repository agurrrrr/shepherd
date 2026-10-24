// Shepherd (목자) wake logic (#597).
//
// When a task linked to a watched issue finishes, the daemon wakes the
// shepherd by enqueueing a `review` task. queue cannot import manager
// (manager imports queue), so the daemon connects them at startup through
// RegisterHooks → queue.SetIssueTaskFinishedHook.
package manager

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/taskchanges"
)

// maxReviewRoundsDefault / maxWakeupsPerDayDefault are the fallbacks when the
// shepherd_max_review_rounds / shepherd_max_wakeups_per_day config keys are
// unset or non-positive (#597). Both are exposed through
// GET/PATCH /api/shepherd/config and the settings → 목자 tab.
const (
	maxReviewRoundsDefault  = 3
	maxWakeupsPerDayDefault = 30

	// reviewSummaryMaxChars bounds the summary/error block inside a review
	// prompt; longer text is cut and the shepherd is pointed at get_task_detail.
	reviewSummaryMaxChars = 1500
)

// appendMu serializes review-task coalescing appends within this process. The
// status=pending guard in queue.AppendToPendingReviewTaskPrompt protects
// against the processor claiming the row; the mutex protects against two wake
// events racing on the same prompt read-modify-write.
var appendMu sync.Mutex

// RegisterHooks connects the shepherd wake path to the queue (#597). Call once
// at daemon startup, before RecoverStuckTasks runs, so recovered tasks also
// flow through the hook. processor may be nil (wake then relies on the tick
// loop instead of an immediate ProcessPendingNow).
func RegisterHooks(processor *queue.Processor) {
	queue.SetIssueTaskFinishedHook(handleIssueTaskFinished)
	wakeProcessor = processor
}

// wakeProcessor is the daemon's queue processor, used to start a freshly
// enqueued review task without waiting for the next tick.
var wakeProcessor *queue.Processor

// IsShepherdCaller reports whether taskID belongs to a task running on the
// manager (목자) sheep (#597). The daemon uses it to recognize MCP tool calls
// forwarded with caller_task_id and auto-mark issues as watched, even when the
// shepherd forgot watch=true. Stage #601 reuses this for permission checks.
func IsShepherdCaller(taskID int) bool {
	if taskID <= 0 {
		return false
	}
	ctx := context.Background()
	t, err := db.Client().Task.Query().
		Where(entTask.ID(taskID)).
		WithSheep().
		Only(ctx)
	if err != nil {
		return false
	}
	if t.Edges.Sheep == nil {
		return false
	}
	return names.IsManager(t.Edges.Sheep.Name)
}

// maxReviewRounds returns the per-issue review round cap.
func maxReviewRounds() int {
	n := config.GetInt("shepherd_max_review_rounds")
	if n <= 0 {
		return maxReviewRoundsDefault
	}
	return n
}

// maxWakeupsPerDay returns the per-day shepherd wake cap (local midnight).
func maxWakeupsPerDay() int {
	n := config.GetInt("shepherd_max_wakeups_per_day")
	if n <= 0 {
		return maxWakeupsPerDayDefault
	}
	return n
}

// wakeupsToday counts automatic shepherd wakes created since local midnight
// — one wake is one review run, and coalescing an event into an
// already-pending review task does not add to the day total. Stage #600's
// patrol wakes count toward the same budget (shepherd_max_wakeups_per_day),
// so both kinds are included; user `command` tasks never were and still are
// not — the user waking the shepherd by hand is not an automatic wake.
func wakeupsToday() (int, error) {
	ctx := context.Background()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return db.Client().Task.Query().
		Where(
			entTask.ShepherdKindIn(KindReview, KindPatrol),
			entTask.CreatedAtGTE(midnight),
		).
		Count(ctx)
}

// handleIssueTaskFinished applies the wake rules (#597):
//
//  1. the issue is shepherd_watch=true;
//  2. the finished task is not the shepherd's own task;
//  3. the task was not stopped/cancelled by the user (question stops DO wake);
//  4. for cascade bundles, failures and question stops wake immediately, but a
//     success waits until no pending/running watched task remains in the bundle;
//  5. shepherd_rounds < shepherd_max_review_rounds;
//  6. today's wake count < shepherd_max_wakeups_per_day.
//
// Every queue goroutine calling this is already post-DB-update, and queue runs
// the hook in its own goroutine, so nothing here blocks task completion.
func handleIssueTaskFinished(ev queue.IssueTaskEvent) {
	ctx := context.Background()
	client := db.Client()

	iss, err := client.Issue.Query().
		Where(entIssue.ID(ev.IssueID)).
		Only(ctx)
	if err != nil {
		log.Printf("[shepherd-wake] issue #%d lookup failed: %v", ev.IssueID, err)
		return
	}

	// Rule 1: only watched issues wake the shepherd.
	if !iss.ShepherdWatch {
		return
	}
	// Rule 2: never wake for the shepherd's own work (review/command/patrol).
	if names.IsManager(ev.SheepName) {
		return
	}
	// Rule 3: user-initiated stops mean the user took over; #599 surfaces the
	// issue in the needs-attention list instead. Question stops stay wakeable —
	// the shepherd can answer and re-execute.
	if ev.UserStop {
		log.Printf("[shepherd-wake] issue #%d task #%d stopped by user — no wake (needs attention)", ev.IssueID, ev.TaskID)
		return
	}

	// Rule 4: cascade bundle gate for successes.
	if ev.TaskState == "completed" && bundleHasActiveWork(ctx, client, iss) {
		log.Printf("[shepherd-wake] issue #%d task #%d completed, but the bundle still has queued/running watched work — deferring wake", ev.IssueID, ev.TaskID)
		return
	}

	// Rule 5: per-issue review round cap.
	maxRounds := maxReviewRounds()
	if iss.ShepherdRounds >= maxRounds {
		log.Printf("[shepherd-wake] issue #%d reached the review round cap (%d/%d) — no wake (needs attention)", ev.IssueID, iss.ShepherdRounds, maxRounds)
		return
	}

	// Rule 6: per-day wake cap.
	today, err := wakeupsToday()
	if err != nil {
		log.Printf("[shepherd-wake] counting today's wakeups: %v", err)
		return
	}
	if today >= maxWakeupsPerDay() {
		log.Printf("[shepherd-wake] daily shepherd wake cap reached (%d/%d) — not waking for issue #%d task #%d (needs attention)",
			today, maxWakeupsPerDay(), ev.IssueID, ev.TaskID)
		return
	}

	// Rule 7: daily cost limit (#601). Over the cap, automatic waking stops —
	// the cost_limit attention item (with its one-time push/Discord notice)
	// is raised from here and from the patrol pass, not by a background timer.
	if exceeded, spent, limit, cerr := CostLimitStatus(time.Now()); cerr != nil {
		log.Printf("[shepherd-wake] cost-limit check: %v", cerr)
	} else if exceeded {
		log.Printf("[shepherd-wake] daily cost limit reached ($%.2f/$%.2f) — not waking for issue #%d task #%d (needs attention)",
			spent, limit, ev.IssueID, ev.TaskID)
		MaybeNotifyCostLimit(time.Now(), spent, limit)
		return
	}

	// Count the round before building the prompt so "review round k/max" shows
	// this wake's number.
	if _, err := client.Issue.UpdateOneID(iss.ID).AddShepherdRounds(1).Save(ctx); err != nil {
		log.Printf("[shepherd-wake] issue #%d increment rounds: %v", ev.IssueID, err)
		return
	}

	block := buildReviewBlock(ctx, iss, ev, iss.ShepherdRounds+1, maxRounds)

	// Coalescing: if a review task is still pending, append this event to its
	// prompt instead of stacking another review run. The pending-guarded UPDATE
	// loses when the processor claimed the row first (0 rows), and then — and
	// only then — a new review task is created.
	appended := false
	appendMu.Lock()
	if queue.AppendToPendingReviewTaskPrompt(block) {
		appended = true
	}
	appendMu.Unlock()

	if appended {
		log.Printf("[shepherd-wake] issue #%d task #%d (%s): merged into the pending review task", ev.IssueID, ev.TaskID, ev.TaskState)
		return
	}

	t, err := EnqueueShepherdTask(KindReview, reviewPromptHeader+"\n\n"+block, 0)
	if err != nil {
		log.Printf("[shepherd-wake] enqueue review task for issue #%d: %v", ev.IssueID, err)
		return
	}
	log.Printf("[shepherd-wake] issue #%d task #%d (%s): enqueued shepherd review task #%d (round %d/%d)",
		ev.IssueID, ev.TaskID, ev.TaskState, t.ID, iss.ShepherdRounds+1, maxRounds)

	if wakeProcessor != nil {
		wakeProcessor.ProcessPendingNow()
	}
}

// bundleHasActiveWork reports whether a cascade bundle still has watched work
// queued or running. The bundle of an issue is itself + its children, plus the
// parent's other children when the issue is one of them (exactly the set
// issue.Execute enqueues in one cascade). Successes wait for the bundle's last
// task; failures and question stops bypass this gate.
func bundleHasActiveWork(ctx context.Context, client *ent.Client, iss *ent.Issue) bool {
	ids := map[int]bool{iss.ID: true}

	childIDs := func(parentID int) []int {
		rows, err := client.Issue.Query().
			Where(entIssue.ParentIDEQ(parentID)).
			Select(entIssue.FieldID).
			All(ctx)
		if err != nil {
			return nil
		}
		out := make([]int, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}

	for _, id := range childIDs(iss.ID) {
		ids[id] = true
	}
	if iss.ParentID != nil && *iss.ParentID > 0 {
		parentID := *iss.ParentID
		ids[parentID] = true
		for _, sibling := range childIDs(parentID) {
			ids[sibling] = true
		}
	}

	idList := make([]int, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}

	n, err := client.Task.Query().
		Where(
			entTask.HasIssueWith(entIssue.IDIn(idList...), entIssue.ShepherdWatch(true)),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning),
		).
		Count(ctx)
	if err != nil {
		// Fail open: an unexpected query error must not silently swallow the
		// wake — better one early review than a shepherd that never wakes.
		log.Printf("[shepherd-wake] bundle check for issue #%d: %v", iss.ID, err)
		return false
	}
	return n > 0
}

const reviewPromptHeader = `[Shepherd review request]
A watched issue's task finished. Decide per the review procedure: done / re-execute with added guidance / new follow-up issue.`

// buildReviewBlock renders one event for the review prompt (#597):
//
//   - project: <name>, issue #<id> "<title>" (type, status, review round k/max)
//   - task #<tid>: <completed | failed | stopped(question)>, sheep <name>, duration, cost
//   - summary or error: (≤1500 chars; longer text cut + get_task_detail hint)
//   - checkpoint changes: N files, +a -d (only when available)
//   - issue goal: <goal>
func buildReviewBlock(ctx context.Context, iss *ent.Issue, ev queue.IssueTaskEvent, round, maxRounds int) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("- project: %s, issue #%d %q (%s, %s, review round %d/%d)\n",
		ev.Project, iss.ID, iss.Title, string(iss.Type), string(iss.Status), round, maxRounds))

	t, err := db.Client().Task.Query().Where(entTask.ID(ev.TaskID)).Only(ctx)
	if err != nil {
		t = nil
	}

	state := ev.TaskState
	if ev.QuestionStop {
		state = "stopped(question)"
	}
	sheep := ev.SheepName
	if sheep == "" {
		sheep = "?"
	}

	line := fmt.Sprintf("- task #%d: %s, sheep %s", ev.TaskID, state, sheep)
	if t != nil {
		if d := taskDuration(t); d != "" {
			line += ", duration " + d
		}
		if t.CostUsd > 0 {
			line += fmt.Sprintf(", cost $%.2f", t.CostUsd)
		}
	}
	sb.WriteString(line + "\n")

	summaryOrError := ""
	label := "summary"
	if t != nil {
		if ev.TaskState == "failed" || ev.TaskState == "stopped" {
			summaryOrError = strings.TrimSpace(t.Error)
			label = "error"
			if summaryOrError == "" {
				summaryOrError = strings.TrimSpace(t.Summary)
				label = "summary"
			}
		} else {
			summaryOrError = strings.TrimSpace(t.Summary)
		}
	}
	if summaryOrError == "" {
		summaryOrError = "(none)"
	} else if len(summaryOrError) > reviewSummaryMaxChars {
		summaryOrError = summaryOrError[:reviewSummaryMaxChars] +
			fmt.Sprintf("\n…(truncated — call get_task_detail task_id=%d for the full result)", ev.TaskID)
	}
	sb.WriteString(fmt.Sprintf("- %s: %s\n", label, summaryOrError))

	if t != nil {
		if cs := taskchanges.Build(ctx, t, false); cs.Available && len(cs.Files) > 0 {
			sb.WriteString(fmt.Sprintf("- checkpoint changes: %d files, +%d -%d\n",
				len(cs.Files), cs.Additions, cs.Deletions))
		}
	}

	goal := strings.TrimSpace(iss.Goal)
	if goal == "" {
		goal = "(none)"
	}
	sb.WriteString(fmt.Sprintf("- issue goal: %s", goal))

	return sb.String()
}

// taskDuration renders started_at → completed_at ("12m30s"), or "" when the
// timestamps are missing.
func taskDuration(t *ent.Task) string {
	if t.StartedAt.IsZero() || t.CompletedAt.IsZero() {
		return ""
	}
	d := t.CompletedAt.Sub(t.StartedAt)
	if d < 0 {
		return ""
	}
	return d.Round(time.Second).String()
}
