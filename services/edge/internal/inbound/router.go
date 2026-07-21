package inbound

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/agent"
)

const (
	// DefaultDestination is the jalon-B pool label for inbound DIDs.
	DefaultDestination = "agent_pool:default"

	agentKeyPrefix = "agent:"
)

// Action is the routing outcome for an inbound DID.
type Action string

const (
	ActionBridge Action = "bridge"
	ActionBusy   Action = "busy"
)

// ErrNoAgent means no available agent was found in Redis.
var ErrNoAgent = errors.New("no available agent")

// ErrUnknownDID means the called number is not provisioned.
var ErrUnknownDID = errors.New("unknown did")

// ESL is the FreeSWITCH command surface used to bridge or reject inbound legs.
type ESL interface {
	API(cmd string) (string, error)
}

// DIDLookup resolves a called number to a DID row.
type DIDLookup interface {
	LookupByNumber(ctx context.Context, number string) (*models.DID, error)
}

// Decision is the result of DID → agent routing.
type Decision struct {
	Action      Action    `json:"action"`
	DID         string    `json:"did"`
	Destination string    `json:"destination"`
	AgentID     uuid.UUID `json:"agent_id,omitempty"`
	AgentUser   string    `json:"agent_user,omitempty"`
}

// Event is a normalized inbound notify from FreeSWITCH (CUSTOM or CHANNEL_CREATE).
type Event struct {
	ChannelUUID string
	DID         string
}

// Router maps inbound DIDs to the first available agent.
type Router struct {
	RDB  *redis.Client
	DIDs DIDLookup
	ESL  ESL

	seen sync.Map // channel UUID → struct{} to dedupe CUSTOM + CHANNEL_CREATE
}

// AgentSIPUser returns the FreeSWITCH directory user for an agent.
func AgentSIPUser(agentID uuid.UUID) string {
	return "agent-" + agentID.String()
}

// NormalizeDID trims and ensures a leading + when the number is digits-only E.164-ish.
func NormalizeDID(number string) string {
	n := strings.TrimSpace(number)
	n = strings.TrimPrefix(n, "sip:")
	if i := strings.IndexByte(n, '@'); i >= 0 {
		n = n[:i]
	}
	n = strings.TrimSpace(n)
	if n == "" {
		return n
	}
	if strings.HasPrefix(n, "+") {
		return n
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, n)
	if len(digits) >= 8 && len(digits) <= 15 {
		return "+" + digits
	}
	return n
}

// isAgentPool reports whether destination routes to the shared available-agent pool.
func isAgentPool(dest string) bool {
	d := strings.TrimSpace(strings.ToLower(dest))
	return d == DefaultDestination || d == "queue:default" || strings.HasPrefix(d, "agent_pool:")
}

// FirstAvailableAgent scans Redis for the first agent:* key with state available.
func (r *Router) FirstAvailableAgent(ctx context.Context) (uuid.UUID, error) {
	var cursor uint64
	for {
		keys, next, err := r.RDB.Scan(ctx, cursor, agentKeyPrefix+"*", 50).Result()
		if err != nil {
			return uuid.Nil, err
		}
		for _, key := range keys {
			val, err := r.RDB.Get(ctx, key).Result()
			if err == redis.Nil {
				continue
			}
			if err != nil {
				return uuid.Nil, err
			}
			if agent.State(val) != agent.StateAvailable {
				continue
			}
			idStr := strings.TrimPrefix(key, agentKeyPrefix)
			id, err := uuid.Parse(idStr)
			if err != nil {
				continue
			}
			return id, nil
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return uuid.Nil, ErrNoAgent
}

// Route resolves a DID and picks an available agent (or busy).
func (r *Router) Route(ctx context.Context, didNumber string) (*Decision, error) {
	number := NormalizeDID(didNumber)
	if number == "" {
		return nil, ErrUnknownDID
	}

	did, err := r.DIDs.LookupByNumber(ctx, number)
	if err != nil {
		if errors.Is(err, ErrUnknownDID) {
			return &Decision{Action: ActionBusy, DID: number}, nil
		}
		return nil, err
	}

	dec := &Decision{
		DID:         did.Number,
		Destination: did.Destination,
	}
	if !isAgentPool(did.Destination) {
		dec.Action = ActionBusy
		return dec, nil
	}

	agentID, err := r.FirstAvailableAgent(ctx)
	if err != nil {
		if errors.Is(err, ErrNoAgent) {
			dec.Action = ActionBusy
			return dec, nil
		}
		return nil, err
	}
	dec.Action = ActionBridge
	dec.AgentID = agentID
	dec.AgentUser = AgentSIPUser(agentID)
	return dec, nil
}

// HandleEvent applies a routing decision to a live FreeSWITCH channel.
// Duplicate events for the same channel UUID are ignored (CUSTOM + CHANNEL_CREATE).
func (r *Router) HandleEvent(ctx context.Context, ev Event) (*Decision, error) {
	if ev.ChannelUUID == "" {
		return nil, fmt.Errorf("missing channel uuid")
	}
	if _, loaded := r.seen.LoadOrStore(ev.ChannelUUID, struct{}{}); loaded {
		return nil, nil
	}
	dec, err := r.Route(ctx, ev.DID)
	if err != nil {
		r.seen.Delete(ev.ChannelUUID)
		return nil, err
	}
	if err := r.Apply(ctx, ev.ChannelUUID, dec); err != nil {
		r.seen.Delete(ev.ChannelUUID)
		return dec, err
	}
	return dec, nil
}

// Apply bridges the parked channel to the agent or rejects with busy.
func (r *Router) Apply(ctx context.Context, channelUUID string, dec *Decision) error {
	_ = ctx
	if r.ESL == nil {
		return fmt.Errorf("esl unavailable")
	}
	switch dec.Action {
	case ActionBridge:
		// Transfer parked inbound leg into an inline bridge to the agent WebRTC user.
		cmd := fmt.Sprintf("uuid_transfer %s bridge:user/%s inline", channelUUID, escapeFS(dec.AgentUser))
		body, err := r.ESL.API(cmd)
		if err != nil {
			return err
		}
		if !isESLOK(body) {
			return fmt.Errorf("uuid_transfer: %s", strings.TrimSpace(body))
		}
		return nil
	case ActionBusy:
		// Cause 17 = USER_BUSY → SIP 486.
		cmd := fmt.Sprintf("uuid_kill %s USER_BUSY", channelUUID)
		body, err := r.ESL.API(cmd)
		if err != nil {
			return err
		}
		if !isESLOK(body) && !strings.Contains(strings.ToLower(body), "no such") {
			return fmt.Errorf("uuid_kill: %s", strings.TrimSpace(body))
		}
		return nil
	default:
		return fmt.Errorf("unknown action %q", dec.Action)
	}
}

func escapeFS(s string) string {
	return strings.NewReplacer(`\`, `\\`, ` `, `\ `).Replace(s)
}

func isESLOK(body string) bool {
	return strings.HasPrefix(strings.TrimSpace(body), "+OK")
}

// DIDLoader looks up DIDs from Postgres.
type DIDLoader struct {
	DB *sql.DB
}

// LookupByNumber returns the DID for an E.164 number.
func (l *DIDLoader) LookupByNumber(ctx context.Context, number string) (*models.DID, error) {
	number = NormalizeDID(number)
	row := l.DB.QueryRowContext(ctx, `
		SELECT id, number, carrier_id, destination, created_at
		FROM dids
		WHERE number = $1
	`, number)
	var d models.DID
	var carrierID uuid.NullUUID
	err := row.Scan(&d.ID, &d.Number, &carrierID, &d.Destination, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUnknownDID
	}
	if err != nil {
		return nil, err
	}
	if carrierID.Valid {
		id := carrierID.UUID
		d.CarrierID = &id
	}
	return &d, nil
}

// MapDIDLookup is an in-memory DIDLookup for tests.
type MapDIDLookup map[string]models.DID

// LookupByNumber implements DIDLookup.
func (m MapDIDLookup) LookupByNumber(_ context.Context, number string) (*models.DID, error) {
	number = NormalizeDID(number)
	d, ok := m[number]
	if !ok {
		return nil, ErrUnknownDID
	}
	cp := d
	return &cp, nil
}

// ParseEvent extracts Channel-UUID and DID from FreeSWITCH event headers.
func ParseEvent(get func(string) string) (Event, bool) {
	subclass := strings.ToLower(get("Event-Subclass"))
	name := strings.ToUpper(get("Event-Name"))
	did := firstNonEmpty(
		get("Variable_callvoice_did"),
		get("variable_callvoice_did"),
		get("Callvoice-Did"),
		get("callvoice_did"),
		get("Caller-Destination-Number"),
		get("Variable_destination_number"),
		get("Channel-Destination-Number"),
	)
	uuid := firstNonEmpty(get("Unique-ID"), get("Channel-Call-UUID"), get("Caller-Unique-ID"))

	switch {
	case name == "CUSTOM" && (subclass == "callvoice::inbound" || strings.Contains(subclass, "callvoice")):
		// intended path from dialplan/lua park notify
	case name == "CHANNEL_CREATE":
		dir := strings.ToLower(get("Call-Direction"))
		if dir != "" && dir != "inbound" {
			return Event{}, false
		}
		// Ignore agent WebRTC legs (user/agent-*).
		if strings.HasPrefix(strings.ToLower(get("Caller-Destination-Number")), "agent-") {
			return Event{}, false
		}
		if strings.Contains(strings.ToLower(get("Channel-Name")), "agent-") {
			return Event{}, false
		}
	default:
		return Event{}, false
	}

	if uuid == "" || did == "" {
		return Event{}, false
	}
	return Event{ChannelUUID: uuid, DID: NormalizeDID(did)}, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// EventConn is the ESL surface needed by the inbound event loop (testable).
type EventConn interface {
	Send(cmd string) error
	ReadEvent() (headerGetter, error)
	Close()
}

type headerGetter interface {
	Get(string) string
}

// DialEventConn dials FreeSWITCH ESL for event subscription.
// Separated from the API client so event reads do not block originate/keepalive.
type DialEventConn func(addr, password string) (EventConn, error)

// RunListener connects to ESL, subscribes to CUSTOM/CHANNEL_CREATE, and routes inbound.
// Retries with backoff until ctx is cancelled. dial may be nil to use the default ESL dialer.
func RunListener(ctx context.Context, addr, password string, router *Router, dial DialEventConn) {
	if dial == nil {
		dial = defaultDialEventConn
	}
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := dial(addr, password)
		if err != nil {
			log.Printf("inbound esl: connect %s: %v (retry in %s)", addr, err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		backoff = time.Second
		log.Printf("inbound esl: event listener connected to %s", addr)
		if err := conn.Send("events json CUSTOM CHANNEL_CREATE"); err != nil {
			log.Printf("inbound esl: subscribe: %v", err)
			conn.Close()
			continue
		}
		err = listenLoop(ctx, conn, router)
		conn.Close()
		if ctx.Err() != nil {
			return
		}
		log.Printf("inbound esl: listener stopped: %v (reconnect)", err)
	}
}

func listenLoop(ctx context.Context, conn EventConn, router *Router) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ev, err := conn.ReadEvent()
		if err != nil {
			return err
		}
		parsed, ok := ParseEvent(ev.Get)
		if !ok {
			continue
		}
		dec, err := router.HandleEvent(ctx, parsed)
		if err != nil {
			log.Printf("inbound route did=%s uuid=%s: %v", parsed.DID, parsed.ChannelUUID, err)
			continue
		}
		if dec == nil {
			continue
		}
		log.Printf("inbound route did=%s uuid=%s action=%s agent=%s",
			parsed.DID, parsed.ChannelUUID, dec.Action, dec.AgentUser)
	}
}
