package inbound_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/inbound"
)

type fakeESL struct {
	mu   sync.Mutex
	cmds []string
	ok   bool
}

func (f *fakeESL) API(cmd string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	if f.ok {
		return "+OK", nil
	}
	return "-ERR fail", nil
}

func (f *fakeESL) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) == 0 {
		return ""
	}
	return f.cmds[len(f.cmds)-1]
}

func TestRouteDIDToAvailableAgent(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	agentID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	paused := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	ctx := context.Background()
	if err := rdb.Set(ctx, agent.Key(paused), string(agent.StatePaused), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Set(ctx, agent.Key(agentID), string(agent.StateAvailable), 0).Err(); err != nil {
		t.Fatal(err)
	}

	r := &inbound.Router{
		RDB: rdb,
		DIDs: inbound.MapDIDLookup{
			"+33123456789": {
				Number:      "+33123456789",
				Destination: inbound.DefaultDestination,
			},
		},
	}

	dec, err := r.Route(ctx, "33123456789")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if dec.Action != inbound.ActionBridge {
		t.Fatalf("action = %q, want bridge", dec.Action)
	}
	if dec.AgentID != agentID {
		t.Fatalf("agent = %s, want %s", dec.AgentID, agentID)
	}
	if dec.AgentUser != inbound.AgentSIPUser(agentID) {
		t.Fatalf("agent user = %q", dec.AgentUser)
	}
	// Claim must leave agent on_call so a second inbound cannot pick them.
	st, err := rdb.Get(ctx, agent.Key(agentID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if agent.State(st) != agent.StateOnCall {
		t.Fatalf("after claim state = %q, want on_call", st)
	}
}

func TestRouteEmptyPoolBusy(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	paused := uuid.New()
	_ = rdb.Set(ctx, agent.Key(paused), string(agent.StatePaused), 0).Err()

	r := &inbound.Router{
		RDB: rdb,
		DIDs: inbound.MapDIDLookup{
			"+33987654321": {
				Number:      "+33987654321",
				Destination: "agent_pool:default",
			},
		},
	}

	dec, err := r.Route(ctx, "+33987654321")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if dec.Action != inbound.ActionBusy {
		t.Fatalf("action = %q, want busy", dec.Action)
	}
	if dec.AgentID != uuid.Nil {
		t.Fatalf("unexpected agent %s", dec.AgentID)
	}
}

func TestRouteUnknownDIDBusy(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	r := &inbound.Router{
		RDB:  rdb,
		DIDs: inbound.MapDIDLookup{},
	}
	dec, err := r.Route(context.Background(), "+33000000000")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if dec.Action != inbound.ActionBusy {
		t.Fatalf("action = %q, want busy", dec.Action)
	}
}

func TestHandleEventBridgeAndBusy(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	agentID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	ctx := context.Background()
	_ = rdb.Set(ctx, agent.Key(agentID), string(agent.StateAvailable), 0).Err()

	esl := &fakeESL{ok: true}
	r := &inbound.Router{
		RDB: rdb,
		ESL: esl,
		DIDs: inbound.MapDIDLookup{
			"+33111222333": {Number: "+33111222333", Destination: inbound.DefaultDestination},
		},
	}

	dec, err := r.HandleEvent(ctx, inbound.Event{ChannelUUID: "chan-1", DID: "+33111222333"})
	if err != nil {
		t.Fatalf("HandleEvent bridge: %v", err)
	}
	if dec.Action != inbound.ActionBridge {
		t.Fatalf("action = %q", dec.Action)
	}
	if !strings.Contains(esl.last(), "uuid_transfer chan-1 bridge:user/agent-"+agentID.String()) {
		t.Fatalf("esl cmd = %q", esl.last())
	}

	_ = rdb.Del(ctx, agent.Key(agentID)).Err()
	dec, err = r.HandleEvent(ctx, inbound.Event{ChannelUUID: "chan-2", DID: "+33111222333"})
	if err != nil {
		t.Fatalf("HandleEvent busy: %v", err)
	}
	if dec.Action != inbound.ActionBusy {
		t.Fatalf("action = %q, want busy", dec.Action)
	}
	if !strings.Contains(esl.last(), "uuid_kill chan-2 USER_BUSY") {
		t.Fatalf("esl cmd = %q", esl.last())
	}
}

func TestParseEventCustomInbound(t *testing.T) {
	headers := map[string]string{
		"Event-Name":                "CUSTOM",
		"Event-Subclass":            "callvoice::inbound",
		"Unique-ID":                 "uuid-abc",
		"Variable_callvoice_did":    "+33123456789",
		"Caller-Destination-Number": "ignored",
	}
	ev, ok := inbound.ParseEvent(func(k string) string { return headers[k] })
	if !ok {
		t.Fatal("expected parse ok")
	}
	if ev.ChannelUUID != "uuid-abc" || ev.DID != "+33123456789" {
		t.Fatalf("got %+v", ev)
	}
}

func TestParseEventIgnoresOutboundAgent(t *testing.T) {
	headers := map[string]string{
		"Event-Name":                "CHANNEL_CREATE",
		"Call-Direction":            "outbound",
		"Unique-ID":                 "uuid-x",
		"Caller-Destination-Number": "+33123456789",
	}
	if _, ok := inbound.ParseEvent(func(k string) string { return headers[k] }); ok {
		t.Fatal("expected skip outbound")
	}
}

func TestRouteConcurrentClaimSingleWinner(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	agentID := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")
	ctx := context.Background()
	if err := rdb.Set(ctx, agent.Key(agentID), string(agent.StateAvailable), 0).Err(); err != nil {
		t.Fatal(err)
	}

	r := &inbound.Router{
		RDB: rdb,
		DIDs: inbound.MapDIDLookup{
			"+33155556666": {Number: "+33155556666", Destination: inbound.DefaultDestination},
		},
	}

	const n = 20
	var bridges atomic.Int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			dec, err := r.Route(ctx, "+33155556666")
			if err != nil {
				t.Errorf("Route: %v", err)
				return
			}
			if dec.Action == inbound.ActionBridge {
				bridges.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := bridges.Load(); got != 1 {
		t.Fatalf("bridge winners = %d, want 1", got)
	}
	st, _ := rdb.Get(ctx, agent.Key(agentID)).Result()
	if agent.State(st) != agent.StateOnCall {
		t.Fatalf("state = %q, want on_call", st)
	}
}

func TestNormalizeDID(t *testing.T) {
	cases := map[string]string{
		"+33123456789":           "+33123456789",
		"33123456789":            "+33123456789",
		"sip:+33123456789@trunk": "+33123456789",
	}
	for in, want := range cases {
		if got := inbound.NormalizeDID(in); got != want {
			t.Fatalf("NormalizeDID(%q)=%q want %q", in, got, want)
		}
	}
}
