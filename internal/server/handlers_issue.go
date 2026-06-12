package server

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/projects/:name/issues
func (s *Server) handleListIssues(c *fiber.Ctx) error {
	name := c.Params("name")
	ctx := context.Background()
	client := db.Client()

	query := client.Issue.Query().Where(entIssue.HasProjectWith(entProject.Name(name)))

	// Filters
	if status := c.Query("status"); status != "" {
		query = query.Where(entIssue.StatusEQ(entIssue.Status(status)))
	}
	if issueType := c.Query("type"); issueType != "" {
		query = query.Where(entIssue.TypeEQ(entIssue.Type(issueType)))
	}
	if q := c.Query("q"); q != "" {
		query = query.Where(entIssue.TitleContains(q))
	}

	// Count total
	total, err := query.Clone().Count(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Pagination
	page := c.QueryInt("page", 1)
	limit := c.QueryInt("limit", 20)
	if limit > 100 {
		limit = 100
	}
	if page < 1 {
		page = 1
	}

	// Sort order (newest first)
	orderFunc := ent.Desc(entIssue.FieldCreatedAt)
	if c.Query("sort") == "asc" {
		orderFunc = ent.Asc(entIssue.FieldCreatedAt)
	}

	issues, err := query.
		Order(orderFunc).
		Offset((page - 1) * limit).
		Limit(limit).
		WithProject().
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type issueItem struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		Type        string `json:"type"`
		Status      string `json:"status"`
		Body        string `json:"body,omitempty"`
		Goal        string `json:"goal,omitempty"`
		TaskCount   int    `json:"task_count"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
		StartedAt   string `json:"started_at,omitempty"`
		CompletedAt string `json:"completed_at,omitempty"`
	}

	var items []issueItem
	for _, issue := range issues {
		item := issueItem{
			ID:      issue.ID,
			Title:   issue.Title,
			Type:    string(issue.Type),
			Status:  string(issue.Status),
			Body:    issue.Body,
			Goal:    issue.Goal,
			CreatedAt:   issue.CreatedAt.Format("2006-01-02 15:04:05"),
			UpdatedAt:   issue.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
		if !issue.StartedAt.IsZero() {
			item.StartedAt = issue.StartedAt.Format("2006-01-02 15:04:05")
		}
		if !issue.CompletedAt.IsZero() {
			item.CompletedAt = issue.CompletedAt.Format("2006-01-02 15:04:05")
		}

		// Count linked tasks
		tc, _ := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(issue.ID))).Count(ctx)
		item.TaskCount = tc

		items = append(items, item)
	}

	totalPages := (total + limit - 1) / limit

	return c.JSON(fiber.Map{
		"success":     true,
		"data":        items,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": totalPages,
	})
}

// POST /api/projects/:name/issues
func (s *Server) handleCreateIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	ctx := context.Background()
	client := db.Client()

	var body struct {
		Title string `json:"title"`
		Type  string `json:"type"`
		Body  string `json:"body"`
		Goal  string `json:"goal"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Title == "" {
		return fail(c, fiber.StatusBadRequest, "title is required")
	}

	// Validate type
	if body.Type == "" {
		body.Type = "feature"
	}
	switch body.Type {
	case "design", "feature", "bug":
	default:
		return fail(c, fiber.StatusBadRequest, "invalid type: must be design, feature, or bug")
	}

	// Find project
	project, err := client.Project.Query().Where(entProject.Name(name)).Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	issue, err := client.Issue.Create().
		SetTitle(body.Title).
		SetType(entIssue.Type(body.Type)).
		SetBody(body.Body).
		SetGoal(body.Goal).
		SetProject(project).
		Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"data": map[string]interface{}{
			"id":         issue.ID,
			"title":      issue.Title,
			"type":       string(issue.Type),
			"status":     string(issue.Status),
			"body":       issue.Body,
			"goal":       issue.Goal,
			"created_at": issue.CreatedAt.Format("2006-01-02 15:04:05"),
		},
	})
}

// GET /api/projects/:name/issues/:id
func (s *Server) handleGetIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	ctx := context.Background()
	client := db.Client()

	issue, err := client.Issue.Query().
		Where(entIssue.ID(id), entIssue.HasProjectWith(entProject.Name(name))).
		WithProject().
		WithTasks(func(tq *ent.TaskQuery) {
			tq.Order(ent.Desc(entTask.FieldCreatedAt))
		}).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "issue not found")
	}

	result := map[string]interface{}{
		"id":         issue.ID,
		"title":      issue.Title,
		"type":       string(issue.Type),
		"status":     string(issue.Status),
		"body":       issue.Body,
		"goal":       issue.Goal,
		"created_at": issue.CreatedAt.Format("2006-01-02 15:04:05"),
		"updated_at": issue.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if !issue.StartedAt.IsZero() {
		result["started_at"] = issue.StartedAt.Format("2006-01-02 15:04:05")
	}
	if !issue.CompletedAt.IsZero() {
		result["completed_at"] = issue.CompletedAt.Format("2006-01-02 15:04:05")
	}

	type linkedTask struct {
		ID          int    `json:"id"`
		Status      string `json:"status"`
		Summary     string `json:"summary,omitempty"`
		CreatedAt   string `json:"created_at"`
		CompletedAt string `json:"completed_at,omitempty"`
	}

	var tasks []linkedTask
	for _, t := range issue.Edges.Tasks {
		tm := linkedTask{
			ID:        t.ID,
			Status:    string(t.Status),
			Summary:   t.Summary,
			CreatedAt: t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if !t.CompletedAt.IsZero() {
			tm.CompletedAt = t.CompletedAt.Format("2006-01-02 15:04:05")
		}
		tasks = append(tasks, tm)
	}
	result["tasks"] = tasks

	return success(c, result)
}

// PATCH /api/projects/:name/issues/:id
func (s *Server) handleUpdateIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	ctx := context.Background()
	client := db.Client()

	issue, err := client.Issue.Query().
		Where(entIssue.ID(id), entIssue.HasProjectWith(entProject.Name(name))).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "issue not found")
	}

	var body struct {
		Title  *string `json:"title,omitempty"`
		Type   *string `json:"type,omitempty"`
		Body   *string `json:"body,omitempty"`
		Goal   *string `json:"goal,omitempty"`
		Status *string `json:"status,omitempty"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	update := client.Issue.UpdateOneID(id)

	if body.Title != nil {
		if *body.Title == "" {
			return fail(c, fiber.StatusBadRequest, "title cannot be empty")
		}
		update = update.SetTitle(*body.Title)
	}
	if body.Type != nil {
		switch *body.Type {
		case "design", "feature", "bug":
			update = update.SetType(entIssue.Type(*body.Type))
		default:
			return fail(c, fiber.StatusBadRequest, "invalid type: must be design, feature, or bug")
		}
	}
	if body.Body != nil {
		update = update.SetBody(*body.Body)
	}
	if body.Goal != nil {
		update = update.SetGoal(*body.Goal)
	}
	if body.Status != nil {
		switch *body.Status {
		case "todo", "in_progress", "testing", "failed", "done":
			update = update.SetStatus(entIssue.Status(*body.Status))

			// Record completed_at when marking as done or failed
			if *body.Status == "done" || *body.Status == "failed" {
				if issue.CompletedAt.IsZero() {
					update = update.SetCompletedAt(time.Now())
				}
			}
		default:
			return fail(c, fiber.StatusBadRequest, "invalid status: must be todo, in_progress, testing, failed, or done")
		}
	}

	issue, err = update.Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, map[string]interface{}{
		"id":         issue.ID,
		"title":      issue.Title,
		"type":       string(issue.Type),
		"status":     string(issue.Status),
		"body":       issue.Body,
		"goal":       issue.Goal,
		"updated_at": issue.UpdatedAt.Format("2006-01-02 15:04:05"),
	})
}

// DELETE /api/projects/:name/issues/:id
func (s *Server) handleDeleteIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	ctx := context.Background()
	client := db.Client()

	_, err = client.Issue.Query().
		Where(entIssue.ID(id), entIssue.HasProjectWith(entProject.Name(name))).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "issue not found")
	}

	// Clear issue edge from linked tasks (preserve tasks)
	tasks, _ := client.Task.Query().Where(entTask.HasIssueWith(entIssue.ID(id))).All(ctx)
	for _, t := range tasks {
		client.Task.UpdateOneID(t.ID).ClearIssue().Save(ctx) //nolint:errcheck
	}

	// Delete issue
	err = client.Issue.DeleteOneID(id).Exec(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, map[string]interface{}{
		"message": "issue deleted",
	})
}

// POST /api/projects/:name/issues/:id/execute
func (s *Server) handleExecuteIssue(c *fiber.Ctx) error {
	name := c.Params("name")
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid issue ID")
	}

	ctx := context.Background()
	client := db.Client()

	// Find issue within project
	issue, err := client.Issue.Query().
		Where(entIssue.ID(id), entIssue.HasProjectWith(entProject.Name(name))).
		WithProject().
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "issue not found")
	}

	var body struct {
		SheepName string `json:"sheep_name"`
		Model     string `json:"model,omitempty"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// Resolve sheep: from body, or from project assignment
	var sheep *ent.Sheep
	if body.SheepName != "" {
		sheep, err = worker.Get(body.SheepName)
		if err != nil {
			return fail(c, fiber.StatusNotFound, "sheep not found: "+err.Error())
		}
	} else if issue.Edges.Project != nil && issue.Edges.Project.Edges.Sheep != nil {
		sheep, err = worker.Get(issue.Edges.Project.Edges.Sheep.Name)
		if err != nil {
			return fail(c, fiber.StatusNotFound, "assigned sheep not found: "+err.Error())
		}
		body.SheepName = sheep.Name
	} else {
		return fail(c, fiber.StatusBadRequest, "sheep_name is required or project must have an assigned sheep")
	}

	// Block task creation when the sheep's provider is disabled in settings.
	if !config.IsProviderEnabled(string(sheep.Provider)) {
		return fail(c, fiber.StatusBadRequest, "provider '"+string(sheep.Provider)+"' is disabled in settings")
	}

	// Assemble prompt from issue content
	typeLabel := map[string]string{
		"design":  "설계",
		"feature": "기능",
		"bug":     "버그",
	}[string(issue.Type)]

	prompt := fmt.Sprintf(
		"[이슈 #%d] %s (타입: %s)\n\n## 이슈 내용\n%s\n\n## 목표 (완료 기준)\n%s\n\n위 이슈를 해결하라. 목표 기준을 충족했는지 스스로 검증하고 결과를 보고하라.",
		issue.ID, issue.Title, typeLabel, issue.Body, issue.Goal,
	)

	projectID := issue.Edges.Project.ID

	// Create task via queue
	t, err := queue.CreateTask(prompt, sheep.ID, projectID)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Set model override if provided
	if body.Model != "" {
		queue.SetTaskModel(t.ID, body.Model)
	}

	// Link task to issue (update task edge)
	_, err = client.Task.UpdateOneID(t.ID).SetIssue(issue).Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to link task to issue: "+err.Error())
	}

	// Update issue status to in_progress
	now := time.Now()
	update := client.Issue.UpdateOne(issue).SetStatus(entIssue.StatusInProgress)
	if issue.StartedAt.IsZero() {
		update = update.SetStartedAt(now)
	}
	_, err = update.Save(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Trigger immediate processing
	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"task_id":    t.ID,
		"sheep_name": body.SheepName,
	})
}
