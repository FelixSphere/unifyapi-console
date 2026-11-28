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

// Through the real Distribute middleware: one account is throttled on a
// channel by user_rate_limit_overrides, and gets exactly what an account over
// user_rate_limit gets -- nothing in the answer says it was singled out.
func TestAnOverriddenUserIsRefusedLikeAnyUserOverTheirAllowanceAndOthersPass(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9481, group: "GenAI",
		settings: `{"user_rate_limit":{"rpm":20},"user_rate_limit_overrides":{"581":{"rpm":2}}}`})
	model.InitChannelCache()

	for i := 0; i < 2; i++ {
		require.Equal(t, http.StatusOK, distributeAs(t, 581, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "").status)
	}
	before := relayed.Load()
	third := distributeAs(t, 581, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusTooManyRequests, third.status, "the override is 2 a minute")
	assert.Equal(t, before, relayed.Load(), "nothing is relayed, so nothing is billed")
	assertRetryAfterIsTheRestOfTheMinute(t, third.retryAfter)
	errorBody, ok := third.body["error"].(map[string]any)
	require.True(t, ok, "OpenAI-shaped error body, got %v", third.body)
	assert.Equal(t, "rate_limit_error", errorBody["type"])
	assert.Equal(t, "user_rate_limit_exceeded", errorBody["code"])
	assert.Contains(t, errorBody["message"], "Rate limit reached for model claude-mythos-5: your account has used its requests per minute allowance for this model. Please retry in ")
	assert.NotContains(t, errorBody["message"], "override")

	for i := 0; i < 5; i++ {
		other := distributeAs(t, 582, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
		require.Equal(t, http.StatusOK, other.status, "another account is still on the channel's 20 a minute")
		assert.Equal(t, 9481, other.channelId)
	}
}

func TestAnOverriddenUserIsServedByAnotherChannelWhenThereIsOne(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9491, group: "GenAI", priority: 10,
		settings: `{"user_rate_limit_overrides":{"591":{"rpm":1}}}`})
	createLimitedChannel(t, db, "claude-mythos-5", limitedChannel{id: 9492, group: "GenAI", priority: 0})
	model.InitChannelCache()

	first := distributeAs(t, 591, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, first.status)
	require.Equal(t, 9491, first.channelId)
	second := distributeAs(t, 591, "GenAI", "/v1/chat/completions", "claude-mythos-5", 0, "")
	require.Equal(t, http.StatusOK, second.status, "the throttle is on the channel that carries it, not on the model")
	assert.Equal(t, 9492, second.channelId)
}
