package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const (
	MCPVersion = "2024-11-05"
	ServerName = "shepherd"
	ServerVer  = "0.1.0"
)

// JSON-RPC 2.0 structures
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCP Protocol structures
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type InitializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      ServerInfo   `json:"serverInfo"`
	Capabilities    Capabilities `json:"capabilities"`
}

type Capabilities struct {
	Tools *ToolsCapability `json:"tools,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type Property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type ToolsListResult struct {
	Tools []Tool `json:"tools"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// Server represents the MCP server
type Server struct {
	reader  *bufio.Reader
	writer  io.Writer
	tools   map[string]ToolHandler
	minimal bool
}

// ToolHandler is a function that handles a tool call
type ToolHandler func(args map[string]interface{}) (string, error)

// NewServer creates a daemon-side MCP server with all handlers registered
// in-process. Browser sessions are kept in this process's memory, so this
// constructor must only be used by the long-running shepherd daemon.
//
// If minimal is true, browser tools are not registered.
func NewServer(minimal bool) *Server {
	s := &Server{
		reader:  bufio.NewReader(os.Stdin),
		writer:  os.Stdout,
		tools:   make(map[string]ToolHandler),
		minimal: minimal,
	}
	s.registerTools()
	return s
}

// NewClient creates an MCP server intended to run as a stateless child of
// `claude` (or another MCP host). Core tools (task_*, get_*, skill_load) run
// in-process, but browser tools forward over HTTP to the running shepherd
// daemon — so chrome sessions survive across the per-call lifetime of this
// process.
//
// If minimal is true, browser tools are not registered (the OpenCode/CLI
// minimal contract is unchanged).
func NewClient(minimal bool) *Server {
	s := &Server{
		reader:  bufio.NewReader(os.Stdin),
		writer:  os.Stdout,
		tools:   make(map[string]ToolHandler),
		minimal: minimal,
	}
	s.registerCoreTools()
	if !minimal {
		s.registerBrowserForwarders()
	}
	return s
}

// ExecuteTool runs a registered tool by name. Used by the daemon's
// /api/_internal/mcp/call proxy endpoint to dispatch forwarded calls.
func (s *Server) ExecuteTool(name string, args map[string]interface{}) (string, error) {
	handler, ok := s.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return handler(args)
}

// Run starts the MCP server
func (s *Server) Run() error {
	for {
		line, err := s.reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("읽기 실패: %w", err)
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendError(nil, -32700, "Parse error", nil)
			continue
		}

		s.handleRequest(&req)
	}
}

func (s *Server) handleRequest(req *Request) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "initialized":
		// 알림, 응답 불필요
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(req)
	case "ping":
		s.sendResult(req.ID, map[string]string{})
	default:
		s.sendError(req.ID, -32601, "Method not found", nil)
	}
}

func (s *Server) handleInitialize(req *Request) {
	result := InitializeResult{
		ProtocolVersion: MCPVersion,
		ServerInfo: ServerInfo{
			Name:    ServerName,
			Version: ServerVer,
		},
		Capabilities: Capabilities{
			Tools: &ToolsCapability{},
		},
	}
	s.sendResult(req.ID, result)
}

func (s *Server) handleToolsList(req *Request) {
	tools := ListCoreToolDefs()

	// 위키 도구 추가 (모든 모드에서 사용 가능)
	tools = append(tools, getWikiToolsList()...)

	// 브라우저 도구 추가 (minimal 모드에서는 제외)
	if !s.minimal {
		tools = append(tools, getBrowserToolsList()...)
	}

	s.sendResult(req.ID, ToolsListResult{Tools: tools})
}

func (s *Server) handleToolsCall(req *Request) {
	var params CallToolParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		s.sendError(req.ID, -32602, "Invalid params", nil)
		return
	}

	handler, ok := s.tools[params.Name]
	if !ok {
		s.sendToolResult(req.ID, fmt.Sprintf("Unknown tool: %s", params.Name), true)
		return
	}

	result, err := handler(params.Arguments)
	if err != nil {
		s.sendToolResult(req.ID, err.Error(), true)
		return
	}

	s.sendToolResult(req.ID, result, false)
}

func (s *Server) sendResult(id interface{}, result interface{}) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	s.send(resp)
}

func (s *Server) sendError(id interface{}, code int, message string, data interface{}) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	s.send(resp)
}

func (s *Server) sendToolResult(id interface{}, text string, isError bool) {
	result := CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: text},
		},
		IsError: isError,
	}
	s.sendResult(id, result)
}

func (s *Server) send(resp Response) {
	data, _ := json.Marshal(resp)
	fmt.Fprintln(s.writer, string(data))
}
