//go:build !windows

package worker

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/envutil"
	"github.com/creack/pty"
)

// executeInteractiveWithPty runs Claude Code using a pseudo-terminal.
func executeInteractiveWithPty(ctx context.Context, sheepName, projectPath, sessionID, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	args := []string{
		"--mcp-config", GetMCPConfigJSON(),
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
	cmd.Env = append(envutil.CleanEnv(),
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"LC_ALL=en_US.UTF-8",
	)

	// Start PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start PTY: %w", err)
	}
	defer ptmx.Close()

	// Register running task
	registerRunningTask(sheepName, cancel, cmd)

	// Send prompt
	_, err = ptmx.WriteString(buildPromptWithContext(sheepName, prompt) + "\n")
	if err != nil {
		return nil, fmt.Errorf("failed to send prompt: %w", err)
	}

	// Collect output
	var outputBuilder strings.Builder
	var recentOutput strings.Builder // Recent output buffer (for menu detection)
	var mu sync.Mutex
	done := make(chan struct{})
	bypassAccepted := false // Whether Bypass menu has already been handled

	// Output reading goroutine
	go func() {
		defer close(done)
		reader := bufio.NewReader(ptmx)
		var lineBuffer strings.Builder

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Read rune by rune (supports UTF-8 multi-byte characters)
			r, _, err := reader.ReadRune()
			if err != nil {
				if err != io.EOF {
					// PTY closed
				}
				return
			}

			char := string(r)
			lineBuffer.WriteString(char)

			mu.Lock()
			outputBuilder.WriteString(char)
			recentOutput.WriteString(char)
			// Limit recent output buffer to 2000 chars
			if recentOutput.Len() > 2000 {
				recent := recentOutput.String()
				recentOutput.Reset()
				recentOutput.WriteString(recent[1000:])
			}
			recentText := recentOutput.String()
			mu.Unlock()

			// Output on newline
			if r == '\n' {
				line := lineBuffer.String()
				lineBuffer.Reset()

				// Output after stripping ANSI codes
				cleanLine := stripAnsi(line)
				if opts.OnOutput != nil && strings.TrimSpace(cleanLine) != "" {
					opts.OnOutput(cleanLine)
				}
			}

			// Detect and auto-approve Bypass Permissions menu
			cleanRecent := stripAnsi(recentText)
			if !bypassAccepted &&
			   strings.Contains(cleanRecent, "Bypass Permissions mode") &&
			   strings.Contains(cleanRecent, "Yes, I accept") {
				bypassAccepted = true
				time.Sleep(100 * time.Millisecond) // Wait for menu rendering
				ptmx.WriteString("2\n") // Select "Yes, I accept"
				mu.Lock()
				recentOutput.Reset()
				mu.Unlock()
				lineBuffer.Reset()
				continue
			}

			// Auto-handle Enter to confirm prompt
			if strings.Contains(cleanRecent, "Enter to confirm") ||
			   strings.Contains(cleanRecent, "to confirm · Esc") {
				time.Sleep(50 * time.Millisecond)
				ptmx.WriteString("\n")
				mu.Lock()
				recentOutput.Reset()
				mu.Unlock()
				lineBuffer.Reset()
				continue
			}

			// Detect regular input prompts (when Claude asks a question)
			currentLine := lineBuffer.String()
			if isInputPrompt(currentLine) && !strings.Contains(cleanRecent, "Bypass Permissions") {
				cleanLine := stripAnsi(currentLine)

				if opts.OnOutput != nil {
					opts.OnOutput(cleanLine)
				}

				// Request user input
				if opts.OnInput != nil {
					userInput, err := opts.OnInput(currentLine)
					if err != nil {
						return
					}
					ptmx.WriteString(userInput + "\n")
					lineBuffer.Reset()
				}
			}
		}
	}()

	// Wait for completion
	select {
	case <-done:
	case <-ctx.Done():
		return nil, fmt.Errorf("execution timeout")
	}

	// Wait for process to exit
	cmd.Wait()

	mu.Lock()
	fullOutput := outputBuilder.String()
	mu.Unlock()

	// Parse result (simple result only in interactive mode)
	result := &ExecuteResult{
		Result:        extractResultFromOutput(fullOutput),
		FilesModified: extractFilesFromOutput(fullOutput),
	}

	return result, nil
}
