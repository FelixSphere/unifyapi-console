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
		{name: "exact", configured: "Builder_hub_2026_Sep_Batch", sent: "Builder_hub_2026_Sep_Batch", valid: true},
		{name: "unicode boundary", configured: strings.Repeat("界", 120), sent: strings.Repeat("界", 120), valid: true},
		{name: "too long", configured: strings.Repeat("界", 121), sent: strings.Repeat("界", 121)},
		{name: "spaces", configured: "Builder Local Test", sent: "Builder Local Test", valid: true},
		{name: "whitespace", configured: " name", sent: " name"},
		{name: "control", configured: "name\nline", sent: "name\nline"},
		{name: "missing", configured: "Program"},
		{name: "case", configured: "Program", sent: "program"},
		{name: "both configured", configured: "Program", sent: "Program", code: "code", sentCode: "code"},
		{name: "both sent", configured: "Program", sent: "Program", sentCode: "code"},
		{name: "no fallback", configured: "Program", sentCode: "Program"},
		{name: "legacy", code: "legacy", sentCode: "legacy", valid: true},
		{name: "name in legacy", code: "legacy", sentCode: "legacy", sent: "Program"},
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
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "Missing Program")
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
			assert.JSONEq(t, `{"code":"UNIFY_PROGRAM_UNAVAILABLE"}`, recorder.Body.String())
			assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		})
	}
}
