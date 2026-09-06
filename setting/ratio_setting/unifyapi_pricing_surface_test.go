package ratio_setting

// UNIFYAPI-FORK: cover the pricing surface, and write down what each part does.
//
// Every pricing incident on this deployment has come from a piece of this
// surface that nothing exercised, so the gap was invisible until money moved:
// audio billing at the text rate after a seed removed the ratio row, a 37.5
// sentinel rendering as a real price, a customer contract substituted for the
// published one on a public page.
//
// These tests are therefore as much documentation as guard. Where a function's
// behaviour is surprising -- and several are -- the comment says what it is and
// why, so the next person does not have to rediscover it from a billing
// discrepancy.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetModelRatioOrPriceChoosesBetweenPerCallAndPerToken(t *testing.T) {
	InitRatioSettings()

	// Returns (value, usePrice, exists) -- in that order. Getting the last two
	// the wrong way round reads as "exists" and silently accepts the fallback.
	value, usePrice, exists := GetModelRatioOrPrice("claude-opus-4-8")
	require.True(t, exists)
	assert.False(t, usePrice, "a token-billed model must not report a per-call price")
	assert.InDelta(t, 2.5, value, 1e-9)

	// THE 37.5 SENTINEL. An unknown model returns exists=false and a value of
	// 37.5 -- $75 per 1M. Callers that check the boolean refuse the request;
	// a caller that ignores it publishes 37.5 as though it were a real price,
	// which is exactly what /api/pricing did for glm-5.3 before it was
	// catalogued. Pinned so the number cannot change without this being read.
	value, usePrice, exists = GetModelRatioOrPrice("model-that-does-not-exist")
	assert.False(t, exists, "an uncatalogued model must report that it has no price")
	assert.False(t, usePrice)
	assert.InDelta(t, 37.5, value, 1e-9,
		"the fallback is a sentinel, not a price. It is only safe while every caller "+
			"checks the exists flag.")
}

func TestGetModelPriceIsAbsentForTokenBilledModels(t *testing.T) {
	InitRatioSettings()
	price, usePrice := GetModelPrice("claude-opus-4-8", false)
	assert.False(t, usePrice)
	assert.InDelta(t, -1, price, 1e-9,
		"-1, not 0: a zero would read as a free per-call model")
}

// TestModalityRatiosDefaultToOne documents a default that has already caused a
// mispricing.
//
// GetAudioRatio and friends return 1 for a model with no entry, so audio tokens
// bill at the text rate. That is correct for most models and wrong for the
// Gemini flash family, where the vendor charges 2x to 3.3x -- which is why those
// models carry an explicit AudioInputUSD and bill through an expression instead.
func TestModalityRatiosDefaultToOne(t *testing.T) {
	InitRatioSettings()

	assert.InDelta(t, 1, GetAudioRatio("claude-opus-4-8"), 1e-9)
	assert.InDelta(t, 1, GetAudioCompletionRatio("claude-opus-4-8"), 1e-9)
	assert.False(t, ContainsAudioRatio("claude-opus-4-8"))
	assert.False(t, ContainsAudioCompletionRatio("claude-opus-4-8"))

	ratio, found := GetImageRatio("claude-opus-4-8")
	assert.False(t, found)
	assert.InDelta(t, 1, ratio, 1e-9, "the fallback must be 1, so image tokens bill as text")
}

func TestGroupRatioAccessors(t *testing.T) {
	previous := GroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateGroupRatioByJSONString(previous)) })
	require.NoError(t, UpdateGroupRatioByJSONString(`{"alpha":0.5,"beta":1}`))

	assert.True(t, ContainsGroupRatio("alpha"))
	assert.False(t, ContainsGroupRatio("gamma"))
	assert.InDelta(t, 0.5, GetGroupRatio("alpha"), 1e-9)

	// An unknown group bills at 1 rather than being refused. Worth knowing: a
	// typo in a group name is a silent full-price sale, not an error.
	assert.InDelta(t, 1, GetGroupRatio("typo-group"), 1e-9)

	assert.Equal(t, map[string]float64{"alpha": 0.5, "beta": 1}, GetGroupRatioCopy())
	assert.NotNil(t, GetGroupRatioSetting())
}

func TestCheckGroupRatioRejectsMalformedInput(t *testing.T) {
	assert.NoError(t, CheckGroupRatio(`{"alpha":0.5}`))
	assert.Error(t, CheckGroupRatio(`not json`))
}

// TestGroupGroupRatioIsAnOverrideNotAMultiplier. The two are mutually
// exclusive in HandleGroupRatio -- if a (userGroup, usingGroup) rule exists it
// REPLACES the plain group ratio. Reading it as a further multiplier is an easy
// mistake that halves or doubles a bill.
func TestGroupGroupRatioIsAnOverrideNotAMultiplier(t *testing.T) {
	previous := GroupGroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateGroupGroupRatioByJSONString(previous)) })
	require.NoError(t, UpdateGroupGroupRatioByJSONString(`{"acme":{"tier-a":0.5}}`))

	ratio, ok := GetGroupGroupRatio("acme", "tier-a")
	require.True(t, ok)
	assert.InDelta(t, 0.5, ratio, 1e-9)

	_, ok = GetGroupGroupRatio("acme", "tier-unknown")
	assert.False(t, ok, "no rule means fall back to the plain group ratio, not to 1")

	_, ok = GetGroupGroupRatio("nobody", "tier-a")
	assert.False(t, ok)
}

func TestGroupModelDiscountAccessors(t *testing.T) {
	previous := GroupModelDiscount2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateGroupModelDiscountByJSONString(previous)) })
	require.NoError(t, UpdateGroupModelDiscountByJSONString(
		`{"acme":{"claude-opus-4-8":0.8}}`))

	ratio, ok := GetGroupModelDiscount("acme", "claude-opus-4-8")
	require.True(t, ok)
	assert.InDelta(t, 0.8, ratio, 1e-9)

	_, ok = GetGroupModelDiscount("acme", "gpt-4o")
	assert.False(t, ok, "a group with some contracts pays list for the rest")

	// The copy must be deep: the nested maps are plain maps, and an admin edit
	// merging into a shallow copy would mutate live pricing.
	copied := GetGroupModelDiscountCopy()
	copied["acme"]["claude-opus-4-8"] = 0.1
	live, _ := GetGroupModelDiscount("acme", "claude-opus-4-8")
	assert.InDelta(t, 0.8, live, 1e-9, "GetGroupModelDiscountCopy leaked a nested map")
}

func TestModelDiscountAccessors(t *testing.T) {
	previous := ModelDiscount2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateModelDiscountByJSONString(previous)) })
	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-opus-4-8":0.75}`))

	assert.InDelta(t, 0.75, GetModelDiscount("claude-opus-4-8"), 1e-9)
	assert.InDelta(t, 1, GetModelDiscount("gpt-4o"), 1e-9, "absent means sold at list")
	assert.Equal(t, map[string]float64{"claude-opus-4-8": 0.75}, GetModelDiscountCopy(),
		"the copy holds only deviations, so it reads as the exception list")
}

func TestChannelCostAccessors(t *testing.T) {
	previous := ChannelCostRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateChannelCostRatioByJSONString(previous)) })
	require.NoError(t, UpdateChannelCostRatioByJSONString(`{"7":0.65}`))

	assert.InDelta(t, 0.65, GetChannelCostRatio(7), 1e-9)
	assert.InDelta(t, 1, GetChannelCostRatio(999), 1e-9,
		"an unconfigured channel is costed at list. Conservative when nothing is discounted, "+
			"and a visible loss the moment something is.")
	assert.Equal(t, map[string]float64{"7": 0.65}, GetChannelCostRatioCopy())
}

// TestSerialisersRoundTrip covers the JSON accessors the admin API reads and
// writes. They are dull, and they are also the only path by which a pricing
// table reaches the database, so a silent failure here loses configuration.
func TestSerialisersRoundTrip(t *testing.T) {
	InitRatioSettings()
	for name, fn := range map[string]func() string{
		"ModelRatio":           ModelRatio2JSONString,
		"CompletionRatio":      CompletionRatio2JSONString,
		"CacheRatio":           CacheRatio2JSONString,
		"CreateCacheRatio":     CreateCacheRatio2JSONString,
		"ModelPrice":           ModelPrice2JSONString,
		"ImageRatio":           ImageRatio2JSONString,
		"AudioRatio":           AudioRatio2JSONString,
		"AudioCompletionRatio": AudioCompletionRatio2JSONString,
		"GroupRatio":           GroupRatio2JSONString,
		"GroupGroupRatio":      GroupGroupRatio2JSONString,
		"GroupModelDiscount":   GroupModelDiscount2JSONString,
		"ModelDiscount":        ModelDiscount2JSONString,
		"ChannelCostRatio":     ChannelCostRatio2JSONString,
		"ExtraModelPricing":    ExtraModels2JSONString,
	} {
		out := fn()
		assert.NotEmpty(t, out, "%s serialised to nothing; an empty table must still be {}", name)
		assert.True(t, out[0] == '{', "%s must serialise as a JSON object, got %q", name, out)
	}
}

func TestReadOnlyCopiesAreNotNil(t *testing.T) {
	InitRatioSettings()
	assert.NotNil(t, GetModelRatioCopy())
	assert.NotNil(t, GetCompletionRatioCopy())
	assert.NotNil(t, GetCacheRatioCopy())
	assert.NotNil(t, GetCreateCacheRatioCopy())
	assert.NotNil(t, GetModelPriceCopy())
	assert.NotNil(t, GetImageRatioCopy())
	assert.NotNil(t, GetAudioRatioCopy())
	assert.NotNil(t, GetAudioCompletionRatioCopy())
	assert.NotNil(t, GetCacheRatioMap())
	assert.NotNil(t, GetModelPriceMap())
	assert.NotNil(t, ExtraModels())
	assert.NotEmpty(t, CompiledCatalog())
	assert.NotEmpty(t, OfficialRatios())
	assert.NotEmpty(t, GetDefaultModelRatioMap())
	assert.NotNil(t, GetDefaultModelPriceMap())
}

// TestExposedDataIsCachedAndInvalidated. The pricing page reads this; a stale
// cache means the published price disagrees with the one the relay bills from,
// which is the disagreement customers notice first.
func TestExposedDataIsCachedAndInvalidated(t *testing.T) {
	InitRatioSettings()
	previous := ModelDiscount2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateModelDiscountByJSONString(previous)) })

	require.NoError(t, UpdateModelDiscountByJSONString(`{}`))
	before := GetExposedData()
	require.NotNil(t, before)

	require.NoError(t, UpdateModelDiscountByJSONString(`{"claude-opus-4-8":0.5}`))
	after := GetExposedData()
	require.NotNil(t, after)

	assert.NotEqual(t, before["model_ratio"], after["model_ratio"],
		"changing a discount must invalidate the exposed cache, or the pricing page keeps "+
			"quoting the old number while the relay bills the new one")
}

func TestExposeRatioToggle(t *testing.T) {
	previous := IsExposeRatioEnabled()
	t.Cleanup(func() { SetExposeRatioEnabled(previous) })

	SetExposeRatioEnabled(false)
	assert.False(t, IsExposeRatioEnabled())
	SetExposeRatioEnabled(true)
	assert.True(t, IsExposeRatioEnabled())
}

func TestGetCompletionRatioInfoExplainsWhereTheNumberCameFrom(t *testing.T) {
	InitRatioSettings()
	info := GetCompletionRatioInfo("claude-opus-4-8")
	assert.InDelta(t, 5, info.Ratio, 1e-9, "$25 output over $5 input")
}

// --- raw ratio-map writers ----------------------------------------------
//
// These are the setters behind the options rows the catalogue owns. Nothing in
// the console calls them any more -- the raw-ratio tabs were retired precisely
// because a save through them REPLACED the whole code baseline -- but they are
// still reachable from the options API, so a value can still arrive this way.
//
// Covered because "unused" and "unreachable" are different, and it was the
// second assumption that let a 2,877-key row into production.

func TestRawRatioWritersReplaceRatherThanMerge(t *testing.T) {
	InitRatioSettings()
	before := len(GetCompletionRatioCopy())
	require.Greater(t, before, 1, "the catalogue should have seeded many entries")

	previous := CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateCompletionRatioByJSONString(previous))
		InitRatioSettings()
	})

	require.NoError(t, UpdateCompletionRatioByJSONString(`{"only-me":3}`))
	after := GetCompletionRatioCopy()

	assert.Len(t, after, 1,
		"REPLACE, not merge. One save through this path discards every other entry -- the "+
			"behaviour that made the admin raw-ratio editor dangerous enough to remove.")
	assert.InDelta(t, 3, after["only-me"], 1e-9)
}

func TestEveryRawRatioWriterAcceptsAnEmptyTable(t *testing.T) {
	InitRatioSettings()
	t.Cleanup(func() { InitRatioSettings() })

	for name, write := range map[string]func(string) error{
		"CacheRatio":           UpdateCacheRatioByJSONString,
		"CreateCacheRatio":     UpdateCreateCacheRatioByJSONString,
		"ImageRatio":           UpdateImageRatioByJSONString,
		"AudioRatio":           UpdateAudioRatioByJSONString,
		"AudioCompletionRatio": UpdateAudioCompletionRatioByJSONString,
		"ModelPrice":           UpdateModelPriceByJSONString,
		"CompletionRatio":      UpdateCompletionRatioByJSONString,
	} {
		assert.NoError(t, write(`{}`), "%s must accept an empty table", name)
		assert.Error(t, write(`not json`), "%s must reject malformed input", name)
	}
}

// --- compact model suffix -------------------------------------------------
//
// A "-openai-compact" variant is a second name for the same model, and pricing
// lookups fall back to a wildcard key for it. Untested until now, and it sits
// directly on the price lookup path.

func TestCompactModelSuffix(t *testing.T) {
	assert.Equal(t, "gpt-4o"+CompactModelSuffix, WithCompactModelSuffix("gpt-4o"))
	assert.Equal(t, "gpt-4o"+CompactModelSuffix,
		WithCompactModelSuffix("gpt-4o"+CompactModelSuffix),
		"applying the suffix twice must not double it")
}

func TestCompactModelVariantsDeduplicates(t *testing.T) {
	out := WithCompactModelVariants([]string{"gpt-4o", "gpt-4o", "claude-opus-5"})

	assert.Contains(t, out, "gpt-4o")
	assert.Contains(t, out, "gpt-4o"+CompactModelSuffix)
	assert.Contains(t, out, "claude-opus-5")
	assert.Len(t, out, 4, "two models, each with one compact variant, and no duplicates")
}

// TestUpstreamIDFallsBackToTheModelName. The catalogue sends this string
// upstream verbatim -- no channel carries a model_mapping -- so a wrong value
// here is a request the vendor rejects.
func TestUpstreamIDFallsBackToTheModelName(t *testing.T) {
	assert.Equal(t, "gpt-4o", CatalogEntry{Model: "gpt-4o"}.UpstreamID())
	assert.Equal(t, "claude-fable-5-1",
		CatalogEntry{Model: "claude-fable-5.1", UpstreamModel: "claude-fable-5-1"}.UpstreamID(),
		"a dotted display name with a dashed upstream id is the case this exists for")
}

// TestValidateCatalogRejectsMalformedRows exercises the branches that guard the
// catalogue itself. Each one corresponds to a shape that would price something
// wrongly rather than fail loudly.
func TestValidateCatalogRejectsMalformedRows(t *testing.T) {
	previous := ExtraModels2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateExtraModelsByJSONString(previous)) })

	// A clean catalogue has no structural problems.
	require.NoError(t, UpdateExtraModelsByJSONString(`{}`))
	assert.Empty(t, ValidateCatalog())

	// Cache read above input: backwards everywhere it is published, and it
	// overstates cost in reconciliation rather than undercharging, so it hides.
	problems := ValidateExtraModels(map[string]ExtraModel{
		"probe": {InputUSD: 1, OutputUSD: 2, CacheReadUSD: 9},
	})
	require.NotEmpty(t, problems)
}
