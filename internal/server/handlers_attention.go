package server

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Needs-attention API (#599).
//
// GET  /api/shepherd/attention        — run CollectAttention and return the
//                                       un-acked items (severity desc). The
//                                       Shepherd screen's panel and (stage
//                                       #600) the patrol loop share this one
//                                       data pass.
// POST /api/shepherd/attention/ack    — body {"fingerprint": "..."} stores an
//                                       ack so the item disappears from later
//                                       collections until its fingerprint
//                                       changes.
//
// The collection is cheap (one query per kind) but not free, so the panel
// calls it on mount + SSE-debounced refreshes rather than on a timer.

// GET /api/shepherd/attention
func (s *Server) handleGetAttention(c *fiber.Ctx) error {
	items, err := manager.CollectAttention(time.Now())
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}
	if items == nil {
		items = []manager.AttentionItem{}
	}
	return success(c, map[string]interface{}{
		"items": items,
		"count": len(items),
	})
}

// POST /api/shepherd/attention/ack
func (s *Server) handleAckAttention(c *fiber.Ctx) error {
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if err := manager.AckAttention(body.Fingerprint); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	return success(c, map[string]interface{}{"fingerprint": body.Fingerprint})
}

// POST /api/sheep/:name/reset — the web-side circuit-breaker reset (#599).
// The CLI has `sheep reset` but the API had no counterpart, so the panel's
// "서킷 초기화" button had nothing to call. Body is optional:
//
//	{"clear_session": true} additionally clears the sheep's session id.
//
// Reset refuses (409/400) when the sheep still owns a live running task; the
// error text explains why. On success the processor is nudged so a freshly
// untripped sheep dispatches its pending work without waiting for the next
// tick.
func (s *Server) handleResetSheep(c *fiber.Ctx) error {
	name := paramDecoded(c, "name")

	var body struct {
		ClearSession bool `json:"clear_session"`
	}
	// Empty body is allowed — ResetCircuit is the default for this endpoint.
	_ = c.BodyParser(&body)

	result, err := worker.Reset(name, worker.ResetOptions{
		ClearSession: body.ClearSession,
		ResetCircuit: true,
	})
	if err != nil {
		return fail(c, fiber.StatusConflict, err.Error())
	}

	if s.processor != nil {
		s.processor.ProcessPendingNow()
	}

	return success(c, map[string]interface{}{
		"name":                 result.Name,
		"previous_status":      string(result.PreviousStatus),
		"new_status":           string(result.NewStatus),
		"already_idle":         result.AlreadyIdle,
		"cleared_session":      result.ClearedSession,
		"reset_circuit":        result.ResetCircuitFailures,
		"cleared_orphan_tasks": result.ClearedOrphanTasks,
	})
}
