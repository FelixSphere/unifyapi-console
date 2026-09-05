package service

// UNIFYAPI-FORK: assert against the billing configuration production is ACTUALLY
// running, not one the test invented.
//
// Every other pricing test here sets its own ratios and then asserts on them.
// Those prove the mechanism and cannot fail on a production fact -- which is
// exactly the criticism that sank the previous "every customer gets at least
// 10% off" test, and it was fair.
//
// So this file reads a snapshot of the real `options` rows, taken read-only from
// the production instance, and asserts the properties that actually matter for
// money:
//
//	the discount layer is where we think it is
//	every customer group is discounted, on every model it sells
//	we sell above what we pay, on every channel
//
// The last one is the one worth having. It is the check that would have stopped
// the DeepSeek 0.7 proposal: at a 0.7 sale price against a 0.7 purchasing
// ratio, margin is exactly zero, and nothing in the console would have said so.
//
// Refreshing the snapshot is a deliberate act. If a property here starts
// failing, the question is whether production changed on purpose -- not whether
// to re-capture until it passes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const liveConfigPath = "../setting/ratio_setting/testdata/production-billing-config-2026-09-05.json"

type liveBillingConfig struct {
	Captured           string                        `json:"captured"`
	ModelDiscount      map[string]float64            `json:"ModelDiscount"`
	GroupRatio         map[string]float64            `json:"GroupRatio"`
	GroupGroupRatio    map[string]map[string]float64 `json:"GroupGroupRatio"`
	GroupModelDiscount map[string]map[string]float64 `json:"GroupModelDiscount"`
	ChannelCostRatio   map[string]float64            `json:"ChannelCostRatio"`
}

func loadLiveConfig(t *testing.T) liveBillingConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(liveConfigPath))
	require.NoError(t, err)
	var cfg liveBillingConfig
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.NotEmpty(t, cfg.Captured)
	return cfg
}

// TestTheDiscountLayerIsWhereWeThinkItIs.
//
// I asserted the opposite of this from the pricing endpoint and was wrong:
// /api/pricing serves the RAW GroupRatio table to an anonymous caller and only
// resolves per-customer discounts for a logged-in user, so "group_ratio is all
// 1" says nothing about what anyone pays. Pinning the real shape means the next
// person does not have to re-derive it from an endpoint that cannot show it.
func TestTheDiscountLayerIsWhereWeThinkItIs(t *testing.T) {
	cfg := loadLiveConfig(t)

	assert.Empty(t, cfg.ModelDiscount,
		"model discounts were reset to 1 so the pricing page shows the vendor's real list "+
			"price. An entry here makes the published price a discounted number again.")

	for group, ratio := range cfg.GroupRatio {
		assert.InDelta(t, 1.0, ratio, 1e-12,
			"%s carries a flat group ratio. Discounts live in GroupModelDiscount now, and two "+
				"layers both discounting is how a price becomes impossible to explain.", group)
	}

	assert.NotEmpty(t, cfg.GroupModelDiscount,
		"this is the only remaining discount layer; empty means every customer pays list")
}

// TestEveryCustomerGroupIsActuallyDiscounted is the check the previous test
// only pretended to be: it reads the live table rather than one it set itself.
func TestEveryCustomerGroupIsActuallyDiscounted(t *testing.T) {
	cfg := loadLiveConfig(t)

	for group, models := range cfg.GroupModelDiscount {
		t.Run(group, func(t *testing.T) {
			require.NotEmpty(t, models, "%s has no per-model discounts, so it pays list price", group)
			for model, discount := range models {
				assert.Greater(t, discount, 0.0, "%s/%s: a zero discount makes the model free", group, model)
				assert.LessOrEqual(t, discount, 1.0,
					"%s/%s is sold ABOVE the vendor's list price at %g. That is a markup, which may "+
						"be intended, but it should never arrive by accident.", group, model, discount)
			}
		})
	}
}

// TestWeSellAboveWhatWePay is the margin guard.
//
// Sale price is the vendor's list times the customer's discount; cost is the
// same list times the channel's purchasing ratio. So the comparison reduces to
// the two ratios, and it holds for every model regardless of its price.
//
// This is the test that makes a margin-destroying decision visible before it
// ships rather than in the next month's reconciliation.
func TestWeSellAboveWhatWePay(t *testing.T) {
	cfg := loadLiveConfig(t)
	require.NotEmpty(t, cfg.ChannelCostRatio, "with no purchasing ratios, margin is unmeasured")

	worstCost := 0.0
	worstChannel := ""
	for channel, ratio := range cfg.ChannelCostRatio {
		if ratio > worstCost {
			worstCost, worstChannel = ratio, channel
		}
	}

	for group, models := range cfg.GroupModelDiscount {
		for model, discount := range models {
			assert.Greater(t, discount, worstCost,
				"%s pays %g x list for %s while channel %s costs us %g x list -- that sale is at or "+
					"below cost. Margin is (discount - cost) / discount, so these two ratios are the "+
					"whole of it.", group, discount, model, worstChannel, worstCost)
		}
	}
}

// TestWorstCaseMarginDoesNotGetWorse is a ratchet, not a policy.
//
// I first wrote this as "margin must exceed 10%" and it failed: against channel
// 169, the most expensive at 0.85 x list, a 0.9 sale leaves 5.6%. That number is
// a real fact about the configuration, and 10% was a threshold I invented rather
// than one the business set -- exactly the mistake that made the previous
// "at least 10% off" test worthless.
//
// So this pins the worst case as it stands and only fails when it DEGRADES.
// Margin may be improved freely; it cannot quietly erode. Widening the bound is
// then a deliberate edit with a number attached, which is the point.
//
// The comparison is against the single most expensive channel, which is
// pessimistic: that channel may not serve the models being checked. Erring that
// way is correct here -- routing is load balanced, so any model may land on any
// channel that carries it, and the worst case is the one that has to be
// survivable.
const worstCaseMarginPctAsOf20260905 = 5.55

func TestWorstCaseMarginDoesNotGetWorse(t *testing.T) {
	cfg := loadLiveConfig(t)

	worstCost, worstChannel := 0.0, ""
	for channel, ratio := range cfg.ChannelCostRatio {
		if ratio > worstCost {
			worstCost, worstChannel = ratio, channel
		}
	}

	thinnest, thinnestAt := 100.0, ""
	for group, models := range cfg.GroupModelDiscount {
		for model, discount := range models {
			if margin := (discount - worstCost) / discount * 100; margin < thinnest {
				thinnest, thinnestAt = margin, group+"/"+model
			}
		}
	}

	assert.GreaterOrEqual(t, thinnest, worstCaseMarginPctAsOf20260905,
		"worst-case margin fell to %.2f%% on %s, against channel %s at %g x list. It was %.2f%% "+
			"when this was pinned. Either a discount deepened or a purchasing ratio rose; if the "+
			"new number is intended, change the constant and say why.",
		thinnest, thinnestAt, worstChannel, worstCost, worstCaseMarginPctAsOf20260905)

	// Reported on every run so the number is visible without reading the source.
	t.Logf("worst-case margin %.2f%% (%s) against channel %s at %g x list",
		thinnest, thinnestAt, worstChannel, worstCost)
}

// --- purchasing cost coverage -------------------------------------------

type liveChannels struct {
	Channels []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Models int    `json:"models"`
	} `json:"channels"`
}

func loadLiveChannels(t *testing.T) liveChannels {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(
		"../setting/ratio_setting/testdata/production-channels-2026-09-05.json"))
	require.NoError(t, err)
	var c liveChannels
	require.NoError(t, json.Unmarshal(raw, &c))
	require.NotEmpty(t, c.Channels)
	return c
}

// uncostedChannelsAsOf20260905 are enabled channels with no purchasing ratio.
//
// Every one is an OpenRouter channel, and every Flatkey channel has one -- so
// this is a category that was missed wholesale, not a scatter of oversights.
//
// Pinned rather than fixed here because the real cost of an OpenRouter route is
// a commercial fact only the operator knows. The list exists so the number
// cannot grow unnoticed, and so that shrinking it is visible progress.
var uncostedChannelsAsOf20260905 = 29

// TestEveryServingChannelHasAPurchasingCost.
//
// An unconfigured channel is costed at the vendor's LIST price, which is not a
// neutral default once anything is discounted: at the current 0.9 it renders as
// a steady 11% loss on every request that routes there.
//
// That is the failure worth naming. The gap does not look like missing data on
// the profit or settlement screens -- it looks like a model losing money, which
// is a conclusion someone will act on. Reconciliation cannot tell the two apart,
// because "cost equals list" is exactly what an unpriced channel produces.
//
// Routing is load balanced, so a model on both a costed and an uncosted channel
// has a margin that depends on which one answered -- and the same request bills
// the customer identically either way.
func TestEveryServingChannelHasAPurchasingCost(t *testing.T) {
	cfg := loadLiveConfig(t)
	channels := loadLiveChannels(t)

	var uncosted []string
	for _, ch := range channels.Channels {
		if _, ok := cfg.ChannelCostRatio[ch.ID]; !ok {
			uncosted = append(uncosted, ch.ID+" "+ch.Name)
		}
	}

	assert.LessOrEqual(t, len(uncosted), uncostedChannelsAsOf20260905,
		"%d enabled channels have no purchasing cost, up from %d. Each one is costed at the "+
			"vendor's list price, so its traffic reports as an 11%% loss at today's 0.9 sale "+
			"price rather than as missing configuration:\n  %v",
		len(uncosted), uncostedChannelsAsOf20260905, uncosted)

	if len(uncosted) < uncostedChannelsAsOf20260905 {
		t.Logf("purchasing cost coverage improved: %d uncosted, was %d. Lower the constant.",
			len(uncosted), uncostedChannelsAsOf20260905)
	}
}

// TestMarginIsUnmeasuredNotNegativeOnUncostedChannels states the distinction in
// the one place someone will look after reading a red number on the profit
// screen.
//
// With the sale at 0.9 and an uncosted channel at 1.0, reconciliation reports
// -11.1%. The true margin is unknown, not negative, and the difference decides
// whether the right response is "raise the price" or "fill in the ratio".
func TestMarginIsUnmeasuredNotNegativeOnUncostedChannels(t *testing.T) {
	cfg := loadLiveConfig(t)

	const uncostedRatio = 1.0 // what GetChannelCostRatio returns when unset
	sale := 0.0
	for _, models := range cfg.GroupModelDiscount {
		for _, discount := range models {
			if discount > sale {
				sale = discount
			}
		}
	}
	require.Greater(t, sale, 0.0)

	apparent := (sale - uncostedRatio) / sale * 100
	assert.Less(t, apparent, 0.0,
		"an uncosted channel must look like a loss for this test to be about anything")
	assert.InDelta(t, -11.11, apparent, 0.01,
		"at a %g sale price an uncosted channel reports %.2f%%. If this number moves, the "+
			"reconciliation screens are telling operators something different and the docs "+
			"explaining it need to change too.", sale, apparent)
}
