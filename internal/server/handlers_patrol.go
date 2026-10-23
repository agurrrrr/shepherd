package server

import (
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/manager"
)

// Patrol API (#600).
//
// GET  /api/shepherd/patrols?limit=  — newest patrol records first: when the
//                                      code looked, how many findings were new,
//                                      which shepherd task it woke (plus that
//                                      task's first summary line), and why a
//                                      wake was skipped.
// POST /api/shepherd/patrols/run     — run one pass right now ("지금 순찰").
//                                      Same rules as the ticker pass: it obeys
//                                      shepherd_patrol_wake, the pending
//                                      patrol guard, and the daily wake cap.
//
// The record is stored for every pass, including passes that found nothing,
// so the screen can show when the system was last checked. Rows older than 30
// days are pruned by the pass itself.

// GET /api/shepherd/patrols
func (s *Server) handleListPatrols(c *fiber.Ctx) error {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return fail(c, fiber.StatusBadRequest, "limit must be a positive integer")
		}
		if n > 100 {
			n = 100 // the screen lists a page, not the whole month
		}
		limit = n
	}

	records, err := manager.ListPatrols(limit)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	return success(c, map[string]interface{}{
		"patrols": records,
		"count":   len(records),
	})
}

// POST /api/shepherd/patrols/run
func (s *Server) handleRunPatrol(c *fiber.Ctx) error {
	rec, err := manager.RunPatrolPass(time.Now())
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// A woken patrol task should start without waiting for the next tick —
	// same nudge the attention actions and review wakes use.
	if rec.TaskID > 0 && s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{"patrol": rec})
}
