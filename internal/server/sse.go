package server

import (
	"sync"
)

// SSEEvent represents a Server-Sent Event.
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

// SSEHub manages SSE client connections and event broadcasting.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[string]chan SSEEvent
}

// NewSSEHub creates a new SSE event hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[string]chan SSEEvent),
	}
}

// Subscribe registers a new SSE client and returns its event channel.
func (h *SSEHub) Subscribe(clientID string) chan SSEEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch := make(chan SSEEvent, 256)
	h.clients[clientID] = ch
	return ch
}

// Unsubscribe removes a client and closes its channel.
func (h *SSEHub) Unsubscribe(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.clients[clientID]; ok {
		close(ch)
		delete(h.clients, clientID)
	}
}

// Broadcast sends an event to all connected clients.
// If a client's channel is full, the event is dropped for that client.
func (h *SSEHub) Broadcast(event SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.clients {
		select {
		case ch <- event:
		default:
			// Client channel is full — skip to avoid blocking
		}
	}
}

// CloseAll closes all SSE client channels and removes them.
// This must be called before server shutdown to unblock streaming connections.
func (h *SSEHub) CloseAll() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for id, ch := range h.clients {
		close(ch)
		delete(h.clients, id)
	}
}

// ClientCount returns the number of connected SSE clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
