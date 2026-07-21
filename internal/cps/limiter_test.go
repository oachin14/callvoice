package cps_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/cps"
)

func TestAllowUnderCap(t *testing.T) {
	r := miniredis.RunT(t)
	lim := cps.New(redis.NewClient(&redis.Options{Addr: r.Addr()}))
	ok, err := lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	ok, _ = lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
	require.True(t, ok)
	ok, _ = lim.Allow(context.Background(), "cps:carrier:1", 2, time.Now())
	require.False(t, ok)
}
