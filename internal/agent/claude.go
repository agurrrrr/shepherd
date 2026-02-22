package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/agurrrrr/shepherd/internal/envutil"
)

// ClaudeProvider Claude Code CLI 프로바이더
type ClaudeProvider struct{}

// NewClaudeProvider 새 ClaudeProvider 생성
func NewClaudeProvider() *ClaudeProvider {
	return &ClaudeProvider{}
}

// Name 프로바이더 이름 반환
func (p *ClaudeProvider) Name() string {
	return "claude"
}

// IsAvailable Claude CLI 사용 가능 여부 확인
func (p *ClaudeProvider) IsAvailable() bool {
	_, err := exec.LookPath("claude")
	return err == nil
}

// Execute 프로그래매틱 실행
func (p *ClaudeProvider) Execute(workdir, prompt string, opts ExecuteOptions) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "json",
		"--dangerously-skip-permissions",
	}

	if opts.MCPConfig != "" {
		args = append(args, "--mcp-config", opts.MCPConfig)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(prompt)
	envutil.SetCleanEnv(cmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("실행 타임아웃 (%v 초과)", opts.Timeout)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("Claude 실행 실패: %s", errMsg)
		}
		return nil, fmt.Errorf("Claude 실행 실패: %w", err)
	}

	return p.parseJSONOutput(stdout.Bytes())
}

// ExecuteInteractive 대화형 실행 (스트리밍)
func (p *ClaudeProvider) ExecuteInteractive(workdir, sessionID, prompt string, opts InteractiveOptions) (*Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--mcp-config", GetMCPConfigJSON(),
	}

	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = workdir
	cmd.Stdin = strings.NewReader(prompt)
	envutil.SetCleanEnv(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout 파이프 생성 실패: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("Claude 시작 실패: %w", err)
	}

	var outputLines []string
	var resultText string
	var newSessionID string

	scanner := bufio.NewScanner(stdout)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// 세션 ID 추출
		if sid := extractSessionID(line); sid != "" {
			newSessionID = sid
		}

		// 텍스트 파싱
		parsed := parseStreamLine(line)
		if parsed != "" {
			outputLines = append(outputLines, parsed)
			if opts.OnOutput != nil {
				opts.OnOutput(parsed)
			}
		}

		// 결과 텍스트 추출
		if result := extractResultText(line); result != "" {
			resultText = result
		}
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("실행 타임아웃")
		}
		return nil, fmt.Errorf("Claude 실행 오류: %w", err)
	}

	return &Result{
		Result:    resultText,
		SessionID: newSessionID,
		Output:    outputLines,
	}, nil
}

// parseJSONOutput JSON 출력 파싱
func (p *ClaudeProvider) parseJSONOutput(data []byte) (*Result, error) {
	var output struct {
		Result    string   `json:"result"`
		SessionID string   `json:"session_id"`
		Files     []string `json:"files_modified"`
	}

	if err := json.Unmarshal(data, &output); err != nil {
		return nil, fmt.Errorf("JSON 파싱 실패: %w", err)
	}

	return &Result{
		Result:        output.Result,
		SessionID:     output.SessionID,
		FilesModified: output.Files,
	}, nil
}

// extractSessionID stream-json에서 세션 ID 추출
func extractSessionID(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return ""
	}

	var msg struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err == nil {
		return msg.SessionID
	}
	return ""
}

// parseStreamLine stream-json 라인 파싱
func parseStreamLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "{") {
		return ""
	}

	var msg struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"message"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err == nil {
		switch msg.Type {
		case "assistant":
			for _, c := range msg.Message.Content {
				if c.Type == "text" && c.Text != "" {
					return c.Text
				}
			}
		case "content_block_delta":
			if msg.Content != "" {
				return msg.Content
			}
		}
	}

	return ""
}

// extractResultText 결과 텍스트 추출
func extractResultText(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return ""
	}

	var msg struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err == nil {
		if msg.Type == "result" && msg.Result != "" {
			return msg.Result
		}
	}
	return ""
}

// GetMCPConfigJSON MCP 설정 JSON 반환
func GetMCPConfigJSON() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	settingsPath := homeDir + "/.claude/settings.json"
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	mcpServers, ok := settings["mcpServers"].(map[string]interface{})
	if !ok {
		mcpServers = make(map[string]interface{})
	}

	// shepherd MCP 추가
	mcpServers["shepherd"] = map[string]interface{}{
		"command": "shepherd",
		"args":    []string{"mcp"},
	}

	result := map[string]interface{}{
		"mcpServers": mcpServers,
	}

	jsonData, err := json.Marshal(result)
	if err != nil {
		return `{"mcpServers":{"shepherd":{"command":"shepherd","args":["mcp"]}}}`
	}

	return string(jsonData)
}
