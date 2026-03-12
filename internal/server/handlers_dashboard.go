package server

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/dashboard
func (s *Server) handleDashboard(c *fiber.Ctx) error {
	ctx := context.Background()
	client := db.Client()

	// 1. Sheep list with project info
	sheepList, err := worker.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type sheepItem struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Provider string `json:"provider"`
		Project  string `json:"project,omitempty"`
	}
	var sheepItems []sheepItem
	for _, sh := range sheepList {
		item := sheepItem{
			Name:     sh.Name,
			Status:   string(sh.Status),
			Provider: string(sh.Provider),
		}
		if sh.Edges.Project != nil {
			item.Project = sh.Edges.Project.Name
		}
		sheepItems = append(sheepItems, item)
	}

	// 2. Task counts (all-time)
	counts, err := queue.CountByStatus()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// 3. Today's task counts
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	todayCompleted, _ := client.Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusCompleted),
			entTask.CreatedAtGTE(todayStart),
		).Count(ctx)

	todayFailed, _ := client.Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusFailed),
			entTask.CreatedAtGTE(todayStart),
		).Count(ctx)

	todayTotal, _ := client.Task.Query().
		Where(entTask.CreatedAtGTE(todayStart)).
		Count(ctx)

	// 4. Recent tasks (last 10)
	recentTasks, err := client.Task.Query().
		WithSheep().
		WithProject().
		Order(ent.Desc(entTask.FieldCreatedAt)).
		Limit(10).
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type taskItem struct {
		ID        int    `json:"id"`
		Prompt    string `json:"prompt"`
		Status    string `json:"status"`
		Summary   string `json:"summary,omitempty"`
		Error     string `json:"error,omitempty"`
		Sheep     string `json:"sheep,omitempty"`
		Project   string `json:"project,omitempty"`
		CreatedAt string `json:"created_at"`
	}

	var recentItems []taskItem
	for _, t := range recentTasks {
		item := taskItem{
			ID:        t.ID,
			Prompt:    truncate(t.Prompt, 100),
			Status:    string(t.Status),
			Summary:   truncate(t.Summary, 120),
			Error:     truncate(t.Error, 120),
			CreatedAt: t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if t.Edges.Sheep != nil {
			item.Sheep = t.Edges.Sheep.Name
		}
		if t.Edges.Project != nil {
			item.Project = t.Edges.Project.Name
		}
		recentItems = append(recentItems, item)
	}

	// 5. Projects count
	projects, err := project.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Sheep counts
	working := 0
	idle := 0
	errCount := 0
	for _, sh := range sheepList {
		switch sh.Status {
		case "working":
			working++
		case "idle":
			idle++
		case "error":
			errCount++
		}
	}

	return success(c, map[string]interface{}{
		"sheep": map[string]interface{}{
			"total":   len(sheepList),
			"working": working,
			"idle":    idle,
			"error":   errCount,
			"list":    sheepItems,
		},
		"tasks": map[string]interface{}{
			"pending":   counts[entTask.StatusPending],
			"running":   counts[entTask.StatusRunning],
			"completed": counts[entTask.StatusCompleted],
			"failed":    counts[entTask.StatusFailed],
			"stopped":   counts[entTask.StatusStopped],
		},
		"today": map[string]interface{}{
			"total":     todayTotal,
			"completed": todayCompleted,
			"failed":    todayFailed,
		},
		"recent_tasks": recentItems,
		"projects":     len(projects),
	})
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
