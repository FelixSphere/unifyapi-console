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
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// user_rate_limit_overrides: one account held to its own allowance on a
// channel, while every other account keeps the channel's.

func admitTimes(ctx context.Context, userId int, ch *Channel, times int) (admitted int, last *ChannelUserLimitRefusal) {
	for i := 0; i < times; i++ {
		if refusal := AdmitChannelUserRequest(ctx, userId, ch); refusal != nil {
			last = refusal
		} else {
			admitted++
		}
	}
	return admitted, last
}

func TestAnOverriddenUserIsHeldToTheOverrideAndNobodyElseIs(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(261, `{"user_rate_limit":{"rpm":5},"user_rate_limit_overrides":{"77":{"rpm":2}}}`)

	admitted, refusal := admitTimes(ctx, 77, ch, 4)
	assert.Equal(t, 2, admitted, "user 77 is admitted against the override, 2 a minute")
	require.NotNil(t, refusal)
	assert.Equal(t, ChannelUserLimitRefusal{Limit: "requests per minute", RetryAfter: 40}, *refusal,
		"the same refusal anyone over user_rate_limit gets")

	admitted, _ = admitTimes(ctx, 78, ch, 6)
	assert.Equal(t, 5, admitted, "user 78 has no entry and keeps the channel's 5 a minute")
}

func TestRemovingTheOverrideRestoresTheChannelAllowance(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(261, `{"user_rate_limit":{"rpm":5},"user_rate_limit_overrides":{"77":{"rpm":2}}}`)

	admitted, _ := admitTimes(ctx, 77, ch, 3)
	require.Equal(t, 2, admitted)

	ch.OtherSettings = `{"user_rate_limit":{"rpm":5}}`
	admitted, _ = admitTimes(ctx, 77, ch, 4)
	assert.Equal(t, 3, admitted, "the count carries over: 2 already sent this minute, 3 left of the channel's 5")
}

func TestAnOverrideAppliesOnAChannelWithNoLimitOfItsOwn(t *testing.T) {
	srv := useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(300, `{"user_rate_limit_overrides":{"77":{"rpm":1}}}`)

	admitted, _ := admitTimes(ctx, 78, ch, 5)
	assert.Equal(t, 5, admitted, "nobody else is limited")
	assert.Empty(t, srv.Keys(), "and nothing is counted for them")

	admitted, _ = admitTimes(ctx, 77, ch, 3)
	assert.Equal(t, 1, admitted)
}

// The operator's case: 5 requests a minute and 200 a day for one account.
func TestAnOverrideCanCapBothTheMinuteAndTheDay(t *testing.T) {
	useChannelLimitRedis(t)
	now := userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(261, `{"user_rate_limit":{"rpm":20},"user_rate_limit_overrides":{"77":{"rpm":5,"daily_requests":200}}}`)

	total := 0
	for minute := 0; minute < 41; minute++ {
		admitted, refusal := admitTimes(ctx, 77, ch, 6)
		total += admitted
		switch {
		case minute < 39:
			require.Equal(t, 5, admitted, "minute %d", minute)
			require.NotNil(t, refusal)
			assert.Equal(t, "requests per minute", refusal.Limit)
		case minute == 39:
			require.Equal(t, 5, admitted, "the last 5 of the day")
			require.NotNil(t, refusal)
			assert.Equal(t, "requests per day", refusal.Limit, "over both: the longer wait is the one reported")
		default:
			require.Zero(t, admitted, "the day is used up")
			assert.Equal(t, "requests per day", refusal.Limit)
			assert.True(t, refusal.Daily)
		}
		*now = now.Add(time.Minute)
	}
	assert.Equal(t, 200, total)

	*now = time.Date(2026, 11, 23, 0, 0, 5, 0, time.UTC)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 77, ch), "a new UTC day")
}

// An override is the user's whole allowance, not a patch on the channel's: a
// field it leaves at zero is unlimited for that user, as zero is in
// user_rate_limit.
func TestAZeroFieldInAnOverrideIsUnlimitedNotInherited(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(261, `{"user_rate_limit":{"rpm":2,"daily_tokens":100},"user_rate_limit_overrides":{"77":{"rpm":0,"daily_requests":4}}}`)
	withCachedChannels(t, ch)

	RecordChannelUserTokens(ctx, 77, 261, 5000)
	RecordChannelUserTokens(ctx, 78, 261, 5000)

	admitted, refusal := admitTimes(ctx, 77, ch, 5)
	assert.Equal(t, 4, admitted, "neither the channel's rpm 2 nor its daily_tokens 100 applies to user 77")
	require.NotNil(t, refusal)
	assert.Equal(t, "requests per day", refusal.Limit)

	refusal = AdmitChannelUserRequest(ctx, 78, ch)
	require.NotNil(t, refusal, "user 78 is still on the channel's allowance")
	assert.Equal(t, "tokens per day", refusal.Limit)
}

func TestAnOverrideThatLimitsNothingLeavesTheChannelAllowanceInForce(t *testing.T) {
	for _, settings := range []string{
		`{"user_rate_limit":{"rpm":2},"user_rate_limit_overrides":{"77":{}}}`,
		`{"user_rate_limit":{"rpm":2},"user_rate_limit_overrides":{"77":{"rpm":0,"tpm":0}}}`,
		`{"user_rate_limit":{"rpm":2},"user_rate_limit_overrides":{"77":null}}`,
		`{"user_rate_limit":{"rpm":2},"user_rate_limit_overrides":{"77":{"rpm":-1}}}`,
		`{"user_rate_limit":{"rpm":2},"user_rate_limit_overrides":{"077":{"rpm":9}}}`,
	} {
		limit, scope := guarded(261, settings).UserRateLimitFor(77)
		require.NotNil(t, limit, settings)
		assert.EqualValues(t, 2, limit.RPM, settings)
		assert.Equal(t, "c:261", scope, settings)
	}
}

func TestTokensAreRecordedAgainstTheOverride(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(261, `{"user_rate_limit":{"rpm":50},"user_rate_limit_overrides":{"77":{"tpm":1000}}}`)
	withCachedChannels(t, ch)

	require.Nil(t, AdmitChannelUserRequest(ctx, 77, ch))
	RecordChannelUserTokens(ctx, 77, 261, 1000)
	RecordChannelUserTokens(ctx, 78, 261, 1000)

	refusal := AdmitChannelUserRequest(ctx, 77, ch)
	require.NotNil(t, refusal)
	assert.Equal(t, "tokens per minute", refusal.Limit)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 78, ch), "the channel has no token limit for anyone else")
}

func TestAReleasedRequestGivesAnOverriddenUserTheSlotBack(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	ch := guarded(5, `{"user_rate_limit":{"rpm":9},"user_rate_limit_overrides":{"77":{"rpm":1,"daily_requests":1}}}`)

	require.Nil(t, AdmitChannelUserRequest(ctx, 77, ch))
	ReleaseChannelUserRequest(ctx, 77, ch)
	assert.Nil(t, AdmitChannelUserRequest(ctx, 77, ch), "the request was not sent: both the minute and the day slot come back")
	assert.NotNil(t, AdmitChannelUserRequest(ctx, 77, ch))
}

// Channels in a bucket share the user's count. They do not share the
// override: it is read from the channel being admitted.
func TestInABucketTheCountIsSharedAndTheOverrideIsPerChannel(t *testing.T) {
	useChannelLimitRedis(t)
	userLimitClock(t, userLimitNoon)
	ctx := context.Background()
	const throttled = `{"user_rate_limit":{"rpm":5},"user_limit_bucket":"mythos","user_rate_limit_overrides":{"77":{"rpm":2}}}`
	a, b := guarded(261, throttled), guarded(274, throttled)
	forgotten := guarded(280, `{"user_rate_limit":{"rpm":5},"user_limit_bucket":"mythos"}`)

	_, scope := a.UserRateLimitFor(77)
	assert.Equal(t, "b:mythos", scope)

	require.Nil(t, AdmitChannelUserRequest(ctx, 77, a))
	require.Nil(t, AdmitChannelUserRequest(ctx, 77, b))
	assert.NotNil(t, AdmitChannelUserRequest(ctx, 77, a), "2 a minute across the bucket, not 2 on each channel")
	assert.NotNil(t, AdmitChannelUserRequest(ctx, 77, b))

	admitted, _ := admitTimes(ctx, 77, forgotten, 5)
	assert.Equal(t, 3, admitted, "a bucket channel without the entry admits against its own 5, of which 2 are used")
}

func TestAMalformedOverrideIsRefusedWhenTheChannelIsSaved(t *testing.T) {
	tooMany := make([]string, 0, 201)
	for id := 1; id <= 201; id++ {
		tooMany = append(tooMany, fmt.Sprintf(`"%d":{"rpm":1}`, id))
	}
	for _, settings := range []string{
		`{"user_rate_limit_overrides":{"0":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"-4":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"alice":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"077":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{" 77":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"7.0":{"rpm":1}}}`,
		`{"user_rate_limit_overrides":{"77":{"rpm":-1}}}`,
		`{"user_rate_limit_overrides":{"77":{"daily_tokens":-20}}}`,
		`{"user_rate_limit_overrides":{"77":{"rpm":"2"}}}`,
		`{"user_rate_limit_overrides":{"77":{}}}`,
		`{"user_rate_limit_overrides":{"77":{"rpm":0}}}`,
		`{"user_rate_limit_overrides":{"77":null}}`,
		`{"user_rate_limit_overrides":{"77":5}}`,
		`{"user_rate_limit_overrides":[{"77":{"rpm":1}}]}`,
		`{"user_rate_limit_overrides":{` + strings.Join(tooMany, ",") + `}}`,
	} {
		assert.Error(t, (&Channel{Id: 1, OtherSettings: settings}).ValidateSettings(), settings)
	}
	for _, settings := range []string{
		`{"user_rate_limit_overrides":{}}`,
		`{"user_rate_limit_overrides":{"77":{"rpm":5,"daily_requests":200}}}`,
		`{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos","user_rate_limit_overrides":{"77":{"tpm":1},"1203":{"daily_tokens":9}}}`,
		`{"user_rate_limit_overrides":{` + strings.Join(tooMany[:200], ",") + `}}`,
	} {
		assert.NoError(t, (&Channel{Id: 1, OtherSettings: settings}).ValidateSettings(), settings)
	}
}
