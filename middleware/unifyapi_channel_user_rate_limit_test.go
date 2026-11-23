/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package middleware

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Through the real Distribute middleware, like the hard-limit tests beside
// this file: a guarded channel is one that goes to a provider on our own key
// and carries user_rate_limit; a reseller channel carries nothing.

const guardedRPM1 = `{"user_rate_limit":{"rpm":1}}`

func TestAUserOverTheirAllowanceOnTheOnlyChannelIsRefusedAndNothingIsRelayed(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9401, group: "GenAI", settings: guardedRPM1})
	model.InitChannelCache()

	first := distributeAs(t, 501, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, first.status)
	require.Equal(t, 9401, first.channelId)

	before := relayed.Load()
	second := distributeAs(t, 501, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, second.status, "1 request per minute per user: the second must not be served")
	assert.Equal(t, before, relayed.Load(), "a refused request never reaches the relay, so nothing is pre-consumed, sent or billed")
	assertRetryAfterIsTheRestOfTheMinute(t, second.retryAfter)
	errorBody, ok := second.body["error"].(map[string]any)
	require.True(t, ok, "OpenAI-shaped error body, got %v", second.body)
	assert.Equal(t, "rate_limit_error", errorBody["type"])
	assert.Equal(t, "user_rate_limit_exceeded", errorBody["code"], "distinct from a model that is full for everyone")
	assert.Contains(t, errorBody["message"], "requests per minute allowance")

	other := distributeAs(t, 502, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, other.status, "the allowance is per user: another account is not affected")
	assert.Equal(t, 9401, other.channelId)
}

func TestTheUserRefusalIsAnthropicShapedOnTheMessagesAPI(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9411, group: "GenAI", settings: guardedRPM1})
	model.InitChannelCache()

	require.Equal(t, http.StatusOK, distributeAs(t, 511, "GenAI", "/v1/messages", "claude-mythos-5", 0, "").status)
	second := distributeAs(t, 511, "GenAI", "/v1/messages", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, second.status)
	assertRetryAfterIsTheRestOfTheMinute(t, second.retryAfter)
	assert.Equal(t, "error", second.body["type"])
	errorBody, ok := second.body["error"].(map[string]any)
	require.True(t, ok, "Anthropic-shaped error body, got %v", second.body)
	assert.Equal(t, "rate_limit_error", errorBody["type"])
	assert.Contains(t, errorBody["message"], "requests per minute allowance")
}

// The existing per-group limiter is looked up by the key's token group, so a
// key in `auto` gets the global default. This one is keyed by the user.
func TestAnAutoGroupKeyDoesNotGetAroundTheUserAllowance(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9421, group: "GenAI", settings: guardedRPM1})
	model.InitChannelCache()

	first := distributeAs(t, 521, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, first.status)

	viaAuto := distributeAs(t, 521, "auto", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, viaAuto.status, "the same user through an auto key is the same allowance")
	assert.Equal(t, "user_rate_limit_exceeded", viaAuto.body["error"].(map[string]any)["code"])

	pinned := distributeAs(t, 521, "GenAI", "/v1/chat/completions", "claude-mythos-5", 9421, "")
	require.Equal(t, http.StatusTooManyRequests, pinned.status, "and so is a key pinned to the channel")
}

// Each Mythos model is two guarded rows on the same supply. Without a shared
// bucket a user's effective allowance would be the sum.
func TestChannelsInOneBucketShareOneAllowancePerUser(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	shared := `{"user_rate_limit":{"rpm":2},"user_limit_bucket":"mythos-direct"}`
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9431, group: "GenAI", settings: shared})
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9432, group: "GenAI", settings: shared})
	model.InitChannelCache()

	for i := 0; i < 2; i++ {
		got := distributeAs(t, 531, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
	}
	third := distributeAs(t, 531, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, third.status, "2 per minute across both rows, not 2 on each")
}

func TestChannelsWithoutABucketCountSeparately(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9441, group: "GenAI", settings: guardedRPM1})
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9442, group: "GenAI", settings: guardedRPM1})
	model.InitChannelCache()

	served := map[int]int{}
	for i := 0; i < 2; i++ {
		got := distributeAs(t, 541, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		served[got.channelId]++
	}
	assert.Equal(t, map[int]int{9441: 1, 9442: 1}, served, "one on each, whichever was picked first")
	require.Equal(t, http.StatusTooManyRequests, distributeAs(t, 541, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "").status)
}

// A guarded channel at priority 3 with resellers above and below it.
//
// Selection takes the highest priority that still has a usable channel, and
// reaches a lower one only on retry. A channel a user is over their allowance
// on is not usable for that user, so it is left out BEFORE priorities are
// worked out: the request goes to the next priority down at once, with no
// failed attempt in between.
func TestAUserOverTheirAllowanceIsServedByTheNextPriorityAtOnce(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9451, group: "GenAI", priority: 3, settings: guardedRPM1})
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9452, group: "GenAI", priority: 1})
	model.InitChannelCache()

	served := []int{}
	for i := 0; i < 4; i++ {
		got := distributeAs(t, 551, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		served = append(served, got.channelId)
	}
	assert.Equal(t, []int{9451, 9452, 9452, 9452}, served,
		"the guarded channel takes this user's one request, the reseller below it takes the rest, nobody is refused")

	fresh := distributeAs(t, 552, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, fresh.status)
	assert.Equal(t, 9451, fresh.channelId, "and another user still gets the guarded channel first")
}

func TestAResellerAboveAGuardedChannelKeepsTakingTheTraffic(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9461, group: "GenAI", priority: 5})
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9462, group: "GenAI", priority: 3, settings: guardedRPM1})
	model.InitChannelCache()

	for i := 0; i < 4; i++ {
		got := distributeAs(t, 561, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		assert.Equal(t, 9461, got.channelId, "a channel without the setting is never limited per user")
	}
}

// A hard-limited channel's slots are not given back. A user who is over their
// own allowance must not be able to spend them.
func TestAUserOverTheirAllowanceDoesNotUseUpAHardChannel(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9471, group: "GenAI", rpm: 3,
		settings: `{"hard_rate_limit":true,"user_rate_limit":{"rpm":1}}`})
	model.InitChannelCache()

	require.Equal(t, http.StatusOK, distributeAs(t, 571, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "").status)
	for i := 0; i < 5; i++ {
		refused := distributeAs(t, 571, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusTooManyRequests, refused.status)
		assert.Equal(t, "user_rate_limit_exceeded", refused.body["error"].(map[string]any)["code"])
	}
	for _, user := range []int{572, 573} {
		got := distributeAs(t, user, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, got.status, "user %d: the channel still has 2 of its 3 slots", user)
	}
	full := distributeAs(t, 574, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, full.status)
	assert.Equal(t, "rate_limit_exceeded", full.body["error"].(map[string]any)["code"], "now the channel itself is full")
}
