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

// Hub fans out events to per-user subscriber channels (drop-on-slow).
type Hub struct {
	mu      sync.RWMutex
	subs    map[string]map[chan Event]struct{}
	bufSize int
}

// NewHub returns an empty broadcast hub.
func NewHub() *Hub {
	return &Hub{
		subs:    make(map[string]map[chan Event]struct{}),
		bufSize: 16,
	}
}

// Subscribe registers a buffered channel for userID. Caller must Unsubscribe.
func (h *Hub) Subscribe(userID string) chan Event {
	ch := make(chan Event, h.bufSize)
	h.mu.Lock()
	if h.subs[userID] == nil {
		h.subs[userID] = make(map[chan Event]struct{})
	}
	h.subs[userID][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes ch for userID and closes it.
func (h *Hub) Unsubscribe(userID string, ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	userSubs, ok := h.subs[userID]
	if !ok {
		return
	}
	if _, ok := userSubs[ch]; ok {
		delete(userSubs, ch)
		close(ch)
	}
	if len(userSubs) == 0 {
		delete(h.subs, userID)
	}
}

// Broadcast sends ev to all subscribers for userID; slow consumers drop the event.
func (h *Hub) Broadcast(userID string, ev Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[userID] {
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
	n := 0
	for _, userSubs := range h.subs {
		n += len(userSubs)
	}
	return n
}
