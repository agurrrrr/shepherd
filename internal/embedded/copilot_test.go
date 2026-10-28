package embedded

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func withCopilotURLs(t *testing.T, device, access, exchange string) {
	t.Helper()
	oldDevice, oldAccess, oldExchange := copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenExchangeURL
	copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenExchangeURL = device, access, exchange
	t.Cleanup(func() {
		copilotDeviceCodeURL, copilotAccessTokenURL, copilotTokenExchangeURL = oldDevice, oldAccess, oldExchange
	})
}

func TestCopilotDeviceFlow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"device_code":      "dev-1",
				"user_code":        "ABCD-1234",
				"verification_uri": "https://github.com/login/device",
				"expires_in":       900,
				"interval":         5,
			})
		case "/token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["device_code"] != "dev-1" {
				t.Errorf("unexpected device_code %q", body["device_code"])
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "ghu_test"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	dev, err := StartCopilotDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("StartCopilotDeviceFlow: %v", err)
	}
	if dev.DeviceCode != "dev-1" || dev.UserCode != "ABCD-1234" || dev.Interval != 5 {
		t.Fatalf("unexpected device code: %+v", dev)
	}

	res, err := PollCopilotDeviceFlow(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("PollCopilotDeviceFlow: %v", err)
	}
	if res.Status != CopilotPollAuthorized || res.GitHubToken != "ghu_test" {
		t.Fatalf("unexpected poll result: %+v", res)
	}
}

func TestPollCopilotDeviceFlowPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	res, err := PollCopilotDeviceFlow(context.Background(), "dev-1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != CopilotPollPending {
		t.Fatalf("status = %q, want pending", res.Status)
	}
}

func TestCopilotTokenCacheAndInvalidate(t *testing.T) {
	var exchanges atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		if got := r.Header.Get("Authorization"); got != "token ghu_cache_test" {
			t.Errorf("exchange Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "copilot-token",
			"expires_at": 4102444800,
			"endpoints":  map[string]string{"api": "https://api.githubcopilot.com"},
		})
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	ctx := context.Background()
	tok, err := CopilotToken(ctx, "ghu_cache_test")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Token != "copilot-token" {
		t.Fatalf("token = %q", tok.Token)
	}
	if _, err := CopilotToken(ctx, "ghu_cache_test"); err != nil {
		t.Fatal(err)
	}
	if got := exchanges.Load(); got != 1 {
		t.Fatalf("exchanges = %d, want 1 (cache hit)", got)
	}
	InvalidateCopilotToken("ghu_cache_test")
	if _, err := CopilotToken(ctx, "ghu_cache_test"); err != nil {
		t.Fatal(err)
	}
	if got := exchanges.Load(); got != 2 {
		t.Fatalf("exchanges after invalidate = %d, want 2", got)
	}
}

func TestListCopilotModelsFiltersAndLimits(t *testing.T) {
	var srv *httptest.Server
	var exchanges atomic.Int32
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exchange":
			exchanges.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "copilot-token",
				"expires_at": 4102444800,
				"endpoints":  map[string]string{"api": srv.URL},
			})
		case "/models":
			if got := r.Header.Get("Copilot-Integration-Id"); got != "copilot-developer-cli" {
				t.Errorf("Copilot-Integration-Id = %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer copilot-token" {
				t.Errorf("Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"id":   "gpt-4o",
						"name": "GPT-4o",
						"capabilities": map[string]interface{}{
							"type": "chat",
							"limits": map[string]interface{}{
								"max_context_window_tokens": 128000,
								"max_prompt_tokens":         96000,
								"max_output_tokens":         16384,
							},
							"supports": map[string]interface{}{"tool_calls": true, "vision": true},
						},
						"supported_endpoints": []string{"/chat/completions", "/responses"},
					},
					{
						"id":                  "gpt-5-responses-only",
						"name":                "Responses only",
						"capabilities":        map[string]interface{}{"type": "chat"},
						"supported_endpoints": []string{"/responses"},
					},
					{
						"id":           "embedding-1",
						"capabilities": map[string]interface{}{"type": "embeddings"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	models, apiBase, err := ListCopilotModels(context.Background(), "ghu_models_test")
	if err != nil {
		t.Fatal(err)
	}
	if apiBase != srv.URL {
		t.Fatalf("apiBase = %q", apiBase)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v, want chat-completions and responses models", models)
	}
	m := models[0]
	if m.ID != "gpt-4o" || m.ContextTokens() != 96000 || m.ContextWindowTokens != 128000 || !m.SupportsTools || !m.SupportsVision || m.API != "chat" {
		t.Fatalf("unexpected model: %+v (context=%d)", m, m.ContextTokens())
	}
	if models[1].ID != "gpt-5-responses-only" || models[1].API != "responses" {
		t.Fatalf("responses-only model not exposed: %+v", models[1])
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d", exchanges.Load())
	}
}

func TestCopilotClientHeadersURLAnd401Retry(t *testing.T) {
	var chatCalls atomic.Int32
	var exchangeCalls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exchange":
			n := exchangeCalls.Add(1)
			token := "copilot-token-1"
			if n > 1 {
				token = "copilot-token-2"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      token,
				"expires_at": 4102444800,
				"endpoints":  map[string]string{"api": srv.URL},
			})
		case "/chat/completions":
			if got := r.Header.Get("Copilot-Integration-Id"); got != "copilot-developer-cli" {
				t.Errorf("Copilot-Integration-Id = %q", got)
			}
			if got := r.Header.Get("Editor-Version"); got == "" {
				t.Error("Editor-Version header missing")
			}
			n := chatCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("expired token"))
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer copilot-token-2" {
				t.Errorf("Authorization after 401 = %q, want refreshed token", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":    "chat-1",
				"model": "gpt-4o",
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "hi"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")

	c := NewClientWithAuth("https://api.githubcopilot.com/chat/completions", "", "gpt-4o", AuthCopilot, "ghu_client_test")
	resp, err := c.Chat(context.Background(), &ChatRequest{
		Model:    "gpt-4o",
		Messages: []ChatMessage{{Role: ChatRoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "hi" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if chatCalls.Load() != 2 {
		t.Fatalf("chat calls = %d, want 2 (401 then retry)", chatCalls.Load())
	}
	if exchangeCalls.Load() != 2 {
		t.Fatalf("exchange calls = %d, want 2 (401 invalidates cache)", exchangeCalls.Load())
	}
}

// copilotVisionServer serves the token exchange, a /models list with image
// limits, and a chat endpoint that records what each chat request carried.
type copilotVisionCall struct {
	visionHeader string
	images       []string
	texts        []string
}

func copilotVisionServer(t *testing.T, chatPath string, modelsStatus int, calls *[]copilotVisionCall, modelsCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exchange":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "copilot-token",
				"expires_at": 4102444800,
				"endpoints":  map[string]string{"api": srv.URL},
			})
		case "/models":
			modelsCalls.Add(1)
			if modelsStatus != http.StatusOK {
				w.WriteHeader(modelsStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{{
					"id": "vision-model",
					"capabilities": map[string]interface{}{
						"type": "chat",
						"limits": map[string]interface{}{
							"max_prompt_tokens": 64000,
							"vision": map[string]interface{}{
								"max_prompt_image_size": 3145728,
								"max_prompt_images":     2,
							},
						},
						"supports": map[string]interface{}{"tool_calls": true, "vision": true},
					},
				}},
			})
		case chatPath:
			var body struct {
				Messages []struct {
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
				Input []struct {
					Content json.RawMessage `json:"content"`
				} `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			call := copilotVisionCall{visionHeader: r.Header.Get("Copilot-Vision-Request")}
			contents := make([]json.RawMessage, 0)
			for _, m := range body.Messages {
				contents = append(contents, m.Content)
			}
			for _, m := range body.Input {
				contents = append(contents, m.Content)
			}
			for _, c := range contents {
				var parts []struct {
					Type     string          `json:"type"`
					Text     string          `json:"text"`
					ImageURL json.RawMessage `json:"image_url"`
				}
				if json.Unmarshal(c, &parts) != nil {
					continue
				}
				for _, p := range parts {
					switch p.Type {
					case "image_url":
						var u struct {
							URL string `json:"url"`
						}
						_ = json.Unmarshal(p.ImageURL, &u)
						call.images = append(call.images, u.URL)
					case "input_image":
						var u string
						_ = json.Unmarshal(p.ImageURL, &u)
						call.images = append(call.images, u)
					default:
						call.texts = append(call.texts, p.Text)
					}
				}
			}
			*calls = append(*calls, call)
			if chatPath == "/responses" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"status": "completed",
					"output": []map[string]interface{}{{
						"type":    "message",
						"content": []map[string]string{{"type": "output_text", "text": "ok"}},
					}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": "ok"}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	withCopilotURLs(t, srv.URL+"/device", srv.URL+"/token", srv.URL+"/exchange")
	return srv
}

// screenshotHistory is a conversation with three screenshots, oldest first.
func screenshotHistory() []ChatMessage {
	img := func(name string) ChatMessage {
		return ChatMessage{Role: ChatRoleUser, ContentParts: []ContentPart{
			{Type: "text", Text: "Attached image(s) from the tool call(s) above:"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64," + name}},
		}}
	}
	return []ChatMessage{
		{Role: ChatRoleSystem, Content: "system"},
		{Role: ChatRoleUser, Content: "test the page"},
		img("AAAA"),
		img("BBBB"),
		img("CCCC"),
	}
}

// TestCopilotVisionHeaderAndImageLimit: a Copilot request with images must
// carry Copilot-Vision-Request and at most the model's max_prompt_images
// (newest first). Without the header, screenshot tasks on Copilot stopped
// with "empty response loop detected".
func TestCopilotVisionHeaderAndImageLimit(t *testing.T) {
	var calls []copilotVisionCall
	var modelsCalls atomic.Int32
	copilotVisionServer(t, "/chat/completions", http.StatusOK, &calls, &modelsCalls)

	c := NewClientWithAuth("https://api.githubcopilot.com/chat/completions", "", "vision-model", AuthCopilot, "ghu_vision_limit_test")
	ctx := context.Background()

	if _, err := c.Chat(ctx, &ChatRequest{Model: "vision-model", Messages: []ChatMessage{{Role: ChatRoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("text Chat: %v", err)
	}
	history := screenshotHistory()
	for i := 0; i < 2; i++ {
		if _, err := c.Chat(ctx, &ChatRequest{Model: "vision-model", Messages: history}); err != nil {
			t.Fatalf("image Chat: %v", err)
		}
	}

	if len(calls) != 3 {
		t.Fatalf("chat calls = %d, want 3", len(calls))
	}
	if calls[0].visionHeader != "" || len(calls[0].images) != 0 {
		t.Fatalf("text-only request: header=%q images=%v, want no vision header", calls[0].visionHeader, calls[0].images)
	}
	img := calls[1]
	if img.visionHeader != "true" {
		t.Fatalf("Copilot-Vision-Request = %q, want true", img.visionHeader)
	}
	want := []string{"data:image/png;base64,BBBB", "data:image/png;base64,CCCC"}
	if len(img.images) != 2 || img.images[0] != want[0] || img.images[1] != want[1] {
		t.Fatalf("images sent = %v, want the newest two %v", img.images, want)
	}
	omitted := false
	for _, txt := range img.texts {
		if strings.Contains(txt, "Earlier image omitted") {
			omitted = true
		}
	}
	if !omitted {
		t.Fatalf("texts = %v, want a note for the omitted image", img.texts)
	}
	if got := modelsCalls.Load(); got != 1 {
		t.Fatalf("/models calls = %d, want 1 (looked up once, only for image requests)", got)
	}
	// The loop's history must keep every image.
	if n := countImageParts(history); n != 3 {
		t.Fatalf("history images = %d after the call, want 3 (input must not change)", n)
	}
}

// TestCopilotImageLimitFallback: when /models cannot be read, one image (the
// newest) is sent, and the failed lookup is cached instead of repeated.
func TestCopilotImageLimitFallback(t *testing.T) {
	var calls []copilotVisionCall
	var modelsCalls atomic.Int32
	copilotVisionServer(t, "/chat/completions", http.StatusInternalServerError, &calls, &modelsCalls)

	c := NewClientWithAuth("https://api.githubcopilot.com/chat/completions", "", "vision-model", AuthCopilot, "ghu_vision_fallback_test")
	for i := 0; i < 2; i++ {
		if _, err := c.Chat(context.Background(), &ChatRequest{Model: "vision-model", Messages: screenshotHistory()}); err != nil {
			t.Fatalf("Chat: %v", err)
		}
	}
	if len(calls) != 2 || len(calls[0].images) != 1 || calls[0].images[0] != "data:image/png;base64,CCCC" {
		t.Fatalf("calls = %+v, want only the newest image", calls)
	}
	if calls[0].visionHeader != "true" {
		t.Fatalf("Copilot-Vision-Request = %q, want true", calls[0].visionHeader)
	}
	if got := modelsCalls.Load(); got != 1 {
		t.Fatalf("/models calls = %d, want 1 (failure cached)", got)
	}
}

// TestCopilotResponsesVisionHeader: the Responses API path (GPT-6.1 Sol) needs
// the same header and limit for input_image parts.
func TestCopilotResponsesVisionHeader(t *testing.T) {
	var calls []copilotVisionCall
	var modelsCalls atomic.Int32
	copilotVisionServer(t, "/responses", http.StatusOK, &calls, &modelsCalls)

	c := NewClientWithAuth("https://api.githubcopilot.com/responses", "", "vision-model", AuthCopilot, "ghu_vision_responses_test")
	if _, err := c.Chat(context.Background(), &ChatRequest{Model: "vision-model", Messages: screenshotHistory()}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(calls) != 1 || calls[0].visionHeader != "true" || len(calls[0].images) != 2 {
		t.Fatalf("calls = %+v, want vision header and 2 input_image parts", calls)
	}
}
