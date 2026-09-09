package ratio_setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promotionalPrices are catalogue rows priced at a vendor's introductory rate
// that the vendor has already announced will rise, with a date.
//
// A price in a Go file does not expire. Google publishes gemini-3.7-flash and
// gemini-3.8-flash at $0.75/$3.75 "through December 31, 2026" and $1.50/$7.50
// "starting January 1, 2027" -- our cost doubles on a known date while the
// catalogue keeps quoting half of it. That is not a drift the pricing-drift
// checker can catch either: models.dev reports today's price, which is
// correct, right up until it is not.
//
// So the deadline is encoded, and this test fails BEFORE it, not after.
var promotionalPrices = []struct {
	model     string
	reviewBy  string // fail from this date, ahead of the rise
	risesOn   string
	nextInput float64
	nextOutur float64
}{
	{"gemini-3.8-flash", "2026-12-15", "2027-01-01", 1.5, 7.5},
	{"gemini-3.7-flash", "2026-12-15", "2027-01-01", 1.5, 7.5},
}

// TestPromotionalPricesHaveNotExpired is a deliberate time bomb with a fuse.
//
// It goes red on 2026-12-15, two weeks before the rise, while there is still
// time to act. Failing on the day would mean discovering it from a revenue
// report instead. If you are reading this because CI went red: the fix is to
// set the catalogue row to the new price and move reviewBy, or to drop the
// model. Do not just push the date.
func TestPromotionalPricesHaveNotExpired(t *testing.T) {
	for _, promo := range promotionalPrices {
		reviewBy, err := time.Parse("2006-01-02", promo.reviewBy)
		require.NoError(t, err)

		entry, ok := CatalogEntryFor(promo.model)
		require.True(t, ok, "%s is listed as promotional but is not in the catalogue; "+
			"remove it from promotionalPrices too", promo.model)

		if time.Now().Before(reviewBy) {
			// Still inside the promotional window. Assert only that the row
			// has not quietly been moved to some third number.
			assert.InDelta(t, 0.75, entry.InputUSD, 1e-9,
				"%s is inside its promotional window; the row should still hold the promotional price", promo.model)
			continue
		}

		assert.InDelta(t, promo.nextInput, entry.InputUSD, 1e-9,
			"%s: the vendor's promotional price ends %s and rises to $%.2f/1M input. "+
				"The catalogue still says $%.2f, so from that date we sell below cost. "+
				"Update the row and move reviewBy, or drop the model.",
			promo.model, promo.risesOn, promo.nextInput, entry.InputUSD)

		assert.InDelta(t, promo.nextOutur, entry.OutputUSD, 1e-9,
			"%s: output rises to $%.2f/1M on %s; catalogue still says $%.2f",
			promo.model, promo.nextOutur, promo.risesOn, entry.OutputUSD)
	}
}

// TestPromotionalModelsAreActuallyInTheCatalogue keeps the two lists from
// drifting apart in the other direction -- a model dropped from the catalogue
// but left here would make the guard above silently vacuous.
func TestPromotionalModelsAreActuallyInTheCatalogue(t *testing.T) {
	require.NotEmpty(t, promotionalPrices)
	for _, promo := range promotionalPrices {
		_, ok := CatalogEntryFor(promo.model)
		assert.True(t, ok, "%s", promo.model)
	}
}
