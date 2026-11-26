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
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// A key fixed to a group is refused when the group has no price ("分组 X
// 已被弃用") or when its login may not use it. A rename must leave such a key
// working, and working under the new name.
func TestAFixedGroupKeyStillAuthenticatesAfterItsGroupIsRenamed(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Token{}, &model.Tenant{},
		&model.CustomerWallet{}, &model.Channel{}, &model.Ability{}, &model.PricingConfigHistory{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousCache := common.RedisEnabled, common.MemoryCacheEnabled
	previousOptions, previousRatios := common.OptionMap, ratio_setting.GroupRatio2JSONString()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.OptionMap = map[string]string{}
	// InitLogDB with no LOG_SQL_DSN only points LOG_DB at DB and sets the
	// dialect's reserved-word column quoting, which a key lookup needs.
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousType)
		common.SetLogDatabaseType(previousLogType)
		common.RedisEnabled, common.MemoryCacheEnabled = previousRedis, previousCache
		common.OptionMap = previousOptions
		_ = sqlDB.Close()
	})

	const groupPricing = `{"default":1,"friend solo":0.9}`
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: groupPricing}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupPricing))
	login := &model.User{Username: "gade", Password: "not-a-real-password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "friend solo", AffCode: "gade", AuthVersion: 1}
	require.NoError(t, db.Create(login).Error)
	const key = "fixedgroupkeyfixedgroupkeyfixedgroupkeyfixedgrou"
	require.NoError(t, db.Create(&model.Token{UserId: login.Id, Key: key, Name: "fixed", Status: common.TokenStatusEnabled,
		ExpiredTime: -1, UnlimitedQuota: true, Group: "friend solo"}).Error)

	gin.SetMode(gin.TestMode)
	call := func() (status int, usingGroup string) {
		recorder := httptest.NewRecorder()
		_, engine := gin.CreateTestContext(recorder)
		engine.GET("/v1/models", TokenAuth(), func(c *gin.Context) {
			usingGroup = common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
			c.Status(http.StatusOK)
		})
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer sk-"+key)
		engine.ServeHTTP(recorder, request)
		return recorder.Code, usingGroup
	}

	status, group := call()
	require.Equal(t, http.StatusOK, status, "precondition: the key works before the rename")
	require.Equal(t, "friend solo", group)

	_, err = model.RenamePricingGroup("friend solo", "Gade Union", "root")
	require.NoError(t, err)

	status, group = call()
	assert.Equal(t, http.StatusOK, status, "the key is neither deprecated nor forbidden")
	assert.Equal(t, "Gade Union", group, "and its requests are priced and routed as the renamed group")
}
