package ratio_setting

// UNIFYAPI-FORK: guards on seed-pricing.sql and on what production actually
// serves.
//
// This file used to prove that seed-model-discount-neutral.sql reproduced
// production's prices byte for byte. That seed is gone: on 2026-08-30 the
// decision was that the commercial price IS the vendor's official list price,
// with no discount layer beneath it, so a table whose only purpose was to
// restore the previous prices became a way to undo that decision by accident --
// and it also carried the documented 11.8x claude-opus-4-8 typo, since it was
// derived from live ratios by division and could not tell a discount from a
// mistake.
//
// What survives are the checks that are still load-bearing: the seed backs up
// everything it deletes, it deletes every map the catalog owns, it does not
// delete business config, and the models production serves but cannot price are
// named rather than discovered by a customer.

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// productionFixture is a capture of what production served on 2026-08-28. It is
// kept as the record of which model names production actually exposes, which is
// what TestServedButUnsellableModelsAreDeclaredAndRefused checks against.
const productionFixture = "testdata/production-pricing-2026-08-28.json"

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
