/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

// What an operator is told about a customer price save has to be what
// happened to the stored table.
//
// On staging, 2026-11-22, PUT /api/option/ answered success=false for a
// GroupModelDiscount table that still named a delisted model, and stored it
// anyway; the prices in it started billing with the next sync. The first test
// is that request. The second is why refusing is enough: the Customer model
// prices editor never sends the whole table, so a stale entry in the stored
// row does not stand between an operator and an unrelated edit.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const storedCustomerPricesNamingADelistedModel = `{"GenAI":{"a-model-we-delisted":0.9,"claude-opus-5":1}}`

// bootWithStoredCustomerPrices stores the table and loads it the way a boot
// does, so memory holds what a running server would hold: the live contract,
// without the stale entry.
func bootWithStoredCustomerPrices(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.PricingConfigHistory{}))

	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousOptions := common.OptionMap
	previousGroups := ratio_setting.GroupRatio2JSONString()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMap = previousOptions
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`))
	})

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"GenAI":1}`))
	require.NoError(t, model.DB.Create(&model.Option{
		Key: "GroupModelDiscount", Value: storedCustomerPricesNamingADelistedModel,
	}).Error)
	model.InitOptionMap()
}

func storedCustomerPrices(t *testing.T) string {
	t.Helper()
	var option model.Option
	require.NoError(t, model.DB.Where(map[string]any{"key": "GroupModelDiscount"}).First(&option).Error)
	return option.Value
}

func putJSON(t *testing.T, handler gin.HandlerFunc, path, body string) (success bool, message string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("username", "operator")
	handler(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload.Success, payload.Message
}

func TestTheOptionAPIDoesNotStoreACustomerPriceTableItRefuses(t *testing.T) {
	bootWithStoredCustomerPrices(t)

	// The stored table with one multiplier changed from 1 to 0.9.
	submitted, err := common.Marshal(map[string]string{
		"key":   "GroupModelDiscount",
		"value": `{"GenAI":{"a-model-we-delisted":0.9,"claude-opus-5":0.9}}`,
	})
	require.NoError(t, err)
	success, message := putJSON(t, UpdateOption, "/api/option/", string(submitted))

	require.False(t, success, "a table naming a model with no catalogue price is refused")
	assert.Contains(t, message, "GenAI / a-model-we-delisted")
	assert.Equal(t, storedCustomerPricesNamingADelistedModel, storedCustomerPrices(t),
		"success=false must mean the stored table is unchanged")

	// The next read of the options table, which is where the refused price
	// used to arrive.
	model.InitOptionMap()
	ratio, ok := ratio_setting.GetGroupModelDiscount("GenAI", "claude-opus-5")
	require.True(t, ok)
	assert.InDelta(t, 1.0, ratio, 1e-12, "a refused price must never start billing")
}

func TestAStaleStoredEntryDoesNotBlockSavingOneCustomersPrices(t *testing.T) {
	bootWithStoredCustomerPrices(t)

	success, message := putJSON(t, UpdateGroupModelPricing, "/api/pricing/group_model",
		`{"group":"GenAI","discounts":{"claude-opus-5":0.9}}`)

	require.True(t, success, message)
	assert.JSONEq(t, `{"GenAI":{"claude-opus-5":0.9}}`, storedCustomerPrices(t),
		"the save stores the table in force, which no longer carries the delisted model")
	ratio, ok := ratio_setting.GetGroupModelDiscount("GenAI", "claude-opus-5")
	require.True(t, ok)
	assert.InDelta(t, 0.9, ratio, 1e-12)
}
