package dialer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/callvoice/callvoice/internal/models"
	"github.com/callvoice/callvoice/services/edge/internal/agent"
	"github.com/callvoice/callvoice/services/edge/internal/cpsgate"
	"github.com/callvoice/callvoice/services/edge/internal/fs"
)

const (
	callEventsChannel = "call.events"
	agentCallKeyPref  = "call:agent:"
	callMetaKeyPref   = "call:meta:"
)

// ErrCarrierCapacity means every enabled carrier denied the call (CPS or channels).
var ErrCarrierCapacity = errors.New("carrier_capacity")

// ErrInvalidE164 means the destination or caller ID is not E.164.
var ErrInvalidE164 = errors.New("invalid_e164")

// ErrNoActiveCall means the agent has no tracked outbound call to hang up.
var ErrNoActiveCall = errors.New("no_active_call")

// ErrCallNotFound means the given call UUID is not tracked.
var ErrCallNotFound = errors.New("call_not_found")

// ErrCallForbidden means the call exists but is owned by another agent.
var ErrCallForbidden = errors.New("call_forbidden")

var e164Re = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)

// ESL is the FreeSWITCH command surface used for originate/hangup.
type ESL interface {
	API(cmd string) (string, error)
}

// CarrierLister loads carriers for outbound failover.
type CarrierLister interface {
	ListOrdered(ctx context.Context) ([]models.Carrier, error)
}

// OutboundRequest is a server-side manual originate.
type OutboundRequest struct {
	AgentID    uuid.UUID
	To         string
	CallerID   string
	CampaignID *uuid.UUID
	LeadID     *uuid.UUID
}

// OutboundResult is returned after a successful ESL originate.
type OutboundResult struct {
	CallUUID  string    `json:"call_uuid"`
	CarrierID uuid.UUID `json:"carrier_id"`
	To        string    `json:"to"`
}

// Manual places outbound calls with CPS and channel failover.
type Manual struct {
	ESL          ESL
	Gate         *cpsgate.Gate
	RDB          *redis.Client
	Carriers     CarrierLister
	Pres         *agent.Presence // optional; releases on_call after hangup
	GlobalMaxCPS int
	Now          func() time.Time
}

// ChannelKey is the Redis concurrent-channel counter for a carrier.
func ChannelKey(carrierID uuid.UUID) string {
	return "channels:carrier:" + carrierID.String()
}

// IsE164 reports whether s is a valid E.164 number (+ and 1–15 digits).
func IsE164(s string) bool {
	return e164Re.MatchString(s)
}

// EscapeDialString escapes FreeSWITCH dialstring special characters.
func EscapeDialString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `,`, `\,`, `'`, `\'`, `{`, `\{`, `}`, `\}`)
	return r.Replace(s)
}

// BuildOriginate builds an ESL api originate command bridging agent ↔ gateway.
func BuildOriginate(agentUser, gatewayName, to, callerID, callUUID string) (string, error) {
	if !IsE164(to) {
		return "", ErrInvalidE164
	}
	if callerID != "" && !IsE164(callerID) {
		return "", ErrInvalidE164
	}
	if agentUser == "" || gatewayName == "" || callUUID == "" {
		return "", fmt.Errorf("missing originate fields")
	}

	vars := "origination_uuid=" + EscapeDialString(callUUID)
	if callerID != "" {
		vars += ",origination_caller_id_number=" + EscapeDialString(callerID)
	}
	return fmt.Sprintf(
		"originate {%s}user/%s &bridge(sofia/gateway/%s/%s)",
		vars,
		EscapeDialString(agentUser),
		EscapeDialString(gatewayName),
		EscapeDialString(to),
	), nil
}

// Originate selects a carrier (CPS + channels), originates via ESL, and tracks the call.
func (m *Manual) Originate(ctx context.Context, req OutboundRequest) (*OutboundResult, error) {
	if !IsE164(req.To) {
		return nil, ErrInvalidE164
	}
	if req.CallerID != "" && !IsE164(req.CallerID) {
		return nil, ErrInvalidE164
	}
	if req.AgentID == uuid.Nil {
		return nil, fmt.Errorf("agent id required")
	}

	now := m.now()
	if m.GlobalMaxCPS > 0 {
		ok, err := m.Gate.AllowGlobal(ctx, m.GlobalMaxCPS, now)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrCarrierCapacity
		}
	}

	carriers, err := m.Carriers.ListOrdered(ctx)
	if err != nil {
		return nil, err
	}

	agentUser := "agent-" + req.AgentID.String()
	var lastESL error
	tried := 0

	for _, c := range carriers {
		if !c.Enabled {
			continue
		}
		tried++

		okChan, err := m.channelAvailable(ctx, c)
		if err != nil {
			return nil, err
		}
		if !okChan {
			continue
		}

		okCPS, err := m.Gate.AllowCarrier(ctx, c.ID.String(), c.MaxCPS, now)
		if err != nil {
			return nil, err
		}
		if !okCPS {
			continue
		}

		callUUID := uuid.New().String()
		cmd, err := BuildOriginate(agentUser, fs.GatewayName(c.ID), req.To, req.CallerID, callUUID)
		if err != nil {
			return nil, err
		}
		body, err := m.ESL.API(cmd)
		if err != nil || !isESLOK(body) {
			if err != nil {
				lastESL = err
			} else {
				lastESL = fmt.Errorf("esl originate: %s", strings.TrimSpace(body))
			}
			continue
		}

		if err := m.trackCall(ctx, req, c.ID, callUUID); err != nil {
			_, _ = m.ESL.API("uuid_kill " + callUUID)
			return nil, err
		}
		_ = m.publishEvent(ctx, map[string]any{
			"type":       "answered",
			"call_uuid":  callUUID,
			"agent_id":   req.AgentID.String(),
			"carrier_id": c.ID.String(),
			"to":         req.To,
		})

		return &OutboundResult{CallUUID: callUUID, CarrierID: c.ID, To: req.To}, nil
	}

	if tried == 0 {
		return nil, ErrCarrierCapacity
	}
	if lastESL != nil {
		return nil, fmt.Errorf("originate failed: %w", lastESL)
	}
	return nil, ErrCarrierCapacity
}

// Hangup kills the agent's active call (or the given UUID) and cleans channel state.
// Ownership: empty UUID uses call:agent:{id}; explicit UUID requires meta.AgentID == agentID.
func (m *Manual) Hangup(ctx context.Context, agentID uuid.UUID, callUUID string) error {
	var meta *callMeta

	if callUUID == "" {
		stored, err := m.RDB.Get(ctx, agentCallKey(agentID)).Result()
		if err == redis.Nil {
			return ErrNoActiveCall
		}
		if err != nil {
			return err
		}
		callUUID = stored
		meta, _ = m.loadMeta(ctx, callUUID)
	} else {
		loaded, err := m.loadMeta(ctx, callUUID)
		if err == redis.Nil {
			return ErrCallNotFound
		}
		if err != nil {
			return err
		}
		if loaded.AgentID != agentID.String() {
			return ErrCallForbidden
		}
		meta = loaded
	}

	body, err := m.ESL.API("uuid_kill " + callUUID)
	if err != nil {
		return err
	}
	if !isESLOK(body) && !strings.Contains(strings.ToLower(body), "no such") {
		return fmt.Errorf("uuid_kill: %s", strings.TrimSpace(body))
	}

	if err := m.cleanupCall(ctx, agentID, callUUID, meta); err != nil {
		return err
	}
	if m.Pres != nil {
		_ = m.Pres.ReleaseOnCall(ctx, agentID)
	}
	_ = m.publishEvent(ctx, map[string]any{
		"type":       "hangup",
		"call_uuid":  callUUID,
		"agent_id":   agentID.String(),
		"carrier_id": metaCarrier(meta),
	})
	return nil
}

type callMeta struct {
	AgentID    string `json:"agent_id"`
	CarrierID  string `json:"carrier_id"`
	To         string `json:"to"`
	CampaignID string `json:"campaign_id,omitempty"`
	LeadID     string `json:"lead_id,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
}

func (m *Manual) channelAvailable(ctx context.Context, c models.Carrier) (bool, error) {
	if c.MaxChannels <= 0 {
		return false, nil
	}
	n, err := m.channelCount(ctx, c.ID)
	if err != nil {
		return false, err
	}
	return n < int64(c.MaxChannels), nil
}

func (m *Manual) channelCount(ctx context.Context, carrierID uuid.UUID) (int64, error) {
	val, err := m.RDB.Get(ctx, ChannelKey(carrierID)).Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (m *Manual) trackCall(ctx context.Context, req OutboundRequest, carrierID uuid.UUID, callUUID string) error {
	if err := m.RDB.Incr(ctx, ChannelKey(carrierID)).Err(); err != nil {
		return err
	}
	meta := callMeta{
		AgentID:   req.AgentID.String(),
		CarrierID: carrierID.String(),
		To:        req.To,
		StartedAt: m.now().UTC().Format(time.RFC3339),
	}
	if req.CampaignID != nil && *req.CampaignID != uuid.Nil {
		meta.CampaignID = req.CampaignID.String()
	}
	if req.LeadID != nil && *req.LeadID != uuid.Nil {
		meta.LeadID = req.LeadID.String()
	}
	body, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	pipe := m.RDB.TxPipeline()
	pipe.Set(ctx, callMetaKey(callUUID), body, 24*time.Hour)
	pipe.Set(ctx, agentCallKey(req.AgentID), callUUID, 24*time.Hour)
	_, err = pipe.Exec(ctx)
	return err
}

func (m *Manual) cleanupCall(ctx context.Context, agentID uuid.UUID, callUUID string, meta *callMeta) error {
	if meta != nil {
		if cid, err := uuid.Parse(meta.CarrierID); err == nil {
			n, err := m.RDB.Decr(ctx, ChannelKey(cid)).Result()
			if err != nil {
				return err
			}
			if n < 0 {
				_ = m.RDB.Set(ctx, ChannelKey(cid), 0, 0).Err()
			}
		}
	}
	_ = m.RDB.Del(ctx, callMetaKey(callUUID)).Err()
	cur, err := m.RDB.Get(ctx, agentCallKey(agentID)).Result()
	if err == nil && cur == callUUID {
		_ = m.RDB.Del(ctx, agentCallKey(agentID)).Err()
	}
	return nil
}

func (m *Manual) loadMeta(ctx context.Context, callUUID string) (*callMeta, error) {
	raw, err := m.RDB.Get(ctx, callMetaKey(callUUID)).Bytes()
	if err != nil {
		return nil, err
	}
	var meta callMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func (m *Manual) publishEvent(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.RDB.Publish(ctx, callEventsChannel, body).Err()
}

func (m *Manual) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now().UTC()
}

func agentCallKey(agentID uuid.UUID) string {
	return agentCallKeyPref + agentID.String()
}

func callMetaKey(callUUID string) string {
	return callMetaKeyPref + callUUID
}

func isESLOK(body string) bool {
	s := strings.TrimSpace(body)
	return strings.HasPrefix(s, "+OK")
}

func metaCarrier(meta *callMeta) string {
	if meta == nil {
		return ""
	}
	return meta.CarrierID
}
