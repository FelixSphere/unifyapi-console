/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useHardLimitRedis(t *testing.T) *miniredis.Miniredis {
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
	return srv
}

func makeChannelHardLimited(t *testing.T, db *gorm.DB, id int, rpm int) {
	t.Helper()
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", id).Updates(map[string]any{
		"rate_limit_rpm": rpm,
		"settings":       `{"hard_rate_limit":true}`,
	}).Error)
}

func hardLimitParam(tokenGroup, modelName string) *RetryParam {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	retry := 0
	return &RetryParam{Ctx: ctx, TokenGroup: tokenGroup, ModelName: modelName, RequestPath: "/v1/chat/completions", Retry: &retry}
}

// Every selection is an attempt, and every attempt on a hard channel counts:
// a request that is retried onto the same channel uses two of its slots.
func TestEachSelectionOfAHardChannelCountsAgainstItsRPM(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	srv := useHardLimitRedis(t)
	const modelName = "hard-limit-retry-model"
	createChannelSelectAutoGroupsChannel(t, db, 2301, "default", modelName)
	makeChannelHardLimited(t, db, 2301, 2)
	model.InitChannelCache()

	param := hardLimitParam("default", modelName)
	first, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.Equal(t, 2301, first.Id)

	param.IncreaseRetry()
	retried, _, err := CacheGetRandomSatisfiedChannel(param)
	require.NoError(t, err)
	require.Equal(t, 2301, retried.Id, "the retry is the second of two slots")

	param.IncreaseRetry()
	third, _, err := CacheGetRandomSatisfiedChannel(param)
	assert.Nil(t, third)
	assert.ErrorIs(t, err, model.ErrChannelHardRateLimited)

	keys := srv.Keys()
	require.Len(t, keys, 1)
	counted, err := srv.Get(keys[0])
	require.NoError(t, err)
	assert.Equal(t, "2", counted, "each admitted attempt is counted once, by admission alone")
}

func TestAnAutoKeyIsToldTheModelIsFullNotThatItHasNoChannel(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useHardLimitRedis(t)
	const modelName = "hard-limit-auto-model"
	createChannelSelectAutoGroupsChannel(t, db, 2311, "vip", modelName)
	makeChannelHardLimited(t, db, 2311, 1)
	model.InitChannelCache()

	newParam := func() *RetryParam {
		param := hardLimitParam("auto", modelName)
		common.SetContextKey(param.Ctx, constant.ContextKeyTokenAutoGroups, []string{"vip", "default"})
		return param
	}

	first, group, err := CacheGetRandomSatisfiedChannel(newParam())
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, 2311, first.Id)
	require.Equal(t, "vip", group)

	second, _, err := CacheGetRandomSatisfiedChannel(newParam())
	assert.Nil(t, second)
	assert.ErrorIs(t, err, model.ErrChannelHardRateLimited,
		"every auto group was walked and the only channel is full: that is a rate limit, not a missing channel")
}

func TestASoftChannelIsCountedAndSelectedExactlyAsBefore(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	srv := useHardLimitRedis(t)
	const modelName = "soft-limit-model"
	createChannelSelectAutoGroupsChannel(t, db, 2321, "default", modelName)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 2321).Update("rate_limit_rpm", 1).Error)
	model.InitChannelCache()

	for i := 0; i < 3; i++ {
		got, _, err := CacheGetRandomSatisfiedChannel(hardLimitParam("default", modelName))
		require.NoError(t, err)
		require.NotNil(t, got, "request %d: a soft limit never refuses", i+1)
		assert.Equal(t, 2321, got.Id)
	}
	keys := srv.Keys()
	require.Len(t, keys, 1)
	counted, err := srv.Get(keys[0])
	require.NoError(t, err)
	assert.Equal(t, "3", counted, "one count per selection, as before")
}
