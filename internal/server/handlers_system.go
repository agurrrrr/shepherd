package server

import (
	"os"
	"os/exec"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// GET /api/system/status
func (s *Server) handleSystemStatus(c *fiber.Ctx) error {
	sheepList, err := worker.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	projects, err := project.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	counts, err := queue.CountByStatus()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// Count sheep by status
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
		"sheep": map[string]int{
			"total":   len(sheepList),
			"working": working,
			"idle":    idle,
			"error":   errCount,
		},
		"projects": len(projects),
		"tasks": map[string]int{
			"pending":   counts[task.StatusPending],
			"running":   counts[task.StatusRunning],
			"completed": counts[task.StatusCompleted],
			"failed":    counts[task.StatusFailed],
		},
		"sse_clients": s.hub.ClientCount(),
	})
}

// GET /api/config
func (s *Server) handleGetConfig(c *fiber.Ctx) error {
	// Return safe config values only (no password hash, jwt secret)
	return success(c, map[string]interface{}{
		"language":         config.GetString("language"),
		"default_provider": config.GetString("default_provider"),
		"workspace_path":   config.GetString("workspace_path"),
		"server_port":      config.GetInt("server_port"),
		"server_host":      config.GetString("server_host"),
		"max_sheep":        config.GetInt("max_sheep"),
		"auto_approve":      config.GetBool("auto_approve"),
		"session_reuse":      config.GetBool("session_reuse"),
		"include_task_history": config.GetBool("include_task_history"),
		"include_mcp_guide":   config.GetBool("include_mcp_guide"),
	})
}

// PATCH /api/config
func (s *Server) handleUpdateConfig(c *fiber.Ctx) error {
	var body map[string]interface{}
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	// Whitelist of updatable config keys
	allowed := map[string]bool{
		"language":         true,
		"default_provider": true,
		"workspace_path":   true,
		"max_sheep":        true,
		"auto_approve":      true,
		"session_reuse":      true,
		"include_task_history": true,
		"include_mcp_guide":   true,
	}

	for key, value := range body {
		if !allowed[key] {
			continue
		}
		if err := config.Set(key, value); err != nil {
			return fail(c, fiber.StatusInternalServerError, "failed to save config: "+err.Error())
		}

		// Apply language change immediately
		if key == "language" {
			if lang, ok := value.(string); ok {
				i18n.Init(lang)
			}
		}
	}

	return success(c, nil)
}

// POST /api/system/restart
func (s *Server) handleRestart(c *fiber.Ctx) error {
	// Respond before restarting
	err := success(c, map[string]interface{}{"restarting": true})

	go func() {
		// Wait for response to be sent
		time.Sleep(500 * time.Millisecond)

		exe, exeErr := os.Executable()
		if exeErr != nil {
			return
		}

		// Close all SSE connections first to unblock Shutdown()
		s.hub.CloseAll()

		// Shutdown current server to release the port
		s.Shutdown()

		// Small delay to ensure port is released
		time.Sleep(200 * time.Millisecond)

		// Start new process detached from current session
		child := exec.Command(exe, "serve-foreground")
		child.Stdout = nil
		child.Stderr = nil
		child.Stdin = nil
		detachProcess(child)
		if startErr := child.Start(); startErr != nil {
			// If start fails, exit anyway (user can restart manually)
			os.Exit(1)
		}

		os.Exit(0)
	}()

	return err
}
