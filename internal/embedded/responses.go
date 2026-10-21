package embedded

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// OpenAI Responses API support.
//
// Some models are not served on /chat/completions at all: GitHub Copilot
// advertises them with only "/responses" in supported_endpoints (GPT-6.1 Sol,
// for example). The agent loop speaks Chat Completions internally, so this file
// translates a ChatRequest into a Responses request, and translates the
// Responses SSE events back into the StreamEvent shape the accumulator expects.
//
// The translation is intentionally lossless only for what shepherd uses:
// messages, tool definitions, tool calls, tool results, images and usage.

// isResponsesURL reports whether the configured endpoint URL targets the
// Responses API, which is what selects the request/response translation.
func isResponsesURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return strings.EqualFold(lastSegment(strings.TrimRight(u.Path, "/")), "responses")
}

type responsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []interface{}   `json:"input"`
	Tools           []responsesTool `json:"tools,omitempty"`
	ToolChoice      interface{}     `json:"tool_choice,omitempty"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Stream          bool            `json:"stream"`
	// Store=false keeps Copilot stateless; shepherd replays the conversation
	// itself, so the server does not need to retain responses.
	Store bool `json:"store"`
}

type responsesTool struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesInputMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

// buildResponsesRequest converts the loop's ChatRequest to the Responses API
// shape. Temperature and the repetition penalties are omitted: reasoning models
// served on /responses commonly reject them, and the streaming repetition guard
// remains the hard backstop.
func buildResponsesRequest(req *ChatRequest, stream bool) *responsesRequest {
	out := &responsesRequest{
		Model:           req.Model,
		Stream:          stream,
		MaxOutputTokens: req.MaxTokens,
		Store:           false,
	}
	if req.ToolChoice != nil {
		out.ToolChoice = req.ToolChoice
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, responsesTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}

	systemSeen := false
	for _, m := range req.Messages {
		switch m.Role {
		case ChatRoleSystem:
			// The leading system message becomes instructions; later system
			// messages stay in-band so mid-run system reminders are not lost.
			if !systemSeen {
				out.Instructions = m.Content
				systemSeen = true
				continue
			}
			out.Input = append(out.Input, responsesInputMessage{
				Role:    string(ChatRoleSystem),
				Content: m.Content,
			})
		case ChatRoleTool:
			out.Input = append(out.Input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": m.ToolCallID,
				"output":  m.Content,
			})
		case ChatRoleAssistant:
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					out.Input = append(out.Input, map[string]interface{}{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Func.Name,
						"arguments": tc.Func.Args,
					})
				}
			}
			if m.Content != "" || len(m.ContentParts) > 0 {
				out.Input = append(out.Input, responsesInputMessage{
					Role:    string(ChatRoleAssistant),
					Content: responsesMessageContent(m),
				})
			}
		default:
			out.Input = append(out.Input, responsesInputMessage{
				Role:    string(m.Role),
				Content: responsesMessageContent(m),
			})
		}
	}
	return out
}

// responsesMessageContent renders plain text as a string and multimodal content
// as Responses input parts (input_text / input_image).
func responsesMessageContent(m ChatMessage) interface{} {
	if len(m.ContentParts) == 0 {
		return m.Content
	}
	parts := make([]responsesContentPart, 0, len(m.ContentParts))
	for _, p := range m.ContentParts {
		switch p.Type {
		case "image_url":
			if p.ImageURL != nil {
				parts = append(parts, responsesContentPart{Type: "input_image", ImageURL: p.ImageURL.URL})
			}
		default:
			parts = append(parts, responsesContentPart{Type: "input_text", Text: p.Text})
		}
	}
	return parts
}

// responsesStreamEvent covers the subset of the Responses streaming events the
// agent loop consumes. Only `delta`, `output_index`, `item` and `response` are
// used; the event type drives the mapping.
type responsesStreamEvent struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int             `json:"output_index"`
	Item        responsesItem   `json:"item"`
	Response    *responsesReply `json:"response"`
	Message     string          `json:"message"`
}

type responsesItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesReply struct {
	Status            string               `json:"status"`
	Output            []responsesItem      `json:"output"`
	Usage             *responsesUsage      `json:"usage"`
	IncompleteDetails *responsesIncomplete `json:"incomplete_details"`
	Error             *responsesError      `json:"error"`
}

type responsesUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type responsesIncomplete struct {
	Reason string `json:"reason"`
}

type responsesError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func responsesUsageToChat(u *responsesUsage) *ChatUsage {
	if u == nil {
		return nil
	}
	return &ChatUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
}

// responsesEventToStream maps one Responses SSE event to a StreamEvent. A nil
// event with a nil error means "nothing for the accumulator". Errors abort the
// stream (failed response or a bare error event).
func responsesEventToStream(data []byte) (*StreamEvent, error) {
	var ev responsesStreamEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, nil
	}
	out := &StreamEvent{}

	switch ev.Type {
	case "response.output_text.delta":
		out.Delta.Content = ev.Delta
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		out.Delta.ReasoningContent = ev.Delta
	case "response.output_item.added":
		if ev.Item.Type == "function_call" {
			out.Delta.ToolCalls = []DeltaToolCall{{
				Index: ev.OutputIndex,
				ID:    ev.Item.CallID,
				Type:  "function",
				Func:  ToolCallFunction{Name: ev.Item.Name, Args: ev.Item.Arguments},
			}}
		}
	case "response.function_call_arguments.delta":
		out.Delta.ToolCalls = []DeltaToolCall{{
			Index: ev.OutputIndex,
			Func:  ToolCallFunction{Args: ev.Delta},
		}}
	case "response.completed", "response.incomplete":
		if ev.Response == nil {
			return nil, nil
		}
		out.Usage = responsesUsageToChat(ev.Response.Usage)
		reason := "stop"
		if ev.Response.Status == "incomplete" {
			reason = "length"
			if ev.Response.IncompleteDetails != nil &&
				ev.Response.IncompleteDetails.Reason == "content_filter" {
				reason = "content_filter"
			}
		}
		for _, it := range ev.Response.Output {
			if it.Type == "function_call" {
				reason = "tool_calls"
				break
			}
		}
		out.FinishReason = &reason
	case "response.failed":
		msg := "response failed"
		if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
			msg = ev.Response.Error.Message
		}
		return nil, fmt.Errorf("responses API: %s", msg)
	case "error":
		msg := ev.Message
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("responses API: %s", msg)
	default:
		return nil, nil
	}

	return out, nil
}

// responsesResponse is the non-streaming response body.
type responsesResponse struct {
	ID     string          `json:"id"`
	Model  string          `json:"model"`
	Status string          `json:"status"`
	Output []responsesItem `json:"output"`
	Usage  *responsesUsage `json:"usage"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesToChatResponse flattens a non-streaming Responses body into the
// ChatResponse shape used by Client.Chat.
func responsesToChatResponse(raw []byte) (*ChatResponse, error) {
	var resp struct {
		ID     string          `json:"id"`
		Model  string          `json:"model"`
		Status string          `json:"status"`
		Error  *responsesError `json:"error"`
		Output []struct {
			Type      string                   `json:"type"`
			CallID    string                   `json:"call_id"`
			Name      string                   `json:"name"`
			Arguments string                   `json:"arguments"`
			Content   []responsesOutputContent `json:"content"`
		} `json:"output"`
		Usage *responsesUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Status == "failed" && resp.Error != nil {
		return nil, fmt.Errorf("responses API: %s", resp.Error.Message)
	}

	msg := ChatMessage{Role: ChatRoleAssistant}
	var text strings.Builder
	for _, item := range resp.Output {
		switch item.Type {
		case "function_call":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   item.CallID,
				Type: "function",
				Func: ToolCallFunction{Name: item.Name, Args: item.Arguments},
			})
		case "message":
			for _, c := range item.Content {
				if c.Type == "output_text" {
					text.WriteString(c.Text)
				}
			}
		}
	}
	msg.Content = text.String()

	finish := "stop"
	if len(msg.ToolCalls) > 0 {
		finish = "tool_calls"
	} else if resp.Status == "incomplete" {
		finish = "length"
	}
	return &ChatResponse{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []ChatChoice{{
			Message:      msg,
			FinishReason: finish,
		}},
		Usage: derefUsage(responsesUsageToChat(resp.Usage)),
	}, nil
}

func derefUsage(u *ChatUsage) ChatUsage {
	if u == nil {
		return ChatUsage{}
	}
	return *u
}
