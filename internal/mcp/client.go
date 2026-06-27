package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent"
)

// ExternalMCPServer manages a single external MCP server process (stdio transport).
type ExternalMCPServer struct {
	name        string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	tools       []Tool
	initialized bool
	mu          sync.Mutex
}

// NewExternalMCPServer spawns an external MCP server via stdio and initializes it.
func NewExternalMCPServer(serverInfo *ent.MCPServer) (*ExternalMCPServer, error) {
	// Parse command and args
	command := serverInfo.Command
	if command == "" {
		return nil, fmt.Errorf("MCP server %q has no command set", serverInfo.Name)
	}

	var args []string
	if serverInfo.Args != "" {
		if err := json.Unmarshal([]byte(serverInfo.Args), &args); err != nil {
			return nil, fmt.Errorf("MCP server %q has invalid args JSON: %w", serverInfo.Name, err)
		}
	}

	cmd := exec.Command(command, args...)

	// Parse env
	var envMap map[string]string
	if serverInfo.Env != "" {
		if err := json.Unmarshal([]byte(serverInfo.Env), &envMap); err != nil {
			return nil, fmt.Errorf("MCP server %q has invalid env JSON: %w", serverInfo.Name, err)
		}
	}
	// Build env: inherit current process env + override with server env
	cmd.Env = os.Environ()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server %q stdin pipe: %w", serverInfo.Name, err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("MCP server %q stdout pipe: %w", serverInfo.Name, err)
	}

	// Merge stderr into stdout for debugging (or discard)
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("MCP server %q start: %w", serverInfo.Name, err)
	}

	s := &ExternalMCPServer{
		name:   serverInfo.Name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	// Initialize the server
	if err := s.initialize(); err != nil {
		s.Close()
		return nil, fmt.Errorf("MCP server %q initialize: %w", serverInfo.Name, err)
	}

	// Fetch tools
	if err := s.listTools(); err != nil {
		s.Close()
		return nil, fmt.Errorf("MCP server %q list tools: %w", serverInfo.Name, err)
	}

	return s, nil
}

// Tools returns the list of tools provided by this external MCP server.
func (s *ExternalMCPServer) Tools() []Tool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tools
}

// CallTool calls a tool on this external MCP server and returns the text result.
// ToolImage is an image content block returned by an MCP tool call (e.g. a
// screenshot from mobile_take_screenshot). The embedded loop surfaces it to a
// vision model as an image_url message part, the same way read_file does for
// image files on disk.
type ToolImage struct {
	MIMEType string // e.g. "image/png"
	Data     string // base64-encoded image bytes (no "data:" prefix)
}

// CallTool runs a tool and returns only its text result, preserving the
// original text-only contract for callers that don't handle images.
func (s *ExternalMCPServer) CallTool(name string, args map[string]interface{}) (string, error) {
	text, _, err := s.CallToolMultimodal(name, args)
	return text, err
}

// CallToolMultimodal runs a tool and returns its text result together with any
// image content blocks it produced. Tools like mobile_take_screenshot return
// their picture as an image block with no text; renderToolResult only flattens
// text, so without surfacing the images here a vision model is blind to whatever
// the tool shows it and ends up working from stale/imagined state (task #6684).
func (s *ExternalMCPServer) CallToolMultimodal(name string, args map[string]interface{}) (string, []ToolImage, error) {
	s.mu.Lock()
	id := nextRequestID()
	s.mu.Unlock()

	// Guarantee a non-nil map so the request marshals `"arguments":{}` instead of
	// `null`. No-arg tool calls (e.g. mobile_list_available_devices) otherwise hit
	// strict object validation on the server side and fail with -32602 (task #6211).
	if args == nil {
		args = map[string]interface{}{}
	}
	params := CallToolParams{Name: name, Arguments: args}
	req := Request{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params:  mustMarshalJSON(params),
	}

	if err := s.sendRequest(req); err != nil {
		return "", nil, fmt.Errorf("send tools/call: %w", err)
	}

	resp, err := s.readResponse()
	if err != nil {
		return "", nil, err
	}

	if resp.Error != nil {
		return "", nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", nil, fmt.Errorf("parse tools/call result: %w", err)
	}

	if result.IsError {
		if len(result.Content) > 0 {
			return "", nil, fmt.Errorf("tool error: %s", result.Content[0].Text)
		}
		return "", nil, fmt.Errorf("tool error (no message)")
	}

	return renderToolResult(result), extractToolImages(result), nil
}

// extractToolImages pulls image content blocks out of a tools/call result so a
// vision-capable caller can surface them to the model. Blocks without base64
// data are skipped. renderToolResult only handles text blocks, so this is the
// only place the picture survives (task #6684).
func extractToolImages(result CallToolResult) []ToolImage {
	var imgs []ToolImage
	for _, block := range result.Content {
		if block.Type != "image" || block.Data == "" {
			continue
		}
		mime := block.MIMEType
		if mime == "" {
			mime = "image/png"
		}
		imgs = append(imgs, ToolImage{MIMEType: mime, Data: block.Data})
	}
	return imgs
}

// renderToolResult flattens a tools/call result into the text the agent sees.
// It concatenates the text content blocks, and — when those yield nothing —
// falls back to the structuredContent payload. MCP servers that declare an
// outputSchema (e.g. nagar-mcp via the official Go SDK) return their data in
// structuredContent and leave the text content array empty; the spec only
// SHOULD-duplicates it into a text block, so without this fallback such results
// look empty and the caller believes the tool returned nothing (task #6350).
func renderToolResult(result CallToolResult) string {
	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(block.Text)
		}
	}

	if sb.Len() == 0 && len(result.StructuredContent) > 0 && string(result.StructuredContent) != "null" {
		return string(result.StructuredContent)
	}

	return sb.String()
}

// Close terminates the external MCP server process.
func (s *ExternalMCPServer) Close() error {
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	return nil
}

// initialize sends the initialize request to the MCP server.
func (s *ExternalMCPServer) initialize() error {
	req := Request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: mustMarshalJSON(map[string]interface{}{
			"protocolVersion": MCPVersion,
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "shepherd-embedded",
				"version": "0.1.0",
			},
		}),
	}

	if err := s.sendRequest(req); err != nil {
		return err
	}

	resp, err := s.readResponse()
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("initialize error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	// Send initialized notification (no response expected)
	notify := Request{
		JSONRPC: "2.0",
		Method:  "initialized",
	}
	_ = s.sendRequest(notify) // ignore error, notification

	s.initialized = true
	return nil
}

// listTools fetches the list of tools from the MCP server.
func (s *ExternalMCPServer) listTools() error {
	req := Request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	if err := s.sendRequest(req); err != nil {
		return err
	}

	resp, err := s.readResponse()
	if err != nil {
		return err
	}

	if resp.Error != nil {
		return fmt.Errorf("tools/list error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return fmt.Errorf("parse tools/list result: %w", err)
	}

	s.mu.Lock()
	s.tools = result.Tools
	s.mu.Unlock()

	return nil
}

// sendRequest writes a JSON-RPC request to the server's stdin.
func (s *ExternalMCPServer) sendRequest(req Request) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

// readResponse reads a JSON-RPC response from the server's stdout with a timeout.
func (s *ExternalMCPServer) readResponse() (*Response, error) {
	// Read with timeout
	done := make(chan struct{})
	var resp Response
	var err error

	go func() {
		defer close(done)
		line, readErr := s.stdout.ReadString('\n')
		if readErr != nil {
			err = readErr
			return
		}
		err = json.Unmarshal([]byte(strings.TrimSpace(line)), &resp)
	}()

	select {
	case <-done:
		return &resp, err
	case <-time.After(10 * time.Second):
		return nil, fmt.Errorf("read response timeout after 10s")
	}
}

// MCPClientManager manages a pool of external MCP server connections.
// It lazily starts servers and caches them for reuse across tasks.
type MCPClientManager struct {
	servers map[string]*ExternalMCPServer
	mu      sync.RWMutex
}

var globalMCPManager *MCPClientManager

// GetMCPManager returns the global MCP client manager singleton.
func GetMCPManager() *MCPClientManager {
	if globalMCPManager == nil {
		globalMCPManager = &MCPClientManager{
			servers: make(map[string]*ExternalMCPServer),
		}
	}
	return globalMCPManager
}

// GetOrCreate returns an existing server connection or creates a new one.
// The serverInfo is used to spawn the process if not already connected.
func (m *MCPClientManager) GetOrCreate(serverInfo *ent.MCPServer) (*ExternalMCPServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if srv, ok := m.servers[serverInfo.Name]; ok {
		return srv, nil
	}

	srv, err := NewExternalMCPServer(serverInfo)
	if err != nil {
		return nil, err
	}

	m.servers[serverInfo.Name] = srv
	return srv, nil
}

// CloseAll terminates all managed external MCP server connections.
func (m *MCPClientManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for name, srv := range m.servers {
		_ = srv.Close()
		delete(m.servers, name)
	}
}

// GetServers returns all managed server connections.
func (m *MCPClientManager) GetServers() map[string]*ExternalMCPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*ExternalMCPServer)
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

// MustCloseAll is a convenience for shutdown — panics-safe wrapper.
func MustCloseAllMCPConnections() {
	if globalMCPManager != nil {
		globalMCPManager.CloseAll()
	}
}

// -- Helpers --

var requestIDCounter int64

func nextRequestID() int64 {
	requestIDCounter++
	return requestIDCounter
}

func mustMarshalJSON(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal JSON: %v", err))
	}
	return data
}
