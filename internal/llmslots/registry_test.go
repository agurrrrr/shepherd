package llmslots

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

func TestSemaphore_AcquireRelease(t *testing.T) {
	Reset() // test isolation (#7463 review: singleton state leakage)
	r := Global()
	sem := r.Get("test-ep", 2)
	if sem == nil {
		t.Fatal("expected non-nil semaphore for maxConcurrent=2")
	}
	if sem.Capacity() != 2 {
		t.Fatalf("expected capacity 2, got %d", sem.Capacity())
	}

	sem.Acquire(context.Background())
	sem.Acquire(context.Background())
	if sem.Available() != 0 {
		t.Fatalf("expected 0 available, got %d", sem.Available())
	}

	sem.Release()
	if sem.Available() != 1 {
		t.Fatalf("expected 1 available, got %d", sem.Available())
	}
}

func TestSemaphore_UnlimitedWhenZero(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("unlimited-ep", 0)
	if sem != nil {
		t.Fatal("expected nil semaphore for maxConcurrent=0")
	}
}

func TestSemaphore_UnlimitedWhenEmptyID(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("", 5)
	if sem != nil {
		t.Fatal("expected nil semaphore for empty endpoint ID")
	}
}

func TestSemaphore_BlocksWhenFull(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("blocked-ep", 1)

	sem.Acquire(context.Background())

	acquired := make(chan bool, 1)
	go func() {
		sem.Acquire(context.Background())
		acquired <- true
	}()

	select {
	case <-acquired:
		t.Fatal("Acquire should have blocked")
	case <-time.After(100 * time.Millisecond):
		// expected: still blocked after 100ms
	}

	// Use a timeout to verify blocking behavior
	go func() {
		sem.Release()
	}()

	select {
	case <-acquired:
		// expected: acquired after release
	case <-time.After(5 * time.Second):
		t.Fatal("Acquire did not complete after Release")
	}
}

func TestSemaphore_SharedAcrossCallers(t *testing.T) {
	Reset()
	r := Global()
	// Same endpoint ID → same semaphore instance
	sem1 := r.Get("shared-ep", 3)
	sem2 := r.Get("shared-ep", 3)
	if sem1 != sem2 {
		t.Fatal("expected same semaphore instance for same endpoint ID")
	}
}

func TestSemaphore_TryAcquire(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("try-ep", 1)

	if !sem.TryAcquire() {
		t.Fatal("TryAcquire should succeed when slots available")
	}
	if sem.TryAcquire() {
		t.Fatal("TryAcquire should fail when no slots available")
	}

	sem.Release()
	if !sem.TryAcquire() {
		t.Fatal("TryAcquire should succeed after Release")
	}
}

func TestSemaphore_ConcurrentAccess(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("concurrent-ep", 3)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem.Acquire(context.Background())
			defer sem.Release()
			// Simulate work
		}()
	}
	wg.Wait()
	// All goroutines completed; semaphore should be fully available
	if sem.Available() != 3 {
		t.Fatalf("expected 3 available after all done, got %d", sem.Available())
	}
}

func TestSemaphore_LookupExisting(t *testing.T) {
	Reset()
	r := Global()
	// Create a semaphore via Get
	created := r.Get("lookup-ep", 2)
	if created == nil {
		t.Fatal("expected non-nil semaphore from Get")
	}
	// Lookup should return the same instance
	found := r.Lookup("lookup-ep")
	if found != created {
		t.Fatal("Lookup should return the same semaphore instance as Get")
	}
}

func TestSemaphore_LookupNonExistent(t *testing.T) {
	Reset()
	r := Global()
	// Lookup for an endpoint that has no semaphore yet
	found := r.Lookup("nonexistent-ep")
	if found != nil {
		t.Fatal("expected nil for non-existent endpoint")
	}
}

func TestSemaphore_LookupEmptyID(t *testing.T) {
	Reset()
	r := Global()
	found := r.Lookup("")
	if found != nil {
		t.Fatal("expected nil for empty endpoint ID")
	}
}

// --- Phase 2 tests ---

// TestSemaphore_AcquireCtxCancelled verifies that Acquire returns ctx.Err()
// immediately when the context is already cancelled, instead of blocking.
func TestSemaphore_AcquireCtxCancelled(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("ctx-cancel-ep", 1)

	// Fill the only slot
	sem.Acquire(context.Background())

	// Pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sem.Acquire(ctx)
	if err == nil {
		t.Fatal("expected error from Acquire with cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Slot should still be held by the first Acquire
	if sem.Available() != 0 {
		t.Fatalf("expected 0 available after cancelled Acquire, got %d", sem.Available())
	}

	sem.Release()
}

// TestSemaphore_AcquireCtxCancelledWhileBlocking verifies that Acquire
// returns ctx.Err() when the context is cancelled *while* blocked waiting
// for a slot. This is the core goroutine-leak prevention scenario.
func TestSemaphore_AcquireCtxCancelledWhileBlocking(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("ctx-block-ep", 1)

	// Fill the only slot
	sem.Acquire(context.Background())

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- sem.Acquire(ctx)
	}()

	// Give the goroutine time to start blocking
	select {
	case err := <-errCh:
		t.Fatalf("Acquire returned too early: %v", err)
	case <-time.After(50 * time.Millisecond):
		// expected: still blocking
	}

	// Cancel while blocked
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire did not return after context cancellation")
	}

	// Clean up
	sem.Release()
}

// TestSemaphore_AcquireNilSemaphore verifies that a nil semaphore (unlimited)
// always returns nil immediately, even with a cancelled context.
func TestSemaphore_AcquireNilSemaphore(t *testing.T) {
	var sem *Semaphore

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sem.Acquire(ctx)
	if err != nil {
		t.Fatalf("nil semaphore Acquire should always return nil, got %v", err)
	}
}

// TestSemaphore_IsUnlimited verifies IsUnlimited for nil and non-nil semaphores.
func TestSemaphore_IsUnlimited(t *testing.T) {
	Reset()
	r := Global()

	var nilSem *Semaphore
	if !nilSem.IsUnlimited() {
		t.Fatal("nil semaphore should be unlimited")
	}

	sem := r.Get("unlimited-check-ep", 2)
	if sem.IsUnlimited() {
		t.Fatal("non-nil semaphore should not be unlimited")
	}
}

// TestSemaphore_AvailableNilReturnsMaxInt verifies that Available() on a nil
// semaphore returns math.MaxInt (not 0).
func TestSemaphore_AvailableNilReturnsMaxInt(t *testing.T) {
	var sem *Semaphore
	if sem.Available() != math.MaxInt {
		t.Fatalf("expected math.MaxInt for nil semaphore Available(), got %d", sem.Available())
	}
}

// TestRegistry_ResizeGrow verifies that Resize can grow a semaphore's capacity.
func TestRegistry_ResizeGrow(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("resize-grow-ep", 2)
	if sem.Capacity() != 2 {
		t.Fatalf("expected initial capacity 2, got %d", sem.Capacity())
	}

	r.Resize("resize-grow-ep", 5)
	if sem.Capacity() != 5 {
		t.Fatalf("expected capacity 5 after resize, got %d", sem.Capacity())
	}

	// Should be able to acquire up to 5 slots now
	for i := 0; i < 5; i++ {
		if err := sem.Acquire(context.Background()); err != nil {
			t.Fatalf("Acquire %d failed: %v", i, err)
		}
	}
	if sem.Available() != 0 {
		t.Fatalf("expected 0 available after 5 acquires, got %d", sem.Available())
	}
	for i := 0; i < 5; i++ {
		sem.Release()
	}
}

// TestRegistry_ResizeShrink verifies that Resize can shrink a semaphore's
// capacity while preserving in-flight acquisitions.
func TestRegistry_ResizeShrink(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("resize-shrink-ep", 4)

	// Acquire 2 slots (simulating in-flight work)
	sem.Acquire(context.Background())
	sem.Acquire(context.Background())

	// Shrink to 3 — the 2 held slots should be preserved
	r.Resize("resize-shrink-ep", 3)
	if sem.Capacity() != 3 {
		t.Fatalf("expected capacity 3 after shrink, got %d", sem.Capacity())
	}

	// 2 slots are held, so 1 should be available
	if sem.Available() != 1 {
		t.Fatalf("expected 1 available after shrink with 2 held, got %d", sem.Available())
	}

	// Acquire the remaining slot
	if err := sem.Acquire(context.Background()); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if sem.Available() != 0 {
		t.Fatalf("expected 0 available, got %d", sem.Available())
	}

	// Next Acquire should block
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := sem.Acquire(ctx)
	if err == nil {
		t.Fatal("expected Acquire to block/fail when full")
	}

	// Release all 3 held slots
	sem.Release()
	sem.Release()
	sem.Release()
	if sem.Available() != 3 {
		t.Fatalf("expected 3 available after all releases, got %d", sem.Available())
	}
}

// TestRegistry_ResizeRemove verifies that Resize with newCap <= 0 removes the
// semaphore (unlimited).
func TestRegistry_ResizeRemove(t *testing.T) {
	Reset()
	r := Global()
	sem := r.Get("resize-remove-ep", 2)

	r.Resize("resize-remove-ep", 0)

	// Lookup should return nil (removed)
	found := r.Lookup("resize-remove-ep")
	if found != nil {
		t.Fatal("expected nil after Resize to 0")
	}

	// Old pointer is still valid but its capacity is unchanged (2).
	// The registry no longer tracks it — new Get calls create a fresh one.
	_ = sem
}

// TestRegistry_ResizeCreateNew verifies that Resize creates a new semaphore
// if one doesn't exist yet.
func TestRegistry_ResizeCreateNew(t *testing.T) {
	Reset()
	r := Global()

	r.Resize("resize-new-ep", 3)

	sem := r.Lookup("resize-new-ep")
	if sem == nil {
		t.Fatal("expected non-nil semaphore after Resize created it")
	}
	if sem.Capacity() != 3 {
		t.Fatalf("expected capacity 3, got %d", sem.Capacity())
	}
}
