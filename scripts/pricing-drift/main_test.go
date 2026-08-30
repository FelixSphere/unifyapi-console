package main

// UNIFYAPI-FORK: offline tests for the drift checker.
//
// The live check needs the network, so CI cannot depend on it for a
// pass/fail signal -- a models.dev outage would turn every unrelated PR red.
// These tests instead drive Check() against a committed fixture, which gives
// two guarantees the live run cannot:
//
//   1. the catalog matches the prices that were verified on the snapshot date,
//      so a careless edit to a price is caught in the PR that makes it; and
//   2. the checker itself detects each kind of drift, verified by mutating the
//      fixture rather than by waiting for a vendor to reprice.
//
// The live check runs on a schedule instead -- see .github/workflows/pricing-drift.yml.

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

const fixturePath = "testdata/models-dev-2026-08-30.json"

func loadFixture(t *testing.T) map[string]modelsDevProvider {
	t.Helper()
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)

	var feed map[string]modelsDevProvider
	require.NoError(t, json.Unmarshal(raw, &feed))
	return feed
}

// feedStaleModels are the entries whose price deliberately disagrees with
// models.dev because we hold a newer quote read off the vendor's own page.
//
// Pinned as an explicit list rather than blanket-allowed: overriding the feed is
// how a typo becomes permanent, so adding one has to be a deliberate edit here
// and not a side effect of editing the catalog.
var feedStaleModels = []string{"deepseek-v4-flash", "deepseek-v4-pro"}

// TestCatalogMatchesTheVerifiedSnapshot is the regression test that gives the
// baseline its meaning: every official price in the catalog is exactly what the
// vendor published on PricingSnapshotDate. Change a price without changing the
// fixture and this fails.
func TestCatalogMatchesTheVerifiedSnapshot(t *testing.T) {
	findings := Check(loadFixture(t))

	var stale []string
	for _, finding := range findings {
		switch finding.Kind {
		case "unverifiable":
		case "feed-stale":
			stale = append(stale, finding.Model)
		default:
			t.Errorf("%s [%s/%s]: %s", finding.Kind, finding.Model, finding.Field, finding.Detail)
		}
	}

	require.ElementsMatch(t, feedStaleModels, stale,
		"a model overriding models.dev must be listed in feedStaleModels. Adding one means "+
			"somebody decided the feed is behind the vendor; removing one means the feed caught "+
			"up and QuoteSource/QuoteDate should be cleared.")
	require.False(t, hasDrift(findings),
		"the catalog must match the fixture it was verified against, except where a dated "+
			"vendor quote deliberately overrides it")
}

// TestAFeedStaleEntryCarriesItsProvenance. Overriding the aggregator is only
// defensible if the override says where the number came from and when. Without
// both, it is indistinguishable from someone editing a price they liked better.
func TestAFeedStaleEntryCarriesItsProvenance(t *testing.T) {
	byModel := map[string]ratio_setting.CatalogEntry{}
	for _, entry := range ratio_setting.Catalog() {
		byModel[entry.Model] = entry
	}

	for _, name := range feedStaleModels {
		entry, ok := byModel[name]
		require.True(t, ok, "%s is listed as feed-stale but is not in the catalog", name)
		require.NotEmpty(t, entry.QuoteSource, "%s must record where its price came from", name)
		require.NotEmpty(t, entry.QuoteDate, "%s must record when it was quoted", name)
		require.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, entry.QuoteDate,
			"%s: QuoteDate must be YYYY-MM-DD so staleness is legible", name)
	}
}

// TestAStaleFeedDoesNotFailTheCheck -- a permanently red check is one nobody
// reads. The catalog is deliberately right here, so this is information, not a
// fault; the same reasoning as the standing "unverifiable" entries.
func TestAStaleFeedDoesNotFailTheCheck(t *testing.T) {
	require.False(t, hasDrift([]Finding{{Kind: "feed-stale", Model: "deepseek-v4-pro"}}))
	require.False(t, hasDrift([]Finding{{Kind: "unverifiable", Model: "glm-5-turbo"}}))
	require.True(t, hasDrift([]Finding{{Kind: "price-changed", Model: "gpt-4o"}}),
		"a genuine price change must still fail")
}

// TestEveryCatalogEntryIsEitherCheckedOrDeclaredUnverifiable makes sure the
// checker has an opinion about all 59 models. A model that fell through both
// branches would be silently unchecked, which is exactly the state this whole
// change exists to eliminate.
func TestEveryCatalogEntryIsEitherCheckedOrDeclaredUnverifiable(t *testing.T) {
	feed := loadFixture(t)
	findings := Check(feed)

	unverifiable := map[string]bool{}
	for _, finding := range findings {
		if finding.Kind == "unverifiable" {
			unverifiable[finding.Model] = true
		}
	}

	for _, entry := range ratio_setting.Catalog() {
		if unverifiable[entry.Model] {
			require.True(t, entry.Unverified, "%s reported unverifiable but is not flagged", entry.Model)
			continue
		}
		require.False(t, entry.Unverified, "%s is flagged unverified but was checked", entry.Model)

		provider, ok := feed[entry.Vendor]
		require.True(t, ok, "%s names vendor %q, absent from the fixture", entry.Model, entry.Vendor)
		_, ok = provider.Models[entry.UpstreamID()]
		require.True(t, ok, "%s points at %s/%s, absent from the fixture",
			entry.Model, entry.Vendor, entry.UpstreamID())
	}
}

func TestUnverifiableCountIsStable(t *testing.T) {
	var count int
	for _, finding := range Check(loadFixture(t)) {
		if finding.Kind == "unverifiable" {
			count++
		}
	}
	require.Equal(t, 5, count,
		"unverifiable entries are prices nothing can defend; growing this number needs a reason")
}

// TestCheckDetectsAPriceChange mutates the fixture the way a vendor repricing
// would, and asserts the checker notices and fails.
func TestCheckDetectsAPriceChange(t *testing.T) {
	feed := loadFixture(t)

	// Anthropic halves Opus input.
	anthropic := feed["anthropic"]
	entry := anthropic.Models["claude-opus-4-8"]
	entry.Cost.Input = 2.5
	anthropic.Models["claude-opus-4-8"] = entry

	findings := Check(feed)
	require.True(t, hasDrift(findings))

	var found bool
	for _, finding := range findings {
		if finding.Model == "claude-opus-4-8" && finding.Field == "input" {
			found = true
			require.Equal(t, "price-changed", finding.Kind)
			require.InDelta(t, 5, finding.Catalog, 1e-9)
			require.InDelta(t, 2.5, finding.Upstream, 1e-9)
		}
	}
	require.True(t, found, "a halved input price must be reported")
}

// TestCheckDetectsEachCachePriceIndependently -- cache read and cache write
// drift separately from input and output, and a checker that only compared the
// two headline prices would miss a cache repricing entirely. Anthropic's cache
// write is 1.25x input, so it moves on its own.
func TestCheckDetectsEachCachePriceIndependently(t *testing.T) {
	for _, tc := range []struct {
		field string
		apply func(*modelsDevCost)
	}{
		{"output", func(cost *modelsDevCost) { cost.Output = 99 }},
		{"cache_read", func(cost *modelsDevCost) { value := 99.0; cost.CacheRead = &value }},
		{"cache_write", func(cost *modelsDevCost) { value := 99.0; cost.CacheWrite = &value }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			feed := loadFixture(t)
			anthropic := feed["anthropic"]
			entry := anthropic.Models["claude-opus-5"]
			cost := *entry.Cost
			tc.apply(&cost)
			entry.Cost = &cost
			anthropic.Models["claude-opus-5"] = entry

			var found bool
			for _, finding := range Check(feed) {
				if finding.Model == "claude-opus-5" && finding.Field == tc.field {
					found = true
					require.InDelta(t, 99, finding.Upstream, 1e-9)
				}
			}
			require.True(t, found, "%s drift must be reported", tc.field)
		})
	}
}

func TestCheckDetectsARetiredModel(t *testing.T) {
	feed := loadFixture(t)
	delete(feed["anthropic"].Models, "claude-opus-5")

	findings := Check(feed)
	require.True(t, hasDrift(findings))

	var found bool
	for _, finding := range findings {
		if finding.Model == "claude-opus-5" && finding.Kind == "model-retired" {
			found = true
		}
	}
	require.True(t, found, "a model that vanished upstream must be reported")
}

func TestCheckDetectsAMissingVendor(t *testing.T) {
	feed := loadFixture(t)
	delete(feed, "moonshotai")

	var found bool
	for _, finding := range Check(feed) {
		if finding.Kind == "vendor-missing" {
			found = true
			require.Equal(t, "moonshotai", "moonshotai")
		}
	}
	require.True(t, found)
}

func TestCheckDetectsAWithdrawnPrice(t *testing.T) {
	feed := loadFixture(t)
	entry := feed["openai"].Models["gpt-4o"]
	entry.Cost = nil
	feed["openai"].Models["gpt-4o"] = entry

	var found bool
	for _, finding := range Check(feed) {
		if finding.Model == "gpt-4o" && finding.Kind == "price-withdrawn" {
			found = true
		}
	}
	require.True(t, found)
}

// TestUnverifiableAloneDoesNotFailTheCheck -- the ten unlisted models are a
// standing known gap. If they failed the check, it would be red forever and
// nobody would read it, which defeats the purpose.
func TestUnverifiableAloneDoesNotFailTheCheck(t *testing.T) {
	findings := Check(loadFixture(t))
	require.NotEmpty(t, findings, "the unverifiable entries must still be reported")
	require.False(t, hasDrift(findings))
}
