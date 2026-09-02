package ratio_setting

// UNIFYAPI-FORK: tests for the per-model customer discount and the per-channel
// upstream cost -- prices two and three of the three the fork keeps apart.
//
// The property that matters most is the separation itself: a discount must move
// what the customer pays and leave the official price alone, and a channel cost
// must move nothing a customer sees at all.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func withCleanDiscounts(t *testing.T) {
	t.Helper()
	require.NoError(t, UpdateModelDiscountByJSONString(`{}`))
	t.Cleanup(func() {
		require.NoError(t, UpdateModelDiscountByJSONString(`{}`))
	})
}

func TestNoDiscountMeansTheOfficialPrice(t *testing.T) {
	withCleanDiscounts(t)

	// claude-opus-4-8 is $5/1M input officially, i.e. ratio 2.5.
	ratio, ok, _ := GetModelRatio("claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 2.5, ratio, 1e-9)
	require.InDelta(t, 1, GetModelDiscount("claude-opus-4-8"), 1e-9)
}

// TestDiscountScalesTheBillingRatio is the seam the whole design rests on: the
// discount is folded into modelRatioMap, so every billing path -- text, audio,
// image, task, tiered -- and GET /api/pricing pick it up without any of them
// knowing discounts exist.
func TestDiscountScalesTheBillingRatio(t *testing.T) {
	withCleanDiscounts(t)

	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-opus-4-8": 0.8}`))

	ratio, ok, _ := GetModelRatio("claude-opus-4-8")
	require.True(t, ok)
	require.InDelta(t, 2.0, ratio, 1e-9, "2.5 official x 0.8 discount")

	// The official price in the catalog is untouched -- that is the point.
	entry, found := CatalogEntryFor("claude-opus-4-8")
	require.True(t, found)
	require.InDelta(t, 5, entry.InputUSD, 1e-9)
	require.InDelta(t, 2.5, entry.ModelRatio(), 1e-9)
}

// TestDiscountAlsoDiscountsOutputAndCache -- completion and cache ratios are
// multipliers ON the model ratio, so discounting the model ratio has to take
// the same percentage off output and cached reads. A "20% off" that only
// applied to input would be a pricing bug customers would find first.
func TestDiscountAlsoDiscountsOutputAndCache(t *testing.T) {
	withCleanDiscounts(t)
	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-opus-4-8": 0.5}`))

	price, ok := CustomerPriceFor("claude-opus-4-8", "nonexistent-group-defaults-to-1")
	require.True(t, ok)

	require.InDelta(t, 5, price.OfficialInputUSD, 1e-9)
	require.InDelta(t, 25, price.OfficialOutputUS, 1e-9)
	require.InDelta(t, 2.5, price.InputUSD, 1e-9, "half of $5")
	require.InDelta(t, 12.5, price.OutputUSD, 1e-9, "half of $25")
	require.InDelta(t, 0.25, price.CacheReadUSD, 1e-9, "half of $0.50")
	require.InDelta(t, 3.125, price.CacheWriteUSD, 1e-9, "half of $6.25")
}

// TestFable51CanBeDiscountedFromItsOfficialPrice pins the exact row the admin
// sees under Official price & discount. The official quote stays read-only;
// changing the discount must scale input, output and the newly reduced cache
// price together.
func TestFable51CanBeDiscountedFromItsOfficialPrice(t *testing.T) {
	withCleanDiscounts(t)
	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-fable-5.1": 0.8}`))

	price, ok := CustomerPriceFor("claude-fable-5.1", "default")
	require.True(t, ok, "the model must be present in the official-price catalog")
	require.InDelta(t, 10, price.OfficialInputUSD, 1e-9)
	require.InDelta(t, 50, price.OfficialOutputUS, 1e-9)
	require.InDelta(t, 8, price.InputUSD, 1e-9, "$10 official x 0.8 discount")
	require.InDelta(t, 40, price.OutputUSD, 1e-9, "$50 official x 0.8 discount")
	require.InDelta(t, 0.20, price.CacheReadUSD, 1e-9, "$0.25 official x 0.8 discount")
	require.InDelta(t, 10, price.CacheWriteUSD, 1e-9, "$12.50 official x 0.8 discount")
}

// TestRemovingADiscountRestoresTheOfficialPrice -- the map is rebuilt rather
// than mutated, so a removed discount cannot leave its last value behind. That
// failure mode is what makes a hand-edited ratio table impossible to reason
// about after a few rounds of edits.
func TestRemovingADiscountRestoresTheOfficialPrice(t *testing.T) {
	withCleanDiscounts(t)

	require.NoError(t, UpdateModelDiscountByJSONString(`{"gpt-4o": 0.5}`))
	ratio, _, _ := GetModelRatio("gpt-4o")
	require.InDelta(t, 0.625, ratio, 1e-9)

	require.NoError(t, UpdateModelDiscountByJSONString(`{}`))
	ratio, _, _ = GetModelRatio("gpt-4o")
	require.InDelta(t, 1.25, ratio, 1e-9, "removing the discount must go back to $2.50/1M")
}

func TestDiscountRejectsUnusableValues(t *testing.T) {
	withCleanDiscounts(t)

	for _, tc := range []struct {
		name, payload, wantMessage string
	}{
		{"uncatalogued model", `{"gpt-4-32k": 0.5}`, "not in the pricing catalog"},
		{"zero", `{"gpt-4o": 0}`, "must be greater than 0"},
		{"negative", `{"gpt-4o": -1}`, "must be greater than 0"},
		{"absurd markup", `{"gpt-4o": 50}`, "sanity bound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := UpdateModelDiscountByJSONString(tc.payload)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)

			// A rejected save must not have applied anything.
			ratio, _, _ := GetModelRatio("gpt-4o")
			require.InDelta(t, 1.25, ratio, 1e-9, "a rejected discount must leave prices alone")
		})
	}
}

// TestMarkupIsAllowedButReported -- selling above list is a real business
// decision (four models were doing it on production), so it must be possible;
// it just must never be silent.
func TestMarkupIsAllowedButReported(t *testing.T) {
	withCleanDiscounts(t)

	problems, markups := ValidateModelDiscounts(map[string]float64{"gemini-flash-latest": 2})
	require.Empty(t, problems)
	require.Len(t, markups, 1)
	require.Contains(t, markups[0], "markup")

	require.NoError(t, UpdateModelDiscountByJSONString(`{"gemini-flash-latest": 2}`))
	ratio, _, _ := GetModelRatio("gemini-flash-latest")
	require.InDelta(t, 0.75, ratio, 1e-9, "$0.75 official x 2 = ratio 0.75 (i.e. $1.50/1M)")
}

// TestCustomerPriceMultipliesDiscountAndGroupRatio pins the full customer-facing
// formula, which is the number an invoice has to be explainable by.
func TestCustomerPriceMultipliesDiscountAndGroupRatio(t *testing.T) {
	withCleanDiscounts(t)

	require.NoError(t, UpdateGroupRatioByJSONString(`{"Standard User": 1, "Vip User": 0.8}`))
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupRatioByJSONString(`{}`))
	})
	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-opus-5": 0.9}`))

	standard, ok := CustomerPriceFor("claude-opus-5", "Standard User")
	require.True(t, ok)
	require.InDelta(t, 4.5, standard.InputUSD, 1e-9, "$5 x 0.9 x 1.0")

	vip, ok := CustomerPriceFor("claude-opus-5", "Vip User")
	require.True(t, ok)
	require.InDelta(t, 3.6, vip.InputUSD, 1e-9, "$5 x 0.9 x 0.8")
	require.InDelta(t, 18, vip.OutputUSD, 1e-9, "$25 x 0.9 x 0.8")

	// The decomposition has to be reported, not just the product, so an invoice
	// dispute can be answered without re-deriving anything.
	require.InDelta(t, 0.9, vip.ModelDiscount, 1e-9)
	require.InDelta(t, 0.8, vip.GroupRatio, 1e-9)
	require.InDelta(t, 5, vip.OfficialInputUSD, 1e-9)
}

func TestCustomerPriceRejectsUncataloguedModel(t *testing.T) {
	_, ok := CustomerPriceFor("gpt-4-32k", "Standard User")
	require.False(t, ok, "a model with no official price has no customer price either")
}

// --- per-channel upstream cost ---

func withCleanChannelCosts(t *testing.T) {
	t.Helper()
	require.NoError(t, UpdateChannelCostRatioByJSONString(`{}`))
	t.Cleanup(func() {
		require.NoError(t, UpdateChannelCostRatioByJSONString(`{}`))
	})
}

func TestUnconfiguredChannelCostsListPrice(t *testing.T) {
	withCleanChannelCosts(t)
	require.InDelta(t, 1, GetChannelCostRatio(999), 1e-9,
		"defaulting to list is the conservative choice: it can only understate margin")
}

func TestUpstreamCostUsesOfficialPriceNotTheDiscountedOne(t *testing.T) {
	withCleanDiscounts(t)
	withCleanChannelCosts(t)

	// A deep customer discount must not make our own cost look cheaper.
	require.NoError(t, UpdateModelDiscountByJSONString(`{"gpt-4o": 0.1}`))

	cost, ok := UpstreamCostUSD("gpt-4o", 1, 1_000_000, 0, 0)
	require.True(t, ok)
	require.InDelta(t, 2.50, cost, 1e-9,
		"cost is what the vendor charges us; a customer discount is irrelevant to it")
}

func TestUpstreamCostClampsCachedTokensToPromptTokens(t *testing.T) {
	withCleanChannelCosts(t)

	// A malformed row claiming more cached tokens than prompt tokens must not
	// produce a negative fresh-token count and a nonsense cost.
	cost, ok := UpstreamCostUSD("gpt-4o", 1, 1000, 5000, 0)
	require.True(t, ok)
	atFullCache, _ := UpstreamCostUSD("gpt-4o", 1, 1000, 1000, 0)
	require.InDelta(t, atFullCache, cost, 1e-12)
	require.Greater(t, cost, 0.0)
}

// TestUpstreamCostChargesFullInputWhenNoCachePriceExists -- a vendor that does
// not publish a cached-read price still bills those tokens at full input price.
// Treating them as free would silently understate cost.
func TestUpstreamCostChargesFullInputWhenNoCachePriceExists(t *testing.T) {
	withCleanChannelCosts(t)

	entry, ok := CatalogEntryFor("gemini-3-pro-image")
	require.True(t, ok)
	require.Zero(t, entry.CacheReadUSD, "fixture assumption: this model has no cache price")

	cost, priced := UpstreamCostUSD("gemini-3-pro-image", 1, 1_000_000, 1_000_000, 0)
	require.True(t, priced)
	require.InDelta(t, 2.0, cost, 1e-9, "$2/1M input, cached or not")
}

func TestUpstreamCostReportsUnknownModel(t *testing.T) {
	_, ok := UpstreamCostUSD("gpt-4-32k", 1, 1_000_000, 0, 1_000_000)
	require.False(t, ok, "an uncostable model must be reported, never counted as free")
}

func TestChannelCostValidationRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ratios      map[string]float64
		wantMessage string
	}{
		{"non-numeric key", map[string]float64{"openai-direct": 0.9}, "keyed by numeric channel id"},
		{"zero", map[string]float64{"3": 0}, "must be greater than 0"},
		{"absurd", map[string]float64{"3": 99}, "sanity bound"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := ValidateChannelCostRatios(tc.ratios)
			require.NotEmpty(t, problems)
			require.Contains(t, problems[0].Error(), tc.wantMessage)
		})
	}

	require.Empty(t, ValidateChannelCostRatios(map[string]float64{"1": 0.85, "2": 1}))
}
