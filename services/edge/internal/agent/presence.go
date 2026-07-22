package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const DefaultTTL = 2 * time.Hour

// Agent presence states stored in Redis.
type State string

const (
	StateAvailable State = "available"
	StatePaused    State = "paused"
	StateOnCall    State = "on_call"
)

// ErrNotFound means the agent has no active presence key.
var ErrNotFound = errors.New("agent presence not found")

// ErrInvalidState is returned for states other than available|paused.
var ErrInvalidState = errors.New("invalid agent state")

// errClaimMiss signals CAS claim failure inside a Redis WATCH txn (not exported).
var errClaimMiss = errors.New("claim miss")

// Presence tracks live agent availability in Redis.
type Presence struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewPresence returns a Redis-backed presence store.
func NewPresence(rdb *redis.Client, ttl time.Duration) *Presence {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Presence{rdb: rdb, ttl: ttl}
}

// Key returns the Redis key for an agent's presence.
func Key(userID uuid.UUID) string {
	return "agent:" + userID.String()
}

type presenceValue struct {
	State      State  `json:"state"`
	CampaignID string `json:"campaign_id,omitempty"`
}

func parsePresence(raw string) (presenceValue, error) {
	var v presenceValue
	if err := json.Unmarshal([]byte(raw), &v); err == nil && v.State != "" {
		return v, nil
	}
	switch State(raw) {
	case StateAvailable, StatePaused, StateOnCall:
		return presenceValue{State: State(raw)}, nil
	default:
		return presenceValue{}, fmt.Errorf("invalid presence %q", raw)
	}
}

func encodePresence(v presenceValue) (string, error) {
	if v.CampaignID == "" {
		return string(v.State), nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (p *Presence) readPresence(ctx context.Context, userID uuid.UUID) (presenceValue, error) {
	val, err := p.rdb.Get(ctx, Key(userID)).Result()
	if err == redis.Nil {
		return presenceValue{}, ErrNotFound
	}
	if err != nil {
		return presenceValue{}, err
	}
	return parsePresence(val)
}

func (p *Presence) writePresence(ctx context.Context, userID uuid.UUID, v presenceValue) error {
	raw, err := encodePresence(v)
	if err != nil {
		return err
	}
	return p.rdb.Set(ctx, Key(userID), raw, p.ttl).Err()
}

// Start registers the agent as available.
func (p *Presence) Start(ctx context.Context, userID uuid.UUID) error {
	return p.writePresence(ctx, userID, presenceValue{State: StateAvailable})
}

// Stop clears the agent presence key.
func (p *Presence) Stop(ctx context.Context, userID uuid.UUID) error {
	return p.rdb.Del(ctx, Key(userID)).Err()
}

// Get returns the current presence state.
func (p *Presence) Get(ctx context.Context, userID uuid.UUID) (State, error) {
	v, err := p.readPresence(ctx, userID)
	if err != nil {
		return "", err
	}
	return v.State, nil
}

// SetCampaign stores the agent's active campaign on the presence record.
func (p *Presence) SetCampaign(ctx context.Context, userID uuid.UUID, campaignID *uuid.UUID) error {
	v, err := p.readPresence(ctx, userID)
	if err != nil {
		return err
	}
	if campaignID == nil || *campaignID == uuid.Nil {
		v.CampaignID = ""
	} else {
		v.CampaignID = campaignID.String()
	}
	return p.writePresence(ctx, userID, v)
}

// SetState updates presence to available or paused. Requires an active session.
func (p *Presence) SetState(ctx context.Context, userID uuid.UUID, state State) error {
	if state != StateAvailable && state != StatePaused {
		return ErrInvalidState
	}
	n, err := p.rdb.Exists(ctx, Key(userID)).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	v, err := p.readPresence(ctx, userID)
	if err != nil {
		return err
	}
	v.State = state
	return p.writePresence(ctx, userID, v)
}

// ClaimAvailable atomically moves an agent from available → on_call.
// Returns true if this caller won the claim.
func (p *Presence) ClaimAvailable(ctx context.Context, userID uuid.UUID) (bool, error) {
	key := Key(userID)
	err := p.rdb.Watch(ctx, func(tx *redis.Tx) error {
		val, err := tx.Get(ctx, key).Result()
		if err == redis.Nil {
			return errClaimMiss
		}
		if err != nil {
			return err
		}
		v, err := parsePresence(val)
		if err != nil || v.State != StateAvailable {
			return errClaimMiss
		}
		v.State = StateOnCall
		raw, err := encodePresence(v)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, raw, p.ttl)
			return nil
		})
		return err
	}, key)
	if errors.Is(err, errClaimMiss) {
		return false, nil
	}
	if err == redis.TxFailedErr {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReleaseOnCall atomically restores available when the agent is still on_call.
// No-op (and no error) if the key is missing or not on_call.
func (p *Presence) ReleaseOnCall(ctx context.Context, userID uuid.UUID) error {
	key := Key(userID)
	err := p.rdb.Watch(ctx, func(tx *redis.Tx) error {
		val, err := tx.Get(ctx, key).Result()
		if err == redis.Nil {
			return nil
		}
		if err != nil {
			return err
		}
		v, err := parsePresence(val)
		if err != nil || v.State != StateOnCall {
			return nil
		}
		v.State = StateAvailable
		raw, err := encodePresence(v)
		if err != nil {
			return err
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, raw, p.ttl)
			return nil
		})
		return err
	}, key)
	if err == redis.TxFailedErr {
		// Concurrent update — leave state as-is; next cleanup/hangup can retry.
		return nil
	}
	return err
}
