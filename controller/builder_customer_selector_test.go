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
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A signed team name routes a Builder connection to that team's customer, so
// its usage bills to the team's own group and invoice rather than the shared
// default. Each failure reports which selector was at fault.
func TestBuilderConnectRoutesToNamedCustomer(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}, &model.BuilderIdentity{}, &model.Token{}))
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")

	// A team's group is created at list price; discounts are granted per team
	// afterwards rather than inherited by joining the cohort.
	const groups = `{"partner":0.9,"acme_robotics":1}`
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", "GroupRatio").Update("value", groups).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))

	program := model.PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	require.NoError(t, model.CreatePartnershipCustomer(program.Id, &model.PartnershipCustomer{
		Name: "Acme Robotics", Code: "acme-robotics", Group: "acme_robotics", Enabled: true,
	}))

	callWith := func(t *testing.T, action, subject, customerName string, mutate func(map[string]any)) *httptest.ResponseRecorder {
		t.Helper()
		input := map[string]any{
			"subject": subject, "program_name": program.Name,
			"email": subject + "@example.invalid", "email_verified": true,
		}
		if customerName != "" {
			input["customer_name"] = customerName
		}
		if mutate != nil {
			mutate(input)
		}
		body, err := common.Marshal(input)
		require.NoError(t, err)
		path := "/api/builder/v1/" + action
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n"))
		mac.Write(body)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "action", Value: action}}
		c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		c.Request.Header.Set("X-Builder-Timestamp", timestamp)
		c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
		BuilderIntegration(c)
		return recorder
	}
	call := func(t *testing.T, action, subject, customerName string) *httptest.ResponseRecorder {
		t.Helper()
		return callWith(t, action, subject, customerName, nil)
	}

	t.Run("named team is enrolled into its own customer", func(t *testing.T) {
		recorder := call(t, "connect", "acme-user", "Acme Robotics")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
		link, user, err := model.GetBuilderIdentity("acme-user")
		require.NoError(t, err)
		assert.Equal(t, "acme_robotics", user.Group, "the team's pricing group, not the program default")
		var customer model.PartnershipCustomer
		require.NoError(t, model.DB.First(&customer, link.CustomerId).Error)
		assert.Equal(t, "Acme Robotics", customer.Name)
	})

	t.Run("no team name still uses the program default", func(t *testing.T) {
		recorder := call(t, "connect", "default-user", "")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
		_, user, err := model.GetBuilderIdentity("default-user")
		require.NoError(t, err)
		assert.Equal(t, "partner", user.Group)
	})

	t.Run("an unknown team is refused, not absorbed into the default", func(t *testing.T) {
		recorder := call(t, "connect", "orphan-user", "No Such Team")
		assert.Equal(t, 409, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_CUSTOMER_UNAVAILABLE"}`, recorder.Body.String())
		_, _, err := model.GetBuilderIdentity("orphan-user")
		assert.Error(t, err, "nothing may be provisioned for an unresolvable team")
	})

	t.Run("a malformed team name is a bad request", func(t *testing.T) {
		recorder := call(t, "connect", "acme-user", " Acme Robotics")
		assert.Equal(t, 400, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_CUSTOMER_INVALID"}`, recorder.Body.String())
	})

	// Moving a person between teams changes which invoice their usage lands on,
	// so the bridge refuses it rather than repointing the enrollment silently.
	t.Run("claiming a different team than the one enrolled conflicts", func(t *testing.T) {
		recorder := call(t, "connect", "acme-user", "Builder_hub_2026_Sep_Batch")
		assert.Equal(t, 409, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_CUSTOMER_CONFLICT"}`, recorder.Body.String())
	})

	t.Run("a read for the wrong team conflicts instead of answering", func(t *testing.T) {
		recorder := call(t, "workspace", "acme-user", "Builder_hub_2026_Sep_Batch")
		assert.Equal(t, 409, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_CUSTOMER_CONFLICT"}`, recorder.Body.String())
	})

	// The signed email is the caller's claim about who this subject is. connect
	// checked it while provisioning; every other action took the subject on
	// trust. Verify it wherever it is supplied.
	t.Run("a signed email must match the linked account", func(t *testing.T) {
		recorder := callWith(t, "workspace", "acme-user", "Acme Robotics", func(input map[string]any) {
			input["email"] = "someone-else@example.invalid"
		})
		assert.Equal(t, 409, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_EMAIL_MISMATCH"}`, recorder.Body.String())
	})

	t.Run("an unverified email is refused even when it matches", func(t *testing.T) {
		recorder := callWith(t, "workspace", "acme-user", "Acme Robotics", func(input map[string]any) {
			input["email_verified"] = false
		})
		assert.Equal(t, 403, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_EMAIL_UNVERIFIED"}`, recorder.Body.String())
	})

	// Assert on the email decision itself rather than the whole action, so the
	// result does not depend on what a workspace read goes on to do.
	t.Run("email comparison ignores case and surrounding form", func(t *testing.T) {
		recorder := callWith(t, "credit-status", "acme-user", "Acme Robotics", func(input map[string]any) {
			input["email"] = " ACME-USER@Example.Invalid "
		})
		assert.NotContains(t, recorder.Body.String(), "UNIFY_EMAIL_MISMATCH")
		assert.NotContains(t, recorder.Body.String(), "UNIFY_EMAIL_UNVERIFIED")
	})

	// Omitting the field keeps older callers working, so this ships dormant.
	t.Run("omitting the email leaves existing callers unaffected", func(t *testing.T) {
		recorder := callWith(t, "credit-status", "acme-user", "Acme Robotics", func(input map[string]any) {
			delete(input, "email")
			delete(input, "email_verified")
		})
		assert.NotContains(t, recorder.Body.String(), "UNIFY_EMAIL_MISMATCH")
		assert.NotContains(t, recorder.Body.String(), "UNIFY_EMAIL_UNVERIFIED")
	})

	t.Run("reconnecting the same team is idempotent", func(t *testing.T) {
		before, _, err := model.GetBuilderIdentity("acme-user")
		require.NoError(t, err)
		recorder := call(t, "connect", "acme-user", "Acme Robotics")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())
		after, user, err := model.GetBuilderIdentity("acme-user")
		require.NoError(t, err)
		assert.Equal(t, before.Id, after.Id, "no second identity")
		assert.Equal(t, before.CustomerId, after.CustomerId)
		assert.Equal(t, "acme_robotics", user.Group)
	})
}
