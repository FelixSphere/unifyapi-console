package ratio_setting

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The completion ratio is the output-token multiplier: output price =
// model_ratio x completion_ratio. It is the second half of every bill, and it
// lives in a different place from the input price, which is why it drifts.
//
// getHardcodedCompletionModelRatio returns (ratio, locked). When locked is
// true, GetCompletionRatio returns the hardcoded value and NEVER CONSULTS
// completionRatioMap -- so for those models, editing our catalogue changes the
// published price and does not change the bill. There is no warning, no log
// line, and the admin UI still shows the edit as saved.
//
// Ten models we sell are in that state today.

// lockedCatalogModels is the list, measured rather than assumed: every
// catalogue model for which the upstream hardcoded table wins.
func lockedCatalogModels(t *testing.T) []CatalogEntry {
	t.Helper()
	var locked []CatalogEntry
	for _, e := range unifyapiCatalog {
		if _, isLocked := getHardcodedCompletionModelRatio(e.Model); isLocked {
			locked = append(locked, e)
		}
	}
	return locked
}

// TestLockedModelsStillBillTheCatalogueOutputPrice is the guard that matters.
//
// It does not assert that locking is fine. It asserts that for every model
// where we have lost control of the output multiplier, the value we lost
// control to still equals the one we intended. The day a vendor moves an
// output price on one of these models, this fails -- and it has to, because
// fixing the catalogue alone would not fix the bill.
func TestLockedModelsStillBillTheCatalogueOutputPrice(t *testing.T) {
	InitRatioSettings()
	intended := baselineCompletionRatio()

	locked := lockedCatalogModels(t)
	require.NotEmpty(t, locked,
		"if this is empty the whole test is vacuous -- either the hardcoded table "+
			"stopped matching our model names, or the lock was removed upstream")

	for _, e := range locked {
		want, ok := intended[e.Model]
		if !ok {
			want = 1
		}
		hardcoded, _ := getHardcodedCompletionModelRatio(e.Model)

		assert.InDelta(t, want, hardcoded, 1e-9,
			"%s: the catalogue says output/input = %.4f ($%.4f in, $%.4f out), but the "+
				"hardcoded table locks it at %.4f and wins. Editing the catalogue will "+
				"NOT change what this model bills. Fix getHardcodedCompletionModelRatio "+
				"in model_ratio.go, or the customer is billed the old output price.",
			e.Model, want, e.InputUSD, e.OutputUSD, hardcoded)

		// And prove the lock really does beat a configured override, rather
		// than trusting the flag name.
		assert.InDelta(t, hardcoded, GetCompletionRatio(e.Model), 1e-9)
	}
}

// TestALockedRatioIgnoresTheConfiguredOverride demonstrates the mechanism
// itself, so a reader of the test above does not have to take it on faith.
func TestALockedRatioIgnoresTheConfiguredOverride(t *testing.T) {
	InitRatioSettings()
	previous := CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateCompletionRatioByJSONString(previous))
		InitRatioSettings()
	})

	const lockedModel = "gpt-5"
	const unlockedModel = "gpt-5.5"

	require.NoError(t, UpdateCompletionRatioByJSONString(
		fmt.Sprintf(`{"%s": 999, "%s": 999}`, lockedModel, unlockedModel)))

	assert.NotEqual(t, 999.0, GetCompletionRatio(lockedModel),
		"a locked model must ignore the override -- this is the trap being documented")
	assert.InDelta(t, 999, GetCompletionRatio(unlockedModel), 1e-9,
		"an unlocked model must honour the override, or the admin UI is lying too")

	// The API surface the console reads exposes the difference, which is the
	// only reason an operator could ever notice.
	assert.True(t, GetCompletionRatioInfo(lockedModel).Locked)
	assert.False(t, GetCompletionRatioInfo(unlockedModel).Locked)
	assert.InDelta(t, 999, GetCompletionRatioInfo(unlockedModel).Ratio, 1e-9)
}

// TestHardcodedCompletionRatioFamilies walks the branch tree with the shapes
// that actually reach it. Each row is a family whose output multiplier differs
// from its neighbours by enough to matter on a bill.
func TestHardcodedCompletionRatioFamilies(t *testing.T) {
	for _, tc := range []struct {
		model  string
		ratio  float64
		locked bool
	}{
		// Reserved suffixes bypass the family rules entirely.
		{"anything-all", 2, false},
		{"gpt-4-gizmo-*", 2, false},

		{"gpt-4o", 4, false},
		{"gpt-4o-2024-05-13", 3, true},
		{"gpt-4o-mini-tts", 20, false},
		{"gpt-5", 8, true},
		{"gpt-5.4", 6, true},
		{"gpt-5.4-nano", 6.25, true},
		{"gpt-5.5", 6, false},
		{"gpt-4.5-preview", 2, true},
		{"gpt-4-turbo", 3, true},
		{"gpt-4", 2, false},
		{"o1-preview", 4, true},
		{"o3-mini", 4, true},
		{"chatgpt-4o-latest", 3, true},
		{"claude-3-opus", 5, true},
		{"claude-sonnet-4-5", 5, true},
		// The gpt-3.5 block further down in getHardcodedCompletionModelRatio is
		// UNREACHABLE: "gpt-3.5-turbo" starts with "gpt-", so the generic gpt-
		// arm returns (2, false) long before it. These rows pin what actually
		// happens, not what the dead code says. We do not sell gpt-3.5 -- it is
		// not in the catalogue -- so this is dead code, not a live mispricing,
		// and it is left alone deliberately. If a gpt-3.5 model is ever listed,
		// this test is where the surprise shows up.
		{"gpt-3.5-turbo", 2, false},
		{"gpt-3.5-turbo-1106", 2, false},
		{"gpt-3.5-turbo-instruct", 2, false},
		{"mistral-large", 3, true},
		{"gemini-1.5-pro", 4, true},
		{"gemini-2.5-pro", 8, false},
		{"gemini-2.5-flash-lite", 4, false},
		{"gemini-2.5-flash", 2.5 / 0.3, false},
		{"gemini-3-pro", 6, false},
		{"gemini-3-pro-image", 60, false},
		{"command-r", 3, true},
		{"command-r-plus", 5, true},
		{"ERNIE-Speed-128K", 2, true},
	} {
		ratio, locked := getHardcodedCompletionModelRatio(tc.model)
		assert.InDelta(t, tc.ratio, ratio, 1e-9, "ratio for %s", tc.model)
		assert.Equal(t, tc.locked, locked, "locked flag for %s", tc.model)
	}
}

// TestSlashPrefixedModelsSkipTheHardcodedTable. A model name carrying a
// provider prefix ("openrouter/gpt-4o") is a third-party listing and is priced
// only by configuration. If it fell through to the hardcoded table it would
// pick up OpenAI's multiplier for a model OpenAI does not sell us.
func TestSlashPrefixedModelsSkipTheHardcodedTable(t *testing.T) {
	InitRatioSettings()
	previous := CompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateCompletionRatioByJSONString(previous))
		InitRatioSettings()
	})

	require.NoError(t, UpdateCompletionRatioByJSONString(`{"vendor/gpt-5": 1.5}`))

	assert.InDelta(t, 1.5, GetCompletionRatio("vendor/gpt-5"), 1e-9,
		"the slash form must be configurable even though the bare form is locked")
	assert.False(t, GetCompletionRatioInfo("vendor/gpt-5").Locked)
}

// --- price lookup ---------------------------------------------------------

// TestPerCallPriceLookup. GetModelPrice reports whether a model is billed
// per-call instead of per-token. Returning true for a token model would charge
// a flat fee and ignore usage entirely.
func TestPerCallPriceLookup(t *testing.T) {
	InitRatioSettings()
	previous := ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateModelPriceByJSONString(previous))
		InitRatioSettings()
	})

	require.NoError(t, UpdateModelPriceByJSONString(`{"per-call-model": 0.05}`))

	price, usePrice := GetModelPrice("per-call-model", false)
	assert.True(t, usePrice)
	assert.InDelta(t, 0.05, price, 1e-9)

	price, usePrice = GetModelPrice("gpt-4o", false)
	assert.False(t, usePrice, "a token-billed model must not report a per-call price")
	assert.InDelta(t, -1, price, 1e-9)
}

// TestCompactVariantsFallBackToTheWildcardPrice. A "-openai-compact" name is
// the same model reached through a different protocol shape; without the
// wildcard fallback it would price at the 37.5 sentinel.
func TestCompactVariantsFallBackToTheWildcardPrice(t *testing.T) {
	InitRatioSettings()
	prevPrice, prevRatio := ModelPrice2JSONString(), ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateModelPriceByJSONString(prevPrice))
		require.NoError(t, UpdateModelRatioByJSONString(prevRatio))
		InitRatioSettings()
	})

	// No wildcard configured: the compact name is simply unknown.
	require.NoError(t, UpdateModelPriceByJSONString(`{}`))
	_, usePrice := GetModelPrice("mystery"+CompactModelSuffix, false)
	assert.False(t, usePrice)

	require.NoError(t, UpdateModelPriceByJSONString(
		fmt.Sprintf(`{"%s": 0.02}`, CompactWildcardModelKey)))
	price, usePrice := GetModelPrice("mystery"+CompactModelSuffix, false)
	assert.True(t, usePrice)
	assert.InDelta(t, 0.02, price, 1e-9)

	require.NoError(t, UpdateModelRatioByJSONString(
		fmt.Sprintf(`{"%s": 1.25}`, CompactWildcardModelKey)))
	ratio, ok, _ := GetModelRatio("mystery" + CompactModelSuffix)
	assert.True(t, ok)
	assert.InDelta(t, 1.25, ratio, 1e-9)
}

// TestThinkingBudgetNamesCollapseToOnePricedKey. A caller may append a
// thinking budget to a Gemini model name. Those variants are priced as the
// base model; if the collapse stopped working every one of them would hit the
// 37.5 sentinel, which is 15x the real price of gemini-2.5-flash.
func TestThinkingBudgetNamesCollapseToOnePricedKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"gemini-2.5-flash-thinking-1024", "gemini-2.5-flash-thinking-*"},
		{"gemini-2.5-flash-lite-thinking-512", "gemini-2.5-flash-lite-thinking-*"},
		{"gemini-2.5-pro-thinking-8192", "gemini-2.5-pro-thinking-*"},
		{"gpt-4-gizmo-g-abc123", "gpt-4-gizmo-*"},
		{"gpt-4o-gizmo-g-abc123", "gpt-4o-gizmo-*"},

		// Left alone: no budget suffix, and an unrelated family.
		{"gemini-2.5-flash", "gemini-2.5-flash"},
		{"gemini-2.5-pro-preview", "gemini-2.5-pro-preview"},
		{"claude-opus-4-5", "claude-opus-4-5"},
	} {
		assert.Equal(t, tc.want, FormatMatchingModelName(tc.in), "formatting %s", tc.in)
	}
}

// TestResetWritesTheCatalogueNotUpstreamDefaults. The admin "reset ratios"
// button calls this. Upstream's default map is 237 legacy models; because the
// loader replaces rather than merges, writing it would strip the ratio from
// every model we actually sell.
func TestResetWritesTheCatalogueNotUpstreamDefaults(t *testing.T) {
	InitRatioSettings()

	defaults := GetDefaultModelRatioMap()
	require.NotEmpty(t, defaults)

	for _, e := range unifyapiCatalog {
		if e.AdminAdded {
			continue
		}
		_, ok := defaults[e.Model]
		assert.True(t, ok,
			"%s is in the catalogue but not in the reset payload -- pressing reset "+
				"would leave it unpriced and it would start refusing requests", e.Model)
	}

	assert.JSONEq(t, DefaultModelRatio2JSONString(), mustJSON(t, defaults))
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := common.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestGroupRatioSettingLazilyRestoresSpecialGroups. The registered config
// object can come back from a partial load with a nil special-group map; the
// accessor repairs it. A nil map here is a panic on the routing path.
func TestGroupRatioSettingLazilyRestoresSpecialGroups(t *testing.T) {
	InitRatioSettings()

	groupRatioSetting.GroupSpecialUsableGroup = nil
	setting := GetGroupRatioSetting()

	require.NotNil(t, setting)
	require.NotNil(t, setting.GroupSpecialUsableGroup)
	assert.Same(t, setting, GetGroupRatioSetting(), "repeated reads return the same object")
}
