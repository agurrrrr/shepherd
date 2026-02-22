package worker

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

const (
	// DefaultTimeout is the default execution timeout (10 minutes)
	DefaultTimeout = 10 * time.Minute
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

// DefaultExecuteOptions returns default execution options
func DefaultExecuteOptions() ExecuteOptions {
	return ExecuteOptions{
		Timeout:    DefaultTimeout,
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
func executeClaudeCodeWithTimeout(projectPath, sessionID, prompt string, timeout time.Duration) (*ExecuteResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "json",
	}

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
func RecoverStuckSheep() (int, error) {
	ctx := context.Background()
	client := db.Client()

	// Change sheep in working or error status to idle
	count, err := client.Sheep.Update().
		Where(
			sheep.Or(
				sheep.StatusEQ(sheep.StatusWorking),
				sheep.StatusEQ(sheep.StatusError),
			),
		).
		SetStatus(sheep.StatusIdle).
		Save(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to recover sheep status: %w", err)
	}

	return count, nil
}

