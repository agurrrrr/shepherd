package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// embeddedRunRequest is the body for POST /api/embedded/run.
type embeddedRunRequest struct {
	// ProjectPath is the working directory for the run (the client's cwd).
	// When empty, the daemon's own working directory is used.
	ProjectPath string `json:"project_path"`
	// Prompt is the user request.
	Prompt string `json:"prompt"`
	// Model optionally selects an embedded endpoint by id (or unique
	// label/model). Empty means the globally active endpoint.
	Model string `json:"model"`
}

// embeddedRunEvent is a single request-scoped SSE frame.
type embeddedRunEvent struct {
	event string
	data  interface{}
}

// POST /api/embedded/run
//
// Runs the embedded coding agent directly in the given directory, bypassing
// manager.Analyze / project / sheep / queue orchestration. Output is streamed
// as request-scoped SSE so it never mixes with the global /api/events hub.
//
// This is the backend for the thin CLI client (`shepherd "..."`, `shepherd`).
func (s *Server) handleEmbeddedRun(c *fiber.Ctx) error {
	var body embeddedRunRequest
	if err := c.BodyParser(&body); err != nil {
		return fail(c, fiber.StatusBadRequest, "invalid request body")
	}
	if strings.TrimSpace(body.Prompt) == "" {
		return fail(c, fiber.StatusBadRequest, "prompt is required")
	}
	if strings.TrimSpace(body.ProjectPath) == "" {
		if wd, err := os.Getwd(); err == nil {
			body.ProjectPath = wd
		}
	}
	if info, err := os.Stat(body.ProjectPath); err != nil || !info.IsDir() {
		return fail(c, fiber.StatusBadRequest, "project_path is not a directory: "+body.ProjectPath)
	}

	// Resolve a sheep name purely for context (project skills, memory, browser
	// isolation). It may be empty when the cwd is not a registered project.
	sheepName := directRunSheepForPath(body.ProjectPath)

	// Request-scoped context so a client disconnect cancels the run.
	ctx, cancel := context.WithCancel(context.Background())

	// Buffered so short bursts of output do not block the agent loop.
	evCh := make(chan embeddedRunEvent, 512)

	opts := worker.DefaultInteractiveOptions(func(text string) {
		select {
		case evCh <- embeddedRunEvent{event: "output", data: map[string]string{"text": text}}:
		case <-ctx.Done():
		}
	}, nil)
	opts.Model = body.Model
	// Unique registry key: never clobber a queued task running on the same sheep.
	opts.RegistryName = fmt.Sprintf("embedded-cli-%d", time.Now().UnixNano())

	go func() {
		defer close(evCh)

		// Recover so a panic inside the agent loop cannot take down the daemon.
		defer func() {
			if r := recover(); r != nil {
				evCh <- embeddedRunEvent{event: "error", data: map[string]string{
					"message": fmt.Sprintf("embedded run panicked: %v", r),
				}}
			}
		}()

		result, err := worker.ExecuteEmbeddedDirect(ctx, sheepName, body.ProjectPath, body.Prompt, opts, cancel)
		if err != nil {
			evCh <- embeddedRunEvent{event: "error", data: map[string]string{"message": err.Error()}}
			return
		}

		done := map[string]interface{}{
			"result":            result.Result,
			"files_modified":    result.FilesModified,
			"cost_usd":          result.CostUSD,
			"prompt_tokens":     result.PromptTokens,
			"completion_tokens": result.CompletionTokens,
			"incomplete":        result.Incomplete,
		}
		if result.Incomplete {
			done["incomplete_reason"] = result.IncompleteReason
		}
		evCh <- embeddedRunEvent{event: "done", data: done}
	}()

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		// Client disconnect / handler exit cancels the run.
		defer cancel()

		fmt.Fprintf(w, ": connected\n\n")
		w.Flush()

		for ev := range evCh {
			payload, err := json.Marshal(ev.data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.event, payload)
			if err := w.Flush(); err != nil {
				// Client gone: cancel the run and stop draining. The goroutine
				// will observe ctx.Done and close evCh.
				cancel()
				return
			}
		}
	})
	return nil
}

// directRunSheepForPath returns the name of a sheep assigned to the project
// registered at projectPath, or "" when none can be resolved. Used only for
// context injection; a direct run never requires an assigned sheep.
func directRunSheepForPath(projectPath string) string {
	name, err := project.GetByPath(projectPath)
	if err != nil || name == "" {
		return ""
	}
	list, err := worker.List()
	if err != nil {
		return ""
	}
	for _, s := range list {
		if s.Edges.Project != nil && s.Edges.Project.Name == name {
			return s.Name
		}
	}
	return ""
}
