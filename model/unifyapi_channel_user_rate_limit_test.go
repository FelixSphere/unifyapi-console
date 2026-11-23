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
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// userLimitClock pins the limiter's clock so a test can sit in the middle of a
// minute or step over midnight.
func userLimitClock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	now := at
	prev := channelUserLimitNow
	channelUserLimitNow = func() time.Time { return now }
	t.Cleanup(func() {
		channelUserLimitNow = prev
		userLimitLocal.Lock()
		userLimitLocal.values = nil
		userLimitLocal.Unlock()
		userLimitRedisWarnedAt.Store(0)
	})
	return &now
}

var userLimitNoon = time.Date(2026, 11, 22, 12, 0, 20, 0, time.UTC)

func guarded(id int, settings string) *Channel {
	return &Channel{Id: id, OtherSettings: settings}
}

func TestAChannelHasNoUserLimitUnlessItsSettingsCarryOne(t *testing.T) {
	for _, settings := range []string{``, `{}`, `{"hard_rate_limit":true}`, `{"user_rate_limit":{}}`, `{"user_rate_limit":{"rpm":0}}`, `{"user_rate_limit":{"rpm":-5}}`, `not json user_rate_limit`} {
		limit, _ := guarded(1, settings).UserRateLimit()
		assert.Nil(t, limit, settings)
	}
	limit, scope := guarded(7, `{"user_rate_limit":{"daily_tokens":10}}`).UserRateLimit()
	require.NotNil(t, limit)
	assert.Equal(t, "c:7", scope)
	_, scope = guarded(7, `{"user_rate_limit":{"rpm":1},"user_limit_bucket":"mythos"}`).UserRateLimit()
	assert.Equal(t, "b:mythos", scope)
}

func TestAChannelWithoutAUserLimitCountsNothing(t *testing.T) {
	srv := useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	plain := &Channel{Id: 1, RateLimitRPM: intPtr(1)}
	withCachedChannels(t, plain)

	for i := 0; i < 5; i++ {
		assert.Nil(t, AdmitChannelUserRequest(ctx, 9, plain))
	}
	RecordChannelUserTokens(ctx, 9, 1, 5000)
	ReleaseChannelUserRequest(ctx, 9, plain)
	assert.Empty(t, srv.Keys(), "a channel without user_rate_limit never touches a per-user counter")
}

func TestRequestsPerMinuteAreExactAndARefusalIsNotCounted(t *testing.T) {
	srv := useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(2, `{"user_rate_limit":{"rpm":2,"daily_requests":100}}`)

	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	for i := 0; i < 10; i++ {
		refusal := AdmitChannelUserRequest(ctx, 9, ch)
		require.NotNil(t, refusal)
		assert.Equal(t, "requests per minute", refusal.Limit)
		assert.Equal(t, 40, refusal.RetryAfter, "12:00:20 -> 12:01:00")
		assert.False(t, refusal.Daily)
	}
	day, err := srv.Get("chUserLimit:rpd:c:2:9:20261122")
	require.NoError(t, err)
	assert.Equal(t, "2", day, "ten refused calls in a closed minute did not use up the day")

	assert.Nil(t, AdmitChannelUserRequest(ctx, 10, ch), "another user has their own allowance")
}

func TestTheDailyAllowanceIsAUTCDayAndComesBackAtMidnightUTC(t *testing.T) {
	useChannelLimitRedis(t)
	now := userLimitClock(t, time.Date(2026, 11, 22, 23, 58, 0, 0, time.UTC))
	ctx := context.Background()
	ch := guarded(3, `{"user_rate_limit":{"daily_requests":2}}`)

	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	*now = now.Add(time.Minute)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))

	refusal := AdmitChannelUserRequest(ctx, 9, ch)
	require.NotNil(t, refusal, "the day is used up, in a different minute from the first request")
	assert.Equal(t, "requests per day", refusal.Limit)
	assert.True(t, refusal.Daily)
	assert.Equal(t, 60, refusal.RetryAfter, "23:59:00 UTC -> 00:00:00 UTC")

	*now = time.Date(2026, 11, 23, 0, 0, 1, 0, time.UTC)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "a new UTC day is a new allowance")
}

func TestTokenAllowancesRefuseOnceTheRecordedTotalReachesThem(t *testing.T) {
	useChannelLimitRedis(t)
	now := userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(4, `{"user_rate_limit":{"tpm":1000,"daily_tokens":1500}}`)
	withCachedChannels(t, ch)

	require.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	RecordChannelUserTokens(ctx, 9, 4, 600)
	require.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "600 of 1000 this minute")
	RecordChannelUserTokens(ctx, 9, 4, 600)

	refusal := AdmitChannelUserRequest(ctx, 9, ch)
	require.NotNil(t, refusal)
	assert.Equal(t, "tokens per minute", refusal.Limit)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 10, ch), "another user's tokens are their own")

	*now = now.Add(time.Minute)
	require.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "next minute: 1200 of 1500 today")
	RecordChannelUserTokens(ctx, 9, 4, 400)

	*now = now.Add(time.Minute)
	refusal = AdmitChannelUserRequest(ctx, 9, ch)
	require.NotNil(t, refusal)
	assert.Equal(t, "tokens per day", refusal.Limit)
	assert.True(t, refusal.Daily)
	assert.Equal(t, secondsToNextUTCDay(*now), refusal.RetryAfter)
}

func TestChannelsInOneBucketShareAUsersCount(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	a := guarded(261, `{"user_rate_limit":{"rpm":1},"user_limit_bucket":"mythos"}`)
	b := guarded(274, `{"user_rate_limit":{"rpm":1},"user_limit_bucket":"mythos"}`)
	alone := guarded(300, `{"user_rate_limit":{"rpm":1}}`)

	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, a))
	assert.NotNil(t, AdmitChannelUserRequest(ctx, 9, b), "the same allowance under another row")
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, alone), "a channel outside the bucket counts by itself")
}

func TestAReleasedRequestGivesTheSlotBack(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(5, `{"user_rate_limit":{"rpm":1,"daily_requests":1}}`)

	require.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	ReleaseChannelUserRequest(ctx, 9, ch)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "the channel was full, the request was not sent, the user keeps the slot")
	assert.NotNil(t, AdmitChannelUserRequest(ctx, 9, ch))
}

func TestWithoutRedisTheUserAllowanceIsKeptInTheProcess(t *testing.T) {
	prev := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = prev })
	now := userLimitClock(t, time.Date(2026, 11, 22, 23, 59, 30, 0, time.UTC))
	ctx := context.Background()
	ch := guarded(6, `{"user_rate_limit":{"rpm":1,"daily_tokens":100}}`)
	withCachedChannels(t, ch)

	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	refusal := AdmitChannelUserRequest(ctx, 9, ch)
	require.NotNil(t, refusal)
	assert.Equal(t, "requests per minute", refusal.Limit)

	RecordChannelUserTokens(ctx, 9, 6, 100)
	refusal = AdmitChannelUserRequest(ctx, 9, ch)
	require.NotNil(t, refusal)
	assert.Equal(t, "tokens per day", refusal.Limit)

	*now = now.Add(time.Minute)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "past midnight UTC both windows are new")
}

func TestARedisFailureAdmitsRatherThanRefusingEveryone(t *testing.T) {
	srv := useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(8, `{"user_rate_limit":{"rpm":1}}`)

	require.Nil(t, AdmitChannelUserRequest(ctx, 9, ch))
	require.NotNil(t, AdmitChannelUserRequest(ctx, 9, ch))
	srv.Close()
	assert.Nil(t, AdmitChannelUserRequest(ctx, 9, ch), "Redis is down: the request is admitted, and the failure is logged")
}

// A refused save must be refused before anything is written: ValidateSettings
// is what controller.validateChannel runs ahead of the insert or update.
func TestAMalformedUserLimitIsRefusedWhenTheChannelIsSaved(t *testing.T) {
	for _, settings := range []string{
		`{"user_rate_limit":{"rpm":-1}}`,
		`{"user_rate_limit":{"daily_tokens":-20}}`,
		`{"user_rate_limit":{"rpm":"20"}}`,
		`{"user_rate_limit":{"rpm":1.5}}`,
		`{"user_rate_limit":[1,2]}`,
		`{"user_rate_limit":{"rpm":1}`,
		`{"user_rate_limit":{"rpm":1},"user_limit_bucket":"has space"}`,
		`{"user_rate_limit":{"rpm":1},"user_limit_bucket":"a:b"}`,
	} {
		assert.Error(t, (&Channel{Id: 1, OtherSettings: settings}).ValidateSettings(), settings)
	}
	for _, settings := range []string{
		``, `{}`,
		`{"user_rate_limit":{"rpm":20,"tpm":400000,"daily_requests":2000,"daily_tokens":20000000},"user_limit_bucket":"mythos-direct"}`,
		`{"user_rate_limit":{"rpm":0}}`,
	} {
		assert.NoError(t, (&Channel{Id: 1, OtherSettings: settings}).ValidateSettings(), settings)
	}
}
