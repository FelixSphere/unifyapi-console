/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTeamWalletTest(t *testing.T) *PartnershipProgram {
	t.Helper()
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}))
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: 5000000, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	return program
}

func connectMember(t *testing.T, program *PartnershipProgram, team, subject string) *User {
	t.Helper()
	// The controller provisions an unknown team before connecting; mirror that
	// order here so the test exercises the same sequence production runs.
	require.NoError(t, ProvisionBuilderCustomer(program.Name, team))
	link, err := ConnectBuilderIdentityWithProgram(subject, subject+"@example.invalid",
		BuilderProgramSelector{ProgramName: program.Name, CustomerName: team}, "")
	require.NoError(t, err)
	var user User
	require.NoError(t, DB.First(&user, link.UserId).Error)
	return &user
}

// The point of the whole change: two people on the same team spend from one
// balance. Before this, each member had their own wallet and the team's credit
// was multiplied by however many people joined.
func TestTwoMembersOfATeamSpendFromOneWallet(t *testing.T) {
	program := setupTeamWalletTest(t)
	alice := connectMember(t, program, "Nusa Labs", "nusa-alice")
	bob := connectMember(t, program, "Nusa Labs", "nusa-bob")

	require.NotZero(t, alice.TenantId, "a team member must be on the team's tenant")
	assert.Equal(t, alice.TenantId, bob.TenantId, "both members of a team share one wallet")

	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", alice.TenantId).
		Update("quota", 5000000).Error)

	require.NoError(t, DecreaseUserQuota(alice.Id, 2000000, true))

	aliceSees, err := GetUserQuota(alice.Id, true)
	require.NoError(t, err)
	bobSees, err := GetUserQuota(bob.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 3000000, aliceSees)
	assert.Equal(t, 3000000, bobSees, "what one member spends must leave the team's balance for everyone")
}

// The other half, and the one that would be a billing disaster if it were
// wrong: two different teams must never draw on the same balance.
func TestTwoTeamsNeverShareAWallet(t *testing.T) {
	program := setupTeamWalletTest(t)
	nusa := connectMember(t, program, "Nusa Labs", "nusa-only")
	acme := connectMember(t, program, "Acme Robotics", "acme-only")

	require.NotZero(t, nusa.TenantId)
	require.NotZero(t, acme.TenantId)
	assert.NotEqual(t, nusa.TenantId, acme.TenantId,
		"two teams sharing a wallet would bill one team's usage to the other")

	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", nusa.TenantId).Update("quota", 5000000).Error)
	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", acme.TenantId).Update("quota", 5000000).Error)
	require.NoError(t, DecreaseUserQuota(nusa.Id, 2000000, true))

	acmeSees, err := GetUserQuota(acme.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5000000, acmeSees, "the other team's balance must be untouched")
}

// The team's wallet is created once and then reused. A second member joining
// must not quietly mint a second tenant and split the team's money in two.
func TestAThirdMemberJoinsTheSameWalletRatherThanMakingANewOne(t *testing.T) {
	program := setupTeamWalletTest(t)
	var tenantIds []int
	for i := range 3 {
		member := connectMember(t, program, "Nusa Labs", fmt.Sprintf("nusa-%d", i))
		tenantIds = append(tenantIds, member.TenantId)
	}
	for _, id := range tenantIds {
		assert.Equal(t, tenantIds[0], id, "every member of the team lands on the same wallet")
	}
	var tenants int64
	require.NoError(t, DB.Model(&Tenant{}).Count(&tenants).Error)
	assert.Equal(t, int64(1), tenants, "exactly one wallet was created for the team")
}

// The customer row is where the wallet is anchored, so the link has to be
// recorded there rather than rediscovered by guessing at names.
func TestTheTeamsWalletIsRecordedOnTheCustomer(t *testing.T) {
	program := setupTeamWalletTest(t)
	member := connectMember(t, program, "Nusa Labs", "nusa-anchor")

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", program.Id, "Nusa Labs").
		First(&customer).Error)
	assert.Equal(t, member.TenantId, customer.TenantId,
		"the customer must own the wallet its members spend from")
}

// Operators must not be swept into a team wallet. IsStaffRole accounts are
// refused from the bridge entirely, so this checks the guard still holds with
// a tenant in play.
func TestAnOperatorAccountIsStillRefusedATeamWallet(t *testing.T) {
	program := setupTeamWalletTest(t)
	staff := User{
		Username: "operator_1", Email: "operator@example.invalid",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AffCode: "opaf",
	}
	require.NoError(t, DB.Create(&staff).Error)

	_, err := ConnectBuilderIdentityWithProgram("staff-subject", "operator@example.invalid",
		BuilderProgramSelector{ProgramName: program.Name, CustomerName: "Nusa Labs"}, "")
	require.Error(t, err, "an operator account must not be linked into a team wallet")
}

// The program's default customer is a catch-all bucket, not a team: it holds
// everyone who connected without a team name, and those people have nothing to
// do with each other. They must keep their own wallets, or strangers would
// spend each other's credit.
func TestUsersWithNoTeamDoNotShareAWallet(t *testing.T) {
	program := setupTeamWalletTest(t)

	first, err := ConnectBuilderIdentity("loner-one", "loner-one@example.invalid", program.Code, "")
	require.NoError(t, err)
	second, err := ConnectBuilderIdentity("loner-two", "loner-two@example.invalid", program.Code, "")
	require.NoError(t, err)

	var one, two User
	require.NoError(t, DB.First(&one, first.UserId).Error)
	require.NoError(t, DB.First(&two, second.UserId).Error)

	assert.NotEqual(t, one.TenantId, two.TenantId,
		"two strangers in the default bucket must not share a wallet")

	require.NoError(t, IncreaseUserQuota(one.Id, 5000000, true))
	otherSees, err := GetUserQuota(two.Id, true)
	require.NoError(t, err)
	assert.Zero(t, otherSees, "one stranger's credit must not appear in another's balance")
}
