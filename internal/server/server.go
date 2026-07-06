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
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
	"github.com/google/uuid"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/discord"
	"github.com/agurrrrr/shepherd/internal/embedded"
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

	// Initialize magi provider executor (avoids import cycle: worker → magi → embedded)
	initMagiExecutor()

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
		toolDefs := toolRegistry.OpenAIToolDefs()

		// Run the embedded agent loop
		result, err := embedded.Run(ctx, embedded.ExecuteOptions{
			SheepName:     sheepName,
			ProjectPath:   projectPath,
			BaseURL:       ep.BaseURL,
			APIKey:        ep.APIKey,
			Model:         ep.Model,
			SystemPrompt:  systemPrompt,
			UserPrompt:    prompt,
			Tools:         toolDefs,
			OnOutput:      opts.OnOutput,
			MaxIterations: ep.MaxIterations,
			ContextTokens: ep.ContextTokens,
			Vision:        ep.Vision,
			MCPDefs:       mcpDefs,
			MCPDispatch:   mcpDispatch,
			InjectCh:      injectCh,
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

// initMagiExecutor registers the magi consensus executor with the worker
// package. This bridges the magi pipeline (proposers → aggregator → verdict)
// to the worker's provider switch. The worker falls back to a single embedded
// run when fewer than 2 proposers succeed (design §5.1).
func initMagiExecutor() {
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

		// 3. Resolve proposer endpoints → magi.EndpointRef.
		proposers := make([]magi.ProposerSpec, len(magiCfg.Proposers))
		var firstProposerEP *magi.EndpointRef
		for i, p := range magiCfg.Proposers {
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
			proposers[i] = magi.ProposerSpec{
				Endpoint:     ref,
				PersonaKey:   p.Persona,
				CustomPrompt: p.CustomPrompt,
			}
			if firstProposerEP == nil {
				firstProposerEP = &ref
			}
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
			}
			// Fallback to first proposer endpoint (design §7).
			if firstProposerEP != nil {
				aggregator.FallbackEndpoint = *firstProposerEP
			}
		default:
			return nil, fmt.Errorf("magi aggregator: unknown type %q", magiCfg.Aggregator.Type)
		}

		// 5. Build base system prompt (same builder as embedded, no MCP guide).
		baseSystem := worker.BuildSystemPromptForEmbedded(sheepName, projectPath, "")

		// 6. Assemble magi.Options and run the pipeline.
		magiOpts := magi.Options{
			SheepName:           sheepName,
			TaskPrompt:          prompt,
			BaseSystem:          baseSystem,
			Proposers:           proposers,
			Aggregator:          aggregator,
			ConfidenceThreshold: magiCfg.Escalation.ConfidenceThreshold,
			MaxDebateRounds:     magiCfg.Escalation.MaxDebateRounds,
			ProposerTimeout:     time.Duration(magiCfg.ProposerTimeoutSeconds) * time.Second,
			OnOutput:            opts.OnOutput,
		}

		result, err := magi.Run(ctx, magiOpts)
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
