package browser

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const maxDebugEntries = 500

// ConsoleMessage represents a captured console message.
type ConsoleMessage struct {
	Type      string    `json:"type"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// NetworkRequestEntry represents a captured network request with optional response.
type NetworkRequestEntry struct {
	RequestID  string    `json:"request_id"`
	Method     string    `json:"method"`
	URL        string    `json:"url"`
	Type       string    `json:"type"`
	Status     int       `json:"status"`
	StatusText string    `json:"status_text"`
	MIMEType   string    `json:"mime_type"`
	Timestamp  time.Time `json:"timestamp"`
	Duration   float64   `json:"duration_ms"`
}

// DebugState holds debug capture state for a session.
type DebugState struct {
	mu sync.RWMutex

	consoleMessages []ConsoleMessage
	consoleCancel   context.CancelFunc

	networkRequests map[string]*NetworkRequestEntry
	networkOrder    []string // ordered request IDs for iteration
	networkCancel   context.CancelFunc
}

// NewDebugState creates a new debug state.
func NewDebugState() *DebugState {
	return &DebugState{
		networkRequests: make(map[string]*NetworkRequestEntry),
	}
}

// StartConsoleCapture starts capturing console messages from the page.
func (d *DebugState) StartConsoleCapture(page *rod.Page) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Stop existing capture
	if d.consoleCancel != nil {
		d.consoleCancel()
	}
	d.consoleMessages = nil

	// Enable Runtime domain
	if err := (proto.RuntimeEnable{}).Call(page); err != nil {
		return fmt.Errorf("failed to enable runtime: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.consoleCancel = cancel

	go func() {
		p := page.Context(ctx)
		wait := p.EachEvent(func(e *proto.RuntimeConsoleAPICalled) bool {
			var parts []string
			for _, arg := range e.Args {
				if arg.Description != "" {
					parts = append(parts, arg.Description)
				} else if v := arg.Value.String(); v != "" && v != "undefined" {
					parts = append(parts, v)
				}
			}

			msg := ConsoleMessage{
				Type:      string(e.Type),
				Text:      strings.Join(parts, " "),
				Timestamp: time.Now(),
			}

			d.mu.Lock()
			d.consoleMessages = append(d.consoleMessages, msg)
			if len(d.consoleMessages) > maxDebugEntries {
				d.consoleMessages = d.consoleMessages[len(d.consoleMessages)-maxDebugEntries:]
			}
			d.mu.Unlock()

			return false // keep listening
		})
		wait()
	}()

	return nil
}

// StopConsoleCapture stops capturing console messages.
func (d *DebugState) StopConsoleCapture() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.consoleCancel != nil {
		d.consoleCancel()
		d.consoleCancel = nil
	}
}

// GetConsoleMessages returns captured console messages.
func (d *DebugState) GetConsoleMessages() []ConsoleMessage {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]ConsoleMessage, len(d.consoleMessages))
	copy(result, d.consoleMessages)
	return result
}

// ClearConsoleMessages clears captured console messages.
func (d *DebugState) ClearConsoleMessages() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.consoleMessages = nil
}

// StartNetworkCapture starts capturing network requests from the page.
func (d *DebugState) StartNetworkCapture(page *rod.Page) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Stop existing capture
	if d.networkCancel != nil {
		d.networkCancel()
	}
	d.networkRequests = make(map[string]*NetworkRequestEntry)
	d.networkOrder = nil

	// Enable Network domain
	if err := (proto.NetworkEnable{}).Call(page); err != nil {
		return fmt.Errorf("failed to enable network: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.networkCancel = cancel

	go func() {
		p := page.Context(ctx)
		wait := p.EachEvent(
			func(e *proto.NetworkRequestWillBeSent) bool {
				entry := &NetworkRequestEntry{
					RequestID: string(e.RequestID),
					Method:    e.Request.Method,
					URL:       e.Request.URL,
					Type:      string(e.Type),
					Timestamp: time.Now(),
				}

				d.mu.Lock()
				d.networkRequests[string(e.RequestID)] = entry
				d.networkOrder = append(d.networkOrder, string(e.RequestID))
				// Trim old entries
				if len(d.networkOrder) > maxDebugEntries {
					removeID := d.networkOrder[0]
					d.networkOrder = d.networkOrder[1:]
					delete(d.networkRequests, removeID)
				}
				d.mu.Unlock()

				return false
			},
			func(e *proto.NetworkResponseReceived) bool {
				d.mu.Lock()
				if entry, ok := d.networkRequests[string(e.RequestID)]; ok {
					entry.Status = e.Response.Status
					entry.StatusText = e.Response.StatusText
					entry.MIMEType = e.Response.MIMEType
					entry.Type = string(e.Type)
					entry.Duration = float64(time.Since(entry.Timestamp).Milliseconds())
				}
				d.mu.Unlock()

				return false
			},
		)
		wait()
	}()

	return nil
}

// StopNetworkCapture stops capturing network requests.
func (d *DebugState) StopNetworkCapture() {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.networkCancel != nil {
		d.networkCancel()
		d.networkCancel = nil
	}
}

// GetNetworkRequests returns captured network requests in order.
func (d *DebugState) GetNetworkRequests() []NetworkRequestEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]NetworkRequestEntry, 0, len(d.networkOrder))
	for _, id := range d.networkOrder {
		if entry, ok := d.networkRequests[id]; ok {
			result = append(result, *entry)
		}
	}
	return result
}

// GetNetworkRequest returns a specific network request by ID.
func (d *DebugState) GetNetworkRequest(requestID string) (*NetworkRequestEntry, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entry, ok := d.networkRequests[requestID]
	if !ok {
		return nil, false
	}
	cp := *entry
	return &cp, true
}

// ClearNetworkRequests clears captured network requests.
func (d *DebugState) ClearNetworkRequests() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.networkRequests = make(map[string]*NetworkRequestEntry)
	d.networkOrder = nil
}

// Close stops all captures.
func (d *DebugState) Close() {
	d.StopConsoleCapture()
	d.StopNetworkCapture()
}
