package controller

// UNIFYAPI-FORK: the public catalogue must price the same for everyone.
//
// Model Square answers "what does this model cost", not "what do I pay". Those
// are different questions, and conflating them produced a bug that was very hard
// to read from the outside: the global ModelDiscount was reset to 1 so the page
// would quote true vendor list prices, and the page went on quoting 0.9x,
// because a per-customer contract was being substituted for the public price
// before any other logic ran.
//
// The symptom was "the discount reset did not take effect". The cause was that
// the page had never been showing the discount at all -- it was showing whoever
// happened to be looking.

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withGroupModelDiscount(t *testing.T, jsonStr string) {
	t.Helper()
	previous := ratio_setting.GroupModelDiscount2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(jsonStr))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(previous))
	})
}

// TestACustomerContractNeverRewritesThePublishedPrice.
//
// The annotation is fine and useful -- a caller may want to render "your price"
// deliberately. Rewriting ModelRatio is not: that field IS the published price,
// and overwriting it makes the catalogue say something different to different
// people while claiming to be a catalogue.
func TestACustomerContractNeverRewritesThePublishedPrice(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"GenAI":{"claude-opus-5":0.8}}`)

	// A NON-UNIT global discount is essential here. With ModelDiscount at 1 the
	// published price and the official price are the same number, so rewriting
	// one to the other is invisible and this test proves nothing -- I wrote it
	// that way first and the mutation passed.
	previousDiscount := ratio_setting.ModelDiscount2JSONString()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{"claude-opus-5":0.6}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(previousDiscount))
	})

	entry, ok := ratio_setting.CatalogEntryFor("claude-opus-5")
	require.True(t, ok)
	published := entry.ModelRatio() * ratio_setting.GetModelDiscount("claude-opus-5")
	require.NotEqual(t, entry.ModelRatio(), published,
		"the fixture must distinguish official price from published price")

	rows := []model.Pricing{{ModelName: "claude-opus-5", ModelRatio: published}}
	out := applyCustomerGroupModelPricing(rows, "GenAI")
	require.Len(t, out, 1)

	assert.InDelta(t, published, out[0].ModelRatio, 1e-12,
		"the published price changed for a viewer who has a contract. Model Square would then "+
			"quote a different number to that customer than to everyone else, with nothing on "+
			"the page saying so.")

	require.NotNil(t, out[0].CustomerGroupModelRatio,
		"the contract should still be reported, just not substituted")
	assert.InDelta(t, 0.8, *out[0].CustomerGroupModelRatio, 1e-12)
}

// TestTheCatalogueReadsTheSameForEveryViewer is the invariant, stated once.
//
// Whatever mechanism is added later -- another discount layer, a per-tenant
// override -- the published ratio must not move with the identity of the caller.
func TestTheCatalogueReadsTheSameForEveryViewer(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{
		"GenAI":   {"claude-opus-5":0.8,"gpt-4o":0.5},
		"Chinhin": {"claude-opus-5":0.6}
	}`)
	previousDiscount := ratio_setting.ModelDiscount2JSONString()
	require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(
		`{"claude-opus-5":0.6,"gpt-4o":0.6}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(previousDiscount))
	})

	// Published prices, i.e. official x the global discount -- deliberately not
	// equal to the official ratios, so a reset to official is detectable.
	base := []model.Pricing{
		{ModelName: "claude-opus-5", ModelRatio: 2.5 * 0.6},
		{ModelName: "gpt-4o", ModelRatio: 1.25 * 0.6},
	}

	anonymous := applyCustomerGroupModelPricing(base, "")
	for _, group := range []string{"GenAI", "Chinhin", "Builder_hub_2026"} {
		seen := applyCustomerGroupModelPricing(base, group)
		require.Len(t, seen, len(anonymous))
		for i := range seen {
			assert.InDelta(t, anonymous[i].ModelRatio, seen[i].ModelRatio, 1e-12,
				"%s sees a different published price for %s than an anonymous visitor",
				group, seen[i].ModelName)
		}
	}
}

// TestAnnotationDoesNotLeakAcrossViewers -- applyCustomerGroupModelPricing
// copies the slice, but the rows come from a cache shared by every request. A
// contract written into a shared row would follow the next caller.
func TestAnnotationDoesNotLeakAcrossViewers(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"GenAI":{"claude-opus-5":0.8}}`)

	shared := []model.Pricing{{ModelName: "claude-opus-5", ModelRatio: 2.5}}
	_ = applyCustomerGroupModelPricing(shared, "GenAI")

	assert.Nil(t, shared[0].CustomerGroupModelRatio,
		"a contract was written into the shared pricing row. The next request, for a different "+
			"customer, would carry it.")

	other := applyCustomerGroupModelPricing(shared, "Chinhin")
	assert.Nil(t, other[0].CustomerGroupModelRatio,
		"Chinhin has no contract for this model and must not inherit GenAI's")
}
