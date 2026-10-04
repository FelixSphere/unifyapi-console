package helper

// UNIFYAPI-FORK: a customer contract (GroupModelDiscount) is a FINAL multiplier
// over the official catalog price. The flat ratio path honours that by
// replacing the discounted ratio with entry.ModelRatio(). The expression path
// (context tiers, audio input) has the ModelDiscount baked into every
// coefficient, so the same contract must not be applied on top of it.

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func tieredContractPreConsume(t *testing.T, model, group string) (int, *billingexpr.BillingSnapshot) {
	t.Helper()
	ctx, info := billingContextFor(t, model, group)
	priceData, err := ModelPriceHelper(ctx, info, 1_000_000, &types.TokenCountMeta{MaxTokens: 1_000_000})
	require.NoError(t, err)
	require.NotNil(t, info.TieredBillingSnapshot, "%s must be billed by expression for this test to mean anything", model)
	return priceData.QuotaToPreConsume, info.TieredBillingSnapshot
}

// TestContractPriceIsFinalOnTheExpressionPathToo mirrors
// TestCustomerModelPriceIsFinal for the models that bill by expression. The
// global ModelDiscount must have no effect on a customer whose group carries a
// contract price for the model, exactly as it has none on the flat path.
func TestContractPriceIsFinalOnTheExpressionPathToo(t *testing.T) {
	for _, model := range []string{"qwen3.7-plus", "gemini-2.5-pro", "gemini-2.5-flash"} {
		t.Run(model, func(t *testing.T) {
			resetPricingState(t)
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"GenAI":1}`))
			require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(
				`{"GenAI":{"`+model+`":0.8}}`))

			contractOnly, snapContractOnly := tieredContractPreConsume(t, model, "GenAI")

			require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"`+model+`":0.5}`))
			withGlobalDiscount, snapStacked := tieredContractPreConsume(t, model, "GenAI")

			require.Equal(t, contractOnly, withGlobalDiscount,
				"a contract of 0.8 must charge 80%% of official regardless of the global discount; "+
					"the flat path already guarantees this (TestCustomerModelPriceIsFinal)")

			// Settlement runs the frozen snapshot, so it inherits whatever the
			// pre-consume path decided. Prove the two agree on actual usage too.
			params := billingexpr.TokenParams{P: 1_000_000, C: 1_000_000, Len: 1_000_000}
			settledContractOnly, err := billingexpr.ComputeTieredQuota(snapContractOnly, params)
			require.NoError(t, err)
			settledStacked, err := billingexpr.ComputeTieredQuota(snapStacked, params)
			require.NoError(t, err)
			require.Equal(t, settledContractOnly.ActualQuotaAfterGroup, settledStacked.ActualQuotaAfterGroup,
				"settlement must not stack the global discount under the contract either")
		})
	}
}

// TestContractOnTheExpressionPathChargesTheContractedDollars pins the absolute
// number, in the form an invoice dispute is argued in.
func TestContractOnTheExpressionPathChargesTheContractedDollars(t *testing.T) {
	resetPricingState(t)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"GenAI":1}`))
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"qwen3.7-plus":0.5}`))
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{"GenAI":{"qwen3.7-plus":0.8}}`))

	// qwen3.7-plus lists at $0.50 in / $3.00 out below 256K. 100k tokens each
	// is $0.35 at list; the contract says 80% of that, $0.28, which is
	// 140,000 quota at 500,000 quota per dollar.
	ctx, info := billingContextFor(t, "qwen3.7-plus", "GenAI")
	priceData, err := ModelPriceHelper(ctx, info, 100_000, &types.TokenCountMeta{MaxTokens: 100_000})
	require.NoError(t, err)
	require.Equal(t, 140_000, priceData.QuotaToPreConsume)
}
