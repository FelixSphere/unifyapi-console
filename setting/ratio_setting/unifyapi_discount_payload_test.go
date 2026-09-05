package ratio_setting

// UNIFYAPI-FORK: guard the discount payload that gets applied to production.
//
// docs/model-discount-2026-08-31.json is applied by replacing the whole
// ModelDiscount row, because that is what LoadFromJsonString does. A model
// missing from the file does not keep its old discount -- it loses it and
// starts selling at the vendor's list price that instant.
//
// That is not hypothetical. On 2026-08-29 the entire discount table was lost to
// exactly this semantics, and nobody noticed for two days because an emptied
// discount table and a healthy one both render as "official list price" on the
// pricing page.
//
// So the payload is checked here rather than trusted: it must cover every model
// production serves, and it must actually say 0.7 for the DeepSeek family.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const discountPayloadPath = "../../docs/model-discount-2026-08-31.json"

func loadDiscountPayload(t *testing.T) map[string]float64 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(discountPayloadPath))
	require.NoError(t, err, "the payload the runbook tells an operator to apply must exist")

	var body struct {
		Discounts map[string]float64 `json:"discounts"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.NotEmpty(t, body.Discounts)
	return body.Discounts
}

// TestPayloadCoversEveryServedModel is the one that would have prevented the
// 08-29 loss.
func TestPayloadCoversEveryServedModel(t *testing.T) {
	payload := loadDiscountPayload(t)
	production := loadProductionPricing(t)

	var missing []string
	for _, row := range production.Data {
		if _, ok := payload[row.Model]; !ok {
			missing = append(missing, row.Model)
		}
	}
	assert.Empty(t, missing,
		"these models are served but absent from the payload. Applying it REPLACES the whole "+
			"ModelDiscount row, so each of them would silently jump to the vendor's list price: %s",
		strings.Join(missing, ", "))
}

// TestPayloadPricesNothingItCannot -- a discount naming a model with no catalog
// row is rejected by the server on save, so it would fail the apply rather than
// half-apply. Catching it here means finding out before the change window.
func TestPayloadNamesOnlyCataloguedModels(t *testing.T) {
	for model := range loadDiscountPayload(t) {
		_, ok := CatalogEntryFor(model)
		assert.True(t, ok, "%s has no catalog row, so it has no official price to discount from", model)
	}
}

// TestPayloadSetsDeepSeekToSeventyPercent pins the commercial decision, in the
// file that actually gets applied rather than only in a commit message.
func TestPayloadSetsDeepSeekToSeventyPercent(t *testing.T) {
	payload := loadDiscountPayload(t)

	var seen int
	for model, discount := range payload {
		if !strings.HasPrefix(model, "deepseek") {
			continue
		}
		seen++
		assert.InDelta(t, 0.7, discount, 1e-12,
			"%s: the whole DeepSeek family goes to 0.7. A partial rollout is the shape of a "+
				"mistake, not of a decision.", model)
	}
	assert.Equal(t, 5, seen, "expected the five DeepSeek models to be in the payload")
}

// TestPayloadLeavesEveryOtherModelAlone. The change is DeepSeek-only; anything
// else moving in this file is an accident that would reprice a customer with no
// decision behind it.
func TestPayloadLeavesEveryOtherModelAlone(t *testing.T) {
	for model, discount := range loadDiscountPayload(t) {
		if strings.HasPrefix(model, "deepseek") {
			continue
		}
		assert.InDelta(t, 0.9, discount, 1e-12,
			"%s changed too. This payload is meant to move DeepSeek and nothing else; "+
				"if another model really is being repriced, it needs its own line in the runbook.",
			model)
	}
}
