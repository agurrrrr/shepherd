package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/agurrrrr/shepherd/internal/daemon"
)

// browserToolNames must stay in sync with registerBrowserTools in
// browser_tools.go. Keeping it here lets the stateless client register
// forwarders without dragging the chromium-DevTools dependency into its
// import graph.
var browserToolNames = []string{
	"browser_session_start", "browser_session_stop", "browser_list_sessions",
	"browser_list_pages",
	"browser_open", "browser_close", "browser_navigate", "browser_reload",
	"browser_back", "browser_forward",
	"browser_click", "browser_type", "browser_select", "browser_check",
	"browser_hover", "browser_scroll",
	"browser_get_text", "browser_get_html", "browser_get_attribute",
	"browser_get_url", "browser_get_title", "browser_eval",
	"browser_wait_selector", "browser_wait_hidden", "browser_wait_load",
	"browser_wait_idle",
	"browser_screenshot", "browser_pdf",
	"browser_console_start", "browser_console_messages",
	"browser_network_start", "browser_network_requests", "browser_network_request",
}

// dbWriteToolNames are MCP tools that mutate shepherd.db (and related files
// under ~/.shepherd). In NewClient (stateless `shepherd mcp` child) these
// forward to the daemon so a sandboxed host that cannot write ~/.shepherd
// still succeeds, and so only the long-running daemon owns SQLite writes
// (task #7864 / #7865 — readonly database under bwrap).
//
// Read tools (wiki_read_page, issue_list, get_history, …) stay in-process.
var dbWriteToolNames = []string{
	"wiki_create", "wiki_edit",
	"issue_upsert", "issue_execute",
}

// registerBrowserForwarders attaches an HTTP-forwarder handler for every
// browser tool. Each call hands off to /api/_internal/mcp/call on the running
// daemon, where the real handler lives in long-running memory.
func (s *Server) registerBrowserForwarders() {
	for _, name := range browserToolNames {
		n := name
		s.tools[n] = func(args map[string]interface{}) (string, error) {
			return forwardToDaemon(n, args)
		}
	}
}

// registerDBWriteForwarders overwrites in-process write handlers (installed by
// registerCoreTools) with daemon forwarders. Call only from NewClient after
// registerCoreTools. NewServer keeps the real handlers for ExecuteTool.
func (s *Server) registerDBWriteForwarders() {
	for _, name := range dbWriteToolNames {
		n := name
		s.tools[n] = func(args map[string]interface{}) (string, error) {
			return forwardToDaemon(n, args)
		}
	}
}

// forwardToDaemon proxies one tool call to the long-running shepherd daemon
// over a localhost HTTP loopback authenticated by the runtime.json token.
func forwardToDaemon(toolName string, args map[string]interface{}) (string, error) {
	info, err := daemon.ReadRuntime()
	if err != nil {
		return "", fmt.Errorf(
			"shepherd daemon is not running — start it with `shepherd serve` first (%s requires the daemon): %w",
			toolName,
			err,
		)
	}

	body, err := json.Marshal(map[string]interface{}{
		"tool": toolName,
		"args": args,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, info.Addr+"/api/_internal/mcp/call", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-MCP-Token", info.MCPToken)

	// Browser actions can be slow (page load + element wait). Wiki/issue writes
	// are fast, but we use one timeout so a hung daemon does not wedge MCP.
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("daemon unreachable at %s: %w", info.Addr, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var envelope struct {
		Success bool        `json:"success"`
		Data    interface{} `json:"data"`
		Error   string      `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("daemon returned invalid response (status %d): %s", resp.StatusCode, string(raw))
	}
	if !envelope.Success {
		msg := envelope.Error
		if msg == "" {
			msg = fmt.Sprintf("daemon error (status %d)", resp.StatusCode)
		}
		return "", fmt.Errorf("%s", msg)
	}
	if s, ok := envelope.Data.(string); ok {
		return s, nil
	}
	return "", fmt.Errorf("daemon returned unexpected data type: %T", envelope.Data)
}
