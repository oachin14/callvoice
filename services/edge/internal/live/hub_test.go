package live_test

import (
	"testing"
	"time"

	"github.com/callvoice/callvoice/services/edge/internal/live"
)

func TestHubBroadcastToTwoSubscribers(t *testing.T) {
	h := live.NewHub()
	a := h.Subscribe()
	b := h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}

	ev := live.Event{
		Type:    live.TypeAgentState,
		Payload: map[string]any{"user_id": "u1", "state": "available"},
	}
	h.Broadcast(ev)

	for i, ch := range []chan live.Event{a, b} {
		select {
		case got := <-ch:
			if got.Type != live.TypeAgentState {
				t.Fatalf("sub %d type = %q, want %q", i, got.Type, live.TypeAgentState)
			}
			payload, ok := got.Payload.(map[string]any)
			if !ok {
				t.Fatalf("sub %d payload type %T", i, got.Payload)
			}
			if payload["state"] != "available" {
				t.Fatalf("sub %d state = %v", i, payload["state"])
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timed out waiting for broadcast", i)
		}
	}
}
