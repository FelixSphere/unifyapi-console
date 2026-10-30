/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// The live staging run that would have exercised this was refused by a
// permission gate twice, so the write path has never been watched on a running
// instance. What COULD be measured was staging's exact data shape, and this
// file reproduces it: the same three customer groups in the program, the same
// 48 hand-set rows, and the same five groups that must not move.
//
// It is not a substitute for the live run and does not make that unnecessary.
// It does mean the arithmetic and the boundaries are pinned against the real
// shape rather than a convenient one, so a live run has a number to disagree
// with instead of a shrug.

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Staging, measured 2026-09-23. `UnifyAI` carries hand-set rows at 1; the two
// other customer groups of the program carry none at all.
const (
	stagingProgramName = "Builder_hub_2026_Sep_Batch"
	stagingHandSet     = "UnifyAI"
	stagingEmptyA      = "Builder_hub_2026_Sep_Batch_Chinhin"
	stagingEmptyB      = "Builder_hub_2026_Sep_Batch_staging-discount-check-1789632115"
	// Belongs to NO live customer of the program. Its name reads like a prefix
	// of the two above, which is exactly why it is here: a group-matching rule
	// written on names rather than on the customer registry would sweep it in
	// and reprice a team nobody asked us to touch.
	stagingLookalike   = "Builder_hub_2026"
	stagingHandSetRows = 48
)

func setupStagingShape(t *testing.T) (programId int, models []string) {
	t.Helper()
	setupGroupRatioProvisionTest(t)
	ratio_setting.InitRatioSettings()

	program := PartnershipProgram{Name: stagingProgramName, Code: "bh26sb", Enabled: true, Discount: 0}
	require.NoError(t, DB.Create(&program).Error)
	for i, group := range []string{stagingHandSet, stagingEmptyA, stagingEmptyB} {
		customer := PartnershipCustomer{
			ProgramId: program.Id,
			Name:      fmt.Sprintf("cust-%d", i),
			Code:      fmt.Sprintf("cust%d", i),
			Group:     group,
			Enabled:   true,
		}
		require.NoError(t, DB.Create(&customer).Error)
	}

	models = ratio_setting.CatalogModels()
	require.GreaterOrEqual(t, len(models), stagingHandSetRows+1,
		"the fixture needs more catalogued models than hand-set rows")

	// The hand-set rows, plus a lookalike group and an unrelated one that must
	// both survive untouched.
	handSet := map[string]float64{}
	for _, name := range models[:stagingHandSetRows] {
		handSet[name] = 1
	}
	seed := map[string]any{
		stagingHandSet:   handSet,
		stagingLookalike: map[string]any{models[0]: 0.9},
		"default":        map[string]any{models[0]: 0.9},
	}
	encoded, err := common.Marshal(seed)
	require.NoError(t, err)
	require.NoError(t, DB.Create(&Option{Key: "GroupModelDiscount", Value: string(encoded)}).Error)
	require.NoError(t, updateOptionMap("GroupModelDiscount", string(encoded)))

	return program.Id, models
}

// The numbers the refused staging run would have produced: 29 filled for the
// hand-set group (77 catalogued less its 48 rows), 77 for each group with
// none, 183 in total across 3 customers, with 48 preserved.
func TestTheStagingShapeFillsExactlyTheAbsentRows(t *testing.T) {
	programId, models := setupStagingShape(t)

	result, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	wantFilled := (len(models) - stagingHandSetRows) + len(models) + len(models)
	assert.Equal(t, wantFilled, result.ModelsWritten,
		"one row per absent model, and not one more")
	assert.Equal(t, 3, result.CustomersChanged)
	assert.Len(t, result.Preserved, stagingHandSetRows,
		"every hand-set row must be reported as left alone")

	after := customerModelPrices(t)
	for _, name := range models[:stagingHandSetRows] {
		assert.InDelta(t, 1, after[stagingHandSet][name], 1e-9,
			"%s / %s was set by hand and must still be 1", stagingHandSet, name)
	}
	for _, name := range models[stagingHandSetRows:] {
		assert.InDelta(t, 0.85, after[stagingHandSet][name], 1e-9,
			"%s / %s was absent and must be filled", stagingHandSet, name)
	}
	for _, group := range []string{stagingEmptyA, stagingEmptyB} {
		assert.Len(t, after[group], len(models))
		for _, name := range models {
			assert.InDelta(t, 0.85, after[group][name], 1e-9, "%s / %s", group, name)
		}
	}
}

// A group whose name looks like a prefix of the program's own groups, and an
// ordinary one, must both be untouched. This is the assertion that would fail
// if group selection ever moved from the customer registry to string matching.
func TestTheStagingShapeLeavesTheLookalikeGroupAlone(t *testing.T) {
	programId, models := setupStagingShape(t)
	before := customerModelPrices(t)

	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)

	after := customerModelPrices(t)
	for _, group := range []string{stagingLookalike, "default"} {
		assert.Equal(t, before[group], after[group],
			"%s belongs to no customer of this program and must not move", group)
		assert.InDelta(t, 0.9, after[group][models[0]], 1e-9)
	}
}

// Clearing must return the option to exactly what it was -- the property the
// refused step 4 would have proven by hashing the bytes on the instance.
func TestTheStagingShapeReturnsByteIdenticalAfterClearing(t *testing.T) {
	programId, _ := setupStagingShape(t)

	var before Option
	require.NoError(t, DB.Where("key = ?", "GroupModelDiscount").First(&before).Error)

	_, err := ApplyProgramDiscount(programId, 0, 0.85)
	require.NoError(t, err)
	_, err = ApplyProgramDiscount(programId, 0.85, 0)
	require.NoError(t, err)

	var after Option
	require.NoError(t, DB.Where("key = ?", "GroupModelDiscount").First(&after).Error)

	// Compared as parsed JSON, not raw bytes: Go's map encoding orders keys,
	// but asserting on the byte string would pin an encoder detail rather than
	// the property, and would pass or fail for the wrong reason.
	var wantMap, gotMap map[string]map[string]float64
	require.NoError(t, common.Unmarshal([]byte(before.Value), &wantMap))
	require.NoError(t, common.Unmarshal([]byte(after.Value), &gotMap))
	assert.Equal(t, wantMap, gotMap,
		"clearing must leave exactly what was there before it was applied")
}
