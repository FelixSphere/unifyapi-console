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

func TestBuilderAssertionRejectsTamperingExpiryAndOtherPrograms(t *testing.T) {
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "builder-test")
	body := `{"subject":"builder-user","partnership_code":"builder-test"}`
	for _, tt := range []struct {
		name, body, path string
		age              int64
		valid            bool
	}{
		{"valid", body, "/api/builder/v1/workspace", 0, true},
		{"changed subject", strings.ReplaceAll(body, "builder-user", "another-user"), "/api/builder/v1/workspace", 0, false},
		{"changed action", body, "/api/builder/v1/key", 0, false},
		{"expired", body, "/api/builder/v1/workspace", -60, false},
		{"future", body, "/api/builder/v1/workspace", 60, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			timestamp := strconv.FormatInt(time.Now().Unix()+tt.age, 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte("POST\n/api/builder/v1/workspace\n" + timestamp + "\n" + body))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tt.path, strings.NewReader(tt.body))
			c.Request.Header.Set("X-Builder-Timestamp", timestamp)
			c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
			request, err := readBuilderRequest(c)
			if tt.valid {
				require.NoError(t, err)
				assert.Equal(t, "builder-user", request.Subject)
			} else {
				require.Error(t, err)
				assert.Nil(t, request)
			}
		})
	}
	t.Setenv("BUILDER_INTEGRATION_SECRET", "")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/builder/v1/workspace", strings.NewReader(body))
	_, err := readBuilderRequest(c)
	require.Error(t, err)
}

func TestBuilderCheckoutRejectsIncompleteStripeConfigurationBeforeAccountLookup(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	oldAPI, oldWebhook, oldPrice := setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = oldAPI, oldWebhook, oldPrice
	})
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "builder-test")
	for _, missing := range []string{"api", "webhook", "price"} {
		t.Run(missing, func(t *testing.T) {
			setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "sk_test_fixture", "whsec_fixture", "price_fixture"
			switch missing {
			case "api":
				setting.StripeApiSecret = ""
			case "webhook":
				setting.StripeWebhookSecret = ""
			case "price":
				setting.StripePriceId = ""
			}
			body := `{"subject":"builder-user","partnership_code":"builder-test","amount":10}`
			path := "/api/builder/v1/checkout"
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n" + body))
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "action", Value: "checkout"}}
			c.Request = httptest.NewRequest("POST", path, strings.NewReader(body))
			c.Request.Header.Set("X-Builder-Timestamp", timestamp)
			c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
			BuilderIntegration(c)
			require.Equal(t, 503, recorder.Code)
			assert.JSONEq(t, `{"code":"UNIFY_STRIPE_UNAVAILABLE"}`, recorder.Body.String())
		})
	}
}

func TestBuilderProgramNameAssertionContract(t *testing.T) {
	for _, tt := range []struct {
		name, configured, sent, code, sentCode string
		valid                                  bool
	}{
		{name: "request selects program without environment", sent: "Builder_hub_2026_Sep_Batch", valid: true},
		{name: "obsolete name setting is ignored", configured: "Old Program", sent: "New Program", valid: true},
		{name: "legacy code setting does not pin name mode", code: "legacy", sent: "Program", valid: true},
		{name: "unicode boundary", sent: strings.Repeat("界", 120), valid: true},
		{name: "too long", sent: strings.Repeat("界", 121)},
		{name: "spaces", sent: "Builder Local Test", valid: true},
		{name: "whitespace", sent: " name"},
		{name: "control", sent: "name\nline"},
		{name: "missing"},
		{name: "configured name is not a fallback", configured: "Program"},
		{name: "case preserved for database lookup", sent: "program", valid: true},
		{name: "both sent", sent: "Program", sentCode: "code"},
		{name: "legacy", code: "legacy", sentCode: "legacy", valid: true},
		{name: "legacy mismatch", code: "legacy", sentCode: "other"},
		{name: "legacy unconfigured", sentCode: "legacy"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			secret := "test-secret-with-at-least-32-characters"
			t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
			t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", tt.configured)
			t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", tt.code)
			body, err := common.Marshal(map[string]string{"subject": "owner", "program_name": tt.sent, "partnership_code": tt.sentCode})
			require.NoError(t, err)
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte("POST\n/api/builder/v1/workspace\n" + timestamp + "\n"))
			mac.Write(body)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/api/builder/v1/workspace", strings.NewReader(string(body)))
			c.Request.Header.Set("X-Builder-Timestamp", timestamp)
			c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
			request, err := readBuilderRequest(c)
			if tt.valid {
				require.NoError(t, err)
				assert.Equal(t, tt.sent, request.ProgramName)
			} else {
				require.Error(t, err)
				assert.Nil(t, request)
			}
		})
	}
}

func TestBuilderMissingProgramReturnsStableErrorForEveryAction(t *testing.T) {
	setupPartnershipControllerTest(t)
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")
	for _, action := range []string{"connect", "workspace", "claim", "credit-status", "key", "checkout"} {
		t.Run(action, func(t *testing.T) {
			body := `{"subject":"owner","program_name":"Missing Program"}`
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
			assert.Equal(t, 409, recorder.Code)
			// The code stays stable across every action -- that is what this
			// test guards. The reason is additive and names which of the four
			// program failures this is.
			assert.JSONEq(t, `{"code":"UNIFY_PROGRAM_UNAVAILABLE","reason":"program_name_not_found"}`, recorder.Body.String())
			assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		})
	}
}

func TestBuilderSignedProgramConnectUsesDatabaseWithoutSelectorEnvironment(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}, &model.BuilderIdentity{}, &model.Token{}))
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")
	program := model.PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	other := model.PartnershipProgram{Name: "Other Program", Code: "other", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&other))
	var customer model.PartnershipCustomer
	require.NoError(t, model.DB.Where("program_id = ? AND is_default = ?", program.Id, true).First(&customer).Error)

	for _, tt := range []struct {
		name, action, program, signedProgram string
		status                               int
		response                             string
	}{
		{"unconnected workspace", "workspace", program.Name, program.Name, 200, `{"connected":false}`},
		{"tampered program", "connect", other.Name, program.Name, 401, `{"code":"UNIFY_UNAUTHORIZED"}`},
		{"connect", "connect", program.Name, program.Name, 200, `{"connected":true}`},
		{"retry", "connect", program.Name, program.Name, 200, `{"connected":true}`},
		{"case mismatch", "workspace", "builder_hub_2026_sep_batch", "builder_hub_2026_sep_batch", 409, `{"code":"UNIFY_PROGRAM_UNAVAILABLE","reason":"program_name_not_found"}`},
		{"cannot switch program", "connect", other.Name, other.Name, 409, `{"code":"UNIFY_PROGRAM_UNAVAILABLE"}`},
		{"cannot read another program", "workspace", other.Name, other.Name, 409, `{"code":"UNIFY_PROGRAM_UNAVAILABLE"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := map[string]any{"subject": "new-builder-user", "program_name": tt.signedProgram, "email": "new-builder@example.invalid", "email_verified": true}
			signedBody, err := common.Marshal(input)
			require.NoError(t, err)
			input["program_name"] = tt.program
			body, err := common.Marshal(input)
			require.NoError(t, err)
			path := "/api/builder/v1/" + tt.action
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			mac := hmac.New(sha256.New, []byte(secret))
			mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n"))
			mac.Write(signedBody)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "action", Value: tt.action}}
			c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
			c.Request.Header.Set("X-Builder-Timestamp", timestamp)
			c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
			BuilderIntegration(c)
			require.Equal(t, tt.status, recorder.Code, recorder.Body.String())
			assert.JSONEq(t, tt.response, recorder.Body.String())
		})
	}
	link, user, err := model.GetBuilderIdentity("new-builder-user")
	require.NoError(t, err)
	assert.Equal(t, program.Id, link.ProgramId)
	assert.Equal(t, customer.Id, link.CustomerId)
	assert.Equal(t, customer.Group, user.Group)
	assert.Zero(t, user.Quota)
	var enrollment model.PartnershipEnrollment
	require.NoError(t, model.DB.Where("user_id = ?", user.Id).First(&enrollment).Error)
	assert.Equal(t, program.Id, enrollment.ProgramId)
	assert.Equal(t, customer.Id, enrollment.CustomerId)
	assert.Zero(t, enrollment.GrantedQuota)
	for _, table := range []any{&model.User{}, &model.BuilderIdentity{}, &model.Token{}, &model.PartnershipEnrollment{}} {
		var count int64
		require.NoError(t, model.DB.Model(table).Count(&count).Error)
		assert.EqualValues(t, 1, count)
	}
}
