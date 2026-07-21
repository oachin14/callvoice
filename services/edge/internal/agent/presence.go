package agent

import (
	"context"
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

// Start registers the agent as available.
func (p *Presence) Start(ctx context.Context, userID uuid.UUID) error {
	return p.rdb.Set(ctx, Key(userID), string(StateAvailable), p.ttl).Err()
}

// Stop clears the agent presence key.
func (p *Presence) Stop(ctx context.Context, userID uuid.UUID) error {
	return p.rdb.Del(ctx, Key(userID)).Err()
}

// Get returns the current presence state.
func (p *Presence) Get(ctx context.Context, userID uuid.UUID) (State, error) {
	val, err := p.rdb.Get(ctx, Key(userID)).Result()
	if err == redis.Nil {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return State(val), nil
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
	if err := p.rdb.Set(ctx, Key(userID), string(state), p.ttl).Err(); err != nil {
		return fmt.Errorf("set presence: %w", err)
	}
	return nil
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
		if State(val) != StateAvailable {
			return errClaimMiss
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, string(StateOnCall), p.ttl)
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
		if State(val) != StateOnCall {
			return nil
		}
		_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
			pipe.Set(ctx, key, string(StateAvailable), p.ttl)
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
