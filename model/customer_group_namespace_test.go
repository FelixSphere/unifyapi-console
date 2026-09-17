/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func namespaceProgram(t *testing.T) *PartnershipProgram {
	t.Helper()
	program := &PartnershipProgram{
		Name: "Builder_hub_2026_Sep_Batch", Code: "bh_2026_sep", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	return program
}

// Taken from a real Builder DEV request. The team was called "UnifyAPI" and a
// pricing group of that name already existed here, carrying 5,835 requests and
// $638 of somebody else's usage. Without this the team billed onto that
// invoice and nothing reported it.
func TestATeamNeverLandsOnAnExistingGroupBySharingItsName(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	require.NoError(t, EnsurePartnershipGroupRatio("UnifyAPI"))
	before := currentGroupRatios(t)["UnifyAPI"]

	program := namespaceProgram(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "UnifyAPI"))

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", program.Id, "UnifyAPI").
		First(&customer).Error)
	assert.NotEqual(t, "UnifyAPI", customer.Group,
		"the team must not bill through the group that already exists")
	assert.Contains(t, customer.Group, "UnifyAPI", "but it must stay recognisable")
	assert.Contains(t, customer.Group, program.Name, "namespaced by its program")

	assert.InDelta(t, before, currentGroupRatios(t)["UnifyAPI"], 1e-9,
		"the existing group's price must be untouched")
}

// Two programs may each have a team of the same name. They are different
// customers and must not share an invoice.
func TestTheSameTeamNameInTwoProgramsIsTwoCustomers(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	first := &PartnershipProgram{
		Name: "Spring_batch", Code: "spring", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	second := &PartnershipProgram{
		Name: "Autumn_batch", Code: "autumn", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(first))
	require.NoError(t, CreatePartnershipProgram(second))
	require.NoError(t, ProvisionBuilderCustomer(first.Name, "Acme"))
	require.NoError(t, ProvisionBuilderCustomer(second.Name, "Acme"))

	var customers []PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Acme").Find(&customers).Error)
	require.Len(t, customers, 2)
	assert.NotEqual(t, customers[0].Group, customers[1].Group,
		"two programs' teams of the same name must not share one invoice")
	assert.NotEqual(t, customers[0].ProgramId, customers[1].ProgramId)
}

// The group is the bill-to line, so the readable form wins where it fits and
// the fallbacks take over only when the column cannot hold it.
func TestTheGroupNameTakesTheMostLegibleFormThatFits(t *testing.T) {
	short := &PartnershipProgram{Name: "Builder_hub", Code: "bh"}
	assert.Equal(t, "Builder_hub_Nusa Labs",
		customerPricingGroupName(short, "Nusa Labs", "nusa-labs"))

	longProgram := &PartnershipProgram{Name: strings.Repeat("P", 60), Code: "bh"}
	assert.Equal(t, "bh_Nusa Labs",
		customerPricingGroupName(longProgram, "Nusa Labs", "nusa-labs"),
		"a program name that will not fit falls back to its code")

	assert.Equal(t, "bh_nusa",
		customerPricingGroupName(longProgram, strings.Repeat("N", 100), "nusa"),
		"a team name that will not fit falls back to its derived code")
}

// Whatever form is chosen must fit the column, or the insert fails and the
// team cannot be provisioned at all.
func TestEveryGeneratedGroupNameFitsTheColumn(t *testing.T) {
	program := &PartnershipProgram{Name: strings.Repeat("P", 120), Code: strings.Repeat("c", 64)}
	for _, team := range []string{"Acme", strings.Repeat("N", 120), "投资管理有限公司", "A"} {
		group := customerPricingGroupName(program, team, partnershipCodeFromName(team))
		assert.True(t, fitsPricingGroupColumn(group),
			"team %q produced %d bytes / %d runes: %q",
			team, len(group), len([]rune(group)), group)
	}
}

// #125's retry ladder still works on top of the namespacing: a second team
// whose name derives to the same code gets its own group, not a shared one.
func TestASecondTeamNeedingTheSameCodeGetsItsOwnGroup(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	program := namespaceProgram(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Acme Robotics"))

	var first PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Acme Robotics").First(&first).Error)
	// A different display name deriving to the same registration code.
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Acme  Robotics"))

	var second PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Acme  Robotics").First(&second).Error)
	assert.NotEqual(t, first.Code, second.Code, "codes must not collide")
	assert.NotEqual(t, first.Group, second.Group, "and neither may the invoices")
}

// The namespaced name can itself be taken -- an operator may have configured a
// group of exactly that name by hand. The allocator must step past it rather
// than adopt somebody else's billing group.
//
// Without this the check is dead code under namespacing: removing it entirely
// broke no test, because no other case reaches it.
func TestEvenTheNamespacedGroupIsCheckedBeforeItIsTaken(t *testing.T) {
	setupGroupRatioProvisionTest(t)
	program := namespaceProgram(t)

	// Exactly the name provisioning would choose first.
	taken := customerPricingGroupName(program, "Nusa Labs", partnershipCodeFromName("Nusa Labs"))
	require.NoError(t, EnsurePartnershipGroupRatio(taken))
	before := currentGroupRatios(t)[taken]

	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", program.Id, "Nusa Labs").
		First(&customer).Error)
	assert.NotEqual(t, taken, customer.Group,
		"a group an operator already configured must not be adopted")
	assert.InDelta(t, before, currentGroupRatios(t)[taken], 1e-9,
		"and its price must be untouched")
}
