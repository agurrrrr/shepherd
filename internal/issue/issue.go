// Package issue provides CRUD and execute operations for shepherd project issues.
// Issues are the built-in tracker (design / feature / bug) linked to tasks via execute.
// Parent/child hierarchy: children hold parent_id; executing a parent enqueues
// incomplete children first (FIFO), then the parent itself.
package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/predicate"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Valid issue types and statuses (mirror ent/schema and REST API).
var (
	ValidTypes    = []string{"design", "feature", "bug"}
	ValidStatuses = []string{"todo", "in_progress", "testing", "failed", "done"}
)

// CreateInput is the payload for creating an issue.
type CreateInput struct {
	Project  string // project name (required)
	Title    string // required
	Type     string // design | feature | bug (default: feature)
	Body     string // optional description
	Goal     string // optional success criteria
	ParentID *int   // optional parent issue ID (same project)
}

// UpdateInput holds optional fields for partial update. Nil pointer = leave unchanged.
// ParentID: nil = leave; pointer to 0 = clear parent; pointer to positive = set parent.
type UpdateInput struct {
	Title    *string
	Type     *string
	Body     *string
	Goal     *string
	Status   *string
	ParentID *int

	// Shepherd (목자) fields (#597). Nil = leave unchanged.
	ShepherdWatch      *bool
	ShepherdLastReview *string
	// ShepherdRounds resets/sets the review round counter (#599): the board's
	// "회차 초기화" button sends 0 so wake.go's round cap opens again.
	ShepherdRounds *int
}

// ListFilter controls listing / filtering.
//
// Single-project mode is the original shape: set Project and leave the scope
// fields below empty. Cross-project mode (#596) is selected by AllProjects or
// a non-empty Projects slice — then Project must be empty, and every returned
// item carries its project through Edges.Project (always eager-loaded).
type ListFilter struct {
	Project  string
	Status   string // empty = all
	Type     string // empty = all
	Query    string // title substring
	ParentID *int   // nil = all; pointer to 0 = roots only; positive = children of that parent
	Page     int    // 1-based, default 1
	Limit    int    // default 20, max 100
	SortAsc  bool   // default newest-first

	// Cross-project scope (#596).
	AllProjects   bool     // true = every project (hidden ones follow IncludeHidden)
	Projects      []string // restrict to these project names (unknown names are ignored)
	IncludeHidden bool     // false (default) drops issues of hidden projects in cross-project mode
	OmitBody      bool     // drop `body` from items so wide-list payloads stay small
	SortBy        string   // "" = created_at (default); "updated" = updated_at first (board ordering)

	// WatchedOnly (#599) keeps only shepherd-watched issues — the board's
	// "감시 중인 이슈만" filter. nil = no filter.
	Watched *bool
}

// LastTask is the most recent task linked to an issue (created_at order).
// The API adds these fields to list items so boards can show task state without
// a per-issue detail request.
type LastTask struct {
	ID     int
	Status string
}

// StatusCounts holds issue counts keyed by status. Every ValidStatuses key is
// present, zero included, so callers never branch on missing keys.
type StatusCounts map[string]int

// ListResult is a page of issues plus pagination metadata.
// LastTasks and StatusCounts are filled only in cross-project mode (#596);
// single-project results keep their original shape.
type ListResult struct {
	Items       []*ent.Issue
	TaskCounts  map[int]int // issue ID → linked task count
	LastTasks   map[int]*LastTask
	StatusCount StatusCounts
	Total       int
	Page        int
	Limit       int
	TotalPages  int
}

// crossProject reports whether the filter asks for more than one project.
func (f ListFilter) crossProject() bool {
	return f.AllProjects || len(f.Projects) > 0
}

// scopePredicate restricts issues by project edge for cross-project mode.
// Unknown names in Projects simply match nothing, which is the expected
// behaviour for a filter list. Hidden projects stay out unless IncludeHidden:
// with an explicit name list, IncludeHidden only lifts the hidden guard — it
// must not narrow the list to hidden projects only.
func (f ListFilter) scopePredicate() predicate.Issue {
	if f.AllProjects {
		if f.IncludeHidden {
			return entIssue.HasProject()
		}
		return entIssue.HasProjectWith(entProject.Hidden(false))
	}
	if f.IncludeHidden {
		return entIssue.HasProjectWith(entProject.NameIn(f.Projects...))
	}
	return entIssue.HasProjectWith(entProject.NameIn(f.Projects...), entProject.Hidden(false))
}

// ExecuteInput controls issue → task enqueue.
type ExecuteInput struct {
	Project   string
	IssueID   int
	SheepName string // optional; falls back to project-assigned sheep
	Model     string // optional per-task model override

	// Watch marks the enqueued issues (and every cascaded child) as shepherd-
	// watched (#597): the 목자 wants to review what this task does. The daemon
	// also forces Watch=true when the caller is a manager-sheep task
	// (manager.IsShepherdCaller), so a shepherd that forgets the flag is still
	// tracked automatically.
	Watch bool
}

// ExecuteResult is returned after enqueueing task(s) for an issue.
// When the issue has incomplete children, those are enqueued first (FIFO),
// then the parent. TaskID / IssueID refer to the primary (requested) issue.
type ExecuteResult struct {
	TaskID    int   // primary issue's task (last enqueued when cascading)
	TaskIDs   []int // all task IDs in enqueue order (children… then primary)
	IssueIDs  []int // corresponding issue IDs in the same order
	SheepName string
	IssueID   int
}

// Create creates a new issue under a project.
func Create(in CreateInput) (*ent.Issue, error) {
	if strings.TrimSpace(in.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}

	issueType := in.Type
	if issueType == "" {
		issueType = "feature"
	}
	if err := validateType(issueType); err != nil {
		return nil, err
	}

	ctx := context.Background()
	client := db.Client()

	project, err := client.Project.Query().Where(entProject.Name(in.Project)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("project %q not found", in.Project)
		}
		return nil, fmt.Errorf("failed to query project: %w", err)
	}

	if in.ParentID != nil {
		if err := validateParent(ctx, client, in.Project, *in.ParentID, 0); err != nil {
			return nil, err
		}
	}

	create := client.Issue.Create().
		SetTitle(in.Title).
		SetType(entIssue.Type(issueType)).
		SetBody(in.Body).
		SetGoal(in.Goal).
		SetProject(project)
	if in.ParentID != nil && *in.ParentID > 0 {
		create = create.SetParentID(*in.ParentID)
	}

	issue, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}
	return issue, nil
}

// List returns a filtered, paginated list of issues.
//
// Single-project mode (f.Project set) keeps the original result shape and the
// original error wording. Cross-project mode (f.AllProjects or f.Projects)
// searches every in-scope project at once and additionally fills LastTasks and
// StatusCount for boards (#596). Items always carry Edges.Project so callers
// can label rows with the project name.
func List(f ListFilter) (*ListResult, error) {
	if f.Project != "" && f.crossProject() {
		return nil, fmt.Errorf("project cannot be combined with all_projects/projects")
	}
	if !f.crossProject() && strings.TrimSpace(f.Project) == "" {
		return nil, fmt.Errorf("project is required")
	}
	if f.Page < 1 {
		f.Page = 1
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}

	ctx := context.Background()
	client := db.Client()
	if client == nil {
		return nil, fmt.Errorf("database is not initialized")
	}

	if !f.crossProject() {
		// Ensure project exists (clearer error than empty list).
		exists, err := client.Project.Query().Where(entProject.Name(f.Project)).Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to query project: %w", err)
		}
		if !exists {
			return nil, fmt.Errorf("project %q not found", f.Project)
		}
	}

	query, err := buildListQuery(ctx, client, f, true)
	if err != nil {
		return nil, err
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count issues: %w", err)
	}

	issues, err := query.
		Order(orderFunc(f)...).
		Offset((f.Page - 1) * f.Limit).
		Limit(f.Limit).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	if f.OmitBody {
		for _, iss := range issues {
			iss.Body = ""
		}
	}

	counts, lastTasks, err := taskStatsFor(ctx, client, issues)
	if err != nil {
		return nil, err
	}

	result := &ListResult{
		Items:      issues,
		TaskCounts: counts,
		Total:      total,
		Page:       f.Page,
		Limit:      f.Limit,
		TotalPages: totalPages(total, f.Limit),
	}

	if f.crossProject() {
		result.LastTasks = lastTasks
		countsNoStatus, err := buildListQuery(ctx, client, f, false)
		if err != nil {
			return nil, err
		}
		statusCount, err := statusCountsFor(ctx, countsNoStatus)
		if err != nil {
			return nil, err
		}
		result.StatusCount = statusCount
	}

	return result, nil
}

// buildListQuery applies the scope + filter predicates. applyStatus is false
// for the status-count clone, which must count every status (issue #596: "개수는
// 상태 필터를 뺀 나머지 필터 기준").
func buildListQuery(ctx context.Context, client *ent.Client, f ListFilter, applyStatus bool) (*ent.IssueQuery, error) {
	var query *ent.IssueQuery
	if f.crossProject() {
		query = client.Issue.Query().Where(f.scopePredicate())
	} else {
		query = client.Issue.Query().Where(entIssue.HasProjectWith(entProject.Name(f.Project)))
	}

	if applyStatus && f.Status != "" {
		if err := validateStatus(f.Status); err != nil {
			return nil, err
		}
		query = query.Where(entIssue.StatusEQ(entIssue.Status(f.Status)))
	}
	if f.Type != "" {
		if err := validateType(f.Type); err != nil {
			return nil, err
		}
		query = query.Where(entIssue.TypeEQ(entIssue.Type(f.Type)))
	}
	if f.Query != "" {
		query = query.Where(entIssue.TitleContains(f.Query))
	}
	if f.ParentID != nil {
		if *f.ParentID == 0 {
			query = query.Where(entIssue.ParentIDIsNil())
		} else {
			query = query.Where(entIssue.ParentIDEQ(*f.ParentID))
		}
	}
	// Shepherd watch filter (#599): the board's "감시 중인 이슈만" toggle.
	if f.Watched != nil {
		query = query.Where(entIssue.ShepherdWatch(*f.Watched))
	}
	return query, nil
}

// orderFunc picks the sort column from SortBy and the direction from SortAsc.
// ID is the tie-breaker so paging is stable when timestamps collide (bulk
// updates share one clock tick).
func orderFunc(f ListFilter) []entIssue.OrderOption {
	column := entIssue.FieldCreatedAt
	if f.SortBy == "updated" {
		column = entIssue.FieldUpdatedAt
	}
	if f.SortAsc {
		return []entIssue.OrderOption{ent.Asc(column), ent.Asc(entIssue.FieldID)}
	}
	return []entIssue.OrderOption{ent.Desc(column), ent.Desc(entIssue.FieldID)}
}

// statusCountsFor groups a filter clone by status in one query. Missing
// statuses stay 0 so the board header always has all five columns.
func statusCountsFor(ctx context.Context, q *ent.IssueQuery) (StatusCounts, error) {
	var rows []struct {
		Status entIssue.Status `json:"status"`
		Count  int             `json:"count"`
	}
	if err := q.GroupBy(entIssue.FieldStatus).Aggregate(ent.Count()).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("failed to count issues by status: %w", err)
	}
	counts := make(StatusCounts, len(ValidStatuses))
	for _, s := range ValidStatuses {
		counts[s] = 0
	}
	for _, row := range rows {
		counts[string(row.Status)] = row.Count
	}
	return counts, nil
}

// taskStatsFor returns per-issue linked-task counts and the newest linked task,
// reading every task of the page in ONE query (the old per-issue COUNT loop was
// N+1; boards need the last task's id/status too).
//
// "issue_tasks" is the FK column of the task→issue edge. Ent exposes it as a
// selectable column (same trick internal/manager/prompt.go uses for
// "project_tasks"); scanning it avoids loading issue edges per task.
func taskStatsFor(ctx context.Context, client *ent.Client, issues []*ent.Issue) (map[int]int, map[int]*LastTask, error) {
	counts := make(map[int]int, len(issues))
	lastTasks := make(map[int]*LastTask, len(issues))
	for _, iss := range issues {
		counts[iss.ID] = 0
	}
	if len(issues) == 0 {
		return counts, lastTasks, nil
	}

	ids := make([]int, 0, len(issues))
	for _, iss := range issues {
		ids = append(ids, iss.ID)
	}

	taskQuery := client.Task.Query().
		Where(entTask.HasIssueWith(entIssue.IDIn(ids...))).
		Order(ent.Desc(entTask.FieldCreatedAt), ent.Desc(entTask.FieldID))

	var rows []struct {
		ID         int            `json:"id"`
		Status     entTask.Status `json:"status"`
		IssueTasks int            `json:"issue_tasks"`
	}
	if err := taskQuery.Select(entTask.FieldID, entTask.FieldStatus, "issue_tasks").Scan(ctx, &rows); err != nil {
		return nil, nil, fmt.Errorf("failed to count tasks: %w", err)
	}

	for _, row := range rows {
		counts[row.IssueTasks]++
		if _, seen := lastTasks[row.IssueTasks]; !seen {
			lastTasks[row.IssueTasks] = &LastTask{ID: row.ID, Status: string(row.Status)}
		}
	}
	return counts, lastTasks, nil
}

// totalPages is pagination metadata shared by both list modes.
func totalPages(total, limit int) int {
	if total <= 0 {
		return 0
	}
	return (total + limit - 1) / limit
}

// Get returns a single issue with linked tasks, parent, and children (oldest first),
// scoped to project.
func Get(projectName string, id int) (*ent.Issue, error) {
	if strings.TrimSpace(projectName) == "" {
		return nil, fmt.Errorf("project is required")
	}
	if id <= 0 {
		return nil, fmt.Errorf("invalid issue id")
	}

	ctx := context.Background()
	client := db.Client()

	iss, err := client.Issue.Query().
		Where(entIssue.ID(id), entIssue.HasProjectWith(entProject.Name(projectName))).
		WithProject(func(pq *ent.ProjectQuery) {
			pq.WithSheep()
		}).
		WithTasks(func(tq *ent.TaskQuery) {
			tq.Order(ent.Desc(entTask.FieldCreatedAt))
		}).
		WithParent().
		WithChildren(func(cq *ent.IssueQuery) {
			// Stable order for checklist + cascade enqueue (oldest first).
			cq.Order(ent.Asc(entIssue.FieldID))
		}).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("issue #%d not found in project %q", id, projectName)
		}
		return nil, fmt.Errorf("failed to get issue: %w", err)
	}
	return iss, nil
}

// Update partially updates an issue.
func Update(projectName string, id int, in UpdateInput) (*ent.Issue, error) {
	iss, err := Get(projectName, id)
	if err != nil {
		return nil, err
	}

	if in.Title == nil && in.Type == nil && in.Body == nil && in.Goal == nil && in.Status == nil && in.ParentID == nil &&
		in.ShepherdWatch == nil && in.ShepherdLastReview == nil && in.ShepherdRounds == nil {
		return nil, fmt.Errorf("nothing to update: provide at least one of title, type, body, goal, status, parent_id, shepherd_watch, shepherd_last_review, shepherd_rounds")
	}

	ctx := context.Background()
	client := db.Client()
	update := client.Issue.UpdateOneID(iss.ID)

	if in.Title != nil {
		if strings.TrimSpace(*in.Title) == "" {
			return nil, fmt.Errorf("title cannot be empty")
		}
		update = update.SetTitle(*in.Title)
	}
	if in.Type != nil {
		if err := validateType(*in.Type); err != nil {
			return nil, err
		}
		update = update.SetType(entIssue.Type(*in.Type))
	}
	if in.Body != nil {
		update = update.SetBody(*in.Body)
	}
	if in.Goal != nil {
		update = update.SetGoal(*in.Goal)
	}
	if in.Status != nil {
		if err := validateStatus(*in.Status); err != nil {
			return nil, err
		}
		update = update.SetStatus(entIssue.Status(*in.Status))
		if *in.Status == "done" || *in.Status == "failed" {
			if iss.CompletedAt == nil || iss.CompletedAt.IsZero() {
				update = update.SetCompletedAt(time.Now())
			}
		}
	}
	if in.ParentID != nil {
		if *in.ParentID <= 0 {
			update = update.ClearParent()
		} else {
			if err := validateParent(ctx, client, projectName, *in.ParentID, id); err != nil {
				return nil, err
			}
			update = update.SetParentID(*in.ParentID)
		}
	}
	// Shepherd watch toggle (#597). Turning the watch on stamps watched_at when
	// it has never been set (the execute cascade sets it itself when Watch is
	// requested, so this only covers toggles from Web/API/MCP upsert).
	if in.ShepherdWatch != nil {
		update = update.SetShepherdWatch(*in.ShepherdWatch)
		if *in.ShepherdWatch && (iss.ShepherdWatchedAt == nil || iss.ShepherdWatchedAt.IsZero()) {
			update = update.SetShepherdWatchedAt(time.Now())
		}
	}
	if in.ShepherdLastReview != nil {
		update = update.SetShepherdLastReview(*in.ShepherdLastReview)
	}
	// Review round reset (#599). Negative values are rejected; 0 re-opens the
	// wake.go round cap so the shepherd can be woken for this issue again.
	if in.ShepherdRounds != nil {
		if *in.ShepherdRounds < 0 {
			return nil, fmt.Errorf("shepherd_rounds must be >= 0")
		}
		update = update.SetShepherdRounds(*in.ShepherdRounds)
	}

	updated, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update issue: %w", err)
	}
	// Re-load with edges so callers (handlers) can render children/parent.
	return Get(projectName, updated.ID)
}

// Delete removes an issue. Linked tasks are preserved (issue edge cleared).
// Children are reparented to null (become roots) so they are not deleted.
func Delete(projectName string, id int) error {
	// Scope check
	if _, err := Get(projectName, id); err != nil {
		return err
	}

	ctx := context.Background()
	client := db.Client()

	// Detach children first (FK would otherwise block or cascade depending on DB).
	_, _ = client.Issue.Update().
		Where(entIssue.ParentIDEQ(id)).
		ClearParent().
		Save(ctx)

	tasks, _ := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(id))).All(ctx)
	for _, t := range tasks {
		_, _ = client.Task.UpdateOneID(t.ID).ClearIssue().Save(ctx)
	}

	if err := client.Issue.DeleteOneID(id).Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete issue: %w", err)
	}
	return nil
}

// cascadeEnqueueDelay spaces out bulk child/parent task creation so the
// processor can claim the first task (and mark the sheep working) before the
// next pending row appears. Without this, cascade enqueue + ProcessPendingNow
// (or concurrent tickers) can double-dispatch the same sheep on two children.
const cascadeEnqueueDelay = 2 * time.Second

// Execute builds a prompt from the issue and enqueues a task for a sheep.
// If the issue has incomplete children (status != done), those are enqueued first
// in stable ID order, then the parent. Tasks are created with a short delay so
// they start sequentially rather than racing on one sheep.
// Does not require a live daemon; the processor picks up pending tasks when running.
func Execute(in ExecuteInput) (*ExecuteResult, error) {
	iss, err := Get(in.Project, in.IssueID)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	client := db.Client()

	// Resolve sheep once for the whole cascade.
	sheep, sheepName, err := resolveSheep(ctx, client, in, iss)
	if err != nil {
		return nil, err
	}
	if !config.IsProviderEnabled(string(sheep.Provider)) {
		return nil, fmt.Errorf("provider %q is disabled in settings", sheep.Provider)
	}

	projectID, err := resolveProjectID(ctx, client, in.Project, iss)
	if err != nil {
		return nil, err
	}

	// Cancel-all / daemon crash used to leave issues at in_progress while tasks
	// were stopped/failed. Reconcile before cascade so re-execute is possible.
	_ = reconcileStaleProgress(ctx, client, iss)
	for _, child := range iss.Edges.Children {
		_ = reconcileStaleProgress(ctx, client, child)
	}
	// Reload after reconcile so cascade filters see updated statuses.
	iss, err = Get(in.Project, in.IssueID)
	if err != nil {
		return nil, err
	}

	// Incomplete children first (status != done), oldest ID first — already ordered by Get.
	var toEnqueue []*ent.Issue
	for _, child := range iss.Edges.Children {
		if child.Status != entIssue.StatusDone {
			toEnqueue = append(toEnqueue, child)
		}
	}
	// Parent last.
	toEnqueue = append(toEnqueue, iss)

	result := &ExecuteResult{
		SheepName: sheepName,
		IssueID:   iss.ID,
		TaskIDs:   make([]int, 0, len(toEnqueue)),
		IssueIDs:  make([]int, 0, len(toEnqueue)),
	}

	enqueued := 0
	skipped := 0
	for _, target := range toEnqueue {
		// Skip issues that already have a live queue entry (avoid duplicate cascade).
		// This is the friendly fast path; the partial unique index on
		// tasks.issue_tasks (#627) is the real guard — it closes the window
		// between this check and the INSERT, so a concurrent enqueue that wins
		// the race makes CreateTaskForIssue fail with ErrDuplicateActiveTask,
		// which we treat as "already queued" below.
		active, aerr := hasActiveTask(ctx, client, target.ID)
		if aerr != nil {
			return nil, fmt.Errorf("failed to check active tasks for issue #%d: %w", target.ID, aerr)
		}
		if active {
			skipped++
			continue
		}

		if enqueued > 0 {
			time.Sleep(cascadeEnqueueDelay)
		}

		// Children from Edges.Children may lack Project; use cascade projectID/sheep.
		taskID, err := enqueueOne(ctx, client, target, sheep, projectID, in.Model)
		if err != nil {
			if errors.Is(err, queue.ErrDuplicateActiveTask) {
				// Lost the race to a concurrent enqueue — the issue is queued.
				skipped++
				continue
			}
			return nil, fmt.Errorf("failed to enqueue issue #%d: %w", target.ID, err)
		}
		result.TaskIDs = append(result.TaskIDs, taskID)
		result.IssueIDs = append(result.IssueIDs, target.ID)
		if target.ID == iss.ID {
			result.TaskID = taskID
		}
		enqueued++
	}

	if enqueued == 0 {
		if skipped > 0 {
			return nil, fmt.Errorf("no tasks enqueued: all target issues already have pending/running tasks")
		}
		return nil, fmt.Errorf("no tasks enqueued: no target issues")
	}

	// Watch marking (#597): every issue actually enqueued in this cascade —
	// children and parent alike — becomes shepherd-watched when requested, so
	// the wake hook fires for each linked task that finishes.
	if in.Watch && len(result.IssueIDs) > 0 {
		now := time.Now()
		_, werr := client.Issue.Update().
			Where(entIssue.IDIn(result.IssueIDs...)).
			SetShepherdWatch(true).
			SetShepherdWatchedAt(now).
			Save(ctx)
		if werr != nil {
			return nil, fmt.Errorf("failed to set shepherd_watch on issues %v: %w", result.IssueIDs, werr)
		}
	}

	return result, nil
}

// hasActiveTask reports whether the issue already has a pending or running task.
func hasActiveTask(ctx context.Context, client *ent.Client, issueID int) (bool, error) {
	n, err := client.Task.Query().
		Where(
			entTask.HasIssueWith(entIssue.ID(issueID)),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning),
		).
		Count(ctx)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// reconcileStaleProgress fixes issues left at in_progress/testing after their
// linked tasks were bulk-cancelled or recovered without status sync.
// No-op when a pending/running task still exists, or status is terminal/todo.
func reconcileStaleProgress(ctx context.Context, client *ent.Client, iss *ent.Issue) error {
	if iss == nil {
		return nil
	}
	switch iss.Status {
	case entIssue.StatusInProgress, entIssue.StatusTesting:
		// continue
	default:
		return nil
	}

	active, err := hasActiveTask(ctx, client, iss.ID)
	if err != nil || active {
		return err
	}

	// Latest linked task decides the repaired status.
	latest, err := client.Task.Query().
		Where(entTask.HasIssueWith(entIssue.ID(iss.ID))).
		Order(ent.Desc(entTask.FieldCreatedAt)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// No tasks at all — return to todo so execute can start clean.
			_, uerr := client.Issue.UpdateOneID(iss.ID).
				SetStatus(entIssue.StatusTodo).
				ClearCompletedAt().
				Save(ctx)
			return uerr
		}
		return err
	}

	newStatus := entIssue.StatusFailed
	switch latest.Status {
	case entTask.StatusCompleted:
		newStatus = entIssue.StatusTesting
	case entTask.StatusFailed, entTask.StatusStopped:
		newStatus = entIssue.StatusFailed
	case entTask.StatusPending, entTask.StatusRunning:
		// Race: task became active after hasActiveTask — leave issue alone.
		return nil
	}

	upd := client.Issue.UpdateOneID(iss.ID).SetStatus(newStatus)
	if newStatus == entIssue.StatusFailed {
		upd = upd.SetCompletedAt(time.Now())
	}
	_, err = upd.Save(ctx)
	return err
}

// ChildrenChecklist returns a markdown checklist of direct children with status.
// Empty string when there are no children.
func ChildrenChecklist(iss *ent.Issue) string {
	if iss == nil || len(iss.Edges.Children) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## 하위 이슈\n")
	for _, c := range iss.Edges.Children {
		mark := " "
		if c.Status == entIssue.StatusDone {
			mark = "x"
		}
		sb.WriteString(fmt.Sprintf("- [%s] #%d %s (%s)\n", mark, c.ID, c.Title, llmStatusLabel(string(c.Status))))
	}
	return strings.TrimRight(sb.String(), "\n")
}

func enqueueOne(ctx context.Context, client *ent.Client, iss *ent.Issue, sheep *ent.Sheep, projectID int, model string) (int, error) {
	typeLabel := map[string]string{
		"design":  "설계",
		"feature": "기능",
		"bug":     "버그",
	}[string(iss.Type)]
	if typeLabel == "" {
		typeLabel = string(iss.Type)
	}

	body := iss.Body
	if checklist := ChildrenChecklist(iss); checklist != "" {
		if body != "" {
			body = body + "\n\n" + checklist
		} else {
			body = checklist
		}
	}

	prompt := fmt.Sprintf(
		"[이슈 #%d] %s (타입: %s)\n\n## 이슈 내용\n%s\n\n## 목표 (완료 기준)\n%s\n\n위 이슈를 해결하라. 목표 기준을 충족했는지 스스로 검증하고 결과를 보고하라.",
		iss.ID, iss.Title, typeLabel, body, iss.Goal,
	)

	t, err := queue.CreateTaskForIssue(prompt, sheep.ID, projectID, iss.ID)
	if err != nil {
		return 0, fmt.Errorf("failed to create task: %w", err)
	}

	if model != "" {
		queue.SetTaskModel(t.ID, model)
	}

	now := time.Now()
	upd := client.Issue.UpdateOneID(iss.ID).
		SetStatus(entIssue.StatusInProgress).
		ClearCompletedAt() // re-run after fail/stop must not look finalized
	// Prefer StartedAt from the entity if already loaded; otherwise set.
	if iss.StartedAt == nil || iss.StartedAt.IsZero() {
		upd = upd.SetStartedAt(now)
	}
	if _, err = upd.Save(ctx); err != nil {
		return 0, fmt.Errorf("failed to update issue status: %w", err)
	}

	return t.ID, nil
}

func resolveSheep(ctx context.Context, client *ent.Client, in ExecuteInput, iss *ent.Issue) (*ent.Sheep, string, error) {
	var sheep *ent.Sheep
	var err error
	sheepName := in.SheepName
	if sheepName != "" {
		sheep, err = worker.Get(sheepName)
		if err != nil {
			return nil, "", fmt.Errorf("sheep not found: %w", err)
		}
		return sheep, sheepName, nil
	}
	if iss.Edges.Project != nil && iss.Edges.Project.Edges.Sheep != nil {
		sheep = iss.Edges.Project.Edges.Sheep
		return sheep, sheep.Name, nil
	}
	proj, perr := client.Project.Query().
		Where(entProject.Name(in.Project)).
		WithSheep().
		Only(ctx)
	if perr == nil && proj.Edges.Sheep != nil {
		return proj.Edges.Sheep, proj.Edges.Sheep.Name, nil
	}
	return nil, "", fmt.Errorf("sheep is required: pass --sheep or assign a sheep to project %q", in.Project)
}

func resolveProjectID(ctx context.Context, client *ent.Client, projectName string, iss *ent.Issue) (int, error) {
	if iss.Edges.Project != nil {
		return iss.Edges.Project.ID, nil
	}
	proj, perr := client.Project.Query().Where(entProject.Name(projectName)).Only(ctx)
	if perr != nil {
		return 0, fmt.Errorf("project not found: %w", perr)
	}
	return proj.ID, nil
}

// validateParent ensures parentID exists in the same project and is not a cycle
// (self or descendant of childID). childID is 0 when creating.
func validateParent(ctx context.Context, client *ent.Client, projectName string, parentID, childID int) error {
	if parentID <= 0 {
		return fmt.Errorf("invalid parent_id")
	}
	if childID > 0 && parentID == childID {
		return fmt.Errorf("invalid parent_id: issue cannot be its own parent")
	}

	parent, err := client.Issue.Query().
		Where(entIssue.ID(parentID), entIssue.HasProjectWith(entProject.Name(projectName))).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return fmt.Errorf("parent issue #%d not found in project %q", parentID, projectName)
		}
		return fmt.Errorf("failed to query parent issue: %w", err)
	}
	_ = parent

	// Walk ancestors of the proposed parent; if we hit childID, setting this
	// parent would create a cycle.
	if childID > 0 {
		seen := map[int]bool{childID: true}
		cur := parentID
		for cur > 0 {
			if seen[cur] {
				return fmt.Errorf("invalid parent_id: would create a cycle")
			}
			seen[cur] = true
			anc, aerr := client.Issue.Query().Where(entIssue.ID(cur)).Only(ctx)
			if aerr != nil {
				break
			}
			if anc.ParentID == nil {
				break
			}
			cur = *anc.ParentID
		}
	}
	return nil
}

// statusLabel renders an issue status in the current display language (#665).
func statusLabel(s string) string {
	switch s {
	case "todo":
		return i18n.T().IssueStatus.Todo
	case "in_progress":
		return i18n.T().IssueStatus.InProgress
	case "testing":
		return i18n.T().IssueStatus.Testing
	case "failed":
		return i18n.T().IssueStatus.Failed
	case "done":
		return i18n.T().IssueStatus.Done
	default:
		return s
	}
}

// llmStatusLabel is the Korean status text the LLM-facing prompt blocks use.
// The prompt format (ChildrenChecklist, issue prompt) is a stable contract and
// is deliberately NOT localized (#665 constraint), so ChildrenChecklist keeps
// calling this instead of statusLabel.
func llmStatusLabel(s string) string {
	switch s {
	case "todo":
		return "작업전"
	case "in_progress":
		return "작업중"
	case "testing":
		return "테스트"
	case "failed":
		return "실패"
	case "done":
		return "성공"
	default:
		return s
	}
}

func validateType(t string) error {
	switch t {
	case "design", "feature", "bug":
		return nil
	default:
		return fmt.Errorf("invalid type %q: must be one of %s", t, strings.Join(ValidTypes, ", "))
	}
}

func validateStatus(s string) error {
	switch s {
	case "todo", "in_progress", "testing", "failed", "done":
		return nil
	default:
		return fmt.Errorf("invalid status %q: must be one of %s", s, strings.Join(ValidStatuses, ", "))
	}
}

// FormatTime formats a time for CLI display (empty if zero/nil).
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// FormatTimePtr formats a *time.Time for CLI display.
func FormatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}
