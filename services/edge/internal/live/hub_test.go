package live_test

import (
	"testing"
	"time"

	"github.com/callvoice/callvoice/services/edge/internal/live"
)

func TestHubBroadcastToTwoSubscribersSameUser(t *testing.T) {
	h := live.NewHub()
	a := h.Subscribe("u1")
	b := h.Subscribe("u1")
	defer h.Unsubscribe("u1", a)
	defer h.Unsubscribe("u1", b)

	if h.Len() != 2 {
		t.Fatalf("Len = %d, want 2", h.Len())
	}

	ev := live.Event{
		Type: live.TypeAgentState,
		Payload: map[string]any{
			"user_id":  "u1",
			"agent_id": "u1",
			"state":    "available",
		},
	}
	h.Broadcast("u1", ev)

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
			if payload["user_id"] != "u1" {
				t.Fatalf("sub %d user_id = %v", i, payload["user_id"])
			}
			if payload["state"] != "available" {
				t.Fatalf("sub %d state = %v", i, payload["state"])
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timed out waiting for broadcast", i)
		}
	}
}

func TestHubBroadcastDoesNotLeakAcrossUsers(t *testing.T) {
	h := live.NewHub()
	alice := h.Subscribe("alice")
	bob := h.Subscribe("bob")
	defer h.Unsubscribe("alice", alice)
	defer h.Unsubscribe("bob", bob)

	h.Broadcast("alice", live.Event{
		Type: live.TypeCallState,
		Payload: map[string]any{
			"user_id":   "alice",
			"agent_id":  "alice",
			"call_uuid": "c1",
			"state":     "active",
		},
	})

	select {
	case <-bob:
		t.Fatal("bob received alice event")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case got := <-alice:
		payload, ok := got.Payload.(map[string]any)
		if !ok || payload["user_id"] != "alice" {
			t.Fatalf("alice payload = %#v", got.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("alice timed out waiting for own event")
	}
}
