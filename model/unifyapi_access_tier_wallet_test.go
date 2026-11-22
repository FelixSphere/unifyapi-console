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

// What happened on production on 2026-11-22: `Vip User` is the group whose
// members may call a model the other groups are blocked from. Four logins that
// have nothing to do with each other were moved into it, and because a pricing
// group is a customer with one wallet, they were pooled into one balance.
//
// This file names nothing the fix adds: the tier list is seeded as the option
// row it is stored in, and everything else is the console's own "Users ->
// Create" and "Users -> Edit". It therefore compiles without the fix, and
// fails there on the assertions.

// seedAccessTiers stores the list the way the server keeps it.
func seedAccessTiers(t *testing.T, groups string) {
	t.Helper()
	require.NoError(t, DB.Create(&Option{Key: "AccessTierGroups", Value: groups}).Error)
}

// adminMoves is the console's "Users -> Edit" path with the group changed.
func adminMoves(t *testing.T, user *User, group string) {
	t.Helper()
	moved := &User{Id: user.Id, Username: user.Username, DisplayName: user.DisplayName, Group: group}
	require.NoError(t, moved.Edit(false))
	require.NoError(t, InvalidateBillingQuotaCacheForUser(user.Id))
}

func TestTwoLoginsMovedIntoAnAccessTierKeepTheirOwnWallets(t *testing.T) {
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User"]`)
	steven := adminCreates(t, "steven", "default")
	daniel := adminCreates(t, "daniel", "default")
	topUp(t, steven.Id, 77_965_000)
	topUp(t, daniel.Id, 2_500_000)
	stevenWallet, danielWallet := walletOf(t, steven.Id).TenantId, walletOf(t, daniel.Id).TenantId

	adminMoves(t, steven, "Vip User")
	adminMoves(t, daniel, "Vip User")

	assert.NotEqual(t, walletOf(t, steven.Id).TenantId, walletOf(t, daniel.Id).TenantId,
		"two strangers granted the same access do not become one customer")
	assert.Equal(t, stevenWallet, walletOf(t, steven.Id).TenantId, "each keeps the wallet it had")
	assert.Equal(t, danielWallet, walletOf(t, daniel.Id).TenantId)
	assert.Equal(t, 77_965_000, balanceOf(t, steven.Id), "and the balance in it")
	assert.Equal(t, 2_500_000, balanceOf(t, daniel.Id))

	var registered int64
	require.NoError(t, DB.Model(&CustomerWallet{}).Where("pricing_group = ?", "Vip User").Count(&registered).Error)
	assert.Zero(t, registered, "an access tier has no shared wallet to register")
}

func TestLoginsCreatedInAnAccessTierGetAWalletEach(t *testing.T) {
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User"]`)
	one := adminCreates(t, "one", "Vip User")
	two := adminCreates(t, "two", "Vip User")

	require.NotZero(t, walletOf(t, one.Id).TenantId)
	assert.NotEqual(t, walletOf(t, one.Id).TenantId, walletOf(t, two.Id).TenantId)

	topUp(t, one.Id, 700)
	assert.Equal(t, 700, balanceOf(t, one.Id))
	assert.Zero(t, balanceOf(t, two.Id), "a stranger's top-up is not yours")
}

func TestMovingBetweenDefaultAndAccessTiersMovesNoWalletAndNoBalance(t *testing.T) {
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User","Gold"]`)
	login := adminCreates(t, "steven", "default")
	other := adminCreates(t, "other", "Gold")
	topUp(t, login.Id, 1_000)
	topUp(t, other.Id, 50)
	wallet := walletOf(t, login.Id).TenantId
	before := totalCredit(t)
	var tenantsBefore int64
	require.NoError(t, DB.Model(&Tenant{}).Count(&tenantsBefore).Error)

	for _, group := range []string{"Vip User", "Gold", "default", "Gold", "Vip User", "default"} {
		adminMoves(t, login, group)
		assert.Equal(t, wallet, walletOf(t, login.Id).TenantId, "to %s", group)
		assert.Equal(t, 1_000, balanceOf(t, login.Id), "to %s", group)
	}

	assert.Equal(t, 50, balanceOf(t, other.Id), "the login already in the tier was not touched")
	assert.Equal(t, before, totalCredit(t), "no credit was minted or lost")
	var tenantsAfter int64
	require.NoError(t, DB.Model(&Tenant{}).Count(&tenantsAfter).Error)
	assert.Equal(t, tenantsBefore, tenantsAfter, "and no wallet was created along the way")
}

// customerToNonCustomer and nonCustomerToCustomer run the same move against
// `default` and against an access tier. The assertions are whatever `default`
// does: the tier must do the same, not something this file decided.
type walletMove struct {
	sharedWallet, loginWallet, loginBalance, sharedBalance int
	loginIsAloneInItsWallet                                bool
}

func leaveACustomerFor(t *testing.T, destination string) walletMove {
	t.Helper()
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User"]`)
	alice := adminCreates(t, "alice", "acme")
	bob := adminCreates(t, "bob", "acme")
	topUp(t, alice.Id, 1_000)
	shared := walletOf(t, bob.Id).TenantId

	adminMoves(t, alice, destination)

	var members int64
	require.NoError(t, DB.Model(&User{}).Where("tenant_id = ?", walletOf(t, alice.Id).TenantId).Count(&members).Error)
	return walletMove{
		sharedWallet: shared, loginWallet: walletOf(t, alice.Id).TenantId,
		loginBalance: balanceOf(t, alice.Id), sharedBalance: balanceOf(t, bob.Id),
		loginIsAloneInItsWallet: members == 1,
	}
}

func TestLeavingACustomerForAnAccessTierIsLeavingItForDefault(t *testing.T) {
	toDefault := leaveACustomerFor(t, "default")
	toTier := leaveACustomerFor(t, "Vip User")

	for name, move := range map[string]walletMove{"default": toDefault, "access tier": toTier} {
		assert.NotEqual(t, move.sharedWallet, move.loginWallet, "%s: the login leaves the customer's wallet", name)
		assert.True(t, move.loginIsAloneInItsWallet, "%s: for one of its own", name)
		assert.Zero(t, move.loginBalance, "%s: which starts empty", name)
		assert.Equal(t, 1_000, move.sharedBalance, "%s: the money was the customer's and stays with it", name)
	}
}

func joinACustomerFrom(t *testing.T, origin string) walletMove {
	t.Helper()
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User"]`)
	bob := adminCreates(t, "bob", "acme")
	topUp(t, bob.Id, 400)
	alice := adminCreates(t, "alice", origin)
	topUp(t, alice.Id, 1_000)
	before := totalCredit(t)

	adminMoves(t, alice, "acme")

	require.Equal(t, before, totalCredit(t), "no credit was minted or lost")
	return walletMove{
		sharedWallet: walletOf(t, bob.Id).TenantId, loginWallet: walletOf(t, alice.Id).TenantId,
		loginBalance: balanceOf(t, alice.Id), sharedBalance: balanceOf(t, bob.Id),
	}
}

func TestJoiningACustomerFromAnAccessTierIsJoiningItFromDefault(t *testing.T) {
	fromDefault := joinACustomerFrom(t, "default")
	fromTier := joinACustomerFrom(t, "Vip User")

	for name, move := range map[string]walletMove{"default": fromDefault, "access tier": fromTier} {
		assert.Equal(t, move.sharedWallet, move.loginWallet, "%s: the login bills as the customer", name)
		assert.Equal(t, 1_400, move.sharedBalance, "%s: and brought the wallet it had to itself along", name)
		assert.Equal(t, 1_400, move.loginBalance, name)
	}
}

func TestTheBackfillLeavesAnAccessTierUnpooled(t *testing.T) {
	setupCustomerWalletSpec(t)
	seedAccessTiers(t, `["Vip User"]`)
	steven := legacySoloWallet(t, "steven", "Vip User", 77_965_000)
	daniel := legacySoloWallet(t, "daniel", "Vip User", 2_500_000)

	_, err := BackfillCustomerPools(false)
	require.NoError(t, err)

	assert.NotEqual(t, walletOf(t, steven.Id).TenantId, walletOf(t, daniel.Id).TenantId)
	assert.Equal(t, 77_965_000, balanceOf(t, steven.Id))
	assert.Equal(t, 2_500_000, balanceOf(t, daniel.Id))
}

// A list that cannot be read is not an empty list. Treating it as one would
// pool the next login moved into a tier, so the move fails instead.
func TestAnUnreadableTierListStopsAGroupChangeInsteadOfPooling(t *testing.T) {
	setupCustomerWalletSpec(t)
	login := adminCreates(t, "steven", "default")
	topUp(t, login.Id, 1_000)
	wallet := walletOf(t, login.Id).TenantId
	seedAccessTiers(t, `{"Vip User":true}`)

	moved := &User{Id: login.Id, Username: login.Username, DisplayName: login.DisplayName, Group: "Vip User"}
	err := moved.Edit(false)

	require.Error(t, err)
	var stored User
	require.NoError(t, DB.First(&stored, login.Id).Error)
	assert.Equal(t, "default", stored.Group, "the group change was rolled back with it")
	assert.Equal(t, wallet, stored.TenantId)
}
