/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCreditPoolControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Tenant{}, &model.CreditPool{}, &model.CreditPoolLot{},
		&model.TenantCreditGrant{}, &model.CreditPoolReservation{}, &model.CreditPoolReservationLot{},
	))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestGetMyPromotionalCreditsIsTenantScopedAndActiveOnly(t *testing.T) {
	db := setupCreditPoolControllerDB(t)
	firstTenant := model.Tenant{Name: "First", Slug: "first", Quota: 100}
	secondTenant := model.Tenant{Name: "Second", Slug: "second", Quota: 100}
	require.NoError(t, db.Create(&firstTenant).Error)
	require.NoError(t, db.Create(&secondTenant).Error)
	user := model.User{Username: "first-user", TenantId: firstTenant.Id, Status: 1}
	require.NoError(t, db.Create(&user).Error)
	pool := model.CreditPool{Name: "Promo", RoutingGroup: "promo", Models: "*"}
	require.NoError(t, model.CreateCreditPool(&pool))
	require.NoError(t, model.CreateTenantCreditGrant(&model.TenantCreditGrant{
		TenantId: firstTenant.Id, PoolId: pool.Id, Name: "Active", OriginalQuota: 80,
	}))
	require.NoError(t, db.Create(&model.TenantCreditGrant{
		TenantId: firstTenant.Id, PoolId: pool.Id, Name: "Expired", OriginalQuota: 50, RemainingQuota: 50,
		Status: model.CreditPoolStatusEnabled, ExpiresAt: time.Now().Add(-time.Hour).Unix(),
	}).Error)
	require.NoError(t, model.CreateTenantCreditGrant(&model.TenantCreditGrant{
		TenantId: secondTenant.Id, PoolId: pool.Id, Name: "Other tenant", OriginalQuota: 90,
	}))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/credit-pool/self", nil)
	ctx.Set("id", user.Id)
	GetMyPromotionalCredits(ctx)

	var response struct {
		Success bool `json:"success"`
		Data    struct {
			OriginalQuota  int64                     `json:"original_quota"`
			RemainingQuota int64                     `json:"remaining_quota"`
			Grants         []model.TenantCreditGrant `json:"grants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.EqualValues(t, 80, response.Data.OriginalQuota)
	assert.EqualValues(t, 80, response.Data.RemainingQuota)
	require.Len(t, response.Data.Grants, 1)
	assert.Equal(t, "Active", response.Data.Grants[0].Name)
}
