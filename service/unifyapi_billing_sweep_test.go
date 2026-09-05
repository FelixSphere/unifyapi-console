package service

// UNIFYAPI-FORK: prove the billing pipeline applies the catalog faithfully, for
// EVERY model rather than a chosen few.
//
// WHAT THIS DOES NOT PROVE, stated first because I got it wrong writing it: the
// per-model assertions derive their expected dollars FROM the catalog row, so
// they are self-confirming about catalog VALUES. I mutated gpt-4o's input price
// from $2.50 to $0.25 and this file still passed. A wrong price is caught one
// layer up, by scripts/pricing-drift, which compares the catalog against
// models.dev -- and which does fail on that mutation.
//
// So the layers are:
//
//	pricing-drift   catalog  vs  the vendor's published price
//	this file       the bill vs  the catalog          <- plumbing, all 49 models
//	pinned dollars  the bill vs  numbers typed by hand from the vendor
//
// What this file genuinely catches: a formula change, a model that silently
// stops responding to its discount, a per-call or expression-billed model
// wrongly routed through the ratio path, and -- the one worth most --
// TestACustomerDiscountNeverMovesUpstreamCost, which is NOT self-confirming and
// fails when a customer discount leaks into the modelled upstream cost.
// Verified by mutation: injecting that leak fails the test by name.

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/require"
)

// oneMillionEach is a request big enough that a per-model rounding difference
// shows up as cents rather than disappearing into the quota integer.
func oneMillionEach() *dto.Usage {
	return &dto.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000, TotalTokens: 2_000_000}
}

// sweepableCatalog is every catalogued model that bills per token through the
// ratio path. Per-call models and the handful on billing expressions are
// covered by their own tests -- their charge is not officialPrice x ratio, so
// asserting that identity here would be asserting the wrong formula.
func sweepableCatalog(t *testing.T) []ratio_setting.CatalogEntry {
	t.Helper()
	var out []ratio_setting.CatalogEntry
	for _, entry := range ratio_setting.Catalog() {
		if entry.PerCallUSD > 0 || entry.NeedsBillingExpr() {
			continue
		}
		out = append(out, entry)
	}
	require.NotEmpty(t, out, "the sweep found no models, so it is proving nothing")
	return out
}

// TestEveryModelChargesItsOfficialPriceTimesItsDiscount sweeps the customer
// side. A single bad catalog row fails here with the model named.
func TestEveryModelChargesItsOfficialPriceTimesItsDiscount(t *testing.T) {
	withDiscount(t, `{}`)

	for _, entry := range sweepableCatalog(t) {
		t.Run(entry.Model, func(t *testing.T) {
			// 1M in + 1M out at list is exactly input + output dollars.
			want := entry.InputUSD + entry.OutputUSD
			got := chargeUSD(t, entry.Model, "default", oneMillionEach())
			require.InDelta(t, want, got, 1e-6,
				"%s: 1M in + 1M out must cost input($%g) + output($%g)",
				entry.Model, entry.InputUSD, entry.OutputUSD)
			require.Greater(t, got, 0.0, "%s bills nothing, which is never right", entry.Model)
		})
	}
}

// TestEveryModelRespondsToItsDiscount. A model whose charge does not move when
// its discount does is one where the discount silently does not apply -- the
// exact failure mode that moving models onto billing expressions could have
// introduced, and that nothing downstream would report.
func TestEveryModelRespondsToItsDiscount(t *testing.T) {
	for _, entry := range sweepableCatalog(t) {
		t.Run(entry.Model, func(t *testing.T) {
			withDiscount(t, `{}`)
			full := chargeUSD(t, entry.Model, "default", oneMillionEach())

			withDiscount(t, `{"`+entry.Model+`":0.25}`)
			quarter := chargeUSD(t, entry.Model, "default", oneMillionEach())

			require.InDelta(t, 0.25, quarter/full, 1e-9,
				"%s: a 0.25 discount must produce exactly a quarter of the charge", entry.Model)
		})
	}
}

// TestUpstreamCostIsOfficialPriceTimesThePurchasingRatio sweeps the cost side.
//
// Cost is modelled, not read, so the thing to prove is that the model is the
// vendor's published price scaled by what we negotiated -- and nothing else.
func TestUpstreamCostIsOfficialPriceTimesThePurchasingRatio(t *testing.T) {
	const channel = 42
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"42":0.6}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	})

	for _, entry := range sweepableCatalog(t) {
		t.Run(entry.Model, func(t *testing.T) {
			cost, priced := ratio_setting.UpstreamCostUSD(entry.Model, channel, 1_000_000, 0, 1_000_000)
			require.True(t, priced, "%s has a catalog row but cannot be costed", entry.Model)
			require.InDelta(t, (entry.InputUSD+entry.OutputUSD)*0.6, cost, 1e-9,
				"%s: cost must be the vendor's price times the purchasing ratio", entry.Model)
		})
	}
}

// TestACustomerDiscountNeverMovesUpstreamCost is the separation that makes a
// margin number mean anything.
//
// If a customer discount leaked into the cost model, reconciliation would
// report a healthy margin no matter what we charged -- the two sides would be
// derived from each other instead of independently.
func TestACustomerDiscountNeverMovesUpstreamCost(t *testing.T) {
	const channel = 43
	require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{"43":0.8}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateChannelCostRatioByJSONString(`{}`))
	})

	for _, entry := range sweepableCatalog(t) {
		withDiscount(t, `{}`)
		before, _ := ratio_setting.UpstreamCostUSD(entry.Model, channel, 1_000_000, 0, 1_000_000)

		withDiscount(t, `{"`+entry.Model+`":0.1}`)
		after, _ := ratio_setting.UpstreamCostUSD(entry.Model, channel, 1_000_000, 0, 1_000_000)

		require.InDelta(t, before, after, 1e-12,
			"%s: a 90%% customer discount changed the modelled UPSTREAM cost. "+
				"Revenue and cost must stay independently derived, or every margin is self-confirming.",
			entry.Model)
	}
}

// TestDeepSeekSellsAtSeventyPercent pins the commercial decision of
// 2026-08-31: the DeepSeek family goes to 0.7 for every existing customer.
//
// Written as dollars rather than as a ratio so the number in the test is the
// number on the invoice.
func TestDeepSeekSellsAtSeventyPercent(t *testing.T) {
	discounts := `{"deepseek-v3":0.7,"deepseek-v3.2":0.7,"deepseek-v3.2-thinking":0.7,` +
		`"deepseek-v4-flash":0.7,"deepseek-v4-pro":0.7}`
	withDiscount(t, discounts)

	// Official list, then what a customer pays at 0.7, for 1M in + 1M out.
	cases := map[string]float64{
		"deepseek-v4-flash": (0.44 + 1.32) * 0.7,
		"deepseek-v4-pro":   (1.32 + 3.96) * 0.7,
	}
	for model, want := range cases {
		require.InDelta(t, want, chargeUSD(t, model, "default", oneMillionEach()), 1e-6,
			"%s must bill at 70%% of DeepSeek's published price", model)
	}

	// And the family moves together -- a partial rollout is the shape of a
	// mistake, not of a decision.
	for _, model := range []string{
		"deepseek-v3", "deepseek-v3.2", "deepseek-v3.2-thinking",
		"deepseek-v4-flash", "deepseek-v4-pro",
	} {
		require.InDelta(t, 0.7, ratio_setting.GetModelDiscount(model), 1e-12,
			"%s is in the DeepSeek family and must carry the same 0.7", model)
	}
}

// TestPinnedDollarsForTheModelsThatCarryTheTraffic is the antidote to the
// self-confirmation above.
//
// These figures are typed by hand from each vendor's published price list, not
// read from the catalog, so editing a catalog row fails here too. Kept to the
// handful of models that carry real traffic -- pinning all 49 by hand would rot
// into a second catalog that disagrees with the first.
func TestPinnedDollarsForTheModelsThatCarryTheTraffic(t *testing.T) {
	withDiscount(t, `{}`)

	// model -> what 1M input + 1M output costs at the vendor's list price.
	pinned := map[string]float64{
		"claude-opus-4-8":   5 + 25,      // Anthropic $5 / $25
		"claude-sonnet-5":   2 + 10,      // Anthropic $2 / $10
		"gpt-4o":            2.5 + 10,    // OpenAI $2.50 / $10
		"gpt-5-mini":        0.25 + 2,    // OpenAI $0.25 / $2
		"deepseek-v4-flash": 0.44 + 1.32, // DeepSeek peak tier, 2026-08-16 increase
		"deepseek-v4-pro":   1.32 + 3.96,
		"glm-5.3":           1.4 + 4.4, // Zhipu $1.40 / $4.40
		"kimi-k3":           3 + 15,    // Moonshot $3 / $15
	}
	for model, want := range pinned {
		t.Run(model, func(t *testing.T) {
			require.InDelta(t, want, chargeUSD(t, model, "default", oneMillionEach()), 1e-6,
				"%s: 1M in + 1M out must cost $%g at the vendor's list price. "+
					"If the vendor repriced, change BOTH this number and the catalog, "+
					"and say where the new price was read from.", model, want)
		})
	}
}
