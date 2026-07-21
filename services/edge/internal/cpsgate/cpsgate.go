package cpsgate

import (
	"context"
	"fmt"
	"time"

	"github.com/callvoice/callvoice/internal/cps"
	"github.com/redis/go-redis/v9"
)

const globalKey = "cps:global"

// Gate wraps the shared CPS limiter with edge key conventions.
type Gate struct {
	lim *cps.Limiter
}

// New returns a CPS gate backed by Redis.
func New(client *redis.Client) *Gate {
	return &Gate{lim: cps.New(client)}
}

// CarrierKey returns the Redis CPS key for a carrier.
func CarrierKey(id string) string {
	return fmt.Sprintf("cps:carrier:%s", id)
}

// AllowGlobal checks the global CPS cap.
func (g *Gate) AllowGlobal(ctx context.Context, maxCPS int, now time.Time) (bool, error) {
	return g.lim.Allow(ctx, globalKey, maxCPS, now)
}

// AllowCarrier checks the per-carrier CPS cap.
func (g *Gate) AllowCarrier(ctx context.Context, carrierID string, maxCPS int, now time.Time) (bool, error) {
	return g.lim.Allow(ctx, CarrierKey(carrierID), maxCPS, now)
}
