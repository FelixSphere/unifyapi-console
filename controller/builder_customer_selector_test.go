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
	// Provisioning writes Group Pricing, which snapshots the previous value and
	// republishes the option cache. Both need to exist for the write to land.
	require.NoError(t, model.DB.AutoMigrate(&model.PricingConfigHistory{}))
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
		t.Cleanup(func() { common.OptionMap = nil })
	}
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")

	// A team's group is created at the provisioned discount (0.9), never by
	// inheriting the program's own ratio; per-team pricing comes afterwards.
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

	// A team that does not exist here yet is created, not refused. It is never
	// absorbed into the program default, which would bill it to another team.
	t.Run("an unknown team is provisioned as its own customer", func(t *testing.T) {
		recorder := call(t, "connect", "newcomer", "Nusa Labs")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())

		link, user, err := model.GetBuilderIdentity("newcomer")
		require.NoError(t, err)
		var customer model.PartnershipCustomer
		require.NoError(t, model.DB.First(&customer, link.CustomerId).Error)
		assert.Equal(t, "Nusa Labs", customer.Name, "the signed name is kept verbatim")
		assert.False(t, customer.IsDefault, "a provisioned team must never become the program default")
		assert.Equal(t, customer.Group, user.Group, "the member joins their own team's pricing group")
		assert.NotEqual(t, "partner", user.Group, "and not the program default group")

		// Every new customer starts at 90% of the published price -- not at the
		// program's own ratio, which is a different contract.
		assert.InDelta(t, 0.9, ratio_setting.GetGroupRatio(customer.Group), 1e-9)
	})

	// Reconnecting must not create a second customer or reprice the first.
	t.Run("provisioning the same team twice is idempotent", func(t *testing.T) {
		before := call(t, "connect", "newcomer-two", "Nusa Labs")
		require.Equal(t, 200, before.Code, before.Body.String())
		var count int64
		require.NoError(t, model.DB.Model(&model.PartnershipCustomer{}).
			Where("name = ?", "Nusa Labs").Count(&count).Error)
		assert.EqualValues(t, 1, count, "one team, one customer, however many members join")
	})

	// A read must never provision: a typo on a read would otherwise leave a
	// stray customer and an empty invoice behind.
	t.Run("a read never provisions a team", func(t *testing.T) {
		recorder := call(t, "credit-status", "acme-user", "Typo Team")
		assert.Equal(t, 200, recorder.Code, recorder.Body.String())
		var count int64
		require.NoError(t, model.DB.Model(&model.PartnershipCustomer{}).
			Where("name = ?", "Typo Team").Count(&count).Error)
		assert.Zero(t, count, "a read must not create anything")
	})

	t.Run("a malformed team name is a bad request", func(t *testing.T) {
		recorder := call(t, "connect", "acme-user", " Acme Robotics")
		assert.Equal(t, 400, recorder.Code)
		assert.JSONEq(t, `{"code":"UNIFY_CUSTOMER_INVALID"}`, recorder.Body.String())
	})

	// Connect is idempotent. An identity that already has an account keeps the
	// customer it is enrolled in, whatever team the caller now names, and the
	// call succeeds. Refusing here locked out every account that connected
	// before team names started arriving -- they are all in the program
	// default while the Builder side names their team.
	//
	// Nothing is repointed: moving a person between customers moves their
	// usage onto another invoice and stays an administrative act.
	t.Run("connecting again keeps the enrollment and does not fail", func(t *testing.T) {
		before, _, err := model.GetBuilderIdentity("acme-user")
		require.NoError(t, err)

		recorder := call(t, "connect", "acme-user", "Builder_hub_2026_Sep_Batch")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())

		after, user, err := model.GetBuilderIdentity("acme-user")
		require.NoError(t, err)
		assert.Equal(t, before.CustomerId, after.CustomerId,
			"naming another team must not move the enrollment")
		assert.Equal(t, "acme_robotics", user.Group,
			"nor move the member's pricing group")
	})

	// A read is answered from the enrollment on the link, not from the team the
	// caller happened to name. Refusing here would lock out every account that
	// connected before teams existed, which is what it did in DEV.
	t.Run("a read is answered from the enrollment, whatever team is named", func(t *testing.T) {
		recorder := call(t, "credit-status", "acme-user", "Builder_hub_2026_Sep_Batch")
		assert.Equal(t, 200, recorder.Code, recorder.Body.String())
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

// Turning the selector on from the Builder side must not lock out accounts
// that connected before teams existed. They are enrolled in the program
// default; the team name now arriving on every call resolves to a different
// customer, or -- before any team is provisioned -- to none at all.
//
// Reads answered 409 for exactly this reason in DEV. The account exists, its
// owner is on the link, and naming a team cannot change that.
func TestAnExistingAccountKeepsWorkingWhenTeamNamesStartArriving(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}, &model.BuilderIdentity{}, &model.Token{}))
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")

	const groups = `{"partner":0.9,"acme_robotics":1}`
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", "GroupRatio").Update("value", groups).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))

	program := model.PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))

	call := func(t *testing.T, action, subject, customerName string) *httptest.ResponseRecorder {
		t.Helper()
		input := map[string]any{
			"subject": subject, "program_name": program.Name,
			"email": subject + "@example.invalid", "email_verified": true,
		}
		if customerName != "" {
			input["customer_name"] = customerName
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

	// Connected before teams existed: no name sent, enrolled in the default.
	require.Equal(t, 200, call(t, "connect", "legacy-user", "").Code)

	t.Run("a read survives a team that is not provisioned yet", func(t *testing.T) {
		recorder := call(t, "credit-status", "legacy-user", "Not Provisioned Yet")
		assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	})

	t.Run("a read survives a team the account is not enrolled in", func(t *testing.T) {
		require.NoError(t, model.CreatePartnershipCustomer(program.Id, &model.PartnershipCustomer{
			Name: "Acme Robotics", Code: "acme-robotics", Group: "acme_robotics", Enabled: true,
		}))
		recorder := call(t, "credit-status", "legacy-user", "Acme Robotics")
		assert.Equal(t, 200, recorder.Code, recorder.Body.String())
	})

	// The protection that matters is still there, and it is now the stronger
	// form: naming another team does not move the enrollment. It no longer
	// fails either, because failing locked these accounts out entirely.
	t.Run("naming another team leaves the enrollment where it is", func(t *testing.T) {
		before, _, err := model.GetBuilderIdentity("legacy-user")
		require.NoError(t, err)

		recorder := call(t, "connect", "legacy-user", "Acme Robotics")
		require.Equal(t, 200, recorder.Code, recorder.Body.String())

		after, user, err := model.GetBuilderIdentity("legacy-user")
		require.NoError(t, err)
		assert.Equal(t, before.CustomerId, after.CustomerId, "the enrollment must not move")
		assert.Equal(t, "partner", user.Group, "nor the member's pricing group")
	})
}

// A binding whose program was deleted must not make the account unreadable.
// connect heals it, but a read has to answer in the meantime -- refusing meant
// the console showed nothing at all and the person could not even see the
// account they already had.
func TestAReadSurvivesABindingToADeletedProgram(t *testing.T) {
	setupPartnershipControllerTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.User{}, &model.Tenant{}, &model.BuilderIdentity{}, &model.Token{}))
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")

	program := model.PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&program))
	var customer model.PartnershipCustomer
	require.NoError(t, model.DB.Where("program_id = ? AND is_default = ?", program.Id, true).First(&customer).Error)

	user := model.User{Username: "builder_orphan", Email: "orphan@example.invalid", Role: 1, Status: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	require.NoError(t, model.DB.Create(&model.BuilderIdentity{
		Subject: "orphan-subject", UserId: user.Id,
		ProgramId: 987654, CustomerId: 999, TokenId: 1, // a program that is not there
	}).Error)

	body, err := common.Marshal(map[string]any{
		"subject": "orphan-subject", "program_name": program.Name,
	})
	require.NoError(t, err)
	path := "/api/builder/v1/credit-status"
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n"))
	mac.Write(body)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "action", Value: "credit-status"}}
	c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
	c.Request.Header.Set("X-Builder-Timestamp", timestamp)
	c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
	BuilderIntegration(c)

	// The point is the program check, not whether credit-status can read a
	// synthetic fixture: it must not refuse the account over a binding whose
	// program is gone.
	assert.NotEqual(t, 409, recorder.Code, recorder.Body.String())
	assert.NotContains(t, recorder.Body.String(), "UNIFY_PROGRAM_UNAVAILABLE")
}

// A misconfigured program name and a genuinely unusable program answered
// identically. That cost a full day: the caller's configuration pointed at a
// name no program had, and the response was indistinguishable from an outage
// on this side, so the search went everywhere except the one field that was
// wrong.
//
// The code stays UNIFY_PROGRAM_UNAVAILABLE on purpose -- Builder Hub matches
// it exactly, and renaming it would drop these into its generic upstream
// failure branch, turning a precise 409 into an opaque 502.
func TestAMisconfiguredProgramNameSaysSo(t *testing.T) {
	setupPartnershipControllerTest(t)
	secret := "test-secret-with-at-least-32-characters"
	t.Setenv("BUILDER_INTEGRATION_SECRET", secret)
	t.Setenv("BUILDER_INTEGRATION_PROGRAM_NAME", "")
	t.Setenv("BUILDER_INTEGRATION_PARTNERSHIP_CODE", "")

	live := model.PartnershipProgram{Name: "Builder_hub_2026_Sep_Batch", Code: "builders", Group: "partner", Enabled: true}
	require.NoError(t, model.CreatePartnershipProgram(&live))

	ask := func(t *testing.T, programName string) map[string]any {
		t.Helper()
		body, err := common.Marshal(map[string]any{"subject": "cfg-probe", "program_name": programName})
		require.NoError(t, err)
		path := "/api/builder/v1/workspace"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("POST\n" + path + "\n" + timestamp + "\n"))
		mac.Write(body)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Params = gin.Params{{Key: "action", Value: "workspace"}}
		c.Request = httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		c.Request.Header.Set("X-Builder-Timestamp", timestamp)
		c.Request.Header.Set("X-Builder-Signature", hex.EncodeToString(mac.Sum(nil)))
		BuilderIntegration(c)
		out := map[string]any{}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &out))
		out["_status"] = recorder.Code
		return out
	}

	// The exact shape of today's outage: a name nothing matches.
	t.Run("a name no program has", func(t *testing.T) {
		got := ask(t, "UnifyAPI")
		assert.EqualValues(t, 409, got["_status"])
		assert.Equal(t, "UNIFY_PROGRAM_UNAVAILABLE", got["code"], "the code must stay stable for existing callers")
		assert.Equal(t, "program_name_not_found", got["reason"], "and the reason must name the real problem")
	})

	// A program that exists but is switched off is a different situation with a
	// different fix, and must not look like a typo.
	t.Run("a program that is disabled", func(t *testing.T) {
		require.NoError(t, model.DB.Model(&model.PartnershipProgram{}).
			Where("id = ?", live.Id).Update("enabled", false).Error)
		got := ask(t, live.Name)
		assert.Equal(t, "UNIFY_PROGRAM_UNAVAILABLE", got["code"])
		assert.Equal(t, "program_disabled_or_out_of_schedule", got["reason"])
		assert.NotEqual(t, "program_name_not_found", got["reason"],
			"a switched-off campaign must not be reported as a configuration typo")
	})
}
