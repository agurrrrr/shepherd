package server

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/taskrevert"
)

// POST /api/tasks/:id/revert — revert a finished task's working-tree changes.
// Body: {"dry_run": true|false}
func (s *Server) handleRevertTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	var body struct {
		DryRun bool `json:"dry_run"`
	}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&body); err != nil {
			return fail(c, fiber.StatusBadRequest, "invalid request body")
		}
	}

	result, err := taskrevert.Revert(context.Background(), t, body.DryRun)
	if err != nil {
		return respondRevertError(c, err)
	}

	if !body.DryRun && result.Changed {
		s.broadcastTaskUpdate(t, result.RevertedAt)
	}
	return success(c, result)
}

// POST /api/tasks/:id/revert/undo — restore the pre-revert working tree.
func (s *Server) handleUndoRevertTask(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid task ID")
	}

	t, err := queue.GetTask(id)
	if err != nil {
		return fail(c, fiber.StatusNotFound, err.Error())
	}

	result, err := taskrevert.Undo(context.Background(), t)
	if err != nil {
		return respondRevertError(c, err)
	}

	s.broadcastTaskUpdate(t, time.Time{})
	return success(c, result)
}

func respondRevertError(c *fiber.Ctx, err error) error {
	var revertErr *taskrevert.Error
	if errors.As(err, &revertErr) {
		payload := fiber.Map{
			"success": false,
			"message": revertErr.Message,
		}
		if len(revertErr.Conflicts) > 0 {
			payload["conflicts"] = revertErr.Conflicts
		}
		return c.Status(revertErr.Status).JSON(payload)
	}
	return fail(c, fiber.StatusInternalServerError, err.Error())
}

// broadcastTaskUpdate lets other tabs refresh a task whose revert state
// changed. It uses the same event envelope as the other task events.
func (s *Server) broadcastTaskUpdate(t *ent.Task, revertedAt time.Time) {
	if s.hub == nil || t == nil {
		return
	}
	data := map[string]interface{}{
		"task_id": t.ID,
	}
	if !revertedAt.IsZero() {
		data["reverted_at"] = revertedAt.Format("2006-01-02 15:04:05")
	} else {
		data["reverted_at"] = ""
	}
	if t.Edges.Sheep != nil {
		data["sheep_name"] = t.Edges.Sheep.Name
	}
	if t.Edges.Project != nil {
		data["project_name"] = t.Edges.Project.Name
	}
	s.hub.Broadcast(SSEEvent{Type: "task_update", Data: data})
}
