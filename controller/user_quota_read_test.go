/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * What an operator is SHOWN must equal what the relay will SPEND.
 *
 * Every login created through the admin API gets its own tenant, and from then
 * on its balance lives in `tenants.quota` while `users.quota` stays at 0. The
 * admin read paths returned the User row verbatim, so a funded account showed
 * as empty and every quota an operator set looked silently discarded -- the
 * number on screen never moved. The money was never lost, and GetSelf was
 * always right, which is why customers saw a correct balance and operators did
 * not.
 *
 * model/billing_entity_test.go covers the resolver. These tests assert the
 * HANDLERS' JSON, because that is where the defect actually lived: a resolver
 * nobody calls is exactly the bug.
 */
package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fundedTenantLogin is the shape that broke: balance on the tenant, 0 on the
// user's own column.
func fundedTenantLogin(t *testing.T, username string, walletQuota int) *model.User {
	t.Helper()
	tenant := model.Tenant{Name: username, Slug: username, Quota: walletQuota}
	require.NoError(t, model.DB.Create(&tenant).Error)
	user := model.User{
		Username: username,
		AffCode:  "aff-" + username,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		TenantId: tenant.Id,
		Quota:    0,
	}
	require.NoError(t, model.DB.Create(&user).Error)
	return &user
}

func setupAdminUserReadTest(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Tenant{}))

	previousDB := model.DB
	previousRedis := common.RedisEnabled
	model.DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
	})
}

// rootContext is an operator looking at the screen.
func rootContext(t *testing.T, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("id", 0)
	return ctx, recorder
}

func decodeUsers(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope))
	require.True(t, envelope.Success)
	return envelope.Data.Items
}

func quotaOf(t *testing.T, users []map[string]any, username string) float64 {
	t.Helper()
	for _, user := range users {
		if user["username"] == username {
			quota, ok := user["quota"].(float64)
			require.True(t, ok, "quota missing for %s", username)
			return quota
		}
	}
	t.Fatalf("%s not in the response", username)
	return 0
}

func TestAdminUserListShowsTheSpendableBalance(t *testing.T) {
	setupAdminUserReadTest(t)
	fundedTenantLogin(t, "funded", 50_000_000)
	// A tenantless login keeps its own column; both must be right at once.
	require.NoError(t, model.DB.Create(&model.User{
		Username: "solo", AffCode: "aff-solo", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TenantId: 0, Quota: 777,
	}).Error)

	ctx, recorder := rootContext(t, http.MethodGet, "/api/user/?p=1&page_size=20")
	GetAllUsers(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	users := decodeUsers(t, recorder.Body.Bytes())
	assert.Equal(t, float64(50_000_000), quotaOf(t, users, "funded"),
		"the list showed users.quota (0) instead of the tenant wallet")
	assert.Equal(t, float64(777), quotaOf(t, users, "solo"))
}

func TestAdminUserSearchShowsTheSpendableBalance(t *testing.T) {
	setupAdminUserReadTest(t)
	fundedTenantLogin(t, "funded", 12_345)

	ctx, recorder := rootContext(t, http.MethodGet, "/api/user/search?keyword=funded")
	SearchUsers(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	users := decodeUsers(t, recorder.Body.Bytes())
	assert.Equal(t, float64(12_345), quotaOf(t, users, "funded"))
}

func TestAdminSingleUserReadShowsTheSpendableBalance(t *testing.T) {
	setupAdminUserReadTest(t)
	user := fundedTenantLogin(t, "funded", 9_000_000)

	ctx, recorder := rootContext(t, http.MethodGet, "/api/user/1")
	ctx.Params = gin.Params{{Key: "id", Value: "1"}}
	GetUser(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.EqualValues(t, user.Id, envelope.Data["id"])
	assert.Equal(t, float64(9_000_000), envelope.Data["quota"],
		"the edit screen showed users.quota (0), so a set quota looked discarded")
}

// --- surfaces that were already correct, pinned so they stay correct ---
//
// GetSelf and the OpenAI-compatible billing endpoint resolved the wallet all
// along, which is the only reason customers saw a real balance while operators
// saw 0. Neither had a test. A regression in either is worse than the operator
// bug was: it faces customers and SDKs.

func TestSelfReadShowsTheSpendableBalance(t *testing.T) {
	setupAdminUserReadTest(t)
	user := fundedTenantLogin(t, "funded", 4_200_000)

	shown := buildSelfUserData(user)
	assert.Equal(t, 4_200_000, shown["quota"],
		"a customer must see the tenant wallet, not their own stale column")
}

func TestOpenAIBillingSubscriptionShowsTheSpendableBalance(t *testing.T) {
	setupAdminUserReadTest(t)
	user := fundedTenantLogin(t, "funded", 5_000_000)

	previousDisplayTokenStat := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = false // report the wallet, not one token
	t.Cleanup(func() { common.DisplayTokenStatEnabled = previousDisplayTokenStat })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/dashboard/billing/subscription", nil)
	ctx.Set("id", user.Id)
	GetSubscription(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var subscription struct {
		HardLimitUSD float64 `json:"hard_limit_usd"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &subscription))
	// remain + used, converted at QuotaPerUnit; used is 0 here.
	assert.InDelta(t, 5_000_000/common.QuotaPerUnit, subscription.HardLimitUSD, 1e-9,
		"an SDK reading the OpenAI-compatible endpoint must see the spendable balance")
}

// --- the write side: every mode must land on the wallet ---
//
// Manually verified 0 -> 25M -> 30M -> 20M through the API; pinned here so a
// future refactor cannot quietly send one mode to the wrong column. A mode
// that writes users.quota would look like it worked and change nothing
// spendable.

func TestEveryAdminQuotaModeLandsOnTheWallet(t *testing.T) {
	setupAdminUserReadTest(t)
	user := fundedTenantLogin(t, "funded", 0)

	walletOf := func() int {
		var tenant model.Tenant
		require.NoError(t, model.DB.First(&tenant, user.TenantId).Error)
		return tenant.Quota
	}
	ownColumnOf := func() int {
		var raw model.User
		require.NoError(t, model.DB.First(&raw, user.Id).Error)
		return raw.Quota
	}

	require.NoError(t, model.SetUserQuota(user.Id, 25_000_000))
	assert.Equal(t, 25_000_000, walletOf(), "override must set the wallet")

	require.NoError(t, model.IncreaseUserQuota(user.Id, 5_000_000, true))
	assert.Equal(t, 30_000_000, walletOf(), "add must credit the wallet")

	require.NoError(t, model.DecreaseUserQuota(user.Id, 10_000_000, true))
	assert.Equal(t, 20_000_000, walletOf(), "subtract must debit the wallet")

	assert.Equal(t, 0, ownColumnOf(),
		"users.quota must stay untouched; it is not the balance for a tenant login")

	spendable, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 20_000_000, spendable, "what is spent must equal what was set")
}

// TestAdminSubtractCanDriveABalanceNegative pins CURRENT behaviour, which is
// inconsistent and is flagged for a product decision rather than changed here.
//
// SetUserQuota refuses a negative value outright ("quota cannot be negative"),
// and the relay's spend path uses tryDecreaseUserQuotaWithTx with
// `WHERE quota >= ?` so consumption can never overdraw. But the admin subtract
// path applies an unguarded `quota - N`, so an operator can push a wallet to
// -999,999,999 and the API reports success. Two writers of one field with
// opposite rules.
//
// Whether a negative balance is legitimate (a clawback or a debt) is a business
// call. This test exists so the answer cannot change by accident: if a floor is
// added, this test should fail and be replaced by one asserting the refusal.
func TestAdminSubtractCanDriveABalanceNegative(t *testing.T) {
	setupAdminUserReadTest(t)
	user := fundedTenantLogin(t, "funded", 1_000)

	require.NoError(t, model.DecreaseUserQuota(user.Id, 999_999_999, true))

	spendable, err := model.GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 1_000-999_999_999, spendable,
		"admin subtract is unguarded today; see the comment before changing this")

	// The asymmetry, asserted so the inconsistency is visible in one place.
	assert.Error(t, model.SetUserQuota(user.Id, -1),
		"override refuses what subtract will happily produce")
}
