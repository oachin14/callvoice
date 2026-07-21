// Package cps implements a fixed 1-second window CPS limiter backed by Redis.
// A true sliding window is optional for later; fixed windows are acceptable for jalon B.
package cps

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const windowTTLSeconds = 2

var allowScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
if count > tonumber(ARGV[1]) then
  redis.call('DECR', KEYS[1])
  return 0
end
return 1
`)

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
	res, err := allowScript.Run(ctx, l.client, []string{windowKey}, maxCPS, windowTTLSeconds).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}
