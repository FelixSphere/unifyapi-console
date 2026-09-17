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

func defaultCustomerOf(t *testing.T, programId int) (PartnershipCustomer, bool) {
	t.Helper()
	var customer PartnershipCustomer
	err := DB.Where("program_id = ? AND is_default = ? AND removed_at = ?", programId, true, 0).
		First(&customer).Error
	if err != nil {
		return PartnershipCustomer{}, false
	}
	return customer, true
}

// What the operator asked for: a program that does not hand everyone who
// arrives without a team into one shared catch-all.
func TestAProgramCanBeCreatedWithNoDefaultCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Teams only", Code: "teams-only", Group: "",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))

	_, found := defaultCustomerOf(t, program.Id)
	assert.False(t, found, "no group means no default customer")
}

// Clearing the group on an existing program retires its default customer.
// This is the path that removes Builder_hub_2026.
func TestClearingTheGroupRetiresTheDefaultCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	existing, found := defaultCustomerOf(t, program.Id)
	require.True(t, found, "the fixture must start with a default customer")

	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))

	_, found = defaultCustomerOf(t, program.Id)
	assert.False(t, found, "clearing the group must retire the default customer")

	// Retired, not deleted: usage already billed through it stays attributable.
	var retired PartnershipCustomer
	require.NoError(t, DB.First(&retired, existing.Id).Error)
	assert.NotZero(t, retired.RemovedAt)
	assert.False(t, retired.Enabled)
}

// And the group it was holding open becomes deletable, which is the whole
// point: Builder_hub_2026 could not be removed while the program reserved it.
func TestAProgramWithNoGroupHoldsNoGroupOpen(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))

	require.Error(t, validateOptionValueWithDB(DB, "GroupRatio", `{"default":1}`),
		"while the program reserves the group it must not be deletable")

	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))

	assert.NoError(t, validateOptionValueWithDB(DB, "GroupRatio", `{"default":1}`),
		"with no group reserved, the group can finally be removed")
}

// An upgrade must not quietly hand back the default customer the operator
// just removed.
func TestTheUpgradeDoesNotRestoreARemovedDefaultCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))

	require.NoError(t, initializePartnershipCustomers())

	_, found := defaultCustomerOf(t, program.Id)
	assert.False(t, found, "the upgrade path must respect the operator's choice")
}

// A program that still has a group keeps behaving exactly as before.
func TestAProgramWithAGroupStillGetsItsDefaultCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	customer, found := defaultCustomerOf(t, program.Id)
	require.True(t, found)
	assert.Equal(t, "partner", customer.Group)
	assert.True(t, customer.IsDefault)
}
