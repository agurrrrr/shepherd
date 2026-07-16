package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/google/uuid"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/discord"
	"github.com/agurrrrr/shepherd/internal/embedded"
	"github.com/agurrrrr/shepherd/internal/llmslots"
	"github.com/agurrrrr/shepherd/internal/magi"
	"github.com/agurrrrr/shepherd/internal/mcp"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/scheduler"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// Version can be set by the caller to reflect in /api/health.
var Version = "0.2.0"

// Server is the Fiber HTTP server with SSE hub.
type Server struct {
	app       *fiber.App
	hub       *SSEHub
	processor *queue.Processor
	scheduler *scheduler.Scheduler
	discord   *discord.TaskNotifier

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
	mcpServer := mcp.NewServer(false) // full mode — browser handlers live here

	// Initialize embedded provider executor (avoids import cycle: worker → mcp → queue → worker)
	initEmbeddedExecutor(mcpServer)

	// Sync llmslots registry with configured endpoints at startup.
	syncEndpointSemaphores()

	// Initialize magi provider executor (avoids import cycle: worker → magi → embedded)
	initMagiExecutor(mcpServer)

	s := &Server{
		app:       app,
		hub:       hub,
		processor: processor,
		scheduler: sched,
		discord:   discord.NewTaskNotifier(),
		mcpInner:  mcpServer,
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

	// Embedded provider endpoints
	api.Get("/config/embedded", s.handleGetEmbeddedEndpoints)
	api.Post("/config/embedded", s.handleCreateEmbeddedEndpoint)
	api.Put("/config/embedded/:id", s.handleUpdateEmbeddedEndpoint)
	api.Delete("/config/embedded/:id", s.handleDeleteEmbeddedEndpoint)
	api.Post("/config/embedded/:id/set-active", s.handleSetActiveEndpoint)
	api.Post("/config/embedded/test", s.handleTestEmbeddedEndpoint)

	// MAGI consensus config (stored in embedded.yaml magi section)
	api.Get("/config/magi", s.handleGetMagiConfig)
	api.Put("/config/magi", s.handleUpdateMagiConfig)

	// Backup & portable task history
	api.Get("/settings/db-backup", s.handleDownloadDBBackup)
	api.Get("/settings/tasks-export", s.handleExportTasks)
	api.Post("/settings/tasks-import-preview", s.handleImportTasksPreview)
	api.Post("/settings/tasks-import", s.handleImportTasks)

	// MCP registration (Shepherd → external providers)
	api.Get("/mcp/status", s.handleMCPStatus)
	api.Post("/mcp/register", s.handleMCPRegister)

	// External MCP server management (Shepherd as MCP client)
	api.Get("/mcp/servers", s.handleListMCPServers)
	api.Post("/mcp/servers", s.handleCreateMCPServer)
	api.Put("/mcp/servers/:id", s.handleUpdateMCPServer)
	api.Delete("/mcp/servers/:id", s.handleDeleteMCPServer)

	// Per-project MCP server settings
	api.Get("/projects/:name/mcp-servers", s.handleGetProjectMCPServers)
	api.Put("/projects/:name/mcp-servers", s.handleUpdateProjectMCPServers)

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

	// File browser
	api.Get("/projects/:name/files", s.handleListFiles)
	api.Get("/projects/:name/files/download/*", s.handleDownloadFile)
	api.Get("/projects/:name/files/content/*", s.handleGetFileContent)

	// Git
	api.Get("/projects/:name/git/log", s.handleGitLog)
	api.Get("/projects/:name/git/branches", s.handleGitBranches)
	api.Get("/projects/:name/git/commits/:hash", s.handleGitCommitDetail)
	api.Get("/projects/:name/git/commits/:hash/diff", s.handleGitCommitDiff)
	api.Get("/projects/:name/git/changes", s.handleGitChanges)
	api.Post("/projects/:name/git/stage", s.handleGitStage)
	api.Post("/projects/:name/git/unstage", s.handleGitUnstage)
	api.Post("/projects/:name/git/commit", s.handleGitCommit)
	api.Post("/projects/:name/git/push", s.handleGitPush)

	// Task management
	api.Get("/tasks", s.handleListTasks)
	api.Post("/tasks", s.handleCreateTask)
	api.Get("/tasks/:id", s.handleGetTask)
	api.Post("/tasks/:id/stop", s.handleStopTask)
	api.Post("/tasks/:id/retry", s.handleRetryTask)
	api.Post("/tasks/:id/retry-from", s.handleRetryFromTask)

	// Issue management
	api.Get("/projects/:name/issues", s.handleListIssues)
	api.Post("/projects/:name/issues", s.handleCreateIssue)
	api.Get("/projects/:name/issues/:id", s.handleGetIssue)
	api.Patch("/projects/:name/issues/:id", s.handleUpdateIssue)
	api.Delete("/projects/:name/issues/:id", s.handleDeleteIssue)
	api.Post("/projects/:name/issues/:id/execute", s.handleExecuteIssue)

	// Prompt injection (mid-execution)
	api.Post("/sheep/:name/inject", s.handleInjectPrompt)

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

	// Wiki management
	api.Get("/wiki/pages", s.handleListWikiPages)
	api.Get("/wiki/pages/:slug", s.handleGetWikiPage)
	api.Post("/wiki/pages", s.handleCreateWikiPage)
	api.Put("/wiki/pages/:slug", s.handleUpdateWikiPage)
	api.Delete("/wiki/pages/:slug", s.handleDeleteWikiPage)
	api.Get("/wiki/pages/:slug/versions", s.handleWikiPageVersions)
	api.Post("/wiki/lint", s.handleWikiLint)
	api.Post("/wiki/ingest/:task_id", s.handleWikiIngest)

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
		// Discord notification
		go s.sendDiscordComplete(taskID, sheepName, projectName, summary)
	}
	s.processor.OnTaskFail = func(taskID int, sheepName, projectName, errMsg string) {
		s.hub.Broadcast(SSEEvent{Type: "task_fail", Data: map[string]interface{}{
			"task_id": taskID, "sheep_name": sheepName,
			"project_name": projectName, "error": errMsg,
		}})
		// Discord notification
		go s.discord.SendTaskFail(taskID, sheepName, projectName, errMsg)
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

// sendDiscordComplete sends a Discord notification for task completion.
// It looks up task details from the DB to include cost and modified files.
func (s *Server) sendDiscordComplete(taskID int, sheepName, projectName, summary string) {
	task, err := queue.GetTask(taskID)
	if err != nil {
		return
	}

	s.discord.SendTaskComplete(taskID, sheepName, projectName, summary, task.CostUsd, task.FilesModified)
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

const (
	// handoffAlarmDepth: once a task's context-overflow handoff chain reaches this
	// many links, warn via Discord — a long chain usually means the model keeps
	// running out of context without finishing (a no-progress loop).
	handoffAlarmDepth = 8
	// maxHandoffChain: hard ceiling. Beyond this we stop auto-chaining and fall
	// back to plain trimming, so a stuck task can't spawn follow-ups without bound.
	maxHandoffChain = 12
)

// taskHandoffDepth returns the handoff-chain depth recorded on the given task,
// or 0 when it can't be resolved (unknown id / lookup failure).
func taskHandoffDepth(taskID int) int {
	if taskID == 0 {
		return 0
	}
	t, err := queue.GetTask(taskID)
	if err != nil || t == nil {
		return 0
	}
	return t.HandoffDepth
}

// initEmbeddedExecutor registers the embedded executor with the worker package.
// This bridges the MCP server (with its tool handlers) to the embedded agent loop.
func initEmbeddedExecutor(mcpServer *mcp.Server) {
	worker.SetEmbeddedExecutor(func(
		ctx context.Context,
		sheepName, projectPath string,
		prompt string,
		opts worker.InteractiveOptions,
		cancel context.CancelFunc,
		injectCh <-chan string,
	) (*worker.ExecuteResult, error) {
		// When the project explicitly selects an endpoint (opts.Model holds the
		// endpoint ID), use that one; otherwise fall back to the globally active
		// endpoint.
		var ep *config.EmbeddedEndpoint
		var err error
		if opts.Model != "" {
			ep, err = config.GetEmbeddedEndpointByID(opts.Model)
			if err != nil {
				return nil, fmt.Errorf("embedded config error: %w", err)
			}
			if ep == nil {
				return nil, fmt.Errorf("embedded endpoint %q not found or disabled", opts.Model)
			}
		} else {
			ep, err = config.GetActiveEmbeddedEndpoint()
			if err != nil {
				return nil, fmt.Errorf("embedded config error: %w", err)
			}
			if ep == nil {
				return nil, fmt.Errorf("no active embedded endpoint configured. Add one in Settings > Embedded")
			}
		}
		if ep.BaseURL == "" || ep.Model == "" {
			return nil, fmt.Errorf("embedded endpoint %q is incomplete (base_url and model required)", ep.ID)
		}

		// Resolve project name from path so we can look up per-project MCP settings.
		projectName, projErr := project.GetByPath(projectPath)
		if projErr != nil {
			// Fallback: try to infer from sheep's assigned project
			if projErr2 := projErr; projErr2 != nil {
				projectName = ""
			}
		}

		// Collect MCP tool definitions — always include shepherd built-in tools
		var mcpDefs []embedded.MCPToolDef
		for _, t := range mcp.ListCoreToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}
		for _, t := range mcp.ListWikiToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}
		for _, t := range mcp.ListBrowserToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}

		// Collect external MCP server tools enabled for this project.
		// Each external server is spawned as a stdio child process and communicated
		// with via JSON-RPC. We cache connections in the global MCPClientManager so
		// repeated tasks reuse the same process instead of re-spawning every time.
		var externalServers map[string]*mcp.ExternalMCPServer
		var externalToolNames map[string]bool // for dispatch routing
		if projectName != "" {
			activeServers, activeErr := getProjectActiveMCPServers(projectName)
			if activeErr == nil && len(activeServers) > 0 {
				manager := mcp.GetMCPManager()
				externalServers = make(map[string]*mcp.ExternalMCPServer)
				externalToolNames = make(map[string]bool)

				for _, srv := range activeServers {
					ext, spawnErr := manager.GetOrCreate(srv)
					if spawnErr != nil {
						// Log but don't fail the whole task — some servers may not be installed
						fmt.Printf("[embedded] warning: failed to spawn MCP server %q: %v\n", srv.Name, spawnErr)
						continue
					}
					externalServers[srv.Name] = ext
					for _, t := range ext.Tools() {
						mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
						externalToolNames[t.Name] = true
					}
				}
			}
		}

		// Build project-specific MCP guide for the system prompt.
		// This includes both built-in tools AND any external MCP server tools
		// that are enabled for this project.
		mcpGuide := buildMCPGuide(mcpDefs, projectName)

		// Build system prompt with project-specific MCP guide
		systemPrompt := worker.BuildSystemPromptForEmbedded(sheepName, projectPath, mcpGuide)

		// Dispatcher that routes MCP tool calls:
		// 1. Built-in tools (task_*, get_*, skill_load, wiki_*, browser_*) → mcpServer
		// 2. External MCP server tools → respective ExternalMCPServer
		mcpDispatch := func(name string, args map[string]interface{}) (resultText string, images []embedded.MCPImage, err error) {
			// Recover from panics in MCP tool handlers so a single bad tool call
			// cannot crash the entire shepherd process. Without this, a nil-pointer
			// or index-out-of-range in any tool handler propagates through the
			// goroutine in dispatchTool and kills the daemon (task #6887/#6888).
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("tool %s panicked: %v", name, r)
				}
			}()

			// Check if this is an external MCP tool
			if externalToolNames[name] {
				// Find which server provides this tool
				for _, ext := range externalServers {
					for _, t := range ext.Tools() {
						if t.Name == name {
							// Use the multimodal call so image results (e.g.
							// mobile_take_screenshot) reach the vision model
							// instead of being dropped (task #6684).
							text, imgs, err := ext.CallToolMultimodal(name, args)
							return text, toEmbeddedMCPImages(imgs), err
						}
					}
				}
				return "", nil, fmt.Errorf("external tool %q not found", name)
			}
			// Fall back to built-in shepherd MCP server (text-only result).
			text, err := mcpServer.ExecuteTool(name, args)
			return text, nil, err
		}

		// Create tool registry (used here to derive the tool definitions the model sees)
		toolRegistry := embedded.NewToolRegistry(projectPath, sheepName, mcpDefs, mcpDispatch)
		// Opt-in todo_write (Phase 3-2): must EnableTodo before OpenAIToolDefs
		// so the model sees the tool when embedded_todo_gate is on. Loop-owned
		// registry is enabled separately via ExecuteOptions.TodoGateEnabled.
		todoGate := config.GetBool("embedded_todo_gate")
		if todoGate {
			toolRegistry.EnableTodo()
		}
		toolDefs := toolRegistry.OpenAIToolDefs()

		// Resolve endpoint concurrency limiter from max_concurrent. When
		// max_concurrent > 0, a shared semaphore is created (or reused) via
		// llmslots.Global().Get(). The semaphore is acquired per LLM call in
		// the embedded Client, so the parent loop, MAGI proposers, and
		// sub-agents all share the same per-endpoint capacity.
		var sem *llmslots.Semaphore
		if ep.MaxConcurrent > 0 {
			sem = llmslots.Global().Get(ep.ID, ep.MaxConcurrent)
		}

		// SubagentSpawner callback — creates a read-only sub-agent loop.
		// Each sub-agent gets its own embedded.Run with a filtered (read-only)
		// tool set. Token/cost usage is returned for parent task aggregation
		// (#7461 I3). The sub-agent's ToolRegistry does NOT set a spawner,
		// so spawn_subagents is invisible to it (depth 1 enforcement).
		//
		// subOutMu serializes complete-line emits from parallel sub-agents
		// into the shared parent OnOutput sink (parent LineCoalescer is not
		// concurrent-safe). Per-agent LineCoalescer + [SUB:name] prefix is
		// applied inside wrapSubagentOnOutput (tasks #7562/#7564, MAGI #7209).
		subOutMu := &sync.Mutex{}
		subagentSpawner := func(ctx context.Context, name, subPrompt, endpointID string, maxIter int, onOutput func(string)) (*embedded.SubagentResult, error) {
			// Resolve endpoint — empty endpointID means use the parent's endpoint.
			var subEp *config.EmbeddedEndpoint
			var subEpErr error // local err to avoid shadowing parent scope's err
			if endpointID != "" {
				subEp, subEpErr = config.GetEmbeddedEndpointByID(endpointID)
			} else {
				subEp = ep
			}
			if subEpErr != nil || subEp == nil {
				return nil, fmt.Errorf("subagent endpoint %q not found", endpointID)
			}

			// Build read-only tool set for the sub-agent.
			// Reuse IsAllowedProposerTool to filter MCP tools — same allowlist
			// used by MAGI proposers (read_file/grep/glob + browser + query tools).
			var filteredMCPDefs []embedded.MCPToolDef
			for _, d := range mcpDefs {
				if magi.IsAllowedProposerTool(d.Name) {
					filteredMCPDefs = append(filteredMCPDefs, d)
				}
			}

			// Read-only MCP dispatcher wrapper — rejects write tools at the
			// dispatch level as a second safety net behind the tool definition
			// filtering above.
			subDispatch := func(toolName string, toolArgs map[string]interface{}) (string, []embedded.MCPImage, error) {
				if !magi.IsAllowedProposerTool(toolName) {
					return "", nil, fmt.Errorf("tool %q is not allowed for sub-agents (read-only)", toolName)
				}
				return mcpDispatch(toolName, toolArgs)
			}

			// Sub-agent sheep name for browser session isolation (MAGI PersonaSheepName pattern).
			subSheepName := fmt.Sprintf("%s-sub-%s", sheepName, name)

			subRegistry := embedded.NewToolRegistry(projectPath, subSheepName, filteredMCPDefs, subDispatch)
			subRegistry.SetVision(subEp.Vision)
			// DO NOT call SetSubagentSpawner on sub-registry → depth 1 enforcement.
			// spawn_subagents tool won't appear in OpenAIToolDefs.

			// Sub-agent system prompt: derive from parent's prompt to preserve
			// project context (git config, code style), but add explicit
			// read-only constraints (#7461 M1). The tool-level enforcement
			// (IsAllowedProposerTool + no SetSubagentSpawner) is the real
			// safety mechanism; this prompt is advisory.
			subSystemPrompt := buildSubagentSystemPrompt(systemPrompt, name)

			// Endpoint semaphore — passed via ExecuteOptions.Semaphore (step-03).
			// Shares the same per-endpoint capacity with the parent and MAGI.
			var subSem *llmslots.Semaphore
			if subEp.MaxConcurrent > 0 {
				subSem = llmslots.Global().Get(subEp.ID, subEp.MaxConcurrent)
			}

			if maxIter <= 0 {
				maxIter = 15
			}

			// Prefix every complete body line with [SUB:name] via per-agent
			// LineCoalescer. Lifecycle lines (시작/완료) are emitted by
			// tools.go outside this wrapper — do not double-prefix them.
			wrappedOut, flushOut := wrapSubagentOnOutput(name, onOutput, subOutMu)
			defer flushOut()

			result, runErr := embedded.Run(ctx, embedded.ExecuteOptions{
				SheepName:     subSheepName,
				ProjectPath:   projectPath,
				BaseURL:       subEp.BaseURL,
				APIKey:        subEp.APIKey,
				Model:         subEp.Model,
				SystemPrompt:  subSystemPrompt,
				UserPrompt:    subPrompt,
				Tools:         subRegistry.OpenAIToolDefs(),
				OnOutput:      wrappedOut,
				MaxIterations: maxIter,
				ContextTokens: subEp.ContextTokens,
				Vision:        subEp.Vision,
				MCPDefs:       filteredMCPDefs,
				MCPDispatch:   subDispatch,
				Semaphore:     subSem,
				// No InjectCh, ShouldHandoff, EnqueueFollowUp — sub-agents are short-lived.
			})
			if runErr != nil {
				return nil, runErr
			}

			return &embedded.SubagentResult{
				Content:          result.Result,
				PromptTokens:     result.PromptTokens,
				CompletionTokens: result.CompletionTokens,
				CostUSD:          result.CostUSD,
			}, nil
		}

		toolRegistry.SetSubagentSpawner(subagentSpawner)

		// Rebuild tool definitions now that the spawner is registered —
		// spawn_subagents will appear in the list when HasSubagentSpawner() is true.
		toolDefs = toolRegistry.OpenAIToolDefs()

		// Add spawn_subagents usage guide to the system prompt when the tool
		// is available. BuildSystemPromptForEmbedded doesn't know about the
		// ToolRegistry state, so we append conditionally here (safer than
		// always adding it in the prompt builder).
		if toolRegistry.HasSubagentSpawner() {
			systemPrompt += "\n\n## spawn_subagents — 병렬 서브에이전트 도구\n"
			systemPrompt += "여러 읽기 전용 서브에이전트를 병렬로 실행할 수 있습니다. 각 서브에이전트는 독립된 컨텍스트에서 작동하며, 결과만 부모에게 반환합니다.\n\n"
			systemPrompt += "사용 예:\n"
			systemPrompt += "- 여러 모델로 코드 리뷰 분산\n"
			systemPrompt += "- 여러 디렉토리 동시 탐색\n"
			systemPrompt += "- 여러 접근법으로 리서치 수행\n\n"
			systemPrompt += "제약:\n"
			systemPrompt += "- 서브에이전트는 읽기 전용입니다 (write_file/edit_file/bash 사용 불가).\n"
			systemPrompt += "- 서브에이전트는 자식을 spawn할 수 없습니다 (깊이 1).\n"
			systemPrompt += "- 최대 4개까지 동시 실행 가능합니다.\n"
			systemPrompt += "- 태스크당 최대 3회까지 호출할 수 있습니다.\n"
			systemPrompt += "- 결과는 개별적으로 잘릴 수 있습니다.\n"
		}

		// Run the embedded agent loop
		result, err := embedded.Run(ctx, embedded.ExecuteOptions{
			SheepName:       sheepName,
			ProjectPath:     projectPath,
			BaseURL:         ep.BaseURL,
			APIKey:          ep.APIKey,
			Model:           ep.Model,
			SystemPrompt:    systemPrompt,
			UserPrompt:      prompt,
			Tools:           toolDefs,
			OnOutput:        opts.OnOutput,
			MaxIterations:   ep.MaxIterations,
			ContextTokens:   ep.ContextTokens,
			Vision:          ep.Vision,
			MCPDefs:         mcpDefs,
			MCPDispatch:     mcpDispatch,
			InjectCh:        injectCh,
			TodoGateEnabled: todoGate,
			// Context-overflow handoff: when the conversation outgrows the
			// context window, finish the task with a summary and queue the
			// remaining work as a follow-up instead of trimming old turns.
			// Always prefer handoff over trimming — the follow-up is created with
			// priority 1 (see EnqueueFollowUp), so it runs ahead of any queued
			// pending tasks rather than being pushed to the back of the queue
			// (the old "only when queue is empty" guard is gone). The sole
			// refusal is a runaway guard: once this work has handed itself off
			// maxHandoffChain times it's likely looping without progress, so we
			// stop growing the chain and let plain trimming carry it to the end.
			ShouldHandoff: func() bool {
				return taskHandoffDepth(opts.TaskID) < maxHandoffChain
			},
			EnqueueFollowUp: func(followUpPrompt string) error {
				s, serr := worker.Get(sheepName)
				if serr != nil {
					return serr
				}
				depth := taskHandoffDepth(opts.TaskID) + 1
				projectID := 0
				projectName := ""
				if s.Edges.Project != nil {
					projectID = s.Edges.Project.ID
					projectName = s.Edges.Project.Name
				}
				if _, err := queue.CreateFollowUpTask(followUpPrompt, s.ID, projectID, depth); err != nil {
					return err
				}
				// A deep chain means the model keeps exhausting its context
				// without finishing — alert a human so they can step in.
				if depth >= handoffAlarmDepth {
					go discord.NewTaskNotifier().SendHandoffDepthAlert(sheepName, projectName, depth)
				}
				return nil
			},
			Semaphore: sem,
			// Pass the subagent spawner so Run() can set it on its internal
			// ToolRegistry. Without this, the tool definition is visible to
			// the model (via toolDefs) but dispatch fails with "unknown tool".
			SubagentSpawner: subagentSpawner,
		})
		if err != nil {
			return nil, err
		}

		return &worker.ExecuteResult{
			Result:           result.Result,
			SessionID:        result.SessionID,
			FilesModified:    result.FilesModified,
			CostUSD:          result.CostUSD,
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			Incomplete:       result.Incomplete,
			IncompleteReason: result.IncompleteReason,
		}, nil
	})
}

// buildSubagentSystemPrompt creates a system prompt for a read-only sub-agent.
// It derives from the parent's system prompt to preserve project context
// (git config, code style, project structure), but appends explicit read-only
// constraints that override any earlier write instructions.
//
// Note: This function does not attempt to parse and selectively remove write
// directives from the parent prompt — that would be fragile (natural language
// parsing). Instead, it appends explicit read-only constraints that override
// any earlier write instructions. The tool-level enforcement
// (IsAllowedProposerTool + no SetSubagentSpawner) is the real safety
// mechanism; this prompt is advisory.
func buildSubagentSystemPrompt(parentPrompt, agentName string) string {
	base := parentPrompt

	base += "\n\n## 서브에이전트 제약사항\n"
	base += fmt.Sprintf("당신은 '%s'라는 이름의 읽기 전용 서브에이전트입니다.\n", agentName)
	base += "- 파일을 탐색하고 분석할 수 있지만, 파일을 수정할 수 없습니다 (write_file/edit_file/bash 사용 불가).\n"
	base += "- 브라우저 도구는 사용할 수 있습니다 (세션 격리됨).\n"
	base += "- 최종 답변으로 발견한 내용을 상세히 요약해 주세요. 이 요약이 부모 에이전트에게 전달됩니다.\n"

	return base
}

// syncEndpointSemaphores ensures the llmslots registry has semaphores for
// all configured embedded endpoints. Called at server startup and when
// endpoint configs are updated via API.
//
// Phase 2: uses Resize instead of Get so that runtime config changes to
// max_concurrent take effect without server restart.
func syncEndpointSemaphores() {
	cfg, err := config.LoadEmbeddedConfig()
	if err != nil {
		return
	}
	for _, ep := range cfg.Endpoints {
		if ep.Enabled && ep.MaxConcurrent > 0 {
			llmslots.Global().Resize(ep.ID, ep.MaxConcurrent)
		}
	}
}

// initMagiExecutor registers the magi consensus executor with the worker
// package. This bridges the magi pipeline (proposers → aggregator → verdict)
// to the worker's provider switch. The worker falls back to a single embedded
// run when fewer than 2 proposers succeed (design §5.1).
func initMagiExecutor(mcpServer *mcp.Server) {
	worker.SetMagiExecutor(func(
		ctx context.Context,
		sheepName, projectPath string,
		prompt string,
		opts worker.InteractiveOptions,
		cancel context.CancelFunc,
	) (*worker.ExecuteResult, error) {
		// 1. Load magi config.
		magiCfg, err := config.GetMagiConfig()
		if err != nil {
			return nil, fmt.Errorf("magi config error: %w", err)
		}
		if magiCfg == nil || !magiCfg.Enabled {
			return nil, fmt.Errorf("magi is not configured or disabled. Configure it in Settings > Embedded > MAGI")
		}

		// 2. Validate — hard errors make the config unusable; warnings are advisory.
		embeddedCfg, err := config.LoadEmbeddedConfig()
		if err != nil {
			return nil, fmt.Errorf("load embedded config for magi validation: %w", err)
		}
		errs, _ := config.ValidateMagiConfig(embeddedCfg)
		if len(errs) > 0 {
			return nil, fmt.Errorf("magi config validation failed: %s", strings.Join(errs, "; "))
		}

		// 3. Resolve proposer endpoints → magi.ProposerSpec.
		proposers := make([]magi.ProposerSpec, len(magiCfg.Proposers))
		var firstProposerEP *magi.EndpointRef
		for i, p := range magiCfg.Proposers {
			provider := p.Provider
			if provider == "" {
				provider = "embedded"
			}

			spec := magi.ProposerSpec{
				PersonaKey:   p.Persona,
				DisplayName:  p.DisplayName,
				CustomPrompt: p.CustomPrompt,
				Timeout:      time.Duration(p.TimeoutSeconds) * time.Second,
			}

			switch provider {
			case "claude_cli":
				spec.Provider = magi.ProviderClaudeCLI
				spec.ModelID = p.ModelID
			case "opencode_cli":
				spec.Provider = magi.ProviderOpenCodeCLI
				spec.ModelID = p.ModelID
			case "grok_cli":
				spec.Provider = magi.ProviderGrokCLI
				spec.ModelID = p.ModelID
			default: // "embedded"
				spec.Provider = magi.ProviderEmbedded
				ep, epErr := config.GetEmbeddedEndpointByID(p.EndpointID)
				if epErr != nil {
					return nil, fmt.Errorf("magi proposer %d: config error: %w", i+1, epErr)
				}
				if ep == nil {
					return nil, fmt.Errorf("magi proposer endpoint %q not found or disabled", p.EndpointID)
				}
				ref := magi.EndpointRef{
					ID:            ep.ID,
					BaseURL:       ep.BaseURL,
					APIKey:        ep.APIKey,
					Model:         ep.Model,
					ContextTokens: ep.ContextTokens,
				}
				spec.Endpoint = ref
				if firstProposerEP == nil {
					firstProposerEP = &ref
				}
			}

			proposers[i] = spec
		}

		// 4. Resolve aggregator.
		var aggregator magi.AggregatorSpec
		switch magiCfg.Aggregator.Type {
		case "endpoint":
			ep, epErr := config.GetEmbeddedEndpointByID(magiCfg.Aggregator.EndpointID)
			if epErr != nil {
				return nil, fmt.Errorf("magi aggregator: config error: %w", epErr)
			}
			if ep == nil {
				return nil, fmt.Errorf("magi aggregator endpoint %q not found or disabled", magiCfg.Aggregator.EndpointID)
			}
			aggregator = magi.AggregatorSpec{
				Type:     "endpoint",
				Endpoint: magi.EndpointRef{ID: ep.ID, BaseURL: ep.BaseURL, APIKey: ep.APIKey, Model: ep.Model, ContextTokens: ep.ContextTokens},
			}
			// Fallback to first proposer endpoint (design §7).
			if firstProposerEP != nil {
				aggregator.FallbackEndpoint = *firstProposerEP
			}
		case "claude_cli":
			aggregator = magi.AggregatorSpec{
				Type:    "claude_cli",
				WorkDir: projectPath,
				ModelID: magiCfg.Aggregator.ModelID,
			}
			// Fallback to first proposer endpoint (design §7).
			if firstProposerEP != nil {
				aggregator.FallbackEndpoint = *firstProposerEP
			}
		case "opencode_cli":
			aggregator = magi.AggregatorSpec{
				Type:    "opencode_cli",
				WorkDir: projectPath,
				ModelID: magiCfg.Aggregator.ModelID,
			}
			// Fallback to first proposer endpoint (design §7).
			if firstProposerEP != nil {
				aggregator.FallbackEndpoint = *firstProposerEP
			}
		case "grok_cli":
			aggregator = magi.AggregatorSpec{
				Type:    "grok_cli",
				WorkDir: projectPath,
				ModelID: magiCfg.Aggregator.ModelID,
			}
			// Fallback to first proposer endpoint (design §7).
			if firstProposerEP != nil {
				aggregator.FallbackEndpoint = *firstProposerEP
			}
		default:
			return nil, fmt.Errorf("magi aggregator: unknown type %q", magiCfg.Aggregator.Type)
		}

		// 5. Build the MAGI deliberation base prompt.
		// Phase 1.5: proposers now have read-only tools, so the prompt includes
		// the MCP guide, skills, memory, and custom prompt — same context
		// sections as the embedded agent, but with a MAGI-specific identity.

		// 5a. Resolve project name for per-project MCP settings.
		projectName, _ := project.GetByPath(projectPath)

		// 5b. Collect MCP tool definitions — same pattern as initEmbeddedExecutor.
		var mcpDefs []embedded.MCPToolDef
		for _, t := range mcp.ListCoreToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}
		for _, t := range mcp.ListWikiToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}
		// Browser tools are included in full — navigation, reading, interaction
		// (click/type/select), session lifecycle, capture, and debug — enabling
		// web research and automation for MAGI proposers. All browser tools are
		// permitted by magi.IsAllowedProposerTool() (step 5c below) because each
		// proposer runs in its own isolated browser session (tasks #7138/#7139).
		for _, t := range mcp.ListBrowserToolDefs() {
			mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
		}

		// Collect external MCP server tools enabled for this project.
		var externalServers map[string]*mcp.ExternalMCPServer
		var externalToolNames map[string]bool
		if projectName != "" {
			activeServers, activeErr := getProjectActiveMCPServers(projectName)
			if activeErr == nil && len(activeServers) > 0 {
				manager := mcp.GetMCPManager()
				externalServers = make(map[string]*mcp.ExternalMCPServer)
				externalToolNames = make(map[string]bool)

				for _, srv := range activeServers {
					ext, spawnErr := manager.GetOrCreate(srv)
					if spawnErr != nil {
						fmt.Printf("[magi] warning: failed to spawn MCP server %q: %v\n", srv.Name, spawnErr)
						continue
					}
					externalServers[srv.Name] = ext
					for _, t := range ext.Tools() {
						mcpDefs = append(mcpDefs, toEmbeddedMCPDef(t))
						externalToolNames[t.Name] = true
					}
				}
			}
		}

		// 5c. Filter to the proposer-permitted tool set (Phase 1.5): read/query
		// tools plus the full browser tool set; shared-state mutations dropped.
		var proposerMCPDefs []embedded.MCPToolDef
		for _, d := range mcpDefs {
			if magi.IsAllowedProposerTool(d.Name) {
				proposerMCPDefs = append(proposerMCPDefs, d)
			}
		}

		// 5d. Build MCP guide from the proposer-permitted tool set.
		mcpGuide := buildMCPGuide(proposerMCPDefs, projectName)

		// 5e. Build the system prompt with MAGI identity + permitted tools.
		baseSystem := worker.BuildSystemPromptForMagi(sheepName, projectPath, mcpGuide)

		// 5f. Build the MCP dispatcher (same pattern as initEmbeddedExecutor).
		mcpDispatch := func(name string, args map[string]interface{}) (resultText string, images []embedded.MCPImage, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("tool %s panicked: %v", name, r)
				}
			}()

			// Check if this is an external MCP tool.
			if externalToolNames[name] {
				for _, ext := range externalServers {
					for _, t := range ext.Tools() {
						if t.Name == name {
							text, imgs, err := ext.CallToolMultimodal(name, args)
							return text, toEmbeddedMCPImages(imgs), err
						}
					}
				}
				return "", nil, fmt.Errorf("external tool %q not found", name)
			}
			// Fall back to built-in shepherd MCP server.
			text, err := mcpServer.ExecuteTool(name, args)
			return text, nil, err
		}

		// 5g. Build OpenAI tool definitions from the read-only MCP defs.
		// Native tools (read_file, grep, glob) are added by the ToolRegistry
		// inside each proposer's callEndpoint; we only need the MCP defs here
		// so the ToolRegistry can be constructed with them.
		var toolDefs []embedded.OpenAIToolDef
		// Add native read-only tools.
		nativeReadOnly := []embedded.OpenAIToolDef{
			{
				Type: "function",
				Function: embedded.OpenAIFunction{
					Name:        "read_file",
					Description: "Read the contents of a file. For text files, returns the text. For image files (png/jpeg/gif/webp), when the task has attached images, returns the image for you to view directly. Large text files are returned one page at a time.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path":   map[string]interface{}{"type": "string", "description": "Path to the file"},
							"offset": map[string]interface{}{"type": "number", "description": "Line number to start from (1-indexed)."},
							"limit":  map[string]interface{}{"type": "number", "description": "Maximum number of lines to read."},
						},
						"required": []string{"path"},
					},
				},
			},
			{
				Type: "function",
				Function: embedded.OpenAIFunction{
					Name:        "grep",
					Description: "Search for a pattern in files using ripgrep.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"pattern": map[string]interface{}{"type": "string", "description": "Pattern to search for"},
							"glob":    map[string]interface{}{"type": "string", "description": "Glob pattern to filter files (optional)"},
						},
						"required": []string{"pattern"},
					},
				},
			},
			{
				Type: "function",
				Function: embedded.OpenAIFunction{
					Name:        "glob",
					Description: "Find files matching a glob pattern in the project directory.",
					Parameters: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern (e.g., **/*.go)"},
						},
						"required": []string{"pattern"},
					},
				},
			},
		}
		toolDefs = append(toolDefs, nativeReadOnly...)

		// Add the proposer-permitted MCP tools (read/query + browser).
		for _, d := range proposerMCPDefs {
			toolDefs = append(toolDefs, embedded.OpenAIToolDef{
				Type: "function",
				Function: embedded.OpenAIFunction{
					Name:        d.Name,
					Description: d.Description,
					Parameters:  d.Parameters,
				},
			})
		}

		// 6. Assemble magi.Options and run the pipeline.
		// MAGI proposer token streaming (task #7209):
		//
		// Previously, every per-token delta was wrapped with [MAGI:N] prefix
		// and sent to OnOutput. This caused two problems:
		//   1. Tokens without trailing '\n' got open-line-merged by the
		//      frontend's appendLiveOutput, cross-contaminating slots:
		//      "[MAGI:0] 안녕[MAGI:1] Hello" on a single line.
		//   2. The prefix appeared on every tiny fragment, creating hundreds
		//      of [MAGI:N] x, [MAGI:N] y lines in the output.
		//
		// Now each slot has its own LineCoalescer. The prefix is attached
		// exactly once per complete line, and incomplete lines stay buffered
		// per-slot so they can never merge with another slot's content.
		magiMu := &sync.Mutex{}
		magiCoalescers := make(map[int]*worker.LineCoalescer)
		magiFlushed := false

		flushMagiCoalescers := func() {
			if magiFlushed {
				return
			}
			magiFlushed = true
			magiMu.Lock()
			for _, c := range magiCoalescers {
				c.Flush()
			}
			magiMu.Unlock()
		}

		magiOpts := magi.Options{
			SheepName:           sheepName,
			ProjectPath:         projectPath,
			TaskPrompt:          prompt,
			BaseSystem:          baseSystem,
			Proposers:           proposers,
			Aggregator:          aggregator,
			ConfidenceThreshold: magiCfg.Escalation.ConfidenceThreshold,
			MaxDebateRounds:     magiCfg.Escalation.MaxDebateRounds,
			ProposerTimeout:     time.Duration(magiCfg.ProposerTimeoutSeconds) * time.Second,
			OnOutput:            opts.OnOutput,
			OnProposerToken: func(slot int, text string) {
				if opts.OnOutput == nil {
					return
				}
				magiMu.Lock()
				c, ok := magiCoalescers[slot]
				if !ok {
					c = worker.NewLineCoalescer(func(line string) {
						opts.OnOutput(fmt.Sprintf("[MAGI:%d] %s", slot, line))
					})
					magiCoalescers[slot] = c
				}
				magiMu.Unlock()
				c.Append(text)
			},
			ToolDefs:     toolDefs,
			ToolDispatch: mcpDispatch,
		}
		// Note: MAGI proposer LLM calls are gated via llmslots.Global().Lookup()
		// inside callEndpoint/reaskProposer (proposer.go), not via this Options
		// struct. The semaphores are created by syncEndpointSemaphores at server
		// startup and looked up per endpoint ID.

		result, err := magi.Run(ctx, magiOpts)
		// Flush remaining buffered content from all MAGI coalescers so the
		// final partial lines are not lost.
		flushMagiCoalescers()
		if err != nil {
			// ErrInsufficientProposers is returned as-is so the worker fallback
			// branch can identify it via errors.Is (design §5.1).
			return nil, err
		}

		// 7. Convert embedded.ExecuteResult → worker.ExecuteResult (same field
		// mapping as the embedded executor closure).
		return &worker.ExecuteResult{
			Result:           result.Result,
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			CostUSD:          result.CostUSD,
			Incomplete:       result.Incomplete,
			IncompleteReason: result.IncompleteReason,
		}, nil
	})
}

// buildMCPGuide constructs the [Available Shepherd MCP Tools] section of the
// system prompt, listing all tool definitions that will be available to the
// embedded agent — both built-in and external (project-specific).
func buildMCPGuide(defs []embedded.MCPToolDef, projectName string) string {
	var sb strings.Builder
	sb.WriteString("[Available Shepherd MCP Tools]\n\n")

	// Group tools by category for readability
	categories := map[string][]embedded.MCPToolDef{}
	categoryOrder := []string{"Task management", "Skills", "Wiki", "Browser automation", "External MCP"}

	// Categorize each tool
	for _, d := range defs {
		switch {
		case strings.HasPrefix(d.Name, "task_") || strings.HasPrefix(d.Name, "get_") || d.Name == "get_status":
			categories["Task management"] = append(categories["Task management"], d)
		case d.Name == "skill_load":
			categories["Skills"] = append(categories["Skills"], d)
		case strings.HasPrefix(d.Name, "wiki_"):
			categories["Wiki"] = append(categories["Wiki"], d)
		case strings.HasPrefix(d.Name, "browser_"):
			categories["Browser automation"] = append(categories["Browser automation"], d)
		default:
			categories["External MCP"] = append(categories["External MCP"], d)
		}
	}

	for _, cat := range categoryOrder {
		tools, ok := categories[cat]
		if !ok || len(tools) == 0 {
			continue
		}
		sb.WriteString(cat + ":\n")
		for _, t := range tools {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
		}
		sb.WriteString("\n")
	}

	// Add any remaining uncategorized tools
	for catName, tools := range categories {
		if catName == "Task management" || catName == "Skills" || catName == "Wiki" || catName == "Browser automation" {
			continue // already printed above
		}
		if len(tools) == 0 {
			continue
		}
		sb.WriteString(catName + ":\n")
		for _, t := range tools {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", t.Name, t.Description))
		}
		sb.WriteString("\n")
	}

	if projectName != "" {
		sb.WriteString(fmt.Sprintf("Project: %s\n", projectName))
	}

	return sb.String()
}

// toEmbeddedMCPDef converts an mcp.Tool to embedded.MCPToolDef.
func toEmbeddedMCPDef(t mcp.Tool) embedded.MCPToolDef {
	properties := make(map[string]interface{})
	for k, v := range t.InputSchema.Properties {
		properties[k] = map[string]interface{}{
			"type":        v.Type,
			"description": v.Description,
		}
	}
	// JSON Schema requires `required` to be an array. A nil slice marshals to
	// `null`, which strict tool-template parsers (e.g. llama.cpp) reject with a
	// 400 ("type must be array, but is null"), aborting the whole request before
	// any inference runs. Tools with no required params (e.g. get_status) hit
	// this, so always emit an empty array instead of null.
	required := t.InputSchema.Required
	if required == nil {
		required = []string{}
	}
	return embedded.MCPToolDef{
		Name:        t.Name,
		Description: t.Description,
		Parameters: map[string]interface{}{
			"type":       t.InputSchema.Type,
			"properties": properties,
			"required":   required,
		},
	}
}

// toEmbeddedMCPImages converts MCP image content blocks into the embedded
// package's image type so the agent loop can surface them to a vision model.
func toEmbeddedMCPImages(imgs []mcp.ToolImage) []embedded.MCPImage {
	if len(imgs) == 0 {
		return nil
	}
	out := make([]embedded.MCPImage, len(imgs))
	for i, img := range imgs {
		out[i] = embedded.MCPImage{MIMEType: img.MIMEType, Data: img.Data}
	}
	return out
}

// wrapSubagentOnOutput returns a body-stream OnOutput wrapper and a flush
// function for a single sub-agent (tasks #7562/#7564).
//
// Contract (mirrors MAGI OnProposerToken + LineCoalescer from #7209):
//   - Incomplete chunks stay buffered per agent so they never glue onto
//     another agent's open line.
//   - Each complete line (and Flush residual) is emitted as
//     "[SUB:name] <line>" — every physical line, not only the first.
//   - mu serializes emits into the shared parent sink. Callers must pass the
//     same mutex for all sub-agents of one parent task.
//   - Lifecycle lines ("시작"/"완료") stay outside this wrapper so they are
//     not double-prefixed by tools.go.
//
// When onOutput is nil both returned funcs are no-ops.
func wrapSubagentOnOutput(name string, onOutput func(string), mu *sync.Mutex) (wrapped func(string), flush func()) {
	if onOutput == nil {
		return func(string) {}, func() {}
	}
	if mu == nil {
		mu = &sync.Mutex{}
	}
	c := worker.NewLineCoalescer(func(line string) {
		// Called while mu is held by Append/Flush — parent sink sees
		// one complete prefixed line at a time.
		onOutput(fmt.Sprintf("[SUB:%s] %s", name, line))
	})
	wrapped = func(text string) {
		mu.Lock()
		defer mu.Unlock()
		c.Append(text)
	}
	flush = func() {
		mu.Lock()
		defer mu.Unlock()
		c.Flush()
	}
	return wrapped, flush
}
