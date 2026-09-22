package doubao

// UNIFYAPI-FORK: Seedance is priced in two places -- the catalog row bills the
// base $/M token rate, and videoPriceTable turns "has video input" and the
// output resolution into a multiplier on that base. The multiplier is only
// right while the table's base equals the catalog's InputUSD; if either moves
// alone, the customer is billed base x (stale / new) without any test noticing.

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoPriceTableBaseMatchesTheCatalogueListPrice(t *testing.T) {
	checked := 0
	for model, prices := range videoPriceTable {
		entry, catalogued := ratio_setting.CatalogEntryFor(model)
		if !catalogued {
			continue
		}
		checked++
		base := prices[videoPriceKey{}]
		assert.InDelta(t, entry.InputUSD, base, 1e-9,
			"%s: videoPriceTable base $%g/M disagrees with catalogue InputUSD $%g/M", model, base, entry.InputUSD)
		assert.InDelta(t, entry.InputUSD, entry.OutputUSD, 1e-9,
			"%s: Seedance reports one token total, so input and output must carry the same rate", model)
		for key, price := range prices {
			assert.LessOrEqual(t, price, base,
				"%s %+v: a variant priced above the base would bill more than the catalogue advertises", model, key)
		}
	}
	require.GreaterOrEqual(t, checked, 3, "seedance-2.5 and both provider ids must be catalogued and in the table")
}

func TestEveryCataloguedSeedanceModelIsInTheVideoPriceTable(t *testing.T) {
	for _, model := range ModelList {
		if _, catalogued := ratio_setting.CatalogEntryFor(model); !catalogued {
			continue
		}
		_, priced := videoPriceTable[model]
		assert.True(t, priced, "%s is sold but has no video-input schedule, so video input would bill at the no-video rate", model)
	}
}
