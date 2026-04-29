package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/google/uuid"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/mcp"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/scheduler"
)

// Version can be set by the caller to reflect in /api/health.
var Version = "0.2.0"

// Server is the Fiber HTTP server with SSE hub.
type Server struct {
	app       *fiber.App
	hub       *SSEHub
	processor *queue.Processor
	scheduler *scheduler.Scheduler

	// In-process MCP server used by /api/_internal/mcp/call to dispatch
	// browser tool calls forwarded from stateless `shepherd mcp` children.
	// Browser sessions live in this daemon's memory.
	mcpInner *mcp.Server
	mcpToken string
}

// New creates a new Server with routes configured.
// webFS is an optional embedded filesystem for serving the Svelte SPA (can be nil for dev mode).
// corsOrigin controls CORS allowed origins (comma-separated, or "*" for all, or "" to use env/default).
func New(processor *queue.Processor, sched *scheduler.Scheduler, webFS fs.FS, corsOrigin string) *Server {
	app := fiber.New(fiber.Config{
		AppName:               "Shepherd API",
		DisableStartupMessage: true,
		UnescapePath:          true,             // URL-decode path params (한글 등 비ASCII 문자 지원)
		BodyLimit:             50 * 1024 * 1024, // 50MB for file uploads
		ReadBufferSize:        16384,            // 16KB — fasthttp default 4KB causes 431 on browsers with large headers
	})

	hub := NewSSEHub()
	s := &Server{
		app:       app,
		hub:       hub,
		processor: processor,
		scheduler: sched,
		mcpInner:  mcp.NewServer(false), // full mode — browser handlers live here
	}

	// Global middleware
	app.Use(CORSMiddleware(corsOrigin))

	// Public routes (no auth required)
	app.Get("/api/health", s.handleHealth)
	app.Post("/api/auth/login", s.handleLogin)
	app.Post("/api/auth/refresh", s.handleRefresh)

	// Internal MCP proxy — token-authenticated, localhost-only by intent.
	// Stateless `shepherd mcp` children forward browser calls here so that
	// chrome processes survive past the per-call lifetime of the child.
	app.Post("/api/_internal/mcp/call", s.handleMCPProxy)

	// Authenticated routes
	jwtSecret := config.GetString("auth_jwt_secret")
	api := app.Group("/api", AuthMiddleware(jwtSecret))

	// SSE event stream
	api.Get("/events", s.handleSSE)

	// Dashboard (aggregated data for home page)
	api.Get("/dashboard", s.handleDashboard)

	// System
	api.Get("/system/status", s.handleSystemStatus)
	api.Post("/system/restart", s.handleRestart)
	api.Get("/config", s.handleGetConfig)
	api.Patch("/config", s.handleUpdateConfig)
	api.Get("/config/system-prompt-preview", s.handleSystemPromptPreview)
	api.Get("/config/model-options", s.handleGetModelOptions)

	// MCP registration
	api.Get("/mcp/status", s.handleMCPStatus)
	api.Post("/mcp/register", s.handleMCPRegister)

	// Sheep management
	api.Get("/sheep", s.handleListSheep)
	api.Post("/sheep", s.handleCreateSheep)
	api.Get("/sheep/:name", s.handleGetSheep)
	api.Delete("/sheep/:name", s.handleDeleteSheep)
	api.Patch("/sheep/:name/provider", s.handleUpdateSheepProvider)

	// Project management
	api.Get("/projects", s.handleListProjects)
	api.Post("/projects", s.handleCreateProject)
	api.Get("/projects/:name", s.handleGetProject)
	api.Delete("/projects/:name", s.handleDeleteProject)
	api.Post("/projects/:name/assign", s.handleAssignSheep)
	api.Get("/projects/:name/docs", s.handleListDocs)
	api.Get("/projects/:name/docs-download/*", s.handleDownloadDoc)
	api.Get("/projects/:name/docs/*", s.handleGetDoc)

	// Spec files
	api.Get("/spec-types", s.handleListSpecTypes)
	api.Get("/projects/:name/specs", s.handleListSpecs)
	api.Get("/projects/:name/specs-download/*", s.handleDownloadSpec)
	api.Get("/projects/:name/specs/*", s.handleGetSpec)

	// File browser
	api.Get("/projects/:name/files", s.handleListFiles)
	api.Get("/projects/:name/files/download/*", s.handleDownloadFile)
	api.Get("/projects/:name/files/content/*", s.handleGetFileContent)

	// Git (read-only)
	api.Get("/projects/:name/git/log", s.handleGitLog)
	api.Get("/projects/:name/git/branches", s.handleGitBranches)
	api.Get("/projects/:name/git/commits/:hash", s.handleGitCommitDetail)
	api.Get("/projects/:name/git/commits/:hash/diff", s.handleGitCommitDiff)
	api.Get("/projects/:name/git/changes", s.handleGitChanges)

	// Task management
	api.Get("/tasks", s.handleListTasks)
	api.Post("/tasks", s.handleCreateTask)
	api.Get("/tasks/:id", s.handleGetTask)
	api.Post("/tasks/:id/stop", s.handleStopTask)
	api.Post("/tasks/:id/retry", s.handleRetryTask)
	api.Post("/tasks/:id/retry-from", s.handleRetryFromTask)

	// Schedule management
	api.Get("/schedules", s.handleListAllSchedules)
	api.Get("/schedules/preview", s.handleSchedulePreview)
	api.Get("/projects/:name/schedules", s.handleListProjectSchedules)
	api.Post("/projects/:name/schedules", s.handleCreateSchedule)
	api.Get("/projects/:name/schedules/:id", s.handleGetSchedule)
	api.Patch("/projects/:name/schedules/:id", s.handleUpdateSchedule)
	api.Delete("/projects/:name/schedules/:id", s.handleDeleteSchedule)
	api.Post("/projects/:name/schedules/:id/run", s.handleRunScheduleNow)

	// Skill management
	api.Get("/skills", s.handleListAllSkills)
	api.Post("/skills", s.handleCreateGlobalSkill)
	api.Post("/skills/import", s.handleImportSkill)
	api.Post("/skills/sync-all", s.handleSyncAllSkills)
	api.Get("/skills/:id", s.handleGetSkill)
	api.Patch("/skills/:id", s.handleUpdateSkill)
	api.Delete("/skills/:id", s.handleDeleteSkill)
	api.Get("/skills/:id/export", s.handleExportSkill)
	api.Get("/projects/:name/skills", s.handleListProjectSkills)
	api.Post("/projects/:name/skills", s.handleCreateProjectSkill)

	// File upload
	api.Post("/upload", s.handleUpload)

	// Command (natural language)
	api.Post("/command", s.handleCommand)

	// Serve embedded Svelte SPA (if provided)
	if webFS != nil {
		app.Use("/", filesystem.New(filesystem.Config{
			Root:         http.FS(webFS),
			Index:        "index.html",
			NotFoundFile: "index.html", // SPA fallback
		}))
	}

	return s
}

// SetMCPToken configures the shared secret that the MCP forwarder uses to
// authenticate against /api/_internal/mcp/call. Must be called before the
// daemon advertises runtime.json to disk.
func (s *Server) SetMCPToken(token string) {
	s.mcpToken = token
}

// Listen starts the HTTP server on the given address.
func (s *Server) Listen(addr string) error {
	return s.app.Listen(addr)
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown() error {
	return s.app.Shutdown()
}

// Hub returns the SSE hub for external use.
func (s *Server) Hub() *SSEHub {
	return s.hub
}

// WireProcessorCallbacks connects queue.Processor callbacks to the SSE Hub.
func (s *Server) WireProcessorCallbacks() {
	s.processor.OnTaskStart = func(taskID int, sheepName, projectName, prompt string) {
		s.hub.Broadcast(SSEEvent{Type: "task_start", Data: map[string]interface{}{
			"task_id": taskID, "sheep_name": sheepName,
			"project_name": projectName, "prompt": prompt,
		}})
	}
	s.processor.OnTaskComplete = func(taskID int, sheepName, projectName, summary string) {
		s.hub.Broadcast(SSEEvent{Type: "task_complete", Data: map[string]interface{}{
			"task_id": taskID, "sheep_name": sheepName,
			"project_name": projectName, "summary": summary,
		}})
	}
	s.processor.OnTaskFail = func(taskID int, sheepName, projectName, errMsg string) {
		s.hub.Broadcast(SSEEvent{Type: "task_fail", Data: map[string]interface{}{
			"task_id": taskID, "sheep_name": sheepName,
			"project_name": projectName, "error": errMsg,
		}})
	}
	s.processor.OnTaskStop = func(taskID int, sheepName, projectName, reason string) {
		s.hub.Broadcast(SSEEvent{Type: "task_stop", Data: map[string]interface{}{
			"task_id": taskID, "sheep_name": sheepName,
			"project_name": projectName, "reason": reason,
		}})
	}
	s.processor.OnOutput = func(sheepName, projectName, text string) {
		s.hub.Broadcast(SSEEvent{Type: "output", Data: map[string]interface{}{
			"sheep_name": sheepName, "project_name": projectName, "text": text,
		}})
	}
	s.processor.OnStatusChange = func(sheepName, status string) {
		s.hub.Broadcast(SSEEvent{Type: "status_change", Data: map[string]interface{}{
			"sheep_name": sheepName, "status": status,
		}})
	}
}

// WireSchedulerCallbacks connects Scheduler callbacks to the SSE Hub.
func (s *Server) WireSchedulerCallbacks() {
	if s.scheduler == nil {
		return
	}
	s.scheduler.OnScheduleTriggered = func(scheduleID int, scheduleName, projectName, prompt string, taskID int) {
		s.hub.Broadcast(SSEEvent{Type: "schedule_triggered", Data: map[string]interface{}{
			"schedule_id":   scheduleID,
			"schedule_name": scheduleName,
			"project_name":  projectName,
			"prompt":        prompt,
			"task_id":       taskID,
		}})
	}
}

// --- Auth handlers ---

func (s *Server) handleHealth(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "ok", "version": Version})
}

func (s *Server) handleLogin(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}

	storedUsername := config.GetString("auth_username")
	storedHash := config.GetString("auth_password_hash")

	// If no auth configured, reject
	if storedUsername == "" || storedHash == "" {
		return fail(c, fiber.StatusForbidden, "authentication not configured, run 'shepherd auth setup' first")
	}

	if req.Username != storedUsername {
		return fail(c, fiber.StatusUnauthorized, "invalid credentials")
	}

	if err := ComparePassword(storedHash, req.Password); err != nil {
		return fail(c, fiber.StatusUnauthorized, "invalid credentials")
	}

	jwtSecret := config.GetString("auth_jwt_secret")
	accessTTL, _ := time.ParseDuration(config.GetString("auth_access_ttl"))
	refreshTTL, _ := time.ParseDuration(config.GetString("auth_refresh_ttl"))
	if accessTTL == 0 {
		accessTTL = 24 * time.Hour
	}
	if refreshTTL == 0 {
		refreshTTL = 168 * time.Hour
	}

	accessToken, err := GenerateAccessToken(req.Username, jwtSecret, accessTTL)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to generate token")
	}

	refreshToken, err := GenerateRefreshToken(req.Username, jwtSecret, refreshTTL)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to generate token")
	}

	return c.JSON(LoginResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Username:     req.Username,
	})
}

func (s *Server) handleRefresh(c *fiber.Ctx) error {
	auth := c.Get("Authorization")
	tokenString := ""
	if len(auth) > 7 {
		tokenString = auth[7:]
	}
	if tokenString == "" {
		return fail(c, fiber.StatusUnauthorized, "missing refresh token")
	}

	jwtSecret := config.GetString("auth_jwt_secret")
	claims, err := ValidateToken(tokenString, jwtSecret)
	if err != nil {
		return fail(c, fiber.StatusUnauthorized, "invalid refresh token")
	}
	if claims.Type != "refresh" {
		return fail(c, fiber.StatusUnauthorized, "invalid token type")
	}

	accessTTL, _ := time.ParseDuration(config.GetString("auth_access_ttl"))
	refreshTTL, _ := time.ParseDuration(config.GetString("auth_refresh_ttl"))
	if accessTTL == 0 {
		accessTTL = 24 * time.Hour
	}
	if refreshTTL == 0 {
		refreshTTL = 168 * time.Hour
	}

	newAccess, err := GenerateAccessToken(claims.Username, jwtSecret, accessTTL)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to generate token")
	}
	newRefresh, err := GenerateRefreshToken(claims.Username, jwtSecret, refreshTTL)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, "failed to generate token")
	}

	return c.JSON(LoginResponse{
		AccessToken:  newAccess,
		RefreshToken: newRefresh,
		Username:     claims.Username,
	})
}

// --- SSE handler ---

func (s *Server) handleSSE(c *fiber.Ctx) error {
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	clientID := uuid.New().String()
	ch := s.hub.Subscribe(clientID)

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer s.hub.Unsubscribe(clientID)

		// Send initial heartbeat
		fmt.Fprintf(w, ": connected\n\n")
		w.Flush()

		for event := range ch {
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data)
			if err := w.Flush(); err != nil {
				return // Client disconnected
			}
		}
	})
	return nil
}

// --- Response helpers ---

func success(c *fiber.Ctx, data interface{}) error {
	return c.JSON(fiber.Map{"success": true, "data": data})
}

func fail(c *fiber.Ctx, status int, message string) error {
	return c.Status(status).JSON(fiber.Map{"success": false, "message": message})
}

// paramDecoded returns URL-decoded route parameter (handles %EC%96%91 → 양 etc.)
func paramDecoded(c *fiber.Ctx, key string) string {
	raw := c.Params(key)
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}
