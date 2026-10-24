package queue

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/predicate"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/daemon"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// IssueTaskEvent describes one finished task that was linked to an issue
// (#597). The manager package registers a hook (SetTaskFinishedHook) so the
// shepherd (목자) can wake up and review delegated work. queue cannot import
// manager — manager imports queue — so the wiring goes through this callback.
type IssueTaskEvent struct {
	TaskID       int    // finished task
	IssueID      int    // issue the task was linked to
	Project      string // project name of the issue (empty when unresolvable)
	TaskState    string // completed | failed | stopped
	StopReason   string // error/reason column of the task when stopped/failed
	QuestionStop bool   // stopped by AskUserQuestion (the shepherd can answer and re-execute)
	UserStop     bool   // stopped/cancelled by the user from Web or CLI (do not wake)
	SheepName    string // sheep that ran the task
}

// issueTaskFinishedHook is nil until the daemon registers it (manager.RegisterHooks).
// CLI processes never register it, so their bulk-cancel paths stay silent.
var issueTaskFinishedHook func(IssueTaskEvent)

// SetIssueTaskFinishedHook installs the issue-task finished hook. Call with nil to
// remove it (tests). Registration order: the daemon registers before startup
// recovery runs, so recovered tasks also flow through the hook.
func SetIssueTaskFinishedHook(hook func(IssueTaskEvent)) {
	issueTaskFinishedHook = hook
}

// isUserStopReason reports whether a stopped task's error column marks a
// user-initiated stop/cancel (#597 wake rule 3). These paths never wake the
// shepherd: the user intervened on purpose, and stage #599 surfaces the issue
// in the needs-attention list instead.
//
// Sources of these reasons:
//   - "stopped by user" — POST /api/tasks/:id/stop (Web/CLI)
//   - "cancelled by user" — queue clear (CancelPendingTask(s)/CancelRunningTasks)
//   - "task was cancelled" — worker.StopTask cancel surfaced through the processor
//   - "cancelled: stale task from previous session" — startup cleanup, which
//     normally follows a user-initiated `shepherd stop`
//
// A question stop ("question: ...") is NOT a user stop: the shepherd may answer
// it and re-execute the issue.
func isUserStopReason(reason string) bool {
	switch reason {
	case "stopped by user", "cancelled by user", "task was cancelled",
		"cancelled: stale task from previous session":
		return true
	}
	return false
}

// notifyTaskFinished loads the task's issue link and project name, then runs
// the registered hook in a goroutine so task-finish handling is never delayed.
// It must be called after the DB status update (and after
// updateIssueStatusOnTaskComplete) on every single-task terminal path;
// syncIssuesFailedForTasks calls it per ID. Non-terminal rows (pending/running)
// are skipped so a delete-before-row-removal can never wake the shepherd.
func notifyTaskFinished(taskID int) {
	if issueTaskFinishedHook == nil {
		return
	}

	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.ID(taskID)).
		WithIssue().
		WithSheep().
		Only(ctx)
	if err != nil || t.Edges.Issue == nil {
		return
	}
	if t.Status != task.StatusCompleted && t.Status != task.StatusFailed && t.Status != task.StatusStopped {
		return
	}

	projectName := ""
	if iss, qerr := client.Issue.Query().
		Where(entIssue.ID(t.Edges.Issue.ID)).
		WithProject().
		Only(ctx); qerr == nil && iss.Edges.Project != nil {
		projectName = iss.Edges.Project.Name
	}

	ev := IssueTaskEvent{
		TaskID:     taskID,
		IssueID:    t.Edges.Issue.ID,
		Project:    projectName,
		TaskState:  string(t.Status),
		StopReason: t.Error,
	}
	if t.Status == task.StatusStopped {
		ev.QuestionStop = strings.HasPrefix(t.Error, "question: ")
	}
	// The CLI stop path records a failed row with reason "stopped by user", so
	// the user-stop check applies to every terminal state, not just stopped.
	ev.UserStop = isUserStopReason(t.Error)
	if t.Edges.Sheep != nil {
		ev.SheepName = t.Edges.Sheep.Name
	}

	hook := issueTaskFinishedHook
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[queue] task-finished hook panicked for task #%d: %v", taskID, r)
			}
		}()
		hook(ev)
	}()
}

// CreateTask creates a new task with pending status.
func CreateTask(prompt string, sheepID, projectID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		SetProjectID(projectID).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return t, nil
}

// CreateManagerTask creates a task for the manager (shepherd commands).
// Manager tasks don't have a project association.
func CreateManagerTask(prompt string, sheepID int) (*ent.Task, error) {
	return CreateManagerTaskWithKind(prompt, sheepID, 0, "")
}

// CreateManagerTaskWithKind is CreateManagerTask with the two fields shepherd
// (목자) tasks need (#594):
//   - priority: user commands run at priority 1 so a queued command overtakes
//     the automatic review/patrol tasks (priority 0).
//   - kind: marks the task as a shepherd task — "command" (user instruction),
//     "review" (delegated-task review), or "patrol". Empty means a normal
//     manager task without the shepherd semantics.
func CreateManagerTaskWithKind(prompt string, sheepID, priority int, kind string) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	create := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		SetPriority(priority)
	if kind != "" {
		create = create.SetShepherdKind(kind)
	}

	t, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	return t, nil
}

// CreateFollowUpTask creates a continuation task produced by a context-overflow
// handoff. It is given priority 1 so it runs ahead of ordinary pending tasks
// (priority 0) — keeping the handoff chain A→A'→A” contiguous instead of being
// pushed to the back of the FIFO queue. handoffDepth records how many times this
// line of work has handed itself off (parent depth + 1), for loop detection and
// alarms. projectID <= 0 creates a manager task (no project association).
//
// model, when non-empty, is persisted as the per-task model override so the
// follow-up runs on the same endpoint/model as the parent instead of falling
// back to the provider default. For embedded this is the endpoint ID (or the
// unique label/model the parent was created with).
func CreateFollowUpTask(prompt string, sheepID, projectID, handoffDepth int, model string) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	create := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(task.StatusPending).
		SetSheepID(sheepID).
		SetPriority(1).
		SetHandoffDepth(handoffDepth)
	if projectID > 0 {
		create = create.SetProjectID(projectID)
	}
	if model != "" {
		create = create.SetModel(model)
	}

	t, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create follow-up task: %w", err)
	}

	return t, nil
}

// GetTask returns a task by ID.
func GetTask(id int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.ID(id)).
		WithSheep().
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("task #%d not found", id)
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// ListTasks returns all tasks ordered by creation time (newest first).
func ListTasks(limit int) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	query := client.Task.Query().
		WithSheep().
		WithProject().
		Order(ent.Desc(task.FieldCreatedAt))

	if limit > 0 {
		query = query.Limit(limit)
	}

	tasks, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksByStatus returns tasks with the given status.
func ListTasksByStatus(status task.Status) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(task.StatusEQ(status)).
		WithSheep().
		WithProject().
		Order(ent.Asc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksBySheep returns tasks for the given sheep.
func ListTasksBySheep(sheepName string) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		WithSheep().
		WithProject().
		Where(task.HasSheepWith(sheep.Name(sheepName))).
		Order(ent.Desc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ListTasksByProject returns tasks for the given project.
//
// Only the columns history listings render are selected: the task row carries
// the full output log, and loading it for every task made `shepherd history`
// and the get_history tool read hundreds of megabytes.
func ListTasksByProject(projectName string) ([]*ent.Task, error) {
	ctx := context.Background()
	client := db.ReadClient()

	tasks, err := client.Task.Query().
		Select(
			task.FieldID,
			task.FieldStatus,
			task.FieldPrompt,
			task.FieldSummary,
			task.FieldCreatedAt,
		).
		WithSheep(func(q *ent.SheepQuery) { q.Select(sheep.FieldName) }).
		Where(task.HasProjectWith(project.Name(projectName))).
		Order(ent.Desc(task.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	return tasks, nil
}

// ShepherdTaskFilter narrows the shepherd (목자) task list (#623). Query is a
// substring matched against prompt OR summary — the same pair of fields and
// the same Or() shape GET /api/tasks uses (handlers_task.go), so search feels
// identical across screens. Limit/Offset drive pagination; zero Limit means
// "no cap" (the wrapper ListShepherdTasks relies on that).
type ShepherdTaskFilter struct {
	Kind   string // "" = every kind
	Status string // "" = every status
	Query  string // "" = no search
	Limit  int
	Offset int
}

// shepherdTaskBase builds the shared filtered query for list + count so the
// two never drift apart (#623: the response carries `total` for the same
// filter). Select/Order/Offset/Limit live in the list path — ent's COUNT
// chokes on a selected-column list, and GET /api/tasks counts before
// selecting too.
func shepherdTaskBase(f ShepherdTaskFilter) *ent.TaskQuery {
	client := db.ReadClient()

	query := client.Task.Query().Where(task.ShepherdKindNEQ(""))

	if f.Kind != "" {
		query = query.Where(task.ShepherdKindEQ(f.Kind))
	}
	if f.Status != "" {
		query = query.Where(task.StatusEQ(task.Status(f.Status)))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		query = query.Where(
			task.Or(
				task.PromptContains(q),
				task.SummaryContains(q),
			),
		)
	}
	return query
}

// ListShepherdTasksFiltered returns shepherd tasks (#594: shepherd_kind is
// non-empty) newest first, with offset pagination and prompt/summary search
// (#623). Only the columns the shepherd console renders are selected: the
// task row carries the full output log, and loading it for every row made
// list queries read hundreds of megabytes (same reasoning as
// ListTasksByProject).
func ListShepherdTasksFiltered(f ShepherdTaskFilter) ([]*ent.Task, error) {
	ctx := context.Background()

	sel := shepherdTaskBase(f).Select(
		task.FieldID,
		task.FieldStatus,
		task.FieldPrompt,
		task.FieldSummary,
		task.FieldError,
		task.FieldShepherdKind,
		task.FieldModel,
		task.FieldCostUsd,
		task.FieldCreatedAt,
		task.FieldStartedAt,
		task.FieldCompletedAt,
	).Order(ent.Desc(task.FieldCreatedAt))

	if f.Offset > 0 {
		sel = sel.Offset(f.Offset)
	}
	if f.Limit > 0 {
		sel = sel.Limit(f.Limit)
	}

	tasks, err := sel.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list shepherd tasks: %w", err)
	}

	return tasks, nil
}

// CountShepherdTasks counts the rows ListShepherdTasksFiltered would return
// for the same filter without Limit/Offset (the `total` field of the list
// response, #623).
func CountShepherdTasks(f ShepherdTaskFilter) (int, error) {
	ctx := context.Background()

	total, err := shepherdTaskBase(f).Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count shepherd tasks: %w", err)
	}
	return total, nil
}

// ListShepherdTasks is the thin pre-#623 wrapper (kind/status/limit, no
// offset or search) kept so older callers keep compiling.
func ListShepherdTasks(kind, status string, limit int) ([]*ent.Task, error) {
	return ListShepherdTasksFiltered(ShepherdTaskFilter{Kind: kind, Status: status, Limit: limit})
}

// ShepherdTodayStats counts shepherd (목자) tasks created today and sums their
// cost (#598, the /shepherd status card). "Today" is local midnight in the
// daemon's timezone — the same window the dashboard's Today tile uses.
func ShepherdTodayStats() (int, float64, error) {
	ctx := context.Background()
	client := db.ReadClient()

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var rows []struct {
		Count int     `json:"count"`
		Cost  float64 `json:"cost"`
	}
	if err := client.Task.Query().
		Where(
			task.ShepherdKindNEQ(""),
			task.CreatedAtGTE(todayStart),
		).
		Aggregate(ent.Count(), ent.As(ent.Sum(task.FieldCostUsd), "cost")).
		Scan(ctx, &rows); err != nil {
		return 0, 0, fmt.Errorf("failed to aggregate shepherd tasks: %w", err)
	}
	if len(rows) == 0 {
		return 0, 0, nil
	}
	return rows[0].Count, rows[0].Cost, nil
}

// GetPendingTask returns the next pending task to run. Higher priority first,
// then oldest-created (FIFO) within the same priority. Context-overflow handoff
// follow-ups are created with priority 1 so they run ahead of ordinary pending
// tasks (priority 0), keeping a handoff chain A→A'→A” contiguous instead of
// being pushed to the back of the queue.
func GetPendingTask() (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.StatusEQ(task.StatusPending)).
		WithSheep().
		WithProject().
		Order(ent.Desc(task.FieldPriority), ent.Asc(task.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending tasks
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// GetPendingTaskBySheep returns the next pending task for the given sheep.
// Higher priority first, then oldest-created (FIFO) within the same priority —
// so a handoff follow-up (priority 1) runs before queued ordinary tasks.
func GetPendingTaskBySheep(sheepID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(
			task.StatusEQ(task.StatusPending),
			task.HasSheepWith(sheep.ID(sheepID)),
		).
		WithSheep().
		WithProject().
		Order(ent.Desc(task.FieldPriority), ent.Asc(task.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending tasks
		}
		return nil, fmt.Errorf("failed to query task: %w", err)
	}

	return t, nil
}

// GetPendingShepherdTaskBySheep returns the next pending shepherd (목자) task
// for the given sheep — only tasks with a non-empty shepherd_kind (#594).
// Legacy project-less manager tasks (kind empty, e.g. an old MCP-created
// manager task) are deliberately NOT dispatched: they could never run before
// this issue either (no project → ExecuteInteractive failed), and letting the
// dispatcher hand them the workspace would silently change their fate.
// Higher priority first, then FIFO within the same priority.
func GetPendingShepherdTaskBySheep(sheepID int) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(
			task.StatusEQ(task.StatusPending),
			task.HasSheepWith(sheep.ID(sheepID)),
			task.ShepherdKindNEQ(""),
		).
		WithSheep().
		WithProject().
		Order(ent.Desc(task.FieldPriority), ent.Asc(task.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // No pending shepherd tasks
		}
		return nil, fmt.Errorf("failed to query shepherd task: %w", err)
	}

	return t, nil
}

// CountPendingTasksBySheep returns the number of pending tasks for the given sheep.
func CountPendingTasksBySheep(sheepID int) (int, error) {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Task.Query().
		Where(
			task.StatusEQ(task.StatusPending),
			task.HasSheepWith(sheep.ID(sheepID)),
		).
		Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to count tasks: %w", err)
	}

	return count, nil
}

// StartTask marks a task as running.
func StartTask(id int) error {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusRunning).
		SetStartedAt(time.Now()).
		SetOwnerPid(os.Getpid()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to update task status: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	return nil
}

// CompleteTask marks a task as completed with summary and modified files.
func CompleteTask(id int, summary string, filesModified []string) error {
	return CompleteTaskWithOutput(id, summary, filesModified, nil)
}

// CompleteTaskWithCost marks a task as completed with summary, modified files, output, and cost.
func CompleteTaskWithCost(id int, summary string, filesModified []string, output []string, costUSD float64) error {
	return CompleteTaskWithTokens(id, summary, filesModified, output, costUSD, 0, 0)
}

// CompleteTaskWithTokens marks a task as completed with full token usage data.
func CompleteTaskWithTokens(id int, summary string, filesModified []string, output []string, costUSD float64, promptTokens, completionTokens int64) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusCompleted).
		SetSummary(summary).
		SetFilesModified(filesModified).
		SetCompletedAt(time.Now())

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
	}
	if costUSD > 0 {
		updateQuery = updateQuery.SetCostUsd(costUSD)
	}
	if promptTokens > 0 {
		updateQuery = updateQuery.SetPromptTokens(promptTokens)
	}
	if completionTokens > 0 {
		updateQuery = updateQuery.SetCompletionTokens(completionTokens)
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to complete task: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	// Auto-update linked issue status to testing
	updateIssueStatusOnTaskComplete(id, entIssue.StatusTesting)

	notifyTaskFinished(id)

	return nil
}

// CompleteTaskWithOutput marks a task as completed with summary, modified files, and output.
func CompleteTaskWithOutput(id int, summary string, filesModified []string, output []string) error {
	return CompleteTaskWithCost(id, summary, filesModified, output, 0)
}

// FailTask marks a task as failed with an error message.
func FailTask(id int, errMsg string) error {
	return FailTaskWithOutput(id, errMsg, nil)
}

// FailTaskWithOutput marks a task as failed with an error message and output.
func FailTaskWithOutput(id int, errMsg string, output []string) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusFailed).
		SetError(errMsg).
		SetCompletedAt(time.Now())

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
		// Save the last few lines of output as summary (viewable even on interruption)
		summary := buildSummaryFromOutput(output)
		if summary != "" {
			updateQuery = updateQuery.SetSummary(summary)
		}
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to mark task as failed: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	// Auto-update linked issue status to failed
	updateIssueStatusOnTaskComplete(id, entIssue.StatusFailed)

	notifyTaskFinished(id)

	return nil
}

// StopTaskWithOutput marks a task as stopped (user cancel or question detected).
func StopTaskWithOutput(id int, reason string, output []string) error {
	ctx := context.Background()
	client := db.Client()

	updateQuery := client.Task.Update().
		Where(task.ID(id)).
		SetStatus(task.StatusStopped).
		SetError(reason).
		SetCompletedAt(time.Now())

	if len(output) > 0 {
		updateQuery = updateQuery.SetOutput(output)
		summary := buildSummaryFromOutput(output)
		if summary != "" {
			updateQuery = updateQuery.SetSummary(summary)
		}
	}

	count, err := updateQuery.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to mark task as stopped: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}

	// Auto-update linked issue status to failed (stopped tasks also fail the issue)
	updateIssueStatusOnTaskComplete(id, entIssue.StatusFailed)

	notifyTaskFinished(id)

	return nil
}

// StopTaskByID is the common "stop a running task" entry point shared by
// POST /api/tasks/:id/stop (web), `shepherd task stop` (CLI), and the MCP
// task_stop tool (#601). It loads the task, kills the provider process through
// worker.StopTask, then records the stopped row with the captured output —
// exactly what handleStopTask did inline before #601. Callers outside queue
// cannot reach worker.StopTask directly (mcp imports neither queue nor worker;
// manager imports queue), so routing this through queue is the single path.
func StopTaskByID(id int, reason string) error {
	t, err := GetTask(id)
	if err != nil {
		return err // already "task #N not found" shaped
	}
	if t.Status != task.StatusRunning {
		return fmt.Errorf("task #%d is not running (status: %s)", id, t.Status)
	}
	if t.Edges.Sheep == nil {
		return fmt.Errorf("task #%d has no assigned sheep", id)
	}

	result, err := worker.StopTask(t.Edges.Sheep.Name)
	if err != nil {
		return fmt.Errorf("stop provider process: %w", err)
	}

	return StopTaskWithOutput(id, reason, result.OutputLines)
}

// SetTaskCheckpointStart records the start checkpoint of a task. dir is the
// absolute directory the snapshot was taken in, sha is the snapshot commit
// (empty when the snapshot was skipped), and errMsg explains a skip/failure.
func SetTaskCheckpointStart(id int, dir, sha, errMsg string) error {
	ctx := context.Background()
	client := db.Client()

	update := client.Task.Update().
		Where(task.ID(id)).
		SetCheckpointDir(dir).
		SetCheckpointError(errMsg)
	if sha != "" {
		update = update.SetCheckpointStart(sha)
	}

	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save task checkpoint start: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}
	return nil
}

// SetTaskCheckpointEnd records the end checkpoint of a task. A non-empty errMsg
// is appended to any existing checkpoint error so start and end failures are
// both preserved.
func SetTaskCheckpointEnd(id int, sha, errMsg string) error {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to load task for checkpoint end: %w", err)
	}

	update := client.Task.Update().Where(task.ID(id))
	if sha != "" {
		update = update.SetCheckpointEnd(sha)
	}
	if errMsg != "" {
		update = update.SetCheckpointError(appendCheckpointError(t.CheckpointError, errMsg))
	}

	count, err := update.Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to save task checkpoint end: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("task #%d not found", id)
	}
	return nil
}

// appendCheckpointError joins checkpoint messages without dropping earlier ones.
func appendCheckpointError(existing, message string) string {
	if existing == "" {
		return message
	}
	if message == "" {
		return existing
	}
	return existing + "; " + message
}

func issueHasCompletedAt(i *ent.Issue) bool {
	return i != nil && i.CompletedAt != nil && !i.CompletedAt.IsZero()
}

// updateIssueStatusOnTaskComplete updates the linked issue status when a task finishes.
// If the task is linked to an issue and the issue is not already done/failed (user-finalized),
// it transitions the issue to the given status.
func updateIssueStatusOnTaskComplete(taskID int, newIssueStatus entIssue.Status) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(task.ID(taskID), task.HasIssue()).
		WithIssue().
		All(ctx)
	if err != nil || len(tasks) == 0 {
		return
	}

	t := tasks[0]
	if t.Edges.Issue == nil {
		return
	}

	i := t.Edges.Issue
	// Don't override user-finalized issues (done or failed with completed_at set)
	if i.Status == entIssue.StatusDone || (i.Status == entIssue.StatusFailed && issueHasCompletedAt(i)) {
		return
	}

	update := client.Issue.UpdateOne(i).SetStatus(newIssueStatus)
	if newIssueStatus == entIssue.StatusFailed && !issueHasCompletedAt(i) {
		update = update.SetCompletedAt(time.Now())
	}
	if _, err := update.Save(ctx); err != nil {
		fmt.Printf("[queue] failed to update issue #%d status: %v\n", i.ID, err)
	}
}

// buildSummaryFromOutput builds a summary from output lines (last meaningful lines).
func buildSummaryFromOutput(output []string) string {
	// Collect last meaningful lines (excluding empty lines)
	var meaningful []string
	for _, line := range output {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && trimmed != "✅ Task complete" && trimmed != "🔧 Running tool..." && trimmed != "✅ Tool complete" {
			meaningful = append(meaningful, trimmed)
		}
	}
	if len(meaningful) == 0 {
		return ""
	}
	// Last 5 lines only
	start := len(meaningful) - 5
	if start < 0 {
		start = 0
	}
	return strings.Join(meaningful[start:], "\n")
}

// CountByStatus returns task counts by status.
func CountByStatus() (map[task.Status]int, error) {
	ctx := context.Background()
	client := db.ReadClient()

	result := make(map[task.Status]int)

	// One grouped query instead of five COUNT scans: the tasks table carries
	// the full output log, so scanning it repeatedly was the main cost of
	// /api/system/status and /api/dashboard.
	var rows []struct {
		Status task.Status `json:"status"`
		Count  int         `json:"count"`
	}
	if err := client.Task.Query().
		GroupBy(task.FieldStatus).
		Aggregate(ent.Count()).
		Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("failed to count tasks: %w", err)
	}

	for _, status := range []task.Status{
		task.StatusPending,
		task.StatusRunning,
		task.StatusCompleted,
		task.StatusFailed,
		task.StatusStopped,
	} {
		result[status] = 0
	}
	for _, row := range rows {
		result[row.Status] = row.Count
	}

	return result, nil
}

// CountRunningByGroup returns the number of running tasks grouped by their
// canonical (provider+model) concurrency-group key (see groupKey). The model is
// taken from each task's persisted per-task override, falling back to the global
// per-provider default — so several local OpenCode systems are counted as
// distinct groups. Tasks without a sheep edge are skipped. Used by the processor
// to enforce per-group concurrency limits in addition to the global ceiling.
func CountRunningByGroup() (map[string]int, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(task.StatusEQ(task.StatusRunning)).
		WithSheep().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count running tasks by group: %w", err)
	}

	result := make(map[string]int)
	for _, t := range tasks {
		if t.Edges.Sheep == nil {
			continue
		}
		result[groupKey(string(t.Edges.Sheep.Provider), t.Model)]++
	}
	return result, nil
}

// StatusToKorean converts task status to a display string.
func StatusToKorean(status task.Status) string {
	switch status {
	case task.StatusPending:
		return "pending"
	case task.StatusRunning:
		return "running"
	case task.StatusCompleted:
		return "completed"
	case task.StatusFailed:
		return "failed"
	case task.StatusStopped:
		return "stopped"
	default:
		return string(status)
	}
}

// RecoverStuckTasks marks running tasks as failed after abnormal termination.
// This should be called on startup.
//
// A "running" row is only failed when its owning process is actually gone. The
// owner_pid recorded by StartTask is checked against the live process table:
//   - owner is a *different*, still-alive process (e.g. the daemon, while a
//     separate CLI command runs recovery) → leave it untouched. Failing it here
//     is the false-"interrupted" bug that corrupted the queue.
//   - owner is this same process (graceful shutdown) or a dead/unknown PID →
//     genuinely orphaned, recover it.
//
// Linked issues are moved to failed so they are not left stuck at in_progress
// after a daemon crash (cascade cancel / restart path).
func RecoverStuckTasks() (int, error) {
	ctx := context.Background()
	client := db.Client()
	now := time.Now()
	selfPID := os.Getpid()

	running, err := client.Task.Query().
		Where(task.StatusEQ(task.StatusRunning)).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query running tasks: %w", err)
	}

	runningCount := 0
	var recoveredIDs []int
	for _, t := range running {
		// Skip tasks still owned by a live, different process.
		if t.OwnerPid != 0 && t.OwnerPid != selfPID && daemon.IsPIDAlive(t.OwnerPid) {
			continue
		}
		update := client.Task.UpdateOneID(t.ID).
			SetStatus(task.StatusFailed).
			SetError("interrupted due to abnormal termination").
			SetCompletedAt(now)
		// A start snapshot without an end snapshot means the daemon died before
		// finishCheckpoint could run. Record why so the diff UI does not show a
		// silently incomplete checkpoint.
		if t.CheckpointStart != "" && t.CheckpointEnd == "" {
			update = update.SetCheckpointError(
				appendCheckpointError(t.CheckpointError, "no end snapshot (daemon restarted)"))
		}
		_, err := update.Save(ctx)
		if err != nil {
			return runningCount, fmt.Errorf("failed to recover task #%d: %w", t.ID, err)
		}
		recoveredIDs = append(recoveredIDs, t.ID)
		runningCount++
	}
	syncIssuesFailedForTasks(recoveredIDs)

	return runningCount, nil
}

// CancelStalePendingTasks cancels pending tasks that belong to a previous session.
// This should only be called by the daemon on startup — not by CLI commands —
// because CLI callers would otherwise cancel their own legitimately queued tasks.
func CancelStalePendingTasks() (int, error) {
	ctx := context.Background()
	client := db.Client()
	now := time.Now()

	// Only cancel pending tasks that were created before this process started.
	// Tasks created in the current session (e.g. by a CLI command running in the
	// same process) must not be touched. We use a small grace window: tasks
	// created within the last 2 seconds are considered "current session".
	cutoff := now.Add(-2 * time.Second)

	ids, err := taskIDsWhere(ctx, client,
		task.StatusEQ(task.StatusPending),
		task.CreatedAtLT(cutoff),
	)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	count, err := client.Task.Update().
		Where(task.IDIn(ids...)).
		SetStatus(task.StatusStopped).
		SetError("cancelled: stale task from previous session").
		SetCompletedAt(now).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel stale pending tasks: %w", err)
	}
	syncIssuesFailedForTasks(ids)

	return count, nil
}

// CancelPendingTask cancels one pending task. The row becomes stopped with
// error "cancelled by user" so it matches queue clear / CancelPendingTasks.
// The update is predicated on status=pending so a concurrent StartTask
// wins and this write does not overwrite a running row.
func CancelPendingTask(id int) error {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Task.Update().
		Where(task.ID(id), task.StatusEQ(task.StatusPending)).
		SetStatus(task.StatusStopped).
		SetError("cancelled by user").
		SetCompletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to cancel task: %w", err)
	}
	if count == 0 {
		t, getErr := GetTask(id)
		if getErr != nil {
			return getErr
		}
		return fmt.Errorf("task #%d is not pending (status: %s)", id, t.Status)
	}

	updateIssueStatusOnTaskComplete(id, entIssue.StatusFailed)

	notifyTaskFinished(id)
	return nil
}

// DeleteTask removes a task row. The linked issue is synced to failed first —
// updateIssueStatusOnTaskComplete re-queries the task row, so it must run
// before the delete (afterwards the query would match 0 rows).
func DeleteTask(id int) error {
	ctx := context.Background()
	client := db.Client()

	t, err := client.Task.Query().
		Where(task.ID(id)).
		WithIssue().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("task #%d not found", id)
		}
		return fmt.Errorf("failed to query task: %w", err)
	}

	if t.Edges.Issue != nil {
		updateIssueStatusOnTaskComplete(id, entIssue.StatusFailed)
		// Hook before the row disappears — notifyTaskFinished re-queries it.
		notifyTaskFinished(id)
	}

	// Remove the task's checkpoint refs before the row disappears. A failure
	// here must not block deletion; the retention pruner will retry later.
	if t.CheckpointDir != "" {
		if err := checkpoint.DeleteTaskRefs(ctx, t.CheckpointDir, id); err != nil {
			log.Printf("[queue] task #%d delete checkpoint refs: %v", id, err)
		}
	}

	if _, err := client.Task.Delete().
		Where(task.ID(id)).
		Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	return nil
}

// CancelPendingTasks marks all pending tasks as stopped (cancelled).
func CancelPendingTasks() (int, error) {
	ctx := context.Background()
	client := db.Client()

	ids, err := taskIDsWhere(ctx, client, task.StatusEQ(task.StatusPending))
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	count, err := client.Task.Update().
		Where(task.IDIn(ids...)).
		SetStatus(task.StatusStopped).
		SetError("cancelled by user").
		SetCompletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel pending tasks: %w", err)
	}
	syncIssuesFailedForTasks(ids)

	return count, nil
}

// CancelRunningTasks marks all running tasks as stopped (cancelled).
func CancelRunningTasks() (int, error) {
	ctx := context.Background()
	client := db.Client()

	ids, err := taskIDsWhere(ctx, client, task.StatusEQ(task.StatusRunning))
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}

	count, err := client.Task.Update().
		Where(task.IDIn(ids...)).
		SetStatus(task.StatusStopped).
		SetError("cancelled by user").
		SetCompletedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cancel running tasks: %w", err)
	}
	syncIssuesFailedForTasks(ids)

	return count, nil
}

// taskIDsWhere returns IDs of tasks matching the given predicates.
func taskIDsWhere(ctx context.Context, client *ent.Client, preds ...predicate.Task) ([]int, error) {
	rows, err := client.Task.Query().Where(preds...).Select(task.FieldID).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query task ids: %w", err)
	}
	ids := make([]int, 0, len(rows))
	for _, t := range rows {
		ids = append(ids, t.ID)
	}
	return ids, nil
}

// syncIssuesFailedForTasks moves linked issues to failed after bulk task
// cancel/recover. Bulk task updates skip StopTaskWithOutput/FailTask paths,
// so without this issues stay stuck at in_progress and cannot be re-run cleanly.
// It is also the wake-hook entry for bulk paths (#597): RecoverStuckTasks
// failures wake the shepherd, while user-cancel markers keep it asleep via
// isUserStopReason.
func syncIssuesFailedForTasks(taskIDs []int) {
	for _, id := range taskIDs {
		updateIssueStatusOnTaskComplete(id, entIssue.StatusFailed)
		notifyTaskFinished(id)
	}
}

// GetProjectCostStats returns total cost for a project.
func GetProjectCostStats(projectName string) (float64, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(
			task.HasProjectWith(project.Name(projectName)),
			task.StatusEQ(task.StatusCompleted),
			task.CostUsdGT(0),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query cost: %w", err)
	}

	var total float64
	for _, t := range tasks {
		total += t.CostUsd
	}
	return total, nil
}

// GetTotalCost returns total cost across all tasks.
func GetTotalCost() (float64, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(
			task.CostUsdGT(0),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query total cost: %w", err)
	}

	var total float64
	for _, t := range tasks {
		total += t.CostUsd
	}
	return total, nil
}

// IsRateLimitError checks if an error message indicates a rate limit.
func IsRateLimitError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "hit your limit") ||
		strings.Contains(lower, "hit your session limit") ||
		strings.Contains(lower, "session limit") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "limit exceeded")
}
