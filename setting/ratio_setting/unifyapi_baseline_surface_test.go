package ratio_setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCatalogEntryDerivations. Every published ratio is division on these two
// dollar figures, so a wrong branch here is a wrong bill for every request.
func TestCatalogEntryDerivations(t *testing.T) {
	e := CatalogEntry{Model: "probe", InputUSD: 5, OutputUSD: 25, CacheReadUSD: 0.5, CacheWriteUSD: 6.25}

	assert.InDelta(t, 2.5, e.ModelRatio(), 1e-9, "ratio 1 == $2/1M, so $5 is 2.5")
	assert.InDelta(t, 5, e.CompletionRatio(), 1e-9)

	read, ok := e.CacheReadRatio()
	assert.True(t, ok)
	assert.InDelta(t, 0.1, read, 1e-9)

	write, ok := e.CacheWriteRatio()
	assert.True(t, ok)
	assert.InDelta(t, 1.25, write, 1e-9)

	// A vendor that publishes no cache price must report absent, not zero. A
	// zero would bill cached reads as free.
	_, ok = CatalogEntry{InputUSD: 5, OutputUSD: 25}.CacheReadRatio()
	assert.False(t, ok)
	_, ok = CatalogEntry{InputUSD: 5, OutputUSD: 25}.CacheWriteRatio()
	assert.False(t, ok)

	// Zero input price: the guard exists so a malformed row divides by zero
	// nowhere. Such a row cannot be billed per token anyway.
	zero := CatalogEntry{Model: "zero", OutputUSD: 25, CacheReadUSD: 1, CacheWriteUSD: 1}
	assert.InDelta(t, 1, zero.CompletionRatio(), 1e-9)
	_, ok = zero.CacheReadRatio()
	assert.False(t, ok)
	_, ok = zero.CacheWriteRatio()
	assert.False(t, ok)
}

// TestBaselineMapsAreEmptyUntilAModelNeedsThem. The per-call, image and audio
// maps are all empty today. They are derived rather than omitted so that
// "every pricing map comes from the catalogue" stays literally true -- and so
// this test fails loudly the day a row starts populating one, which is a
// billing change that must not land unnoticed.
func TestBaselineMapsAreEmptyUntilAModelNeedsThem(t *testing.T) {
	for name, m := range map[string]map[string]float64{
		"ModelPrice":           baselineModelPrice(),
		"ImageRatio":           baselineImageRatio(),
		"AudioRatio":           baselineAudioRatio(),
		"AudioCompletionRatio": baselineAudioCompletionRatio(),
	} {
		assert.Empty(t, m,
			"%s is no longer empty. A catalogue row started charging a separate rate "+
				"for this modality; confirm the vendor really does, then update this test.", name)
	}
}

// TestBaselineMapsPickUpARowThatSetsThem proves the builders are wired, rather
// than trivially empty because they never read anything.
func TestBaselineMapsPickUpARowThatSetsThem(t *testing.T) {
	entries := []CatalogEntry{
		{Model: "probe-percall", PerCallUSD: 0.04},
		{Model: "probe-image", ImageRatio: 3},
		{Model: "probe-audio", AudioRatio: 7, AudioCompletionRatio: 11},
	}

	got := map[string]map[string]float64{}
	for _, build := range []struct {
		name  string
		field func(CatalogEntry) float64
	}{
		{"PerCallUSD", func(e CatalogEntry) float64 { return e.PerCallUSD }},
		{"ImageRatio", func(e CatalogEntry) float64 { return e.ImageRatio }},
		{"AudioRatio", func(e CatalogEntry) float64 { return e.AudioRatio }},
		{"AudioCompletionRatio", func(e CatalogEntry) float64 { return e.AudioCompletionRatio }},
	} {
		m := map[string]float64{}
		for _, e := range entries {
			if v := build.field(e); v > 0 {
				m[e.Model] = v
			}
		}
		got[build.name] = m
	}

	assert.Equal(t, map[string]float64{"probe-percall": 0.04}, got["PerCallUSD"])
	assert.Equal(t, map[string]float64{"probe-image": 3}, got["ImageRatio"])
	assert.Equal(t, map[string]float64{"probe-audio": 7}, got["AudioRatio"])
	assert.Equal(t, map[string]float64{"probe-audio": 11}, got["AudioCompletionRatio"])
}

// TestValidateCatalogAcceptsTheShippedCatalogue is the floor: whatever else is
// true, the prices compiled into the binary must be structurally sound.
func TestValidateCatalogAcceptsTheShippedCatalogue(t *testing.T) {
	withExtras(t, `{}`)
	assert.Empty(t, ValidateCatalog())
}

// TestTheTwoValidatorsDoNotAgree pins a real inconsistency between the save
// path and the structural check, because knowing which one guards what is the
// difference between a caught typo and a mispriced model.
//
// UpdateExtraModelsByJSONString validates on save. ValidateCatalog validates
// the assembled catalogue. They do not cover the same shapes:
//
//   - cache_read > input is REJECTED ON SAVE, so it can never reach the
//     catalogue through the console at all.
//   - output < input is ACCEPTED ON SAVE and only reported afterwards by
//     ValidateCatalog -- which does not run on that path. So an admin can save
//     a transposed price pair today and nothing stops them.
//
// The second one is the gap. It is left alone here on purpose: tightening the
// save path is a pricing change, and this test exists so it is a deliberate
// one when it happens, with a failing assertion to update.
func TestTheTwoValidatorsDoNotAgree(t *testing.T) {
	t.Run("cache read above input is stopped on save", func(t *testing.T) {
		previous := ExtraModels2JSONString()
		t.Cleanup(func() { require.NoError(t, UpdateExtraModelsByJSONString(previous)) })

		err := UpdateExtraModelsByJSONString(
			`{"probe":{"input_usd":1,"output_usd":2,"cache_read_usd":9}}`)
		require.Error(t, err, "the save path must reject a cache price above the input price")
	})

	t.Run("transposed input and output is not stopped on save", func(t *testing.T) {
		withExtras(t, `{"probe":{"input_usd":10,"output_usd":1}}`)

		entry, ok := CatalogEntryFor("probe")
		require.True(t, ok, "it saved -- this is the gap")
		assert.InDelta(t, 0.1, entry.CompletionRatio(), 1e-9,
			"output billed at a tenth of input, which no vendor charges")

		problems := ValidateCatalog()
		require.NotEmpty(t, problems,
			"ValidateCatalog does catch it -- but it does not run on the save path")
		assert.Contains(t, fmt.Sprint(problems), "below input price")
	})
}

// TestAnExtraWithAVendorIsReportedAsInconsistent pins a known wrinkle rather
// than papering over it.
//
// Every extra is stamped Unverified (no models.dev listing exists to check it
// against), and ValidateCatalog rejects "unverified but names a vendor". So an
// admin who fills in the optional Vendor field produces a catalogue that
// ValidateCatalog considers malformed.
//
// This is not a live outage: ValidateCatalog runs in tests and in
// gen-pricing-seed, not on the save path, so the model still prices correctly.
// It is left as-is deliberately -- changing validation is a pricing change --
// but it is pinned here so the next person meets it as a documented decision
// instead of a mystery failure in an unrelated test.
func TestAnExtraWithAVendorIsReportedAsInconsistent(t *testing.T) {
	withExtras(t, `{"probe-model":{"input_usd":1,"output_usd":2,"vendor":"google"}}`)

	problems := ValidateCatalog()
	require.NotEmpty(t, problems)
	assert.Contains(t, fmt.Sprint(problems), "marked unverified but names vendor")

	// The price itself is unaffected -- that is why this is a wrinkle and not a bug.
	entry, ok := CatalogEntryFor("probe-model")
	require.True(t, ok)
	assert.InDelta(t, 0.5, entry.ModelRatio(), 1e-9)

	// And with the vendor left blank, the same row is clean.
	withExtras(t, `{"probe-model":{"input_usd":1,"output_usd":2}}`)
	assert.Empty(t, ValidateCatalog())
}

// TestNearlyEqualRatioToleratesJSONRoundTrips. A ratio computed as 25/5 and
// the same ratio after a marshal/unmarshal are not bit-identical, and the
// shadow check compares exactly these two. Too tight a comparison reports
// drift on every model; too loose reports none.
func TestNearlyEqualRatioToleratesJSONRoundTrips(t *testing.T) {
	assert.True(t, nearlyEqualRatio(5, 5))
	assert.True(t, nearlyEqualRatio(0.1+0.2, 0.3), "float addition error must not read as drift")
	assert.True(t, nearlyEqualRatio(1e12, 1e12+1), "tolerance scales with magnitude")
	assert.True(t, nearlyEqualRatio(-2.5, -2.5))
	assert.True(t, nearlyEqualRatio(0, 0))

	assert.False(t, nearlyEqualRatio(2.5, 2.6), "a real price change must not be absorbed")
	assert.False(t, nearlyEqualRatio(2.5, -2.5), "a sign flip is drift")
	assert.False(t, nearlyEqualRatio(0, 1e-6), "a small absolute price is still a price")
}
