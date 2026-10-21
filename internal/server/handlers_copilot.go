package server

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/embedded"
)

// POST /api/config/copilot/device/start
// Starts the GitHub OAuth device flow for a Copilot subscription.
func (s *Server) handleCopilotDeviceStart(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 20*time.Second)
	defer cancel()

	device, err := embedded.StartCopilotDeviceFlow(ctx)
	if err != nil {
		return fail(c, fiber.StatusBadGateway, err.Error())
	}
	return success(c, map[string]interface{}{
		"device_code":      device.DeviceCode,
		"user_code":        device.UserCode,
		"verification_uri": device.VerificationURI,
		"expires_in":       device.ExpiresIn,
		"interval":         device.Interval,
	})
}

// POST /api/config/copilot/device/poll
// One poll of the device flow. Returns status=authorized plus the GitHub token
// once the operator approves the request in GitHub.
func (s *Server) handleCopilotDevicePoll(c *fiber.Ctx) error {
	var body struct {
		DeviceCode string `json:"device_code"`
	}
	if err := c.BodyParser(&body); err != nil || body.DeviceCode == "" {
		return fail(c, fiber.StatusBadRequest, "device_code is required")
	}

	ctx, cancel := context.WithTimeout(c.UserContext(), 20*time.Second)
	defer cancel()

	result, err := embedded.PollCopilotDeviceFlow(ctx, body.DeviceCode)
	if err != nil {
		return fail(c, fiber.StatusBadGateway, err.Error())
	}
	resp := map[string]interface{}{"status": result.Status}
	if result.Message != "" {
		resp["message"] = result.Message
	}
	if result.Status == embedded.CopilotPollAuthorized {
		resp["github_token"] = result.GitHubToken
	}
	return success(c, resp)
}

// POST /api/config/copilot/models
// Lists Copilot chat models with their per-model context/output limits. The
// GitHub token comes from the request body (fresh device flow) or, when id is
// set, from the stored endpoint (refresh after the token was masked in GET).
func (s *Server) handleCopilotModels(c *fiber.Ctx) error {
	var body struct {
		GitHubToken string `json:"github_token"`
		ID          string `json:"id"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if body.GitHubToken == "" && body.ID != "" {
		ep, err := config.GetEmbeddedEndpointByID(body.ID)
		if err != nil {
			return fail(c, fiber.StatusInternalServerError, "failed to load embedded config: "+err.Error())
		}
		if ep == nil {
			return fail(c, fiber.StatusNotFound, "endpoint not found: "+body.ID)
		}
		body.GitHubToken = ep.GitHubToken
	}
	if body.GitHubToken == "" {
		return fail(c, fiber.StatusBadRequest, "github_token or endpoint id is required")
	}

	ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
	defer cancel()

	models, apiBase, err := embedded.ListCopilotModels(ctx, body.GitHubToken)
	if err != nil {
		return fail(c, fiber.StatusBadGateway, err.Error())
	}

	out := make([]map[string]interface{}, 0, len(models))
	for _, m := range models {
		out = append(out, map[string]interface{}{
			"id":                    m.ID,
			"name":                  m.Name,
			"context_window_tokens": m.ContextWindowTokens,
			"max_prompt_tokens":     m.MaxPromptTokens,
			"max_output_tokens":     m.MaxOutputTokens,
			"context_tokens":        m.ContextTokens(),
			"supports_tools":        m.SupportsTools,
			"supports_vision":       m.SupportsVision,
			"api":                   m.API,
		})
	}
	return success(c, map[string]interface{}{
		"models":   out,
		"api_base": apiBase,
	})
}
