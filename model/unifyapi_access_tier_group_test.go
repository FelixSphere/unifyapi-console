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

// Marking a group as an access tier, and removing the mark. The wallet
// behaviour of a marked group is in unifyapi_access_tier_wallet_test.go; this
// file is about what the mark itself may and may not do to a group that
// already has logins and money.

func setupAccessTierTest(t *testing.T) {
	t.Helper()
	setupPricingGroupTest(t, map[string]string{
		"GroupRatio":       `{"default":1,"Vip User":1,"Gold":1,"Acme":0.9,"byok-acme":1}`,
		"UserUsableGroups": `{"default":"Default"}`,
	})
	// A group change revokes the login's sessions, and a new login is placed
	// in a wallet through the builder-identity lookup.
	require.NoError(t, DB.AutoMigrate(&UserSession{}, &BuilderIdentity{}))
}

// snapshotMoney is every balance and every login's wallet, to compare before
// and after an operation that must not move either.
func snapshotMoney(t *testing.T) map[string]int {
	t.Helper()
	money := map[string]int{}
	var tenants []Tenant
	require.NoError(t, DB.Find(&tenants).Error)
	for _, tenant := range tenants {
		money["balance of tenant "+tenant.Slug] = tenant.Quota
	}
	var users []User
	require.NoError(t, DB.Find(&users).Error)
	for _, user := range users {
		money["wallet of "+user.Username] = user.TenantId
		money["own quota of "+user.Username] = user.Quota
	}
	return money
}

func TestMarkingAGroupAsAnAccessTierStoresAndPublishesTheList(t *testing.T) {
	setupAccessTierTest(t)

	change, err := SetPricingGroupAccessTier(" Vip User ", true, "root")
	require.NoError(t, err)

	assert.Equal(t, "Vip User", change.Group)
	assert.True(t, change.AccessTier)
	assert.True(t, change.Changed)
	assert.Zero(t, change.ReleasedWalletTenant, "there was no shared wallet to release")
	assert.Equal(t, []string{"Vip User"}, change.Groups)
	assert.JSONEq(t, `["Vip User"]`, storedOption(t, AccessTierGroupsOptionKey))
	common.OptionMapRWMutex.RLock()
	published := common.OptionMap[AccessTierGroupsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	assert.JSONEq(t, `["Vip User"]`, published)

	listed, err := AccessTierGroups()
	require.NoError(t, err)
	assert.Equal(t, []string{"Vip User"}, listed)

	t.Run("a second group is added, not swapped in", func(t *testing.T) {
		change, err := SetPricingGroupAccessTier("Gold", true, "root")
		require.NoError(t, err)
		assert.Equal(t, []string{"Vip User", "Gold"}, change.Groups)
	})
	t.Run("marking a marked group changes nothing", func(t *testing.T) {
		change, err := SetPricingGroupAccessTier("Vip User", true, "root")
		require.NoError(t, err)
		assert.False(t, change.Changed)
		assert.Equal(t, []string{"Vip User", "Gold"}, change.Groups)
		assert.JSONEq(t, `["Vip User","Gold"]`, storedOption(t, AccessTierGroupsOptionKey))
	})
	t.Run("un-marking an unmarked group changes nothing", func(t *testing.T) {
		change, err := SetPricingGroupAccessTier("Acme", false, "root")
		require.NoError(t, err)
		assert.False(t, change.Changed)
		assert.JSONEq(t, `["Vip User","Gold"]`, storedOption(t, AccessTierGroupsOptionKey))
	})
}

func TestNoGroupIsAnAccessTierUntilOneIsMarked(t *testing.T) {
	setupAccessTierTest(t)

	listed, err := AccessTierGroups()
	require.NoError(t, err)
	assert.Empty(t, listed)
	assert.NotNil(t, listed, "the admin page is sent a list, not null")

	one := adminCreates(t, "one", "Vip User")
	two := adminCreates(t, "two", "Vip User")
	assert.Equal(t, walletOf(t, one.Id).TenantId, walletOf(t, two.Id).TenantId,
		"an unmarked group is a customer with one wallet, as before")
}

func TestGroupsThatCannotBeAnAccessTier(t *testing.T) {
	setupAccessTierTest(t)
	program := &PartnershipProgram{Name: "Builders", Code: "builders", Group: "Gold"}
	require.NoError(t, DB.Create(program).Error)
	require.NoError(t, DB.Create(&PartnershipCustomer{ProgramId: program.Id, Name: "Acme", Code: "acme", Group: "Acme"}).Error)

	for group, reason := range map[string]string{
		"":          "the group is required",
		"default":   "already gives every login its own wallet",
		"auto":      "is not a pricing group",
		"byok-acme": "one customer's own provider key",
		"Platinum":  "does not exist in Group Pricing",
		"Gold":      "belongs to a partnership program",
		"Acme":      "belongs to a partnership customer",
	} {
		_, err := SetPricingGroupAccessTier(group, true, "root")
		require.Error(t, err, group)
		assert.Contains(t, err.Error(), reason, group)
	}

	var stored int64
	require.NoError(t, DB.Model(&Option{}).Where(map[string]any{"key": AccessTierGroupsOptionKey}).Count(&stored).Error)
	assert.Zero(t, stored, "a refusal stores nothing")
}

// Production on 2026-11-22: the group's shared wallet had been emptied and its
// logins given wallets of their own by hand, leaving the registration pointing
// at a tenant nobody is in. One login in the group bills to a partnership
// team's wallet, which is none of this group's business.
func TestMarkingAGroupWhoseSharedWalletIsEmptyAndUnusedRemovesOnlyTheRegistration(t *testing.T) {
	setupAccessTierTest(t)
	abandoned := createWallet(t, "Vip User", 0)
	team := &Tenant{Name: "Nusa Labs", Slug: "team-nusa-labs", Status: TenantStatusEnabled, Quota: 4_200_000, Group: "builders/nusa"}
	require.NoError(t, DB.Create(team).Error)
	builder := createLogin(t, "builder_7ck4", "Vip User", team.Id)
	createLogin(t, "teammate", "builders/nusa", team.Id)
	before := snapshotMoney(t)

	change, err := SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)

	assert.Equal(t, abandoned.Id, change.ReleasedWalletTenant)
	var registered int64
	require.NoError(t, DB.Model(&CustomerWallet{}).Where("pricing_group = ?", "Vip User").Count(&registered).Error)
	assert.Zero(t, registered, "the group no longer has a shared wallet")
	assert.Equal(t, before, snapshotMoney(t), "no balance moved and no login changed wallet")
	var kept Tenant
	require.NoError(t, DB.First(&kept, abandoned.Id).Error, "the empty tenant is left for the record")

	t.Run("and the next logins moved in are not pooled", func(t *testing.T) {
		steven := adminCreates(t, "steven", "default")
		daniel := adminCreates(t, "daniel", "default")
		topUp(t, steven.Id, 77_965_000)
		adminMoves(t, steven, "Vip User")
		adminMoves(t, daniel, "Vip User")

		wallets := map[int]string{}
		for _, login := range []*User{builder, steven, daniel} {
			wallet := walletOf(t, login.Id).TenantId
			assert.NotEqual(t, abandoned.Id, wallet, login.Username)
			assert.Empty(t, wallets[wallet], "%s shares a wallet", login.Username)
			wallets[wallet] = login.Username
		}
		assert.Equal(t, 77_965_000, balanceOf(t, steven.Id))
		assert.Zero(t, balanceOf(t, daniel.Id))
	})
}

func TestMarkingAGroupWithOneLoginInItsWalletLeavesThatLoginItsWallet(t *testing.T) {
	setupAccessTierTest(t)
	wallet := createWallet(t, "Vip User", 15_000_000)
	hj := createLogin(t, "hj", "Vip User", wallet.Id)
	before := snapshotMoney(t)

	change, err := SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)

	assert.Equal(t, wallet.Id, change.ReleasedWalletTenant)
	assert.Equal(t, before, snapshotMoney(t))
	assert.Equal(t, wallet.Id, walletOf(t, hj.Id).TenantId, "the wallet is simply hj's now")
	assert.Equal(t, 15_000_000, balanceOf(t, hj.Id))

	newcomer := adminCreates(t, "newcomer", "Vip User")
	assert.NotEqual(t, wallet.Id, walletOf(t, newcomer.Id).TenantId, "and nobody else is placed in it")
	assert.Zero(t, balanceOf(t, newcomer.Id))
}

func TestMarkingRefusesAGroupWhoseWalletSeveralLoginsSpendFrom(t *testing.T) {
	setupAccessTierTest(t)
	wallet := createWallet(t, "Vip User", 80_465_000)
	createLogin(t, "steven", "Vip User", wallet.Id)
	createLogin(t, "daniel", "Vip User", wallet.Id)
	before := snapshotMoney(t)

	_, err := SetPricingGroupAccessTier("Vip User", true, "root")

	require.Error(t, err)
	for _, named := range []string{"tenant " + common.Interface2String(wallet.Id), "80465000", "2 logins", "steven, daniel"} {
		assert.Contains(t, err.Error(), named)
	}
	assert.Equal(t, before, snapshotMoney(t))
	var registered CustomerWallet
	require.NoError(t, DB.Where("pricing_group = ?", "Vip User").First(&registered).Error)
	assert.Equal(t, wallet.Id, registered.TenantId, "the registration is where it was")
	listed, err := AccessTierGroups()
	require.NoError(t, err)
	assert.Empty(t, listed, "and the group is not marked")
}

func TestMarkingRefusesAGroupWhoseEmptiedWalletStillHoldsABalance(t *testing.T) {
	setupAccessTierTest(t)
	wallet := createWallet(t, "Vip User", 1_250_000)

	_, err := SetPricingGroupAccessTier("Vip User", true, "root")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "holds a balance of 1250000 and has no login")
	var registered int64
	require.NoError(t, DB.Model(&CustomerWallet{}).Where("tenant_id = ?", wallet.Id).Count(&registered).Error)
	assert.EqualValues(t, 1, registered)
}

// A pool with no registration: two logins of the group left on one tenant by
// an earlier repair. The mark would not part them, so it is refused.
func TestMarkingRefusesAGroupInWhichTwoLoginsAlreadyShareAWallet(t *testing.T) {
	setupAccessTierTest(t)
	pooled := &Tenant{Name: "left over", Slug: "left-over", Status: TenantStatusEnabled, Quota: 500}
	require.NoError(t, DB.Create(pooled).Error)
	createLogin(t, "steven", "Vip User", pooled.Id)
	createLogin(t, "daniel", "Vip User", pooled.Id)

	_, err := SetPricingGroupAccessTier("Vip User", true, "root")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "2 logins of group \"Vip User\" spend from one wallet")
	assert.Contains(t, err.Error(), "steven, daniel")
	listed, listErr := AccessTierGroups()
	require.NoError(t, listErr)
	assert.Empty(t, listed)
}

func TestStaffInAGroupNeverStandInTheWayOfTheMark(t *testing.T) {
	setupAccessTierTest(t)
	root := createLogin(t, "yuchengwang", "Vip User", 0)
	require.NoError(t, DB.Model(root).Update("role", common.RoleRootUser).Error)
	admin := createLogin(t, "ops", "Vip User", 0)
	require.NoError(t, DB.Model(admin).Update("role", common.RoleAdminUser).Error)

	_, err := SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)
	_, err = SetPricingGroupAccessTier("Vip User", false, "root")
	require.NoError(t, err, "staff hold no wallet, so there is nothing to merge")
}

func TestRemovingTheMarkIsRefusedWhileTheTierHasLogins(t *testing.T) {
	setupAccessTierTest(t)
	_, err := SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)
	steven := adminCreates(t, "steven", "Vip User")
	daniel := adminCreates(t, "daniel", "Vip User")
	topUp(t, steven.Id, 1_000)
	before := snapshotMoney(t)

	_, err = SetPricingGroupAccessTier("Vip User", false, "root")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "still has 2 login(s): steven, daniel")
	assert.JSONEq(t, `["Vip User"]`, storedOption(t, AccessTierGroupsOptionKey))
	assert.Equal(t, before, snapshotMoney(t))

	t.Run("and goes through once they have been moved out", func(t *testing.T) {
		adminMoves(t, steven, "default")
		adminMoves(t, daniel, "default")

		change, err := SetPricingGroupAccessTier("Vip User", false, "root")
		require.NoError(t, err)
		assert.True(t, change.Changed)
		assert.Empty(t, change.Groups)
		assert.JSONEq(t, `[]`, storedOption(t, AccessTierGroupsOptionKey))
		assert.Equal(t, 1_000, balanceOf(t, steven.Id), "moving out took nothing")
	})
}

func TestAManyLoginRefusalNamesAFewAndCountsTheRest(t *testing.T) {
	assert.Equal(t, "a, b", listNames([]string{"a", "b"}))
	assert.Equal(t, "a, b, c, d, e and 2 more", listNames([]string{"a", "b", "c", "d", "e", "f", "g"}))
}

// The list decides whose money is whose, so it is only ever changed by the
// operation that checks what the change would do.
func TestTheTierListCannotBeSavedAsAPlainOption(t *testing.T) {
	setupAccessTierTest(t)
	wallet := createWallet(t, "Acme", 5_000)
	createLogin(t, "alice", "Acme", wallet.Id)
	createLogin(t, "bob", "Acme", wallet.Id)

	err := UpdateOption(AccessTierGroupsOptionKey, `["Acme"]`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark or un-mark a group as an access tier in Group Pricing")
	var stored int64
	require.NoError(t, DB.Model(&Option{}).Where(map[string]any{"key": AccessTierGroupsOptionKey}).Count(&stored).Error)
	assert.Zero(t, stored)
}

func TestRenamingAnAccessTierKeepsItAnAccessTier(t *testing.T) {
	setupAccessTierTest(t)
	_, err := SetPricingGroupAccessTier("Gold", true, "root")
	require.NoError(t, err)
	_, err = SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)
	steven := adminCreates(t, "steven", "Vip User")
	topUp(t, steven.Id, 1_000)
	before := snapshotMoney(t)

	_, err = RenamePricingGroup("Vip User", "Mythos Access", "root")
	require.NoError(t, err)

	assert.JSONEq(t, `["Gold","Mythos Access"]`, storedOption(t, AccessTierGroupsOptionKey),
		"the mark follows the group, in place")
	common.OptionMapRWMutex.RLock()
	published := common.OptionMap[AccessTierGroupsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	assert.JSONEq(t, `["Gold","Mythos Access"]`, published)
	assert.Equal(t, before, snapshotMoney(t))

	daniel := adminCreates(t, "daniel", "default")
	adminMoves(t, daniel, "Mythos Access")
	assert.NotEqual(t, walletOf(t, steven.Id).TenantId, walletOf(t, daniel.Id).TenantId,
		"a login moved into the renamed tier is still not pooled")

	t.Run("and the old name is an ordinary customer group again if it is reused", func(t *testing.T) {
		require.NoError(t, UpdateOption("GroupRatio", `{"default":1,"Mythos Access":1,"Gold":1,"Acme":0.9,"byok-acme":1,"Vip User":1}`))
		one := adminCreates(t, "one", "Vip User")
		two := adminCreates(t, "two", "Vip User")
		assert.Equal(t, walletOf(t, one.Id).TenantId, walletOf(t, two.Id).TenantId)
	})
}

// Deleting a group is saving Group Pricing without it. A mark left behind
// would apply to whatever group is next given the name.
func TestGroupPricingRefusesToDropAGroupStillMarkedAsAnAccessTier(t *testing.T) {
	setupAccessTierTest(t)
	_, err := SetPricingGroupAccessTier("Vip User", true, "root")
	require.NoError(t, err)
	const withoutTheTier = `{"default":1,"Gold":1,"Acme":0.9,"byok-acme":1}`

	err = UpdateOption("GroupRatio", withoutTheTier)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Vip User")
	assert.Contains(t, err.Error(), "its access tier mark (remove the mark in Group Pricing)")
	assert.Contains(t, storedOption(t, "GroupRatio"), "Vip User", "the save did not happen")

	_, err = SetPricingGroupAccessTier("Vip User", false, "root")
	require.NoError(t, err)
	require.NoError(t, UpdateOption("GroupRatio", withoutTheTier), "with the mark removed the group can go")
	assert.JSONEq(t, `[]`, storedOption(t, AccessTierGroupsOptionKey))
}

func TestAPartnershipProgramCannotUseAnAccessTierAsItsGroup(t *testing.T) {
	setupAccessTierTest(t)
	_, err := SetPricingGroupAccessTier("Gold", true, "root")
	require.NoError(t, err)

	err = CreatePartnershipProgram(&PartnershipProgram{
		Name: "Builder hub", Code: "builder-hub", Group: "Gold", GrantQuota: 5_000_000, GrantLimit: 50, Enabled: true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is an access tier")
}
