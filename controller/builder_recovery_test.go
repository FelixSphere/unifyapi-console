/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recoveryRequest(t *testing.T, action, body string) *httptest.ResponseRecorder {
	t.Helper()
	secret := "builder-recovery-test-secret-at-least-32-bytes"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	path := "/api/builder/v1/" + action
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n" + body))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "action", Value: action}}
	c.Request = httptest.NewRequest("POST", path, strings.NewReader(body))
	c.Request.Header.Set("X-Builder-Timestamp", timestamp)
	c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
	BuilderIntegration(c)
	return recorder
}

func TestBuilderOrphanedAccountHasStableRepairErrorAndNoSideEffects(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	oldAPI, oldWebhook, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldAPI, oldWebhook, oldPrice
	})
	setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "sk_test_fixture", "whsec_fixture", "price_fixture"
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.BuilderIdentity{}))
	program := model.PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	var customer model.PartnershipCustomer
	require.NoError(t, model.DB.Where("program_id = ?", program.Id).First(&customer).Error)
	link := model.BuilderIdentity{Subject: "orphan", UserId: 999, ProgramId: program.Id, CustomerId: customer.Id, TokenId: 12, GrantQuota: 5000000, GrantClaimedAt: 123}
	require.NoError(t, model.DB.Create(&link).Error)
	var beforeCustomers, afterCustomers int64
	require.NoError(t, model.DB.Model(&model.PartnershipCustomer{}).Count(&beforeCustomers).Error)
	for _, action := range []string{"connect", "workspace", "claim", "key", "credit-status", "checkout"} {
		t.Run(action, func(t *testing.T) {
			response := recoveryRequest(t, action, `{"subject":"orphan","program_name":"Builders","customer_name":"New Team","email":"verified@example.invalid","email_verified":true,"owner_eligible":true}`)
			require.Equal(t, 409, response.Code, response.Body.String())
			assert.JSONEq(t, `{"code":"UNIFY_ACCOUNT_REPAIR_REQUIRED"}`, response.Body.String())
		})
	}
	var saved model.BuilderIdentity
	require.NoError(t, model.DB.First(&saved, link.Id).Error)
	assert.Equal(t, link, saved)
	require.NoError(t, model.DB.Model(&model.PartnershipCustomer{}).Count(&afterCustomers).Error)
	assert.Equal(t, beforeCustomers, afterCustomers)
	response := recoveryRequest(t, "workspace", `{"subject":"never-connected","program_name":"Builders"}`)
	require.Equal(t, 200, response.Code)
	assert.JSONEq(t, `{"connected":false}`, response.Body.String())
}

func TestBuilderProvisionStorageErrorIsNotProgramUnavailable(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.BuilderIdentity{}))
	program := model.PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	require.NoError(t, model.DB.Create(&model.Option{Key: "TopupGroupRatio", Value: "corrupt"}).Error)
	response := recoveryRequest(t, "connect", `{"subject":"new","program_name":"Builders","customer_name":"New Team","email":"verified@example.invalid","email_verified":true}`)
	require.Equal(t, 503, response.Code, response.Body.String())
	assert.JSONEq(t, `{"code":"UNIFY_INTEGRATION_UNAVAILABLE"}`, response.Body.String())
}

func TestBuilderResolutionErrorSeparatesBusinessAndStorageFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{model.ErrPartnershipProgramUnavailable, 409, "UNIFY_PROGRAM_UNAVAILABLE"},
		{model.ErrPartnershipCustomerUnavailable, 409, "UNIFY_CUSTOMER_UNAVAILABLE"},
		{errors.New("database failed; sensitive detail"), 503, "UNIFY_INTEGRATION_UNAVAILABLE"},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		writeBuilderResolutionError(c, tc.err)
		assert.Equal(t, tc.status, recorder.Code)
		assert.Contains(t, recorder.Body.String(), tc.code)
		assert.NotContains(t, recorder.Body.String(), "sensitive")
	}
}

func TestBuilderRemovedCustomerConnectAndWorkspaceThroughSignedHTTP(t *testing.T) {
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = oldRedis })
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}, &model.BuilderIdentity{}, &model.Token{}, &model.Log{}, &model.TopUp{}, &model.Ability{}, &model.PricingConfigHistory{}))
	oldLogDB, oldOptions := model.LOG_DB, common.OptionMap
	model.LOG_DB = model.DB
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { model.LOG_DB = oldLogDB; common.OptionMap = oldOptions })
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	program := model.PartnershipProgram{Name: "Builders", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	retired := model.PartnershipCustomer{ProgramId: program.Id, Name: "UnifyAPI", Code: "unifyapi", Group: "UnifyAPI", Enabled: false, RemovedAt: 123}
	require.NoError(t, model.DB.Create(&retired).Error)
	// GORM applies the enabled default on insert; keep this archived fixture disabled.
	require.NoError(t, model.DB.Model(&retired).Update("enabled", false).Error)
	request := `{"subject":"healthy-new-subject","program_name":"Builders","customer_name":"UnifyAPI","email":"healthy@example.invalid","email_verified":true,"range":"30d"}`
	for i := 0; i < 2; i++ {
		connect := recoveryRequest(t, "connect", request)
		require.Equal(t, 200, connect.Code, connect.Body.String())
		assert.JSONEq(t, `{"connected":true}`, connect.Body.String())
	}
	workspace := recoveryRequest(t, "workspace", request)
	require.Equal(t, 200, workspace.Code, workspace.Body.String())
	var body struct {
		Connected bool    `json:"connected"`
		Balance   float64 `json:"balance"`
		Account   struct {
			Group string `json:"group"`
		} `json:"account"`
	}
	require.NoError(t, common.Unmarshal(workspace.Body.Bytes(), &body))
	assert.True(t, body.Connected)
	assert.Zero(t, body.Balance)
	assert.NotEqual(t, retired.Group, body.Account.Group)
	var identities, keys int64
	require.NoError(t, model.DB.Model(&model.BuilderIdentity{}).Count(&identities).Error)
	require.NoError(t, model.DB.Model(&model.Token{}).Count(&keys).Error)
	assert.EqualValues(t, 1, identities)
	assert.EqualValues(t, 1, keys)
	var unchanged model.PartnershipCustomer
	require.NoError(t, model.DB.First(&unchanged, retired.Id).Error)
	assert.False(t, unchanged.Enabled)
	assert.Equal(t, retired.Group, unchanged.Group)
	assert.EqualValues(t, 123, unchanged.RemovedAt)
}
