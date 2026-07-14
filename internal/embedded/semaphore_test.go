package embedded

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/internal/llmslots"
)

// TestSemaphore_GatingBlocksAndReleases verifies that AccumulateStreamWithProgress
// acquires the endpoint semaphore before the LLM call and releases it after,
// so concurrent calls are bounded by the semaphore capacity.
func TestSemaphore_GatingBlocksAndReleases(t *testing.T) {
	llmslots.Reset()
	sem := llmslots.Global().Get("test-gate-ep", 1)
	if sem == nil {
		t.Fatal("expected non-nil semaphore")
	}

	// Track how many concurrent calls are in flight.
	var inFlight int32
	var maxInFlight int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		// Track high-water mark
		for {
			old := atomic.LoadInt32(&maxInFlight)
			if cur <= old {
				break
			}
			if atomic.CompareAndSwapInt32(&maxInFlight, old, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond) // hold the slot
		atomic.AddInt32(&inFlight, -1)

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	c.SetSemaphore(sem)

	// Launch 3 concurrent calls; only 1 should be in flight at a time.
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := c.AccumulateStreamWithProgress(
				context.Background(),
				&ChatRequest{Model: "test-model"},
				nil, nil,
			)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if max := atomic.LoadInt32(&maxInFlight); max > 1 {
		t.Fatalf("expected max 1 concurrent call, got %d", max)
	}
}

// TestSemaphore_NilNoBlocking verifies that a nil semaphore (max_concurrent=0)
// does not block — all calls run concurrently.
func TestSemaphore_NilNoBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", "test-model")
	// No SetSemaphore call — semaphore stays nil (unlimited).

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := c.AccumulateStreamWithProgress(
				context.Background(),
				&ChatRequest{Model: "test-model"},
				nil, nil,
			)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()
	// If we got here without deadlock, nil semaphore works correctly.
}
