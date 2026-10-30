/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package helper

// ModelPriceHelperPerCall had ZERO test coverage, and it prices the most
// expensive things we sell: the video models bill per request, not per token,
// at up to $10.70 a call. Every discount layer the operator asked about meets
// the customer here just as it does on the token path -- the pricing group's
// ratio, and the per-model customer contract that replaces it -- so an error
// here overcharges or undercharges by the whole discount on the dearest
// requests in the catalogue.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// quotaForUSD is the quota a dollar amount must come to, computed the way the
// caller does rather than copied from it.
func quotaForUSD(usd float64) int {
	quota, _ := common.QuotaFromFloatStrict(usd * common.QuotaPerUnit)
	return quota
}

func TestAPerCallModelBillsItsListPriceAtGroupRatioOne(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"wan3.0-video":0.10}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	ctx, info := billingContextFor(t, "wan3.0-video", "default")
	price, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)

	assert.True(t, price.UsePrice, "a per-call model must bill from its price, not a token ratio")
	assert.InDelta(t, 0.10, price.ModelPrice, 1e-9)
	assert.Equal(t, quotaForUSD(0.10), price.Quota)
	assert.False(t, price.FreeModel)
}

// The partnership discount is not a mechanism of its own: a provisioned
// customer gets GroupRatio 0.9 and that ratio multiplies the per-call price
// like any other. This is the number a partnership customer is actually
// charged for one video.
func TestAPartnershipCustomersGroupRatioDiscountsThePerCallPrice(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"wan3.0-video":0.10}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"Nusa Labs":0.9}`))

	ctx, info := billingContextFor(t, "wan3.0-video", "Nusa Labs")
	price, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)

	assert.InDelta(t, 0.9, price.GroupRatioInfo.GroupRatio, 1e-9)
	assert.Equal(t, quotaForUSD(0.09), price.Quota, "90% of $0.10, not $0.10")
}

// A per-model customer contract REPLACES the group ratio rather than stacking
// with it -- 0.8 over a group at 0.9 is 0.8, never 0.72 -- and it re-bases on
// the catalogue's published per-call price so the contract is a multiple of
// LIST, not of whatever the price map happened to hold.
func TestACustomerContractReplacesTheGroupRatioOnThePerCallPath(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"wan3.0-video":0.10}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"Nusa Labs":0.9}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(
		`{"Nusa Labs":{"wan3.0-video":0.8}}`))

	ctx, info := billingContextFor(t, "wan3.0-video", "Nusa Labs")
	price, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)

	entry, ok := ratio_setting.CatalogEntryFor("wan3.0-video")
	require.True(t, ok)
	require.Greater(t, entry.PerCallUSD, 0.0)

	assert.InDelta(t, 0.8, price.GroupRatioInfo.GroupRatio, 1e-9,
		"the contract is final: 0.8, not 0.8 x 0.9")
	assert.InDelta(t, entry.PerCallUSD, price.ModelPrice, 1e-9,
		"and it is a multiple of the catalogue's list price")
	assert.Equal(t, quotaForUSD(entry.PerCallUSD*0.8), price.Quota)
}

// A group ratio of zero is how an operator makes a model free for a group.
// Both halves matter and they are controlled by a setting most people never
// look at: the CHARGE is zero either way, but the FreeModel flag -- which is
// what suppresses pre-consumption downstream -- is only raised when
// EnableFreeModelPreConsume is off. Writing that down because the obvious
// assumption (ratio zero implies FreeModel) is wrong on the default setting.
func TestAZeroGroupRatioChargesNothingUnderEitherPreConsumeSetting(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"wan3.0-video":0.10}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"Freebie":0}`))

	quotaSetting := operation_setting.GetQuotaSetting()
	original := quotaSetting.EnableFreeModelPreConsume
	t.Cleanup(func() { quotaSetting.EnableFreeModelPreConsume = original })

	for _, tc := range []struct {
		name          string
		preConsume    bool
		wantFreeModel bool
	}{
		{"pre-consume on (the default)", true, false},
		{"pre-consume off", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quotaSetting.EnableFreeModelPreConsume = tc.preConsume

			ctx, info := billingContextFor(t, "wan3.0-video", "Freebie")
			price, err := ModelPriceHelperPerCall(ctx, info)
			require.NoError(t, err)

			assert.Equal(t, 0, price.Quota, "a zero ratio must never charge, whatever the setting")
			assert.Equal(t, tc.wantFreeModel, price.FreeModel)
		})
	}
}

// A model with neither a price nor a ratio must be refused, not billed as
// free. Charging nothing for an unpriced model is the failure mode that costs
// real money, because it looks like success.
func TestAModelWithNoBillingConfigIsRefusedRatherThanBilledAsFree(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{}`))

	ctx, info := billingContextFor(t, "a-model-nobody-priced", "default")
	_, err := ModelPriceHelperPerCall(ctx, info)
	assert.Error(t, err, "an unpriced model must fail loudly, never bill as zero")
}

// HasModelBillingConfig is the question "can we charge for this at all", asked
// before a request is accepted. It had no coverage either.
func TestHasModelBillingConfigAnswersForEachWayAModelCanBePriced(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"priced-per-call":0.5}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"priced-per-token":2}`))

	assert.True(t, HasModelBillingConfig("priced-per-call"))
	assert.True(t, HasModelBillingConfig("priced-per-token"))
	assert.False(t, HasModelBillingConfig("a-model-nobody-priced"))
}
