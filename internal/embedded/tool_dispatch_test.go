package embedded

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsParallelSafeTool(t *testing.T) {
	safe := []string{"read_file", "grep", "glob"}
	unsafe := []string{
		"write_file", "edit_file", "bash", "spawn_subagents", "todo_write",
		"browser_open", "browser_click", "get_history", "mobile_take_screenshot",
		"unknown_mcp_tool", "",
	}
	for _, name := range safe {
		if !isParallelSafeTool(name) {
			t.Errorf("%q should be parallel-safe", name)
		}
	}
	for _, name := range unsafe {
		if isParallelSafeTool(name) {
			t.Errorf("%q must NOT be parallel-safe", name)
		}
	}
}

func TestBatchAllParallelSafe(t *testing.T) {
	read := ToolCall{ID: "1", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a"}`}}
	grep := ToolCall{ID: "2", Func: ToolCallFunction{Name: "grep", Args: `{"pattern":"x"}`}}
	glob := ToolCall{ID: "3", Func: ToolCallFunction{Name: "glob", Args: `{"pattern":"*"}`}}
	write := ToolCall{ID: "4", Func: ToolCallFunction{Name: "write_file", Args: `{"path":"a","content":"x"}`}}
	bash := ToolCall{ID: "5", Func: ToolCallFunction{Name: "bash", Args: `{"command":"ls"}`}}

	if batchAllParallelSafe([]ToolCall{read}) {
		t.Error("single call should not parallelize (no benefit)")
	}
	if !batchAllParallelSafe([]ToolCall{read, grep, glob}) {
		t.Error("all-read batch should parallelize")
	}
	if batchAllParallelSafe([]ToolCall{read, write}) {
		t.Error("read+write mix must stay sequential")
	}
	if batchAllParallelSafe([]ToolCall{read, bash, grep}) {
		t.Error("any bash in batch forces sequential")
	}
	if batchAllParallelSafe(nil) {
		t.Error("empty batch is not parallel")
	}
}

// TestRunToolCallBatch_ParallelOrderPreserved verifies that concurrent reads
// finish out-of-order internally but results are returned in tool_call order.
func TestRunToolCallBatch_ParallelOrderPreserved(t *testing.T) {
	dir := t.TempDir()
	// Three files; middle one is slow so it would finish last if unordered.
	for i, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(fmt.Sprintf("content-%d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	// All three sleep so concurrent dispatch is unambiguous even on a single P.
	// b.txt sleeps longer so unordered completion would invert result order.
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	orig := tr.nativeTools["read_file"]
	tr.nativeTools["read_file"] = func(ctx context.Context, args map[string]interface{}) (string, error) {
		cur := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
				break
			}
		}
		defer concurrent.Add(-1)

		path, _ := args["path"].(string)
		delay := 40 * time.Millisecond
		if strings.Contains(path, "b.txt") {
			// Slow middle file — if results were appended as-completed, order
			// would be a, c, b instead of a, b, c.
			delay = 100 * time.Millisecond
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		return orig(ctx, args)
	}

	calls := []ToolCall{
		{ID: "call_a", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a.txt"}`}},
		{ID: "call_b", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"b.txt"}`}},
		{ID: "call_c", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"c.txt"}`}},
	}

	// Confirm classifier chose parallel.
	if !batchAllParallelSafe(calls) {
		t.Fatal("expected parallel batch")
	}

	start := time.Now()
	outcomes := runToolCallBatch(context.Background(), tr, calls, 0, ExecuteOptions{}, nil)
	elapsed := time.Since(start)

	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(outcomes))
	}
	wantIDs := []string{"call_a", "call_b", "call_c"}
	wantSnips := []string{"content-0", "content-1", "content-2"}
	for i, o := range outcomes {
		if o.msg.ToolCallID != wantIDs[i] {
			t.Errorf("outcome[%d] ToolCallID = %q, want %q", i, o.msg.ToolCallID, wantIDs[i])
		}
		if !strings.Contains(o.msg.Content, wantSnips[i]) {
			t.Errorf("outcome[%d] content missing %q: %q", i, wantSnips[i], o.msg.Content)
		}
		if strings.HasPrefix(o.msg.Content, "Error:") {
			t.Errorf("outcome[%d] unexpected error: %s", i, o.msg.Content)
		}
	}

	// Parallel: wall time ≈ slowest (~100ms), not sum of three (~180ms+).
	// Allow generous CI slack; sequential would exceed ~180ms easily.
	if elapsed > 350*time.Millisecond {
		t.Errorf("batch took %v; expected parallel overlap (slowest tool ~100ms)", elapsed)
	}
	if maxConcurrent.Load() < 2 {
		t.Errorf("max concurrent = %d, want ≥2 (tools did not overlap)", maxConcurrent.Load())
	}
}

// TestRunToolCallBatch_MixedWriteStaysSequential ensures a write mixed into a
// read batch forces sequential execution (no concurrent dispatch).
func TestRunToolCallBatch_MixedWriteStaysSequential(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var mu sync.Mutex
	var order []string

	wrap := func(name string, fn toolFunc) toolFunc {
		return func(ctx context.Context, args map[string]interface{}) (string, error) {
			cur := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			// Hold briefly so a concurrent sibling would be visible.
			time.Sleep(30 * time.Millisecond)
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			concurrent.Add(-1)
			return fn(ctx, args)
		}
	}
	tr.nativeTools["read_file"] = wrap("read_file", tr.nativeTools["read_file"])
	tr.nativeTools["write_file"] = wrap("write_file", tr.nativeTools["write_file"])

	calls := []ToolCall{
		{ID: "r1", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a.txt"}`}},
		{ID: "w1", Func: ToolCallFunction{Name: "write_file", Args: `{"path":"out.txt","content":"x"}`}},
		{ID: "r2", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a.txt"}`}},
	}
	if batchAllParallelSafe(calls) {
		t.Fatal("mixed batch must not be classified as parallel")
	}

	outcomes := runToolCallBatch(context.Background(), tr, calls, 0, ExecuteOptions{}, nil)
	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(outcomes))
	}
	if outcomes[0].msg.ToolCallID != "r1" || outcomes[1].msg.ToolCallID != "w1" || outcomes[2].msg.ToolCallID != "r2" {
		t.Errorf("order of results wrong: %v %v %v",
			outcomes[0].msg.ToolCallID, outcomes[1].msg.ToolCallID, outcomes[2].msg.ToolCallID)
	}
	if maxConcurrent.Load() != 1 {
		t.Errorf("max concurrent = %d, want 1 (sequential)", maxConcurrent.Load())
	}
	// Write should have succeeded between the two reads.
	if _, err := os.Stat(filepath.Join(dir, "out.txt")); err != nil {
		t.Errorf("write_file did not create out.txt: %v", err)
	}
	if len(order) != 3 || order[0] != "read_file" || order[1] != "write_file" || order[2] != "read_file" {
		t.Errorf("execution order = %v, want [read_file write_file read_file]", order)
	}
}

// TestRunToolCallBatch_ErrorPropagation checks that a failing tool becomes
// an Error: tool result without aborting sibling tools, and order is preserved.
func TestRunToolCallBatch_ErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	// Distinct files so parallel same-path auto-page does not interact.
	if err := os.WriteFile(filepath.Join(dir, "ok1.txt"), []byte("ok-body-1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok2.txt"), []byte("ok-body-2"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	calls := []ToolCall{
		{ID: "ok1", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"ok1.txt"}`}},
		{ID: "bad", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"missing-no-such.txt"}`}},
		{ID: "ok2", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"ok2.txt"}`}},
	}

	outcomes := runToolCallBatch(context.Background(), tr, calls, 0, ExecuteOptions{}, nil)
	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(outcomes))
	}
	if !strings.Contains(outcomes[0].msg.Content, "ok-body-1") {
		t.Errorf("first result missing content: %q", outcomes[0].msg.Content)
	}
	if !strings.HasPrefix(outcomes[1].msg.Content, "Error:") {
		t.Errorf("middle failure not propagated as Error: %q", outcomes[1].msg.Content)
	}
	if outcomes[1].msg.ToolCallID != "bad" {
		t.Errorf("error ToolCallID = %q, want bad", outcomes[1].msg.ToolCallID)
	}
	if !strings.Contains(outcomes[2].msg.Content, "ok-body-2") {
		t.Errorf("sibling after error should still succeed: %q", outcomes[2].msg.Content)
	}
}

// TestRunToolCallBatch_ErrorPropagationSequential covers the mixed-batch
// sequential path with a failing write-ish tool.
func TestRunToolCallBatch_ErrorPropagationSequential(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)

	calls := []ToolCall{
		{ID: "r", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a.txt"}`}},
		// Invalid JSON args after repair refusal isn't easy; use bad path edit.
		{ID: "e", Func: ToolCallFunction{Name: "edit_file", Args: `{"path":"nope.txt","oldText":"x","newText":"y"}`}},
		{ID: "r2", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"a.txt"}`}},
	}
	outcomes := runToolCallBatch(context.Background(), tr, calls, 0, ExecuteOptions{}, nil)
	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d", len(outcomes))
	}
	if strings.HasPrefix(outcomes[0].msg.Content, "Error:") {
		t.Errorf("first read should succeed: %s", outcomes[0].msg.Content)
	}
	if !strings.HasPrefix(outcomes[1].msg.Content, "Error:") {
		t.Errorf("edit should fail: %s", outcomes[1].msg.Content)
	}
	if strings.HasPrefix(outcomes[2].msg.Content, "Error:") {
		t.Errorf("third read should still succeed: %s", outcomes[2].msg.Content)
	}
	if outcomes[0].msg.ToolCallID != "r" || outcomes[1].msg.ToolCallID != "e" || outcomes[2].msg.ToolCallID != "r2" {
		t.Errorf("IDs out of order")
	}
}

func TestEnsureToolCallIDs(t *testing.T) {
	calls := []ToolCall{
		{ID: "", Func: ToolCallFunction{Name: "read_file"}},
		{ID: "keep_me", Func: ToolCallFunction{Name: "grep"}},
		{ID: "", Func: ToolCallFunction{Name: "glob"}},
	}
	ensureToolCallIDs(calls, 3)
	if calls[0].ID != "call_3_0" {
		t.Errorf("got %q", calls[0].ID)
	}
	if calls[1].ID != "keep_me" {
		t.Errorf("existing ID overwritten: %q", calls[1].ID)
	}
	if calls[2].ID != "call_3_2" {
		t.Errorf("got %q", calls[2].ID)
	}
}

// TestRunToolCallBatch_ParallelGrepGlob exercises multi-type pure-read batch.
func TestRunToolCallBatch_ParallelGrepGlob(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "src.go"), []byte("package main\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := NewToolRegistry(dir, "test-sheep", nil, nil)
	calls := []ToolCall{
		{ID: "g", Func: ToolCallFunction{Name: "grep", Args: `{"pattern":"Hello","path":"."}`}},
		{ID: "gl", Func: ToolCallFunction{Name: "glob", Args: `{"pattern":"*.go"}`}},
		{ID: "rf", Func: ToolCallFunction{Name: "read_file", Args: `{"path":"src.go"}`}},
	}
	if !batchAllParallelSafe(calls) {
		t.Fatal("grep+glob+read_file should be parallel-safe")
	}
	outcomes := runToolCallBatch(context.Background(), tr, calls, 1, ExecuteOptions{}, nil)
	if len(outcomes) != 3 {
		t.Fatalf("len=%d", len(outcomes))
	}
	for i, id := range []string{"g", "gl", "rf"} {
		if outcomes[i].msg.ToolCallID != id {
			t.Errorf("[%d] id=%q want %q", i, outcomes[i].msg.ToolCallID, id)
		}
		if strings.HasPrefix(outcomes[i].msg.Content, "Error:") {
			t.Errorf("[%d] error: %s", i, outcomes[i].msg.Content)
		}
	}
}
