package server

import (
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/manager"
)

// Shepherd request API (#601).
//
// GET  /api/shepherd/requests?status=pending|approved|rejected|all&limit=
//     — the panel's "목자 요청" list, newest first. Default status: pending.
// POST /api/shepherd/requests/:id/approve
//     — runs the kind's action server-side (stop_task → queue.StopTaskByID,
//       execute_issue → watched issue.Execute, delete_issue → issue.Delete,
//       other → record only), stores the outcome in `result`, and enqueues a
//       shepherd review task with the decision.
// POST /api/shepherd/requests/:id/reject {note}
//     — records the refusal (no action runs) and still wakes the shepherd so
//       it learns the answer instead of waiting forever.
//
// Approving/rejecting an already-decided request is a 409; a missing id is a
// 404. Target problems (vanished task/issue) do NOT fail the call — they land
// in `result` so one stale row cannot wedge the panel.

// GET /api/shepherd/requests
func (s *Server) handleListShepherdRequests(c *fiber.Ctx) error {
	status := c.Query("status", manager.RequestPending)
	limit, err := strconv.Atoi(c.Query("limit", "0"))
	if err != nil {
		limit = 0
	}

	recs, err := manager.ListShepherdRequests(status, limit)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	return success(c, map[string]interface{}{
		"requests": recs,
		"count":    len(recs),
	})
}

// POST /api/shepherd/requests/:id/approve
func (s *Server) handleApproveShepherdRequest(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request ID")
	}

	rec, err := manager.ApproveShepherdRequest(id)
	if err != nil {
		return requestDecisionFail(c, id, err)
	}
	return success(c, map[string]interface{}{"request": rec})
}

// POST /api/shepherd/requests/:id/reject
func (s *Server) handleRejectShepherdRequest(c *fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request ID")
	}

	var body struct {
		Note string `json:"note"`
	}
	_ = c.BodyParser(&body) // empty body is allowed

	rec, err := manager.RejectShepherdRequest(id, body.Note)
	if err != nil {
		return requestDecisionFail(c, id, err)
	}
	return success(c, map[string]interface{}{"request": rec})
}

// requestDecisionFail maps the two expected handler-level errors: not found
// (404) and already decided (409). Anything else is a server fault (500).
func requestDecisionFail(c *fiber.Ctx, id int, err error) error {
	switch {
	case err == nil:
		return nil
	default:
		msg := err.Error()
		switch {
		case strings.Contains(msg, "not found"):
			return fail(c, fiber.StatusNotFound, msg)
		case strings.Contains(msg, "already"):
			return fail(c, fiber.StatusConflict, msg)
		default:
			return fail(c, fiber.StatusInternalServerError, msg)
		}
	}
}
