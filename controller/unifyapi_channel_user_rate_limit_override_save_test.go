/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The watcher that throttles an account has one call to make: PUT
// /api/channel/ with the channel's id and its whole settings string. This
// drives the real handler against a real table and the real channel cache,
// and then asks the cached channel what the throttled user may send.
func TestAPartialUpdateStoresAUserOverrideAndTheCacheServesItAtOnce(t *testing.T) {
	originalDB, originalLogDB, originalCache, originalRedis := model.DB, model.LOG_DB, common.MemoryCacheEnabled, common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}, &model.Log{}))
	model.DB, model.LOG_DB, common.MemoryCacheEnabled, common.RedisEnabled = db, db, true, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.MemoryCacheEnabled, common.RedisEnabled = originalDB, originalLogDB, originalCache, originalRedis
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	require.NoError(t, db.Create(&model.Channel{
		Id: 4411, Type: constant.ChannelTypeOpenAI, Key: "k", Name: "guarded", Status: common.ChannelStatusEnabled,
		Models: "claude-mythos-5", Group: "default", OtherSettings: `{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct"}`,
	}).Error)
	model.InitChannelCache()

	role := common.RoleRootUser
	put := func(settings string) (bool, string) {
		body, err := json.Marshal(map[string]any{"id": 4411, "settings": settings})
		require.NoError(t, err)
		gin.SetMode(gin.TestMode)
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodPut, "/api/channel/", bytes.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		ctx.Set("id", 1)
		ctx.Set("role", role)
		UpdateChannel(ctx)
		var answer struct {
			Success bool   `json:"success"`
			Message string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer), recorder.Body.String())
		return answer.Success, answer.Message
	}
	rpmFor := func(userId int) int64 {
		cached, err := model.CacheGetChannel(4411)
		require.NoError(t, err)
		limit, scope := cached.UserRateLimitFor(userId)
		require.NotNil(t, limit)
		assert.Equal(t, "b:mythos-direct", scope)
		return limit.RPM
	}

	const throttled = `{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct","user_rate_limit_overrides":{"77":{"rpm":5,"daily_requests":200}}}`
	ok, message := put(throttled)
	require.True(t, ok, message)

	var row model.Channel
	require.NoError(t, db.First(&row, 4411).Error)
	assert.Equal(t, throttled, row.OtherSettings)
	assert.Equal(t, "guarded", row.Name, "a PUT of id and settings leaves the rest of the row alone")
	assert.Equal(t, "claude-mythos-5", row.Models)
	assert.Equal(t, "k", row.Key)
	assert.EqualValues(t, 5, rpmFor(77), "no wait for the periodic sync on the instance that took the PUT")
	assert.EqualValues(t, 20, rpmFor(78))

	ok, message = put(`{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct","user_rate_limit_overrides":{"77":{}}}`)
	assert.False(t, ok, "an override that limits nothing is refused")
	assert.Contains(t, message, "user_rate_limit_overrides[77]")
	require.NoError(t, db.First(&row, 4411).Error)
	assert.Equal(t, throttled, row.OtherSettings, "and the stored throttle is untouched")

	// settings is a sensitive channel field: the caller that sets or lifts a
	// throttle needs channel sensitive-write, which a plain admin lacks.
	role = common.RoleAdminUser
	ok, _ = put(`{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct"}`)
	assert.False(t, ok, "an admin without channel sensitive-write cannot lift the throttle")
	require.NoError(t, db.First(&row, 4411).Error)
	assert.Equal(t, throttled, row.OtherSettings)
	role = common.RoleRootUser

	ok, message = put(`{"user_rate_limit":{"rpm":20},"user_limit_bucket":"mythos-direct"}`)
	require.True(t, ok, message)
	assert.EqualValues(t, 20, rpmFor(77), "removing the entry gives the user the channel's allowance back")
}
