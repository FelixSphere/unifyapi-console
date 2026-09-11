/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		&model.CreditSupplier{}, &model.CreditLot{}, &model.CreditLotUsage{}, &model.CreditLotEvent{}, &model.Settlement{}, &model.Log{},
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

	// Selling needs no prior link: an empty submission is refused on its
	// content, not on who is asking.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{}`)
	SubmitSupplierLot(c)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, false, decode(t, recorder)["success"])
}

func TestSupplierSubmissionCreatesDisabledChannelAndVerifiedLot(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "acme", DisplayName: "Acme Labs"}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	verifySupplierChannel = func(*model.Channel, string, int) (verificationUsage, error) { return verificationUsage{}, nil }

	// Refusals first: no attestation, no key, unknown vendor, foreign model, no payout choice.
	for name, body := range map[string]string{
		"no attestation": `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-ant-x","payout_method":"platform_credit"}`,
		"no key":         `{"vendor":"anthropic","face_value_usd":1000,"payout_method":"platform_credit","transfer_rights_confirmed":true}`,
		"unknown vendor": `{"vendor":"mistral","face_value_usd":1000,"upstream_key":"k","payout_method":"platform_credit","transfer_rights_confirmed":true}`,
		"foreign model":  `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"k","payout_method":"platform_credit","transfer_rights_confirmed":true,"models":["gpt-5"]}`,
		"no payout":      `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"k","transfer_rights_confirmed":true}`,
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
		"note":"startup credits","payout_method":"platform_credit","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "verified", data["status"], "the key was exercised; the sale now waits for payment")

	channel, err := model.GetChannelById(int(data["channel_id"].(float64)), true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status, "a submitted key must not serve before payment")
	assert.Equal(t, "sk-ant-supplier-key", channel.Key)
	assert.Equal(t, "supplier:acme", *channel.Tag)
	assert.Equal(t, "https://api.anthropic.com", channel.GetBaseURL())
	assert.Equal(t, "claude-sonnet-5", channel.Models)

	lot, err := model.GetCreditLotById(int(data["lot_id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, model.CreditLotSourceSupplier, lot.Source)
	assert.Equal(t, channel.Id, lot.ChannelId)
	assert.InDelta(t, 0.2, lot.AcquisitionRate, 1e-9, "the posted rate, not the caller's 0.5")
	assert.Equal(t, model.CreditLotAttestationVersion, lot.AttestationVersion)
	assert.Equal(t, "supplier-login", lot.AttestedBy, "the supplier, not the operator, attested")
	assert.NotZero(t, lot.AttestedAt)
	assert.NotZero(t, lot.VerifiedAt)
	assert.InDelta(t, 1, ratio_setting.GetChannelCostRatio(channel.Id), 1e-9, "unpaid lots do not touch pricing")

	// The operator pays: channel enabled, rate written.
	_, err = model.PayCreditLot(lot.Id, model.CreditLotPayment{Actor: "root"})
	require.NoError(t, err)
	channel, _ = model.GetChannelById(channel.Id, false)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.InDelta(t, 0.2, ratio_setting.GetChannelCostRatio(channel.Id), 1e-9)

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
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "acme"}).Error)
	c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"openai","face_value_usd":100,"upstream_key":"sk-x","payout_method":"platform_credit","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	assert.Equal(t, false, payload["success"])
	assert.Contains(t, payload["message"], "suspended")
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
	model.RecordCreditSupplyConsumption(1, "claude-sonnet-5", ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})
	model.RecordCreditSupplyConsumption(2, "claude-sonnet-5", ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})
	model.RecordCreditSupplyConsumption(3, "claude-sonnet-5", ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})

	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/usage?days=7", "")
	GetSupplierUsage(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	days := payload["data"].([]any)
	require.Len(t, days, 1)
	assert.EqualValues(t, 2, days[0].(map[string]any)["requests"])
	list, _ := ratio_setting.ListPriceUSD("claude-sonnet-5", ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})
	assert.InDelta(t, 2*list, days[0].(map[string]any)["face_usd"], 1e-9)
}

func TestSellingIsDirectVerifiedAndPaidBeforeUse(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "seller", DisplayName: "Seller Co", Email: "s@x.io", Quota: 0}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	var testedModel string
	verifySupplierChannel = func(channel *model.Channel, testModel string, testUserID int) (verificationUsage, error) {
		testedModel = testModel
		assert.Equal(t, 42, testUserID, "the test request runs as the seller")
		if channel.Key == "sk-broken" {
			return verificationUsage{}, errors.New("401 invalid x-api-key")
		}
		// One million input tokens, 400k of them cached: the booking must use
		// the cache-read rate for those, like customer draw-down does.
		return verificationUsage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 0}, nil
	}

	// Terms are posted; the caller's rate is ignored.
	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/terms", "")
	GetSupplierTerms(c)
	assert.Contains(t, recorder.Body.String(), `"anthropic":0.2`)

	// A bad key is answered immediately, the lot is rejected and the key is gone.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.99,"upstream_key":"sk-broken","payout_method":"platform_credit","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	assert.Equal(t, false, payload["success"])
	assert.Contains(t, payload["message"], "could not make a request")
	var channels int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channels).Error)
	assert.Zero(t, channels, "a failed verification leaves no key behind")
	rejected, _ := model.GetCreditLots(model.CreditLotFilter{Status: model.CreditLotStatusRejected})
	require.Len(t, rejected, 1)
	assert.Zero(t, rejected[0].ChannelId)
	assert.Contains(t, rejected[0].StatusReason, "invalid x-api-key")

	// No application step: the first sale creates the supplier.
	supplier, err := model.GetCreditSupplierByUserId(42)
	require.NoError(t, err)
	assert.Equal(t, model.CreditSupplierStatusActive, supplier.Status)
	assert.Equal(t, "seller", supplier.Code)

	// Too small, external without an account, then a good sale.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":20,"upstream_key":"sk-ok","payout_method":"platform_credit","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	assert.Contains(t, decode(t, recorder)["message"], "smallest sale")
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-ok","payout_method":"external","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	assert.Contains(t, decode(t, recorder)["message"], "where to send")
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"acquisition_rate":0.99,"upstream_key":"sk-ok","payout_method":"platform_credit","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "verified", data["status"])
	assert.InDelta(t, 200, data["payout_usd"], 1e-9, "posted 20% rate, not the caller's 0.99")
	assert.NotEmpty(t, testedModel)
	assert.NotEqual(t, "claude-fable-5", testedModel, "verification uses the cheapest catalogue model, not the dearest")
	cheapest, _ := ratio_setting.CatalogEntryFor(testedModel)
	opus, _ := ratio_setting.CatalogEntryFor("claude-opus-4-8")
	assert.Less(t, cheapest.InputUSD, opus.InputUSD)

	lotId := int(data["lot_id"].(float64))
	lot, err := model.GetCreditLotById(lotId)
	require.NoError(t, err)
	channel, err := model.GetChannelById(lot.ChannelId, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status, "nothing is consumed before payment")
	withCache, _ := ratio_setting.ListPriceUSD(testedModel, ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 0})
	noCache, _ := ratio_setting.ListPriceUSD(testedModel, ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})
	assert.Greater(t, withCache, 0.0)
	assert.Less(t, withCache, noCache, "the catalogue prices cached reads below fresh input for this model")
	assert.InDelta(t, withCache, lot.ConsumedUSD, 1e-9, "the verification is drawn from the lot on the same cache-aware list-price path as customer traffic")
	assert.Contains(t, lot.VerificationNote, "400000 of them cached")
	var logs int64
	require.NoError(t, model.DB.Model(&model.Log{}).Count(&logs).Error)
	assert.Zero(t, logs, "a verification is not revenue: no consume log row")
	assert.EqualValues(t, 10, *channel.Priority, "bought credits drain before our own accounts")
	assert.Contains(t, channel.Group, "default")

	// Approving a verified sale is refused; paying it activates it and credits the wallet.
	_, err = model.TransitionCreditLot(lotId, model.CreditLotTransition{To: model.CreditLotStatusActive, Actor: "root", TransferRightsConfirmed: true})
	require.ErrorIs(t, err, model.ErrCreditLotNeedsPayment)
	c, recorder = portalContext(t, 1, http.MethodPost, "/api/credit-supply/lots/"+strconv.Itoa(lotId)+"/pay", `{}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(lotId)}}
	PayCreditLot(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	paid, _ := model.GetCreditLotById(lotId)
	assert.Equal(t, model.CreditLotStatusActive, paid.Status)
	assert.InDelta(t, 200, paid.PaidUSD, 1e-9)
	assert.Equal(t, model.CreditLotPayoutPlatformCredit, paid.PayoutMethod)
	seller, _ := model.GetUserById(42, false)
	assert.EqualValues(t, int(200*common.QuotaPerUnit), seller.Quota, "paid into the seller's wallet")
	channel, _ = model.GetChannelById(lot.ChannelId, false)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.InDelta(t, 0.2, ratio_setting.GetChannelCostRatio(lot.ChannelId), 1e-9, "cost basis is the price we paid")

	// The seller's view shows the payment; a second pay is refused.
	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	assert.Contains(t, recorder.Body.String(), `"paid_usd":200`)
	assert.NotContains(t, recorder.Body.String(), "sk-ok")
	_, err = model.PayCreditLot(lotId, model.CreditLotPayment{Actor: "root"})
	require.Error(t, err)
}

func TestExternalPayoutNeedsAReferenceAndBooksNoCredit(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 7, Username: "cashout", Quota: 0}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	verifySupplierChannel = func(*model.Channel, string, int) (verificationUsage, error) { return verificationUsage{}, nil }
	c, recorder := portalContext(t, 7, http.MethodPost, "/api/supplier/lots", `{"vendor":"openai","face_value_usd":500,"upstream_key":"sk-ok","payout_method":"external","payout_account":"PayPal ops@cashout.example","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	lotId := int(payload["data"].(map[string]any)["lot_id"].(float64))
	assert.InDelta(t, 150, payload["data"].(map[string]any)["payout_usd"], 1e-9, "openai is bought at 30%")

	_, err := model.PayCreditLot(lotId, model.CreditLotPayment{Actor: "root"})
	require.Error(t, err, "an external payment needs the transfer reference")
	paid, err := model.PayCreditLot(lotId, model.CreditLotPayment{Actor: "root", Reference: "WISE-20260905-0042"})
	require.NoError(t, err)
	assert.Equal(t, "WISE-20260905-0042", paid.PayoutReference)
	user, _ := model.GetUserById(7, false)
	assert.Zero(t, user.Quota, "cash was sent outside; no platform credit is booked")
}
