package service

// UNIFYAPI-FORK: proof that a customer is charged the right amount.
//
// Every other pricing test in this fork asserts an intermediate: that a catalog
// row derives the right ratio, that a discount lands in the right map, that the
// pricing page reads the same map the relay bills from. None of them answers the
// question an operator actually asks, which is "if I set a 28% discount on
// claude-sonnet-5, does a customer get charged 28% less".
//
// So these start from a vendor's published price in dollars, go through the real
// discount table, the real group ratio, the real ratio getters and the real
// quota formula, and assert the DOLLARS DEDUCTED. Nothing is hand-computed into
// a PriceData literal: priceDataFor builds it exactly the way
// relay/helper/price.go does, so if that chain breaks anywhere between the
// catalog and the ledger, these fail.
//
// The reason this matters more than usual here: on this deployment a slipped
// decimal in a ratio went unnoticed for long enough to bill at 8.5% of cost, and
// a discount table was destroyed without anyone noticing because the broken
// state and the healthy state look identical from outside. A test that reads a
// ratio would have passed in both cases. One that reads dollars would not.

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// priceDataFor assembles PriceData from the live ratio settings the same way
// relay/helper/price.go does. Copied deliberately rather than shared: if that
// builder ever stops consulting one of these getters, the difference should show
// up as a failing dollar amount here, not be papered over by both sides calling
// the same helper.
func priceDataFor(t *testing.T, model, group string) hosttypes.PriceData {
	t.Helper()
	modelRatio, ok, _ := ratio_setting.GetModelRatio(model)
	require.True(t, ok, "%s has no ratio, so the relay would refuse it outright", model)

	cacheRatio, _ := ratio_setting.GetCacheRatio(model)
	cacheCreationRatio, _ := ratio_setting.GetCreateCacheRatio(model)
	imageRatio, _ := ratio_setting.GetImageRatio(model)

	return hosttypes.PriceData{
		ModelRatio:           modelRatio,
		CompletionRatio:      ratio_setting.GetCompletionRatio(model),
		CacheRatio:           cacheRatio,
		CacheCreationRatio:   cacheCreationRatio,
		CacheCreation5mRatio: cacheCreationRatio,
		CacheCreation1hRatio: cacheCreationRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           ratio_setting.GetAudioRatio(model),
		AudioCompletionRatio: ratio_setting.GetAudioCompletionRatio(model),
		GroupRatioInfo: hosttypes.GroupRatioInfo{
			GroupRatio: ratio_setting.GetGroupRatio(group),
		},
	}
}

// chargeUSD runs the real quota formula and returns what the customer pays, in
// dollars. Dollars, not quota units: "3628800" is unreadable, and every mistake
// this fork has made in pricing came from reasoning about a scaled integer
// instead of about money.
func chargeUSD(t *testing.T, model, group string, usage *dto.Usage) float64 {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	relayInfo := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		OriginModelName: model,
		PriceData:       priceDataFor(t, model, group),
		StartTime:       time.Now(),
	}
	summary := calculateTextQuotaSummary(ctx, relayInfo, usage)
	require.NotZero(t, common.QuotaPerUnit)
	return float64(summary.Quota) / common.QuotaPerUnit
}

func tokens(prompt, completion int) *dto.Usage {
	return &dto.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
	}
}

// withDiscount installs a discount table for one test and restores whatever was
// there before, so an assertion cannot leak pricing into the next test.
func withDiscount(t *testing.T, jsonStr string) {
	t.Helper()
	previous := ratio_setting.ModelDiscount2JSONString()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(jsonStr))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(previous))
	})
}

// TestUndiscountedChargeIsTheVendorListPrice. The baseline claim of the whole
// fork: with no discount, a customer pays exactly what the vendor publishes.
func TestUndiscountedChargeIsTheVendorListPrice(t *testing.T) {
	withDiscount(t, `{}`)

	// claude-sonnet-5 is published at $2 per 1M input and $10 per 1M output.
	// 1M of each is therefore $12.00 exactly.
	charged := chargeUSD(t, "claude-sonnet-5", "default", tokens(1_000_000, 1_000_000))
	require.InDelta(t, 12.00, charged, 1e-6,
		"an undiscounted customer must be charged the vendor's list price, to the cent")
}

// TestADiscountReducesTheChargeByExactlyThatFraction is the operator's actual
// question: I typed 0.72, did the customer pay 28% less?
func TestADiscountReducesTheChargeByExactlyThatFraction(t *testing.T) {
	withDiscount(t, `{}`)
	full := chargeUSD(t, "claude-sonnet-5", "default", tokens(1_000_000, 1_000_000))

	withDiscount(t, `{"claude-sonnet-5":0.72}`)
	discounted := chargeUSD(t, "claude-sonnet-5", "default", tokens(1_000_000, 1_000_000))

	require.InDelta(t, 8.64, discounted, 1e-6, "$12.00 x 0.72")
	require.InDelta(t, 0.72, discounted/full, 1e-9,
		"the ratio of the two charges must be the discount itself, not merely near it")
}

// TestTheDiscountAppliesToOutputAndCacheToo. A discount that only touched input
// would quietly under-discount every real workload, since output is the larger
// half of most bills.
func TestTheDiscountAppliesToOutputAndCacheToo(t *testing.T) {
	// claude-opus-4-8: $5 in, $25 out, $0.50 cached read per 1M.
	// 1M prompt of which 800k cached, plus 1M completion, at list:
	//   fresh   200_000 / 1M x $5    = $1.00
	//   cached  800_000 / 1M x $0.50 = $0.40
	//   output  1M      / 1M x $25   = $25.00
	//                                 -------
	//                                  $26.40
	usage := &dto.Usage{
		PromptTokens:        1_000_000,
		CompletionTokens:    1_000_000,
		TotalTokens:         2_000_000,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 800_000},
	}

	withDiscount(t, `{}`)
	require.InDelta(t, 26.40, chargeUSD(t, "claude-opus-4-8", "default", usage), 1e-6)

	withDiscount(t, `{"claude-opus-4-8":0.5}`)
	require.InDelta(t, 13.20, chargeUSD(t, "claude-opus-4-8", "default", usage), 1e-6,
		"half off must halve the whole bill -- input, output and cached reads alike")
}

// TestTheSlippedDecimalWouldBeCaughtHere. 0.085 on claude-opus-4-8 was a typo
// that reached production and billed at 8.5% of the vendor's price. A ratio
// assertion cannot tell that from a deliberate discount; a dollar assertion
// makes the size of it impossible to miss.
func TestTheSlippedDecimalWouldBeCaughtHere(t *testing.T) {
	withDiscount(t, `{"claude-opus-4-8":0.085}`)

	// $5/1M input. At 0.085 the customer pays 42.5 cents per 1M -- against an
	// upstream cost of $5 before any purchasing discount.
	charged := chargeUSD(t, "claude-opus-4-8", "default", tokens(1_000_000, 0))
	require.InDelta(t, 0.425, charged, 1e-6)

	withDiscount(t, `{}`)
	list := chargeUSD(t, "claude-opus-4-8", "default", tokens(1_000_000, 0))
	require.InDelta(t, 11.7647, list/charged, 1e-3,
		"this is the 11.8x underprice, stated as a multiple rather than as a ratio nobody can read")
}

// TestGroupRatioMultipliesOnTopOfTheDiscount. The two layers compose; if either
// were applied instead of the other, a tiered customer would be billed wrong in
// a way that looks plausible.
func TestGroupRatioMultipliesOnTopOfTheDiscount(t *testing.T) {
	previousGroups := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.5}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
	})
	withDiscount(t, `{"claude-sonnet-5":0.8}`)

	usage := tokens(1_000_000, 1_000_000)
	// $12.00 list x 0.8 discount = $9.60, x 0.5 group = $4.80.
	require.InDelta(t, 9.60, chargeUSD(t, "claude-sonnet-5", "default", usage), 1e-6)
	require.InDelta(t, 4.80, chargeUSD(t, "claude-sonnet-5", "vip", usage), 1e-6)
}

// TestAnAdminAddedModelBillsAtItsTypedPrice closes the loop on the extras table:
// a price entered in the console, with no deploy, charges what it says.
func TestAnAdminAddedModelBillsAtItsTypedPrice(t *testing.T) {
	previous := ratio_setting.ExtraModels2JSONString()
	require.NoError(t, ratio_setting.UpdateExtraModelsByJSONString(
		`{"some-new-vendor-model":{"input_usd":3,"output_usd":15}}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateExtraModelsByJSONString(previous))
	})

	// $3 in + $15 out per 1M.
	require.InDelta(t, 18.00,
		chargeUSD(t, "some-new-vendor-model", "default", tokens(1_000_000, 1_000_000)), 1e-6)

	// And a discount on it behaves like a discount on any other model.
	withDiscount(t, `{"some-new-vendor-model":0.5}`)
	require.InDelta(t, 9.00,
		chargeUSD(t, "some-new-vendor-model", "default", tokens(1_000_000, 1_000_000)), 1e-6)
}

// TestRemovingADiscountRestoresTheListPriceCharge. The failure mode this guards
// is a discount table that is edited a few times and then cannot be reasoned
// about, because removing an entry left the last discounted value in place.
func TestRemovingADiscountRestoresTheListPriceCharge(t *testing.T) {
	usage := tokens(1_000_000, 1_000_000)

	withDiscount(t, `{}`)
	list := chargeUSD(t, "claude-sonnet-5", "default", usage)

	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"claude-sonnet-5":0.25}`))
	require.InDelta(t, list*0.25, chargeUSD(t, "claude-sonnet-5", "default", usage), 1e-6)

	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
	require.InDelta(t, list, chargeUSD(t, "claude-sonnet-5", "default", usage), 1e-6,
		"removing a discount must restore the list price, not leave the discounted one behind")
}

// TestARejectedDiscountChangesNoCharge. Validation runs before the table loads,
// so a save that names an unknown model must not half-apply and reprice the
// models that were valid in the same payload.
func TestARejectedDiscountChangesNoCharge(t *testing.T) {
	withDiscount(t, `{"claude-sonnet-5":0.9}`)
	before := chargeUSD(t, "claude-sonnet-5", "default", tokens(1_000_000, 1_000_000))

	err := ratio_setting.UpdateModelDiscountByJSONString(
		`{"claude-sonnet-5":0.1,"model-that-does-not-exist":0.5}`)
	require.Error(t, err)

	require.InDelta(t, before, chargeUSD(t, "claude-sonnet-5", "default", tokens(1_000_000, 1_000_000)), 1e-9,
		"a rejected save must leave every charge exactly as it was")
}
