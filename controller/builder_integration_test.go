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
