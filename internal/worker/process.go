package worker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/daemon"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

const (
	// MaxRetries is the maximum number of retry attempts
	MaxRetries = 2
	// RetryDelay is the delay between retries
	RetryDelay = 2 * time.Second
)

// ExecuteOptions contains options for Execute function
type ExecuteOptions struct {
	Timeout    time.Duration
	MaxRetries int
}

// DefaultExecuteOptions returns default execution options. Timeout comes from
// config (task_timeout); 0 means no deadline.
func DefaultExecuteOptions() ExecuteOptions {
	return ExecuteOptions{
		Timeout:    config.GetTaskTimeout(),
		MaxRetries: MaxRetries,
	}
}

// Execute runs Claude Code CLI with the given prompt for the specified sheep.
// It uses the sheep's assigned project directory as the working directory.
// If the sheep has a session ID, it resumes the previous session.
func Execute(sheepName, prompt string) (*ExecuteResult, error) {
	return ExecuteWithOptions(sheepName, prompt, DefaultExecuteOptions())
}

// ExecuteWithOptions runs Claude Code CLI with custom options.
func ExecuteWithOptions(sheepName, prompt string, opts ExecuteOptions) (*ExecuteResult, error) {
	ctx := context.Background()
	client := db.Client()

	// Look up the sheep
	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("'%s' not found", sheepName)
		}
		return nil, fmt.Errorf("failed to query sheep: %w", err)
	}

	// Check project assignment
	proj, err := s.Edges.ProjectOrErr()
	if err != nil {
		return nil, fmt.Errorf("no project assigned to '%s'", sheepName)
	}

	// Change status to working
	_, err = client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetStatus(sheep.StatusWorking).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to change status: %w", err)
	}

	// Execute Claude Code (with retries)
	var result *ExecuteResult
	var lastErr error

	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(RetryDelay)
		}

		result, lastErr = executeClaudeCodeWithTimeout(proj.Path, s.SessionID, prompt, opts.Timeout)
		if lastErr == nil {
			break
		}

		// Don't retry on timeout or cancellation errors
		if ctx.Err() != nil {
			break
		}
	}

	if lastErr != nil {
		// Change to error status
		_, _ = client.Sheep.Update().
			Where(sheep.Name(sheepName)).
			SetStatus(sheep.StatusError).
			Save(ctx)
		return nil, lastErr
	}

	// Save session ID and restore status
	updateQuery := client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetStatus(sheep.StatusIdle)

	if result.SessionID != "" {
		updateQuery = updateQuery.SetSessionID(result.SessionID)
	}

	_, err = updateQuery.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to save session ID: %w", err)
	}

	return result, nil
}

// executeClaudeCodeWithTimeout runs the claude CLI command with timeout.
// A non-positive timeout disables the deadline (the run still inherits
// cancellation from the parent context indirectly via cmd.Run returning).
func executeClaudeCodeWithTimeout(projectPath, sessionID, prompt string, timeout time.Duration) (*ExecuteResult, error) {
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "json",
	}
	args = append(args, claudeModelArgs()...)

	// Auto-approve mode
	if config.GetBool("auto_approve") {
		args = append(args, "--dangerously-skip-permissions")
	}

	// Resume session (with specific session ID)
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = projectPath
	cmd.Stdin = strings.NewReader(prompt)
	envutil.SetCleanEnv(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check for timeout
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timeout (exceeded %v)", timeout)
		}
		// Include error message from stderr if available
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("Claude Code execution failed: %s", errMsg)
		}
		return nil, fmt.Errorf("Claude Code execution failed: %w", err)
	}

	// Parse JSON output
	result, err := ParseClaudeOutput(stdout.Bytes())
	if err != nil {
		return nil, err
	}

	return result, nil
}

// ClearSession clears the session ID for the specified sheep.
func ClearSession(sheepName string) error {
	ctx := context.Background()
	client := db.Client()

	count, err := client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		ClearSessionID().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("failed to clear session ID: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("'%s' not found", sheepName)
	}

	return nil
}

// RecoverStuckSheep recovers sheep that are stuck in working/error status.
// This should be called on startup to clean up after abnormal termination.
//
// It is ownership-aware: a sheep that still owns a genuinely-running task under a
// live, *different* process is left untouched. Without this guard, recovery that
// runs while the real daemon is still alive — a PID-file race in
// daemon.IsRunning(), a redundant launch, or a CLI subcommand that recovers on
// startup — would reset a working sheep to idle even though its task keeps
// running, leaving the dashboard showing that task as "finished" while it is in
// fact still executing (task #6362). RecoverStuckTasks already skips such
// live-owned running tasks the same way; mirroring that here keeps sheep status
// and task status from ever disagreeing into a running-task/idle-sheep desync.
func RecoverStuckSheep() (int, error) {
	ctx := context.Background()
	client := db.Client()
	selfPID := os.Getpid()

	// Collect sheep that own a running task under another live process. These
	// must be preserved — their task is still executing, just not in this process.
	protected := make(map[int]bool)
	running, err := client.Task.Query().
		Where(task.StatusEQ(task.StatusRunning)).
		WithSheep().
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query running tasks: %w", err)
	}
	for _, t := range running {
		if t.OwnerPid != 0 && t.OwnerPid != selfPID && daemon.IsPIDAlive(t.OwnerPid) && t.Edges.Sheep != nil {
			protected[t.Edges.Sheep.ID] = true
		}
	}

	// Reset working/error sheep to idle, skipping any preserved above.
	stuck, err := client.Sheep.Query().
		Where(
			sheep.Or(
				sheep.StatusEQ(sheep.StatusWorking),
				sheep.StatusEQ(sheep.StatusError),
			),
		).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to query stuck sheep: %w", err)
	}

	count := 0
	for _, s := range stuck {
		if protected[s.ID] {
			continue
		}
		if _, err := client.Sheep.UpdateOneID(s.ID).
			SetStatus(sheep.StatusIdle).
			Save(ctx); err != nil {
			return count, fmt.Errorf("failed to recover sheep status: %w", err)
		}
		count++
	}

	return count, nil
}

