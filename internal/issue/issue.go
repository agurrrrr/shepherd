// Package issue provides CRUD and execute operations for shepherd project issues.
// Issues are the built-in tracker (design / feature / bug) linked to tasks via execute.
package issue

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
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
	Project string // project name (required)
	Title   string // required
	Type    string // design | feature | bug (default: feature)
	Body    string // optional description
	Goal    string // optional success criteria
}

// UpdateInput holds optional fields for partial update. Nil pointer = leave unchanged.
type UpdateInput struct {
	Title  *string
	Type   *string
	Body   *string
	Goal   *string
	Status *string
}

// ListFilter controls listing / filtering.
type ListFilter struct {
	Project string
	Status  string // empty = all
	Type    string // empty = all
	Query   string // title substring
	Page    int    // 1-based, default 1
	Limit   int    // default 20, max 100
	SortAsc bool   // default newest-first
}

// ListResult is a page of issues plus pagination metadata.
type ListResult struct {
	Items      []*ent.Issue
	TaskCounts map[int]int // issue ID → linked task count
	Total      int
	Page       int
	Limit      int
	TotalPages int
}

// ExecuteInput controls issue → task enqueue.
type ExecuteInput struct {
	Project   string
	IssueID   int
	SheepName string // optional; falls back to project-assigned sheep
	Model     string // optional per-task model override
}

// ExecuteResult is returned after enqueueing a task for an issue.
type ExecuteResult struct {
	TaskID    int
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

	issue, err := client.Issue.Create().
		SetTitle(in.Title).
		SetType(entIssue.Type(issueType)).
		SetBody(in.Body).
		SetGoal(in.Goal).
		SetProject(project).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create issue: %w", err)
	}
	return issue, nil
}

// List returns a filtered, paginated list of issues for a project.
func List(f ListFilter) (*ListResult, error) {
	if strings.TrimSpace(f.Project) == "" {
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

	// Ensure project exists (clearer error than empty list).
	exists, err := client.Project.Query().Where(entProject.Name(f.Project)).Exist(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query project: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("project %q not found", f.Project)
	}

	query := client.Issue.Query().Where(entIssue.HasProjectWith(entProject.Name(f.Project)))

	if f.Status != "" {
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

	total, err := query.Clone().Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count issues: %w", err)
	}

	orderFunc := ent.Desc(entIssue.FieldCreatedAt)
	if f.SortAsc {
		orderFunc = ent.Asc(entIssue.FieldCreatedAt)
	}

	issues, err := query.
		Order(orderFunc).
		Offset((f.Page - 1) * f.Limit).
		Limit(f.Limit).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list issues: %w", err)
	}

	counts := make(map[int]int, len(issues))
	for _, iss := range issues {
		tc, _ := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(iss.ID))).Count(ctx)
		counts[iss.ID] = tc
	}

	totalPages := 0
	if total > 0 {
		totalPages = (total + f.Limit - 1) / f.Limit
	}

	return &ListResult{
		Items:      issues,
		TaskCounts: counts,
		Total:      total,
		Page:       f.Page,
		Limit:      f.Limit,
		TotalPages: totalPages,
	}, nil
}

// Get returns a single issue with linked tasks, scoped to project.
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

	if in.Title == nil && in.Type == nil && in.Body == nil && in.Goal == nil && in.Status == nil {
		return nil, fmt.Errorf("nothing to update: provide at least one of --title, --type, --body, --goal, --status")
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

	updated, err := update.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to update issue: %w", err)
	}
	return updated, nil
}

// Delete removes an issue. Linked tasks are preserved (issue edge cleared).
func Delete(projectName string, id int) error {
	// Scope check
	if _, err := Get(projectName, id); err != nil {
		return err
	}

	ctx := context.Background()
	client := db.Client()

	tasks, _ := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(id))).All(ctx)
	for _, t := range tasks {
		_, _ = client.Task.UpdateOneID(t.ID).ClearIssue().Save(ctx)
	}

	if err := client.Issue.DeleteOneID(id).Exec(ctx); err != nil {
		return fmt.Errorf("failed to delete issue: %w", err)
	}
	return nil
}

// Execute builds a prompt from the issue and enqueues a task for a sheep.
// Does not require a live daemon; the processor picks up pending tasks when running.
func Execute(in ExecuteInput) (*ExecuteResult, error) {
	iss, err := Get(in.Project, in.IssueID)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	client := db.Client()

	// Resolve sheep
	var sheep *ent.Sheep
	sheepName := in.SheepName
	if sheepName != "" {
		sheep, err = worker.Get(sheepName)
		if err != nil {
			return nil, fmt.Errorf("sheep not found: %w", err)
		}
	} else if iss.Edges.Project != nil && iss.Edges.Project.Edges.Sheep != nil {
		sheep = iss.Edges.Project.Edges.Sheep
		sheepName = sheep.Name
	} else {
		// Project edge may not load sheep; re-query project with sheep
		proj, perr := client.Project.Query().
			Where(entProject.Name(in.Project)).
			WithSheep().
			Only(ctx)
		if perr == nil && proj.Edges.Sheep != nil {
			sheep = proj.Edges.Sheep
			sheepName = sheep.Name
		} else {
			return nil, fmt.Errorf("sheep is required: pass --sheep or assign a sheep to project %q", in.Project)
		}
	}

	if !config.IsProviderEnabled(string(sheep.Provider)) {
		return nil, fmt.Errorf("provider %q is disabled in settings", sheep.Provider)
	}

	typeLabel := map[string]string{
		"design":  "설계",
		"feature": "기능",
		"bug":     "버그",
	}[string(iss.Type)]
	if typeLabel == "" {
		typeLabel = string(iss.Type)
	}

	prompt := fmt.Sprintf(
		"[이슈 #%d] %s (타입: %s)\n\n## 이슈 내용\n%s\n\n## 목표 (완료 기준)\n%s\n\n위 이슈를 해결하라. 목표 기준을 충족했는지 스스로 검증하고 결과를 보고하라.",
		iss.ID, iss.Title, typeLabel, iss.Body, iss.Goal,
	)

	projectID := 0
	if iss.Edges.Project != nil {
		projectID = iss.Edges.Project.ID
	} else {
		proj, perr := client.Project.Query().Where(entProject.Name(in.Project)).Only(ctx)
		if perr != nil {
			return nil, fmt.Errorf("project not found: %w", perr)
		}
		projectID = proj.ID
	}

	t, err := queue.CreateTask(prompt, sheep.ID, projectID)
	if err != nil {
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	if in.Model != "" {
		queue.SetTaskModel(t.ID, in.Model)
	}

	if _, err = client.Task.UpdateOneID(t.ID).SetIssue(iss).Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to link task to issue: %w", err)
	}

	now := time.Now()
	upd := client.Issue.UpdateOne(iss).SetStatus(entIssue.StatusInProgress)
	if iss.StartedAt == nil || iss.StartedAt.IsZero() {
		upd = upd.SetStartedAt(now)
	}
	if _, err = upd.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to update issue status: %w", err)
	}

	return &ExecuteResult{
		TaskID:    t.ID,
		SheepName: sheepName,
		IssueID:   iss.ID,
	}, nil
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
