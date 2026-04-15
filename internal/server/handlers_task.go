package server

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entSheep "github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/tasks
func (s *Server) handleListTasks(c *fiber.Ctx) error {
	ctx := context.Background()
	client := db.Client()

	query := client.Task.Query()

	// Filters
	if status := c.Query("status"); status != "" {
		query = query.Where(entTask.StatusEQ(entTask.Status(status)))
	}
	if projectName := c.Query("project"); projectName != "" {
		query = query.Where(entTask.HasProjectWith(entProject.Name(projectName)))
	}
	if sheepName := c.Query("sheep"); sheepName != "" {
		query = query.Where(entTask.HasSheepWith(entSheep.Name(sheepName)))
	}
	if q := c.Query("q"); q != "" {
		query = query.Where(
			entTask.Or(
				entTask.PromptContains(q),
				entTask.SummaryContains(q),
			),
		)
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

	// Sort order
	orderFunc := ent.Desc(entTask.FieldCreatedAt)
	if c.Query("sort") == "asc" {
		orderFunc = ent.Asc(entTask.FieldCreatedAt)
	}

	tasks, err := query.
		Order(orderFunc).
		Offset((page - 1) * limit).
		Limit(limit).
		WithProject().
		WithSheep().
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type taskItem struct {
		ID        int     `json:"id"`
		Prompt    string  `json:"prompt"`
		Status    string  `json:"status"`
		Summary   string  `json:"summary,omitempty"`
		Error     string  `json:"error,omitempty"`
		CostUSD   float64 `json:"cost_usd,omitempty"`
		Sheep     string  `json:"sheep,omitempty"`
		Project   string  `json:"project,omitempty"`
		CreatedAt string  `json:"created_at"`
	}

	var items []taskItem
	for _, t := range tasks {
		item := taskItem{
			ID:        t.ID,
			Prompt:    t.Prompt,
			Status:    string(t.Status),
			Summary:   t.Summary,
			Error:     t.Error,
			CostUSD:   t.CostUsd,
			CreatedAt: t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if t.Edges.Sheep != nil {
			item.Sheep = t.Edges.Sheep.Name
		}
		if t.Edges.Project != nil {
			item.Project = t.Edges.Project.Name
		}
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

// POST /api/tasks
func (s *Server) handleCreateTask(c *fiber.Ctx) error {
	var body struct {
		Prompt      string `json:"prompt"`
		SheepName   string `json:"sheep_name"`
		ProjectName string `json:"project_name"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Prompt == "" {
		return fail(c, fiber.StatusBadRequest, "prompt is required")
	}

	// Look up sheep and project
	// If sheep_name is omitted but project_name is given, auto-resolve the assigned sheep
	var sheep *ent.Sheep
	var err error

	if body.SheepName != "" {
		sheep, err = worker.Get(body.SheepName)
		if err != nil {
			return fail(c, fiber.StatusNotFound, "sheep not found: "+err.Error())
		}
	}

	var projectID int
	if body.ProjectName != "" {
		ctx := context.Background()
		p, err := db.Client().Project.Query().
			Where(entProject.Name(body.ProjectName)).
			WithSheep().
			Only(ctx)
		if err != nil {
			return fail(c, fiber.StatusNotFound, "project not found")
		}
		projectID = p.ID

		// Auto-resolve sheep from project if not explicitly provided
		if sheep == nil {
			if p.Edges.Sheep == nil {
				return fail(c, fiber.StatusBadRequest, "no sheep assigned to project '"+body.ProjectName+"', specify sheep_name explicitly")
			}
			sheep, err = worker.Get(p.Edges.Sheep.Name)
			if err != nil {
				return fail(c, fiber.StatusNotFound, "assigned sheep not found: "+err.Error())
			}
			body.SheepName = sheep.Name
		}
	} else if sheep != nil && sheep.Edges.Project != nil {
		projectID = sheep.Edges.Project.ID
	}

	if sheep == nil {
		return fail(c, fiber.StatusBadRequest, "sheep_name or project_name is required")
	}

	var t *ent.Task
	if projectID > 0 {
		t, err = queue.CreateTask(body.Prompt, sheep.ID, projectID)
	} else {
		t, err = queue.CreateManagerTask(body.Prompt, sheep.ID)
	}
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Trigger immediate processing
	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"task_id":      t.ID,
		"sheep_name":   body.SheepName,
		"project_name": body.ProjectName,
	})
}

// GET /api/tasks/:id
func (s *Server) handleGetTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	result := map[string]interface{}{
		"id":             t.ID,
		"prompt":         t.Prompt,
		"status":         string(t.Status),
		"summary":        t.Summary,
		"error":          t.Error,
		"files_modified": t.FilesModified,
		"output":         t.Output,
		"cost_usd":       t.CostUsd,
		"created_at":     t.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if !t.StartedAt.IsZero() {
		result["started_at"] = t.StartedAt.Format("2006-01-02 15:04:05")
	}
	if !t.CompletedAt.IsZero() {
		result["completed_at"] = t.CompletedAt.Format("2006-01-02 15:04:05")
	}
	if t.Edges.Sheep != nil {
		result["sheep"] = t.Edges.Sheep.Name
	}
	if t.Edges.Project != nil {
		result["project"] = t.Edges.Project.Name
	}

	return success(c, result)
}

// POST /api/tasks/:id/stop
func (s *Server) handleStopTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	// Get the task to find which sheep is running it
	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	if t.Status != entTask.StatusRunning {
		return fail(c, fiber.StatusBadRequest, "task is not running")
	}

	if t.Edges.Sheep == nil {
		return fail(c, fiber.StatusInternalServerError, "task has no assigned sheep")
	}

	// Stop the running task
	result, err := worker.StopTask(t.Edges.Sheep.Name)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Mark task as stopped
	_ = queue.StopTaskWithOutput(id, "stopped by user", result.OutputLines)

	return success(c, map[string]interface{}{
		"task_id": id,
		"stopped": true,
	})
}

// POST /api/tasks/:id/retry
func (s *Server) handleRetryTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	if t.Status != entTask.StatusFailed && t.Status != entTask.StatusStopped {
		return fail(c, fiber.StatusBadRequest, "only failed or stopped tasks can be retried")
	}

	if t.Edges.Sheep == nil {
		return fail(c, fiber.StatusInternalServerError, "task has no assigned sheep")
	}

	// Reset circuit breaker on manual retry
	queue.ResetCircuitBreaker(t.Edges.Sheep.Name)

	var projectID int
	if t.Edges.Project != nil {
		projectID = t.Edges.Project.ID
	}

	var newTask *ent.Task
	if projectID > 0 {
		newTask, err = queue.CreateTask(t.Prompt, t.Edges.Sheep.ID, projectID)
	} else {
		newTask, err = queue.CreateManagerTask(t.Prompt, t.Edges.Sheep.ID)
	}
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"task_id":          newTask.ID,
		"original_task_id": id,
		"sheep_name":       t.Edges.Sheep.Name,
	})
}

// POST /api/tasks/:id/retry-from
// Retries all failed/stopped tasks from this task ID onwards (inclusive) for the same project
func (s *Server) handleRetryFromTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	if t.Edges.Project == nil {
		return fail(c, fiber.StatusBadRequest, "task has no project")
	}

	// Find all failed/stopped tasks for this project with ID >= given ID, ordered by ID asc
	ctx := context.Background()
	client := db.Client()
	failedTasks, err := client.Task.Query().
		Where(
			entTask.IDGTE(id),
			entTask.HasProjectWith(entProject.ID(t.Edges.Project.ID)),
			entTask.Or(
				entTask.StatusEQ(entTask.StatusFailed),
				entTask.StatusEQ(entTask.StatusStopped),
			),
		).
		WithSheep().
		WithProject().
		Order(ent.Asc(entTask.FieldID)).
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	if len(failedTasks) == 0 {
		return fail(c, fiber.StatusNotFound, "no failed tasks found from this ID")
	}

	var createdIDs []int
	for _, ft := range failedTasks {
		if ft.Edges.Sheep == nil {
			continue
		}
		var projectID int
		if ft.Edges.Project != nil {
			projectID = ft.Edges.Project.ID
		}
		var newTask *ent.Task
		if projectID > 0 {
			newTask, err = queue.CreateTask(ft.Prompt, ft.Edges.Sheep.ID, projectID)
		} else {
			newTask, err = queue.CreateManagerTask(ft.Prompt, ft.Edges.Sheep.ID)
		}
		if err != nil {
			continue
		}
		createdIDs = append(createdIDs, newTask.ID)
	}

	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"created_tasks": createdIDs,
		"count":         len(createdIDs),
	})
}

// POST /api/command
func (s *Server) handleCommand(c *fiber.Ctx) error {
	var body struct {
		Prompt string `json:"prompt"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Prompt == "" {
		return fail(c, fiber.StatusBadRequest, "prompt is required")
	}

	// Analyze the prompt to determine project and sheep
	decision, err := manager.Analyze(body.Prompt)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "analysis failed: "+err.Error())
	}

	// Look up sheep
	sheep, err := worker.Get(decision.SheepName)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "sheep not found: "+err.Error())
	}

	// Look up project
	var projectID int
	if decision.ProjectName != "" {
		ctx := context.Background()
		p, err := db.Client().Project.Query().
			Where(entProject.Name(decision.ProjectName)).
			Only(ctx)
		if err == nil {
			projectID = p.ID
		}
	}
	if projectID == 0 && sheep.Edges.Project != nil {
		projectID = sheep.Edges.Project.ID
	}

	// Create task
	var t *ent.Task
	if projectID > 0 {
		t, err = queue.CreateTask(body.Prompt, sheep.ID, projectID)
	} else {
		t, err = queue.CreateManagerTask(body.Prompt, sheep.ID)
	}
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Trigger immediate processing
	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"task_id":      t.ID,
		"sheep_name":   decision.SheepName,
		"project_name": decision.ProjectName,
		"reason":       decision.Reason,
	})
}
