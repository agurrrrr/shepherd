package embedded

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// ── TodoState unit tests ──────────────────────────────────────────

func TestTodoStateEmptyNotIncomplete(t *testing.T) {
	var s TodoState
	if s.HasIncomplete() {
		t.Error("empty state must not be incomplete (gate must not fire)")
	}
	if !s.IsEmpty() {
		t.Error("expected empty")
	}
	if s.Summary() != "No tasks currently tracked." {
		t.Errorf("summary = %q", s.Summary())
	}
}

func TestTodoApplyReplaceAndMerge(t *testing.T) {
	var s TodoState
	if err := s.ApplyReplace([]TodoUpdate{
		{ID: "1", Content: "Build", Status: TodoInProgress},
		{ID: "2", Content: "Test", Status: TodoPending},
	}); err != nil {
		t.Fatal(err)
	}
	if !s.HasIncomplete() {
		t.Fatal("expected incomplete")
	}
	// Status-only merge preserves content.
	if err := s.ApplyMerge([]TodoUpdate{
		{ID: "1", Status: TodoCompleted},
		{ID: "3", Content: "Deploy", Status: TodoPending},
	}); err != nil {
		t.Fatal(err)
	}
	items := s.Items()
	if len(items) != 3 {
		t.Fatalf("len=%d want 3", len(items))
	}
	if items[0].Content != "Build" || items[0].Status != TodoCompleted {
		t.Errorf("item0 = %+v", items[0])
	}
	if items[2].Content != "Deploy" {
		t.Errorf("item2 = %+v", items[2])
	}
}

func TestTodoApplyReplaceClears(t *testing.T) {
	var s TodoState
	_ = s.ApplyReplace([]TodoUpdate{{ID: "old", Content: "Old", Status: TodoPending}})
	_ = s.ApplyReplace([]TodoUpdate{{ID: "new", Content: "New", Status: TodoCompleted}})
	if s.HasIncomplete() {
		t.Error("all completed must not be incomplete")
	}
	if len(s.Items()) != 1 || s.Items()[0].ID != "new" {
		t.Errorf("items = %+v", s.Items())
	}
}

func TestTodoAutoMergeOnStatusOnlyForgotMergeFlag(t *testing.T) {
	// Model sets merge:false but only sends id+status for existing items.
	var s TodoState
	_ = s.Apply(false, []TodoUpdate{
		{ID: "1", Content: "Explore", Status: TodoInProgress},
		{ID: "2", Content: "Write", Status: TodoPending},
	})
	if err := s.Apply(false, []TodoUpdate{
		{ID: "1", Status: TodoCompleted},
		{ID: "2", Status: TodoInProgress},
	}); err != nil {
		t.Fatal(err)
	}
	items := s.Items()
	if items[0].Content != "Explore" || items[0].Status != TodoCompleted {
		t.Errorf("content wiped? %+v", items[0])
	}
	if items[1].Content != "Write" || items[1].Status != TodoInProgress {
		t.Errorf("item1 = %+v", items[1])
	}
}

func TestTodoDuplicateIDRejected(t *testing.T) {
	var s TodoState
	err := s.ApplyReplace([]TodoUpdate{
		{ID: "dup", Content: "A", Status: TodoPending},
		{ID: "dup", Content: "B", Status: TodoPending},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestTodoInvalidStatusRejected(t *testing.T) {
	var s TodoState
	err := s.ApplyReplace([]TodoUpdate{{ID: "1", Content: "x", Status: "wip"}})
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("want invalid status error, got %v", err)
	}
}

func TestTodoContentFallbackToID(t *testing.T) {
	var s TodoState
	_ = s.ApplyMerge([]TodoUpdate{{ID: "explore_codebase", Status: TodoCompleted}})
	it := s.Items()[0]
	if it.Content != "explore_codebase" || it.Status != TodoCompleted {
		t.Errorf("got %+v", it)
	}
}

func TestTodoAllCompletedOrCancelledNotIncomplete(t *testing.T) {
	var s TodoState
	_ = s.ApplyReplace([]TodoUpdate{
		{ID: "1", Content: "A", Status: TodoCompleted},
		{ID: "2", Content: "B", Status: TodoCancelled},
	})
	if s.HasIncomplete() {
		t.Error("terminal statuses must not be incomplete")
	}
}

func TestParseTodoWriteArgsAliases(t *testing.T) {
	merge, updates, err := parseTodoWriteArgs(map[string]interface{}{
		"merge": false,
		"steps": []interface{}{
			map[string]interface{}{"text": "Do thing", "status": "in_progress"},
			map[string]interface{}{"id": "x", "description": "Other", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if merge {
		t.Error("merge should be false")
	}
	if len(updates) != 2 || updates[0].ID != "1" || updates[0].Content != "Do thing" {
		t.Errorf("updates = %+v", updates)
	}
	if updates[1].ID != "x" || updates[1].Content != "Other" {
		t.Errorf("updates[1] = %+v", updates[1])
	}
}

func TestBuildTodoGateReminder(t *testing.T) {
	body := buildTodoGateReminder([]string{"p1"}, []string{"ip1"})
	if !strings.Contains(body, todoGateNudgeLead) {
		t.Error("missing lead")
	}
	if !strings.Contains(body, "Pending:") || !strings.Contains(body, "- p1") {
		t.Errorf("pending section missing: %q", body)
	}
	if !strings.Contains(body, "In-progress:") || !strings.Contains(body, "- ip1") {
		t.Errorf("in-progress section missing: %q", body)
	}
	// Empty buckets omitted.
	onlyP := buildTodoGateReminder([]string{"only"}, nil)
	if strings.Contains(onlyP, "In-progress:") {
		t.Error("empty in-progress section should be omitted")
	}
}

// ── Tool registry opt-in ──────────────────────────────────────────

func TestTodoWriteToolOptInOffByDefault(t *testing.T) {
	tr := NewToolRegistry(t.TempDir(), "sheep", nil, nil)
	if tr.TodoEnabled() {
		t.Fatal("todo must be off by default")
	}
	for _, def := range tr.OpenAIToolDefs() {
		if def.Function.Name == "todo_write" {
			t.Fatal("todo_write must not appear when disabled")
		}
	}
	_, err := tr.Dispatch(context.Background(), "todo_write", map[string]interface{}{
		"todos": []interface{}{map[string]interface{}{"id": "1", "content": "x", "status": "pending"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("want unknown tool when disabled, got %v", err)
	}
}

func TestTodoWriteToolEnableAndUpdate(t *testing.T) {
	tr := NewToolRegistry(t.TempDir(), "sheep", nil, nil)
	tr.EnableTodo()
	if !tr.TodoEnabled() {
		t.Fatal("expected enabled")
	}
	found := false
	for _, def := range tr.OpenAIToolDefs() {
		if def.Function.Name == "todo_write" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("todo_write missing from defs")
	}

	out, err := tr.Dispatch(context.Background(), "todo_write", map[string]interface{}{
		"merge": false,
		"todos": []interface{}{
			map[string]interface{}{"id": "1", "content": "A", "status": "in_progress"},
			map[string]interface{}{"id": "2", "content": "B", "status": "pending"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "[in_progress]") {
		t.Errorf("out = %q", out)
	}
	if !tr.Todo().HasIncomplete() {
		t.Error("expected incomplete after write")
	}

	// Status-only merge.
	_, err = tr.Dispatch(context.Background(), "todo_write", map[string]interface{}{
		"todos": []interface{}{
			map[string]interface{}{"id": "1", "status": "completed"},
			map[string]interface{}{"id": "2", "status": "completed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Todo().HasIncomplete() {
		t.Error("all completed should clear incomplete")
	}
}

// ── Loop: nudge bound + opt-in off ────────────────────────────────

// roundTodoWrite then content-only incomplete completion attempts.
func TestTodoGateNudgeBoundThenIncomplete(t *testing.T) {
	// Round 0: model calls todo_write with incomplete items.
	todoArgs := `{"merge":false,"todos":[{"id":"1","content":"Still open","status":"pending"}]}`
	r0 := buildSSELines([]toolCallSpec{
		{id: "c1", name: "todo_write", args: todoArgs},
	}, "", "tool_calls")
	// Rounds 1–3: content-only "done" claims → 2 nudges then incomplete.
	// Avoid future-intention / pause-summary patterns so only the todo gate fires.
	r1 := buildSSELines(nil, "작업을 모두 완료했습니다.", "stop")
	r2 := buildSSELines(nil, "정말 완료했습니다.", "stop")
	r3 := buildSSELines(nil, "이제 끝났습니다.", "stop")

	var bodies []string
	var count atomic.Int64
	rounds := [][]string{r0, r1, r2, r3}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(count.Add(1)) - 1
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if idx >= len(rounds) {
			http.Error(w, "no more rounds", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for _, l := range rounds[idx] {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL,
		Model:           "qwen3-test",
		SystemPrompt:    "test",
		UserPrompt:      "multi-step work",
		MaxIterations:   10,
		TodoGateEnabled: true,
		// No opts.Tools → internal registry OpenAIToolDefs after EnableTodo.
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if !result.Incomplete {
		t.Fatalf("expected Incomplete after bound, got complete: %q", result.Result)
	}
	if !strings.Contains(result.IncompleteReason, "incomplete todos") {
		t.Errorf("reason = %q", result.IncompleteReason)
	}
	// At least one request after first content-only should carry the system-reminder.
	foundNudge := false
	for i := 1; i < len(bodies); i++ {
		users := lastUserContents(t, bodies[i])
		for _, c := range users {
			if strings.Contains(c, "<system-reminder>") && strings.Contains(c, "미완료 todo") {
				foundNudge = true
			}
			if c == todoGateNudgeLead {
				t.Error("nudge body must be system-reminder-wrapped, not bare")
			}
		}
	}
	if !foundNudge {
		t.Errorf("expected system-reminder todo nudge in later requests; bodies=%d", len(bodies))
	}
	// Cap: maxTodoGateNudges=2 → at most 2 nudge injections before incomplete.
	// Requests: todo_write round + 3 content rounds = 4 if model is asked twice then fails.
	if len(bodies) < 3 {
		t.Fatalf("expected multiple rounds, got %d", len(bodies))
	}
}

func TestTodoGateAllowsCompletionWhenTodosDone(t *testing.T) {
	todoArgs := `{"merge":false,"todos":[{"id":"1","content":"Done item","status":"completed"}]}`
	r0 := buildSSELines([]toolCallSpec{
		{id: "c1", name: "todo_write", args: todoArgs},
	}, "", "tool_calls")
	r1 := buildSSELines(nil, "모든 항목을 완료했습니다.", "stop")

	var count atomic.Int64
	rounds := [][]string{r0, r1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(count.Add(1)) - 1
		if idx >= len(rounds) {
			http.Error(w, "extra", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, l := range rounds[idx] {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL,
		Model:           "qwen3-test",
		SystemPrompt:    "test",
		UserPrompt:      "finish work",
		MaxIterations:   5,
		TodoGateEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Incomplete {
		t.Fatalf("should complete when todos done: %s", result.IncompleteReason)
	}
	if !strings.Contains(result.Result, "완료") {
		t.Errorf("result = %q", result.Result)
	}
}

func TestTodoGateOffNoEffectEvenWithIncompleteState(t *testing.T) {
	// Gate off: content-only completion succeeds; todo_write not even available.
	// (State can't be set without the tool; just ensure normal complete works.)
	r1 := buildSSELines(nil, "작업을 완료했습니다.", "stop")
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = count.Add(1)
		raw, _ := io.ReadAll(r.Body)
		// Ensure todo_write is not offered.
		if strings.Contains(string(raw), `"name":"todo_write"`) || strings.Contains(string(raw), `"name": "todo_write"`) {
			t.Error("todo_write must not be in tools when gate off")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, l := range r1 {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL,
		Model:           "qwen3-test",
		SystemPrompt:    "test",
		UserPrompt:      "simple task",
		MaxIterations:   3,
		TodoGateEnabled: false, // default
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Incomplete {
		t.Fatalf("off gate must not block: %s", result.IncompleteReason)
	}
	if count.Load() != 1 {
		t.Errorf("expected single round, got %d", count.Load())
	}
}

func TestTodoGateEmptyStateNoFire(t *testing.T) {
	// Gate on but model never called todo_write → complete normally.
	r1 := buildSSELines(nil, "한 줄 답변으로 충분합니다.", "stop")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, l := range r1 {
			_, _ = fmt.Fprintln(w, l)
			_, _ = fmt.Fprintln(w)
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	result, err := Run(context.Background(), ExecuteOptions{
		BaseURL:         srv.URL,
		Model:           "qwen3-test",
		SystemPrompt:    "test",
		UserPrompt:      "quick question",
		MaxIterations:   3,
		TodoGateEnabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Incomplete {
		t.Fatalf("empty todo state must not fire gate: %s", result.IncompleteReason)
	}
}

func TestMaxTodoGateNudgesConstant(t *testing.T) {
	if maxTodoGateNudges != 2 {
		t.Fatalf("maxTodoGateNudges = %d, want 2 (grok DEFAULT_TODO_GATE_MAX_FIRES)", maxTodoGateNudges)
	}
}
