package agent_test

import (
	"context"
	"testing"
	"time"

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

func TestPresenceStartSetsTTL(t *testing.T) {
	p, mr, cleanup := newPresenceWithMini(t)
	defer cleanup()

	uid := uuid.New()
	ctx := context.Background()
	if err := p.Start(ctx, uid); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := mr.TTL(agent.Key(uid)); got != agent.DefaultTTL {
		t.Fatalf("TTL = %v, want %v", got, agent.DefaultTTL)
	}
}

func TestPresenceSetStateRefreshesTTL(t *testing.T) {
	p, mr, cleanup := newPresenceWithMini(t)
	defer cleanup()

	uid := uuid.New()
	ctx := context.Background()
	_ = p.Start(ctx, uid)
	mr.FastForward(agent.DefaultTTL - time.Minute)
	if err := p.SetState(ctx, uid, agent.StatePaused); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if got := mr.TTL(agent.Key(uid)); got < agent.DefaultTTL-time.Minute {
		t.Fatalf("TTL after SetState = %v, expected refresh near %v", got, agent.DefaultTTL)
	}
}

func newPresence(t *testing.T) (*agent.Presence, func()) {
	t.Helper()
	p, _, cleanup := newPresenceWithMini(t)
	return p, cleanup
}

func newPresenceWithMini(t *testing.T) (*agent.Presence, *miniredis.Miniredis, func()) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return agent.NewPresence(rdb, agent.DefaultTTL), mr, func() { _ = rdb.Close() }
}
