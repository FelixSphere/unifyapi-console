/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These drive the real Distribute middleware, so what is asserted is what a
// customer receives: which channel served, or the refusal's status, header
// and body.

func useHardLimitRedis(t *testing.T) {
	t.Helper()
	prevEnabled, prevRDB := common.RedisEnabled, common.RDB
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	require.NoError(t, client.Ping(context.Background()).Err())
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled, common.RDB = prevEnabled, prevRDB
	})
	// The window is wall clock; do not let a test straddle a minute boundary.
	if model.ChannelRateLimitRetryAfter() < 3 {
		time.Sleep(4 * time.Second)
	}
}

type limitedChannel struct {
	id       int
	group    string
	priority int64
	rpm      int
	hard     bool
	settings string
}

func createLimitedChannel(t *testing.T, db *gorm.DB, modelName string, spec limitedChannel) {
	t.Helper()
	weight := uint(100)
	channel := &model.Channel{
		Id: spec.id, Type: constant.ChannelTypeOpenAI, Key: fmt.Sprintf("key-%d", spec.id),
		Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("channel-%d", spec.id),
		Weight: &weight, Models: modelName, Group: spec.group, Priority: &spec.priority,
	}
	if spec.rpm > 0 {
		channel.RateLimitRPM = &spec.rpm
	}
	if spec.hard {
		channel.OtherSettings = `{"hard_rate_limit":true}`
	}
	if spec.settings != "" {
		channel.OtherSettings = spec.settings
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: spec.group, Model: modelName, ChannelId: spec.id, Enabled: true, Priority: &spec.priority, Weight: weight}).Error)
}

// relayed counts requests that got past Distribute, which is where a request
// starts to cost anything: pre-consume, the upstream call and the consume log
// are all behind it.
var relayed atomic.Int64

type distributed struct {
	status     int
	channelId  int
	retryAfter string
	body       map[string]any
}

// distribute sends one request through Distribute as a customer in group
// GenAI and reports which channel it was handed to.
func distribute(t *testing.T, path, modelName string, pinnedChannel int) distributed {
	t.Helper()
	return distributeWithSession(t, path, modelName, pinnedChannel, "")
}

func distributeWithSession(t *testing.T, path, modelName string, pinnedChannel int, session string) distributed {
	t.Helper()
	return distributeAs(t, 77, "GenAI", path, modelName, pinnedChannel, session)
}

// distributeAs is one request by the given user with a key in the given
// token group ("auto" for a key that walks the Auto groups).
func distributeAs(t *testing.T, userId int, tokenGroup, path, modelName string, pinnedChannel int, session string) distributed {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", userId)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "GenAI")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, effectiveUsingGroup("GenAI", tokenGroup))
		common.SetContextKey(c, constant.ContextKeyTokenGroup, tokenGroup)
		if pinnedChannel > 0 {
			common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, strconv.Itoa(pinnedChannel))
		}
	})
	engine.Use(Distribute())
	engine.POST(path, func(c *gin.Context) {
		relayed.Add(1)
		c.JSON(http.StatusOK, gin.H{"served_by": common.GetContextKeyInt(c, constant.ContextKeyChannelId)})
	})

	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, modelName)))
	request.Header.Set("Content-Type", "application/json")
	if session != "" {
		request.Header.Set("X-Test-Session", session)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	out := distributed{status: recorder.Code, retryAfter: recorder.Header().Get("Retry-After")}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &out.body), recorder.Body.String())
	if served, ok := out.body["served_by"].(float64); ok {
		out.channelId = int(served)
	}
	return out
}

func assertRetryAfterIsTheRestOfTheMinute(t *testing.T, value string) {
	t.Helper()
	seconds, err := strconv.Atoi(value)
	require.NoError(t, err, "Retry-After must be a number of seconds, got %q", value)
	assert.GreaterOrEqual(t, seconds, 1)
	assert.LessOrEqual(t, seconds, 60)
}

// The measurement that asked for this change, on staging 2026-11-22: RPM 1 on
// the only channel at its priority, and every call in the minute was served.
func TestTheOnlyChannelWithAHardLimitRefusesTheRequestOverItsRPM(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9301, group: "GenAI", rpm: 1, hard: true})
	model.InitChannelCache()

	first := distribute(t, "/v1/chat/completions", "claude-fable-5", 0)
	require.Equal(t, http.StatusOK, first.status)
	require.Equal(t, 9301, first.channelId)

	second := distribute(t, "/v1/chat/completions", "claude-fable-5", 0)
	require.Equal(t, http.StatusTooManyRequests, second.status, "RPM 1 with the hard switch: the second request of the minute must not be served")
	assertRetryAfterIsTheRestOfTheMinute(t, second.retryAfter)
	errorBody, ok := second.body["error"].(map[string]any)
	require.True(t, ok, "OpenAI-shaped error body, got %v", second.body)
	assert.Equal(t, "rate_limit_error", errorBody["type"])
	assert.Equal(t, "rate_limit_exceeded", errorBody["code"])
	assert.Contains(t, errorBody["message"], "Rate limit reached for model claude-fable-5")
	assert.NotContains(t, errorBody["message"], "9301", "the refusal names the model, not our channel")
}

func TestTheRefusalIsAnthropicShapedOnTheMessagesAPI(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9311, group: "GenAI", rpm: 1, hard: true})
	model.InitChannelCache()

	require.Equal(t, http.StatusOK, distribute(t, "/v1/messages", "claude-fable-5", 0).status)

	second := distribute(t, "/v1/messages", "claude-fable-5", 0)
	require.Equal(t, http.StatusTooManyRequests, second.status)
	assertRetryAfterIsTheRestOfTheMinute(t, second.retryAfter)
	assert.Equal(t, "error", second.body["type"])
	errorBody, ok := second.body["error"].(map[string]any)
	require.True(t, ok, "Anthropic-shaped error body, got %v", second.body)
	assert.Equal(t, "rate_limit_error", errorBody["type"])
	assert.Contains(t, errorBody["message"], "Rate limit reached for model claude-fable-5")
}

// Default off: the same channel and the same limit without the switch behaves
// exactly as it did before, which is to serve every request.
func TestTheOnlyChannelWithoutTheSwitchStillServesOverItsRPM(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9321, group: "GenAI", rpm: 1})
	model.InitChannelCache()

	for i := 0; i < 4; i++ {
		got := distribute(t, "/v1/chat/completions", "claude-fable-5", 0)
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		assert.Equal(t, 9321, got.channelId)
		assert.Empty(t, got.retryAfter)
	}
}

func TestTrafficOverAHardLimitMovesToAnotherChannelWhenThereIsOne(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9331, group: "GenAI", priority: 10, rpm: 2, hard: true})
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9332, group: "GenAI", priority: 0})
	model.InitChannelCache()

	served := []int{}
	for i := 0; i < 6; i++ {
		got := distribute(t, "/v1/chat/completions", "claude-fable-5", 0)
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		served = append(served, got.channelId)
	}
	assert.Equal(t, []int{9331, 9331, 9332, 9332, 9332, 9332}, served,
		"the limited channel takes exactly its 2, the rest go to the backup, nobody is refused")
}

func TestAKeyPinnedToAFullHardChannelIsRefused(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9341, group: "GenAI", priority: 10, rpm: 1, hard: true})
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9342, group: "GenAI", priority: 0})
	model.InitChannelCache()

	first := distribute(t, "/v1/chat/completions", "claude-fable-5", 9341)
	require.Equal(t, http.StatusOK, first.status)
	require.Equal(t, 9341, first.channelId)

	second := distribute(t, "/v1/chat/completions", "claude-fable-5", 9341)
	require.Equal(t, http.StatusTooManyRequests, second.status, "pinning a key to the channel is not a way round its cap")
	assertRetryAfterIsTheRestOfTheMinute(t, second.retryAfter)

	unpinned := distribute(t, "/v1/chat/completions", "claude-fable-5", 0)
	require.Equal(t, http.StatusOK, unpinned.status)
	assert.Equal(t, 9342, unpinned.channelId, "and the pinned request counted: an ordinary key now goes to the backup")
}

// Affinity sends a session back to the channel that served it last. Before
// this change that path neither counted the request nor looked at the limit.
func TestAffinityDoesNotCarryASessionPastAHardLimit(t *testing.T) {
	db := setupDefaultUserRoutingDB(t)
	useHardLimitRedis(t)
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9351, group: "GenAI", priority: 10, rpm: 2, hard: true})
	createLimitedChannel(t, db, "claude-fable-5", limitedChannel{id: 9352, group: "GenAI", priority: 0})
	model.InitChannelCache()

	affinity := operation_setting.GetChannelAffinitySetting()
	prev := *affinity
	affinity.Enabled, affinity.SwitchOnSuccess, affinity.FailoverAfterFailures = true, false, 0
	affinity.Rules = []operation_setting.ChannelAffinityRule{{
		Name:       "hard limit test",
		ModelRegex: []string{"^claude-fable-5$"},
		KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Test-Session"}},
		TTLSeconds: 600,
	}}
	t.Cleanup(func() { *affinity = prev })
	session := fmt.Sprintf("session-%d", time.Now().UnixNano())

	served := []int{}
	for i := 0; i < 4; i++ {
		got := distributeWithSession(t, "/v1/chat/completions", "claude-fable-5", 0, session)
		require.Equal(t, http.StatusOK, got.status, "request %d", i+1)
		served = append(served, got.channelId)
	}
	assert.Equal(t, []int{9351, 9351, 9352, 9352}, served,
		"the session's own channel takes its 2 per minute, counted, and then the session is served elsewhere")
}

// A retry inside the relay that finds only full hard channels is rendered by
// the relay, from this error.
func TestTheRetryPathRefusalIsA429ThatIsNeitherRetriedNorLoggedAsAChannelError(t *testing.T) {
	for _, tc := range []struct {
		path   string
		claude bool
	}{{"/v1/chat/completions", false}, {"/v1/messages", true}} {
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, tc.path, nil)

		apiErr := ChannelHardRateLimitedError(c, "claude-fable-5")

		assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
		assertRetryAfterIsTheRestOfTheMinute(t, recorder.Header().Get("Retry-After"))
		assert.True(t, types.IsSkipRetryError(apiErr))
		assert.False(t, types.IsRecordErrorLog(apiErr), "no error row in logs for our own refusal")
		assert.False(t, types.IsChannelError(apiErr), "not a channel fault: it must not disable the channel")
		if tc.claude {
			claude := apiErr.ToClaudeError()
			assert.Equal(t, "rate_limit_error", claude.Type)
			assert.Contains(t, claude.Message, "Rate limit reached for model claude-fable-5")
		} else {
			openAI := apiErr.ToOpenAIError()
			assert.Equal(t, "rate_limit_error", openAI.Type)
			assert.Equal(t, "rate_limit_exceeded", openAI.Code)
			assert.Contains(t, openAI.Message, "Rate limit reached for model claude-fable-5")
		}
	}
}
