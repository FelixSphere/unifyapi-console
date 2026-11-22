/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const accessTierRoute = "/api/pricing/group/access-tier"

type accessTierRouteBody struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		Group                string   `json:"group"`
		AccessTier           bool     `json:"access_tier"`
		Changed              bool     `json:"changed"`
		ReleasedWalletTenant int      `json:"released_wallet_tenant"`
		Groups               []string `json:"groups"`
	} `json:"data"`
}

// The mark decides whether logins share money, so it is the operator's alone:
// the whole API router is mounted here, and the route is called the way the
// console's own page calls it.
func TestOnlyRootMayMarkAGroupAsAnAccessTier(t *testing.T) {
	engine := setupAccessTierRouteTest(t)
	root := createChannelKeyRouteUser(t, "tier-root", common.RoleRootUser, "")
	admin := createChannelKeyRouteUser(t, "tier-admin", common.RoleAdminUser, "")
	customer := createChannelKeyRouteUser(t, "tier-customer", common.RoleCommonUser, "")
	rootSession := signInForChannelKeyRoute(t, root)

	refused := []struct {
		name       string
		credential string
		wantStatus int
	}{
		{"nobody signed in", "", http.StatusUnauthorized},
		{"an admin who is not root", signInForChannelKeyRoute(t, admin), http.StatusForbidden},
		{"a customer", signInForChannelKeyRoute(t, customer), http.StatusForbidden},
	}
	for _, test := range refused {
		t.Run("refused: "+test.name, func(t *testing.T) {
			write := callAccessTier(engine, http.MethodPost, test.credential, `{"group":"Vip User","access_tier":true}`)
			assert.Equal(t, test.wantStatus, write.Code, write.Body.String())
			read := callAccessTier(engine, http.MethodGet, test.credential, "")
			assert.Equal(t, test.wantStatus, read.Code, read.Body.String())
		})
	}
	groups, err := model.AccessTierGroups()
	require.NoError(t, err)
	require.Empty(t, groups, "none of the refused calls marked anything")

	t.Run("no group is a tier until one is marked", func(t *testing.T) {
		body := decodeAccessTier(t, callAccessTier(engine, http.MethodGet, rootSession, ""))
		assert.True(t, body.Success)
		assert.NotNil(t, body.Data.Groups)
		assert.Empty(t, body.Data.Groups)
	})

	t.Run("a request that does not say which way is not read as un-mark", func(t *testing.T) {
		response := callAccessTier(engine, http.MethodPost, rootSession, `{"group":"Vip User"}`)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "access_tier is required")
	})

	t.Run("root marks the group", func(t *testing.T) {
		body := decodeAccessTier(t, callAccessTier(engine, http.MethodPost, rootSession, `{"group":"Vip User","access_tier":true}`))
		require.True(t, body.Success, body.Message)
		assert.True(t, body.Data.Changed)
		assert.Equal(t, []string{"Vip User"}, body.Data.Groups)
		assert.Contains(t, body.Message, "its logins keep their own wallets")

		listed := decodeAccessTier(t, callAccessTier(engine, http.MethodGet, rootSession, ""))
		assert.Equal(t, []string{"Vip User"}, listed.Data.Groups)
	})

	t.Run("the change is in the audit log, once", func(t *testing.T) {
		callAccessTier(engine, http.MethodPost, rootSession, `{"group":"Vip User","access_tier":true}`)
		var audits []model.Log
		require.NoError(t, model.LOG_DB.Where("user_id = ? AND type = ? AND other LIKE ?",
			root.Id, model.LogTypeManage, "%pricing.group_access_tier_mark%").Find(&audits).Error)
		require.Len(t, audits, 1, "marking a marked group changes nothing and records nothing")
		assert.Contains(t, audits[0].Content, "Marked pricing group Vip User as an access tier")
	})

	t.Run("a refusal is an answer the page can show, not an HTTP error", func(t *testing.T) {
		wallet := &model.Tenant{Name: "Acme", Slug: "customer-acme", Status: model.TenantStatusEnabled, Quota: 5_000, Group: "Acme"}
		require.NoError(t, model.DB.Create(wallet).Error)
		require.NoError(t, model.DB.Create(&model.CustomerWallet{Group: "Acme", TenantId: wallet.Id}).Error)
		for _, name := range []string{"alice", "bob"} {
			require.NoError(t, model.DB.Create(&model.User{Username: name, Password: "password-placeholder", Role: common.RoleCommonUser,
				Status: common.UserStatusEnabled, Group: "Acme", TenantId: wallet.Id, AffCode: "aff-" + name}).Error)
		}

		response := callAccessTier(engine, http.MethodPost, rootSession, `{"group":"Acme","access_tier":true}`)

		assert.Equal(t, http.StatusOK, response.Code)
		body := decodeAccessTier(t, response)
		assert.False(t, body.Success)
		assert.Contains(t, body.Message, fmt.Sprintf("tenant %d", wallet.Id))
		assert.Contains(t, body.Message, "alice, bob")
	})

	t.Run("the list cannot be written through the settings API instead", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"AccessTierGroups","value":"[\"Acme\"]"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+rootSession)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		assert.Contains(t, response.Body.String(), `"success":false`, response.Body.String())
		groups, err := model.AccessTierGroups()
		require.NoError(t, err)
		assert.Equal(t, []string{"Vip User"}, groups)
	})

	t.Run("root removes the mark", func(t *testing.T) {
		body := decodeAccessTier(t, callAccessTier(engine, http.MethodPost, rootSession, `{"group":"Vip User","access_tier":false}`))
		require.True(t, body.Success, body.Message)
		assert.True(t, body.Data.Changed)
		assert.Empty(t, body.Data.Groups)
		var audits int64
		require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("user_id = ? AND type = ? AND other LIKE ?",
			root.Id, model.LogTypeManage, "%pricing.group_access_tier_unmark%").Count(&audits).Error)
		assert.EqualValues(t, 1, audits)
	})
}

func setupAccessTierRouteTest(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousSecret := common.RedisEnabled, common.SessionSecret
	previousOptions := common.OptionMap

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Log{}, &model.Option{},
		&model.Tenant{}, &model.CustomerWallet{}, &model.PartnershipProgram{}, &model.PartnershipCustomer{},
		&model.PricingConfigHistory{}))
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "access-tier-route-test-secret"
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled, common.SessionSecret = previousRedis, previousSecret
		common.OptionMap = previousOptions
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"default":1,"Vip User":1,"Acme":0.9}`}).Error)

	engine := gin.New()
	engine.Use(gin.Recovery())
	SetApiRouter(engine)
	return engine
}

func callAccessTier(engine *gin.Engine, method, credential, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, accessTierRoute, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func decodeAccessTier(t *testing.T, response *httptest.ResponseRecorder) accessTierRouteBody {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body accessTierRouteBody
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body), response.Body.String())
	return body
}
