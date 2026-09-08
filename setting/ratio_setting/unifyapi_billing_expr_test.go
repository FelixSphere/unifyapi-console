package ratio_setting

// UNIFYAPI-FORK: tests for catalog-generated billing expressions.
//
// These exist because moving a model onto an expression moves it OFF the ratio
// path, and the ratio path is where the discount lives. Get that wrong and a
// model quietly sells at list while the console shows a discount -- which is the
// class of bug this whole exercise has been about.

import (
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exprDollars runs an expression and converts its output to dollars the way the
// settlement path does: expr.md specifies quota = output / 1,000,000 *
// QuotaPerUnit, so the output itself is dollars-per-million-tokens times raw
// token counts. Asserting on the raw output would have let a unit error pass --
// it nearly did.
func exprDollars(t *testing.T, expr string, params billingexpr.TokenParams) float64 {
	t.Helper()
	raw, _, err := billingexpr.RunExpr(expr, params)
	require.NoError(t, err)
	return raw / 1_000_000
}

// TestEveryGeneratedExpressionCompiles. A generated expression that does not
// parse would take the model's billing down entirely, and the generator is
// string concatenation -- exactly the thing that produces almost-valid syntax.
func TestEveryGeneratedExpressionCompiles(t *testing.T) {
	exprs := BillingExprs()
	require.NotEmpty(t, exprs, "the catalog has models needing expressions; generating none means the generator is broken")

	// The same vectors billing_setting.smokeTestExpr uses on save, plus a
	// long-context one, since half these expressions exist for the tier.
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 1_000_000, C: 1_000_000, Len: 1_000_000},
		{P: 300_000, C: 1000, Len: 300_000, CR: 50_000, AI: 1000},
	}
	for model, expr := range exprs {
		for _, v := range vectors {
			cost, _, err := billingexpr.RunExpr(expr, v)
			require.NoError(t, err, "%s rejected by the engine:\n%s", model, expr)
			require.GreaterOrEqual(t, cost, 0.0, "%s went negative at %+v", model, v)
		}
	}
}

// TestALongRequestIsBilledAtTheTierPrice runs the real engine and checks the
// DOLLARS, not that a string contains a number.
//
// This is the money the audit found we were not collecting: qwen3.7-plus costs
// 4x more per input token above 256K, and we charged the base rate at every
// length.
func TestALongRequestIsBilledAtTheTierPrice(t *testing.T) {
	entry, ok := CatalogEntryFor("qwen3.7-plus")
	require.True(t, ok)
	expr := entry.BillingExpr(1)

	// 1M input just under the threshold is impossible, so compare like for
	// like: 100k of input, once below the threshold and once above.
	below := exprDollars(t, expr, billingexpr.TokenParams{P: 100_000, C: 0, Len: 100_000})
	above := exprDollars(t, expr, billingexpr.TokenParams{P: 100_000, C: 0, Len: 300_000})

	// $0.50 and $2.00 per 1M -> 100k costs $0.05 and $0.20.
	require.InDelta(t, 0.05, below, 1e-9)
	require.InDelta(t, 0.20, above, 1e-9)
	require.InDelta(t, 4.0, above/below, 1e-9,
		"the vendor charges 4x above 256K; billing the base rate there is the gap this closes")
}

// TestAudioTokensAreBilledAtTheAudioPrice -- the other half of the audit.
// Google charges 3.3x the text rate for audio on gemini-2.5-flash, and those
// tokens used to fall through to the text price.
func TestAudioTokensAreBilledAtTheAudioPrice(t *testing.T) {
	entry, ok := CatalogEntryFor("gemini-2.5-flash")
	require.True(t, ok)
	expr := entry.BillingExpr(1)

	// P is 0 and AI is 1M: the caller subtracts separately-priced
	// sub-categories from `p` BEFORE the expression runs (expr.md's
	// auto-exclusion), so passing both here would double-count and read as
	// $1.30. Getting that wrong in the test is how you would "prove" a bill
	// that the relay never produces.
	cost := exprDollars(t, expr, billingexpr.TokenParams{
		P: 0, C: 0, Len: 1_000_000, AI: 1_000_000,
	})
	require.InDelta(t, 1.00, cost, 1e-9,
		"audio must bill at $1.00/1M, not the $0.30 text rate")

	// And the discount reaches it.
	discounted := exprDollars(t, entry.BillingExpr(0.5), billingexpr.TokenParams{
		P: 0, C: 0, Len: 1_000_000, AI: 1_000_000,
	})
	require.InDelta(t, 0.50, discounted, 1e-9)

	// The finding restated as money: the same million tokens as TEXT costs
	// $0.30. Billing audio at the text rate under-charged by 3.3x.
	asText := exprDollars(t, expr, billingexpr.TokenParams{P: 1_000_000, C: 0, Len: 1_000_000})
	require.InDelta(t, 0.30, asText, 1e-9)
	require.InDelta(t, 3.3333, cost/asText, 1e-3)
}

// TestFlatPricesStillUseTheRatioPath. Only models the flat maps cannot express
// should move; putting the whole catalog on expressions would abandon a
// well-exercised path for no reason.
func TestFlatPricesStillUseTheRatioPath(t *testing.T) {
	exprs := BillingExprs()
	for _, model := range []string{"claude-opus-4-8", "gpt-4o", "claude-sonnet-5", "kimi-k3"} {
		assert.NotContains(t, exprs, model, "%s is flat-priced and must stay on the ratio path", model)
	}
	// And the ones that genuinely cannot be expressed flatly did move.
	for _, model := range []string{
		"gemini-2.5-flash", // audio priced above text
		"gemini-2.5-pro",   // context tier
		"qwen3.7-plus",     // context tier
	} {
		assert.Contains(t, exprs, model, "%s cannot be expressed by the flat maps", model)
	}
}

// TestTheDiscountIsBakedIntoEveryCoefficient is the one that matters most.
//
// Tiered billing applies the group ratio and never touches modelRatioMap, so a
// model on an expression gets its ModelDiscount ONLY if the coefficients carry
// it. Nothing downstream would notice; the model would just sell at list.
func TestTheDiscountIsBakedIntoEveryCoefficient(t *testing.T) {
	entry, ok := CatalogEntryFor("gemini-2.5-pro")
	require.True(t, ok)
	require.NotNil(t, entry.ContextTier)

	full := entry.BillingExpr(1)
	half := entry.BillingExpr(0.5)

	// $1.25 input at list, $0.625 at half; and the LONG tier must halve too --
	// discounting only the base tier would overcharge exactly the expensive
	// requests.
	assert.Contains(t, full, "p * 1.25")
	assert.Contains(t, half, "p * 0.625")
	assert.Contains(t, full, "p * 2.5")  // long tier at list
	assert.Contains(t, half, "p * 1.25") // long tier halved
	assert.Contains(t, half, "c * 5")    // output 10 -> 5
	assert.Contains(t, half, "c * 7.5")  // long output 15 -> 7.5
	assert.Contains(t, half, "cr * 0.0625")

	// The threshold is a token count, not a price, and must never be scaled.
	assert.Contains(t, half, "len <= 200000")
}

// TestAudioCoefficientIsDiscountedToo. Audio input was the field with no home
// in the flat maps; it would be easy to add it to the expression and forget it
// in the discount pass.
func TestAudioCoefficientIsDiscountedToo(t *testing.T) {
	entry, ok := CatalogEntryFor("gemini-2.5-flash")
	require.True(t, ok)
	assert.Contains(t, entry.BillingExpr(1), "ai * 1")
	assert.Contains(t, entry.BillingExpr(0.25), "ai * 0.25")
}

// TestTierConditionUsesLenNotP. expr.md is explicit: `p` has sub-categories
// subtracted from it, so a cache-heavy long request would have a small `p` and
// fall into the cheap tier while actually being a long request. `len` is the
// full input length regardless.
func TestTierConditionUsesLenNotP(t *testing.T) {
	for model, expr := range BillingExprs() {
		if !strings.Contains(expr, "?") {
			continue
		}
		condition := strings.SplitN(expr, "?", 2)[0]
		assert.Contains(t, condition, "len", "%s: tier condition must key off len", model)
		assert.NotContains(t, condition, "p ",
			"%s: keying the tier off p lets a cache hit drop a long request into the cheap tier", model)
	}
}

// TestGeneratedPricesAreRealDollars. The expression engine takes real $/1M
// prices, not the /2 ratio convention. Mixing the two would halve or double
// every bill on these models.
func TestGeneratedPricesAreRealDollars(t *testing.T) {
	entry, ok := CatalogEntryFor("gemini-2.5-flash")
	require.True(t, ok)
	expr := entry.BillingExpr(1)
	assert.Contains(t, expr, "p * 0.3", "input is $0.30/1M and must appear as 0.3, not as a ratio")
	assert.Contains(t, expr, "c * 2.5")
}

// TestNoExponentNotation. A coefficient rendered as 1e-06 is not accepted by
// the parser, and the cheapest catalogued cache prices are small enough to
// trigger %g's switch to exponent form.
func TestNoExponentNotation(t *testing.T) {
	for model, expr := range BillingExprs() {
		assert.NotContains(t, expr, "e-", "%s: exponent notation does not parse:\n%s", model, expr)
		assert.NotContains(t, expr, "e+", "%s: %s", model, expr)
	}
	// A deliberately tiny coefficient, to prove the formatter rather than the
	// current catalog's luck.
	entry := CatalogEntry{Model: "x", InputUSD: 0.0000001, OutputUSD: 1, AudioInputUSD: 0.0000002}
	assert.NotContains(t, entry.BillingExpr(1), "e-")
}

// TestRebuildInstallsTheExpressions -- generation is useless if nothing loads
// it. This checks the wiring end to end: after a rebuild, the engine reports
// these models as tiered and hands back the generated expression.
func TestRebuildInstallsTheExpressions(t *testing.T) {
	InitRatioSettings()

	for _, model := range []string{"gemini-2.5-pro", "qwen3.7-plus", "gemini-2.5-flash"} {
		assert.Equal(t, billing_setting.BillingModeTieredExpr, billing_setting.GetBillingMode(model),
			"%s must bill through its expression, not the flat ratio", model)
		expr, ok := billing_setting.GetBillingExpr(model)
		assert.True(t, ok, "%s has no installed expression", model)
		assert.Equal(t, BillingExprs()[model], expr)
	}

	// A flat-priced model must stay on the ratio path.
	assert.Equal(t, billing_setting.BillingModeRatio, billing_setting.GetBillingMode("claude-opus-4-8"))
}

// TestChangingADiscountReinstallsTheExpressions. The coefficients carry the
// discount, so an expression generated before a discount change would keep
// billing the old price forever -- silently, since nothing downstream reads
// modelRatioMap for these models.
func TestChangingADiscountReinstallsTheExpressions(t *testing.T) {
	InitRatioSettings()
	previous := ModelDiscount2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateModelDiscountByJSONString(previous)) })

	before, ok := billing_setting.GetBillingExpr("gemini-2.5-pro")
	require.True(t, ok)
	assert.Contains(t, before, "p * 1.25")

	require.NoError(t, UpdateModelDiscountByJSONString(`{"gemini-2.5-pro":0.4}`))

	after, ok := billing_setting.GetBillingExpr("gemini-2.5-pro")
	require.True(t, ok)
	assert.Contains(t, after, "p * 0.5", "1.25 x 0.4 = 0.5")
	assert.Contains(t, after, "p * 1", "the long tier 2.5 x 0.4 = 1")
	assert.NotEqual(t, before, after)
}

// TestAnAdminExpressionForAnUncataloguedModelSurvives. The catalog replaces its
// own entries wholesale; it must not take a hand-written one with it.
func TestAnAdminExpressionForAnUncataloguedModelSurvives(t *testing.T) {
	InitRatioSettings()
	billing_setting.SetBillingExprForTest("hand-written-model", `tier("base", p * 9 + c * 9)`)

	InitRatioSettings() // a rebuild, e.g. after a discount change

	expr, ok := billing_setting.GetBillingExpr("hand-written-model")
	assert.True(t, ok, "a rebuild must not delete an expression the catalog does not manage")
	assert.Contains(t, expr, "p * 9")
}
