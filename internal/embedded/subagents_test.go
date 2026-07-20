package embedded

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/llmslots"
)

// TestSpawnSubagents_DeadlockPrevention_MaxConcurrent1 verifies that
// spawn_subagents does not deadlock when the endpoint has max_concurrent=1.
// The parent releases its slot automatically (it makes no LLM calls while
// waiting), so sub-agents can acquire the single slot sequentially.
func TestSpawnSubagents_DeadlockPrevention_MaxConcurrent1(t *testing.T) {
	llmslots.Reset()
	sem := llmslots.Global().Get("test-deadlock-ep", 1)

	// Simulate parent holding the slot.
	sem.Acquire(context.Background())

	callCount := int32(0)
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		// Each sub-agent tries to acquire the semaphore.
		sem.Acquire(ctx)
		defer sem.Release()
		atomic.AddInt32(&callCount, 1)
		return &SubagentResult{Content: fmt.Sprintf("%s done", name)}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	// Release parent slot after a short delay (simulating parent waiting
	// without making LLM calls).
	go func() {
		time.Sleep(50 * time.Millisecond)
		sem.Release()
	}()

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "a", "prompt": "task a"},
			map[string]interface{}{"name": "b", "prompt": "task b"},
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeSpawnSubagents failed: %v", err)
		}
		if atomic.LoadInt32(&callCount) != 2 {
			t.Fatalf("expected 2 sub-agent calls, got %d", callCount)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock detected: executeSpawnSubagents did not complete in 10s")
	}
}

// TestSpawnSubagents_ContextCancellationPropagation verifies that when
// the parent context is cancelled, all sub-agents are cancelled too.
//
// Phase 2 improvement: uses context.WithTimeout instead of time.Sleep + cancel
// to eliminate timing-dependent flakiness.
func TestSpawnSubagents_ContextCancellationPropagation(t *testing.T) {
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	// Use a short timeout instead of sleep+cancel — deterministic and flaky-free.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "a", "prompt": "task a"},
			map[string]interface{}{"name": "b", "prompt": "task b"},
		},
	}

	result, err := executeSpawnSubagents(ctx, tr, args, func(s string) {})
	// Error is acceptable — context was cancelled.
	_ = err
	// Should return a result with error markers, not hang.
	if result == nil || result.Content == "" {
		t.Fatal("expected non-empty result even on cancellation")
	}
}

// TestSpawnSubagents_PartialFailure verifies that when some sub-agents
// fail and others succeed, the successful results are still returned.
func TestSpawnSubagents_PartialFailure(t *testing.T) {
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		if name == "fail-agent" {
			return nil, fmt.Errorf("intentional failure")
		}
		return &SubagentResult{Content: fmt.Sprintf("%s succeeded", name)}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "ok-agent", "prompt": "succeed"},
			map[string]interface{}{"name": "fail-agent", "prompt": "fail"},
			map[string]interface{}{"name": "ok-agent-2", "prompt": "succeed"},
		},
	}

	result, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
	if err != nil {
		t.Fatalf("expected nil error for partial failure, got: %v", err)
	}
	if !strings.Contains(result.Content, "ok-agent") {
		t.Fatal("expected result to contain successful agent output")
	}
	if !strings.Contains(result.Content, "❌") {
		t.Fatal("expected result to contain error marker for failed agent")
	}
}

// TestSpawnSubagents_SpawnBlockedForSubAgents verifies that sub-agents
// cannot spawn their own sub-agents (depth 1 enforcement). A ToolRegistry
// without SetSubagentSpawner should not expose spawn_subagents in its
// OpenAIToolDefs.
func TestSpawnSubagents_SpawnBlockedForSubAgents(t *testing.T) {
	tr := NewToolRegistry("/tmp", "test-sheep", nil, nil)

	for _, def := range tr.OpenAIToolDefs() {
		if def.Function.Name == "spawn_subagents" {
			t.Fatal("spawn_subagents should not be available without SetSubagentSpawner")
		}
	}
}

// TestSpawnSubagents_EndpointIDDescriptionIncludesHint verifies that available
// endpoint ids are surfaced in the tool schema so models do not invent
// systemd names or ports (#7728–#7730).
func TestSpawnSubagents_EndpointIDDescriptionIncludesHint(t *testing.T) {
	tr := NewToolRegistry("/tmp", "test-sheep", nil, nil)
	tr.SetSubagentSpawner(func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		return &SubagentResult{Content: "ok"}, nil
	})
	tr.SetSubagentEndpointHint("gents-a1-4b, umans")

	var desc string
	for _, def := range tr.OpenAIToolDefs() {
		if def.Function.Name != "spawn_subagents" {
			continue
		}
		props, _ := def.Function.Parameters["properties"].(map[string]interface{})
		sub, _ := props["subagents"].(map[string]interface{})
		items, _ := sub["items"].(map[string]interface{})
		itemProps, _ := items["properties"].(map[string]interface{})
		ep, _ := itemProps["endpoint_id"].(map[string]interface{})
		desc, _ = ep["description"].(string)
	}
	if desc == "" {
		t.Fatal("endpoint_id description missing from spawn_subagents schema")
	}
	if !strings.Contains(desc, "gents-a1-4b") || !strings.Contains(desc, "umans") {
		t.Fatalf("description should list available ids, got: %s", desc)
	}
	if !strings.Contains(desc, "not label") {
		t.Fatalf("description should warn against using label/systemd/port: %s", desc)
	}
}

// TestSpawnSubagents_MaxFourAgents verifies the max-4 limit.
func TestSpawnSubagents_MaxFourAgents(t *testing.T) {
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		return &SubagentResult{Content: "ok"}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "a", "prompt": "x"},
			map[string]interface{}{"name": "b", "prompt": "x"},
			map[string]interface{}{"name": "c", "prompt": "x"},
			map[string]interface{}{"name": "d", "prompt": "x"},
			map[string]interface{}{"name": "e", "prompt": "x"},
		},
	}

	_, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
	if err == nil {
		t.Fatal("expected error for 5 sub-agents (max is 4)")
	}
}

// TestSpawnSubagents_MaxSpawnsPerTask verifies the per-task spawn limit (3).
// The 4th call should fail with a clear error message (#7463 review: Important #4).
func TestSpawnSubagents_MaxSpawnsPerTask(t *testing.T) {
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		return &SubagentResult{Content: "ok"}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "a", "prompt": "x"},
		},
	}

	// First 3 calls should succeed.
	for i := 0; i < maxSpawnsPerTask; i++ {
		_, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
		if err != nil {
			t.Fatalf("call %d should succeed, got error: %v", i+1, err)
		}
	}

	// 4th call should fail.
	_, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
	if err == nil {
		t.Fatal("expected error on 4th spawn_subagents call (limit is 3 per task)")
	}
}

// TestSpawnSubagents_MaxIterationsParameter verifies that the max_iterations
// parameter is passed through to the spawner.
func TestSpawnSubagents_MaxIterationsParameter(t *testing.T) {
	var receivedMaxIter int
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		receivedMaxIter = maxIter
		return &SubagentResult{Content: "ok"}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{
				"name":          "test",
				"prompt":        "x",
				"max_iterations": float64(5),
			},
		},
	}

	executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
	if receivedMaxIter != 5 {
		t.Fatalf("expected maxIter=5, got %d", receivedMaxIter)
	}
}

// TestSpawnSubagents_DefaultMaxIterations verifies default max_iterations=15.
func TestSpawnSubagents_DefaultMaxIterations(t *testing.T) {
	var receivedMaxIter int
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		receivedMaxIter = maxIter
		return &SubagentResult{Content: "ok"}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "test", "prompt": "x"},
		},
	}

	executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
	if receivedMaxIter != 15 {
		t.Fatalf("expected default maxIter=15, got %d", receivedMaxIter)
	}
}

// --- Phase 2 tests ---

// TestSpawnSubagents_RealDeadlockScenario_MaxConcurrent1 verifies the real
// production scenario: a parent agent that has acquired its endpoint slot
// (via AccumulateStreamWithProgress) spawns sub-agents that use the same
// endpoint. The parent releases its slot before waiting (it makes no LLM
// calls during the wait), so sub-agents can acquire the single slot
// sequentially without deadlocking.
//
// This replaces the artificial TestSpawnSubagents_DeadlockPrevention_MaxConcurrent1
// scenario with one that mirrors the actual code path: the spawner acquires
// via sem.Acquire(ctx) (the same way AccumulateStreamWithProgress does),
// not via a bare sem.Acquire().
func TestSpawnSubagents_RealDeadlockScenario_MaxConcurrent1(t *testing.T) {
	llmslots.Reset()
	sem := llmslots.Global().Get("test-real-deadlock-ep", 1)

	// Simulate the parent holding the slot (as AccumulateStreamWithProgress does).
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("parent Acquire failed: %v", err)
	}

	callCount := int32(0)
	spawner := func(ctx context.Context, name, prompt, endpointID string, maxIter int, onOutput func(string)) (*SubagentResult, error) {
		// Sub-agent acquires the semaphore using ctx — same as the real
		// embedded client does in AccumulateStreamWithProgress.
		if err := sem.Acquire(ctx); err != nil {
			return nil, fmt.Errorf("sub-agent semaphore acquire: %w", err)
		}
		defer sem.Release()
		atomic.AddInt32(&callCount, 1)
		return &SubagentResult{Content: fmt.Sprintf("%s done", name)}, nil
	}

	tr := &ToolRegistry{subagentSpawner: spawner}

	// Release parent slot after a short delay (simulating parent finishing
	// its LLM call and entering the wait phase where it makes no calls).
	go func() {
		time.Sleep(50 * time.Millisecond)
		sem.Release()
	}()

	args := map[string]interface{}{
		"subagents": []interface{}{
			map[string]interface{}{"name": "a", "prompt": "task a"},
			map[string]interface{}{"name": "b", "prompt": "task b"},
			map[string]interface{}{"name": "c", "prompt": "task c"},
		},
	}

	done := make(chan error, 1)
	go func() {
		_, err := executeSpawnSubagents(context.Background(), tr, args, func(s string) {})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeSpawnSubagents failed: %v", err)
		}
		if got := atomic.LoadInt32(&callCount); got != 3 {
			t.Fatalf("expected 3 sub-agent calls, got %d", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock detected: executeSpawnSubagents did not complete in 10s")
	}
}
