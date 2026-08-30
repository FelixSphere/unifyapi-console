package ratio_setting

// UNIFYAPI-FORK: proves seed-model-discount-neutral.sql is actually neutral.
//
// The claim "this rollout does not change anyone's invoice" is the entire
// justification for shipping the official-price baseline without a customer
// announcement. It is a checkable claim, so it is checked here rather than
// asserted in a SQL comment: apply the seed's discounts to the catalog and
// every billing ratio must come back byte-equal to what production was
// charging.
//
// If this fails, the seed no longer reproduces production and applying it would
// silently reprice customers -- which is the exact failure it exists to prevent.

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	productionFixture = "testdata/production-pricing-2026-08-28.json"
	neutralSeedPath   = "../../seed-model-discount-neutral.sql"
)

type productionPricing struct {
	Captured   string             `json:"captured"`
	GroupRatio map[string]float64 `json:"group_ratio"`
	Data       []struct {
		Model string  `json:"model_name"`
		Ratio float64 `json:"model_ratio"`
	} `json:"data"`
}

func loadProductionPricing(t *testing.T) productionPricing {
	t.Helper()
	raw, err := os.ReadFile(productionFixture)
	require.NoError(t, err)
	var p productionPricing
	require.NoError(t, json.Unmarshal(raw, &p))
	require.NotEmpty(t, p.Data)
	return p
}

// discountsFromSeed extracts the JSON payload the seed INSERTs, so the test
// reads the same bytes that will be applied rather than a copy of them.
func discountsFromSeed(t *testing.T) map[string]float64 {
	t.Helper()
	raw, err := os.ReadFile(neutralSeedPath)
	require.NoError(t, err)

	match := regexp.MustCompile(`\('ModelDiscount', '(\{[^']*\})'\)`).FindSubmatch(raw)
	require.NotNil(t, match, "could not find the ModelDiscount INSERT in %s", neutralSeedPath)

	var discounts map[string]float64
	require.NoError(t, json.Unmarshal(match[1], &discounts))
	require.NotEmpty(t, discounts)
	return discounts
}

func TestNeutralSeedReproducesProductionPricing(t *testing.T) {
	resetRatioMapsToBaseline(t)
	t.Cleanup(func() {
		require.NoError(t, UpdateModelDiscountByJSONString(`{}`))
		resetRatioMapsToBaseline(t)
	})

	production := loadProductionPricing(t)
	discounts := discountsFromSeed(t)

	encoded, err := json.Marshal(discounts)
	require.NoError(t, err)
	require.NoError(t, UpdateModelDiscountByJSONString(string(encoded)),
		"the seed's payload must pass the same validation the admin UI applies")

	var checked int
	for _, row := range production.Data {
		entry, catalogued := CatalogEntryFor(row.Model)
		if !catalogued {
			// Not in the catalog: unsellable after the release either way, so
			// there is no price to preserve. Covered by its own test below.
			continue
		}
		got, ok, _ := GetModelRatio(row.Model)
		require.True(t, ok, "%s is catalogued but not billable", row.Model)
		require.InDelta(t, row.Ratio, got, 1e-9,
			"%s would be repriced: production charges ratio %g, the seed produces %g "+
				"(official %g x discount %g)",
			row.Model, row.Ratio, got, entry.ModelRatio(), GetModelDiscount(row.Model))
		checked++
	}

	// Derived, not a magic number: every fixture row is either catalogued and
	// checked, or listed below as deliberately unsellable. A hardcoded floor
	// silently stops covering the catalog as models come and go.
	require.Equal(t, len(production.Data)-len(servedButUnsellable), checked,
		"every production model must be either price-checked or a known unsellable")
}

// TestNeutralSeedOnlyCarriesDeviations keeps the table reviewable: a model sold
// at list price is represented by its absence, not by an entry of 1.0. Padding
// it to all 58 would make the exception list unreadable, which is how the
// original ratio table became unauditable.
func TestNeutralSeedOnlyCarriesDeviations(t *testing.T) {
	resetRatioMapsToBaseline(t)
	production := loadProductionPricing(t)
	discounts := discountsFromSeed(t)

	for _, row := range production.Data {
		entry, catalogued := CatalogEntryFor(row.Model)
		if !catalogued {
			continue
		}
		atListPrice := row.Ratio == entry.ModelRatio()
		_, hasDiscount := discounts[row.Model]
		require.Equal(t, !atListPrice, hasDiscount,
			"%s: sold at list = %v, but the seed %s carry a discount",
			row.Model, atListPrice, map[bool]string{true: "does", false: "does not"}[hasDiscount])
	}
}

// TestNeutralSeedPassesAdminValidation -- the seed is applied straight to the
// options table, bypassing the endpoint, so nothing else would reject a value
// the UI would have refused.
func TestNeutralSeedPassesAdminValidation(t *testing.T) {
	problems, markups := ValidateModelDiscounts(discountsFromSeed(t))
	for _, problem := range problems {
		t.Errorf("seed would be rejected by the admin UI: %v", problem)
	}
	// Three models are sold ABOVE list price. Legitimate, but it must be a
	// visible fact rather than something the seed slips through.
	require.Len(t, markups, 3, "expected exactly the three known markups: %v", markups)
}

// servedButUnsellable are models a channel still offers that the catalog does
// not price, so the relay refuses them.
//
// Six, for two different reasons, and the distinction is the point:
//
//   - glm-5.3 was never catalogued. It has always been refused, and
//     /api/pricing has always shown its 37.5 fallback as though it were a real
//     price -- $75 per 1M input.
//   - the other five were dropped deliberately in #17 as models nobody had ever
//     been billed for. Correct to drop, but they are STILL on their channels'
//     model lists, so the pricing page keeps advertising them and a caller who
//     tries one now gets a refusal instead of a response.
//
// Neither is a regression from the pricing work. Both are loose ends with the
// same fix: take them off the channel model lists, or catalogue them. Pinned
// here so the set cannot quietly grow -- every entry is a model we advertise
// and cannot serve.
var servedButUnsellable = []string{
	"MiniMax-H3",
	"glm-5.3",
	"seedance-2.0",
	"seedance-2.0-fast",
	"seedance-2.0-mini",
	"seedance2.0-pro",
}

func TestServedButUnsellableModelsAreDeclaredAndRefused(t *testing.T) {
	resetRatioMapsToBaseline(t)
	production := loadProductionPricing(t)

	var uncatalogued []string
	for _, row := range production.Data {
		if _, ok := CatalogEntryFor(row.Model); !ok {
			uncatalogued = append(uncatalogued, row.Model)
		}
	}
	require.ElementsMatch(t, servedButUnsellable, uncatalogued,
		"a model that production serves but the catalog does not price is advertised and then refused. "+
			"Adding one needs a reason; removing one means it was catalogued or unlisted, which is the fix.")

	for _, name := range uncatalogued {
		_, ok, _ := GetModelRatio(name)
		require.False(t, ok, "%s must be refused, not billed at the 37.5 fallback", name)
	}
}

// --- seed-pricing.sql consistency ---

const pricingSeedPath = "../../seed-pricing.sql"

// keysInSQLList pulls the quoted identifiers out of one region of the seed.
func keysInSQLList(t *testing.T, region string) []string {
	t.Helper()
	var keys []string
	for _, m := range regexp.MustCompile(`'([A-Za-z]\w+)'`).FindAllStringSubmatch(region, -1) {
		keys = append(keys, m[1])
	}
	require.NotEmpty(t, keys)
	return keys
}

func sectionBetween(t *testing.T, body, from, to string) string {
	t.Helper()
	require.Equal(t, 1, strings.Count(body, from),
		"anchor %q must appear exactly once, or the extracted region is not the one intended", from)
	start := strings.Index(body, from)
	require.GreaterOrEqual(t, start, 0, "could not find %q in the seed", from)
	end := strings.Index(body[start:], to)
	require.GreaterOrEqual(t, end, 0, "could not find %q after %q", to, from)
	return body[start : start+end]
}

// TestSeedBacksUpEverythingItDeletes is the one that stops a rollback from being
// impossible.
//
// The seed's header tells the operator to \copy the rows out before deleting
// them. When the DELETE grew from four keys to eight, the backup instruction did
// not -- so following the file literally would have destroyed four rows with no
// copy of them anywhere, and production's are hand-tuned values that exist in no
// other place. The two lists must be the same set, and the preview SELECT above
// them must match too, since that is what an operator eyeballs first.
func TestSeedBacksUpEverythingItDeletes(t *testing.T) {
	raw, err := os.ReadFile(pricingSeedPath)
	require.NoError(t, err)
	body := string(raw)

	deleted := keysInSQLList(t, sectionBetween(t, body, "DELETE FROM options", ");"))
	backed := keysInSQLList(t, sectionBetween(t, body, `\copy (SELECT key, value`, "TO '"))
	// Anchored on the full statement: the header also mentions
	// "SELECT key, length(value)" in prose a few lines above, and matching that
	// makes the region swallow the \copy block and read sixteen keys.
	preview := keysInSQLList(t, sectionBetween(t, body, "SELECT key, length(value) FROM options", ");"))

	require.ElementsMatch(t, deleted, backed,
		"the \\copy backup must cover exactly what the DELETE removes, or following this file "+
			"loses rows that exist nowhere else")
	require.ElementsMatch(t, deleted, preview,
		"the preview SELECT is what an operator checks before running the seed; it must show "+
			"the same rows the DELETE will take")
}

// TestSeedDeletesEveryCatalogOwnedMap -- the catalog is authoritative for these
// maps, so leaving one in the database means it silently keeps shadowing the
// code baseline and the admin pricing page keeps listing models we do not sell.
func TestSeedDeletesEveryCatalogOwnedMap(t *testing.T) {
	raw, err := os.ReadFile(pricingSeedPath)
	require.NoError(t, err)

	deleted := keysInSQLList(t, sectionBetween(t, string(raw), "DELETE FROM options", ");"))

	owned := make([]string, 0, len(BaselineRatios()))
	for option := range BaselineRatios() {
		owned = append(owned, option)
	}
	require.ElementsMatch(t, owned, deleted,
		"every option the catalog owns must be deleted by the seed, and nothing else")
}

// TestSeedPreservesTheDiscountTable -- a baseline reset must not double as a
// silent repricing. ModelDiscount and ChannelCostRatio are business config and
// have to survive.
func TestSeedPreservesTheDiscountTable(t *testing.T) {
	raw, err := os.ReadFile(pricingSeedPath)
	require.NoError(t, err)

	deleted := keysInSQLList(t, sectionBetween(t, string(raw), "DELETE FROM options", ");"))
	for _, mustSurvive := range []string{"ModelDiscount", "ChannelCostRatio", "GroupRatio", "GroupGroupRatio"} {
		require.NotContains(t, deleted, mustSurvive,
			"%s is business config; deleting it here would turn a baseline reset into a repricing", mustSurvive)
	}
}
