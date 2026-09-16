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
	"gorm.io/gorm"
)

func seedRemovableProgram(t *testing.T, name, code string) *PartnershipProgram {
	t.Helper()
	program := &PartnershipProgram{
		Name: name, Code: code, Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	return program
}

// What the operator asked for: a program they are finished with must leave
// the console. Until now nothing could remove one at all.
func TestARemovedProgramLeavesTheConsoleList(t *testing.T) {
	setupPartnershipTestDB(t)
	keep := seedRemovableProgram(t, "Autumn batch", "autumn-batch")
	drop := seedRemovableProgram(t, "Mistyped program", "mistyped")

	_, err := DeletePartnershipProgram(drop.Id)
	require.NoError(t, err)

	programs, err := GetPartnershipPrograms()
	require.NoError(t, err)
	names := make([]string, 0, len(programs))
	for _, program := range programs {
		names = append(names, program.Name)
	}
	assert.Contains(t, names, keep.Name)
	assert.NotContains(t, names, drop.Name, "a removed program must not still be listed")
}

// The row itself survives. A program is what usage, grants and invoices are
// attributed through, so removing it must not destroy the record of what it
// already billed.
func TestRemovingAProgramKeepsItsBillingRecord(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")
	require.NoError(t, DB.Create(&PartnershipEnrollment{
		ProgramId: program.Id, UserId: 7, CustomerGroup: "partner", GrantedQuota: 5000000,
	}).Error)

	enrolled, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)
	assert.Equal(t, int64(1), enrolled, "the operator is told how many members this affected")

	var stored PartnershipProgram
	require.NoError(t, DB.First(&stored, program.Id).Error,
		"the program row must survive so past invoices stay attributable")
	assert.NotZero(t, stored.RemovedAt)
	assert.False(t, stored.Enabled, "removal must also stop the program being active")

	var enrollments []PartnershipEnrollment
	require.NoError(t, DB.Where("program_id = ?", program.Id).Find(&enrollments).Error)
	assert.Len(t, enrollments, 1, "who claimed what must not be erased")
	assert.Equal(t, 5000000, enrollments[0].GrantedQuota)
}

// The self-heal added for deleted programs keys on whether the program still
// exists. If a removed program still reads as present, a member bound to it
// is left pointing at something the operator can no longer see or fix -- the
// exact lockout that machinery was written to prevent.
func TestARemovedProgramReadsAsGoneToBoundAccounts(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")

	present, err := PartnershipProgramExists(program.Id)
	require.NoError(t, err)
	require.True(t, present, "the fixture must start with a program that exists")

	_, err = DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	present, err = PartnershipProgramExists(program.Id)
	require.NoError(t, err)
	assert.False(t, present, "a removed program must read as gone, so a bound account is rebound")
}

// Names are how Builder Hunt selects a program over the bridge. Removing a
// misconfigured program has to free its name, or the replacement cannot be
// called the right thing.
func TestARemovedProgramFreesItsNameForAReplacement(t *testing.T) {
	setupPartnershipTestDB(t)
	wrong := seedRemovableProgram(t, "Builder hub", "builder-hub-old")
	_, err := DeletePartnershipProgram(wrong.Id)
	require.NoError(t, err)

	replacement := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub-new", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(replacement),
		"the name of a removed program must be reusable")

	resolved, err := resolveProgramByName(DB, "Builder hub")
	require.NoError(t, err)
	assert.Equal(t, replacement.Id, resolved.Id,
		"resolving the name must reach the replacement, never the removed program")
}

// Codes are unique in the database, so a removed program keeps holding its
// code. The operator cannot see that row, so the refusal has to say why.
func TestReusingARemovedProgramsCodeSaysWhoHoldsIt(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")
	_, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	err = CreatePartnershipProgram(&PartnershipProgram{
		Name: "Replacement", Code: "mistyped", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 10, Enabled: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removed program",
		"the operator must be told an invisible row holds the code")
	assert.Contains(t, err.Error(), "Mistyped program")
}

// Removing the same program twice is the operator double-clicking, not a new
// instruction. The second call must not report success or move the timestamp.
func TestRemovingAProgramTwiceIsRefused(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")
	_, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	var afterFirst PartnershipProgram
	require.NoError(t, DB.First(&afterFirst, program.Id).Error)

	_, err = DeletePartnershipProgram(program.Id)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	var afterSecond PartnershipProgram
	require.NoError(t, DB.First(&afterSecond, program.Id).Error)
	assert.Equal(t, afterFirst.RemovedAt, afterSecond.RemovedAt,
		"a repeated removal must not overwrite when it happened")
}

// Removal releases the program's pricing group so the operator can reclaim
// it. That is right for a program created by mistake, and wrong while members
// are still billing under that group: deleting it would leave them pointing at
// a price that no longer exists.
func TestAReleasedGroupStaysProtectedWhileMembersRemain(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")
	require.NoError(t, DB.Create(&User{
		Username: "member1", Password: "password123", DisplayName: "Member",
		Role: 1, Status: 1, Group: "partner",
	}).Error)
	_, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	err = validateOptionValueWithDB(DB, "GroupRatio", `{"default":1}`)
	require.Error(t, err, "a group a removed program left members in must not be deletable")
	assert.Contains(t, err.Error(), "partner")
	assert.Contains(t, err.Error(), "member")
}

// The common case: a program created by mistake, with nobody in it. Its group
// must become free immediately, or removal would not actually clean anything up.
func TestAnEmptyReleasedGroupBecomesFreeImmediately(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Mistyped program", "mistyped")
	_, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	assert.NoError(t, validateOptionValueWithDB(DB, "GroupRatio", `{"default":1}`),
		"with nobody left in it, the group must be reclaimable")
}

// An enabled program is still protected by its own rule, and must not start
// reporting the weaker members-remain message instead.
func TestAnEnabledProgramStillProtectsItsGroupOutright(t *testing.T) {
	setupPartnershipTestDB(t)
	seedRemovableProgram(t, "Live program", "live-program")

	err := validateOptionValueWithDB(DB, "GroupRatio", `{"default":1}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enabled partnership program")
}

// A program's own group is not the whole story. Every auto-provisioned team
// gets a customer with its own pricing group, which is where that team's
// members actually bill. Removing the program must protect those groups too,
// or a tidy-up after removal strands exactly the teams this was built for.
func TestAReleasedCustomerGroupStaysProtectedWhileMembersRemain(t *testing.T) {
	setupPartnershipTestDB(t)
	program := seedRemovableProgram(t, "Builder hub", "builder-hub")
	require.NoError(t, DB.Create(&PartnershipCustomer{
		ProgramId: program.Id, Name: "Nusa Labs", Code: "nusa-labs",
		Group: "vip", Enabled: true,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "nusa1", Password: "password123", DisplayName: "Nusa",
		Role: 1, Status: 1, Group: "vip",
	}).Error)

	_, err := DeletePartnershipProgram(program.Id)
	require.NoError(t, err)

	// "partner" is empty and may go; "vip" still bills a member and may not.
	err = validateOptionValueWithDB(DB, "GroupRatio", `{"default":1,"vip":0.8}`)
	assert.NoError(t, err, "the program's own empty group must still be reclaimable")

	err = validateOptionValueWithDB(DB, "GroupRatio", `{"default":1,"partner":0.9}`)
	require.Error(t, err, "a customer group that still bills members must not be deletable")
	assert.Contains(t, err.Error(), "vip")
}
