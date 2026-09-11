/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: regressions found while reviewing the credit supply. Each
// test names the wrong behaviour it pins down, not the code that produces it.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lot bought outright is paid in full at activation, so nothing is owed as
// it is consumed. Reporting consumption x rate as payable invoices the same
// credits twice: once at the till, once on the statement screen.
func TestPaidLotOwesNothingAsItIsConsumed(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	supplier.UserId = 42
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	require.NoError(t, DB.Create(&User{Id: 42, Username: "acme"}).Error)
	seedSupplierChannel(t, 7)
	listPrice, ok := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	require.True(t, ok)

	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: listPrice * 4, AcquisitionRate: 0.4,
		Source: CreditLotSourceSupplier, PayoutMethod: CreditLotPayoutPlatformCredit,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	paid, err := PayCreditLot(lot.Id, CreditLotPayment{Actor: "root"})
	require.NoError(t, err)
	assert.InDelta(t, listPrice*4*0.4, paid.PaidUSD, 1e-9)

	RecordCreditSupplyConsumption(CreditSupplyUsage{ChannelId: 7, ModelName: "claude-sonnet-5", PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Zero(t, fresh.PayableUSD(), "the sale was paid at activation; nothing accrues on top of it")

	overview, err := GetCreditSupplyOverview()
	require.NoError(t, err)
	assert.Zero(t, overview.PayableUSD, "the pool owes nothing for a sale it has already paid for")
	require.Len(t, overview.ByVendor, 1)
	assert.Zero(t, overview.ByVendor[0].PayableUSD)
}

// The posted terms are a quote, and a quote a seller accepted must survive the
// operator editing the terms before the payment is made.
func TestPlatformCreditBonusIsFrozenWhenTheSaleIsMade(t *testing.T) {
	setupCreditSupplyTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 42, Username: "acme"}).Error)
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"platform_credit_bonus":0.1,"min_face_usd":100,"channel_priority":10}`))

	supplier := seedSupplier(t, "acme")
	supplier.UserId = 42
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	channel := &Channel{Type: 14, Key: "sk-ant-seller", Name: "acme", Models: "claude-sonnet-5"}
	lot := &CreditLot{
		Vendor: "anthropic", FaceValueUSD: 1000, AcquisitionRate: 0.2,
		PayoutMethod: CreditLotPayoutPlatformCredit,
	}
	require.NoError(t, SubmitSupplierCreditLot(supplier, channel, lot, "acme"))
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)

	// The operator withdraws the bonus after the sale was quoted and accepted.
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"platform_credit_bonus":0,"min_face_usd":100,"channel_priority":10}`))

	paid, err := PayCreditLot(lot.Id, CreditLotPayment{Actor: "root"})
	require.NoError(t, err)
	assert.InDelta(t, 220.0, paid.PaidUSD, 1e-9, "the seller is paid the terms they accepted, not today's")
}

// A key pasted into the payout details is a credential in plain text next to
// the operator's notes, which is exactly what the note guard exists to stop.
func TestPayoutDetailsRefuseSomethingThatLooksLikeAKey(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	err := CreateCreditLot(&CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 100, AcquisitionRate: 0.4,
		PayoutMethod: CreditLotPayoutExternal, PayoutAccount: "wire to sk-ant-api03-oops",
	}, "test")
	require.ErrorIs(t, err, ErrCreditLotSecretInText)
}

// Draw-down and the daily ledger have to agree. When the conditional update
// finds the lot is no longer active -- retired or suspended on another
// instance while this one still had it cached -- nothing was drawn, so
// nothing may be charted as drawn.
func TestNoDailyUsageIsRecordedWhenTheDrawDownDidNotLand(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 1000, AcquisitionRate: 0.4, Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))

	// Warm this instance's cache, then let another instance suspend the lot.
	_, ok := activeLotForChannel(7)
	require.True(t, ok)
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).
		Update("status", CreditLotStatusSuspended).Error)

	RecordCreditSupplyConsumption(CreditSupplyUsage{ChannelId: 7, ModelName: "claude-sonnet-5", PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0})

	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Zero(t, fresh.ConsumedUSD, "a suspended lot is not drawn down")
	usage, err := GetCreditLotUsage(lot.Id, 7)
	require.NoError(t, err)
	assert.Empty(t, usage, "the daily ledger must not show a draw-down that never happened")
}

// Raising the face value of a lot we have already bought and paid for hands
// the seller's remaining credits over for free, and rewriting its acquisition
// rate rewrites the cost basis of traffic already reconciled.
func TestAPaidSaleCannotBeRepricedOrEnlarged(t *testing.T) {
	setupCreditSupplyTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 42, Username: "acme"}).Error)
	supplier := seedSupplier(t, "acme")
	supplier.UserId = 42
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	seedSupplierChannel(t, 7)
	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 1000, AcquisitionRate: 0.4,
		Source: CreditLotSourceSupplier, PayoutMethod: CreditLotPayoutPlatformCredit,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	_, err = PayCreditLot(lot.Id, CreditLotPayment{Actor: "root"})
	require.NoError(t, err)

	bigger := *lot
	bigger.FaceValueUSD = 5000
	require.Error(t, UpdateCreditLot(lot.Id, &bigger, "root"),
		"more credit than was paid for is a new sale, not an edit")

	cheaper := *lot
	cheaper.AcquisitionRate = 0.1
	require.Error(t, UpdateCreditLot(lot.Id, &cheaper, "root"),
		"the rate a sale was settled at is history")

	// Everything else about a paid lot stays editable.
	renamed := *lot
	renamed.Note = "seller confirmed the balance by screenshot"
	renamed.LowWaterUSD = 50
	require.NoError(t, UpdateCreditLot(lot.Id, &renamed, "root"))
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, 50.0, fresh.LowWaterUSD, 1e-9)
	assert.InDelta(t, 1000.0, fresh.FaceValueUSD, 1e-9)
	_ = common.GetTimestamp()
}
