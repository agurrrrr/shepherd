package embedded

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildResponsesRequest(t *testing.T) {
	req := &ChatRequest{
		Model: "gpt-6.1-sol",
		Messages: []ChatMessage{
			{Role: ChatRoleSystem, Content: "system prompt"},
			{Role: ChatRoleUser, Content: "hello"},
			{Role: ChatRoleAssistant, Content: "calling", ToolCalls: []ToolCall{{
				ID: "call_1", Type: "function",
				Func: ToolCallFunction{Name: "bash", Args: `{"command":"ls"}`},
			}}},
			{Role: ChatRoleTool, ToolCallID: "call_1", Content: "file.txt"},
			{Role: ChatRoleUser, ContentParts: []ContentPart{
				{Type: "text", Text: "look"},
				{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAA"}},
			}},
		},
		Tools: []OpenAIToolDef{{
			Type: "function",
			Function: OpenAIFunction{
				Name:        "bash",
				Description: "run a command",
				Parameters:  map[string]interface{}{"type": "object"},
			},
		}},
		ToolChoice:       "auto",
		Temperature:      0.7,
		FrequencyPenalty: 0.1,
		MaxTokens:        4096,
		Stream:           true,
	}

	out := buildResponsesRequest(req, true)
	if out.Instructions != "system prompt" {
		t.Fatalf("instructions = %q", out.Instructions)
	}
	if out.Model != "gpt-6.1-sol" || out.MaxOutputTokens != 4096 || !out.Stream || out.Store {
		t.Fatalf("unexpected request: %+v", out)
	}
	if len(out.Tools) != 1 || out.Tools[0].Type != "function" || out.Tools[0].Name != "bash" {
		t.Fatalf("tools = %+v", out.Tools)
	}
	if out.ToolChoice != "auto" {
		t.Fatalf("tool_choice = %v", out.ToolChoice)
	}

	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"temperature", "frequency_penalty", "presence_penalty", "messages"} {
		if _, ok := body[key]; ok {
			t.Errorf("responses request must not carry %q", key)
		}
	}

	if len(out.Input) != 5 {
		t.Fatalf("input = %d items, want 5: %+v", len(out.Input), out.Input)
	}
	if _, ok := out.Input[0].(responsesInputMessage); !ok {
		t.Errorf("first input item = %T, want user message", out.Input[0])
	}
	fc, ok := out.Input[1].(map[string]interface{})
	if !ok || fc["type"] != "function_call" || fc["call_id"] != "call_1" || fc["name"] != "bash" {
		t.Errorf("function_call item = %+v", out.Input[1])
	}
	fco, ok := out.Input[3].(map[string]interface{})
	if !ok || fco["type"] != "function_call_output" || fco["call_id"] != "call_1" || fco["output"] != "file.txt" {
		t.Errorf("function_call_output item = %+v", out.Input[3])
	}
	parts, ok := out.Input[4].(responsesInputMessage).Content.([]responsesContentPart)
	if !ok || len(parts) != 2 || parts[0].Type != "input_text" || parts[1].Type != "input_image" {
		t.Errorf("vision parts = %+v", out.Input[4])
	}
}

func TestResponsesEventToStream(t *testing.T) {
	ev, err := responsesEventToStream([]byte(`{"type":"response.output_text.delta","delta":"hello"}`))
	if err != nil || ev == nil || ev.Delta.Content != "hello" {
		t.Fatalf("text delta: %+v err=%v", ev, err)
	}

	ev, err = responsesEventToStream([]byte(`{"type":"response.reasoning_summary_text.delta","delta":"think"}`))
	if err != nil || ev == nil || ev.Delta.ReasoningContent != "think" {
		t.Fatalf("reasoning delta: %+v err=%v", ev, err)
	}

	ev, err = responsesEventToStream([]byte(`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"bash","arguments":""}}`))
	if err != nil || ev == nil || len(ev.Delta.ToolCalls) != 1 {
		t.Fatalf("item added: %+v err=%v", ev, err)
	}
	tc := ev.Delta.ToolCalls[0]
	if tc.Index != 1 || tc.ID != "call_1" || tc.Type != "function" || tc.Func.Name != "bash" {
		t.Fatalf("tool call = %+v", tc)
	}

	ev, err = responsesEventToStream([]byte(`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"command\":\"ls\"}"}`))
	if err != nil || ev == nil || ev.Delta.ToolCalls[0].Func.Args != `{"command":"ls"}` {
		t.Fatalf("args delta: %+v err=%v", ev, err)
	}

	ev, err = responsesEventToStream([]byte(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15},"output":[{"type":"function_call"}]}}`))
	if err != nil || ev == nil || ev.FinishReason == nil || *ev.FinishReason != "tool_calls" {
		t.Fatalf("completed: %+v err=%v", ev, err)
	}
	if ev.Usage == nil || ev.Usage.PromptTokens != 10 || ev.Usage.CompletionTokens != 5 || ev.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", ev.Usage)
	}

	if _, err := responsesEventToStream([]byte(`{"type":"response.failed","response":{"error":{"message":"boom"}}}`)); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("failed event err = %v", err)
	}
}

func TestClientResponsesModeStreamsAndTranslates(t *testing.T) {
	var srv *httptest.Server
	var gotBody map[string]interface{}
	var gotPath string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exchange":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "copilot-token",
				"expires_at": 4102444800,
				"endpoints":  map[string]string{"api": srv.URL},
			})
		case "/responses":
			gotPath = r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(
				"event: response.output_text.delta\n" +
					"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n" +
					"event: response.output_item.added\n" +
					"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"bash\",\"arguments\":\"\"}}\n\n" +
					"event: response.function_call_arguments.delta\n" +
					"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"command\\\":\\\"ls\\\"}\"}\n\n" +
					"event: response.completed\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15},\"output\":[{\"type\":\"function_call\"}]}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	c := NewClientWithAuth(srv.URL+"/responses", "", "gpt-6.1-sol", AuthCopilot, "ghu_responses_test")
	msg, finish, usage, err := c.AccumulateStream(context.Background(), &ChatRequest{
		Model:     "gpt-6.1-sol",
		Messages:  []ChatMessage{{Role: ChatRoleSystem, Content: "sys"}, {Role: ChatRoleUser, Content: "hi"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/responses" {
		t.Fatalf("path = %q, want /responses", gotPath)
	}
	if gotBody["model"] != "gpt-6.1-sol" || gotBody["instructions"] != "sys" || gotBody["stream"] != true {
		t.Fatalf("request body = %+v", gotBody)
	}
	if _, ok := gotBody["messages"]; ok {
		t.Fatalf("chat-completions messages leaked into responses request: %+v", gotBody)
	}
	if msg.Content != "hello " {
		t.Fatalf("content = %q", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_1" || msg.ToolCalls[0].Func.Name != "bash" || msg.ToolCalls[0].Func.Args != `{"command":"ls"}` {
		t.Fatalf("tool calls = %+v", msg.ToolCalls)
	}
	if finish != "tool_calls" {
		t.Fatalf("finish = %q", finish)
	}
	if usage == nil || usage.PromptTokens != 10 || usage.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", usage)
	}
}
