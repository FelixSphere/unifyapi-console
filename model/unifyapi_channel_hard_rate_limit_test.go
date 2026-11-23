/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const hardSetting = `{"hard_rate_limit":true}`

// resetHardLimitState also steps clear of a minute boundary: the window is
// wall clock, and a test that straddles one would count in two windows.
func resetHardLimitState(t *testing.T) {
	t.Helper()
	if ChannelRateLimitRetryAfter() < 3 {
		time.Sleep(4 * time.Second)
	}
	reset := func() {
		channelHardClosed.Store(map[int]int64{})
		hardLocal.Lock()
		hardLocal.requests, hardLocal.tokens = nil, nil
		hardLocal.Unlock()
		hardLocalWarnedAt.Store(0)
	}
	reset()
	t.Cleanup(reset)
}

// withRoutedChannels puts channels in the selection cache for one group and
// model, the way InitChannelCache would.
func withRoutedChannels(t *testing.T, group, modelName string, channels ...*Channel) {
	t.Helper()
	withCachedChannels(t, channels...)
	ids := make([]int, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.Id)
	}
	channelSyncLock.Lock()
	prevRoutes, prevCache := group2model2channels, common.MemoryCacheEnabled
	group2model2channels = map[string]map[string][]int{group: {modelName: ids}}
	common.MemoryCacheEnabled = true
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels, common.MemoryCacheEnabled = prevRoutes, prevCache
		channelSyncLock.Unlock()
	})
}

func priorityPtr(v int64) *int64 { return &v }

func TestHardRateLimitIsOffUnlessTheChannelSaysSo(t *testing.T) {
	assert.False(t, (&Channel{Id: 1, RateLimitRPM: intPtr(5)}).HardRateLimit(), "a limit alone is the soft limit")
	assert.False(t, (&Channel{Id: 1, RateLimitRPM: intPtr(5), OtherSettings: `{"excluded_groups":["a"]}`}).HardRateLimit())
	assert.False(t, (&Channel{Id: 1, RateLimitRPM: intPtr(5), OtherSettings: `{"hard_rate_limit":false}`}).HardRateLimit())
	assert.False(t, (&Channel{Id: 1, OtherSettings: hardSetting}).HardRateLimit(), "the switch without a limit caps nothing")
	assert.False(t, (&Channel{Id: 1, RateLimitRPM: intPtr(0), OtherSettings: hardSetting}).HardRateLimit(), "zero is unlimited")
	assert.True(t, (&Channel{Id: 1, RateLimitRPM: intPtr(5), OtherSettings: hardSetting}).HardRateLimit())
	assert.True(t, (&Channel{Id: 1, RateLimitTPM: intPtr(5), OtherSettings: hardSetting}).HardRateLimit())
	assert.False(t, (*Channel)(nil).HardRateLimit())
}

func TestAHardChannelAdmitsExactlyItsRPMHoweverManyArriveAtOnce(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	ch := &Channel{Id: 7, RateLimitRPM: intPtr(5), OtherSettings: hardSetting}

	var admitted atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if AdmitChannelRequest(context.Background(), ch) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()

	assert.EqualValues(t, 5, admitted.Load(), "RPM 5 admits 5 of 40 concurrent requests, not about 5")
	assert.True(t, ChannelIsHardRateLimited(7))
}

func TestASingleHardChannelAtItsRPMIsNotSelected(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	ch := &Channel{Id: 11, RateLimitRPM: intPtr(1), OtherSettings: hardSetting}
	withRoutedChannels(t, "g", "m", ch)
	ctx := context.Background()

	first, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.Equal(t, ch, first)
	require.True(t, AdmitChannelRequest(ctx, first), "the first request of the window fits")

	second, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
	assert.Nil(t, second, "the only channel is full and hard-limited: it must not be handed out")
	assert.ErrorIs(t, err, ErrChannelHardRateLimited)
	assert.False(t, AdmitChannelRequest(ctx, ch), "and a path that holds the channel already is refused too")
}

// The control for the test above: the same channel without the switch is the
// soft limit, and is handed out over its limit exactly as before.
func TestASingleSoftChannelOverItsRPMIsStillSelected(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	ch := &Channel{Id: 12, RateLimitRPM: intPtr(1)}
	withRoutedChannels(t, "g", "m", ch)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		got, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
		require.NoError(t, err)
		require.Equal(t, ch, got)
		require.True(t, AdmitChannelRequest(ctx, got))
		RecordChannelRequest(ctx, got.Id)
		forceRefresh(ctx)
	}
	assert.True(t, ChannelIsRateLimited(12), "it is over its limit")
	assert.False(t, ChannelIsHardRateLimited(12))
}

func TestTwoSoftChannelsBothOverTheirLimitStillFallThrough(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	a := &Channel{Id: 13, RateLimitRPM: intPtr(1), Priority: priorityPtr(0)}
	b := &Channel{Id: 14, RateLimitRPM: intPtr(1), Priority: priorityPtr(0)}
	withRoutedChannels(t, "g", "m", a, b)
	ctx := context.Background()
	RecordChannelRequest(ctx, 13)
	RecordChannelRequest(ctx, 14)
	forceRefresh(ctx)

	got, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.NotNil(t, got, "soft limits never refuse")
}

func TestAFullHardChannelGivesWayToAnotherChannel(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	weight := uint(100)
	hard := &Channel{Id: 21, RateLimitRPM: intPtr(1), OtherSettings: hardSetting, Priority: priorityPtr(10), Weight: &weight}
	backup := &Channel{Id: 22, Priority: priorityPtr(0), Weight: &weight}
	withRoutedChannels(t, "g", "m", hard, backup)
	ctx := context.Background()

	first, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
	require.NoError(t, err)
	require.Equal(t, 21, first.Id, "the higher priority channel serves while it has room")
	require.True(t, AdmitChannelRequest(ctx, first))

	for i := 0; i < 5; i++ {
		got, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, 22, got.Id, "the lower priority channel takes over; the full one is never used")
	}
	assert.Len(t, group2model2channels["g"]["m"], 2, "the cached candidate list is not modified")
}

func TestAHardChannelAtItsTPMRefusesUntilTheWindowEnds(t *testing.T) {
	useChannelLimitRedis(t)
	resetHardLimitState(t)
	ch := &Channel{Id: 31, RateLimitTPM: intPtr(1000), OtherSettings: hardSetting}
	withRoutedChannels(t, "g", "m", ch)
	ctx := context.Background()

	require.True(t, AdmitChannelRequest(ctx, ch))
	RecordChannelTokens(ctx, 31, 400)
	require.True(t, AdmitChannelRequest(ctx, ch), "400 of 1000 tokens leaves room")
	RecordChannelTokens(ctx, 31, 700)

	assert.False(t, AdmitChannelRequest(ctx, ch), "1100 of 1000: the window is closed")
	_, err := GetRandomSatisfiedChannel("g", "m", 0, "/v1/chat/completions")
	assert.ErrorIs(t, err, ErrChannelHardRateLimited)
}

func TestWithoutRedisAHardChannelIsStillCapped(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })
	resetHardLimitState(t)
	rpm := &Channel{Id: 41, RateLimitRPM: intPtr(2), OtherSettings: hardSetting}
	tpm := &Channel{Id: 42, RateLimitTPM: intPtr(100), OtherSettings: hardSetting}
	withRoutedChannels(t, "g", "m", rpm, tpm)
	ctx := context.Background()

	assert.True(t, AdmitChannelRequest(ctx, rpm))
	assert.True(t, AdmitChannelRequest(ctx, rpm))
	assert.False(t, AdmitChannelRequest(ctx, rpm), "the third request of the window is refused without Redis too")

	assert.True(t, AdmitChannelRequest(ctx, tpm))
	RecordChannelTokens(ctx, 42, 100)
	assert.False(t, AdmitChannelRequest(ctx, tpm))
}

func TestAdmissionLeavesEveryOtherChannelAlone(t *testing.T) {
	srv := useChannelLimitRedis(t)
	resetHardLimitState(t)
	soft := &Channel{Id: 51, RateLimitRPM: intPtr(1)}
	unlimited := &Channel{Id: 52}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		assert.True(t, AdmitChannelRequest(ctx, soft))
		assert.True(t, AdmitChannelRequest(ctx, unlimited))
	}
	assert.Empty(t, srv.Keys(), "admission counts nothing for a channel that is not hard-limited")
}

func TestAFullHardChannelReopensWithTheNextWindow(t *testing.T) {
	resetHardLimitState(t)
	window := channelLimitWindowIndex(time.Now())

	closeHardChannel(61, window-1)
	assert.False(t, ChannelIsHardRateLimited(61), "closed for the previous minute is open now")
	assert.Equal(t, []int{61, 62}, dropHardRateLimitedChannelIds([]int{61, 62}))

	closeHardChannel(61, window)
	if channelLimitWindowIndex(time.Now()) == window {
		assert.True(t, ChannelIsHardRateLimited(61))
		assert.Equal(t, []int{62}, dropHardRateLimitedChannelIds([]int{61, 62}))
	}

	retryAfter := ChannelRateLimitRetryAfter()
	assert.GreaterOrEqual(t, retryAfter, 1)
	assert.LessOrEqual(t, retryAfter, 60)
}
