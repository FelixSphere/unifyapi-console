/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// A program discount is merged into the stored customer price table, and the
// stored table can carry what the live one does not: an entry for a model
// that has since been delisted. The loader drops such an entry on the way
// into memory. The program discount did not. It committed the merged row and
// then published it unsanitised, where it was refused, so the operator read
// "program saved, but applying the discount to its customers failed: 拒绝保存
// 客户模型价格" for a discount that was stored and started billing with the
// next sync. Staging's table was in that state on 2026-11-22.
//
// Two outcomes are honest, and each test holds one: the discount is applied
// and in force when the call returns, or it is refused and nothing was
// written.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// storeCustomerPricesBehindTheLiveTable rewrites the stored table without
// touching memory, which is where a running server stands after a model is
// delisted (the loader skipped the entry) or after a write that ran no Go.
func storeCustomerPricesBehindTheLiveTable(t *testing.T, edit func(map[string]map[string]float64)) string {
	t.Helper()
	stored := customerModelPrices(t)
	edit(stored)
	encoded, err := common.Marshal(stored)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where(map[string]any{"key": "GroupModelDiscount"}).
		Update("value", string(encoded)).Error)
	return string(encoded)
}

func TestAProgramDiscountIsInForceWhenItReturnsDespiteAStaleStoredEntry(t *testing.T) {
	programId, models := setupStagingShape(t)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`)) })
	// One stale entry on a customer of the program, one on a group that is
	// not in it.
	storeCustomerPricesBehindTheLiveTable(t, func(stored map[string]map[string]float64) {
		stored[stagingHandSet]["a-model-we-delisted"] = 0.9
		stored["default"]["a-model-we-delisted"] = 0.9
	})
	before := customerModelPrices(t)

	result, err := ApplyProgramDiscount(programId, 0, 0.85)

	require.NoError(t, err, "a stale entry in the stored table must not turn an applied discount into a reported failure")
	wantFilled := (len(models) - stagingHandSetRows) + len(models) + len(models)
	assert.Equal(t, wantFilled, result.ModelsWritten)

	after := customerModelPrices(t)
	for _, name := range models {
		assert.InDelta(t, 0.85, after[stagingEmptyA][name], 1e-9, "stored: %s / %s", stagingEmptyA, name)
	}
	assert.Equal(t, before["default"], after["default"],
		"a group outside the program is stored exactly as it was, stale entry included")

	// In force now, not after the next sync.
	live, ok := ratio_setting.GetGroupModelDiscount(stagingEmptyA, models[0])
	require.True(t, ok, "the discount must be billed from the moment the call returns")
	assert.InDelta(t, 0.85, live, 1e-9)
	live, ok = ratio_setting.GetGroupModelDiscount(stagingHandSet, models[0])
	require.True(t, ok)
	assert.InDelta(t, 1, live, 1e-9, "a hand-set price is still what is billed")
	_, ok = ratio_setting.GetGroupModelDiscount("default", "a-model-we-delisted")
	assert.False(t, ok, "the delisted model must not become billable")
}

// A stale model is the one thing the loader forgives. A multiplier it would
// refuse (here a zero, which a refused save could leave in the row before
// refusals were checked ahead of the write) still stops the discount, and a
// stopped discount must not have been stored.
func TestAProgramDiscountThatCannotBeLoadedIsNotStored(t *testing.T) {
	programId, models := setupStagingShape(t)
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`)) })
	stored := storeCustomerPricesBehindTheLiveTable(t, func(stored map[string]map[string]float64) {
		stored["default"][models[0]] = 0
	})
	writes := countOptionWrites(t)
	var historyBefore int64
	require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&historyBefore).Error)

	_, err := ApplyProgramDiscount(programId, 0, 0.85)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "default / "+models[0], "the refusal must name the entry in the way")
	assert.Equal(t, stored, storedOptionValue(t, "GroupModelDiscount"),
		"a discount reported as not applied must not be in the stored table")
	assert.Zero(t, *writes)
	var historyAfter int64
	require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&historyAfter).Error)
	assert.Equal(t, historyBefore, historyAfter, "nothing was replaced, so there is nothing to record")
	_, ok := ratio_setting.GetGroupModelDiscount(stagingEmptyA, models[0])
	assert.False(t, ok, "and it must not be billed")
}
