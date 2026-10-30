package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The operator gives new registrations (group `default`) 10% off the way
// every other customer group gets it: one GroupModelDiscount override per
// model, entered in the Customer model prices editor. These tests run that
// configuration through ModelPriceHelper -- the function that prices a relay
// request -- and pin what the customer is charged.
//
// GroupModelDiscount is keyed by the user's OWN group (relayInfo.UserGroup),
// not by the group that routed the request. That is what makes a single
// `default` row sufficient: a default user's request may be priced through
// `default` (an empty or `auto` token) or through any group their token
// names, and the override applies identically to all of them.

// installDefaultGroupDiscount reproduces production -- five customer groups at
// ratio 1 -- plus the two things the operator adds: `default` in GroupRatio,
// and a 0.9 override on each model for `default`.
func installDefaultGroupDiscount(t *testing.T) {
	t.Helper()
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1,"default":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{
		"default": {"gpt-4o":0.9, "claude-opus-5":0.9, "claude-sonnet-5":0.9, "gemini-2.5-flash":0.9},
		"Chinhin": {"gpt-4o":0.9}
	}`))
}

// TestADefaultUserIsBilledNinetyPercentOfOfficialWhicheverGroupRoutedThem.
// Same user group, three different pricing groups, one answer: 0.9 x list.
func TestADefaultUserIsBilledNinetyPercentOfOfficialWhicheverGroupRoutedThem(t *testing.T) {
	installDefaultGroupDiscount(t)

	for _, tc := range []struct {
		model       string
		officialIn  float64 // USD per 1M input tokens, from the catalogue
		usingGroups []string
	}{
		{"gpt-4o", 2.5, []string{"default", "UnifyAI", "GenAI"}},
		{"claude-opus-5", 5, []string{"default", "Chinhin", "Vip User"}},
		{"claude-sonnet-5", 2, []string{"default", "Builder_hub_2026"}},
	} {
		entry, ok := ratio_setting.CatalogEntryFor(tc.model)
		require.True(t, ok)
		require.InDelta(t, tc.officialIn, entry.InputUSD, 1e-9, "%s official price moved; update the row here on purpose", tc.model)

		for _, using := range tc.usingGroups {
			t.Run(tc.model+" via "+using, func(t *testing.T) {
				ctx, info := billingContextFor(t, tc.model, "default")
				info.UsingGroup = using
				priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
				require.NoError(t, err)

				dollars := priceData.ModelRatio * 2 * priceData.GroupRatioInfo.GroupRatio
				assert.InDelta(t, tc.officialIn*0.9, dollars, 1e-9, "$/1M input for a default user")
				assert.True(t, priceData.GroupRatioInfo.HasSpecialRatio, "the 0.9 must be recorded as a customer price")
				assert.Equal(t, int(tc.officialIn*0.9*500000), priceData.QuotaToPreConsume,
					"one million input tokens must pre-consume exactly the discounted dollars")
			})
		}
	}
}

// TestTheDefaultDiscountDoesNotStackWithTheGroupRatio. If someone later sets
// GroupRatio[default] to something other than 1, the override must still be
// the final word -- 0.9 of official, not 0.9 x that. This is the same
// invariant TestCustomerModelPriceIsFinal pins for the other groups, asserted
// for the one that now matters.
func TestTheDefaultDiscountDoesNotStackWithTheGroupRatio(t *testing.T) {
	installDefaultGroupDiscount(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1,"default":0.7}`))

	ctx, info := billingContextFor(t, "gpt-4o", "default")
	priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.InDelta(t, 2.5*0.9, priceData.ModelRatio*2*priceData.GroupRatioInfo.GroupRatio, 1e-9,
		"0.9 is final; a GroupRatio of 0.7 must not turn it into 0.63")
}

// TestOtherCustomersAreBilledExactlyAsBeforeDefaultGotItsRow. Adding the
// `default` key to GroupRatio and GroupModelDiscount must not move a single
// number for any other group. Chinhin keeps its own override; groups without
// one stay at official.
func TestOtherCustomersAreBilledExactlyAsBeforeDefaultGotItsRow(t *testing.T) {
	// Before: production shape without `default` anywhere.
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{"Chinhin": {"gpt-4o":0.9}}`))

	type key struct{ user, model string }
	cases := []key{}
	for _, user := range []string{"Builder_hub_2026", "Chinhin", "GenAI", "UnifyAI", "Vip User"} {
		for _, m := range []string{"gpt-4o", "claude-opus-5"} {
			cases = append(cases, key{user, m})
		}
	}
	before := map[key]int{}
	for _, c := range cases {
		ctx, info := billingContextFor(t, c.model, c.user)
		priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
		require.NoError(t, err)
		before[c] = priceData.QuotaToPreConsume
	}
	require.Equal(t, int(2.5*0.9*500000), before[key{"Chinhin", "gpt-4o"}], "precondition: Chinhin's own override is live")
	require.Equal(t, int(2.5*500000), before[key{"GenAI", "gpt-4o"}], "precondition: GenAI pays official")

	// After: exactly what the operator adds.
	installDefaultGroupDiscount(t)

	for _, c := range cases {
		ctx, info := billingContextFor(t, c.model, c.user)
		priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
		require.NoError(t, err)
		assert.Equal(t, before[c], priceData.QuotaToPreConsume, "%s / %s: pre-consumed quota moved", c.user, c.model)
	}
}

// TestWithoutTheRowADefaultUserPaysOfficial documents the state being fixed,
// so the discount tests above cannot pass vacuously.
func TestWithoutTheRowADefaultUserPaysOfficial(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(
		`{"Builder_hub_2026":1,"Chinhin":1,"GenAI":1,"UnifyAI":1,"Vip User":1}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`))

	ctx, info := billingContextFor(t, "gpt-4o", "default")
	priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.InDelta(t, 2.5, priceData.ModelRatio*2*priceData.GroupRatioInfo.GroupRatio, 1e-9,
		"no `default` row anywhere -> full official price")
	assert.False(t, priceData.GroupRatioInfo.HasSpecialRatio)
}

// TestTheDefaultDiscountReachesExpressionBilledModels. Models with a separate
// audio price or a context tier are not priced by the flat ratio maps at all;
// they go through a billing expression, and the expression path applies the
// group ratio on its own. If the customer override were only wired into the
// flat path, a default user would get 10% off gpt-4o and full price on
// gemini-2.5-flash, and nothing on the flat path would notice.
//
// Asserted as a ratio, not as dollars: the tiered pre-consume adds an
// estimated completion allowance (defaultTieredPreConsumeMaxTokens) that
// settlement later replaces with actual usage, so the absolute pre-consume is
// an estimate by design. The discount must scale that estimate by exactly 0.9.
func TestTheDefaultDiscountReachesExpressionBilledModels(t *testing.T) {
	entry, ok := ratio_setting.CatalogEntryFor("gemini-2.5-flash")
	require.True(t, ok)
	require.True(t, entry.NeedsBillingExpr(), "precondition: this model must be expression-billed or the test proves nothing")

	quotaFor := func(t *testing.T, override string) int {
		resetPricingState(t)
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"GenAI":1,"default":1}`))
		require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(override))
		ctx, info := billingContextFor(t, "gemini-2.5-flash", "default")
		priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{})
		require.NoError(t, err)
		return priceData.QuotaToPreConsume
	}

	full := quotaFor(t, `{}`)
	discounted := quotaFor(t, `{"default":{"gemini-2.5-flash":0.9}}`)
	require.Greater(t, full, 0)
	assert.InDelta(t, 0.9, float64(discounted)/float64(full), 1e-6,
		"a default user's expression-billed pre-consume must be exactly 0.9 of the undiscounted one")
}
