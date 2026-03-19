package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/envutil"
)

// OpenCodeProvider wraps the OpenCode CLI for any model it supports
type OpenCodeProvider struct {
	config *config.OpenCodeConfig
}

// NewOpenCodeProvider creates a new OpenCodeProvider
func NewOpenCodeProvider() *OpenCodeProvider {
	cfg, _ := config.LoadOpenCodeConfig()
	return &OpenCodeProvider{config: cfg}
}

// Name returns provider name
func (p *OpenCodeProvider) Name() string {
	return "opencode"
}

// ModelName returns the configured model name.
// Reads from OpenCode's native config if not set in shepherd's config.
func (p *OpenCodeProvider) ModelName() string {
	if p.config != nil && p.config.Model != "" {
		return p.config.Model
	}
	// Fallback: read directly from OpenCode's native config
	if model := config.ReadOpenCodeNativeModel(); model != "" {
		return model
	}
	return "unknown"
}

// DisplayName returns display name (e.g., "opencode(gpt-4o)")
func (p *OpenCodeProvider) DisplayName() string {
	if p.config != nil {
		return p.config.GetModelDisplayName()
	}
	return "opencode"
}

// IsAvailable checks if the opencode binary exists
func (p *OpenCodeProvider) IsAvailable() bool {
	binary := config.GetOpenCodeBinary()
	_, err := exec.LookPath(binary)
	return err == nil
}

// Execute runs OpenCode programmatically
func (p *OpenCodeProvider) Execute(workdir, prompt string, opts ExecuteOptions) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	args := []string{
		"run",
		"--format", "json",
	}

	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, config.GetOpenCodeBinary(), args...)
	cmd.Dir = workdir
	envutil.SetCleanEnv(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timeout (%v exceeded)", opts.Timeout)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("OpenCode execution failed: %s", errMsg)
		}
		return nil, fmt.Errorf("OpenCode execution failed: %w", err)
	}

	return p.parseJSONOutput(stdout.Bytes())
}

// ExecuteInteractive runs OpenCode interactively with streaming
func (p *OpenCodeProvider) ExecuteInteractive(workdir, sessionID, prompt string, opts InteractiveOptions) (*Result, error) {
	timeout := opts.Timeout
	if p.config != nil && p.config.Timeout > 0 {
		timeout = time.Duration(p.config.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := []string{
		"run",
		"--format", "json",
	}

	if sessionID != "" {
		args = append(args, "-s", sessionID)
	}

	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, config.GetOpenCodeBinary(), args...)
	cmd.Dir = workdir
	envutil.SetCleanEnv(cmd)
	cmd.Env = append(cmd.Env, `OPENCODE_PERMISSION={"*":"allow"}`)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start OpenCode: %w", err)
	}

	var outputLines []string
	var resultText string
	var newSessionID string

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		parsed, sid := parseOpenCodeLine(line)
		if sid != "" {
			newSessionID = sid
		}
		if parsed != "" {
			outputLines = append(outputLines, parsed)
			if opts.OnOutput != nil {
				opts.OnOutput(parsed + "\n")
			}
		}
	}

	stderrScanner := bufio.NewScanner(stderr)
	for stderrScanner.Scan() {
		line := stderrScanner.Text()
		if opts.OnOutput != nil && strings.TrimSpace(line) != "" {
			opts.OnOutput("⚠️ " + line + "\n")
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("execution timeout")
		}
		return nil, fmt.Errorf("OpenCode execution error: %w", err)
	}

	if len(outputLines) > 0 {
		resultText = outputLines[len(outputLines)-1]
	}

	return &Result{
		Result:    resultText,
		SessionID: newSessionID,
		Output:    outputLines,
	}, nil
}

// parseOpenCodeLine parses an OpenCode JSON output line
func parseOpenCodeLine(line string) (text string, sessionID string) {
	if !strings.HasPrefix(line, "{") {
		return line, ""
	}

	var event struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionID"`
		Part      struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"part"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Text    string `json:"text"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", ""
	}

	if event.SessionID != "" {
		sessionID = event.SessionID
	}

	switch event.Type {
	case "text", "text.delta":
		if event.Part.Text != "" {
			text = event.Part.Text
		} else if event.Text != "" {
			text = event.Text
		}
	case "message":
		text = event.Message
	case "tool.start":
		text = "🔧 Running tool..."
	case "tool.end":
		text = "✅ Tool complete"
	case "finish":
		text = "✅ Task complete"
	}

	return text, sessionID
}

// parseJSONOutput parses OpenCode JSON output
func (p *OpenCodeProvider) parseJSONOutput(data []byte) (*Result, error) {
	lines := strings.Split(string(data), "\n")
	var resultText string
	var sessionID string
	var errorMsg string

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		text, sid := parseOpenCodeLine(line)
		if sid != "" {
			sessionID = sid
		}
		if text != "" {
			resultText = text
		}
		// Check for error events
		if strings.HasPrefix(line, "{") {
			var event struct {
				Type  string `json:"type"`
				Error struct {
					Name string `json:"name"`
					Data struct {
						Message string `json:"message"`
					} `json:"data"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Type == "error" {
				errorMsg = event.Error.Data.Message
				if errorMsg == "" {
					errorMsg = event.Error.Name
				}
			}
		}
	}

	// Return error if OpenCode reported an error event
	if resultText == "" && errorMsg != "" {
		return nil, fmt.Errorf("OpenCode error: %s", errorMsg)
	}

	return &Result{
		Result:    resultText,
		SessionID: sessionID,
	}, nil
}

// GetOpenCodeDisplayName returns the display name for OpenCode provider (global function)
func GetOpenCodeDisplayName() string {
	cfg, err := config.LoadOpenCodeConfig()
	if err != nil || cfg == nil {
		return "opencode"
	}
	return cfg.GetModelDisplayName()
}

// Backward compatibility aliases
type LocalProvider = OpenCodeProvider

func NewLocalProvider() *OpenCodeProvider     { return NewOpenCodeProvider() }
func GetLocalModelDisplayName() string        { return GetOpenCodeDisplayName() }
