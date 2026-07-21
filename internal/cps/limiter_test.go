package cps_test

import (
	"context"
	"sync"
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

func TestAllowMaxCPSZeroOrNegative(t *testing.T) {
	r := miniredis.RunT(t)
	lim := cps.New(redis.NewClient(&redis.Options{Addr: r.Addr()}))
	ok, err := lim.Allow(context.Background(), "cps:carrier:1", 0, time.Now())
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = lim.Allow(context.Background(), "cps:carrier:1", -1, time.Now())
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAllowConcurrentAtCap(t *testing.T) {
	r := miniredis.RunT(t)
	lim := cps.New(redis.NewClient(&redis.Options{Addr: r.Addr()}))
	const maxCPS = 5
	const attempts = 50
	now := time.Now()
	results := make([]bool, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := lim.Allow(context.Background(), "cps:carrier:concurrent", maxCPS, now)
			require.NoError(t, err)
			results[i] = ok
		}(i)
	}
	wg.Wait()
	successes := 0
	for _, ok := range results {
		if ok {
			successes++
		}
	}
	require.Equal(t, maxCPS, successes)
}
