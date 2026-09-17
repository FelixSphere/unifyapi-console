/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package model

// The operator's definition of a customer, stated 2026-09-16 and tested here
// word for word:
//
//	one pricing group is one customer, one tenant. A customer has many
//	logins; they draw on one pool; every login's top-up lands in that pool.
//	The `default` group is the exception.
//
// Every test below goes through the paths an operator actually uses -- admin
// "create user", admin "edit user", a Stripe recharge -- not through the pool
// primitives, so a path that forgets the rule fails here.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCustomerWalletSpec(t *testing.T) {
	t.Helper()
	setupGroupRatioProvisionTest(t)
	// A group change revokes the login's sessions, so the table must exist.
	require.NoError(t, DB.AutoMigrate(&User{}, &Tenant{}, &BuilderIdentity{}, &Token{}, &UserSession{}))
}

// adminCreates is the console's "Users -> Create" path: model.User.Insert with
// a pricing group already chosen.
func adminCreates(t *testing.T, name, group string) *User {
	t.Helper()
	user := &User{
		Username: name, Password: "password123", DisplayName: name,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: group,
	}
	require.NoError(t, user.Insert(0))
	require.NoError(t, DB.First(user, user.Id).Error)
	return user
}

// walletOf is what every charge and every top-up resolves before it touches a
// balance. Two logins with the same wallet share money; two with different
// wallets do not, whatever the columns say.
func walletOf(t *testing.T, userId int) BillingEntity {
	t.Helper()
	entity, err := ResolveBillingEntity(userId)
	require.NoError(t, err)
	return entity
}

func balanceOf(t *testing.T, userId int) int {
	t.Helper()
	quota, err := GetUserQuota(userId, true)
	require.NoError(t, err)
	return quota
}

// topUp is what a completed Stripe/epay recharge does with the money.
func topUp(t *testing.T, userId, quota int) {
	t.Helper()
	_, err := IncreaseUserQuotaWithTx(DB, userId, quota)
	require.NoError(t, err)
	require.NoError(t, InvalidateBillingQuotaCacheForUser(userId))
}

func TestLoginsInOnePricingGroupDrawOnOneWallet(t *testing.T) {
	setupCustomerWalletSpec(t)
	alice := adminCreates(t, "alice", "acme")
	bob := adminCreates(t, "bob", "acme")

	a, b := walletOf(t, alice.Id), walletOf(t, bob.Id)
	require.True(t, a.IsTenant(), "a customer login must bill through a wallet, not its own column")
	assert.Equal(t, a.TenantId, b.TenantId, "same pricing group, same wallet")

	topUp(t, alice.Id, 1_000_000)
	assert.Equal(t, 1_000_000, balanceOf(t, bob.Id), "alice's top-up is bob's to spend")

	require.NoError(t, TryDecreaseUserQuota(bob.Id, 400_000))
	assert.Equal(t, 600_000, balanceOf(t, alice.Id), "bob's spend comes out of what alice sees")
}

func TestAPricingGroupHasExactlyOneWallet(t *testing.T) {
	setupCustomerWalletSpec(t)
	for _, name := range []string{"ann", "ben", "cai"} {
		adminCreates(t, name, "acme")
	}
	var wallets int64
	require.NoError(t, DB.Model(&User{}).Where(map[string]any{"group": "acme"}).
		Distinct("tenant_id").Count(&wallets).Error)
	assert.EqualValues(t, 1, wallets, "three logins, one customer, one wallet")
}

func TestEveryMembersTopUpLandsInTheCustomerWallet(t *testing.T) {
	setupCustomerWalletSpec(t)
	alice := adminCreates(t, "alice", "acme")
	bob := adminCreates(t, "bob", "acme")

	topUp(t, alice.Id, 300)
	topUp(t, bob.Id, 500)

	var wallet Tenant
	require.NoError(t, DB.First(&wallet, walletOf(t, alice.Id).TenantId).Error)
	assert.Equal(t, 800, wallet.Quota, "both recharges are in the one wallet")
	assert.Equal(t, 800, balanceOf(t, alice.Id))
	assert.Equal(t, 800, balanceOf(t, bob.Id))
}

func TestDefaultGroupLoginsKeepSeparateWallets(t *testing.T) {
	setupCustomerWalletSpec(t)
	one := adminCreates(t, "one", "default")
	two := adminCreates(t, "two", "default")

	assert.NotEqual(t, walletOf(t, one.Id).TenantId, walletOf(t, two.Id).TenantId,
		"default is not a customer; each login is its own")

	topUp(t, one.Id, 700)
	assert.Equal(t, 700, balanceOf(t, one.Id))
	assert.Zero(t, balanceOf(t, two.Id), "a stranger's top-up is not yours")
}

func TestAnOperatorInACustomerGroupStaysOutOfItsWallet(t *testing.T) {
	setupCustomerWalletSpec(t)
	customer := adminCreates(t, "hj", "vip")
	root := &User{
		Username: "root", Password: "password123", DisplayName: "root",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "vip",
	}
	require.NoError(t, root.Insert(0))

	assert.Zero(t, walletOf(t, root.Id).TenantId, "staff never hold a customer's money")
	topUp(t, customer.Id, 15_000_000)
	assert.Zero(t, balanceOf(t, root.Id), "and never see it as theirs")
}

func TestMovingALoginToAnotherPricingGroupMovesItOntoThatWallet(t *testing.T) {
	setupCustomerWalletSpec(t)
	alice := adminCreates(t, "alice", "acme")
	adminCreates(t, "bob", "acme")
	carol := adminCreates(t, "carol", "beta")
	topUp(t, alice.Id, 1_000)
	acmeWallet := walletOf(t, alice.Id).TenantId

	// The console's "Users -> Edit" path with the group changed.
	moved := &User{Id: alice.Id, Username: alice.Username, DisplayName: alice.DisplayName, Group: "beta"}
	require.NoError(t, moved.Edit(false))
	require.NoError(t, InvalidateBillingQuotaCacheForUser(alice.Id))

	assert.Equal(t, walletOf(t, carol.Id).TenantId, walletOf(t, alice.Id).TenantId,
		"alice now bills as a beta login")
	var acme Tenant
	require.NoError(t, DB.First(&acme, acmeWallet).Error)
	assert.Equal(t, 1_000, acme.Quota, "the money was acme's, not alice's; it stays with acme")
}

func TestAPartnershipTeamAndItsPricingGroupAreTheSameWallet(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Acme"))
	teamWallet := teamTenant(t, program.Id, "Acme")

	// A login the operator adds by hand to the team's pricing group is a
	// member of that customer, not a second customer with the same name.
	dan := adminCreates(t, "dan", "Acme")
	assert.Equal(t, teamWallet, walletOf(t, dan.Id).TenantId)
}

// legacySoloWallet is the shape every pre-existing customer login has in
// production: quota 0 in its column, its money in a tenant of its own.
func legacySoloWallet(t *testing.T, name, group string, quota int) *User {
	t.Helper()
	user := &User{
		Username: name, Password: "password123", DisplayName: name,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: group, AffCode: common.GetRandomString(6),
	}
	require.NoError(t, DB.Create(user).Error)
	solo := &Tenant{Name: name, Slug: slugFromName(name), Status: TenantStatusEnabled, OwnerId: user.Id, Quota: quota, Group: group}
	require.NoError(t, CreateTenantWithTx(DB, solo))
	require.NoError(t, DB.Model(user).Update("tenant_id", solo.Id).Error)
	return user
}

func TestTheBackfillReachesEveryNonDefaultPricingGroup(t *testing.T) {
	setupCustomerWalletSpec(t)
	ycw := legacySoloWallet(t, "ycwtest", "UnifyAI", 9_997_277)
	aaron := legacySoloWallet(t, "Aaron", "UnifyAI", 3_669_055)
	legacySoloWallet(t, "hj", "Vip User", 15_000_000)
	legacySoloWallet(t, "tiankong", "default", 5)
	before := totalCredit(t)

	dry, err := BackfillCustomerPools(true)
	require.NoError(t, err)
	assert.Equal(t, 2, dry.Customers, "UnifyAI and Vip User; default is not a customer")
	assert.Equal(t, 1, dry.MembersMoved, "only Aaron moves: ycwtest's and hj's wallets become their customers'")
	assert.Equal(t, 2, dry.AlreadyPooled)
	assert.Equal(t, 3_669_055, dry.QuotaCarried)
	assert.Equal(t, before, totalCredit(t), "a dry run moves nothing")

	_, err = BackfillCustomerPools(false)
	require.NoError(t, err)
	assert.Equal(t, ycw.TenantId, walletOf(t, aaron.Id).TenantId, "UnifyAI's wallet is the one it already had")
	assert.Equal(t, 9_997_277+3_669_055, balanceOf(t, ycw.Id), "UnifyAI's two balances are now one")
	assert.Equal(t, before, totalCredit(t), "not a unit of credit created or lost")
}

func TestAGroupsFirstLoginsWalletBecomesTheCustomers(t *testing.T) {
	setupCustomerWalletSpec(t)
	chris := legacySoloWallet(t, "Chris", "Chinhin", 24_722_289)
	dan := adminCreates(t, "dan", "Chinhin")

	assert.Equal(t, chris.TenantId, walletOf(t, dan.Id).TenantId, "dan joins the wallet Chris already had")
	assert.Equal(t, 24_722_289, balanceOf(t, dan.Id))
	var wallets int64
	require.NoError(t, DB.Model(&Tenant{}).Where(map[string]any{"group": "Chinhin"}).Count(&wallets).Error)
	assert.EqualValues(t, 1, wallets, "no second tenant was minted for the customer")
	var wallet Tenant
	require.NoError(t, DB.First(&wallet, chris.TenantId).Error)
	assert.Equal(t, "Chinhin", wallet.Name, "the wallet is named for the customer now, not its first login")
}

// A customer can have both kinds of member: one who joined through a
// partnership offer and one the operator created by hand. The dry run must
// count that customer once and promise exactly what the real run then moves.
func TestTheDryRunPromisesWhatTheRealRunMovesForAMixedCustomer(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Acme"))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", program.Id, "Acme").First(&customer).Error)
	enrolled := legacySoloWallet(t, "fay", "Acme", 2_000_000)
	require.NoError(t, DB.Create(&PartnershipEnrollment{
		ProgramId: program.Id, CustomerId: customer.Id, CustomerGroup: "Acme", UserId: enrolled.Id,
	}).Error)
	byHand := legacySoloWallet(t, "gus", "Acme", 3_000_000)
	before := totalCredit(t)

	dry, err := BackfillCustomerPools(true)
	require.NoError(t, err)
	assert.Equal(t, 1, dry.Customers, "one customer, however its members arrived")
	assert.Equal(t, before, totalCredit(t))

	done, err := BackfillCustomerPools(false)
	require.NoError(t, err)
	assert.Equal(t, dry.MembersMoved, done.MembersMoved, "the dry run promised what moved")
	assert.Equal(t, dry.QuotaCarried, done.QuotaCarried)
	assert.Equal(t, walletOf(t, enrolled.Id).TenantId, walletOf(t, byHand.Id).TenantId, "both draw on Acme's wallet")
	assert.Equal(t, 5_000_000, balanceOf(t, byHand.Id))
	assert.Equal(t, before, totalCredit(t), "not a unit of credit created or lost")
}
