package live

import "sync"

// Event types pushed on GET /ws.
const (
	TypeAgentState = "agent.state"
	TypeCallState  = "call.state"
	TypeHeartbeat  = "heartbeat"
)

// Event is a typed live payload broadcast to WebSocket subscribers.
type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Hub fans out events to in-process subscribers (drop-on-slow).
type Hub struct {
	mu      sync.RWMutex
	subs    map[chan Event]struct{}
	bufSize int
}

// NewHub returns an empty broadcast hub.
func NewHub() *Hub {
	return &Hub{
		subs:    make(map[chan Event]struct{}),
		bufSize: 16,
	}
}

// Subscribe registers a buffered channel for events. Caller must Unsubscribe.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, h.bufSize)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch and closes it.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// Broadcast sends ev to all subscribers; slow consumers drop the event.
func (h *Hub) Broadcast(ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Len returns the number of active subscribers (tests / metrics).
func (h *Hub) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
