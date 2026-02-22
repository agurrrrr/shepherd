package server

import (
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// AuthMiddleware creates a JWT authentication middleware.
// jwtSecret must not be empty — the server should ensure a secret is configured before starting.
func AuthMiddleware(jwtSecret string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Reject all requests if JWT secret is not configured
		if jwtSecret == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"message": "authentication not configured",
			})
		}

		var tokenString string

		// 1. Try Authorization header: "Bearer <token>"
		auth := c.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			tokenString = strings.TrimPrefix(auth, "Bearer ")
		}

		// 2. For SSE endpoints, also accept ?token= query param
		// (EventSource API cannot set custom headers)
		if tokenString == "" {
			tokenString = c.Query("token")
		}

		if tokenString == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"message": "missing authentication token",
			})
		}

		// Validate token
		claims, err := ValidateToken(tokenString, jwtSecret)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"message": "invalid or expired token",
			})
		}

		// Ensure it's an access token
		if claims.Type != "access" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"success": false,
				"message": "invalid token type",
			})
		}

		c.Locals("username", claims.Username)
		return c.Next()
	}
}

// CORSMiddleware configures CORS headers.
// corsOrigin can be a single origin, comma-separated origins, or empty/"*" for wildcard.
func CORSMiddleware(corsOrigin string) fiber.Handler {
	// Resolve: flag > env > default "*"
	if corsOrigin == "" {
		corsOrigin = os.Getenv("SHEPHERD_CORS_ORIGIN")
	}
	if corsOrigin == "" {
		corsOrigin = "*"
	}

	// Parse allowed origins into a set for fast lookup
	var allowAll bool
	allowedOrigins := make(map[string]bool)
	if corsOrigin == "*" {
		allowAll = true
	} else {
		for _, o := range strings.Split(corsOrigin, ",") {
			o = strings.TrimSpace(o)
			if o != "" {
				allowedOrigins[o] = true
			}
		}
	}

	return func(c *fiber.Ctx) error {
		origin := c.Get("Origin")

		if allowAll {
			c.Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && allowedOrigins[origin] {
			c.Set("Access-Control-Allow-Origin", origin)
			c.Set("Vary", "Origin")
		}

		c.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")

		if c.Method() == "OPTIONS" {
			return c.SendStatus(fiber.StatusNoContent)
		}

		return c.Next()
	}
}
