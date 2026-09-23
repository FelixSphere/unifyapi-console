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

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Operator rule, 2026-09-22, stated about jev-1.13: a model the operator did
// not deliberately discount is advertised at LIST. Do not infer a per-model
// price from the `default` group's broad ratio.
//
// The temptation is real and I already gave in to it once: the relay DOES fall
// back to the group ratio when no override names the model, so publishing that
// number makes the shop window agree with the till. But it also commits the
// business to a per-model price nobody chose, on every model added from then
// on -- a new catalogue row starts with no override, so the inference would
// quietly discount each new listing at the moment it appears. Quoting list
// while charging less is the safe direction of that gap; the reverse is not.

func TestAModelWithNoDeliberateOverrideIsAdvertisedAtListPrice(t *testing.T) {
	ratio_setting.InitRatioSettings()
	// The group ratio discounts everything, exactly as production's does.
	withGroupRatio(t, `{"default":0.9}`)
	// Only gpt-4o was chosen on purpose.
	withGroupModelDiscount(t, `{"default":{"gpt-4o":0.9}}`)

	rows := applyDefaultGroupModelPricing(publishedRows())

	byModel := map[string]*float64{}
	for _, row := range rows {
		byModel[row.ModelName] = row.DefaultGroupModelRatio
	}

	require.NotNil(t, byModel["gpt-4o"], "the operator set this one on purpose")
	assert.InDelta(t, 0.9, *byModel["gpt-4o"], 1e-9)

	assert.Nil(t, byModel["claude-opus-5"],
		"no override names it, so it is advertised at list -- the 0.9 group ratio must not leak into the public price")
	assert.Nil(t, byModel["gemini-2.5-flash"],
		"same: a broad group ratio is not a per-model discount we publish")
}

// An override of exactly 1 is still a deliberate setting, so it is published
// as-is rather than silently dropped -- the operator saying "this model is at
// list" is different from the operator saying nothing.
func TestAnExplicitOverrideIsPublishedEvenWhenItIsNotADiscount(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupRatio(t, `{"default":0.9}`)
	withGroupModelDiscount(t, `{"default":{"claude-opus-5":1}}`)

	for _, row := range applyDefaultGroupModelPricing(publishedRows()) {
		if row.ModelName != "claude-opus-5" {
			continue
		}
		require.NotNil(t, row.DefaultGroupModelRatio)
		assert.InDelta(t, 1.0, *row.DefaultGroupModelRatio, 1e-9)
	}
}

// The group ratio changing must not move any published number.
func TestChangingTheGroupRatioDoesNotMoveThePublishedNewUserPrice(t *testing.T) {
	ratio_setting.InitRatioSettings()
	withGroupModelDiscount(t, `{"default":{"gpt-4o":0.8}}`)

	snapshot := func() map[string]*float64 {
		out := map[string]*float64{}
		for _, row := range applyDefaultGroupModelPricing(publishedRows()) {
			out[row.ModelName] = row.DefaultGroupModelRatio
		}
		return out
	}

	withGroupRatio(t, `{"default":1}`)
	atOne := snapshot()
	withGroupRatio(t, `{"default":0.5}`)
	atHalf := snapshot()

	for name := range atOne {
		if atOne[name] == nil {
			assert.Nil(t, atHalf[name], "%s: a group-ratio change must not create a published discount", name)
			continue
		}
		require.NotNil(t, atHalf[name])
		assert.InDelta(t, *atOne[name], *atHalf[name], 1e-9,
			"%s: the published number comes from the override alone", name)
	}
}
