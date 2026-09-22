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
		&model.CreditSupplier{}, &model.CreditLot{}, &model.CreditLotUsage{}, &model.CreditLotEvent{}, &model.CreditSharePayout{}, &model.Settlement{}, &model.Log{},
	))
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousOptionMap := common.OptionMap
	previousMemoryCache := common.MemoryCacheEnabled
	previousCost := ratio_setting.ChannelCostRatio2JSONString()
	// Redis is on by default in this binary and no server is reachable from a
	// unit test, so anything that reaches the user cache -- RecordLog on a
	// platform-credit payout, for one -- panics. Without this the file only
	// passes when some other test in the package happens to have turned Redis
	// off first, which is not a property a test should depend on.
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	previousLogDB := model.LOG_DB
	model.DB = db
	// RecordLog writes through LOG_DB, which production points at the same
	// handle unless a separate log database is configured.
	model.LOG_DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMap = map[string]string{}
	common.MemoryCacheEnabled = false
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMap = previousOptionMap
		common.MemoryCacheEnabled = previousMemoryCache
		common.RedisEnabled = previousRedis
		model.LOG_DB = previousLogDB
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

// filePayoutAccount does what every seller must do before selling: say where
// their share goes.
func filePayoutAccount(t *testing.T, userId int, body string) {
	t.Helper()
	c, recorder := portalContext(t, userId, http.MethodPut, "/api/supplier/payout-account", body)
	UpdateSupplierPayoutAccount(c)
	require.Equal(t, true, decode(t, recorder)["success"], recorder.Body.String())
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

func TestSubmissionUnderManualReviewWaitsDisabledUntilAccepted(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "acme", DisplayName: "Acme Labs"}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	verifySupplierChannel = func(*model.Channel, string, int) (verificationUsage, error) { return verificationUsage{}, nil }
	previousTerms := model.CreditSupplyTerms2JSONString()
	t.Cleanup(func() { require.NoError(t, model.UpdateCreditSupplyTermsByJSONString(previousTerms)) })
	require.NoError(t, model.UpdateCreditSupplyTermsByJSONString(`{"revenue_share_rates":{"anthropic":0.5},"min_face_usd":100,"manual_review":true}`))

	// The very first thing a seller hits is the payout account: with a share
	// deal nothing is paid up front, so we ask where the money goes before we
	// can owe any.
	c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"k","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	assert.Equal(t, false, payload["success"])
	assert.Equal(t, "payout_account_required", payload["code"])
	filePayoutAccount(t, 42, `{"method":"platform_credit"}`)

	// Refusals: no attestation, no key, unknown vendor, foreign model.
	for name, body := range map[string]string{
		"no attestation": `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-ant-x"}`,
		"no key":         `{"vendor":"anthropic","face_value_usd":1000,"transfer_rights_confirmed":true}`,
		"unknown vendor": `{"vendor":"mistral","face_value_usd":1000,"upstream_key":"k","transfer_rights_confirmed":true}`,
		"foreign model":  `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"k","transfer_rights_confirmed":true,"models":["gpt-5"]}`,
	} {
		c, recorder := portalContext(t, 42, http.MethodPost, "/api/supplier/lots", body)
		SubmitSupplierLot(c)
		payload := decode(t, recorder)
		assert.Equal(t, false, payload["success"], name)
	}
	var channelCount int64
	require.NoError(t, model.DB.Model(&model.Channel{}).Count(&channelCount).Error)
	assert.Zero(t, channelCount, "a refused submission leaves no channel behind")

	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"anthropic","face_value_usd":1000,"ignored_acquisition_rate":0.5,"deal_type":"purchase",
		"upstream_key":"sk-ant-supplier-key","models":["claude-sonnet-5"],
		"note":"startup credits","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "verified", data["status"], "under manual review the key waits for the operator")
	assert.Equal(t, "revenue_share", data["deal_type"], "sellers get one deal whatever they send")

	channel, err := model.GetChannelById(int(data["channel_id"].(float64)), true)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status, "not in service until accepted")
	assert.Equal(t, "sk-ant-supplier-key", channel.Key)
	assert.Equal(t, "supplier:acme", *channel.Tag)
	assert.Equal(t, "https://api.anthropic.com", channel.GetBaseURL())
	assert.Equal(t, "claude-sonnet-5", channel.Models)

	lot, err := model.GetCreditLotById(int(data["lot_id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, model.CreditLotSourceSupplier, lot.Source)
	assert.Equal(t, channel.Id, lot.ChannelId)
	assert.Zero(t, lot.AcquisitionRate, "nothing is bought; the caller's rate is ignored")
	assert.InDelta(t, 0.5, lot.RevenueSharePct, 1e-9, "the posted share")
	assert.Equal(t, model.CreditShareBasisRevenue, lot.RevenueShareBasis)
	assert.Equal(t, model.CreditLotPayoutPlatformCredit, lot.PayoutMethod, "from the seller's profile")
	assert.Equal(t, model.CreditLotAttestationVersion, lot.AttestationVersion)
	assert.Equal(t, "supplier-login", lot.AttestedBy, "the supplier, not the operator, attested")
	assert.NotZero(t, lot.VerifiedAt)
	assert.InDelta(t, 1, ratio_setting.GetChannelCostRatio(channel.Id), 1e-9, "not in service, not in pricing")

	// The operator accepts: channel enabled, the share becomes the cost basis.
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
	assert.Contains(t, body, `"has_payout_account":true`)
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
	lotA := &model.CreditLot{SupplierId: acme.Id, Vendor: "anthropic", ChannelId: 1, FaceValueUSD: 100, RevenueSharePct: 0.5, Status: "active"}
	lotB := &model.CreditLot{SupplierId: acme.Id, Vendor: "anthropic", ChannelId: 2, FaceValueUSD: 100, RevenueSharePct: 0.5, Status: "active"}
	lotOther := &model.CreditLot{SupplierId: other.Id, Vendor: "anthropic", ChannelId: 3, FaceValueUSD: 100, RevenueSharePct: 0.5, Status: "active"}
	for _, lot := range []*model.CreditLot{lotA, lotB, lotOther} {
		require.NoError(t, model.CreateCreditLot(lot, "test"))
	}
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{ChannelId: 1, ModelName: "claude-sonnet-5", TokenUsage: ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0}})
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{ChannelId: 2, ModelName: "claude-sonnet-5", TokenUsage: ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0}})
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{ChannelId: 3, ModelName: "claude-sonnet-5", TokenUsage: ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0}})

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

func TestSellingIsShareOnlyVerifiedAndLiveAtOnce(t *testing.T) {
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
		return verificationUsage{PromptTokens: 1_000_000, CachedTokens: 400_000, CompletionTokens: 0}, nil
	}

	// The posted offer is a share, and only a share.
	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/terms", "")
	GetSupplierTerms(c)
	assert.Contains(t, recorder.Body.String(), `"revenue_share_rates":{"anthropic":0.5`)
	assert.NotContains(t, recorder.Body.String(), "buy_rates", "buy-out rates are operator business")

	// No payout account yet: refused with a code the page can act on. The
	// supplier record itself exists from this first contact.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-ok","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	assert.Equal(t, "payout_account_required", decode(t, recorder)["code"])
	supplier, err := model.GetCreditSupplierByUserId(42)
	require.NoError(t, err)
	assert.Equal(t, model.CreditSupplierStatusActive, supplier.Status)
	assert.Equal(t, "seller", supplier.Code)

	// A payout account needs enough to actually pay; then it is on file.
	c, recorder = portalContext(t, 42, http.MethodPut, "/api/supplier/payout-account", `{"method":"bank","holder":"Seller Co"}`)
	UpdateSupplierPayoutAccount(c)
	assert.Equal(t, false, decode(t, recorder)["success"], "bank needs the account details")
	c, recorder = portalContext(t, 42, http.MethodPut, "/api/supplier/payout-account", `{"method":"paypal","holder":"Seller Co","details":"sk-ant-api03-not-an-account"}`)
	UpdateSupplierPayoutAccount(c)
	assert.Equal(t, false, decode(t, recorder)["success"], "an API key is not a payout account")
	filePayoutAccount(t, 42, `{"method":"platform_credit"}`)

	// A bad key is answered immediately, the lot is rejected and the key is gone.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-broken","transfer_rights_confirmed":true}`)
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

	// Too small, then a good one: verified and IN SERVICE, no operator click.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":20,"upstream_key":"sk-ok","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	assert.Contains(t, decode(t, recorder)["message"], "smallest balance")
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{"vendor":"anthropic","face_value_usd":1000,"upstream_key":"sk-ok","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "active", data["status"], "verified keys go straight into service")
	assert.Equal(t, "revenue_share", data["deal_type"])
	assert.InDelta(t, 0.5, data["revenue_share_pct"], 1e-9)
	cheapest, _ := ratio_setting.CatalogEntryFor(testedModel)
	opus, _ := ratio_setting.CatalogEntryFor("claude-opus-4-8")
	assert.Less(t, cheapest.InputUSD, opus.InputUSD, "verification uses the cheapest catalogue model")

	lotId := int(data["lot_id"].(float64))
	lot, err := model.GetCreditLotById(lotId)
	require.NoError(t, err)
	channel, err := model.GetChannelById(lot.ChannelId, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.EqualValues(t, 10, *channel.Priority, "contributed credits drain before our own accounts")
	assert.Contains(t, channel.Group, "default")
	assert.InDelta(t, 0.5, ratio_setting.GetChannelCostRatio(lot.ChannelId), 1e-9, "the share is the cost basis reconciliation reads")
	withCache, _ := ratio_setting.ListPriceUSD(testedModel, ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 400_000})
	assert.InDelta(t, withCache, lot.ConsumedUSD, 1e-9, "the verification is drawn from the lot, cache-aware")
	var logs int64
	require.NoError(t, model.DB.Model(&model.Log{}).Count(&logs).Error)
	assert.Zero(t, logs, "a verification is not revenue")

	// Customers buy $80 through it: the seller sees what it sold for and their half.
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{
		ChannelId: lot.ChannelId, ModelName: "claude-sonnet-5",
		TokenUsage:   ratio_setting.TokenUsage{PromptTokens: 1000},
		QuotaCharged: int(80 * common.QuotaPerUnit),
	})
	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	payload = decode(t, recorder)
	totals := payload["data"].(map[string]any)["totals"].(map[string]any)
	assert.InDelta(t, 80.0, totals["share_revenue_usd"], 1e-9)
	assert.InDelta(t, 40.0, totals["share_earned_usd"], 1e-9)
	assert.InDelta(t, 40.0, totals["share_unpaid_usd"], 1e-9)
	assert.NotContains(t, recorder.Body.String(), "sk-ok")

	// The operator pays the share into the wallet; the seller sees it; twice is refused.
	c, recorder = portalContext(t, 1, http.MethodPost, "/api/credit-supply/suppliers/"+strconv.Itoa(supplier.Id)+"/share-payout", `{"method":"platform_credit"}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(supplier.Id)}}
	PaySupplierShare(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	assert.InDelta(t, 40.0, payload["data"].(map[string]any)["amount_usd"], 1e-9)
	seller, _ := model.GetUserById(42, false)
	assert.EqualValues(t, int(40*common.QuotaPerUnit), seller.Quota, "paid into the seller's wallet")
	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	assert.Contains(t, recorder.Body.String(), `"share_paid_usd":40`)
	c, recorder = portalContext(t, 1, http.MethodPost, "/api/credit-supply/suppliers/"+strconv.Itoa(supplier.Id)+"/share-payout", `{"method":"platform_credit"}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(supplier.Id)}}
	PaySupplierShare(c)
	assert.Equal(t, http.StatusConflict, recorder.Code)
}

func TestExternalPayoutNeedsAReferenceAndBooksNoCredit(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 7, Username: "cashout", Quota: 0}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	verifySupplierChannel = func(*model.Channel, string, int) (verificationUsage, error) { return verificationUsage{}, nil }
	// sellerFor creates the supplier record on first contact; file the account.
	c, recorder := portalContext(t, 7, http.MethodPut, "/api/supplier/payout-account", `{"method":"wise","holder":"Cashout Ltd","details":"IBAN GB00 0000 1234 5678 9012 34, SWIFT ABCDGB2L","currency":"eur"}`)
	UpdateSupplierPayoutAccount(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	assert.Equal(t, "EUR", payload["data"].(map[string]any)["payout_currency"])

	c, recorder = portalContext(t, 7, http.MethodPost, "/api/supplier/lots", `{"vendor":"openai","face_value_usd":500,"upstream_key":"sk-ok","transfer_rights_confirmed":true}`)
	SubmitSupplierLot(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	lot, err := model.GetCreditLotById(int(payload["data"].(map[string]any)["lot_id"].(float64)))
	require.NoError(t, err)
	assert.Equal(t, model.CreditLotPayoutExternal, lot.PayoutMethod, "a bank/wise/paypal account is an external payout")
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{
		ChannelId: lot.ChannelId, ModelName: "gpt-4o-mini",
		TokenUsage: ratio_setting.TokenUsage{PromptTokens: 1000}, QuotaCharged: int(100 * common.QuotaPerUnit),
	})
	supplier, _ := model.GetCreditSupplierByUserId(7)
	_, err = model.PaySupplierShare(supplier.Id, model.SharePayoutRequest{Actor: "root", Method: model.CreditLotPayoutExternal})
	require.ErrorIs(t, err, model.ErrShareNeedsReference)
	result, err := model.PaySupplierShare(supplier.Id, model.SharePayoutRequest{Actor: "root", Method: model.CreditLotPayoutExternal, Reference: "WISE-20260920-0042"})
	require.NoError(t, err)
	assert.InDelta(t, 50, result.AmountUSD, 1e-9, "openai keys earn their owner half")
	user, _ := model.GetUserById(7, false)
	assert.Zero(t, user.Quota, "cash was sent outside; no platform credit is booked")
}

// UNIFYAPI-FORK: the second way to hand us a key -- contribute it and take a
// share of what it earns instead of selling it. See docs/credit-supply.md.
func TestContributingAKeyTakesAShareInsteadOfAPayment(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "acme", DisplayName: "Acme Labs"}).Error)
	previous := verifySupplierChannel
	t.Cleanup(func() { verifySupplierChannel = previous })
	verifySupplierChannel = func(*model.Channel, string, int) (verificationUsage, error) { return verificationUsage{}, nil }
	previousTerms := model.CreditSupplyTerms2JSONString()
	t.Cleanup(func() { require.NoError(t, model.UpdateCreditSupplyTermsByJSONString(previousTerms)) })
	require.NoError(t, model.UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"revenue_share_rates":{"anthropic":0.5},"min_face_usd":100,"min_share_payout_usd":20}`))
	filePayoutAccount(t, 42, `{"method":"platform_credit"}`)

	// The posted terms carry both offers, so the seller can compare before typing.
	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/terms", "")
	GetSupplierTerms(c)
	terms := decode(t, recorder)["data"].(map[string]any)
	assert.InDelta(t, 0.5, terms["revenue_share_rates"].(map[string]any)["anthropic"], 1e-9)
	assert.Equal(t, "revenue", terms["revenue_share_basis"])
	assert.InDelta(t, 20.0, terms["min_share_payout_usd"], 1e-9)

	// A vendor with no posted share is not taken.
	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"openai","face_value_usd":1000,
		"upstream_key":"sk-proj-x","transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	assert.Equal(t, false, decode(t, recorder)["success"], "no posted share for OpenAI")

	c, recorder = portalContext(t, 42, http.MethodPost, "/api/supplier/lots", `{
		"vendor":"anthropic","face_value_usd":1000,
		"upstream_key":"sk-ant-contributed","models":["claude-sonnet-5"],
		"transfer_rights_confirmed":true
	}`)
	SubmitSupplierLot(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	assert.Equal(t, "revenue_share", data["deal_type"])
	assert.InDelta(t, 0.5, data["revenue_share_pct"], 1e-9)
	assert.Equal(t, "active", data["status"], "nothing to pay, nothing to wait for")

	lot, err := model.GetCreditLotById(int(data["lot_id"].(float64)))
	require.NoError(t, err)
	assert.Zero(t, lot.AcquisitionRate, "we did not buy these credits")
	assert.InDelta(t, 0.5, lot.RevenueSharePct, 1e-9, "the posted share, frozen on the lot")
	assert.Equal(t, model.CreditShareBasisRevenue, lot.RevenueShareBasis)
	assert.Equal(t, model.CreditLotStatusActive, lot.Status)

	// Traffic starts earning at once.
	model.RecordCreditSupplyConsumption(model.CreditSupplyUsage{
		ChannelId: lot.ChannelId, ModelName: "claude-sonnet-5",
		TokenUsage:   ratio_setting.TokenUsage{PromptTokens: 1000},
		QuotaCharged: int(80 * common.QuotaPerUnit),
	})

	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/me", "")
	GetSupplierPortal(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	totals := payload["data"].(map[string]any)["totals"].(map[string]any)
	assert.InDelta(t, 80.0, totals["share_revenue_usd"], 1e-9)
	assert.InDelta(t, 40.0, totals["share_earned_usd"], 1e-9)
	assert.InDelta(t, 40.0, totals["share_unpaid_usd"], 1e-9)
	assert.NotContains(t, recorder.Body.String(), "sk-ant-contributed", "the key stays write-only")

	// The operator settles it, once, for exactly what was owed.
	supplier, err := model.GetCreditSupplierByUserId(42)
	require.NoError(t, err)
	c, recorder = portalContext(t, 1, http.MethodPost,
		"/api/credit-supply/suppliers/"+strconv.Itoa(supplier.Id)+"/share-payout", `{"method":"platform_credit"}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(supplier.Id)}}
	PaySupplierShare(c)
	payload = decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	assert.InDelta(t, 40.0, payload["data"].(map[string]any)["amount_usd"], 1e-9)

	c, recorder = portalContext(t, 1, http.MethodPost,
		"/api/credit-supply/suppliers/"+strconv.Itoa(supplier.Id)+"/share-payout", `{"method":"platform_credit"}`)
	c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(supplier.Id)}}
	PaySupplierShare(c)
	assert.Equal(t, http.StatusConflict, recorder.Code, "a settled balance cannot be settled again")
}

func TestBothTermsEndpointsOfferOnlyTheShare(t *testing.T) {
	setupSupplierPortalTest(t)
	// Buying credits outright was withdrawn, so no endpoint quotes a buy rate
	// to anybody. The operator's copy carries the terms a seller has no
	// business seeing; the seller's copy carries the offer.
	c, recorder := portalContext(t, 1, http.MethodGet, "/api/credit-supply/terms", "")
	GetCreditSupplyTermsAdmin(c)
	admin := recorder.Body.String()
	assert.NotContains(t, admin, `"buy_rates"`)
	assert.NotContains(t, admin, `"platform_credit_bonus"`)
	assert.Contains(t, admin, `"revenue_share_rates"`)
	assert.Contains(t, admin, `"manual_review"`)

	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/terms", "")
	GetSupplierTerms(c)
	seller := recorder.Body.String()
	assert.NotContains(t, seller, `"buy_rates"`)
	assert.Contains(t, seller, `"manual_review"`, "a seller is told whether their key goes live at once")
	assert.Contains(t, seller, `"revenue_share_rates"`)
}

// The seller's trust problem in one test: they cannot audit our revenue, but
// they CAN audit the usage it is computed from, against their own vendor
// console. So the usage we report has to be theirs, all of it, and nothing
// that belongs to anybody else.
func TestASellerCanCheckTheirOwnUsageAndSeesNobodyElses(t *testing.T) {
	setupSupplierPortalTest(t)
	require.NoError(t, model.DB.Create(&model.User{Id: 42, Username: "seller", AffCode: "seller-aff"}).Error)
	require.NoError(t, model.DB.Create(&model.User{Id: 43, Username: "other", AffCode: "other-aff"}).Error)
	mine := &model.CreditSupplier{Name: "Mine", Code: "mine", UserId: 42, PayoutMethod: "platform_credit"}
	theirs := &model.CreditSupplier{Name: "Theirs", Code: "theirs", UserId: 43, PayoutMethod: "platform_credit"}
	require.NoError(t, model.CreateCreditSupplier(mine))
	require.NoError(t, model.CreateCreditSupplier(theirs))
	for id := 1; id <= 3; id++ {
		require.NoError(t, model.DB.Create(&model.Channel{Id: id, Key: "k", Name: "c", Status: 1, Models: "claude-sonnet-5", Group: "default"}).Error)
	}
	for _, spec := range []struct {
		supplier  *model.CreditSupplier
		channelId int
	}{{mine, 1}, {mine, 2}, {theirs, 3}} {
		lot := &model.CreditLot{SupplierId: spec.supplier.Id, Vendor: "anthropic", ChannelId: spec.channelId,
			FaceValueUSD: 1000, RevenueSharePct: 0.5, Status: "active"}
		require.NoError(t, model.CreateCreditLot(lot, "test"))
	}

	now := common.GetTimestamp()
	log := func(channelId int, model_ string, prompt, cached, completion int, quota int, username string) {
		require.NoError(t, model.LOG_DB.Create(&model.Log{
			UserId: 99, Username: username, CreatedAt: now, Type: model.LogTypeConsume,
			ModelName: model_, ChannelId: channelId, Quota: quota,
			PromptTokens: prompt, CachedTokens: cached, CompletionTokens: completion,
		}).Error)
	}
	// Two models on my two keys, plus somebody else's traffic on a third.
	log(1, "claude-sonnet-5", 1000, 400, 200, int(3*common.QuotaPerUnit), "alice")
	log(2, "claude-sonnet-5", 500, 0, 100, int(1*common.QuotaPerUnit), "bob")
	log(1, "claude-opus-4-8", 100, 0, 50, int(6*common.QuotaPerUnit), "alice")
	log(3, "claude-sonnet-5", 9999, 0, 9999, int(99*common.QuotaPerUnit), "carol")

	c, recorder := portalContext(t, 42, http.MethodGet, "/api/supplier/usage/detail?days=7", "")
	GetSupplierUsageDetail(c)
	payload := decode(t, recorder)
	require.Equal(t, true, payload["success"], recorder.Body.String())
	data := payload["data"].(map[string]any)
	rows := data["rows"].([]any)
	require.Len(t, rows, 2, "one row per model per day, my two keys folded together")

	byModel := map[string]map[string]any{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		byModel[row["model"].(string)] = row
	}
	sonnet := byModel["claude-sonnet-5"]
	require.NotNil(t, sonnet)
	assert.EqualValues(t, 2, sonnet["requests"], "both my keys, one model, one day")
	assert.EqualValues(t, 1500, sonnet["prompt_tokens"], "the number their vendor console shows")
	assert.EqualValues(t, 400, sonnet["cached_tokens"])
	assert.EqualValues(t, 300, sonnet["completion_tokens"])
	assert.InDelta(t, 4.0, sonnet["sold_usd"], 1e-9, "what customers paid for it")
	assert.InDelta(t, 2.0, sonnet["share_usd"], 1e-9, "half, at the share their keys were taken on")
	assert.Greater(t, sonnet["list_usd"], 0.0, "priced against the vendor's published rate")
	assert.Equal(t, true, sonnet["priced"])

	totals := data["totals"].(map[string]any)
	assert.EqualValues(t, 3, totals["requests"], "mine only; 4 rows were logged")
	assert.InDelta(t, 10.0, totals["sold_usd"], 1e-9)
	assert.InDelta(t, 5.0, totals["share_usd"], 1e-9)

	// Nothing about who the customers were, ever.
	body := recorder.Body.String()
	for _, leak := range []string{"alice", "bob", "carol", "user_id", "username"} {
		assert.NotContains(t, body, leak, "a seller audits their usage, not our customers")
	}

	// The same rows as a file they can diff against a vendor export.
	c, recorder = portalContext(t, 42, http.MethodGet, "/api/supplier/usage/export?days=7", "")
	ExportSupplierUsageCSV(c)
	csv := recorder.Body.String()
	assert.Contains(t, recorder.Header().Get("Content-Disposition"), "unifyapi-usage-mine-")
	assert.Contains(t, csv, "day,model,requests,prompt_tokens,cached_tokens,cache_write_tokens,completion_tokens,vendor_list_usd,sold_usd,your_share_usd")
	assert.Contains(t, csv, "claude-sonnet-5,2,1500,400,0,300,")
	assert.NotContains(t, csv, "carol")

	// A login with no keys at all is not a seller and gets nothing.
	c, recorder = portalContext(t, 7, http.MethodGet, "/api/supplier/usage/detail", "")
	GetSupplierUsageDetail(c)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}
