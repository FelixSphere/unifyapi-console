package ratio_setting

// UNIFYAPI-FORK: the Seedance 2.0 family must never be sold below what we pay
// for it.
//
// This has already gone wrong once in this catalog, on the neighbouring model:
// MiniMax-H3 and seedance-2.5 were both listed at exactly the supplier's price,
// so every call earned nothing at list and lost money at any group discount.
// The fix was a 20%-at-list floor. These assertions keep the 2.0 family on that
// floor.
//
// The supplier costs below are typed by hand from the vendor, never read back
// out of the catalog. A test that derived them from the table it is checking
// would pass no matter what the table said -- which is exactly how a tenfold
// gpt-4o error once went green here.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedanceDefaultGroupRatio is what every customer pays today (default_group_model_ratio
// = 0.9, the "90折" basic external price). seedanceWorstCaseGroupRatio is the deepest
// standing customer discount the catalog is held to.
const (
	seedanceDefaultGroupRatio   = 0.9
	seedanceWorstCaseGroupRatio = 0.85
)

func TestSeedance20IsNeverSoldBelowSupplierCost(t *testing.T) {
	cases := []struct {
		model string
		// costUSD is USD per 1M tokens that WE are charged.
		costUSD float64
		// source records where costUSD was read, so the next person can re-check
		// it instead of trusting this file.
		source string
	}{
		// FlatKey quotes these two directly: model_ratio 3.5 on
		// GET /api/pricing, and ratio 3.5 == $7.00/M (usdPerMillionPerRatioUnit).
		{"seedance-2.0", 7.00, "FlatKey /api/pricing model_ratio 3.5, read 2026-10-17"},
		{"seedance2.0-pro", 7.00, "FlatKey /api/pricing model_ratio 3.5 for seedance-2.0-pro, read 2026-10-17"},

		// FlatKey has NOT priced these two -- it returns its 37.5 NOT-PRICED
		// sentinel, which is not a quote. Cost is BytePlus list, which FlatKey
		// is known to resell at 1x (proven by seedance-2.0 above, where its
		// ratio 3.5 equals BytePlus's $7.00 exactly). The no-video rate is used
		// because it is the higher of BytePlus's two tiers, so this cost basis
		// errs high and the margins asserted below are a floor.
		{"seedance-2.0-fast", 5.60, "BytePlus ModelArk list, 480p/720p no video input, read 2026-10-17"},
		{"seedance-2.0-mini", 3.50, "BytePlus ModelArk list, 480p/720p no video input, read 2026-10-17"},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			entry, ok := CatalogEntryFor(tc.model)
			require.True(t, ok, "%s is missing from the catalog, so the relay refuses it", tc.model)

			// Token-billed, like the supplier bills us. A per-second price here
			// would have nothing on the supplier side to reconcile against.
			require.Zero(t, entry.PerCallUSD, "%s must stay token-billed (%s)", tc.model, tc.source)
			require.Empty(t, entry.PriceUnit, "%s must stay token-billed (%s)", tc.model, tc.source)

			// FlatKey reports completion_ratio 1 for the whole family: output
			// costs the same as input, so our prices must match too.
			require.Equal(t, entry.InputUSD, entry.OutputUSD,
				"%s: supplier charges one rate for input and output", tc.model)

			require.Greater(t, entry.InputUSD, tc.costUSD,
				"%s is listed at $%g/M but costs $%g/M (%s) -- that is a loss on every call",
				tc.model, entry.InputUSD, tc.costUSD, tc.source)

			atList := (entry.InputUSD - tc.costUSD) / entry.InputUSD
			assert.GreaterOrEqual(t, atList, 0.20,
				"%s earns %.1f%% at list; the catalog floor is 20%%", tc.model, atList*100)

			discounted := entry.InputUSD * seedanceWorstCaseGroupRatio
			assert.Greater(t, discounted, tc.costUSD,
				"%s sells below cost at the %g group ratio: $%g/M against $%g/M cost",
				tc.model, seedanceWorstCaseGroupRatio, discounted, tc.costUSD)

			// The price every customer actually pays today.
			assert.Greater(t, entry.InputUSD*seedanceDefaultGroupRatio, tc.costUSD,
				"%s sells below cost at the 0.9 every customer gets", tc.model)
		})
	}
}
