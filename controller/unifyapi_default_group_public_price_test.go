package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Model Square advertises the price a brand-new user pays -- the `default`
// group's per-model multiplier -- as "xx% off by default". That is only
// legitimate if it is a PUBLIC number: identical for anonymous visitors and
// for every logged-in group, and never a rewrite of the published list price.
// This file pins both halves. The regression it guards against is the one
// that shipped once before: the catalogue quoting different numbers to
// different people while claiming to be a catalogue.

func publishedRows() []model.Pricing {
	return []model.Pricing{
		{ModelName: "gpt-4o", ModelRatio: 1.25, CompletionRatio: 4},
		{ModelName: "claude-opus-5", ModelRatio: 2.5, CompletionRatio: 5},
		{ModelName: "gemini-2.5-flash", ModelRatio: 0.15, CompletionRatio: 100.0 / 12},
	}
}

// TestNewUserPriceIsTheSameForEveryViewer. Anonymous, a `default` user, and a
// customer with their own contract must all read the identical
// default_group_model_ratio -- it describes the `default` group, not the reader.
func TestNewUserPriceIsTheSameForEveryViewer(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{
		"default": {"gpt-4o":0.9, "claude-opus-5":0.9},
		"Chinhin": {"gpt-4o":0.7}
	}`)

	anonymous := applyDefaultGroupModelPricing(applyCustomerGroupModelPricing(publishedRows(), ""))
	defaultUser := applyDefaultGroupModelPricing(applyCustomerGroupModelPricing(publishedRows(), "default"))
	chinhin := applyDefaultGroupModelPricing(applyCustomerGroupModelPricing(publishedRows(), "Chinhin"))

	for i := range anonymous {
		name := anonymous[i].ModelName
		for viewer, rows := range map[string][]model.Pricing{"default user": defaultUser, "Chinhin": chinhin} {
			require.Equal(t, anonymous[i].DefaultGroupModelRatio == nil, rows[i].DefaultGroupModelRatio == nil,
				"%s: %s sees a new-user price where an anonymous visitor does not (or vice versa)", name, viewer)
			if anonymous[i].DefaultGroupModelRatio != nil {
				assert.InDelta(t, *anonymous[i].DefaultGroupModelRatio, *rows[i].DefaultGroupModelRatio, 1e-12,
					"%s: the advertised new-user price must not depend on who is looking", name)
			}
		}
	}

	require.NotNil(t, anonymous[0].DefaultGroupModelRatio)
	assert.InDelta(t, 0.9, *anonymous[0].DefaultGroupModelRatio, 1e-12, "gpt-4o: 10%% off by default")
	assert.Nil(t, anonymous[2].DefaultGroupModelRatio, "gemini-2.5-flash has no default override, so no badge -- new users pay list")

	// Chinhin's own 0.7 contract stays where it belongs: on the customer
	// annotation, never on the public new-user field.
	require.NotNil(t, chinhin[0].CustomerGroupModelRatio)
	assert.InDelta(t, 0.7, *chinhin[0].CustomerGroupModelRatio, 1e-12)
	assert.InDelta(t, 0.9, *chinhin[0].DefaultGroupModelRatio, 1e-12,
		"a customer's contract must not leak into the advertised new-user price")
}

// TestNewUserPriceNeverRewritesThePublishedPrice. ModelRatio IS the catalogue.
// The new-user multiplier is rendered beside it by the client; the server must
// not fold it in, or the catalogue would say $2.25 where the vendor says $2.50.
func TestNewUserPriceNeverRewritesThePublishedPrice(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"default": {"gpt-4o":0.9, "claude-opus-5":0.5}}`)

	rows := applyDefaultGroupModelPricing(publishedRows())
	want := publishedRows()
	for i := range rows {
		assert.InDelta(t, want[i].ModelRatio, rows[i].ModelRatio, 1e-12, "%s: published price moved", rows[i].ModelName)
		assert.InDelta(t, want[i].CompletionRatio, rows[i].CompletionRatio, 1e-12, "%s: completion ratio moved", rows[i].ModelName)
	}
}

// TestNewUserPriceDoesNotTouchTheSharedRows. GetPricing caches its slice for a
// minute across requests. Annotating in place would make the first request's
// view permanent for everyone; the function must return request-owned rows.
func TestNewUserPriceDoesNotTouchTheSharedRows(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"default": {"gpt-4o":0.9}}`)

	shared := publishedRows()
	_ = applyDefaultGroupModelPricing(shared)

	for _, row := range shared {
		assert.Nil(t, row.DefaultGroupModelRatio, "%s: the input slice must be left untouched", row.ModelName)
	}
}

// TestNoDefaultOverridesMeansNoBadgeAnywhere. With nothing configured for the
// `default` group the field is absent on every row -- the page shows list
// prices and nothing else, exactly as before this feature existed.
func TestNoDefaultOverridesMeansNoBadgeAnywhere(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"Chinhin": {"gpt-4o":0.7}}`)

	for _, row := range applyDefaultGroupModelPricing(publishedRows()) {
		assert.Nil(t, row.DefaultGroupModelRatio, "%s", row.ModelName)
	}
}
