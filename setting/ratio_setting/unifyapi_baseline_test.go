package ratio_setting

// UNIFYAPI-FORK: regression tests for the pricing baseline.
//
// The failures these exist to prevent, all of which happened or nearly
// happened on production:
//
//   * a hand-typed ratio drifting off the vendor's list price with nothing to
//     notice (claude-opus-4-8 sold at 0.085x of Anthropic's price for weeks)
//   * the admin "reset ratios" button wiping every model we sell, because the
//     loader replaces the ratio map instead of merging into it
//   * upstream's hardcoded completion-ratio table silently overriding a
//     catalogued price, so the number in the catalog is not the number billed
//   * a model reaching the relay with no price at all

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// publishedModels is the exact set of models UnifyAPI sells, as served by
// GET /api/pricing on 2026-08-28. It is spelled out rather than derived from
// the catalog so that adding or dropping a model is a deliberate, reviewed edit
// to this list -- a test that reads the catalog to check the catalog would pass
// no matter what changed.
var publishedModels = []string{
	"MiniMax-M2.5",
	"MiniMax-M2.7",
	"claude-fable-5",
	"claude-haiku-4-5-20251001",
	"claude-opus-4-5",
	"claude-opus-4-6",
	"claude-opus-4-7",
	"claude-opus-4-8",
	"claude-opus-5",
	"claude-sonnet-4-5",
	"claude-sonnet-4-6",
	"claude-sonnet-5",
	"deepseek-v3",
	"deepseek-v3.2",
	"deepseek-v3.2-thinking",
	"deepseek-v4-flash",
	"deepseek-v4-pro",
	"gemini-2.5-flash",
	"gemini-2.5-flash-image",
	"gemini-2.5-flash-lite",
	"gemini-2.5-pro",
	"gemini-3-flash-preview",
	"gemini-3-pro-image",
	"gemini-3.1-flash-image",
	"gemini-3.1-flash-lite-preview",
	"gemini-3.1-pro-preview",
	"gemini-3.1-pro-preview-customtools",
	"gemini-flash-latest",
	"gemini-flash-lite-latest",
	"gemini-pro-latest",
	"glm-4.7",
	"glm-5",
	"glm-5-turbo",
	"glm-5.1",
	"glm-5.2",
	"glm-5.3",
	"gpt-4.1-mini",
	"gpt-4o",
	"gpt-4o-mini",
	"gpt-5",
	"gpt-5-mini",
	"gpt-5.4",
	"gpt-image-2",
	"kimi-k2.5",
	"kimi-k2.6",
	"kimi-k3",
	"nano-banana-pro-preview",
	"qwen3.5-27b",
	"qwen3.5-35b-a3b",
	"qwen3.5-397b-a17b",
	"qwen3.5-flash",
	"qwen3.5-plus",
	"qwen3.6-max-preview",
	"qwen3.6-plus",
	"qwen3.7-max",
	"qwen3.7-plus",
	"qwen3.8-max",
}

// resetRatioMapsToBaseline puts the package's global ratio maps back into the
// state a fresh process starts in. The maps are package-level, so a test that
// tampers with them would otherwise leak into every test that runs after it.
func resetRatioMapsToBaseline(t *testing.T) {
	t.Helper()
	modelRatioMap.Clear()
	completionRatioMap.Clear()
	cacheRatioMap.Clear()
	createCacheRatioMap.Clear()
	modelPriceMap.Clear()
	imageRatioMap.Clear()
	audioRatioMap.Clear()
	audioCompletionRatioMap.Clear()
	InitRatioSettings()
}

func TestCatalogIsStructurallyValid(t *testing.T) {
	for _, problem := range ValidateCatalog() {
		t.Errorf("catalog: %v", problem)
	}
}

func TestCatalogHoldsExactlyThePublishedModels(t *testing.T) {
	require.ElementsMatch(t, publishedModels, CatalogModels(),
		"the catalog must hold exactly the models UnifyAPI publishes. Adding a model means "+
			"adding it to publishedModels in this test too, on purpose.")
}

// TestBaselineRatiosDeriveFromOfficialPrices pins the arithmetic that turns a
// vendor list price into a billing ratio. The constants here are the vendors'
// published prices, so a change to the derivation shows up as a test failure
// rather than as a change in what customers are charged.
func TestBaselineRatiosDeriveFromOfficialPrices(t *testing.T) {
	cases := []struct {
		model                                               string
		inputUSD, outputUSD, cacheReadUSD, cacheWriteUSD    float64
		wantModel, wantCompletion, wantCacheRead, wantWrite float64
	}{
		// Anthropic publishes $5/$25 per 1M with a 0.1x cached read and a
		// 1.25x cache write for every Opus tier.
		{"claude-opus-4-8", 5, 25, 0.5, 6.25, 2.5, 5, 0.1, 1.25},
		{"claude-opus-5", 5, 25, 0.5, 6.25, 2.5, 5, 0.1, 1.25},
		{"claude-sonnet-5", 2, 10, 0.2, 2.5, 1, 5, 0.1, 1.25},
		{"claude-fable-5", 10, 50, 1, 12.5, 5, 5, 0.1, 1.25},
		// OpenAI: gpt-4o at $2.50/$10, cached reads at half input.
		{"gpt-4o", 2.5, 10, 1.25, 0, 1.25, 4, 0.5, 0},
		{"gpt-5", 1.25, 10, 0.125, 0, 0.625, 8, 0.1, 0},
		{"gpt-5-mini", 0.25, 2, 0.025, 0, 0.125, 8, 0.1, 0},
		{"gpt-5.4", 2.5, 15, 0.25, 0, 1.25, 6, 0.1, 0},
		// Google publishes no cache-write price, so there must be no entry.
		{"gemini-2.5-pro", 1.25, 10, 0.125, 0, 0.625, 8, 0.1, 0},
		{"gemini-3-pro-image", 2, 120, 0, 0, 1, 60, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			entry, ok := CatalogEntryFor(tc.model)
			require.True(t, ok, "%s is missing from the catalog", tc.model)

			require.Equal(t, tc.inputUSD, entry.InputUSD, "official input price")
			require.Equal(t, tc.outputUSD, entry.OutputUSD, "official output price")
			require.Equal(t, tc.cacheReadUSD, entry.CacheReadUSD, "official cached-read price")
			require.Equal(t, tc.cacheWriteUSD, entry.CacheWriteUSD, "official cache-write price")

			require.InDelta(t, tc.wantModel, entry.ModelRatio(), 1e-9, "model ratio")
			require.InDelta(t, tc.wantCompletion, entry.CompletionRatio(), 1e-9, "completion ratio")

			cacheRead, hasCacheRead := entry.CacheReadRatio()
			require.Equal(t, tc.wantCacheRead != 0, hasCacheRead,
				"a vendor that publishes no cached-read price must get no entry, not a zero one")
			if hasCacheRead {
				require.InDelta(t, tc.wantCacheRead, cacheRead, 1e-9, "cache read ratio")
			}

			cacheWrite, hasCacheWrite := entry.CacheWriteRatio()
			require.Equal(t, tc.wantWrite != 0, hasCacheWrite,
				"a vendor that publishes no cache-write price must get no entry, not a zero one")
			if hasCacheWrite {
				require.InDelta(t, tc.wantWrite, cacheWrite, 1e-9, "cache write ratio")
			}
		})
	}
}

// TestModelRatioRoundTripsToTheOfficialPrice is the property that makes the
// catalog readable: doubling a model ratio must give back the vendor's dollar
// price per 1M input tokens. If this fails, the USD convention moved and every
// price in the catalog now means something different.
func TestModelRatioRoundTripsToTheOfficialPrice(t *testing.T) {
	for _, entry := range Catalog() {
		require.InDelta(t, entry.InputUSD, entry.ModelRatio()*usdPerMillionPerRatioUnit, 1e-9,
			"%s: model ratio must round-trip to the official input price", entry.Model)
		require.InDelta(t, entry.OutputUSD, entry.ModelRatio()*entry.CompletionRatio()*usdPerMillionPerRatioUnit, 1e-9,
			"%s: ratio x completion must round-trip to the official output price", entry.Model)
	}
}

// TestHardcodedCompletionRatioNeverOverridesTheCatalog guards the sharpest
// trap in this package. GetCompletionRatio consults
// getHardcodedCompletionModelRatio FIRST, and when that returns locked=true the
// hardcoded number wins over the catalog and over anything an admin configures.
// So for every catalogued model that upstream locks, the locked value has to
// equal the ratio the official price implies -- otherwise the catalog is a lie
// about what gets billed.
func TestHardcodedCompletionRatioNeverOverridesTheCatalog(t *testing.T) {
	resetRatioMapsToBaseline(t)

	for _, entry := range Catalog() {
		hardcoded, locked := getHardcodedCompletionModelRatio(entry.Model)
		if !locked {
			continue
		}
		require.InDelta(t, entry.CompletionRatio(), hardcoded, 1e-9,
			"%s: upstream locks the completion ratio at %g, which overrides the catalog's %g "+
				"(official %g/%g per 1M). Either the catalog price is wrong or the lock has to be "+
				"relaxed in getHardcodedCompletionModelRatio -- the two cannot disagree.",
			entry.Model, hardcoded, entry.CompletionRatio(), entry.InputUSD, entry.OutputUSD)
	}
}

// TestGetCompletionRatioReturnsTheCatalogValue checks the whole lookup path,
// not just the lock: whatever the precedence rules do, the number that comes
// out of GetCompletionRatio has to be the official one.
func TestGetCompletionRatioReturnsTheCatalogValue(t *testing.T) {
	resetRatioMapsToBaseline(t)

	for _, entry := range Catalog() {
		require.InDelta(t, entry.CompletionRatio(), GetCompletionRatio(entry.Model), 1e-9,
			"%s: GetCompletionRatio disagrees with the catalog", entry.Model)
	}
}

func TestInitRatioSettingsSeedsExactlyTheCatalog(t *testing.T) {
	resetRatioMapsToBaseline(t)

	live := modelRatioMap.ReadAll()
	require.Len(t, live, len(publishedModels),
		"modelRatioMap is the sellable-model allow-list; it must hold the catalog and nothing else")

	for _, entry := range Catalog() {
		ratio, ok := live[entry.Model]
		require.True(t, ok, "%s is catalogued but was not seeded", entry.Model)
		require.InDelta(t, entry.ModelRatio(), ratio, 1e-9, "%s seeded at the wrong ratio", entry.Model)
	}
}

// TestInitRatioSettingsSeedsEveryRatioMapNotJustModelRatio guards a silent
// mispricing. Each of these getters falls back to 1 when its map has no entry,
// and 1 is a plausible-looking ratio, not an error: an unseeded cache map bills
// cached reads at FULL input price (Anthropic charges a tenth), and an unseeded
// completion map bills output at input price (Anthropic charges 5x). Nothing
// would log, and the numbers would look ordinary.
//
// An earlier version of this test only checked modelRatioMap, which is exactly
// the gap that let a billing-path test pass with completion and cache ratios
// silently stuck at 1.
func TestInitRatioSettingsSeedsEveryRatioMapNotJustModelRatio(t *testing.T) {
	resetRatioMapsToBaseline(t)

	for _, entry := range Catalog() {
		gotCompletion := GetCompletionRatio(entry.Model)
		require.InDelta(t, entry.CompletionRatio(), gotCompletion, 1e-9,
			"%s: output multiplier must come from the catalog, not the fallback of 1", entry.Model)

		if want, published := entry.CacheReadRatio(); published {
			got, found := GetCacheRatio(entry.Model)
			require.True(t, found,
				"%s: vendor publishes a cached-read price but the map has no entry, so reads bill at full price",
				entry.Model)
			require.InDelta(t, want, got, 1e-9, "%s cached-read multiplier", entry.Model)
		}

		if want, published := entry.CacheWriteRatio(); published {
			got, found := GetCreateCacheRatio(entry.Model)
			require.True(t, found,
				"%s: vendor publishes a cache-write price but the map has no entry", entry.Model)
			require.InDelta(t, want, got, 1e-9, "%s cache-write multiplier", entry.Model)
		}
	}
}

// TestUncataloguedModelIsRefused is the reason the catalog can double as an
// allow-list: a model with no ratio makes GetModelRatio report failure, and the
// relay turns that into a refusal rather than a guess.
func TestUncataloguedModelIsRefused(t *testing.T) {
	resetRatioMapsToBaseline(t)

	selfUse := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = selfUse })

	for _, name := range []string{"gpt-4-32k", "ERNIE-4.0-8K", "claude-3-opus-20240229", "totally-made-up-model"} {
		_, ok, _ := GetModelRatio(name)
		require.False(t, ok, "%s is not in the catalog and must not be sellable", name)
	}
}

// TestResetBaselineDropsEverythingUncatalogued reproduces the admin reset path
// end to end. This used to be a site-wide outage: reset wrote 237 legacy models
// over the live map, and because LoadFromJsonString REPLACES the map, every
// model actually being sold lost its ratio and started refusing traffic.
func TestResetBaselineDropsEverythingUncatalogued(t *testing.T) {
	resetRatioMapsToBaseline(t)
	t.Cleanup(func() { resetRatioMapsToBaseline(t) })

	// Stand in for a database row that carries junk plus a tampered price.
	modelRatioMap.Set("some-retired-model", 37.5)
	modelRatioMap.Set("claude-opus-4-8", 0.2125) // the production underprice
	require.Contains(t, modelRatioMap.ReadAll(), "some-retired-model")

	require.NoError(t, UpdateModelRatioByJSONString(DefaultModelRatio2JSONString()))

	live := modelRatioMap.ReadAll()
	require.NotContains(t, live, "some-retired-model", "reset must drop uncatalogued models")
	require.Len(t, live, len(publishedModels), "reset must land exactly the catalog")
	require.InDelta(t, 2.5, live["claude-opus-4-8"], 1e-9,
		"reset must restore the official Anthropic price, not the drifted one")
}

func TestDetectBaselineShadowIsQuietOnACleanBaseline(t *testing.T) {
	resetRatioMapsToBaseline(t)
	require.Empty(t, DetectBaselineShadow(),
		"a freshly seeded process bills exactly the catalog, so nothing should be reported")
}

// TestDetectBaselineShadowCatchesEachWayTheDatabaseCanWin covers the three
// shapes of drift a database options row can produce, since the loader replaces
// the whole map: a changed price, a model that vanished, and a model that
// appeared from nowhere.
func TestDetectBaselineShadowCatchesEachWayTheDatabaseCanWin(t *testing.T) {
	resetRatioMapsToBaseline(t)
	t.Cleanup(func() { resetRatioMapsToBaseline(t) })

	modelRatioMap.Set("claude-opus-4-8", 0.2125)
	modelRatioMap.Set("smuggled-in-model", 1)
	completionRatioMap.Clear()

	shadows := DetectBaselineShadow()

	byModel := map[string]BaselineShadow{}
	for _, shadow := range shadows {
		byModel[shadow.Option+"/"+shadow.Model] = shadow
	}

	tampered, ok := byModel["ModelRatio/claude-opus-4-8"]
	require.True(t, ok, "a changed price must be reported")
	require.InDelta(t, 2.5, tampered.Baseline, 1e-9)
	require.InDelta(t, 0.2125, tampered.Live, 1e-9)

	_, ok = byModel["ModelRatio/smuggled-in-model"]
	require.True(t, ok, "a model billing outside the catalog must be reported")

	_, ok = byModel["CompletionRatio/claude-opus-4-8"]
	require.True(t, ok, "a catalogued model missing from a live map must be reported")
}

// TestUnverifiedEntriesAreDeclared keeps the five models with no models.dev
// listing from quietly becoming forty. Each one is a price nothing can check,
// so the list is pinned here and the drift report prints it every run.
func TestUnverifiedEntriesAreDeclared(t *testing.T) {
	want := []string{
		"deepseek-v3",            // retired; DeepSeek publishes v4 only
		"deepseek-v3.2",          //
		"deepseek-v3.2-thinking", //
		"glm-5-turbo",            // not listed; Zhipu lists glm-5v-turbo
		"qwen3.5-flash",          // not listed
	}

	var got []string
	for _, entry := range Catalog() {
		if entry.Unverified {
			got = append(got, entry.Model)
		}
	}
	require.ElementsMatch(t, want, got,
		"an unverified price is one no automated check can defend. Adding to this list needs a reason.")
}
