package server

import (
	"net"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// handleMCPProxy dispatches a browser-tool call forwarded from a stateless
// `shepherd mcp` child. Authentication: shared X-MCP-Token from runtime.json
// (regenerated each daemon start), plus a defence-in-depth localhost check.
func (s *Server) handleMCPProxy(c *fiber.Ctx) error {
	// Loopback-only — token is the real check, but we refuse to even consider
	// non-local callers since this endpoint hands out arbitrary tool execution.
	if !isLoopback(c.IP()) {
		return fail(c, fiber.StatusForbidden, "internal API allowed from loopback only")
	}
	if s.mcpToken == "" {
		return fail(c, fiber.StatusServiceUnavailable, "mcp proxy not configured")
	}
	if c.Get("X-MCP-Token") != s.mcpToken {
		return fail(c, fiber.StatusUnauthorized, "invalid mcp token")
	}
	if s.mcpInner == nil {
		return fail(c, fiber.StatusServiceUnavailable, "mcp server not initialized")
	}

	var body struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if body.Tool == "" {
		return fail(c, fiber.StatusBadRequest, "tool name required")
	}

	result, err := s.mcpInner.ExecuteTool(body.Tool, body.Args)
	if err != nil {
		// 200 with success:false so the forwarder gets a structured error
		// instead of having to parse arbitrary HTTP error bodies.
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"success": false,
			"error":   err.Error(),
		})
	}
	return success(c, result)
}

// isLoopback reports whether the given IP belongs to a loopback interface.
// Accepts IPv4 (127.0.0.0/8) and IPv6 (::1).
func isLoopback(ipStr string) bool {
	ipStr = strings.TrimSpace(ipStr)
	if ipStr == "" {
		return false
	}
	// fiber sometimes returns "ip:port" via X-Forwarded-For-like helpers;
	// strip a trailing port if present.
	if h, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = h
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}
