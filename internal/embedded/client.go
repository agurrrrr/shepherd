package embedded

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// errRepetitionDetected is a sentinel returned from the stream callback to abort
// generation early when the model degenerates into repeating the same phrase.
// It is not a real failure: AccumulateStream catches it and returns the partial
// content with finishReason "repetition" so the caller can stop cleanly instead
// of burning the whole max_tokens budget on garbage (task #6008).
var errRepetitionDetected = errors.New("degenerate repetition detected")

// Client communicates with an OpenAI-compatible LLM API.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	model      string
}

// NewClient creates a new client for the given endpoint.
func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		httpClient: &http.Client{},
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
	}
}

// StreamEvent is a single SSE chunk from streaming response.
type StreamEvent struct {
	// Delta contains the incremental content or tool call updates.
	Delta        ChatDelta  `json:"delta"`
	// FinishReason is "stop" or "tool_calls" when the stream ends.
	FinishReason *string    `json:"finish_reason,omitempty"`
	// Usage is included in the final chunk.
	Usage        *ChatUsage `json:"usage,omitempty"`
	// Raw is the raw JSON line (useful for debugging).
	Raw          json.RawMessage `json:"-"`
}

// Chat sends a chat request and returns the full response (non-streaming).
func (c *Client) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &chatResp, nil
}

// ChatStream sends a chat request with streaming enabled, calling the callback
// for each delta chunk.
func (c *Client) ChatStream(ctx context.Context, req *ChatRequest, cb func(*StreamEvent) error) error {
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	// Parse SSE stream
	var buf bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Buffer multi-line SSE data
		if strings.HasPrefix(line, "data: ") {
			buf.WriteString(line[6:])
		} else if line == "" && buf.Len() > 0 {
			data := strings.TrimSpace(buf.String())
			buf.Reset()

			if data == "[DONE]" {
				break
			}

			var rawMsg json.RawMessage
			if err := json.Unmarshal([]byte(data), &rawMsg); err != nil {
				continue
			}

			var event StreamEvent
			event.Raw = rawMsg

			type sseChoice struct {
				Index        int         `json:"index"`
				Delta        ChatDelta   `json:"delta"`
				FinishReason *string     `json:"finish_reason"`
			}
			type sseResponse struct {
				Choices []sseChoice `json:"choices"`
				Usage   *ChatUsage  `json:"usage,omitempty"`
			}

			var sseResp sseResponse
			if err := json.Unmarshal(rawMsg, &sseResp); err != nil {
				continue
			}

			if len(sseResp.Choices) > 0 {
				choice := sseResp.Choices[0]
				event.Delta = choice.Delta
				event.FinishReason = choice.FinishReason
			}
			event.Usage = sseResp.Usage

			if err := cb(&event); err != nil {
				return fmt.Errorf("stream callback: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan SSE: %w", err)
	}

	return nil
}

// AccumulateStream runs ChatStream and accumulates the full response.
// Returns the assembled message, finish reason, and token usage.
func (c *Client) AccumulateStream(ctx context.Context, req *ChatRequest) (*ChatMessage, string, *ChatUsage, error) {
	var (
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		byIndex          = make(map[int]*ToolCall)
		order            []int
		finishReason     string
		usage            *ChatUsage
	)

	// Track buffer sizes at the last repetition check so the (relatively
	// expensive) scan runs only once per ~400 new chars, not on every tiny delta.
	var lastRepCheckLen int

	err := c.ChatStream(ctx, req, func(event *StreamEvent) error {
		if event == nil {
			return nil
		}

		if event.Usage != nil {
			usage = event.Usage
		}

		if event.FinishReason != nil {
			finishReason = *event.FinishReason
		}

		if event.Delta.Content != "" {
			contentBuilder.WriteString(event.Delta.Content)
		}

		if event.Delta.ReasoningContent != "" {
			reasoningBuilder.WriteString(event.Delta.ReasoningContent)
		}

		// Abort early if the model has fallen into degenerate repetition. Check
		// reasoning and content separately: a reasoning model can loop forever in
		// reasoning_content while content stays empty (the exact #6008 failure).
		grown := contentBuilder.Len() + reasoningBuilder.Len()
		if grown-lastRepCheckLen >= 400 {
			lastRepCheckLen = grown
			if isDegenerateRepetition(reasoningBuilder.String()) ||
				isDegenerateRepetition(contentBuilder.String()) {
				return errRepetitionDetected
			}
		}

		// Accumulate tool calls by their stream Index. The first chunk for an
		// index carries ID/Type/Name; subsequent chunks carry only argument
		// fragments (with empty ID/Name), so we must merge on Index alone.
		for _, deltaTC := range event.Delta.ToolCalls {
			tc, ok := byIndex[deltaTC.Index]
			if !ok {
				tc = &ToolCall{Type: "function"}
				byIndex[deltaTC.Index] = tc
				order = append(order, deltaTC.Index)
			}
			if deltaTC.ID != "" {
				tc.ID = deltaTC.ID
			}
			if deltaTC.Type != "" {
				tc.Type = deltaTC.Type
			}
			if deltaTC.Func.Name != "" {
				tc.Func.Name = deltaTC.Func.Name
			}
			tc.Func.Args += deltaTC.Func.Args
		}

		return nil
	})

	if err != nil {
		// Repetition abort is not a transport error: keep the partial output and
		// signal it via a synthetic finish reason so the loop can stop cleanly.
		if errors.Is(err, errRepetitionDetected) {
			finishReason = "repetition"
		} else {
			return nil, "", nil, err
		}
	}

	toolCalls := make([]ToolCall, 0, len(order))
	for _, idx := range order {
		toolCalls = append(toolCalls, *byIndex[idx])
	}

	content := contentBuilder.String()
	msg := &ChatMessage{
		Role:             ChatRoleAssistant,
		Content:          content,
		ToolCalls:        toolCalls,
		ReasoningContent: reasoningBuilder.String(),
	}

	return msg, finishReason, usage, nil
}

// isDegenerateRepetition reports whether the tail of s is dominated by the same
// chunk repeated over and over — the signature of a model stuck in a generation
// loop. It catches two shapes seen in practice (task #6008):
//   - the same non-trivial line repeated many times (newline-delimited), and
//   - a short phrase repeated back-to-back with no newlines.
//
// Only the last few KB are inspected so the scan stays cheap, and short/trivial
// units are ignored so ordinary repetition (e.g. "    " indentation, "yes yes")
// does not trip it.
func isDegenerateRepetition(s string) bool {
	const window = 4000
	if len(s) > window {
		s = s[len(s)-window:]
	}
	return tailLinesRepeating(s) || tailPhraseRepeating(s)
}

// tailLinesRepeating returns true when the last several non-empty lines are all
// identical and non-trivial.
func tailLinesRepeating(s string) bool {
	const need = 8
	lines := strings.Split(s, "\n")
	last := make([]string, 0, need)
	for i := len(lines) - 1; i >= 0 && len(last) < need; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			last = append(last, t)
		}
	}
	if len(last) < need || len(last[0]) < 10 {
		return false
	}
	for _, l := range last[1:] {
		if l != last[0] {
			return false
		}
	}
	return true
}

// tailPhraseRepeating returns true when the very end of s is a short unit (4..300
// chars) repeated at least 8 times consecutively, even without line breaks.
func tailPhraseRepeating(s string) bool {
	n := len(s)
	if n < 200 {
		return false
	}
	const reps = 8
	for p := 4; p <= 300 && p*reps <= n; p++ {
		unit := s[n-p:]
		repeated := true
		for k := 2; k <= reps; k++ {
			if s[n-p*k:n-p*(k-1)] != unit {
				repeated = false
				break
			}
		}
		if repeated {
			return true
		}
	}
	return false
}
