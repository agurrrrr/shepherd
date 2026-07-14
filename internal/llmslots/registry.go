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

import "sync"

// Registry manages per-endpoint counting semaphores. The semaphores are
// lazily created and shared by all callers that reference the same
// endpoint ID. A maxConcurrent of 0 means unlimited (no semaphore).
type Registry struct {
	mu   sync.Mutex
	sems map[string]*Semaphore
}

// Semaphore is a counting semaphore implemented with a buffered channel.
// A nil Semaphore is a no-op (unlimited concurrency).
type Semaphore struct {
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

// Acquire blocks until a slot is available. nil semaphore → no-op.
func (s *Semaphore) Acquire() {
	if s == nil {
		return
	}
	s.ch <- struct{}{}
}

// TryAcquire attempts to acquire a slot without blocking. Returns true
// if successful. nil semaphore → always true.
func (s *Semaphore) TryAcquire() bool {
	if s == nil {
		return true
	}
	select {
	case s.ch <- struct{}{}:
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
	<-s.ch
}

// Capacity returns the maximum number of concurrent acquisitions.
// nil semaphore → 0 (unlimited).
func (s *Semaphore) Capacity() int {
	if s == nil {
		return 0
	}
	return cap(s.ch)
}

// Available returns the number of currently available slots.
// nil semaphore → 0 (meaningless, but avoids nil deref).
func (s *Semaphore) Available() int {
	if s == nil {
		return 0
	}
	return cap(s.ch) - len(s.ch)
}
