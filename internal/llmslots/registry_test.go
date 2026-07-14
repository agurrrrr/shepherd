package llmslots

import (
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

	sem.Acquire()
	sem.Acquire()
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

	sem.Acquire()

	acquired := make(chan bool, 1)
	go func() {
		sem.Acquire()
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
			sem.Acquire()
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
