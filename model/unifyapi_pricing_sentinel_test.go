/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GetModelRatio answers an unknown model with (37.5, false): a sentinel worth
// $75 per 1M, roughly 250x a typical model, paired with a flag saying it is not
// a price. /api/pricing used to read the number and drop the flag.
//
// It is reachable the moment a channel lists a model the catalog does not --
// by adding one without pricing it, or by delisting one while a channel still
// offers it. Both have happened here: glm-5.3 was published at the sentinel
// before it was catalogued, and a delisted model did it again on staging on
// 2026-09-28.
//
// The relay refuses such a model, so publishing it advertises a price nobody
// can be charged. This pins that /api/pricing leaves it out instead.
func TestUncataloguedModelIsNotPublishedAtTheSentinelPrice(t *testing.T) {
	resetPricingEndpointTestTables(t)
	ratio_setting.InitRatioSettings()

	const priced = "claude-opus-4-8"
	const unpriced = "model-no-channel-should-ever-have-priced"

	_, hasRatio, _ := ratio_setting.GetModelRatio(unpriced)
	require.False(t, hasRatio, "fixture must name a model the catalog does not price")
	sentinel, _, _ := ratio_setting.GetModelRatio(unpriced)
	require.InDelta(t, 37.5, sentinel, 1e-9, "the sentinel this test exists to keep off the page")

	insertPricingEndpointChannel(t, 1, 1, pricingEndpointAdvancedCustomConfig())
	insertPricingEndpointAbility(t, 1, priced)
	insertPricingEndpointAbility(t, 1, unpriced)
	InitChannelCache()
	InvalidatePricingCache()

	published := map[string]Pricing{}
	for _, row := range GetPricing() {
		published[row.ModelName] = row
	}

	require.Contains(t, published, priced, "a catalogued model on a channel is still published")

	// The row stays -- it carries the model's groups and endpoint types, which
	// TestPricingNativeChannelEndpointTypesUnchanged reads for models that have
	// no catalog entry. What must not survive is the number.
	require.Contains(t, published, unpriced, "the row still carries groups and endpoints")
	assert.True(t, published[unpriced].Unpriced,
		"a model with no baseline price must say so")
	assert.NotEqual(t, 37.5, published[unpriced].ModelRatio,
		"the 37.5 sentinel must never reach a public payload: it is $75/1M, "+
			"roughly 250x a typical model, for something the relay refuses to serve")
	assert.Zero(t, published[unpriced].ModelRatio)

	// The guard is worth nothing if it also drops real models, so pin that the
	// catalogued row came through with its own ratio rather than the sentinel.
	wantRatio, ok, _ := ratio_setting.GetModelRatio(priced)
	require.True(t, ok)
	assert.InDelta(t, wantRatio, published[priced].ModelRatio, 1e-9)
	assert.NotEqual(t, 37.5, published[priced].ModelRatio)
	assert.False(t, published[priced].Unpriced)
}
