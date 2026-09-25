package model

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useChannelLimitRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	prevEnabled, prevRDB := common.RedisEnabled, common.RDB
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled, common.RDB = prevEnabled, prevRDB
		channelSaturated.Store(map[int]struct{}{})
		channelRefreshedAt.Store(0)
	})
	return srv
}

func withCachedChannels(t *testing.T, channels ...*Channel) {
	t.Helper()
	channelSyncLock.Lock()
	prev := channelsIDM
	channelsIDM = map[int]*Channel{}
	for _, ch := range channels {
		channelsIDM[ch.Id] = ch
	}
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		channelsIDM = prev
		channelSyncLock.Unlock()
	})
}

func intPtr(v int) *int { return &v }

// forceRefresh bypasses the once-a-second guard, which exists for production
// throughput and would otherwise make these tests depend on wall clock.
func forceRefresh(ctx context.Context) {
	channelRefreshedAt.Store(0)
	RefreshChannelRateLimits(ctx)
}

func TestAChannelUnderItsLimitStaysSelectable(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	ch := &Channel{Id: 1, RateLimitRPM: intPtr(5)}
	withCachedChannels(t, ch)

	for i := 0; i < 4; i++ {
		RecordChannelRequest(ctx, 1)
	}
	forceRefresh(ctx)

	assert.False(t, ChannelIsRateLimited(1), "4 of 5 used is not saturated")
	assert.Equal(t, []*Channel{ch}, dropRateLimitedChannels([]*Channel{ch}))
}

func TestAChannelAtItsRPMLimitIsDroppedFromSelection(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	busy := &Channel{Id: 1, RateLimitRPM: intPtr(3)}
	idle := &Channel{Id: 2, RateLimitRPM: intPtr(3)}
	withCachedChannels(t, busy, idle)

	for i := 0; i < 3; i++ {
		RecordChannelRequest(ctx, 1)
	}
	forceRefresh(ctx)

	require.True(t, ChannelIsRateLimited(1), "the busy channel must be saturated")
	require.False(t, ChannelIsRateLimited(2), "the idle channel must not be")

	// This is the noisy-neighbour fix: traffic goes to the idle channel instead
	// of being sent to the busy one and coming back 429.
	assert.Equal(t, []*Channel{idle}, dropRateLimitedChannels([]*Channel{busy, idle}))
}

func TestAChannelAtItsTPMLimitIsDroppedFromSelection(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	ch := &Channel{Id: 7, RateLimitTPM: intPtr(1000)}
	withCachedChannels(t, ch)

	RecordChannelTokens(ctx, 7, 400)
	forceRefresh(ctx)
	require.False(t, ChannelIsRateLimited(7), "400 of 1000 tokens is not saturated")

	RecordChannelTokens(ctx, 7, 700)
	forceRefresh(ctx)
	assert.True(t, ChannelIsRateLimited(7), "1100 of 1000 tokens is saturated")
}

func TestAChannelWithNoLimitIsNeverSaturated(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	ch := &Channel{Id: 3} // nil limits: unlimited, and the default
	withCachedChannels(t, ch)

	for i := 0; i < 50; i++ {
		RecordChannelRequest(ctx, 3)
		RecordChannelTokens(ctx, 3, 10_000)
	}
	forceRefresh(ctx)

	assert.False(t, ChannelIsRateLimited(3))
	assert.False(t, ch.HasRateLimit())
}

func TestAnExplicitZeroLimitMeansUnlimited(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	ch := &Channel{Id: 4, RateLimitRPM: intPtr(0), RateLimitTPM: intPtr(0)}
	withCachedChannels(t, ch)

	for i := 0; i < 20; i++ {
		RecordChannelRequest(ctx, 4)
	}
	forceRefresh(ctx)

	// 0 clears a limit, matching the per-customer limiter's convention. The
	// pointer is what makes an explicit 0 reach the database at all.
	//
	// Two guards protect this independently -- HasRateLimit() keeps the channel
	// out of limitedChannels(), and the per-counter > 0 checks skip the probe --
	// so mutating either one alone will NOT fail this test. Removing both does.
	// Recorded because a single-mutation check here looks like a toothless test
	// and is not.
	assert.False(t, ChannelIsRateLimited(4))
}

func TestWhenEveryCandidateIsSaturatedSelectionIsNotStarved(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	a := &Channel{Id: 1, RateLimitRPM: intPtr(1)}
	b := &Channel{Id: 2, RateLimitRPM: intPtr(1)}
	withCachedChannels(t, a, b)

	RecordChannelRequest(ctx, 1)
	RecordChannelRequest(ctx, 2)
	forceRefresh(ctx)
	require.True(t, ChannelIsRateLimited(1))
	require.True(t, ChannelIsRateLimited(2))

	// dropRateLimitedChannels returns empty, and the caller in
	// GetRandomSatisfiedChannel deliberately keeps the full list in that case.
	// Turning a busy minute into "no channel found" would be an outage of our
	// making; shedding load is the upstream's job.
	assert.Empty(t, dropRateLimitedChannels([]*Channel{a, b}))
}

func TestTheSnapshotIsNotRebuiltOnEveryCall(t *testing.T) {
	useChannelLimitRedis(t)
	ctx := context.Background()
	ch := &Channel{Id: 9, RateLimitRPM: intPtr(1)}
	withCachedChannels(t, ch)

	forceRefresh(ctx)
	require.False(t, ChannelIsRateLimited(9))

	RecordChannelRequest(ctx, 9)
	// Without forcing, the once-a-second guard should suppress this refresh, so
	// the snapshot still reports the channel as available. That staleness is the
	// price paid for keeping Redis off the selection path.
	RefreshChannelRateLimits(ctx)
	assert.False(t, ChannelIsRateLimited(9), "refresh should have been rate limited itself")

	forceRefresh(ctx)
	assert.True(t, ChannelIsRateLimited(9), "and correct once it is allowed to run")
}
