package embedded

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint represents a configured LLM endpoint (OpenAI-compatible).
type Endpoint struct {
	ID             string `mapstructure:"id"`
	Label          string `mapstructure:"label"`
	BaseURL        string `mapstructure:"base_url"`
	APIKey         string `mapstructure:"api_key"`
	Model          string `mapstructure:"model"`
	Enabled        bool   `mapstructure:"enabled"`
	Thinking       bool   `mapstructure:"thinking"`
	MaxIterations  int    `mapstructure:"max_iterations"`
	ContextTokens  int    `mapstructure:"context_tokens"`
}

// Config holds embedded provider settings loaded from embedded.yaml.
type Config struct {
	Endpoints []Endpoint `mapstructure:"endpoints"`
}

// ChatRole is the message role in chat completions.
type ChatRole string

const (
	ChatRoleSystem    ChatRole = "system"
	ChatRoleUser      ChatRole = "user"
	ChatRoleAssistant ChatRole = "assistant"
	ChatRoleTool      ChatRole = "tool"
)

// ChatMessage represents a message in the chat history.
type ChatMessage struct {
	Role      ChatRole   `json:"role"`
	Content   string     `json:"content"`
	Name      string     `json:"name,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool call from the model.
type ToolCall struct {
	ID   string                 `json:"id"`
	Type string                 `json:"type"`
	Func ToolCallFunction       `json:"function"`
}

// ToolCallFunction is the function details in a tool call.
type ToolCallFunction struct {
	Name string `json:"name"`
	Args string `json:"arguments"`
}

// OpenAIToolDef is an OpenAI-format tool definition.
type OpenAIToolDef struct {
	Type     string          `json:"type"`
	Function OpenAIFunction  `json:"function"`
}

// OpenAIFunction is the function definition within a tool.
type OpenAIFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is the request body for /chat/completions.
type ChatRequest struct {
	Model         string                `json:"model"`
	Messages      []ChatMessage         `json:"messages"`
	Tools         []OpenAIToolDef       `json:"tools,omitempty"`
	ToolChoice    interface{}           `json:"tool_choice,omitempty"`
	Temperature   float32               `json:"temperature,omitempty"`
	MaxTokens     int                   `json:"max_tokens,omitempty"`
	Stream        bool                  `json:"stream"`
	StreamOptions *StreamOptions        `json:"stream_options,omitempty"`
	// Ollama-specific
	Options       map[string]interface{} `json:"options,omitempty"`
	// Thinking/reasoning
	ExtraProperties map[string]interface{} `json:"-"`
}

// StreamOptions requests usage statistics in the final streaming chunk.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// ChatDelta is a streaming delta from the model.
type ChatDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []DeltaToolCall `json:"tool_calls,omitempty"`
	// Reasoning models
	ReasoningContent string `json:"reasoning_content,omitempty"`
}

// DeltaToolCall is a streaming tool-call fragment. OpenAI-compatible providers
// split a single tool call across many chunks keyed by Index: only the first
// chunk carries ID/Type/Name, later chunks carry argument fragments with empty
// ID and Name. Accumulation must therefore key on Index, not ID+Name.
type DeltaToolCall struct {
	Index int              `json:"index"`
	ID    string           `json:"id"`
	Type  string           `json:"type"`
	Func  ToolCallFunction `json:"function"`
}

// ChatChoice is a choice in the response.
type ChatChoice struct {
	Index        int       `json:"index"`
	Delta        ChatDelta `json:"delta"`
	Message      ChatMessage `json:"message"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

// ChatUsage is token usage from a response.
type ChatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// ChatResponse is the complete response from /chat/completions.
type ChatResponse struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
}

// StreamHandler processes streaming deltas from the model.
type StreamHandler func(delta *ChatDelta, usage *ChatUsage) error

// SSEEvent represents a Server-Sent Events chunk.
type SSEEvent struct {
	Event string
	Data  string
}

// ExecuteResult is the result of an embedded execution, matching the worker
// package's ExecuteResult contract.
type ExecuteResult struct {
	Result           string
	SessionID        string
	FilesModified    []string
	PromptTokens     int64
	CompletionTokens int64
	CostUSD          float64
	Incomplete       bool
	IncompleteReason string
}

// ExecuteOptions contains options for embedded execution.
type ExecuteOptions struct {
	SheepName      string
	ProjectPath    string
	BaseURL        string // OpenAI-compatible base URL (with /v1 suffix)
	APIKey         string // API key (empty allowed for local servers)
	Model          string // Model name
	SystemPrompt   string
	UserPrompt     string
	Tools          []OpenAIToolDef
	OnOutput       func(output string)
	MaxIterations  int
	ContextTokens  int
}

// DefaultMaxIterations is the default maximum number of agent loop iterations.
const DefaultMaxIterations = 40

// DefaultContextTokens is the default context window size.
const DefaultContextTokens = 32768

// parseSSE parses SSE stream from reader. Each chunk is "event: <type>\ndata: <json>\n\n".
func parseSSE(reader io.Reader) ([]*SSEEvent, error) {
	buf, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}

	var events []*SSEEvent
	var currentEvent, currentData string

	for _, line := range strings.Split(string(buf), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if currentEvent != "" || currentData != "" {
				events = append(events, &SSEEvent{
					Event: currentEvent,
					Data:  currentData,
				})
				currentEvent = ""
				currentData = ""
			}
			continue
		}

		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			currentData = strings.TrimPrefix(line, "data: ")
		}
	}

	return events, nil
}

// trimMessages truncates the message list to stay within context token limits.
// Uses a simple heuristic: each message is roughly estimated at 100 tokens.
func trimMessages(messages []ChatMessage, maxTokens int) []ChatMessage {
	if len(messages) <= 1 {
		return messages
	}

	// Estimate tokens: rough heuristic of 1 token per 4 bytes
	estimateTokens := func(msg ChatMessage) int {
		return len(msg.Content)/4 + 50
	}

	totalTokens := 0
	for _, msg := range messages {
		totalTokens += estimateTokens(msg)
	}

	if totalTokens < maxTokens {
		return messages
	}

	// Keep system message (index 0) and user message, trim from the middle
	// (oldest tool call results first)
	system := messages[0]
	remaining := messages[1:]

	for len(remaining) > 1 {
		totalTokens -= estimateTokens(remaining[0])
		remaining = remaining[1:]
		if totalTokens < maxTokens {
			break
		}
	}

	return append([]ChatMessage{system}, remaining...)
}

// ValidateEndpoint checks if an endpoint is properly configured.
func ValidateEndpoint(ep *Endpoint) error {
	if ep.ID == "" {
		return fmt.Errorf("endpoint ID is required")
	}
	if ep.BaseURL == "" {
		return fmt.Errorf("endpoint %s: base_url is required", ep.ID)
	}
	if ep.Model == "" {
		return fmt.Errorf("endpoint %s: model is required", ep.ID)
	}
	// Ensure base_url ends with /v1 for consistency
	if !strings.HasSuffix(strings.ToLower(ep.BaseURL), "/v1") {
		ep.BaseURL = strings.TrimRight(ep.BaseURL, "/") + "/v1"
	}
	if ep.MaxIterations <= 0 {
		ep.MaxIterations = DefaultMaxIterations
	}
	if ep.ContextTokens <= 0 {
		ep.ContextTokens = DefaultContextTokens
	}
	return nil
}

// TestConnection checks if an endpoint is reachable by listing available models.
func TestConnection(ctx context.Context, ep *Endpoint) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Use a simple HTTP GET to check connectivity
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(ep.BaseURL + "/models")
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}
