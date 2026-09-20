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

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The operator's "New User Quota" (QuotaForNewUser) is meant for every new
// login. Until 2026-09-20 a signup through a partnership code or over the
// Builder bridge started at zero -- the program grant was treated as the only
// signup credit, and a program with no grant left the user with nothing to
// spend. These tests pin that every signup path grants the credit, that it
// lands where the login actually spends from (its wallet, never the users
// column), and that a program grant is added on top rather than replacing it.

const fiveDollars = 2_500_000 // QuotaPerUnit is 500_000 per USD

func withSignupCredit(t *testing.T, quota int) {
	t.Helper()
	previous := common.QuotaForNewUser
	common.QuotaForNewUser = quota
	// RecordLog writes through the log handle; point it at the fixture's
	// database so the audit line can be read back.
	previousLogDB := LOG_DB
	LOG_DB = DB
	require.NoError(t, DB.AutoMigrate(&Log{}))
	t.Cleanup(func() {
		common.QuotaForNewUser = previous
		LOG_DB = previousLogDB
	})
}

func signupCreditLogs(t *testing.T, userId int) int64 {
	t.Helper()
	var n int64
	require.NoError(t, DB.Model(&Log{}).Where("user_id = ? AND type = ? AND content LIKE ?",
		userId, LogTypeSystem, "新用户注册赠送%").Count(&n).Error)
	return n
}

// A partnership code whose program grants nothing: the login must still get
// the ordinary signup credit, on its own wallet, with the audit line.
func TestAPartnershipSignupGetsTheOrdinarySignupCredit(t *testing.T) {
	setupPartnershipTestDB(t)
	withSignupCredit(t, fiveDollars)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "No grant", Code: "no-grant", Group: "partner", GrantQuota: 0, GrantLimit: 0, Enabled: true,
	}))

	user := &User{Username: "partner-newbie", Password: "password123", DisplayName: "Newbie", Role: 1, Status: 1}
	grant, err := user.InsertForPartnership("no-grant")
	require.NoError(t, err)
	assert.Zero(t, grant, "the program itself grants nothing")

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Zero(t, stored.Quota, "the column is not where a login spends from")
	require.NotZero(t, stored.TenantId)
	var wallet Tenant
	require.NoError(t, DB.First(&wallet, stored.TenantId).Error)
	assert.Equal(t, fiveDollars, wallet.Quota, "the signup credit is spendable")
	spendable, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, fiveDollars, spendable)
	assert.EqualValues(t, 1, signupCreditLogs(t, user.Id), "one audit line for the signup credit")
}

// A program grant is a team-level extra, not a replacement: the first member
// gets credit + grant, and once the grant is exhausted later members still get
// the credit.
func TestAProgramGrantComesOnTopOfTheSignupCredit(t *testing.T) {
	setupPartnershipTestDB(t)
	withSignupCredit(t, fiveDollars)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "One grant", Code: "one-grant", Group: "partner", GrantQuota: 5_000_000, GrantLimit: 1, Enabled: true,
	}))

	for i, want := range []int{fiveDollars + 5_000_000, fiveDollars} {
		user := &User{Username: "member" + string(rune('a'+i)), Password: "password123", DisplayName: "Member", Role: 1, Status: 1}
		_, err := user.InsertForPartnership("one-grant")
		require.NoError(t, err)
		spendable, err := GetUserQuota(user.Id, true)
		require.NoError(t, err)
		assert.Equal(t, want, spendable, "member %d", i)
	}
}

// With the credit switched off nothing changes: a partnership login with an
// exhausted program still starts at zero, exactly as before.
func TestNoSignupCreditMeansAPartnershipLoginStartsAtZero(t *testing.T) {
	setupPartnershipTestDB(t)
	withSignupCredit(t, 0)
	require.NoError(t, CreatePartnershipProgram(&PartnershipProgram{
		Name: "Off", Code: "off", Group: "partner", GrantQuota: 0, GrantLimit: 0, Enabled: true,
	}))
	user := &User{Username: "zero", Password: "password123", DisplayName: "Zero", Role: 1, Status: 1}
	_, err := user.InsertForPartnership("off")
	require.NoError(t, err)
	spendable, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Zero(t, spendable)
	assert.Zero(t, signupCreditLogs(t, user.Id), "no credit, no audit line")
}

// A member connecting over the Builder bridge is a new login too. Its credit
// is the team's: it goes into the shared team wallet, once per member.
func TestEveryBuilderMemberBringsTheSignupCreditToTheTeamWallet(t *testing.T) {
	program := setupTeamWalletTest(t)
	withSignupCredit(t, fiveDollars)

	alice := connectMember(t, program, "Nusa Labs", "credit-alice")
	require.NotZero(t, alice.TenantId)
	assert.Zero(t, alice.Quota, "the column stays empty; the team wallet holds the money")
	aliceSees, err := GetUserQuota(alice.Id, true)
	require.NoError(t, err)
	assert.Equal(t, fiveDollars, aliceSees, "the first member's credit is in the team wallet")
	assert.EqualValues(t, 1, signupCreditLogs(t, alice.Id))

	bob := connectMember(t, program, "Nusa Labs", "credit-bob")
	assert.Equal(t, alice.TenantId, bob.TenantId)
	bobSees, err := GetUserQuota(bob.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 2*fiveDollars, bobSees, "each member brings one credit to the one wallet")

	// Reconnecting the same subject is not a new login and brings nothing.
	again := connectMember(t, program, "Nusa Labs", "credit-alice")
	assert.Equal(t, alice.Id, again.Id)
	aliceSees, err = GetUserQuota(alice.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 2*fiveDollars, aliceSees, "a reconnect must not be paid twice")
	assert.EqualValues(t, 1, signupCreditLogs(t, alice.Id))
}
