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

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A key pasted into the payout details is a credential in plain text next to
// the operator's notes, which is exactly what the note guard exists to stop.
func TestPayoutDetailsRefuseSomethingThatLooksLikeAKey(t *testing.T) {
	setupCreditSupplyTestDB(t)
	supplier := seedSupplier(t, "acme")
	seedSupplierChannel(t, 7)
	err := CreateCreditLot(&CreditLot{
		SupplierId: supplier.Id, Vendor: "anthropic", ChannelId: 7,
		FaceValueUSD: 100, RevenueSharePct: 0.4,
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
		FaceValueUSD: 1000, RevenueSharePct: 0.4, Status: CreditLotStatusActive,
	}
	require.NoError(t, CreateCreditLot(lot, "test"))

	// Warm this instance's cache, then let another instance suspend the lot.
	_, ok := activeLotForChannel(7)
	require.True(t, ok)
	require.NoError(t, DB.Model(&CreditLot{}).Where("id = ?", lot.Id).
		Update("status", CreditLotStatusSuspended).Error)

	RecordCreditSupplyConsumption(CreditSupplyUsage{ChannelId: 7, ModelName: "claude-sonnet-5", TokenUsage: ratio_setting.TokenUsage{PromptTokens: 1_000_000, CachedTokens: 0, CompletionTokens: 0}})

	fresh, err := GetCreditLotById(lot.Id)
	require.NoError(t, err)
	assert.Zero(t, fresh.ConsumedUSD, "a suspended lot is not drawn down")
	usage, err := GetCreditLotUsage(lot.Id, 7)
	require.NoError(t, err)
	assert.Empty(t, usage, "the daily ledger must not show a draw-down that never happened")
}
