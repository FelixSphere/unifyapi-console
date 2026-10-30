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

// Clearing a program's group retires its default customer -- that is what "no
// catch-all" means. Reads deliberately do not send a team name, so they all
// resolved through the default customer, and every one of them started
// answering UNIFY_PROGRAM_UNAVAILABLE the moment the catch-all was removed.
//
// That reached production: a live integration went from working to refusing
// every workspace read, with a bare program-unavailable error and no reason,
// pointing at a program that was enabled and inside its schedule.
func TestAReadResolvesAProgramThatHasNoDefaultCustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	program := PartnershipProgram{Name: "Builder hub", Code: "builder-hub", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	// What clearing the group does.
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).
		Update("removed_at", 1).Error)

	offer, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name}, false)
	require.NoError(t, err, "a read needs the program, not a catch-all customer")
	require.NotNil(t, offer)
	assert.Equal(t, program.Id, offer.Program.Id)
	assert.Zero(t, offer.CustomerId, "and it resolves no customer, rather than a wrong one")
	assert.Empty(t, offer.CustomerGroup)
}

// Enrolling is not a read. Writing an account into a program with no customer
// would give it an empty pricing group -- a member of nothing, on no price
// list -- so it is refused, and the refusal names the customer, which is what
// is missing.
func TestEnrollingStillNeedsACustomer(t *testing.T) {
	setupPartnershipTestDB(t)
	require.NoError(t, DB.AutoMigrate(&BuilderIdentity{}, &Token{}))
	program := PartnershipProgram{Name: "Builder hub", Code: "builder-hub", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).
		Update("removed_at", 1).Error)

	_, err := ConnectBuilderIdentityWithProgram("owner", "owner@example.invalid",
		BuilderProgramSelector{ProgramName: program.Name}, "")
	require.ErrorIs(t, err, ErrPartnershipCustomerUnavailable)

	var identities int64
	require.NoError(t, DB.Model(&BuilderIdentity{}).Count(&identities).Error)
	assert.Zero(t, identities, "and nothing is created on the way to refusing")
	var users int64
	require.NoError(t, DB.Model(&User{}).Count(&users).Error)
	assert.Zero(t, users)
}

// Naming a team still works when there is no catch-all: the team is its own
// customer and never depended on the default.
func TestNamingATeamWorksWithoutACatchAll(t *testing.T) {
	setupPartnershipTestDB(t)
	program := PartnershipProgram{Name: "Builder hub", Code: "builder-hub", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).
		Update("removed_at", 1).Error)
	require.NoError(t, DB.Create(&PartnershipCustomer{
		ProgramId: program.Id, Name: "Nusa Labs", Code: "nusa-labs", Group: "vip", Enabled: true,
	}).Error)

	offer, err := ResolveBuilderProgram(DB,
		BuilderProgramSelector{ProgramName: program.Name, CustomerName: "Nusa Labs"}, false)
	require.NoError(t, err)
	require.NotNil(t, offer)
	assert.Equal(t, "vip", offer.CustomerGroup)
	assert.NotZero(t, offer.CustomerId)
}

// A default customer that exists but is disabled is a broken program, not a
// program without a catch-all, and must still fail rather than resolve to no
// customer.
func TestADisabledDefaultCustomerIsStillAFailure(t *testing.T) {
	setupPartnershipTestDB(t)
	program := PartnershipProgram{Name: "Builder hub", Code: "builder-hub", Group: "partner", Enabled: true}
	require.NoError(t, CreatePartnershipProgram(&program))
	require.NoError(t, DB.Model(&PartnershipCustomer{}).Where("program_id = ?", program.Id).
		Update("enabled", false).Error)

	_, err := ResolveBuilderProgram(DB, BuilderProgramSelector{ProgramName: program.Name}, false)
	require.ErrorIs(t, err, ErrPartnershipProgramUnavailable,
		"a disabled catch-all is a misconfiguration, not the absence of one")
}
