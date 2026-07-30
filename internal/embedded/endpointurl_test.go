package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveChatURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// The point of the change: custom layouts are called verbatim.
		{"full openai url", "http://127.0.0.1:8080/v1/chat/completions", "http://127.0.0.1:8080/v1/chat/completions"},
		{"custom path", "https://gw.example.com/api/llm/generate", "https://gw.example.com/api/llm/generate"},
		{"query string preserved", "https://x.openai.azure.com/openai/deployments/d/chat/completions?api-version=2024-02-01", "https://x.openai.azure.com/openai/deployments/d/chat/completions?api-version=2024-02-01"},
		{"trailing slash kept", "https://gw.example.com/api/chat/", "https://gw.example.com/api/chat/"},
		{"other api version", "https://gw.example.com/v2/chat/completions", "https://gw.example.com/v2/chat/completions"},

		// Legacy shapes shepherd used to produce stay working.
		{"legacy v1 base", "http://127.0.0.1:8080/v1", "http://127.0.0.1:8080/v1/chat/completions"},
		{"legacy v1 base trailing slash", "http://127.0.0.1:8080/v1/", "http://127.0.0.1:8080/v1/chat/completions"},
		{"legacy v1 uppercase", "http://127.0.0.1:8080/V1", "http://127.0.0.1:8080/V1/chat/completions"},
		{"legacy bare host", "http://127.0.0.1:8080", "http://127.0.0.1:8080/v1/chat/completions"},
		{"legacy bare host slash", "http://127.0.0.1:8080/", "http://127.0.0.1:8080/v1/chat/completions"},
		{"legacy nested v1", "https://gw.example.com/openai/v1", "https://gw.example.com/openai/v1/chat/completions"},

		{"whitespace trimmed", "  http://127.0.0.1:8080/v1/chat/completions  ", "http://127.0.0.1:8080/v1/chat/completions"},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveChatURL(tc.in); got != tc.want {
				t.Errorf("ResolveChatURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveModelsURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"openai layout", "http://127.0.0.1:8080/v1/chat/completions", "http://127.0.0.1:8080/v1/models"},
		{"root layout", "http://127.0.0.1:8080/chat/completions", "http://127.0.0.1:8080/models"},
		{"trailing slash", "http://127.0.0.1:8080/v1/chat/completions/", "http://127.0.0.1:8080/v1/models"},
		{"custom path has no sibling models", "https://gw.example.com/api/llm/generate", ""},
		{"bare host", "http://127.0.0.1:8080", ""},
		{"empty", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveModelsURL(tc.in); got != tc.want {
				t.Errorf("ResolveModelsURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestClientPostsConfiguredURLVerbatim is the regression guard: a non-OpenAI
// path must be requested exactly as configured, with no suffix appended.
func TestClientPostsConfiguredURLVerbatim(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/api/llm/generate?api-version=2024-02-01", "", "test-model")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := c.Chat(ctx, &ChatRequest{Model: "test-model", Messages: []ChatMessage{{Role: ChatRoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	if gotPath != "/api/llm/generate" {
		t.Errorf("POST path = %q, want %q", gotPath, "/api/llm/generate")
	}
	if gotQuery != "api-version=2024-02-01" {
		t.Errorf("POST query = %q, want %q", gotQuery, "api-version=2024-02-01")
	}
}

// TestHealthCheckCustomPathFallback: with no /models sibling to probe, a
// non-2xx-but-alive answer on the chat URL still counts as healthy, while a
// 5xx does not.
func TestHealthCheckCustomPathFallback(t *testing.T) {
	status := http.StatusMethodNotAllowed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/api/llm/generate", "", "test-model")
	if err := c.HealthCheck(context.Background(), 5*time.Second); err != nil {
		t.Errorf("405 on a POST-only route should count as alive, got %v", err)
	}

	status = http.StatusServiceUnavailable
	if err := c.HealthCheck(context.Background(), 5*time.Second); err == nil {
		t.Error("503 should count as not ready, got nil")
	}
}
