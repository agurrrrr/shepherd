package embedded

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	if len(models) != 1 {
		t.Fatalf("models = %+v, want only the chat-completions model", models)
	}
	m := models[0]
	if m.ID != "gpt-4o" || m.ContextTokens() != 96000 || m.ContextWindowTokens != 128000 || !m.SupportsTools || !m.SupportsVision {
		t.Fatalf("unexpected model: %+v (context=%d)", m, m.ContextTokens())
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
