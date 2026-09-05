/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupCreditSupplyTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&Channel{}, &Ability{}, &Option{}, &PricingConfigHistory{},
		&CreditSupplier{}, &CreditLot{}, &CreditLotUsage{}, &CreditLotEvent{},
	))
	previous := DB
	previousType := common.MainDatabaseType()
	previousCost := ratio_setting.ChannelCostRatio2JSONString()
	previousHook := CreditLotEventHook
	previousMemoryCache := common.MemoryCacheEnabled
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	invalidateChannelSupplierIndex()
	channelLotCache.Lock()
	channelLotCache.entries = map[int]channelLotEntry{}
	channelLotCache.Unlock()
	t.Cleanup(func() {
		DB = previous
		common.OptionMap = previousOptionMap
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled = previousMemoryCache
		CreditLotEventHook = previousHook
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(previousCost))
		invalidateChannelSupplierIndex()
	})
}

func seedSupplierChannel(t *testing.T, id int) *Channel {
	t.Helper()
	channel := &Channel{
		Id: id, Type: 14, Key: "sk-supplier", Status: common.ChannelStatusEnabled,
		Name: "supplier-anthropic", Models: "claude-sonnet-5", Group: "default",
	}
	require.NoError(t, DB.Create(channel).Error)
	return channel
}

func approve(actor string) CreditLotTransition {
	return CreditLotTransition{To: CreditLotStatusActive, Actor: actor, TransferRightsConfirmed: true}
}

func seedSupplier(t *testing.T, code string) *CreditSupplier {
	t.Helper()
	supplier := &CreditSupplier{Name: "Acme Credits", Code: code, ContactEmail: "ops@acme.example"}
	require.NoError(t, CreateCreditSupplier(supplier))
	return supplier
}

func TestCreditSupplierValidationAndOneLoginPerSupplier(t *testing.T) {
	setupCreditSupplyTestDB(t)

	require.Error(t, CreateCreditSupplier(&CreditSupplier{Name: "", Code: "acme"}))
	require.Error(t, CreateCreditSupplier(&CreditSupplier{Name: "Acme", Code: "Bad Code!"}))
	require.Error(t, CreateCreditSupplier(&CreditSupplier{Name: "Acme", Code: "acme", ContactEmail: "nope"}))

	first := &CreditSupplier{Name: "Acme", Code: "ACME", UserId: 42}
	require.NoError(t, CreateCreditSupplier(first))
	assert.Equal(t, "acme", first.Code, "codes are normalised to lowercase")
	assert.Equal(t, "supplier:acme", first.CounterpartyKey())

	second := &CreditSupplier{Name: "Other", Code: "other", UserId: 42}
	err := CreateCreditSupplier(second)
	require.ErrorIs(t, err, ErrCreditSupplierUserTaken)

	// Two admin-managed suppliers with no login must coexist: 0 is not a claim.
	require.NoError(t, CreateCreditSupplier(&CreditSupplier{Name: "A", Code: "aaa"}))
	require.NoError(t, CreateCreditSupplier(&CreditSupplier{Name: "B", Code: "bbb"}))

	found, err := GetCreditSupplierByUserId(42)
	require.NoError(t, err)
	assert.Equal(t, first.Id, found.Id)
	_, err = GetCreditSupplierByUserId(0)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCreditLotChannelBindingIsExclusiveWhileLive(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	now := common.GetTimestamp()

	require.ErrorIs(t, CreateCreditLot(&CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", FaceValueUSD: 100, AcquisitionRate: 0.5,
		Status: CreditLotStatusActive,
	}, "test"), ErrCreditLotNeedsChannel, "active lots must be bound")

	require.Error(t, CreateCreditLot(&CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 99, FaceValueUSD: 100, AcquisitionRate: 0.5,
	}, "test"), "a channel that does not exist cannot be bound")

	require.Error(t, CreateCreditLot(&CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 100, AcquisitionRate: 1.2,
	}, "test"), "paying above face value is a typo, not a deal")

	first := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 100, AcquisitionRate: 0.5,
		ExpiresAt: now + 3600, Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(first, "test"))
	assert.InDelta(t, 0.5, ratio_setting.GetChannelCostRatio(7), 1e-9,
		"activating a lot writes its acquisition rate into the channel cost ratio")

	second := &CreditLot{SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 50, AcquisitionRate: 0.4}
	require.ErrorIs(t, CreateCreditLot(second, "test"), ErrCreditLotChannelBound)

	// Once the first lot is retired the channel is free again.
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", first.Id).Update("status", CreditLotStatusExhausted).Error)
	require.NoError(t, CreateCreditLot(second, "test"))
}

func TestCreditLotTransitionsFollowTheLifecycle(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	channel := seedSupplierChannel(t, 7)
	// A supplier-submitted lot arrives with a disabled channel.
	require.NoError(t, DB.Model(channel).Update("status", common.ChannelStatusManuallyDisabled).Error)

	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 100, AcquisitionRate: 0.6,
		Source: CreditLotSourceSupplier,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))
	assert.Equal(t, CreditLotStatusPending, lot.Status)
	assert.InDelta(t, 1, ratio_setting.GetChannelCostRatio(7), 1e-9, "a pending lot has not touched pricing yet")

	_, err := TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusSuspended, Actor: "test", Reason: "supplier asked us to pause"})
	require.ErrorIs(t, err, ErrCreditLotTransition, "pending cannot be suspended, only approved or rejected")

	approved, err := TransitionCreditLot(lot.Id, approve("test"))
	require.NoError(t, err)
	assert.Equal(t, CreditLotStatusActive, approved.Status)
	assert.InDelta(t, 0.6, ratio_setting.GetChannelCostRatio(7), 1e-9)
	reloaded, err := GetChannelById(7, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, reloaded.Status, "approval turns the channel on")

	suspended, err := TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusSuspended, Actor: "test", Reason: "supplier asked us to pause"})
	require.NoError(t, err)
	assert.Equal(t, CreditLotStatusSuspended, suspended.Status)
	reloaded, _ = GetChannelById(7, false)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status, "suspension takes the channel out")

	_, err = TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusRejected, Actor: "test", Reason: "provenance unclear"})
	require.ErrorIs(t, err, ErrCreditLotTransition)

	// Exhausted lots come back only once there is something left to draw.
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).
		Updates(map[string]interface{}{"status": CreditLotStatusExhausted, "consumed_usd": 100}).Error)
	_, err = TransitionCreditLot(lot.Id, approve("test"))
	require.ErrorIs(t, err, ErrCreditLotTransition)
	require.NoError(t, UpdateCreditLot(lot.Id, &CreditLot{
		Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 250, AcquisitionRate: 0.6,
	}, "test"))
	reactivated, err := TransitionCreditLot(lot.Id, approve("test"))
	require.NoError(t, err)
	assert.Equal(t, CreditLotStatusActive, reactivated.Status)
	assert.InDelta(t, 150, reactivated.RemainingUSD(), 1e-9)
}

func TestConsumptionDrawsDownAtListPriceAndRetiresOnExhaustion(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	// Set a cost ratio deliberately different from the acquisition rate so
	// the test can tell list price from upstream cost.
	listPrice, ok := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	require.True(t, ok, "the test model must be in the compiled catalogue")
	require.Greater(t, listPrice, 0.0)

	var events []string
	CreditLotEventHook = func(lot CreditLot, event string) { events = append(events, event) }

	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: listPrice * 2.5, AcquisitionRate: 0.4, LowWaterUSD: listPrice,
		Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))
	assert.InDelta(t, 0.4, ratio_setting.GetChannelCostRatio(7), 1e-9)

	// One million prompt tokens on the bound channel: draws exactly the list
	// price, not the discounted upstream cost.
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, listPrice, fresh.ConsumedUSD, 1e-9)
	assert.InDelta(t, listPrice*0.4, fresh.PayableUSD(), 1e-9)
	assert.Equal(t, CreditLotStatusActive, fresh.Status)
	assert.Empty(t, events, "well above low water")

	// Traffic on an unrelated channel is not the supplier's.
	seedSupplierChannel(t, 8)
	RecordCreditSupplyConsumption(8, "claude-sonnet-5", 1_000_000, 0, 0)
	fresh, _ = GetCreditLotById(lot.Id)
	assert.InDelta(t, listPrice, fresh.ConsumedUSD, 1e-9)

	// A model the catalogue cannot price draws nothing and is counted.
	RecordCreditSupplyConsumption(7, "definitely-not-a-model", 1_000_000, 0, 0)
	fresh, _ = GetCreditLotById(lot.Id)
	assert.InDelta(t, listPrice, fresh.ConsumedUSD, 1e-9)
	assert.EqualValues(t, 1, fresh.UnpricedRequests)

	// Second million crosses the low-water mark: one notification, once.
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1, 0, 0)
	fresh, _ = GetCreditLotById(lot.Id)
	assert.Equal(t, []string{CreditLotEventLowWater}, events)
	assert.NotZero(t, fresh.LowWaterNotifiedAt)

	// Third million exhausts the lot: retired, channel auto-disabled, one event.
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	fresh, _ = GetCreditLotById(lot.Id)
	assert.Equal(t, CreditLotStatusExhausted, fresh.Status)
	assert.NotZero(t, fresh.RetiredAt)
	assert.GreaterOrEqual(t, fresh.ConsumedUSD, fresh.FaceValueUSD)
	channel, err := GetChannelById(7, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusAutoDisabled, channel.Status)
	assert.Equal(t, []string{CreditLotEventLowWater, CreditLotEventExhausted}, events)

	// Further traffic on the retired channel is ignored, not double-counted.
	consumedAtRetirement := fresh.ConsumedUSD
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	fresh, _ = GetCreditLotById(lot.Id)
	assert.InDelta(t, consumedAtRetirement, fresh.ConsumedUSD, 1e-9)
	assert.Len(t, events, 2)

	// The daily row accumulated every priced request on the lot.
	usage, err := GetCreditLotUsage(lot.Id, 7)
	require.NoError(t, err)
	require.Len(t, usage, 1)
	assert.Equal(t, creditSupplyDay(common.GetTimestamp()), usage[0].Day)
	assert.EqualValues(t, 5, usage[0].Requests, "4 priced + 1 unpriced requests before retirement")
	assert.InDelta(t, consumedAtRetirement, usage[0].FaceUSD, 1e-9)
}

func TestConsumptionOnAnExpiredLotRetiresItWithoutDrawing(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	var events []string
	CreditLotEventHook = func(lot CreditLot, event string) { events = append(events, event) }

	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 1000, AcquisitionRate: 0.5,
		ExpiresAt: common.GetTimestamp() + 60, Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))
	// Expire it underneath the cache.
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).Update("expires_at", common.GetTimestamp()-1).Error)
	invalidateChannelLot(7)

	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Equal(t, CreditLotStatusExpired, fresh.Status)
	assert.Zero(t, fresh.ConsumedUSD, "an expired lot must not be drawn down")
	assert.Equal(t, []string{CreditLotEventExpired}, events)
	channel, _ := GetChannelById(7, false)
	assert.Equal(t, common.ChannelStatusAutoDisabled, channel.Status)

	_, err = TransitionCreditLot(lot.Id, approve("test"))
	require.ErrorIs(t, err, ErrCreditLotTransition, "still expired")
	require.NoError(t, UpdateCreditLot(lot.Id, &CreditLot{
		Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 1000, AcquisitionRate: 0.5, ExpiresAt: 0,
	}, "test"))
	back, err := TransitionCreditLot(lot.Id, approve("test"))
	require.NoError(t, err)
	assert.Equal(t, CreditLotStatusActive, back.Status)
	assert.Zero(t, back.RetiredAt)
}

func TestChannelSupplierLookupAttributesBoundChannels(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	seedSupplierChannel(t, 8)

	_, _, ok := LookupChannelSupplier(7)
	assert.False(t, ok, "unbound channels keep their host-derived vendor")

	lot := &CreditLot{SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 100, AcquisitionRate: 0.5, Status: CreditLotStatusActive}
	require.NoError(t, CreateCreditLot(lot, "test"))

	key, label, ok := LookupChannelSupplier(7)
	require.True(t, ok)
	assert.Equal(t, "supplier:acme", key)
	assert.Equal(t, "Acme Credits", label)
	assert.True(t, IsSupplierCounterparty(key))
	assert.False(t, IsSupplierCounterparty("anthropic"))
	_, _, ok = LookupChannelSupplier(8)
	assert.False(t, ok)

	// Exhausted lots still attribute: that traffic is still owed.
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).Update("status", CreditLotStatusExhausted).Error)
	invalidateChannelSupplierIndex()
	_, _, ok = LookupChannelSupplier(7)
	assert.True(t, ok)

	// Rejected lots do not.
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).Update("status", CreditLotStatusRejected).Error)
	invalidateChannelSupplierIndex()
	_, _, ok = LookupChannelSupplier(7)
	assert.False(t, ok)
}

func TestCreditSupplyOverviewAggregatesAndFlagsAttention(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	seedSupplierChannel(t, 8)
	seedSupplierChannel(t, 9)
	now := common.GetTimestamp()

	require.NoError(t, CreateCreditLot(&CreditLot{SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 1000, AcquisitionRate: 0.5, Status: CreditLotStatusActive}, "test"))
	require.NoError(t, CreateCreditLot(&CreditLot{SupplierId: supplier.Id, Vendor: "openai", ChannelId: 8,
		FaceValueUSD: 500, AcquisitionRate: 0.4, ExpiresAt: now + 2*86400, Status: CreditLotStatusActive}, "test"))
	pending := &CreditLot{SupplierId: supplier.Id, Vendor: "openai", ChannelId: 9,
		FaceValueUSD: 200, AcquisitionRate: 0.3, Source: CreditLotSourceSupplier}
	require.NoError(t, CreateCreditLot(pending, "test"))
	require.NoError(t, DB.Model(&CreditLot{}).Where("channel_id = 7").Update("consumed_usd", 400).Error)

	overview, err := GetCreditSupplyOverview()
	require.NoError(t, err)
	assert.Equal(t, 1, overview.Suppliers)
	assert.Equal(t, map[string]int{"active": 2, "pending": 1}, overview.LotsByStatus)
	assert.InDelta(t, 1700, overview.FaceUSD, 1e-9)
	assert.InDelta(t, 400, overview.ConsumedUSD, 1e-9)
	assert.InDelta(t, 1300, overview.RemainingUSD, 1e-9)
	assert.InDelta(t, 200, overview.PayableUSD, 1e-9)
	require.Len(t, overview.ByVendor, 2)
	assert.Equal(t, "anthropic", overview.ByVendor[0].Vendor, "largest face value first")
	assert.InDelta(t, 700, overview.ByVendor[1].FaceUSD, 1e-9)

	attention := map[int]bool{}
	for _, lot := range overview.Attention {
		attention[lot.ChannelId] = true
	}
	assert.Equal(t, map[int]bool{8: true, 9: true}, attention,
		"expiring within a week and pending approval need a human; a healthy lot does not")
}

func TestListPriceIgnoresChannelPurchasingRatio(t *testing.T) {
	previous := ratio_setting.ChannelCostRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(previous)) })
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"7":0.25}`))

	list, ok := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 200_000, 100_000)
	require.True(t, ok)
	cost, ok := ratio_setting.UpstreamCostUSD("claude-sonnet-5", 7, 1_000_000, 200_000, 100_000)
	require.True(t, ok)
	assert.InDelta(t, list*0.25, cost, 1e-9)
	_, ok = ratio_setting.ListPriceUSD("not-a-model", 1, 0, 0)
	assert.False(t, ok)
}

func TestApprovalIsRefusedWithoutTheTransferConfirmation(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	lot := &CreditLot{SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: 100, AcquisitionRate: 0.5, Source: CreditLotSourceSupplier}
	require.NoError(t, CreateCreditLot(lot, "portal:user:9"))

	_, err := TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusActive, Actor: "root"})
	require.ErrorIs(t, err, ErrCreditLotApprovalNeedsConfirmation)
	_, err = TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusRejected, Actor: "root"})
	require.ErrorIs(t, err, ErrCreditLotReasonRequired, "a rejection without a reason tells the supplier nothing")
	_, err = TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusRejected, Actor: "root", Reason: "see sk-ant-abc123 in your mail"})
	require.ErrorIs(t, err, ErrCreditLotSecretInText)

	approved, err := TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	assert.Equal(t, "root", approved.ApprovedBy)
	assert.NotZero(t, approved.ApprovedAt)
	assert.Equal(t, CreditLotAttestationVersion, approved.AttestationVersion)
	assert.Equal(t, "portal:user:9", approved.AttestedBy, "the creator attested; the operator approved")

	suspended, err := TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusSuspended, Actor: "root", Reason: "vendor asked for verification"})
	require.NoError(t, err)
	assert.Equal(t, "vendor asked for verification", suspended.StatusReason)
	back, err := TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusActive, Actor: "root"})
	require.NoError(t, err, "reactivating a suspended lot is not a fresh approval")
	assert.Empty(t, back.StatusReason)

	events, err := GetCreditLotEvents(lot.Id, 0)
	require.NoError(t, err)
	types := make([]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		types = append(types, events[i].EventType+":"+events[i].ToStatus)
	}
	assert.Equal(t, []string{"created:pending", "transition:active", "transition:suspended", "transition:active"}, types)
	assert.Equal(t, "vendor asked for verification", events[1].Message)
}

func TestSecretsAreRefusedInFreeText(t *testing.T) {
	setupCreditSupplyTestDB(t)
	err := CreateCreditSupplier(&CreditSupplier{Name: "Acme", Code: "acme", PayoutTerms: "wire; key sk-proj-abcdef"})
	require.ErrorIs(t, err, ErrCreditLotSecretInText)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	err = CreateCreditLot(&CreditLot{SupplierId: supplier.Id, Vendor: "openai", ChannelId: 7, FaceValueUSD: 10, AcquisitionRate: 0.5, Note: "Bearer eyJhbGciOi..."}, "test")
	require.ErrorIs(t, err, ErrCreditLotSecretInText)
	assert.False(t, textLooksLikeProviderSecret("monthly wire, net 15"))
}

func TestRetirementLeavesAnAuditEvent(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	listPrice, _ := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	lot := &CreditLot{SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7, FaceValueUSD: listPrice / 2, AcquisitionRate: 0.5, Status: CreditLotStatusActive}
	require.NoError(t, CreateCreditLot(lot, "root"))
	RecordCreditSupplyConsumption(7, "claude-sonnet-5", 1_000_000, 0, 0)
	events, err := GetCreditLotEvents(lot.Id, 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, "retired", events[0].EventType)
	assert.Equal(t, "system", events[0].Actor)
	assert.Equal(t, CreditLotStatusExhausted, events[0].ToStatus)
}

func TestSupplierApplicationBecomesPendingAndCannotSubmitUntilApproved(t *testing.T) {
	setupCreditSupplyTestDB(t)
	_, err := ApplyForCreditSupplier(9, CreditSupplierApplication{Name: "Acme Labs", ContactEmail: "ops@acme.example"})
	require.Error(t, err, "attestation is required")
	_, err = ApplyForCreditSupplier(9, CreditSupplierApplication{Name: "Acme Labs", Attested: true})
	require.Error(t, err, "contact email is required")

	applied, err := ApplyForCreditSupplier(9, CreditSupplierApplication{
		Name: "Acme Labs (Tel Aviv)!", ContactEmail: "ops@acme.example", Note: "~$5k Anthropic startup credits", Attested: true,
	})
	require.NoError(t, err)
	assert.Equal(t, CreditSupplierStatusPending, applied.Status)
	assert.Equal(t, "acme-labs-tel-aviv", applied.Code)
	assert.Equal(t, CreditLotAttestationVersion, applied.AttestationVersion)
	assert.NotZero(t, applied.AttestedAt)

	_, err = ApplyForCreditSupplier(9, CreditSupplierApplication{Name: "Again", ContactEmail: "x@y.z", Attested: true})
	require.Error(t, err, "one application per login")
	second, err := ApplyForCreditSupplier(10, CreditSupplierApplication{Name: "Acme Labs (Tel Aviv)", ContactEmail: "b@acme.example", Attested: true})
	require.NoError(t, err)
	assert.Equal(t, "acme-labs-tel-aviv-2", second.Code, "codes stay unique")

	seedSupplierChannel(t, 7)
	channel := &Channel{Type: 14, Key: "sk-pending", Name: "pending", Models: "claude-sonnet-5", Group: "default"}
	lot := &CreditLot{Vendor: "anthropic", FaceValueUSD: 100, AcquisitionRate: 0.5}
	err = SubmitSupplierCreditLot(applied, channel, lot, "user:9")
	require.ErrorIs(t, err, ErrCreditSupplierSuspended, "pending suppliers cannot submit lots")

	// Rejecting needs a reason; approving clears it.
	patch := *applied
	patch.Status = CreditSupplierStatusRejected
	require.Error(t, UpdateCreditSupplier(applied.Id, &patch))
	patch.StatusReason = "could not verify the account"
	require.NoError(t, UpdateCreditSupplier(applied.Id, &patch))
	rejected, _ := GetCreditSupplierById(applied.Id)
	assert.Equal(t, "could not verify the account", rejected.StatusReason)
	patch.Status = CreditSupplierStatusActive
	require.NoError(t, UpdateCreditSupplier(applied.Id, &patch))
	approved, _ := GetCreditSupplierById(applied.Id)
	assert.Equal(t, CreditSupplierStatusActive, approved.Status)
	assert.Empty(t, approved.StatusReason)
	require.NoError(t, SubmitSupplierCreditLot(approved, channel, lot, "user:9"))
	assert.Equal(t, CreditLotStatusPending, lot.Status)
}
