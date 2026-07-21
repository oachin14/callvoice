package agent_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/services/edge/internal/agent"
)

func TestPresenceStartSetsAvailable(t *testing.T) {
	p, cleanup := newPresence(t)
	defer cleanup()

	uid := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	ctx := context.Background()

	if err := p.Start(ctx, uid); err != nil {
		t.Fatalf("Start: %v", err)
	}

	got, err := p.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != agent.StateAvailable {
		t.Fatalf("state = %q, want %q", got, agent.StateAvailable)
	}
}

func TestPresenceSetStateTransitions(t *testing.T) {
	p, cleanup := newPresence(t)
	defer cleanup()

	uid := uuid.New()
	ctx := context.Background()

	if err := p.Start(ctx, uid); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.SetState(ctx, uid, agent.StatePaused); err != nil {
		t.Fatalf("SetState paused: %v", err)
	}
	got, err := p.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != agent.StatePaused {
		t.Fatalf("state = %q, want %q", got, agent.StatePaused)
	}

	if err := p.SetState(ctx, uid, agent.StateAvailable); err != nil {
		t.Fatalf("SetState available: %v", err)
	}
	got, err = p.Get(ctx, uid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != agent.StateAvailable {
		t.Fatalf("state = %q, want %q", got, agent.StateAvailable)
	}
}

func TestPresenceSetStateRejectsInvalid(t *testing.T) {
	p, cleanup := newPresence(t)
	defer cleanup()

	uid := uuid.New()
	ctx := context.Background()
	_ = p.Start(ctx, uid)

	if err := p.SetState(ctx, uid, agent.State("busy")); err == nil {
		t.Fatal("expected error for invalid state")
	}
}

func TestPresenceStopClearsKey(t *testing.T) {
	p, cleanup := newPresence(t)
	defer cleanup()

	uid := uuid.New()
	ctx := context.Background()
	_ = p.Start(ctx, uid)

	if err := p.Stop(ctx, uid); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	_, err := p.Get(ctx, uid)
	if err != agent.ErrNotFound {
		t.Fatalf("Get after stop err = %v, want ErrNotFound", err)
	}
}

func TestPresenceSetStateRequiresActiveSession(t *testing.T) {
	p, cleanup := newPresence(t)
	defer cleanup()

	uid := uuid.New()
	err := p.SetState(context.Background(), uid, agent.StatePaused)
	if err != agent.ErrNotFound {
		t.Fatalf("SetState without start err = %v, want ErrNotFound", err)
	}
}

func newPresence(t *testing.T) (*agent.Presence, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return agent.NewPresence(rdb), func() { _ = rdb.Close() }
}
