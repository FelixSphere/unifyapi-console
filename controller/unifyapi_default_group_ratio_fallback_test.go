/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The public "xx% off by default" number has to agree with what the till
// charges. HandleGroupRatio bills a new user at the per-model override when one
// names the model, and otherwise at the `default` group's own ratio -- so a
// model with no override is still discounted. Advertising list price on it
// under-sells a real discount, and the gap grows with every model added, since
// a new catalogue row starts with no override.

func ratiosByModel(rows []model.Pricing) map[string]*float64 {
	out := map[string]*float64{}
	for _, row := range rows {
		out[row.ModelName] = row.DefaultGroupModelRatio
	}
	return out
}

func TestAModelWithNoOverrideStillAdvertisesTheDefaultGroupsDiscount(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"default":0.9}`)
	withGroupModelDiscount(t, `{"default":{"gpt-4o":0.9}}`)

	got := ratiosByModel(applyDefaultGroupModelPricing(publishedRows()))

	require.NotNil(t, got["claude-opus-5"], "no override names it, but the default group's 0.9 still bills it")
	assert.InDelta(t, 0.9, *got["claude-opus-5"], 1e-9)
	require.NotNil(t, got["gemini-2.5-flash"])
	assert.InDelta(t, 0.9, *got["gemini-2.5-flash"], 1e-9)
	require.NotNil(t, got["gpt-4o"], "the override says the same thing here")
	assert.InDelta(t, 0.9, *got["gpt-4o"], 1e-9)
}

// The override is final, not a second multiplier -- 0.8 over a group ratio of
// 0.9 is 0.8, never 0.72. This is the same rule HandleGroupRatio applies.
func TestAPerModelOverrideReplacesTheGroupRatioRatherThanCompoundingIt(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"default":0.9}`)
	withGroupModelDiscount(t, `{"default":{"gpt-4o":0.8}}`)

	got := ratiosByModel(applyDefaultGroupModelPricing(publishedRows()))

	require.NotNil(t, got["gpt-4o"])
	assert.InDelta(t, 0.8, *got["gpt-4o"], 1e-9, "the override wins outright")
	require.NotNil(t, got["claude-opus-5"])
	assert.InDelta(t, 0.9, *got["claude-opus-5"], 1e-9, "everything else keeps the group ratio")
}

// No discount from either source must stay silent, or Model Square renders a
// badge reading "0% off".
func TestNoDiscountFromEitherSourceLeavesTheFieldAbsent(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"default":1}`)
	withGroupModelDiscount(t, `{}`)

	for name, ratio := range ratiosByModel(applyDefaultGroupModelPricing(publishedRows())) {
		assert.Nil(t, ratio, "%s: nothing discounts it, so the field must not appear", name)
	}
}

// The fallback must not become a per-viewer quote: it describes the `default`
// group, so every reader sees it.
func TestTheGroupRatioFallbackIsStillTheSameForEveryViewer(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"default":0.9}`)
	withGroupModelDiscount(t, `{"Chinhin":{"gpt-4o":0.7}}`)

	anonymous := ratiosByModel(applyDefaultGroupModelPricing(applyCustomerGroupModelPricing(publishedRows(), "")))
	chinhin := ratiosByModel(applyDefaultGroupModelPricing(applyCustomerGroupModelPricing(publishedRows(), "Chinhin")))

	for name, ratio := range anonymous {
		require.NotNil(t, ratio)
		require.NotNil(t, chinhin[name])
		assert.InDelta(t, *ratio, *chinhin[name], 1e-9,
			"%s: the new-user price describes the default group, not the reader", name)
	}
}
