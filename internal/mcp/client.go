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
func (s *ExternalMCPServer) CallTool(name string, args map[string]interface{}) (string, error) {
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
		return "", fmt.Errorf("send tools/call: %w", err)
	}

	resp, err := s.readResponse()
	if err != nil {
		return "", err
	}

	if resp.Error != nil {
		return "", fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse tools/call result: %w", err)
	}

	if result.IsError {
		if len(result.Content) > 0 {
			return "", fmt.Errorf("tool error: %s", result.Content[0].Text)
		}
		return "", fmt.Errorf("tool error (no message)")
	}

	// Concatenate all text content blocks
	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(block.Text)
		}
	}
	return sb.String(), nil
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
		Params:  mustMarshalJSON(map[string]interface{}{
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
