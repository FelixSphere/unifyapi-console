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

func programWithNoGroup(t *testing.T) *PartnershipProgram {
	t.Helper()
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))
	return program
}

// A program with no default customer has nothing to register anyone into.
// Serving its registration code anyway handed out an empty pricing group: an
// account belonging to no customer and on no price list. Found on staging,
// where the link kept working after the group was removed.
func TestAProgramWithNoGroupOffersNoPublicRegistration(t *testing.T) {
	setupPartnershipTestDB(t)
	program := programWithNoGroup(t)

	offer, err := getPartnershipOfferByCode(DB, program.Code, false)
	require.Error(t, err, "the registration code must stop working")
	assert.ErrorIs(t, err, ErrPartnershipProgramUnavailable)
	assert.Nil(t, offer)
}

// No offer from any path may carry an empty pricing group -- that was the
// failure: an account registered through one belonged to no customer and sat
// on no price list.
//
// The first version of this test wrapped its assertion in `if err == nil`,
// which made it unfailable: the sibling test above asserts the error, so the
// body never ran. It now walks every code that resolves and checks each one,
// so it has something to be wrong about.
func TestNoOfferEverCarriesAnEmptyGroup(t *testing.T) {
	setupPartnershipTestDB(t)
	withGroup := &PartnershipProgram{
		Name: "With group", Code: "with-group", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(withGroup))
	require.NoError(t, DB.Create(&PartnershipCustomer{
		ProgramId: withGroup.Id, Name: "Nusa Labs", Code: "nusa-labs",
		Group: "vip", Enabled: true,
	}).Error)
	groupless := programWithNoGroup(t)

	resolved := 0
	for _, code := range []string{"with-group", "nusa-labs", groupless.Code} {
		offer, err := getPartnershipOfferByCode(DB, code, false)
		if err != nil {
			continue
		}
		resolved++
		require.NotNil(t, offer, "code %q", code)
		assert.NotEmpty(t, offer.CustomerGroup,
			"code %q resolved to an offer with no pricing group", code)
	}
	require.Equal(t, 2, resolved,
		"exactly the two codes with a group behind them must resolve")
}

// A program that still has a group keeps serving its registration code
// exactly as before. The refusal is scoped to programs with no default
// customer, not to programs generally.
func TestAProgramWithAGroupStillOffersRegistration(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))

	offer, err := getPartnershipOfferByCode(DB, program.Code, false)
	require.NoError(t, err)
	require.NotNil(t, offer)
	assert.Equal(t, "partner", offer.CustomerGroup)
}

// A team's own registration code is unaffected: it has its own customer, so
// it never depended on the program's group.
func TestATeamsOwnCodeStillWorksWithoutAProgramGroup(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	require.NoError(t, DB.Create(&PartnershipCustomer{
		ProgramId: program.Id, Name: "Nusa Labs", Code: "nusa-labs",
		Group: "vip", Enabled: true,
	}).Error)

	cleared := *program
	cleared.Group = ""
	require.NoError(t, UpdatePartnershipProgram(program.Id, &cleared))

	offer, err := getPartnershipOfferByCode(DB, "nusa-labs", false)
	require.NoError(t, err, "a team's own code must keep working")
	require.NotNil(t, offer)
	assert.Equal(t, "vip", offer.CustomerGroup)
}

// The branch the guard actually lives in. getPartnershipOfferByCode looks for
// a customer row first, so both tests above return before ever reaching it.
// Only a program predating customer rows falls through -- and those must keep
// working, or the refusal is over-broad.
//
// Without this, a mutation refusing every offer in that branch passes.
func TestALegacyProgramWithNoCustomerRowStillOffersRegistration(t *testing.T) {
	setupPartnershipTestDB(t)
	program := &PartnershipProgram{
		Name: "Legacy", Code: "legacy-prog", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	// Predates customer rows: delete the one creation made, so the lookup has
	// to fall through to the program itself.
	require.NoError(t, DB.Where("program_id = ?", program.Id).
		Delete(&PartnershipCustomer{}).Error)
	var remaining int64
	require.NoError(t, DB.Model(&PartnershipCustomer{}).
		Where("program_id = ?", program.Id).Count(&remaining).Error)
	require.Zero(t, remaining, "this test is meaningless unless the fallback is the only path left")

	offer, err := getPartnershipOfferByCode(DB, program.Code, false)
	require.NoError(t, err, "a legacy program must keep serving its code")
	require.NotNil(t, offer)
	assert.Equal(t, "partner", offer.CustomerGroup)
}
