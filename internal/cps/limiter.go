// Package cps implements a fixed 1-second window CPS limiter backed by Redis.
// A true sliding window is optional for later; fixed windows are acceptable for jalon B.
package cps

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const windowSeconds = 1

// Limiter enforces calls-per-second caps using Redis counters keyed by second.
type Limiter struct {
	client *redis.Client
}

// New returns a CPS limiter backed by the given Redis client.
func New(client *redis.Client) *Limiter {
	return &Limiter{client: client}
}

// Allow reports whether another call is allowed for key within maxCPS for the second of now.
// Keys are typically cps:global or cps:carrier:{id}.
func (l *Limiter) Allow(ctx context.Context, key string, maxCPS int, now time.Time) (bool, error) {
	if maxCPS <= 0 {
		return false, nil
	}

	windowKey := fmt.Sprintf("%s:%d", key, now.Unix())
	count, err := l.client.Incr(ctx, windowKey).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := l.client.ExpireNX(ctx, windowKey, time.Duration(windowSeconds+1)*time.Second).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(maxCPS), nil
}
