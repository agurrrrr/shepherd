// Shepherd report notifications (#602).
//
// A finished task is announced through Discord and Web Push. This file decides
// HOW one completion is announced, so every call site (the daemon's processor
// callback, the MCP task_complete tool) behaves the same way:
//
//   - shepherd (목자) tasks — review / patrol / command — send a "목자 보고"
//     notification instead of the generic "작업 완료" one. The body carries the
//     shepherd's own report (the task summary). shepherd_notify=false turns
//     these off entirely.
//   - shepherd_notify_quiet_patrol=true suppresses two kinds of report that
//     carry no news: a patrol pass that found nothing new, and a review task
//     that changed no issue at all. The review judgement is only applied when
//     it can be made from the task's own timestamps; when they are missing the
//     report still goes out (fail open — a lost notification is worse than a
//     redundant one).
//   - shepherd_mute_delegated_notifications=true drops the GENERIC completion
//     notification for tasks that belong to a shepherd-watched issue, because
//     the shepherd reviews that work and reports it anyway. Failure and
//     user-stop notifications are never muted: they are exactly what a human
//     needs to see.
//
// queue cannot import manager (manager imports queue), so the daemon's
// processor callback calls in through AnnounceTaskComplete below.
package manager

import (
	"context"
	"log"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entPatrol "github.com/agurrrrr/shepherd/ent/shepherdpatrol"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/discord"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/push"
	"github.com/agurrrrr/shepherd/internal/queue"
)

// boolDefaultTrue reads a bool config key whose absent value means true, so a
// test that resets viper still sees the documented default (#602). Same shape
// as patrol.go's patrolWakeEnabled.
func boolDefaultTrue(key string) bool {
	if v := config.Get(key); v == nil {
		return true
	}
	return config.GetBool(key)
}

// AnnounceTaskComplete sends the completion notification for one finished task,
// choosing between the shepherd report form and the generic task form. It is
// safe to call from a goroutine: every failure is logged, never returned.
func AnnounceTaskComplete(taskID int, sheepName, projectName, summary string) {
	kind := queue.GetTaskShepherdKind(taskID)
	if kind != "" {
		announceShepherdReport(taskID, kind, summary)
		return
	}

	// Delegated (watched-issue) work: the shepherd reviews and reports it, so
	// the generic completion notice can be muted to avoid a double report.
	if MutedDelegatedCompletion(taskID) {
		log.Printf("[shepherd-notify] task #%d completion muted (watched issue, shepherd reports it)", taskID)
		return
	}

	sendGenericComplete(taskID, sheepName, projectName, summary)
}

// sendGenericComplete is the ordinary "작업 완료" notification pair.
func sendGenericComplete(taskID int, sheepName, projectName, summary string) {
	task, err := queue.GetTask(taskID)
	if err != nil {
		return
	}
	discord.NewTaskNotifier().SendTaskComplete(taskID, sheepName, projectName, summary, task.CostUsd, task.FilesModified)
	push.NotifyComplete(taskID, sheepName, projectName, summary)
}

// announceShepherdReport sends the "목자 보고" pair for one shepherd task.
func announceShepherdReport(taskID int, kind, summary string) {
	if !boolDefaultTrue("shepherd_notify") {
		return
	}
	if quiet := quietShepherdReport(context.Background(), taskID, kind); quiet != "" {
		log.Printf("[shepherd-notify] shepherd %s report #%d suppressed: %s", kind, taskID, quiet)
		return
	}

	task, err := queue.GetTask(taskID)
	if err != nil {
		log.Printf("[shepherd-notify] task #%d lookup for report: %v", taskID, err)
		return
	}

	discord.NewTaskNotifier().SendShepherdReport(taskID, kind, summary, task.CostUsd)
	push.NotifyShepherdReport(taskID, kind, summary)
}

// quietShepherdReport returns a non-empty reason when this report carries no
// news and quiet mode is on; "" means "send it".
func quietShepherdReport(ctx context.Context, taskID int, kind string) string {
	if !boolDefaultTrue("shepherd_notify_quiet_patrol") {
		return ""
	}

	switch kind {
	case KindPatrol:
		// The patrol row records exactly this question: did the pass find
		// anything the previous pass had not already seen?
		row, err := db.Client().ShepherdPatrol.Query().
			Where(entPatrol.TaskIDEQ(taskID)).
			Order(ent.Desc(entPatrol.FieldID)).
			First(ctx)
		if err != nil {
			return "" // no record (manual/legacy run) — not determinable, send
		}
		if row.NewCount == 0 {
			return "순찰에서 새 이상이 없었다"
		}
		return ""

	case KindReview:
		changed, determinable := reviewChangedIssues(ctx, taskID)
		if !determinable {
			return "" // timestamps missing — apply the rule to patrols only
		}
		if !changed {
			return "검토가 이슈를 하나도 바꾸지 않았다"
		}
		return ""

	default:
		return ""
	}
}

// reviewChangedIssues reports whether any issue moved during the review task's
// run window, and whether that question could be answered at all (both
// timestamps present, end not before start). An issue edit, a status change, a
// watch toggle or a follow-up task creation all refresh issue.updated_at, so
// one window query answers for all of them. The wake path bumps
// shepherd_rounds BEFORE the review task starts, so the window begins at
// started_at and does not count that bump as the review's own change.
func reviewChangedIssues(ctx context.Context, taskID int) (changed, determinable bool) {
	t, err := db.Client().Task.Query().
		Where(entTask.ID(taskID)).
		Only(ctx)
	if err != nil {
		return false, false
	}
	start, end := t.StartedAt, t.CompletedAt
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return false, false
	}

	n, err := db.Client().Issue.Query().
		Where(
			entIssue.UpdatedAtGTE(start),
			entIssue.UpdatedAtLTE(end),
		).
		Count(ctx)
	if err != nil {
		return false, false
	}
	return n > 0, true
}

// MutedDelegatedCompletion reports whether the generic completion notice for
// this task should be dropped because the shepherd watches its issue and will
// report the outcome itself (#602). Only completion goes through here.
func MutedDelegatedCompletion(taskID int) bool {
	if !config.GetBool("shepherd_mute_delegated_notifications") {
		return false
	}

	ctx := context.Background()
	t, err := db.Client().Task.Query().
		Where(entTask.ID(taskID)).
		WithIssue().
		WithSheep().
		Only(ctx)
	if err != nil || t.Edges.Issue == nil {
		return false
	}
	if t.Edges.Sheep != nil && names.IsManager(t.Edges.Sheep.Name) {
		return false // the shepherd's own task: never muted, it IS the report
	}

	iss, err := db.Client().Issue.Query().
		Where(entIssue.ID(t.Edges.Issue.ID)).
		Only(ctx)
	if err != nil {
		return false
	}
	return iss.ShepherdWatch
}
