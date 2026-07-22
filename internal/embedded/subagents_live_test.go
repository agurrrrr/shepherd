package embedded

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/llmslots"
)

// Live test against agents-a1-4b (llama-server --parallel 8).
// Run: LIVE_SUBAGENT8=1 go test ./internal/embedded -run TestLiveSpawnSubagents_Eight -count=1 -v -timeout 5m
func TestLiveSpawnSubagents_Eight(t *testing.T) {
	if os.Getenv("LIVE_SUBAGENT8") == "" {
		t.Skip("set LIVE_SUBAGENT8=1 to run live 8-way subagent test against agents-a1-4b")
	}

	baseURL := envOr("LIVE_BASE_URL", "http://127.0.0.1:8090/v1")
	apiKey := os.Getenv("LIVE_API_KEY")
	if apiKey == "" {
		// Prefer key from embedded.yaml via env; never hardcode secrets in tests.
		t.Skip("set LIVE_API_KEY (and optionally LIVE_BASE_URL / LIVE_MODEL) for live test")
	}
	model := envOr("LIVE_MODEL", "agents-a1-4b")

	llmslots.Reset()
	sem := llmslots.Global().Get("agents-a1-4b-live", 8)

	var peak, current int32
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		if err := sem.Acquire(ctx); err != nil {
			return nil, err
		}
		defer sem.Release()

		n := atomic.AddInt32(&current, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		defer atomic.AddInt32(&current, -1)

		content, err := liveChat(ctx, baseURL, apiKey, model, name, prompt)
		if err != nil {
			return nil, err
		}
		return &SubagentResult{Content: content, PromptTokens: 10, CompletionTokens: 5}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}
	list := make([]interface{}, 8)
	tasks := []string{
		"Summarize in 1 sentence what a ToolRegistry does in an agent loop.",
		"Name 3 read-only tools suitable for sub-agents.",
		"What does max_concurrent mean for an LLM endpoint?",
		"Why depth-1 spawn matters for sub-agents.",
		"One risk of parallel sub-agent live output without line coalescing.",
		"What is the role of llmslots semaphore?",
		"When should endpoint_id be omitted in spawn_subagents?",
		"One-line definition of handoff in embedded context management.",
	}
	for i := 0; i < 8; i++ {
		list[i] = map[string]interface{}{
			"name":   fmt.Sprintf("live%d", i),
			"prompt": tasks[i],
		}
	}

	var outputs []string
	onOut := func(s string) {
		outputs = append(outputs, s)
		t.Log(s)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	start := time.Now()
	result, err := executeSpawnSubagents(ctx, tr, map[string]interface{}{"subagents": list}, onOut)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("executeSpawnSubagents: %v", err)
	}
	if result == nil || result.Content == "" {
		t.Fatal("empty combined result")
	}

	// Count success markers
	okCount := 0
	for i := 0; i < 8; i++ {
		if containsName(result.Content, fmt.Sprintf("live%d", i)) {
			okCount++
		}
	}
	t.Logf("wall=%v peak_in_flight=%d ok_names=%d content_chars=%d",
		elapsed.Round(time.Millisecond), peak, okCount, len([]rune(result.Content)))
	t.Logf("usage prompt=%d completion=%d", result.PromptTokens, result.CompletionTokens)

	if peak < 6 {
		t.Fatalf("expected high concurrency (peak>=6), got peak=%d", peak)
	}
	if okCount < 8 {
		t.Fatalf("expected all 8 agent names in result, found %d; content:\n%s", okCount, result.Content)
	}
	// Sequential would be ~8x single latency; concurrent should be near 1x.
	// Soft check only: wall under 90s for 8 tiny completions.
	if elapsed > 90*time.Second {
		t.Fatalf("wall clock too high for concurrent 8: %v", elapsed)
	}
}

func containsName(s, name string) bool {
	return len(s) > 0 && (bytesContains(s, name) || bytesContains(s, "### "+name) || bytesContains(s, "[SUB:"+name))
}

func bytesContains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(len(s) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func liveChat(ctx context.Context, baseURL, apiKey, model, name, prompt string) (string, error) {
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a concise shepherd sub-agent. Answer in 1-2 short sentences. Start with your name tag."},
			{"role": "user", "content": fmt.Sprintf("[%s] %s", name, prompt)},
		},
		"max_tokens":  120,
		"temperature": 0.2,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateRaw(string(raw), 300))
	}
	var cr struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("empty choices")
	}
	c := cr.Choices[0].Message.Content
	if c == "" {
		// thinking-only models: surface a stub so parent still sees the agent
		c = fmt.Sprintf("%s: (reasoning-only response)", name)
	}
	return c, nil
}

func truncateRaw(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
