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
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/agurrrrr/shepherd/internal/llmslots"
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
	// semaphore limits concurrent LLM calls to the same endpoint. nil means
	// unlimited. Acquired in AccumulateStreamWithProgress (the single gate
	// for all streaming LLM calls) and released on completion.
	semaphore *llmslots.Semaphore
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

// SetSemaphore sets the endpoint concurrency limiter. Called by the loop
// before the first LLM call. nil means unlimited.
func (c *Client) SetSemaphore(sem *llmslots.Semaphore) {
	c.semaphore = sem
}

// StreamEvent is a single SSE chunk from streaming response.
type StreamEvent struct {
	// Delta contains the incremental content or tool call updates.
	Delta ChatDelta `json:"delta"`
	// FinishReason is "stop" or "tool_calls" when the stream ends.
	FinishReason *string `json:"finish_reason,omitempty"`
	// Usage is included in the final chunk.
	Usage *ChatUsage `json:"usage,omitempty"`
	// Raw is the raw JSON line (useful for debugging).
	Raw json.RawMessage `json:"-"`
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
	return c.ChatStreamWithProgress(ctx, req, cb, nil)
}

// ChatStreamWithProgress is like ChatStream but sends periodic progress messages
// via onProgress when no chunks arrive (task #6955, §4.6). This makes long prompt
// processing visible — llama.cpp stays silent during prompt evaluation (up to
// ~12 min for 92K context), which previously looked like a hang.
func (c *Client) ChatStreamWithProgress(ctx context.Context, req *ChatRequest, cb func(*StreamEvent) error, onProgress func(string)) error {
	if req.MaxTokens == 0 {
		req.MaxTokens = 4096
	}
	req.Stream = true

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// --- B1 fix (task #6955, §4.5): create the cancelable context BEFORE
	// building the HTTP request so that cancel() actually propagates to the
	// in-flight request. The old code created httpReq with the parent ctx,
	// then rebound a local ctx — the timer's cancel() never reached the HTTP
	// request's context.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(streamCtx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
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

	// --- B1: idle timeout with health check (task #6955, §4.5).
	// When no chunk arrives for idleTimeout, we DON'T immediately abort.
	// Instead we do a health check: if the server responds (it's just slow
	// processing the prompt), reset the timer and keep waiting. Only if the
	// server is truly unresponsive do we abort.
	const idleTimeout = 5 * time.Minute
	const hcTimeout = 5 * time.Second

	var idleTriggered atomic.Bool
	idleTimer := time.NewTimer(idleTimeout)
	defer idleTimer.Stop()

	// Progress ticker: sends "⏳ processing..." every 30s before first chunk.
	var firstChunkReceived atomic.Bool
	progressDone := make(chan struct{})
	if onProgress != nil {
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			start := time.Now()
			for {
				select {
				case <-progressDone:
					return
				case <-ticker.C:
					if !firstChunkReceived.Load() {
						elapsed := time.Since(start).Round(time.Second)
						onProgress(fmt.Sprintf("⏳ LLM 프롬프트 처리 중... (%s 경과)", elapsed))
					}
				}
			}
		}()
	}
	defer close(progressDone)

	// Idle monitor goroutine: waits for the idle timer to fire, then does a
	// health check instead of immediately aborting.
	idleDone := make(chan struct{})
	go func() {
		defer close(idleDone)
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-idleTimer.C:
				// Timer fired — check if server is still alive.
				hcCtx, hcCancel := context.WithTimeout(context.Background(), hcTimeout)
				hcErr := c.HealthCheck(hcCtx, hcTimeout)
				hcCancel()
				if hcErr == nil {
					// Server is alive — just slow. Reset and keep waiting.
					idleTimer.Reset(idleTimeout)
					continue
				}
				// Server is unresponsive — abort.
				idleTriggered.Store(true)
				cancel()
				return
			}
		}
	}()

	resetIdle := func() {
		idleTimer.Reset(idleTimeout)
	}
	resetIdle()

	// --- SSE parsing ---
	var buf bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 256*1024)

	flushBuffer := func(data string) error {
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			return nil
		}

		var rawMsg json.RawMessage
		if err := json.Unmarshal([]byte(data), &rawMsg); err != nil {
			return nil // skip malformed lines
		}

		var event StreamEvent
		event.Raw = rawMsg

		type sseChoice struct {
			Index        int       `json:"index"`
			Delta        ChatDelta `json:"delta"`
			FinishReason *string   `json:"finish_reason"`
		}
		type sseResponse struct {
			Choices []sseChoice `json:"choices"`
			Usage   *ChatUsage  `json:"usage,omitempty"`
		}

		var sseResp sseResponse
		if err := json.Unmarshal(rawMsg, &sseResp); err != nil {
			return nil
		}

		if len(sseResp.Choices) > 0 {
			choice := sseResp.Choices[0]
			event.Delta = choice.Delta
			event.FinishReason = choice.FinishReason
		}
		event.Usage = sseResp.Usage

		return cb(&event)
	}

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			buf.WriteString(line[6:])
		} else if line == "" && buf.Len() > 0 {
			if err := flushBuffer(buf.String()); err != nil {
				return fmt.Errorf("stream callback: %w", err)
			}
			buf.Reset()
			resetIdle() // B1: received a chunk — reset idle timer
			firstChunkReceived.Store(true)
		}
	}

	// A6: After the scanner loop ends, flush any remaining data in the buffer.
	// Some servers close the connection without a trailing blank line after the
	// last data: chunk, which would silently discard finish_reason/usage.
	if buf.Len() > 0 {
		if err := flushBuffer(buf.String()); err != nil {
			return fmt.Errorf("stream callback: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		// B1: if the idle timer fired, return a clear error.
		if idleTriggered.Load() {
			return fmt.Errorf("stream idle timeout after 5m")
		}
		return fmt.Errorf("scan SSE: %w", err)
	}

	cancel()
	idleTimer.Stop()
	<-idleDone

	// B1: If the parent context was cancelled (not by us), propagate that.
	if idleTriggered.Load() {
		return fmt.Errorf("stream idle timeout after 5m")
	}

	return nil
}

// AccumulateStream runs ChatStream and accumulates the full response.
// Returns the assembled message, finish reason, and token usage.
func (c *Client) AccumulateStream(ctx context.Context, req *ChatRequest) (*ChatMessage, string, *ChatUsage, error) {
	return c.AccumulateStreamWithProgress(ctx, req, nil, nil)
}

// AccumulateStreamWithProgress is like AccumulateStream but forwards progress
// messages (for long prompt processing visibility, task #6955 §4.6) and live
// token deltas via onToken (may be nil).
//
// This is the single gate for all streaming LLM calls: AccumulateStream,
// AccumulateStreamWithRetry, and AccumulateStreamProposer all funnel through
// here. The endpoint semaphore is acquired before the call and released after,
// so a parent agent waiting for spawn_subagents results (which makes no LLM
// calls during the wait) automatically frees its slot for sub-agents.
func (c *Client) AccumulateStreamWithProgress(ctx context.Context, req *ChatRequest, onProgress func(string), onToken func(string)) (*ChatMessage, string, *ChatUsage, error) {
	// Acquire endpoint slot before making the LLM call. This blocks if the
	// endpoint is at capacity. When nil (max_concurrent=0), it is a no-op.
	// ctx cancellation propagates: if the context is cancelled while waiting
	// for a slot, Acquire returns ctx.Err() immediately (Phase 2: goroutine
	// leak prevention).
	if err := c.semaphore.Acquire(ctx); err != nil {
		return nil, "", nil, fmt.Errorf("semaphore acquire cancelled: %w", err)
	}
	defer c.semaphore.Release()

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

	err := c.ChatStreamWithProgress(ctx, req, func(event *StreamEvent) error {
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
			if onToken != nil {
				onToken(event.Delta.Content)
			}
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
	}, onProgress)

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
	return tailLinesRepeating(s) || tailPhraseRepeating(s) || tailLinesCycling(s)
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

// tailLinesCycling detects alternating (A/B/A/B) repetition among the last
// non-empty lines — the exact pattern that defeated tailLinesRepeating in
// task #6944: two Korean sentences took turns, so no two adjacent lines were
// identical, yet the model was stuck in a degenerate loop.
//
// Returns true when the last `window` non-empty lines contain at most 2
// distinct values, each at least 10 runes long, and each appearing 4+ times.
func tailLinesCycling(s string) bool {
	const window = 12
	lines := strings.Split(s, "\n")
	last := make([]string, 0, window)
	for i := len(lines) - 1; i >= 0 && len(last) < window; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			last = append(last, t)
		}
	}
	if len(last) < window {
		return false
	}
	// Count distinct lines and their frequencies.
	freq := make(map[string]int)
	for _, l := range last {
		freq[l]++
	}
	if len(freq) > 2 {
		return false
	}
	// Each distinct line must be non-trivial (>= 10 runes) and appear >= 4 times.
	for line, count := range freq {
		if utf8.RuneCountInString(line) < 10 {
			return false
		}
		if count < 4 {
			return false
		}
	}
	return true
}

// tailPhraseRepeating returns true when the very end of s is a short unit (4..1200
// chars) repeated at least 8 times consecutively, even without line breaks.
// The upper bound was raised from 300 to 1200 (task #6955, §4.2) to catch Korean
// repetition cycles where two sentences alternate with a period > 300 bytes
// (Korean is ~3 bytes/char, so two sentences easily exceed 300 bytes).
func tailPhraseRepeating(s string) bool {
	n := len(s)
	if n < 200 {
		return false
	}
	const reps = 8
	const maxPeriod = 1200
	for p := 4; p <= maxPeriod && p*reps <= n; p++ {
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

// ─── Transient error retry infrastructure (task #6955, §4.1) ────────────────

// isTransientLLMError reports whether an LLM API error is likely temporary and
// worth retrying. This distinguishes "connection dropped / server restarted /
// overloaded" (retry-worthy) from "malformed request / auth failure" (fatal).
//
// Cases classified as transient:
//   - unexpected EOF / connection reset / broken pipe — server restarted or
//     network blip (#6945: user restarted llama.cpp mid-stream)
//   - connection refused — server still booting after restart
//   - HTTP 5xx (502 overloaded, 503 unavailable, 529 overloaded) — temporary
//     server overload (#6943: umans 502)
//   - idle timeout — server stalled but might recover
//
// Cases classified as fatal (no retry):
//   - context.Canceled — user stopped the task; must not retry
//   - context.DeadlineExceeded — the call budget is spent; retrying can't help
//   - HTTP 4xx (400 bad request, 401/403 auth) — retrying won't help
//   - errRepetitionDetected — model degeneration, not a transport error
func isTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	// User cancellation and deadline expiry are never transient: retrying a
	// canceled/expired context fails immediately again, and for MAGI proposers a
	// hit deadline is the *expected* convergence trigger, not a transport blip
	// (previously classified non-transient only implicitly, via string matching;
	// task #7081 review).
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Repetition abort is not a transport error.
	if errors.Is(err, errRepetitionDetected) {
		return false
	}
	s := err.Error()
	// Connection-level transient errors
	transientSubstrings := []string{
		"unexpected EOF",
		"connection reset",
		"broken pipe",
		"connection refused",
		"EOF",
		"idle timeout",
		"scan SSE", // scanner errors from connection drops
	}
	for _, sub := range transientSubstrings {
		if strings.Contains(s, sub) {
			return true
		}
	}
	// HTTP 5xx errors (extracted from "API error 502: ..." format)
	for code := 500; code <= 530; code++ {
		if strings.Contains(s, fmt.Sprintf("API error %d", code)) {
			return true
		}
	}
	// Also catch 529 (overloaded_error used by Anthropic-compatible APIs)
	if strings.Contains(s, "API error 529") || strings.Contains(s, "overloaded") {
		return true
	}
	return false
}

// HealthCheck pings the /models endpoint to verify the server is alive and
// ready to accept requests. Returns nil if the server responds within the
// timeout. Used by AccumulateStreamWithRetry to wait for server recovery
// before retrying after a transient error.
func (c *Client) HealthCheck(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("health check: HTTP %d", resp.StatusCode)
}

// retryConfig holds parameters for transient-error retries.
type retryConfig struct {
	maxRetries     int           // max retry attempts (not counting the initial try)
	initialDelay   time.Duration // delay before first retry
	maxDelay       time.Duration // cap on delay between retries
	totalWaitLimit time.Duration // total wall-clock budget for all retries
}

// defaultRetryConfig is the main-agent policy: patient reconnection across a
// long-running interactive session (up to ~62s of backoff over 5 retries).
var defaultRetryConfig = retryConfig{
	maxRetries:     5,
	initialDelay:   2 * time.Second,
	maxDelay:       60 * time.Second,
	totalWaitLimit: 10 * time.Minute,
}

// proposerRetryConfig is the MAGI-proposer policy: a short budget so a dead
// endpoint fails fast instead of burning the whole per-proposer timeout on the
// main-agent's patient policy (task #7077 MELCHIOR — a 500-ing endpoint spent
// ~62s exhausting the default 6 attempts, then all accumulated work was
// discarded). The caller's ctx deadline bounds this further.
var proposerRetryConfig = retryConfig{
	maxRetries:     3,
	initialDelay:   1 * time.Second,
	maxDelay:       6 * time.Second,
	totalWaitLimit: 40 * time.Second,
}

// nextDelay computes exponential backoff for the given attempt.
func (rc retryConfig) nextDelay(attempt int) time.Duration {
	d := rc.initialDelay << uint(attempt) // e.g. 2s, 4s, 8s, 16s, 32s...
	if d > rc.maxDelay {
		d = rc.maxDelay
	}
	return d
}

// AccumulateStreamWithRetry wraps AccumulateStream with automatic retry on
// transient errors using the main-agent policy. Between retries it waits for the
// server to recover via health checks. The OnOutput callback (if set) receives
// status messages so the user can see what's happening.
func (c *Client) AccumulateStreamWithRetry(ctx context.Context, req *ChatRequest, onOutput func(string), onToken func(string)) (*ChatMessage, string, *ChatUsage, error) {
	return c.accumulateStreamWithRetry(ctx, req, defaultRetryConfig, onOutput, onToken)
}

// AccumulateStreamProposer is AccumulateStreamWithRetry with the short
// proposerRetryConfig budget (task #7077). MAGI proposers use it so one dead
// endpoint cannot hold a per-proposer budget hostage.
func (c *Client) AccumulateStreamProposer(ctx context.Context, req *ChatRequest, onOutput func(string), onToken func(string)) (*ChatMessage, string, *ChatUsage, error) {
	return c.accumulateStreamWithRetry(ctx, req, proposerRetryConfig, onOutput, onToken)
}

// accumulateStreamWithRetry is the shared retry loop. The retry budget is
// bounded by the smaller of rc.totalWaitLimit and the ctx deadline, so retries
// never outlive the caller's own timeout (task #7077).
func (c *Client) accumulateStreamWithRetry(ctx context.Context, req *ChatRequest, rc retryConfig, onOutput func(string), onToken func(string)) (*ChatMessage, string, *ChatUsage, error) {
	deadline := time.Now().Add(rc.totalWaitLimit)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	var lastErr error
	for attempt := 0; attempt <= rc.maxRetries; attempt++ {
		msg, finishReason, usage, err := c.AccumulateStreamWithProgress(ctx, req, onOutput, onToken)
		if err == nil {
			return msg, finishReason, usage, nil
		}
		lastErr = err

		// Not transient — don't retry.
		if !isTransientLLMError(err) {
			return nil, "", nil, err
		}

		// Last attempt or deadline exceeded — give up.
		if attempt == rc.maxRetries || !time.Now().Before(deadline) {
			if onOutput != nil {
				onOutput(fmt.Sprintf("⚠️ LLM 서버 재연결 한계 초과 (%d/%d 시도). 작업을 중단합니다.",
					attempt+1, rc.maxRetries+1))
			}
			return nil, "", nil, fmt.Errorf("transient error after %d retries: %w", attempt+1, err)
		}

		delay := rc.nextDelay(attempt)

		if onOutput != nil {
			onOutput(fmt.Sprintf("⚠️ LLM 서버 연결 끊김 — 재연결 대기 중 (%d/%d)...",
				attempt+1, rc.maxRetries))
		}

		// Wait for server recovery via health check before retrying.
		hcCtx, hcCancel := context.WithTimeout(ctx, delay)
		hcErr := c.waitForRecovery(hcCtx)
		hcCancel()

		// If health check failed within delay window, wait out the remaining
		// delay (respecting ctx cancellation).
		if hcErr != nil {
			select {
			case <-ctx.Done():
				return nil, "", nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		if !time.Now().Before(deadline) {
			if onOutput != nil {
				onOutput("⚠️ LLM 서버 재연결 대기 시간 초과. 작업을 중단합니다.")
			}
			return nil, "", nil, fmt.Errorf("transient error: recovery timed out after %s: %w", rc.totalWaitLimit, err)
		}
	}

	return nil, "", nil, lastErr
}

// waitForRecovery polls the /models endpoint until it succeeds or ctx expires.
// Returns nil if the server recovered, an error otherwise.
func (c *Client) waitForRecovery(ctx context.Context) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		if err := c.HealthCheck(ctx, 5*time.Second); err == nil {
			return nil // server is back
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue
		}
	}
}
