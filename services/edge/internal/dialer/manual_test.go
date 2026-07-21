package dialer_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/cpsgate"
	"github.com/callvoice/callvoice/services/edge/internal/dialer"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
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
		return "+OK " + uuid.New().String(), nil
	}
	return "-ERR originate failed", nil
}

type staticCarriers struct {
	list []models.Carrier
}

func (s staticCarriers) ListOrdered(ctx context.Context) ([]models.Carrier, error) {
	return s.list, nil
}

func TestBuildOriginateRejectsNonE164(t *testing.T) {
	_, err := dialer.BuildOriginate("agent-1", "gw", "33123456789", "", uuid.New().String())
	if err != dialer.ErrInvalidE164 {
		t.Fatalf("want ErrInvalidE164, got %v", err)
	}
}

func TestBuildOriginateEscapesAndFormats(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	cmd, err := dialer.BuildOriginate("agent-"+id.String(), id.String(), "+33123456789", "+33987654321", "call-uuid-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "origination_caller_id_number=+33987654321") {
		t.Fatalf("missing caller id: %s", cmd)
	}
	if !strings.Contains(cmd, "user/agent-"+id.String()) {
		t.Fatalf("missing agent leg: %s", cmd)
	}
	if !strings.Contains(cmd, "sofia/gateway/"+id.String()+"/+33123456789") {
		t.Fatalf("missing bridge: %s", cmd)
	}
}

func TestOriginateFailoverSkipsCarrierAtCPSCap(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	gate := cpsgate.New(rdb)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

	a := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	b := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	carriers := []models.Carrier{
		{ID: a, Name: "cap", MaxCPS: 1, MaxChannels: 10, Enabled: true, Priority: 1},
		{ID: b, Name: "next", MaxCPS: 5, MaxChannels: 10, Enabled: true, Priority: 2},
	}

	// Exhaust carrier A's CPS window.
	ok, err := gate.AllowCarrier(context.Background(), a.String(), 1, now)
	if err != nil || !ok {
		t.Fatalf("seed CPS allow: ok=%v err=%v", ok, err)
	}

	esl := &fakeESL{ok: true}
	m := &dialer.Manual{
		ESL:      esl,
		Gate:     gate,
		RDB:      rdb,
		Carriers: staticCarriers{list: carriers},
		Now:      func() time.Time { return now },
	}

	agentID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	res, err := m.Originate(context.Background(), dialer.OutboundRequest{
		AgentID: agentID,
		To:      "+33111222333",
	})
	if err != nil {
		t.Fatalf("Originate: %v", err)
	}
	if res.CarrierID != b {
		t.Fatalf("want failover to carrier B, got %s", res.CarrierID)
	}

	esl.mu.Lock()
	defer esl.mu.Unlock()
	if len(esl.cmds) != 1 {
		t.Fatalf("expected one originate, got %v", esl.cmds)
	}
	wantGW := fs.GatewayName(b)
	if !strings.Contains(esl.cmds[0], "sofia/gateway/"+wantGW+"/") {
		t.Fatalf("originate should use carrier B gateway, got %q", esl.cmds[0])
	}
	if strings.Contains(esl.cmds[0], fs.GatewayName(a)) {
		t.Fatalf("should skip capped carrier A: %q", esl.cmds[0])
	}
}

func TestOriginateAllDeniedReturnsCarrierCapacity(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	gate := cpsgate.New(rdb)
	now := time.Now().UTC()
	a := uuid.New()
	carriers := []models.Carrier{
		{ID: a, MaxCPS: 1, MaxChannels: 1, Enabled: true, Priority: 1},
	}
	_ = rdb.Set(context.Background(), dialer.ChannelKey(a), "1", 0).Err()

	m := &dialer.Manual{
		ESL:      &fakeESL{ok: true},
		Gate:     gate,
		RDB:      rdb,
		Carriers: staticCarriers{list: carriers},
		Now:      func() time.Time { return now },
	}
	_, err := m.Originate(context.Background(), dialer.OutboundRequest{
		AgentID: uuid.New(),
		To:      "+33111222333",
	})
	if err != dialer.ErrCarrierCapacity {
		t.Fatalf("want ErrCarrierCapacity, got %v", err)
	}
}

func TestHangupPublishesEventAndFreesChannel(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	pubsub := rdb.Subscribe(context.Background(), "call.events")
	t.Cleanup(func() { _ = pubsub.Close() })
	ch := pubsub.Channel()

	gate := cpsgate.New(rdb)
	carrierID := uuid.New()
	carriers := []models.Carrier{
		{ID: carrierID, MaxCPS: 10, MaxChannels: 5, Enabled: true, Priority: 1},
	}
	esl := &fakeESL{ok: true}
	m := &dialer.Manual{
		ESL:      esl,
		Gate:     gate,
		RDB:      rdb,
		Carriers: staticCarriers{list: carriers},
	}

	agentID := uuid.New()
	res, err := m.Originate(context.Background(), dialer.OutboundRequest{
		AgentID: agentID,
		To:      "+33111222333",
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		var ev map[string]any
		if err := json.Unmarshal([]byte(msg.Payload), &ev); err != nil {
			t.Fatal(err)
		}
		if ev["type"] != "answered" {
			t.Fatalf("want answered event, got %v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for answered event")
	}

	n, _ := rdb.Get(context.Background(), dialer.ChannelKey(carrierID)).Int64()
	if n != 1 {
		t.Fatalf("channel count want 1 got %d", n)
	}

	if err := m.Hangup(context.Background(), agentID, ""); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		var ev map[string]any
		_ = json.Unmarshal([]byte(msg.Payload), &ev)
		if ev["type"] != "hangup" {
			t.Fatalf("want hangup event, got %v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for hangup event")
	}

	n, _ = rdb.Get(context.Background(), dialer.ChannelKey(carrierID)).Int64()
	if n != 0 {
		t.Fatalf("channel count after hangup want 0 got %d", n)
	}
	_ = res
}

func TestHangupRejectsForeignUUID(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	gate := cpsgate.New(rdb)
	carrierID := uuid.New()
	carriers := []models.Carrier{
		{ID: carrierID, MaxCPS: 10, MaxChannels: 5, Enabled: true, Priority: 1},
	}
	m := &dialer.Manual{
		ESL:      &fakeESL{ok: true},
		Gate:     gate,
		RDB:      rdb,
		Carriers: staticCarriers{list: carriers},
	}

	owner := uuid.New()
	intruder := uuid.New()
	res, err := m.Originate(context.Background(), dialer.OutboundRequest{
		AgentID: owner,
		To:      "+33111222333",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.Hangup(context.Background(), intruder, res.CallUUID)
	if err != dialer.ErrCallForbidden {
		t.Fatalf("want ErrCallForbidden, got %v", err)
	}

	// Owner mapping still intact.
	stored, err := rdb.Get(context.Background(), "call:agent:"+owner.String()).Result()
	if err != nil || stored != res.CallUUID {
		t.Fatalf("owner mapping corrupted: stored=%q err=%v", stored, err)
	}

	err = m.Hangup(context.Background(), owner, res.CallUUID)
	if err != nil {
		t.Fatalf("owner hangup: %v", err)
	}
}

func TestHangupUnknownUUIDNotFound(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	m := &dialer.Manual{
		ESL:  &fakeESL{ok: true},
		Gate: cpsgate.New(rdb),
		RDB:  rdb,
	}
	err := m.Hangup(context.Background(), uuid.New(), uuid.New().String())
	if err != dialer.ErrCallNotFound {
		t.Fatalf("want ErrCallNotFound, got %v", err)
	}
}
