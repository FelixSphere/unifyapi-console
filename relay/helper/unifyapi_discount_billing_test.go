package helper

// UNIFYAPI-FORK: proves the per-model customer discount reaches the number the
// relay actually bills with, and that the upstream cost ratio does not.
//
// The existing pricing tests stopped at ratio_setting.GetModelRatio, which is
// one layer short of the thing that matters: the relay bills from the PriceData
// that ModelPriceHelper builds. A discount that changed the ratio map but never
// reached PriceData would have passed every test written so far while charging
// customers the undiscounted price. So these tests go through ModelPriceHelper.
//
// The second property is the one that keeps invoices reconcilable: a channel's
// purchasing cost must NOT move a customer's price. Routing is load balanced, so
// if it did, the same request would bill differently depending on which channel
// happened to serve it.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// billingContextFor builds the minimum gin context and RelayInfo that
// ModelPriceHelper needs to price a plain per-token text request.
func billingContextFor(t *testing.T, modelName, group string) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	request.Body = nil
	request.ContentLength = 0
	ctx.Request = request
	ctx.Set("group", group)

	return ctx, &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       group,
		UsingGroup:      group,
	}
}

func resetPricingState(t *testing.T) {
	t.Helper()
	// InitRatioSettings is what main.go runs at boot; without it the completion
	// and cache maps are empty and GetCacheRatio/GetCompletionRatio silently
	// fall back to 1 -- i.e. cached reads billed at full input price and output
	// at input price. Seeding here makes the test exercise the same maps the
	// process serves from.
	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.8}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{}`))
	})
}

// TestModelPriceHelperBillsTheOfficialPriceWithoutADiscount is the control: no
// discount means the relay bills the vendor's list price.
func TestModelPriceHelperBillsTheOfficialPriceWithoutADiscount(t *testing.T) {
	resetPricingState(t)

	ctx, info := billingContextFor(t, "claude-opus-4-8", "default")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	// Anthropic publishes $5 in / $25 out per 1M for Opus 4.8.
	require.InDelta(t, 2.5, priceData.ModelRatio, 1e-9, "$5/1M input == ratio 2.5")
	require.InDelta(t, 5, priceData.CompletionRatio, 1e-9, "$25/$5 == 5x output")
	require.InDelta(t, 0.1, priceData.CacheRatio, 1e-9, "$0.50/$5 == 0.1x cached read")
	require.InDelta(t, 1.25, priceData.CacheCreationRatio, 1e-9, "$6.25/$5 == 1.25x cache write")
	require.InDelta(t, 1, priceData.GroupRatioInfo.GroupRatio, 1e-9)
}

// TestModelPriceHelperAppliesTheCustomerDiscount is the test whose absence let a
// display bug look like a billing bug: it pins that the discount reaches the
// ratio the relay charges from, not merely the ratio map.
func TestModelPriceHelperAppliesTheCustomerDiscount(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"claude-opus-4-8":0.85}`))

	ctx, info := billingContextFor(t, "claude-opus-4-8", "default")
	priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	require.InDelta(t, 2.125, priceData.ModelRatio, 1e-9, "2.5 official x 0.85 discount")

	// The relative multipliers must NOT be discounted a second time, or output
	// would get the discount twice over. They stay at the official ratios and
	// inherit the discount through ModelRatio.
	require.InDelta(t, 5, priceData.CompletionRatio, 1e-9)
	require.InDelta(t, 0.1, priceData.CacheRatio, 1e-9)
	require.InDelta(t, 1.25, priceData.CacheCreationRatio, 1e-9)
}

// TestBilledPriceIsOfficialTimesDiscountTimesGroupRatio walks the whole
// customer-facing formula in dollars, which is the form an invoice dispute is
// argued in.
func TestBilledPriceIsOfficialTimesDiscountTimesGroupRatio(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"claude-opus-5":0.9}`))

	for _, tc := range []struct {
		group                 string
		wantInputUSD, wantOut float64
	}{
		// claude-opus-5 is $5 / $25 per 1M officially.
		{"default", 5 * 0.9 * 1.0, 25 * 0.9 * 1.0},
		{"vip", 5 * 0.9 * 0.8, 25 * 0.9 * 0.8},
	} {
		t.Run(tc.group, func(t *testing.T) {
			ctx, info := billingContextFor(t, "claude-opus-5", tc.group)
			priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
			require.NoError(t, err)

			// A ratio of 1 is $2 per 1M tokens, so the dollar price per 1M is
			// ratio x group ratio x 2.
			gotInput := priceData.ModelRatio * priceData.GroupRatioInfo.GroupRatio * 2
			gotOutput := gotInput * priceData.CompletionRatio

			require.InDelta(t, tc.wantInputUSD, gotInput, 1e-9, "input $/1M")
			require.InDelta(t, tc.wantOut, gotOutput, 1e-9, "output $/1M")
		})
	}
}

// TestUpstreamCostRatioNeverChangesWhatTheCustomerIsBilled is the separation the
// whole three-price design rests on. If this ever fails, a customer's invoice
// depends on which channel load balancing picked, and nobody can reconcile it.
func TestUpstreamCostRatioNeverChangesWhatTheCustomerIsBilled(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"gpt-4o":0.9}`))

	ctx, info := billingContextFor(t, "gpt-4o", "default")
	before, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	// Configure a deep purchasing discount on every channel id in sight.
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(
		`{"1":0.5,"2":0.5,"3":0.5,"7":0.5}`))

	ctx, info = billingContextFor(t, "gpt-4o", "default")
	after, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)

	require.InDelta(t, before.ModelRatio, after.ModelRatio, 1e-12,
		"an upstream purchasing discount must not move the customer's price")
	require.InDelta(t, before.CompletionRatio, after.CompletionRatio, 1e-12)
	require.InDelta(t, before.CacheRatio, after.CacheRatio, 1e-12)
	require.InDelta(t, before.GroupRatioInfo.GroupRatio, after.GroupRatioInfo.GroupRatio, 1e-12)
	require.Equal(t, before.QuotaToPreConsume, after.QuotaToPreConsume,
		"even the pre-consumed hold must be unaffected")

	// And it must still be visible on the cost side, or it would be inert.
	cost, ok := ratio_setting.UpstreamCostUSD("gpt-4o", 1, 1_000_000, 0, 0)
	require.True(t, ok)
	require.InDelta(t, 1.25, cost, 1e-9, "$2.50/1M list x 0.5 purchasing ratio")
}

// TestRemovingADiscountRestoresTheBilledPrice -- the ratio map is rebuilt rather
// than mutated, so clearing a discount has to go back to the official price
// instead of leaving the last discounted value in place.
func TestRemovingADiscountRestoresTheBilledPrice(t *testing.T) {
	resetPricingState(t)

	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"gpt-4o":0.5}`))
	ctx, info := billingContextFor(t, "gpt-4o", "default")
	discounted, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 0.625, discounted.ModelRatio, 1e-9)

	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
	ctx, info = billingContextFor(t, "gpt-4o", "default")
	restored, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.NoError(t, err)
	require.InDelta(t, 1.25, restored.ModelRatio, 1e-9, "back to $2.50/1M")
}

// TestUncataloguedModelIsRefusedAtBillingTime closes the loop on the catalog
// doubling as an allow-list: the refusal has to happen here, in the pricing
// helper, not merely in a lookup helper.
func TestUncataloguedModelIsRefusedAtBillingTime(t *testing.T) {
	resetPricingState(t)

	ctx, info := billingContextFor(t, "gpt-4-32k", "default")
	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err, "a model with no official price must not be billable")
	require.Contains(t, err.Error(), "gpt-4-32k")
}
