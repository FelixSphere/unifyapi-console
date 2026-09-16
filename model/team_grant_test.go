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

func setupTeamGrantTest(t *testing.T) *PartnershipProgram {
	t.Helper()
	setupGroupRatioProvisionTest(t)
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}))
	quota, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
	require.NoError(t, err)
	program := &PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "partner",
		GrantQuota: quota, GrantLimit: 50, Enabled: true,
	}
	require.NoError(t, CreatePartnershipProgram(program))
	return program
}

func joinTeam(t *testing.T, program *PartnershipProgram, team, subject string) *BuilderIdentity {
	t.Helper()
	require.NoError(t, ProvisionBuilderCustomer(program.Name, team))
	link, err := ConnectBuilderIdentityWithProgram(subject, subject+"@example.invalid",
		BuilderProgramSelector{ProgramName: program.Name, CustomerName: team}, "")
	require.NoError(t, err)
	return link
}

// A claim has to resolve the same offer the connection did: by program name
// and team, not by the program's registration code, which resolves to the
// default customer instead.
func claimFor(t *testing.T, program *PartnershipProgram, team, subject string) error {
	t.Helper()
	return ClaimBuilderTeamGrantWithProgram(subject,
		BuilderProgramSelector{ProgramName: program.Name, CustomerName: team})
}

func teamBalance(t *testing.T, userId int) int {
	t.Helper()
	quota, err := GetUserQuota(userId, true)
	require.NoError(t, err)
	return quota
}

// The change asked for: a team's grant is one grant, however many people
// join. Before this, five members meant five times the money.
func TestATeamTakesTheGrantOnceHoweverManyJoin(t *testing.T) {
	program := setupTeamGrantTest(t)
	var links []*BuilderIdentity
	for i := range 4 {
		links = append(links, joinTeam(t, program, "Nusa Labs", fmt.Sprintf("nusa-%d", i)))
	}
	for _, link := range links {
		require.NoError(t, claimFor(t, program, "Nusa Labs", link.Subject))
	}

	granted, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
	require.NoError(t, err)
	assert.Equal(t, granted, teamBalance(t, links[0].UserId),
		"four members must not multiply the team's grant")

	var refreshed PartnershipProgram
	require.NoError(t, DB.First(&refreshed, program.Id).Error)
	assert.Equal(t, 1, refreshed.ClaimedCount,
		"the program's claim count now counts teams, so the limit caps teams")
}

// Each team still gets its own grant. The cap is on repeats within a team,
// not on teams.
func TestEachTeamStillGetsItsOwnGrant(t *testing.T) {
	program := setupTeamGrantTest(t)
	nusa := joinTeam(t, program, "Nusa Labs", "nusa-one")
	acme := joinTeam(t, program, "Acme Robotics", "acme-one")
	require.NoError(t, claimFor(t, program, "Nusa Labs", nusa.Subject))
	require.NoError(t, claimFor(t, program, "Acme Robotics", acme.Subject))

	granted, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
	require.NoError(t, err)
	assert.Equal(t, granted, teamBalance(t, nusa.UserId))
	assert.Equal(t, granted, teamBalance(t, acme.UserId))

	var refreshed PartnershipProgram
	require.NoError(t, DB.First(&refreshed, program.Id).Error)
	assert.Equal(t, 2, refreshed.ClaimedCount, "two teams, two claims")
}

// The claim is recorded on the customer, which is what makes it survive new
// members joining later rather than being rediscovered per person.
func TestTheClaimIsRecordedOnTheTeam(t *testing.T) {
	program := setupTeamGrantTest(t)
	link := joinTeam(t, program, "Nusa Labs", "nusa-record")
	require.NoError(t, claimFor(t, program, "Nusa Labs", link.Subject))

	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", program.Id, "Nusa Labs").
		First(&customer).Error)
	assert.Positive(t, customer.GrantClaimedAt, "the team's claim must be recorded on the team")
	granted, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
	require.NoError(t, err)
	assert.Equal(t, granted, customer.GrantedQuota)
}

// A member who joins after the team has claimed gets the team's existing
// balance, and brings nothing new in.
func TestAMemberJoiningAfterTheClaimAddsNothing(t *testing.T) {
	program := setupTeamGrantTest(t)
	first := joinTeam(t, program, "Nusa Labs", "nusa-first")
	require.NoError(t, claimFor(t, program, "Nusa Labs", first.Subject))
	before := teamBalance(t, first.UserId)

	late := joinTeam(t, program, "Nusa Labs", "nusa-late")
	require.NoError(t, claimFor(t, program, "Nusa Labs", late.Subject))

	assert.Equal(t, before, teamBalance(t, first.UserId), "the team's balance must not grow")
	assert.Equal(t, before, teamBalance(t, late.UserId), "and the late member draws on the same balance")
}

// Membership records are per member and must stay that way. The grant moved
// to the team; the record of who is in the team did not.
func TestEveryMemberStillHasItsOwnEnrollmentRow(t *testing.T) {
	program := setupTeamGrantTest(t)
	var links []*BuilderIdentity
	for i := range 3 {
		links = append(links, joinTeam(t, program, "Nusa Labs", fmt.Sprintf("nusa-row-%d", i)))
	}
	var enrollments []PartnershipEnrollment
	require.NoError(t, DB.Where("program_id = ?", program.Id).Find(&enrollments).Error)
	assert.Len(t, enrollments, len(links),
		"one enrollment per member: this is the membership record, not the grant")
}

// The default customer is a catch-all of unrelated people, not a team. If it
// were treated as one, the first stranger to register would take the grant and
// every stranger after them would get nothing.
func TestStrangersWithNoTeamEachGetTheirOwnGrant(t *testing.T) {
	program := setupTeamGrantTest(t)
	first, err := ConnectBuilderIdentity("loner-a", "loner-a@example.invalid", program.Code, "")
	require.NoError(t, err)
	second, err := ConnectBuilderIdentity("loner-b", "loner-b@example.invalid", program.Code, "")
	require.NoError(t, err)

	require.NoError(t, ClaimBuilderTeamGrant(first.Subject, program.Code))
	require.NoError(t, ClaimBuilderTeamGrant(second.Subject, program.Code))

	granted, err := common.QuotaFromFloatStrict(10 * common.QuotaPerUnit)
	require.NoError(t, err)
	assert.Equal(t, granted, teamBalance(t, first.UserId))
	assert.Equal(t, granted, teamBalance(t, second.UserId),
		"a stranger must not be denied the grant because another stranger took it")

	var refreshed PartnershipProgram
	require.NoError(t, DB.First(&refreshed, program.Id).Error)
	assert.Equal(t, 2, refreshed.ClaimedCount)
}
