package embedded

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Endpoint represents a configured LLM endpoint (OpenAI-compatible).
type Endpoint struct {
	ID      string `mapstructure:"id"`
	Label   string `mapstructure:"label"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
	Enabled bool   `mapstructure:"enabled"`
	// Thinking is stored in the endpoint CRUD settings but the embedded agent
	// loop does not consume it — reasoning is handled natively via the model's
	// reasoning_content field (see ChatMessage.ReasoningContent).
	Thinking      bool `mapstructure:"thinking"`
	MaxIterations int  `mapstructure:"max_iterations"`
	ContextTokens int  `mapstructure:"context_tokens"`
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
	Role       ChatRole   `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	// ReasoningContent holds the model's "thinking" (reasoning_content) for the
	// turn. It is surfaced to live output but never sent back to the server
	// (json:"-"), so it does not pollute the chat history.
	ReasoningContent string `json:"-"`
	// ContentParts, when non-empty, replaces the plain string Content with an
	// OpenAI multimodal content array (text + image_url parts) at marshal time.
	// Used to attach images for vision-capable models. It is never populated by
	// unmarshaling a response (json:"-"); only the marshaler consults it.
	ContentParts []ContentPart `json:"-"`
}

// ContentPart is one element of a multimodal message content array (OpenAI
// vision format): either a text part or an image_url part.
type ContentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL carries an image as a data URL ("data:<mime>;base64,<...>") for
// vision-capable models.
type ImageURL struct {
	URL string `json:"url"`
}

// MarshalJSON renders a ChatMessage. When ContentParts is set, the "content"
// field is emitted as a multimodal array instead of the plain string Content,
// so images reach vision-capable models in OpenAI format.
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type alias ChatMessage
	if len(m.ContentParts) == 0 {
		return json.Marshal(alias(m))
	}
	raw, err := json.Marshal(alias(m))
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	parts, err := json.Marshal(m.ContentParts)
	if err != nil {
		return nil, err
	}
	obj["content"] = parts
	return json.Marshal(obj)
}

// ToolCall represents a tool call from the model.
type ToolCall struct {
	ID   string           `json:"id"`
	Type string           `json:"type"`
	Func ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function details in a tool call.
type ToolCallFunction struct {
	Name string `json:"name"`
	Args string `json:"arguments"`
}

// OpenAIToolDef is an OpenAI-format tool definition.
type OpenAIToolDef struct {
	Type     string         `json:"type"`
	Function OpenAIFunction `json:"function"`
}

// OpenAIFunction is the function definition within a tool.
type OpenAIFunction struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ChatRequest is the request body for /chat/completions.
type ChatRequest struct {
	Model       string          `json:"model"`
	Messages    []ChatMessage   `json:"messages"`
	Tools       []OpenAIToolDef `json:"tools,omitempty"`
	ToolChoice  interface{}     `json:"tool_choice,omitempty"`
	Temperature float32         `json:"temperature,omitempty"`
	// FrequencyPenalty / PresencePenalty discourage the model from looping on
	// the same token/phrase. Local models (llama.cpp) are prone to degenerate
	// repetition when given an open-ended task ("keep testing"), spinning out
	// the same sentence until they hit max_tokens (task #6008).
	FrequencyPenalty float32        `json:"frequency_penalty,omitempty"`
	PresencePenalty  float32        `json:"presence_penalty,omitempty"`
	MaxTokens        int            `json:"max_tokens,omitempty"`
	Stream           bool           `json:"stream"`
	StreamOptions    *StreamOptions `json:"stream_options,omitempty"`
	// Ollama-specific
	Options map[string]interface{} `json:"options,omitempty"`
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
	Index        int         `json:"index"`
	Delta        ChatDelta   `json:"delta"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

// ChatUsage is token usage from a response.
type ChatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// ChatResponse is the complete response from /chat/completions.
type ChatResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []ChatChoice `json:"choices"`
	Usage   ChatUsage    `json:"usage"`
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
	SheepName     string
	ProjectPath   string
	BaseURL       string // OpenAI-compatible base URL (with /v1 suffix)
	APIKey        string // API key (empty allowed for local servers)
	Model         string // Model name
	SystemPrompt  string
	UserPrompt    string
	Tools         []OpenAIToolDef
	OnOutput      func(output string)
	MaxIterations int
	ContextTokens int

	// Vision marks the configured model as vision-capable. When true, read_file
	// surfaces image files (including screenshots the model captures at runtime)
	// as real images instead of a "cannot read binary" notice — independent of
	// whether the task had attached files. See loop.go's SetVision call.
	Vision bool

	// MCPDefs / MCPDispatch wire external MCP tools into the agent loop. Without
	// MCPDispatch set, the loop can only execute native tools and any MCP tool
	// call (e.g. browser_session_start) fails with "unknown tool".
	MCPDefs     []MCPToolDef
	MCPDispatch MCPDispatcher

	// InjectCh receives user prompts to inject mid-execution. Each string is
	// appended as a {role: user} message to the chat history at the next safe
	// point (after the current turn completes). Nil or closed means no injection.
	InjectCh <-chan string

	// ShouldHandoff is consulted when the conversation no longer fits the
	// context window. Returning true means: instead of trimming old turns
	// (which degrades the model), finish this task with a handoff summary and
	// queue the remaining work as a follow-up task via EnqueueFollowUp.
	// The follow-up is queued at a higher priority than ordinary pending tasks,
	// so it runs next regardless of an existing backlog; the caller therefore
	// returns true in the normal case and only returns false as a runaway guard
	// (handoff chain already too deep). Nil (or EnqueueFollowUp nil) → always trim.
	ShouldHandoff func() bool

	// EnqueueFollowUp queues a continuation task with the given prompt.
	EnqueueFollowUp func(prompt string) error
}

// DefaultMaxIterations is the default maximum number of agent loop iterations.
const DefaultMaxIterations = 40

// DefaultContextTokens is the default context window size.
const DefaultContextTokens = 32768

// estimateTextTokens estimates tokens for a text string. ASCII averages ~4
// bytes per token, but CJK (한글 등) averages ~1 token per character — the old
// bytes/4 heuristic undercounted Korean text by 10x+, so trimming ran too
// late and the request overflowed the real context window.
func estimateTextTokens(s string) int {
	ascii := 0
	tokens := 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			tokens++
		}
	}
	return tokens + ascii/4
}

// estimateMessageTokens estimates the token count for a single message.
// Includes Content, ToolCalls (function name + JSON args), and overhead.
func estimateMessageTokens(msg ChatMessage) int {
	tokens := estimateTextTokens(msg.Content)
	for _, p := range msg.ContentParts {
		tokens += estimateTextTokens(p.Text)
		if p.ImageURL != nil {
			// Local LLM servers (llama.cpp, vLLM) tokenize the entire base64
			// data URL as regular text — the cost scales with payload size, not
			// a fixed vision-encoder constant. A 200KB screenshot (~270KB data
			// URL) costs ~68K tokens, far more than the old fixed 2048. Using
			// the actual URL length prevents context overflow that caused
			// "empty response loop detected" failures (task #6698).
			tokens += EstimateImageTokens(p.ImageURL.URL)
		}
	}
	for _, tc := range msg.ToolCalls {
		// name + args JSON + per-call overhead
		tokens += estimateTextTokens(tc.Func.Name) + estimateTextTokens(tc.Func.Args) + 16
	}
	// +50 per-message overhead
	return tokens + 50
}

// trimMessages truncates the message list to stay within context token limits.
// Removes complete "turns" (assistant message + its tool results) from the
// oldest position, preserving the system prompt and the first user message.
func trimMessages(messages []ChatMessage, maxTokens int) []ChatMessage {
	if len(messages) <= 2 {
		return messages
	}

	totalTokens := 0
	for _, msg := range messages {
		totalTokens += estimateMessageTokens(msg)
	}

	// Leave 25% headroom for the model's reply
	limit := maxTokens * 3 / 4
	if totalTokens <= limit {
		return messages
	}

	// Always preserve: [0] system, [1] user (original request)
	system := messages[0]
	userMsg := messages[1]
	candidates := messages[2:] // older turns are at the front

	for len(candidates) > 0 && totalTokens > limit {
		// Find how many messages belong to the next "turn":
		// one assistant message + all immediately following tool results.
		groupEnd := 1
		if candidates[0].Role == ChatRoleAssistant {
			for groupEnd < len(candidates) && candidates[groupEnd].Role == ChatRoleTool {
				groupEnd++
			}
		}
		// Remove the group
		for i := 0; i < groupEnd; i++ {
			totalTokens -= estimateMessageTokens(candidates[i])
		}
		candidates = candidates[groupEnd:]
	}

	result := make([]ChatMessage, 0, 2+len(candidates))
	result = append(result, system, userMsg)
	result = append(result, candidates...)
	return result
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
