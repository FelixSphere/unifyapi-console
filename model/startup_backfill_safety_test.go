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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The state production was left in: a program whose default customer was
// retired, and whose group was then set again -- so the retired row holds
// (program_id, group).
func programWithRetiredDefaultHoldingTheKey(t *testing.T) *PartnershipProgram {
	t.Helper()
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	require.NoError(t, DB.Model(&PartnershipCustomer{}).
		Where("program_id = ? AND is_default = ?", program.Id, true).
		Updates(map[string]any{"enabled": false, "removed_at": 1}).Error)
	return program
}

// The startup backfill blind-inserted a default customer, and the unique index
// on (program_id, group) does not exclude removed rows -- so a retired default
// holds that key forever and the insert fails on every boot, not once.
//
// The backfill runs inside InitDB, whose error is fatal in main. So this was a
// whole-product outage, console and relay together, armed by a data state that
// had been sitting harmlessly in production for hours and would only detonate
// at the next restart.
func TestTheStartupBackfillRevivesARetiredDefaultInsteadOfCollidingWithIt(t *testing.T) {
	setupPartnershipTestDB(t)
	program := programWithRetiredDefaultHoldingTheKey(t)

	require.NoError(t, initializePartnershipCustomers(),
		"the backfill must not fail on a program whose default was retired")

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ? AND removed_at = ?",
		program.Id, true, 0).First(&customer).Error)
	assert.Equal(t, "partner", customer.Group)
	assert.True(t, customer.Enabled)
}

// Reviving, not duplicating: a second customer row for the same team would
// split whatever was billed through the first.
func TestTheBackfillRevivesTheSameRowRatherThanMintingASecond(t *testing.T) {
	setupPartnershipTestDB(t)
	program := programWithRetiredDefaultHoldingTheKey(t)
	var before PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ?", program.Id, true).
		First(&before).Error)

	require.NoError(t, initializePartnershipCustomers())

	var customers []PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND is_default = ?", program.Id, true).
		Find(&customers).Error)
	require.Len(t, customers, 1, "exactly one default customer, not two")
	assert.Equal(t, before.Id, customers[0].Id, "and it is the original row")
}

// Running it twice must be a no-op, the way a boot-time migration has to be.
func TestTheBackfillIsStillIdempotentAfterReviving(t *testing.T) {
	setupPartnershipTestDB(t)
	program := programWithRetiredDefaultHoldingTheKey(t)
	require.NoError(t, initializePartnershipCustomers())
	require.NoError(t, initializePartnershipCustomers())

	var count int64
	require.NoError(t, DB.Model(&PartnershipCustomer{}).
		Where("program_id = ? AND is_default = ?", program.Id, true).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

// A program the operator deliberately left without a catch-all keeps none: the
// revive must not resurrect what clearing the group retired on purpose.
func TestTheBackfillLeavesADeliberatelyEmptyProgramAlone(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Teams only", Code: "teams-only", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))

	require.NoError(t, initializePartnershipCustomers())

	var count int64
	require.NoError(t, DB.Model(&PartnershipCustomer{}).
		Where("program_id = ? AND is_default = ? AND removed_at = ?", program.Id, true, 0).
		Count(&count).Error)
	assert.Zero(t, count, "no group means no catch-all, and a reboot must respect that")
}
