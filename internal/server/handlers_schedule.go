package server

import (
	"context"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/scheduler"
)

// GET /api/schedules — list all schedules
func (s *Server) handleListAllSchedules(c *fiber.Ctx) error {
	schedules, err := scheduler.ListAll()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	var items []fiber.Map
	for _, sc := range schedules {
		item := scheduleToMap(sc)
		items = append(items, item)
	}

	if items == nil {
		items = []fiber.Map{}
	}

	return success(c, items)
}

// GET /api/projects/:name/schedules — list schedules for a project
func (s *Server) handleListProjectSchedules(c *fiber.Ctx) error {
	projectName := paramDecoded(c, "name")

	schedules, err := scheduler.ListByProject(projectName)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	var items []fiber.Map
	for _, sc := range schedules {
		item := scheduleToMap(sc)
		items = append(items, item)
	}

	if items == nil {
		items = []fiber.Map{}
	}

	return success(c, items)
}

// POST /api/projects/:name/schedules — create a schedule
func (s *Server) handleCreateSchedule(c *fiber.Ctx) error {
	projectName := paramDecoded(c, "name")

	var body struct {
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		ScheduleType    string `json:"schedule_type"`
		CronExpr        string `json:"cron_expr"`
		IntervalSeconds int    `json:"interval_seconds"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	if body.Name == "" {
		return fail(c, fiber.StatusBadRequest, "name is required")
	}
	if body.Prompt == "" {
		return fail(c, fiber.StatusBadRequest, "prompt is required")
	}
	if body.ScheduleType != "cron" && body.ScheduleType != "interval" {
		return fail(c, fiber.StatusBadRequest, "schedule_type must be 'cron' or 'interval'")
	}

	// Look up project
	ctx := context.Background()
	proj, err := db.Client().Project.Query().
		Where(entProject.Name(projectName)).
		Only(ctx)
	if err != nil {
		return fail(c, fiber.StatusNotFound, "project not found")
	}

	sc, err := scheduler.CreateSchedule(proj.ID, body.Name, body.Prompt, body.ScheduleType, body.CronExpr, body.IntervalSeconds)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	// Broadcast SSE event
	s.hub.Broadcast(SSEEvent{Type: "schedule_created", Data: scheduleToMap(sc)})

	return success(c, scheduleToMap(sc))
}

// GET /api/projects/:name/schedules/:id — get a schedule
func (s *Server) handleGetSchedule(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid schedule ID")
	}

	sc, err := scheduler.GetSchedule(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	return success(c, scheduleToMap(sc))
}

// PATCH /api/projects/:name/schedules/:id — update a schedule
func (s *Server) handleUpdateSchedule(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid schedule ID")
	}

	var body struct {
		Name            string `json:"name"`
		Prompt          string `json:"prompt"`
		ScheduleType    string `json:"schedule_type"`
		CronExpr        string `json:"cron_expr"`
		IntervalSeconds int    `json:"interval_seconds"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// If only toggling enabled
	if body.Enabled != nil && body.Name == "" {
		sc, err := scheduler.ToggleEnabled(id, *body.Enabled)
		if err != nil {
			return fail(c, fiber.StatusBadRequest, err.Error())
		}
		s.hub.Broadcast(SSEEvent{Type: "schedule_updated", Data: scheduleToMap(sc)})
		return success(c, scheduleToMap(sc))
	}

	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}

	sc, err := scheduler.UpdateSchedule(id, body.Name, body.Prompt, body.ScheduleType, body.CronExpr, body.IntervalSeconds, enabled)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "schedule_updated", Data: scheduleToMap(sc)})

	return success(c, scheduleToMap(sc))
}

// DELETE /api/projects/:name/schedules/:id — delete a schedule
func (s *Server) handleDeleteSchedule(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid schedule ID")
	}

	if err := scheduler.DeleteSchedule(id); err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	s.hub.Broadcast(SSEEvent{Type: "schedule_deleted", Data: fiber.Map{"id": id}})

	return success(c, fiber.Map{"deleted": true})
}

// POST /api/projects/:name/schedules/:id/run — run a schedule immediately
func (s *Server) handleRunScheduleNow(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid schedule ID")
	}

	task, err := scheduler.RunNow(id)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	if task == nil {
		return fail(c, fiber.StatusBadRequest, "no sheep assigned to project or project not found")
	}

	// Trigger immediate processing
	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	s.hub.Broadcast(SSEEvent{Type: "schedule_triggered", Data: fiber.Map{
		"schedule_id": id,
		"task_id":     task.ID,
	}})

	return success(c, fiber.Map{
		"task_id": task.ID,
	})
}

// GET /api/schedules/preview — preview next run times for a cron expression
func (s *Server) handleSchedulePreview(c *fiber.Ctx) error {
	cronExpr := c.Query("cron")
	if cronExpr == "" {
		return fail(c, fiber.StatusBadRequest, "cron query parameter is required")
	}

	times, err := scheduler.CalcNextRunPreview(cronExpr, 5)
	if err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}

	var formatted []string
	for _, t := range times {
		formatted = append(formatted, t.Format("2006-01-02 15:04:05"))
	}

	return success(c, fiber.Map{
		"cron_expr":  cronExpr,
		"next_runs":  formatted,
	})
}

// scheduleToMap converts a Schedule entity to a response map.
func scheduleToMap(sc *ent.Schedule) fiber.Map {
	m := fiber.Map{
		"id":              sc.ID,
		"name":            sc.Name,
		"prompt":          sc.Prompt,
		"schedule_type":   string(sc.ScheduleType),
		"cron_expr":       sc.CronExpr,
		"interval_seconds": sc.IntervalSeconds,
		"enabled":         sc.Enabled,
		"created_at":      sc.CreatedAt.Format("2006-01-02 15:04:05"),
		"updated_at":      sc.UpdatedAt.Format("2006-01-02 15:04:05"),
	}
	if sc.LastRun != nil {
		m["last_run"] = sc.LastRun.Format("2006-01-02 15:04:05")
	}
	if sc.NextRun != nil {
		m["next_run"] = sc.NextRun.Format("2006-01-02 15:04:05")
	}
	if sc.Edges.Project != nil {
		m["project"] = sc.Edges.Project.Name
	}
	return m
}
