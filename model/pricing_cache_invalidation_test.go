package model

// UNIFYAPI-FORK: the pricing payload is memoised for a minute, and nothing used
// to drop it when a price changed.
//
// The bug this pins was reported from the model square: a discount saved in the
// admin UI took effect in the relay immediately -- customers were charged the new
// price on the very next request -- while GET /api/pricing kept publishing the
// OLD price for up to a minute (and up to five more from the SPA's staleTime).
// So for several minutes the product quoted one price and billed another.
//
// The charge was right, which is why no billing test caught it. That makes it a
// display bug, but a display bug about money is a trust problem: a customer who
// checks the price, sends a request, and gets billed something else has no
// reason to believe the next number either.
//
// InvalidatePricingCache was only wired to billing_setting.* and to channel
// edits, so ModelRatio, ModelDiscount, GroupRatio and friends all had the gap.

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// withOptionMap gives updateOptionMap somewhere to write. In the process this
// is allocated by InitOptionMap, which needs a database; the tests here only
// exercise the dispatch and the cache, so a bare map is enough.
func withOptionMap(t *testing.T) {
	t.Helper()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
		t.Cleanup(func() { common.OptionMap = nil })
	}
}

// primePricingCache makes the memo look freshly populated, the way a real
// request to /api/pricing leaves it.
func primePricingCache(t *testing.T) {
	t.Helper()
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	pricingMap = []Pricing{{ModelName: "sentinel", ModelRatio: 1}}
	lastGetPricingTime = time.Now()
}

func pricingCacheIsPrimed() bool {
	updatePricingLock.Lock()
	defer updatePricingLock.Unlock()
	return len(pricingMap) > 0 && !lastGetPricingTime.IsZero()
}

// pricingOptionSamples is a valid payload for every option that changes a
// published price. Values are per-key rather than a shared "{}" because they are
// not all the same shape -- AutoGroups is a JSON array, the ratio maps are
// objects -- and a wrong shape fails the dispatch for the wrong reason.
var pricingOptionSamples = map[string]string{
	"ModelDiscount":        `{"gpt-4o":0.9}`,
	"ModelRatio":           `{"gpt-4o":1.25}`,
	"CompletionRatio":      `{"gpt-4o":4}`,
	"CacheRatio":           `{"gpt-4o":0.5}`,
	"CreateCacheRatio":     `{"gpt-4o":1.25}`,
	"ModelPrice":           `{"mj_imagine":0.1}`,
	"ImageRatio":           `{"gpt-image-2":2}`,
	"AudioRatio":           `{"gpt-4o-audio-preview":16}`,
	"AudioCompletionRatio": `{"gpt-4o-realtime":2}`,
	"GroupRatio":           `{"default":1,"vip":0.8}`,
	"GroupGroupRatio":      `{}`,
	"UserUsableGroups":     `{"default":"default"}`,
	"AutoGroups":           `[]`,
	"ChannelCostRatio":     `{"1":0.8}`,
}

// TestSavingAPriceDropsTheCachedPricingPayload is the regression test for the
// model-square staleness. It is driven by pricingDisplayOptions itself, so
// adding a price-affecting option without a sample here fails loudly rather than
// going unchecked -- which is the shape the original bug had.
func TestSavingAPriceDropsTheCachedPricingPayload(t *testing.T) {
	withOptionMap(t)
	ratio_setting.InitRatioSettings()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelDiscountByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{}`))
	})

	require.Len(t, pricingOptionSamples, len(pricingDisplayOptions),
		"every price-affecting option needs a sample payload here")

	for key := range pricingDisplayOptions {
		t.Run(key, func(t *testing.T) {
			sample, ok := pricingOptionSamples[key]
			require.True(t, ok,
				"%s is listed as price-affecting but has no sample payload, so nothing verifies "+
					"that saving it drops the cached pricing payload", key)

			primePricingCache(t)
			require.True(t, pricingCacheIsPrimed(), "test setup")

			require.NoError(t, updateOptionMap(key, sample),
				"%s is listed as price-affecting, so it must be a handled option", key)

			require.False(t, pricingCacheIsPrimed(),
				"saving %s changes a published price, so the cached /api/pricing payload must be dropped. "+
					"Without this the model square quotes the old price while the relay already bills the new one.",
				key)
		})
	}
}

// TestSavingAnUnrelatedOptionKeepsTheCache -- invalidating on every option would
// make the memo pointless, since the admin UI saves whole sections at a time.
func TestSavingAnUnrelatedOptionKeepsTheCache(t *testing.T) {
	withOptionMap(t)
	for _, tc := range []struct {
		key, value string
	}{
		{"TopUpLink", "https://example.com/topup"},
		{"RetryTimes", "3"},
		{"DataExportDefaultTime", "hour"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			primePricingCache(t)
			require.NoError(t, updateOptionMap(tc.key, tc.value))
			require.True(t, pricingCacheIsPrimed(),
				"%s does not change a price, so the pricing memo should survive", tc.key)
		})
	}
}
