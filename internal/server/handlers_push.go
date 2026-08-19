package server

import (
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/push"
)

type pushSubscribeBody struct {
	Endpoint  string    `json:"endpoint"`
	Keys      push.Keys `json:"keys"`
	UserAgent string    `json:"user_agent"`
}

type pushUnsubscribeBody struct {
	Endpoint string `json:"endpoint"`
}

type pushTestBody struct {
	Endpoint string `json:"endpoint"`
}

// GET /api/push/status
func (s *Server) handlePushStatus(c *fiber.Ctx) error {
	pub, _, err := push.EnsureVAPIDKeys()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to prepare web push keys: "+err.Error())
	}
	n, err := push.Count()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to read subscriptions: "+err.Error())
	}
	return success(c, fiber.Map{
		"enabled":            config.GetBool("webpush_enabled"),
		"notify_on_complete": config.GetBool("webpush_notify_on_complete"),
		"notify_on_fail":     config.GetBool("webpush_notify_on_fail"),
		"vapid_public_key":   pub,
		"subscription_count": n,
	})
}

// POST /api/push/subscribe
func (s *Server) handlePushSubscribe(c *fiber.Ctx) error {
	if _, _, err := push.EnsureVAPIDKeys(); err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to prepare web push keys: "+err.Error())
	}

	var body pushSubscribeBody
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	ua := strings.TrimSpace(body.UserAgent)
	if ua == "" {
		ua = strings.TrimSpace(c.Get("User-Agent"))
	}
	sub := push.Subscription{
		Endpoint:  strings.TrimSpace(body.Endpoint),
		Keys:      body.Keys,
		UserAgent: ua,
	}
	if err := push.Upsert(sub); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	n, _ := push.Count()
	return success(c, fiber.Map{"subscribed": true, "subscription_count": n})
}

// POST /api/push/unsubscribe
func (s *Server) handlePushUnsubscribe(c *fiber.Ctx) error {
	var body pushUnsubscribeBody
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if err := push.Remove(strings.TrimSpace(body.Endpoint)); err != nil {
		return fail(c, fiber.StatusBadRequest, err.Error())
	}
	n, _ := push.Count()
	return success(c, fiber.Map{"subscribed": false, "subscription_count": n})
}

// POST /api/push/test
func (s *Server) handlePushTest(c *fiber.Ctx) error {
	if !config.GetBool("webpush_enabled") {
		return fail(c, fiber.StatusBadRequest, "web push is disabled")
	}
	var body pushTestBody
	if err := c.BodyParser(&body); err != nil && len(c.Body()) > 0 {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	payload := push.TestPayload()
	var (
		result push.SendResult
		err    error
	)
	if strings.TrimSpace(body.Endpoint) != "" {
		result, err = push.SendToEndpoint(strings.TrimSpace(body.Endpoint), payload, false)
	} else {
		result, err = push.SendToAll(payload, false)
	}
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to send test push: "+err.Error())
	}
	return success(c, result)
}
