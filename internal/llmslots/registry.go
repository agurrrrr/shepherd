// Package llmslots provides per-endpoint concurrency limiting (semaphore)
// for LLM API calls. It is a leaf package — only standard library imports.
//
// The registry is shared across three call paths:
//   - Queue dispatcher (embedded tasks)
//   - MAGI proposers
//   - spawn_subagents sub-agent loops
//
// All three acquire slots through the same semaphore per endpoint ID,
// ensuring the total concurrent LLM calls to an endpoint never exceed
// its configured max_concurrent.
package llmslots

import (
	"context"
	"math"
	"sync"
)

// Registry manages per-endpoint counting semaphores. The semaphores are
// lazily created and shared by all callers that reference the same
// endpoint ID. A maxConcurrent of 0 means unlimited (no semaphore).
type Registry struct {
	mu   sync.Mutex
	sems map[string]*Semaphore
}

// Semaphore is a counting semaphore implemented with a buffered channel.
// A nil Semaphore is a no-op (unlimited concurrency).
//
// The internal channel can be replaced at runtime via Registry.Resize.
// A RWMutex protects the channel pointer: Acquire/TryAcquire/Release take
// a read lock (allowing concurrent callers), while Resize takes a write
// lock (exclusive access during the swap).
type Semaphore struct {
	mu sync.RWMutex
	ch chan struct{}
}

// global is the process-wide singleton registry.
var global = &Registry{
	sems: make(map[string]*Semaphore),
}

// Global returns the process-wide registry instance.
func Global() *Registry {
	return global
}

// Reset clears all semaphores from the global registry. This is intended
// for test isolation — tests that create semaphores with specific endpoint
// IDs can call Reset() in a setup or cleanup to avoid state leakage between
// tests. Production code should not call this.
func Reset() {
	global.mu.Lock()
	defer global.mu.Unlock()
	global.sems = make(map[string]*Semaphore)
}

// Get returns the semaphore for the given endpoint ID. If maxConcurrent is
// 0 or negative, returns nil (unlimited). If the semaphore does not yet
// exist for this endpoint, it is created with the given capacity.
//
// Note: if the same endpoint ID is later called with a different
// maxConcurrent, the original capacity is kept. The caller should ensure
// consistent configuration. In practice, endpoint configs don't change
// at runtime.
func (r *Registry) Get(endpointID string, maxConcurrent int) *Semaphore {
	if maxConcurrent <= 0 || endpointID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	sem, ok := r.sems[endpointID]
	if !ok {
		sem = &Semaphore{ch: make(chan struct{}, maxConcurrent)}
		r.sems[endpointID] = sem
	}
	return sem
}

// Acquire blocks until a slot is available or ctx is cancelled. Returns nil
// on success, ctx.Err() if the context is cancelled while waiting. nil
// semaphore → no-op (always returns nil).
//
// The read lock is held during the channel send, which allows concurrent
// Acquire calls but blocks during a Resize swap.
func (s *Semaphore) Acquire(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	ch := s.ch
	s.mu.RUnlock()
	select {
	case ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire attempts to acquire a slot without blocking. Returns true
// if successful. nil semaphore → always true.
func (s *Semaphore) TryAcquire() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	ch := s.ch
	s.mu.RUnlock()
	select {
	case ch <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release returns a slot. nil semaphore → no-op.
func (s *Semaphore) Release() {
	if s == nil {
		return
	}
	s.mu.RLock()
	ch := s.ch
	s.mu.RUnlock()
	<-ch
}

// Capacity returns the maximum number of concurrent acquisitions.
// nil semaphore → 0 (unlimited).
func (s *Semaphore) Capacity() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cap(s.ch)
}

// Available returns the number of currently available slots.
// nil semaphore → math.MaxInt (unlimited). Callers that need to distinguish
// unlimited from a real semaphore should check IsUnlimited().
func (s *Semaphore) Available() int {
	if s == nil {
		return math.MaxInt
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cap(s.ch) - len(s.ch)
}

// IsUnlimited reports whether this semaphore is nil (no concurrency limit).
func (s *Semaphore) IsUnlimited() bool {
	return s == nil
}

// Lookup returns the existing semaphore for the given endpoint ID, or nil
// if none has been created yet. Unlike Get, it never creates a new
// semaphore — callers that only need to check for an existing limiter
// (e.g. the MAGI proposer path) can use this without knowing the
// configured maxConcurrent value.
func (r *Registry) Lookup(endpointID string) *Semaphore {
	if endpointID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sems[endpointID]
}

// Resize changes the capacity of an existing endpoint's semaphore at runtime.
// If the endpoint has no semaphore yet, one is created with the new capacity.
// If newCap <= 0, the semaphore is removed (unlimited).
//
// Shrinking: in-flight acquisitions are preserved — the new channel is created
// with the reduced capacity, and currently held slots are tracked so that the
// effective limit transitions gracefully. New Acquire calls will block once
// the new capacity is reached.
// Growing: takes effect immediately for new Acquire calls.
//
// This is safe for concurrent use with Acquire/Release.
func (r *Registry) Resize(endpointID string, newCap int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if newCap <= 0 {
		// Remove semaphore → unlimited
		delete(r.sems, endpointID)
		return
	}

	old, ok := r.sems[endpointID]
	if !ok {
		// Create new semaphore with the given capacity
		r.sems[endpointID] = &Semaphore{ch: make(chan struct{}, newCap)}
		return
	}

	// Already has the right capacity — nothing to do
	old.mu.RLock()
	oldCap := cap(old.ch)
	held := len(old.ch) // items in channel = currently held slots
	old.mu.RUnlock()
	if oldCap == newCap {
		return
	}

	// Create a new channel with the new capacity. Transfer currently held
	// slots to the new channel, up to the new capacity.
	if held > newCap {
		held = newCap
	}
	newCh := make(chan struct{}, newCap)
	for i := 0; i < held; i++ {
		newCh <- struct{}{}
	}
	old.mu.Lock()
	old.ch = newCh
	old.mu.Unlock()
}
