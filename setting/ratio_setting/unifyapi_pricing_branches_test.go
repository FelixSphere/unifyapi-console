package ratio_setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCompiledCatalog swaps the compiled catalogue for the duration of one
// test. It is the only way to reach the structural checks in ValidateCatalog:
// the extras save path rejects most malformed shapes before they can be
// assembled, so a bad row can now only originate from a code edit -- which is
// exactly the case these tests stand in for.
func withCompiledCatalog(t *testing.T, entries []CatalogEntry) {
	t.Helper()
	previous := unifyapiCatalog
	t.Cleanup(func() {
		unifyapiCatalog = previous
		InitRatioSettings()
	})
	unifyapiCatalog = entries
}

// TestValidateCatalogRejectsEveryMalformedRow. Each row below is a shape that
// would price something wrongly and keep serving traffic. Together they are
// the reason gen-pricing-seed refuses to emit a seed it cannot vouch for.
func TestValidateCatalogRejectsEveryMalformedRow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry CatalogEntry
		want  string
	}{
		{
			name:  "no model name",
			entry: CatalogEntry{Vendor: "google", InputUSD: 1, OutputUSD: 2},
			want:  "empty model name",
		},
		{
			name:  "free input",
			entry: CatalogEntry{Model: "probe", Vendor: "google", InputUSD: 0, OutputUSD: 2},
			want:  "input price must be positive",
		},
		{
			name:  "free output",
			entry: CatalogEntry{Model: "probe", Vendor: "google", InputUSD: 1, OutputUSD: 0},
			want:  "output price must be positive",
		},
		{
			name:  "negative cache price",
			entry: CatalogEntry{Model: "probe", Vendor: "google", InputUSD: 1, OutputUSD: 2, CacheWriteUSD: -1},
			want:  "cache prices must not be negative",
		},
		{
			name:  "no vendor and not flagged",
			entry: CatalogEntry{Model: "probe", InputUSD: 1, OutputUSD: 2},
			want:  "nothing can check its price",
		},
		{
			name:  "unverified but names a vendor",
			entry: CatalogEntry{Model: "probe", Vendor: "google", InputUSD: 1, OutputUSD: 2, Unverified: true},
			want:  "marked unverified but names vendor",
		},
		{
			name:  "upstream id without a vendor",
			entry: CatalogEntry{Model: "probe", InputUSD: 1, OutputUSD: 2, Unverified: true, UpstreamModel: "probe-v1"},
			want:  "without a vendor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCompiledCatalog(t, []CatalogEntry{tc.entry})
			problems := ValidateCatalog()
			require.NotEmpty(t, problems)
			assert.Contains(t, fmt.Sprint(problems), tc.want)
		})
	}
}

// TestValidateCatalogRejectsDuplicates. Two rows for one model means the
// second silently wins in some maps and the first in others, which is how a
// model ends up published at one price and billed at another.
func TestValidateCatalogRejectsDuplicates(t *testing.T) {
	withCompiledCatalog(t, []CatalogEntry{
		{Model: "probe", Vendor: "google", InputUSD: 1, OutputUSD: 2},
		{Model: "probe", Vendor: "google", InputUSD: 9, OutputUSD: 18},
	})

	problems := ValidateCatalog()
	require.NotEmpty(t, problems)
	assert.Contains(t, fmt.Sprint(problems), "duplicate catalog entry")
}

// TestBaselineMapsAreBuiltFromTheCatalogueRows exercises the real builders
// rather than a stand-in, using a catalogue that actually sets the modality
// fields. Nothing we sell sets them today, so this is the only place the
// bodies run at all -- and the place a future audio-priced model gets its
// first assertion.
func TestBaselineMapsAreBuiltFromTheCatalogueRows(t *testing.T) {
	withCompiledCatalog(t, []CatalogEntry{
		{Model: "per-call", Vendor: "openai", InputUSD: 1, OutputUSD: 2, PerCallUSD: 0.04},
		{Model: "with-image", Vendor: "openai", InputUSD: 1, OutputUSD: 2, ImageRatio: 3},
		{Model: "with-audio", Vendor: "openai", InputUSD: 1, OutputUSD: 2, AudioRatio: 7, AudioCompletionRatio: 11},
		{Model: "plain", Vendor: "openai", InputUSD: 1, OutputUSD: 2},
	})

	assert.Equal(t, map[string]float64{"per-call": 0.04}, baselineModelPrice())
	assert.Equal(t, map[string]float64{"with-image": 3}, baselineImageRatio())
	assert.Equal(t, map[string]float64{"with-audio": 7}, baselineAudioRatio())
	assert.Equal(t, map[string]float64{"with-audio": 11}, baselineAudioCompletionRatio())

	// A plain row contributes to none of them: an omitted modality price means
	// "same rate as text", not "free".
	for _, m := range []map[string]float64{
		baselineModelPrice(), baselineImageRatio(), baselineAudioRatio(), baselineAudioCompletionRatio(),
	} {
		assert.NotContains(t, m, "plain")
	}
}

// TestModalityRatiosReportAbsenceRatherThanZero. Every one of these getters
// returns (value, found). A caller that ignores the flag would bill an
// unconfigured modality at the default; a getter that returned 0 instead of 1
// would bill it at nothing. Both have happened in this codebase's history,
// which is why the default is pinned rather than assumed.
func TestModalityRatiosReportAbsenceRatherThanZero(t *testing.T) {
	InitRatioSettings()

	ratio, found := GetImageRatio("model-with-no-configured-modality-price")
	assert.False(t, found, "ImageRatio must report the miss")
	assert.InDelta(t, 1, ratio, 1e-9,
		"an unconfigured image price bills at the text rate, never 0 -- 0 bills images as free")

	// Cache WRITES are the exception: the default is 1.25, not 1, because that
	// is what Anthropic charges to write a prompt into the cache. A neutral 1
	// here would undercharge every cache-write by 20%.
	writeRatio, found := GetCreateCacheRatio("model-with-no-configured-modality-price")
	assert.False(t, found, "CreateCacheRatio must report the miss")
	assert.InDelta(t, 1.25, writeRatio, 1e-9,
		"the cache-write default is a vendor rate, not a neutral 1")

	// The audio getters have no found flag at all -- they collapse the miss
	// into the default. Pinned separately so the asymmetry is on record: a
	// caller cannot distinguish "configured as 1" from "not configured".
	assert.InDelta(t, 1, GetAudioRatio("model-with-no-configured-modality-price"), 1e-9)
	assert.InDelta(t, 1, GetAudioCompletionRatio("model-with-no-configured-modality-price"), 1e-9)
}

// TestModalityRatiosReturnAConfiguredValue proves the found path too, so the
// test above cannot pass just because nothing is ever configured.
func TestModalityRatiosReturnAConfiguredValue(t *testing.T) {
	InitRatioSettings()
	prevImage, prevAudio, prevAudioC := ImageRatio2JSONString(), AudioRatio2JSONString(), AudioCompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateImageRatioByJSONString(prevImage))
		require.NoError(t, UpdateAudioRatioByJSONString(prevAudio))
		require.NoError(t, UpdateAudioCompletionRatioByJSONString(prevAudioC))
		InitRatioSettings()
	})

	require.NoError(t, UpdateImageRatioByJSONString(`{"probe":3}`))
	require.NoError(t, UpdateAudioRatioByJSONString(`{"probe":7}`))
	require.NoError(t, UpdateAudioCompletionRatioByJSONString(`{"probe":11}`))

	ratio, found := GetImageRatio("probe")
	assert.True(t, found)
	assert.InDelta(t, 3, ratio, 1e-9)

	assert.InDelta(t, 7, GetAudioRatio("probe"), 1e-9)
	assert.InDelta(t, 11, GetAudioCompletionRatio("probe"), 1e-9)
}

// TestHardcodedCompletionRatioLegacyFamilies covers the branches for models we
// do not sell. They are reachable -- any name a channel accepts lands here --
// so a channel listing one of these gets the ratio below, whatever the
// catalogue says. Pinned so that stays a known quantity rather than a
// discovery during an incident.
func TestHardcodedCompletionRatioLegacyFamilies(t *testing.T) {
	for _, tc := range []struct {
		model  string
		ratio  float64
		locked bool
	}{
		{"gemini-2.0-flash", 4, true},
		{"gemini-2.5-flash-preview-04-17", 3.5 / 0.15, false},
		{"gemini-2.5-flash-preview-04-17-nothinking", 4, false},
		{"gemini-robotics-er-1.5-preview", 2.5 / 0.3, false},
		{"gemini-unknown-model", 4, false},
		{"command-r-08-2024", 4, true},
		{"command-r-plus-08-2024", 4, true},
		{"command-light", 4, false},
		{"ERNIE-Lite-8K", 2, true},
		{"ERNIE-Character-8K", 2, true},
		{"ERNIE-Functions-8K", 2, true},
		{"llama2-70b-4096", 0.8 / 0.64, true},
		{"llama3-8b-8192", 2, true},
		{"llama3-70b-8192", 0.79 / 0.59, true},

		// The catch-all. A model nobody has a rule for bills output at the
		// same rate as input unless configuration says otherwise, and reports
		// unlocked so configuration can.
		{"some-model-nobody-has-heard-of", 1, false},
	} {
		ratio, locked := getHardcodedCompletionModelRatio(tc.model)
		assert.InDelta(t, tc.ratio, ratio, 1e-9, "ratio for %s", tc.model)
		assert.Equal(t, tc.locked, locked, "locked flag for %s", tc.model)
	}
}

// TestUnknownModelIsNotSellableByDefault. The 37.5 sentinel is returned with
// exists=false; self-use mode is the one configuration that turns it into a
// real price. Every caller must check the flag, and this pins both halves.
func TestUnknownModelIsNotSellableByDefault(t *testing.T) {
	InitRatioSettings()

	value, usePrice, exists := GetModelRatioOrPrice("no-such-model-anywhere")
	assert.False(t, exists, "an unknown model must not be billable")
	assert.False(t, usePrice)
	assert.InDelta(t, 37.5, value, 1e-9)
}

// TestGroupModelDiscountMissesAreReportedAsAbsent. A per-customer contract
// price that reports 0 instead of absent would bill that customer nothing.
func TestGroupModelDiscountMissesAreReportedAsAbsent(t *testing.T) {
	InitRatioSettings()
	previous := GroupModelDiscount2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupModelDiscountByJSONString(previous))
		InitRatioSettings()
	})

	require.NoError(t, UpdateGroupModelDiscountByJSONString(`{"cust-acme":{"gpt-4o":0.9}}`))

	ratio, ok := GetGroupModelDiscount("cust-acme", "gpt-4o")
	assert.True(t, ok)
	assert.InDelta(t, 0.9, ratio, 1e-9)

	_, ok = GetGroupModelDiscount("cust-acme", "claude-opus-4-5")
	assert.False(t, ok, "a group with a contract on one model has none on the others")

	_, ok = GetGroupModelDiscount("group-with-no-contract", "gpt-4o")
	assert.False(t, ok)
}

// TestGroupModelDiscountRejectsMalformedInput. This table sets what a named
// customer pays. A save that half-applies is worse than one that fails.
func TestGroupModelDiscountRejectsMalformedInput(t *testing.T) {
	InitRatioSettings()
	previous := GroupModelDiscount2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupModelDiscountByJSONString(previous))
		InitRatioSettings()
	})

	assert.Error(t, UpdateGroupModelDiscountByJSONString(`not json`))
	assert.Error(t, UpdateGroupModelDiscountByJSONString(`{"cust":{"gpt-4o":0}}`),
		"a zero discount bills the customer nothing")
	assert.Error(t, UpdateGroupModelDiscountByJSONString(`{"cust":{"gpt-4o":-1}}`))

	// The table is still whatever it was before the rejected saves.
	assert.JSONEq(t, previous, GroupModelDiscount2JSONString(),
		"a rejected save must not partially apply")
}
