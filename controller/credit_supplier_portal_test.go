/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

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

func setupSupplierPortalTest(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.Channel{}, &model.Ability{}, &model.Option{}, &model.PricingConfigHistory{},
		&model.CreditSupplier{}, &model.CreditLot{}, &model.CreditLotUsage{}, &model.CreditLotEvent{}, &model.Settlement{},
	))
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousOptionMap := common.OptionMap
	previousMemoryCache := common.MemoryCacheEnabled
	previousCost := ratio_setting.ChannelCostRatio2JSONString()
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMap = map[string]string{}
	common.MemoryCacheEnabled = false
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMap = previousOptionMap
		common.MemoryCacheEnabled = previousMemoryCache
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(previousCost))
	})
}

func portalContext(t *testing.T, userId int, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userId)
	c.Set("username", "supplier-login")
	return c, recorder
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload), recorder.Body.String())
	return payload
}

func TestSupplierPortalIsInvisibleToUnlinkedLogins(t *testing.T) {
	setupSupplierPortalTest(t)
	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code)

	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{}`)
	SubmitSupplierLot(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestSupplierSubmissionCreatesDisabledChannelAndPendingLot(t *testing.T) {
	setupSupplierPortalTest(t)
	supplier := &model.CreditSupplier{Name: "Acme Labs", Code: "acme", UserId: 42}
	require.NoError(t, model.CreateCreditSupplier(supplier))

	// Refusals first: no attestation, no key, unknown vendor, foreign model.
	for name, body := range map[string]string{
		"no attestation": `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.5,"upstream_key":"sk-ant-x"}`,
		"no key":         `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.5,"transfer_rights_confirmed":true}`,
		"unknown vendor": `{"vendor":"mistral","face_value_usd":1000,"acquisition_rate":0.5,"upstream_key":"k","transfer_rights_confirmed":true}`,
		"foreign model":  `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.5,"upstream_key":"k","transfer_rights_confirmed":true,"models":["gpt-5"]}`,
		"bad rate":       `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":1.5,"upstream_key":"k","transfer_rights_confirmed":true}`,
	} {
		c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", body)
		SubmitSupplierLot(c)
		payload := decode(t, recorder)
		assert.Equal(t, false, payload["success"], name)
	}
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Zero(t, channelCount, "a refused submission leaves no channel behind")

	c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.5,
		"upstream_key":"sk-ant-supplier-key","models":["claude-sonnet-5"],
		"note":"startup credits","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "pending", data["status"])

	channel, err := model.GetChannelById(int(data["channel_id"].(float64)), true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status, "a submitted key must not serve before approval")
	assert.Equal(t, "sk-ant-supplier-key", channel.Key)
	assert.Equal(t, "supplier:acme", *channel.Tag)
	assert.Equal(t, "https://api.anthropic.com", channel.GetBaseURL())
	assert.Equal(t, "claude-sonnet-5", channel.Models)

	lot, err := model.GetCreditLotById(int(data["lot_id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, model.CreditLotSourceSupplier, lot.Source)
	assert.Equal(t, channel.Id, lot.ChannelId)
	assert.Equal(t, model.CreditLotAttestationVersion, lot.AttestationVersion)
	assert.Equal(t, "supplier-login", lot.AttestedBy, "the supplier, not the operator, attested")
	assert.NotZero(t, lot.AttestedAt)
	assert.InDelta(t, 1, ratio_setting.GetChannelCostRatio(channel.Id), 1e-9, "pending lots do not touch pricing")

	// The operator approves: channel enabled, rate written.
	_, err = model.TransitionCreditLot(lot.Id, model.CreditLotTransition{To: model.CreditLotStatusActive, Actor: "root", TransferRightsConfirmed: true})
	require.NoError(t, err)
	channel, _ = model.GetChannelById(channel.Id, false)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.InDelta(t, 0.5, ratio_setting.GetChannelCostRatio(channel.Id), 1e-9)

	// The portal shows the lot without the note and with the channel name.
	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"])
	body := recorder.Body.String()
	assert.Contains(t, body, `"channel_name":"supplier:acme anthropic"`)
	assert.NotContains(t, body, "startup credits", "operator notes stay on our side")
	assert.NotContains(t, body, "sk-ant-supplier-key", "the key is write-only")
	assert.NotContains(t, body, "payout_terms")
	assert.Contains(t, body, `"counterparty":"supplier:acme"`)
	vendors := payload["data"].(map[string]any)["vendors"].([]any)
	assert.GreaterOrEqual(t, len(vendors), 3)
}

func TestSuspendedSupplierCannotSubmit(t *testing.T) {
	setupSupplierPortalTest(t)
	supplier := &model.CreditSupplier{Name: "Acme Labs", Code: "acme", UserId: 42, Status: model.CreditSupplierStatusSuspended, StatusReason: "verification pending"}
	require.NoError(t, model.CreateCreditSupplier(supplier))
	c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"openai","face_value_usd":100,"acquisition_rate":0.4,"upstream_key":"sk-x","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	assert.Equal(t, false, payload["success"])
	assert.Contains(t, payload["message"], "not active")
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Zero(t, channelCount)
}

func TestSupplierSeesOnlyTheirOwnStatementsAndUsage(t *testing.T) {
	setupSupplierPortalTest(t)
	acme := &model.CreditSupplier{Name: "Acme", Code: "acme", UserId: 42}
	other := &model.CreditSupplier{Name: "Other", Code: "other", UserId: 43}
	require.NoError(t, model.CreateCreditSupplier(acme))
	require.NoError(t, model.CreateCreditSupplier(other))
	for _, row := range []*model.Settlement{
		{Kind: "vendor", Counterparty: "supplier:acme", Label: "Acme", PeriodStart: "2026-07-01", PeriodEnd: "2026-07-31", AmountUSD: 120.5, Status: "issued", Note: "internal: check invoice", StatementJSON: `{"kind":"vendor","requests":9,"lines":[{"model":"claude-sonnet-5","requests":9,"amount_usd":120.5}]}`},
		{Kind: "vendor", Counterparty: "supplier:other", Label: "Other", PeriodStart: "2026-07-01", PeriodEnd: "2026-07-31", AmountUSD: 999, Status: "issued", StatementJSON: `{}`},
		{Kind: "vendor", Counterparty: "anthropic", Label: "Anthropic", PeriodStart: "2026-07-01", PeriodEnd: "2026-07-31", AmountUSD: 5000, Status: "issued", StatementJSON: `{}`},
	} {
		require.NoError(t, model.DB.Create(row).Error)
	}
	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/statements", "")
	GetSupplierStatements(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"])
	statements := payload["data"].([]any)
	require.Len(t, statements, 1)
	first := statements[0].(map[string]any)
	assert.InDelta(t, 120.5, first["amount_usd"], 1e-9)
	assert.EqualValues(t, 9, first["requests"])
	assert.NotContains(t, recorder.Body.String(), "internal: check invoice")

	// Usage aggregates across the supplier's lots and excludes everyone else's.
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Key: "k", Name: "a", Status: 1, Models: "claude-sonnet-5", Group: "default"}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 2, Key: "k", Name: "b", Status: 1, Models: "claude-sonnet-5", Group: "default"}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 3, Key: "k", Name: "c", Status: 1, Models: "claude-sonnet-5", Group: "default"}).Error)
	lotA := &model.CreditLot{SupplierId: acme.Id, Vendor: "anthropic", ChannelId: 1, FaceValueUSD: 100, AcquisitionRate: 0.5, Status: "active"}
	lotB := &model.CreditLot{SupplierId: acme.Id, Vendor: "anthropic", ChannelId: 2, FaceValueUSD: 100, AcquisitionRate: 0.5, Status: "active"}
	lotOther := &model.CreditLot{SupplierId: other.Id, Vendor: "anthropic", ChannelId: 3, FaceValueUSD: 100, AcquisitionRate: 0.5, Status: "active"}
	for _, lot := range []*model.CreditLot{lotA, lotB, lotOther} {
		require.NoError(t, model.CreateCreditLot(lot, "test"))
	}
	model.RecordCreditSupplyConsumption(1, "claude-sonnet-5", 1_000_000, 0, 0)
	model.RecordCreditSupplyConsumption(2, "claude-sonnet-5", 1_000_000, 0, 0)
	model.RecordCreditSupplyConsumption(3, "claude-sonnet-5", 1_000_000, 0, 0)

	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/usage?days=7", "")
	GetSupplierUsage(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	days := payload["data"].([]any)
	require.Len(t, days, 1)
	assert.EqualValues(t, 2, days[0].(map[string]any)["requests"])
	list, _ := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	assert.InDelta(t, 2*list, days[0].(map[string]any)["face_usd"], 1e-9)
}

func TestAnyLoginCanApplyAndSeesItsApplicationState(t *testing.T) {
	setupSupplierPortalTest(t)
	c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/apply", `{"name":"Acme Labs","contact_email":"ops@acme.example","note":"OpenAI startup credits","attested":true}`)
	ApplyForSupplier(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	assert.Equal(t, "pending", payload["data"].(map[string]any)["status"])

	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"status":"pending"`)

	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"openai","face_value_usd":100,"acquisition_rate":0.4,"upstream_key":"sk-x","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload = decode(t, recorder)
	assert.Equal(t, false, payload["success"])
	assert.Contains(t, payload["message"], "not active")

	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/apply", `{"name":"Twice","contact_email":"a@b.c","attested":true}`)
	ApplyForSupplier(c)
	assert.Equal(t, false, decode(t, recorder)["success"])
}
