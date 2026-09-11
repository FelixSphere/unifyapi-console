/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: contributed keys and the dividend they earn. See
// credit_share_payout.go and docs/credit-supply.md.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contribute wires up a login, a supplier, a channel and an active
// revenue-share lot on it, the way the portal and the operator would.
func contribute(t *testing.T, channelId int, sharePct float64, basis string) (*CreditSupplier, *CreditLot) {
	t.Helper()
	require.NoError(t, DB.Create(&User{Id: 42, Username: "acme", Quota: 0}).Error)
	supplier := seedSupplier(t, "acme")
	supplier.UserId = 42
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	seedSupplierChannel(t, channelId)
	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: channelId,
		FaceValueUSD: 1000, AcquisitionRate: 0, DealType: CreditLotDealRevenueShare,
		RevenueSharePct: sharePct, RevenueShareBasis: basis,
		Source: CreditLotSourceSupplier, PayoutMethod: CreditLotPayoutPlatformCredit,
	}
	require.NoError(t, CreateCreditLot(lot, "acme"))
	return supplier, lot
}

// revenue records one relayed request that the customer was charged usd for.
func revenue(channelId int, usd float64) {
	RecordCreditSupplyConsumption(CreditSupplyUsage{
		ChannelId: channelId, ModelName: "claude-sonnet-5",
		PromptTokens: 1_000_000, QuotaCharged: int(usd * common.QuotaPerUnit),
	})
}

func TestAContributedKeyIsAcceptedWithoutPaymentAndEarnsAsItServes(t *testing.T) {
	setupCreditSupplyTestDB(t)
	_, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	_, err := MarkCreditLotVerified(lot.Id, "system", "key answered", 0)
	require.NoError(t, err)

	// Nothing was sold, so there is nothing to pay for at the till.
	_, err = PayCreditLot(lot.Id, CreditLotPayment{Actor: "root"})
	require.ErrorIs(t, err, ErrCreditLotNothingToPay)

	// Accepting it is still a decision, and still needs the answer to the
	// right-to-transfer question.
	_, err = TransitionCreditLot(lot.Id, CreditLotTransition{To: CreditLotStatusActive, Actor: "root"})
	require.ErrorIs(t, err, ErrCreditLotApprovalNeedsConfirmation)
	active, err := TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	require.Equal(t, CreditLotStatusActive, active.Status)
	channel, err := GetChannelById(7, false)
	require.NoError(t, err)
	assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
	assert.InDelta(t, 1, ratio_setting.GetChannelCostRatio(7), 1e-9,
		"a contributed key has no purchase price to write as a cost multiplier")

	listPrice, ok := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	require.True(t, ok)
	revenue(7, 40)
	revenue(7, 60)

	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, 100, fresh.ShareRevenueUSD, 1e-9, "what customers paid for traffic this key served")
	assert.Zero(t, fresh.ShareCostUSD, "nothing was paid for these credits up front")
	assert.InDelta(t, 50, fresh.EarnedShareUSD(), 1e-9, "half of it")
	assert.InDelta(t, 50, fresh.UnpaidShareUSD(), 1e-9)
	assert.Zero(t, fresh.PayableUSD(), "the dividend is not a purchase payable")
	assert.InDelta(t, listPrice*2, fresh.ConsumedUSD, 1e-9, "the key is still drawn down at list price")

	usage, err := GetCreditLotUsage(lot.Id, 7)
	require.NoError(t, err)
	require.Len(t, usage, 1)
	assert.InDelta(t, 100, usage[0].RevenueUSD, 1e-9)
}

func TestPayingAContributorSettlesExactlyWhatIsOwedAndNoMore(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	_, err = TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	revenue(7, 100)

	// Platform credit lands in the contributor's wallet as part of the same
	// transaction that records the payment.
	result, err := PaySupplierShare(supplier.Id, SharePayoutRequest{
		Actor: "root", Method: CreditLotPayoutPlatformCredit,
	})
	require.NoError(t, err)
	assert.InDelta(t, 50, result.AmountUSD, 1e-9)
	require.Len(t, result.Payouts, 1)
	assert.Equal(t, lot.Id, result.Payouts[0].LotId)
	assert.InDelta(t, 50, result.Payouts[0].EarnedToDateUSD, 1e-9)
	assert.InDelta(t, 100, result.Payouts[0].RevenueToDateUSD, 1e-9)

	var paidUser User
	require.NoError(t, DB.First(&paidUser, "id = ?", 42).Error)
	assert.InDelta(t, 50*common.QuotaPerUnit, float64(paidUser.Quota), 1)

	// Paying again pays nothing: the balance is earned-minus-paid, not a
	// counter that can be pressed twice.
	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutPlatformCredit})
	require.ErrorIs(t, err, ErrNoShareToPay)

	// More traffic, more owed -- and only the new part.
	revenue(7, 60)
	second, err := PaySupplierShare(supplier.Id, SharePayoutRequest{
		Actor: "root", Method: CreditLotPayoutExternal, Reference: "wire-7781",
	})
	require.NoError(t, err)
	assert.InDelta(t, 30, second.AmountUSD, 1e-9)
	assert.NotEqual(t, result.Batch, second.Batch)

	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, 80, fresh.PaidShareUSD, 1e-9)
	assert.Zero(t, fresh.UnpaidShareUSD())

	payouts, err := ListCreditSharePayouts(CreditShareFilter{SupplierId: supplier.Id})
	require.NoError(t, err)
	require.Len(t, payouts, 2)
	assert.Equal(t, "wire-7781", payouts[0].Reference, "newest first")

	events, err := GetCreditLotEvents(lot.Id, 100)
	require.NoError(t, err)
	var sharePaid int
	for _, event := range events {
		if event.EventType == "share_paid" {
			sharePaid++
		}
	}
	assert.Equal(t, 2, sharePaid, "every dividend payment is on the lot's record")
}

func TestAnExternalDividendNeedsAReferenceAndAWalletlessContributorNeedsATransfer(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	_, err = TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	revenue(7, 100)

	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutExternal})
	require.ErrorIs(t, err, ErrShareNeedsReference)
	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: "cheque"})
	require.Error(t, err)
	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{
		Actor: "root", Method: CreditLotPayoutExternal, Reference: "paid via sk-ant-api03-nope",
	})
	require.ErrorIs(t, err, ErrCreditLotSecretInText)

	// Unlink the login: there is no wallet to credit any more.
	supplier.UserId = -1
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutPlatformCredit})
	require.ErrorIs(t, err, ErrShareNeedsWallet)

	// None of the refusals moved the balance.
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Zero(t, fresh.PaidShareUSD)
	assert.InDelta(t, 50, fresh.UnpaidShareUSD(), 1e-9)
}

func TestTheMinimumPayoutHoldsSmallBalancesBackUntilTheOperatorInsists(t *testing.T) {
	setupCreditSupplyTestDB(t)
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"revenue_share_rates":{"anthropic":0.5},"min_share_payout_usd":20,"min_face_usd":100}`))
	supplier, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	_, err = TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	revenue(7, 10) // earns $5, below the $20 minimum

	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutPlatformCredit})
	require.ErrorIs(t, err, ErrShareBelowMinimum)

	forced, err := PaySupplierShare(supplier.Id, SharePayoutRequest{
		Actor: "root", Method: CreditLotPayoutPlatformCredit, Force: true,
	})
	require.NoError(t, err)
	assert.InDelta(t, 5, forced.AmountUSD, 1e-9)
}

// The margin basis exists for the mixed deal: a smaller payment up front plus a
// share of what is left. With nothing paid up front the two bases agree, which
// is why margin is the safe default.
func TestTheMarginBasisNetsOffWhatWePaidUpFront(t *testing.T) {
	setupCreditSupplyTestDB(t)
	require.NoError(t, DB.Create(&User{Id: 42, Username: "acme"}).Error)
	supplier := seedSupplier(t, "acme")
	supplier.UserId = 42
	require.NoError(t, UpdateCreditSupplier(supplier.Id, supplier))
	seedSupplierChannel(t, 7)
	listPrice, ok := ratio_setting.ListPriceUSD("claude-sonnet-5", 1_000_000, 0, 0)
	require.True(t, ok)

	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: listPrice * 10, AcquisitionRate: 0.1,
		DealType: CreditLotDealRevenueShare, RevenueSharePct: 0.5,
		RevenueShareBasis: CreditShareBasisMargin,
		Source:            CreditLotSourceSupplier, PayoutMethod: CreditLotPayoutPlatformCredit,
	}
	require.NoError(t, CreateCreditLot(lot, "acme"))
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	// Something is owed up front, so this one is bought before it is used.
	_, err = TransitionCreditLot(lot.Id, approve("root"))
	require.ErrorIs(t, err, ErrCreditLotNeedsPayment)
	_, err = PayCreditLot(lot.Id, CreditLotPayment{Actor: "root"})
	require.NoError(t, err)
	assert.InDelta(t, 0.1, ratio_setting.GetChannelCostRatio(7), 1e-9)

	revenue(7, 100)
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, listPrice*0.1, fresh.ShareCostUSD, 1e-9, "one request's worth of face value at the rate we paid")
	assert.InDelta(t, (100-listPrice*0.1)*0.5, fresh.EarnedShareUSD(), 1e-9)

	// On the revenue basis the same traffic would split the gross instead.
	fresh.RevenueShareBasis = CreditShareBasisRevenue
	assert.InDelta(t, 50, fresh.EarnedShareUSD(), 1e-9)
}

func TestALotBoughtOutrightEarnsNoDividend(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	lot := &CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 1000, AcquisitionRate: 0.4, Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(lot, "root"))
	assert.Equal(t, CreditLotDealPurchase, lot.DealType, "the default deal is the one that existed before")
	revenue(7, 100)

	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Zero(t, fresh.ShareRevenueUSD, "revenue is only tracked where somebody is owed a share of it")
	assert.Zero(t, fresh.EarnedShareUSD())
	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutExternal, Reference: "x"})
	require.ErrorIs(t, err, ErrNoShareToPay)
}

func TestTheTwoDealsAreValidatedAgainstEachOther(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	base := func() *CreditLot {
		return &CreditLot{
			SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
			FaceValueUSD: 1000, AcquisitionRate: 0.4,
		}
	}
	sold := base()
	sold.RevenueSharePct = 0.5
	require.Error(t, CreateCreditLot(sold, "root"), "a lot bought outright has no dividend")

	shared := base()
	shared.DealType = CreditLotDealRevenueShare
	shared.AcquisitionRate = 0
	require.Error(t, CreateCreditLot(shared, "root"), "a contributed key with no share is a gift, not a deal")

	shared = base()
	shared.DealType = CreditLotDealRevenueShare
	shared.RevenueSharePct = 1.5
	require.Error(t, CreateCreditLot(shared, "root"))

	shared = base()
	shared.DealType = CreditLotDealRevenueShare
	shared.RevenueSharePct = 0.5
	require.Error(t, CreateCreditLot(shared, "root"), "the basis is part of the deal and is recorded with it")

	unknown := base()
	unknown.DealType = "barter"
	require.Error(t, CreateCreditLot(unknown, "root"))
}

func TestPostedRevenueShareTermsAreValidatedAndSnapshotIsWhatCounts(t *testing.T) {
	setupCreditSupplyTestDB(t)
	terms := GetCreditSupplyTerms()
	share, ok := terms.RevenueShareRate("Anthropic")
	require.True(t, ok)
	assert.InDelta(t, 0.5, share, 1e-9)
	assert.Equal(t, CreditShareBasisMargin, terms.ShareBasis())

	require.Error(t, UpdateCreditSupplyTermsByJSONString(`{"buy_rates":{"anthropic":0.2},"revenue_share_rates":{"anthropic":1.4}}`))
	require.Error(t, UpdateCreditSupplyTermsByJSONString(`{"buy_rates":{"anthropic":0.2},"revenue_share_basis":"gut feel"}`))
	require.Error(t, UpdateCreditSupplyTermsByJSONString(`{"buy_rates":{"anthropic":0.2},"min_share_payout_usd":-1}`))

	// Omitting the rates is how the operator stops taking keys on those terms.
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(`{"buy_rates":{"anthropic":0.2}}`))
	_, ok = GetCreditSupplyTerms().RevenueShareRate("anthropic")
	assert.False(t, ok)

	// A key already earning keeps the deal it was taken on.
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"revenue_share_rates":{"anthropic":0.5}}`))
	_, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	require.NoError(t, UpdateCreditSupplyTermsByJSONString(
		`{"buy_rates":{"anthropic":0.2},"revenue_share_rates":{"anthropic":0.1}}`))
	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.InDelta(t, 0.5, fresh.RevenueSharePct, 1e-9, "the terms moved; the deal did not")
}

func TestTheOverviewShowsWhatIsOwedToContributors(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier, lot := contribute(t, 7, 0.5, CreditShareBasisMargin)
	_, err := MarkCreditLotVerified(lot.Id, "system", "ok", 0)
	require.NoError(t, err)
	_, err = TransitionCreditLot(lot.Id, approve("root"))
	require.NoError(t, err)
	revenue(7, 100)

	overview, err := GetCreditSupplyOverview()
	require.NoError(t, err)
	assert.InDelta(t, 100, overview.Share.RevenueUSD, 1e-9)
	assert.InDelta(t, 50, overview.Share.EarnedUSD, 1e-9)
	assert.InDelta(t, 50, overview.Share.UnpaidUSD, 1e-9)
	assert.Zero(t, overview.Share.PaidUSD)
	assert.Equal(t, 1, overview.Share.Lots)
	require.Len(t, overview.ByVendor, 1)
	assert.InDelta(t, 50, overview.ByVendor[0].ShareUnpaidUSD, 1e-9)
	require.NotEmpty(t, overview.Attention, "a contributor owed more than the minimum needs paying")

	_, err = PaySupplierShare(supplier.Id, SharePayoutRequest{Actor: "root", Method: CreditLotPayoutPlatformCredit})
	require.NoError(t, err)
	overview, err = GetCreditSupplyOverview()
	require.NoError(t, err)
	assert.Zero(t, overview.Share.UnpaidUSD)
	assert.InDelta(t, 50, overview.Share.PaidUSD, 1e-9)
	assert.Empty(t, overview.Attention)
}
