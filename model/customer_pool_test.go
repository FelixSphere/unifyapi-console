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
	"gorm.io/gorm"
)

func setupPoolTest(t *testing.T) *PartnershipProgram {
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

// A customer's wallet is created the first time somebody joins it, so a team
// that has only been provisioned does not have one yet. Create it the same way
// the backfill does.
func teamTenant(t *testing.T, programId int, team string) int {
	t.Helper()
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("program_id = ? AND name = ?", programId, team).First(&customer).Error)
	if customer.TenantId != 0 {
		return customer.TenantId
	}
	var tenantId int
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		created, err := ensureCustomerTenant(tx, &PartnershipOffer{
			Program: PartnershipProgram{Id: customer.ProgramId}, CustomerId: customer.Id,
		})
		tenantId = created
		return err
	}))
	require.NotZero(t, tenantId)
	return tenantId
}

func makeUser(t *testing.T, name string, quota int) *User {
	t.Helper()
	user := &User{
		Username: name, Password: "password123", DisplayName: name,
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
		Group: "partner", Quota: quota, AffCode: common.GetRandomString(6),
	}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func totalCredit(t *testing.T) int {
	t.Helper()
	var userTotal, tenantTotal int
	require.NoError(t, DB.Model(&User{}).Select("COALESCE(SUM(quota),0)").Scan(&userTotal).Error)
	require.NoError(t, DB.Model(&Tenant{}).Select("COALESCE(SUM(quota),0)").Scan(&tenantTotal).Error)
	return userTotal + tenantTotal
}

// The property the whole migration stands on: moving people into a shared
// wallet must not create or destroy a single unit of credit.
func TestMovingIntoAPoolNeitherLosesNorMintsCredit(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	tenantId := teamTenant(t, program.Id, "Nusa Labs")
	require.NotZero(t, tenantId)

	makeUser(t, "alicea", 3000000)
	bob := makeUser(t, "bobbb", 0)
	solo := &Tenant{Name: "bob", Slug: "bob-solo", Status: TenantStatusEnabled, Quota: 2000000}
	require.NoError(t, DB.Create(solo).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", bob.Id).Update("tenant_id", solo.Id).Error)

	before := totalCredit(t)
	var alice User
	require.NoError(t, DB.Where("username = ?", "alicea").First(&alice).Error)
	require.NoError(t, JoinCustomerPoolTx(DB, alice.Id, tenantId))
	require.NoError(t, JoinCustomerPoolTx(DB, bob.Id, tenantId))

	assert.Equal(t, before, totalCredit(t), "the move must not create or destroy credit")

	var pool Tenant
	require.NoError(t, DB.First(&pool, tenantId).Error)
	assert.Equal(t, 5000000, pool.Quota, "both balances landed in the team's wallet")
}

// "保留" -- a member's own credit is carried into the pool, not discarded.
func TestAMembersOwnCreditIsCarriedIntoThePool(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	tenantId := teamTenant(t, program.Id, "Nusa Labs")

	user := makeUser(t, "carol", 1234567)
	require.NoError(t, JoinCustomerPoolTx(DB, user.Id, tenantId))

	sees, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 1234567, sees, "the member still has their money, now in the team's wallet")

	var row User
	require.NoError(t, DB.First(&row, user.Id).Error)
	assert.Zero(t, row.Quota, "the private column is emptied, not double counted")
}

// A solo tenant's balance is absorbed. This is the shape every existing
// Builder account has: quota 0 in the column, the money in its own tenant.
func TestASoloTenantsBalanceIsAbsorbed(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	tenantId := teamTenant(t, program.Id, "Nusa Labs")

	user := makeUser(t, "dave", 0)
	solo := &Tenant{Name: "dave", Slug: "dave-solo", Status: TenantStatusEnabled, Quota: 5000000}
	require.NoError(t, DB.Create(solo).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("tenant_id", solo.Id).Error)

	require.NoError(t, JoinCustomerPoolTx(DB, user.Id, tenantId))

	sees, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 5000000, sees)

	var emptied Tenant
	require.NoError(t, DB.First(&emptied, solo.Id).Error)
	assert.Zero(t, emptied.Quota, "the vacated tenant must not still claim the money")
}

// Refusing to merge two populated wallets. Doing it silently would move one
// customer's money into another customer's invoice.
func TestAPopulatedWalletIsNeverMergedIntoAnother(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Acme Robotics"))
	nusa := teamTenant(t, program.Id, "Nusa Labs")
	acme := teamTenant(t, program.Id, "Acme Robotics")

	first := makeUser(t, "eveee", 0)
	second := makeUser(t, "frank", 0)
	require.NoError(t, DB.Model(&User{}).Where("id IN ?", []int{first.Id, second.Id}).
		Update("tenant_id", acme).Error)
	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", acme).Update("quota", 9000000).Error)

	err := JoinCustomerPoolTx(DB, first.Id, nusa)
	require.ErrorIs(t, err, ErrAlreadyInAnotherPool)

	var untouched Tenant
	require.NoError(t, DB.First(&untouched, acme).Error)
	assert.Equal(t, 9000000, untouched.Quota, "the other customer's money must not move")
}

// The backfill is what the operator runs once. A dry run must report the same
// numbers without writing any of them.
func TestTheBackfillDryRunChangesNothing(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Nusa Labs").First(&customer).Error)

	for i := range 3 {
		user := makeUser(t, fmt.Sprintf("memb%d", i), 5000000)
		require.NoError(t, DB.Create(&PartnershipEnrollment{
			ProgramId: program.Id, CustomerId: customer.Id,
			CustomerGroup: customer.Group, UserId: user.Id,
		}).Error)
	}

	before := totalCredit(t)
	dry, err := BackfillCustomerPools(true)
	require.NoError(t, err)
	assert.Equal(t, 3, dry.MembersMoved)
	assert.Equal(t, 15000000, dry.QuotaCarried)
	assert.Equal(t, before, totalCredit(t), "a dry run must not move money")

	var stillPrivate User
	require.NoError(t, DB.Where("username = ?", "memb0").First(&stillPrivate).Error)
	assert.Equal(t, 5000000, stillPrivate.Quota, "a dry run must not empty the column either")

	// The wallet is created inside the same transaction the dry run rolls
	// back. Without the rollback this is the write that would escape, leaving
	// the customer holding a wallet the operator never agreed to create.
	var afterDry PartnershipCustomer
	require.NoError(t, DB.First(&afterDry, customer.Id).Error)
	assert.Zero(t, afterDry.TenantId, "a dry run must not create the customer's wallet")
	var tenants int64
	require.NoError(t, DB.Model(&Tenant{}).Count(&tenants).Error)
	assert.Zero(t, tenants, "a dry run must leave no tenant behind")
}

// And the real run moves exactly what the dry run promised.
func TestTheBackfillMovesWhatTheDryRunPromised(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Nusa Labs").First(&customer).Error)

	for i := range 3 {
		user := makeUser(t, fmt.Sprintf("real%d", i), 5000000)
		require.NoError(t, DB.Create(&PartnershipEnrollment{
			ProgramId: program.Id, CustomerId: customer.Id,
			CustomerGroup: customer.Group, UserId: user.Id,
		}).Error)
	}
	before := totalCredit(t)
	dry, err := BackfillCustomerPools(true)
	require.NoError(t, err)

	done, err := BackfillCustomerPools(false)
	require.NoError(t, err)
	assert.Equal(t, dry.MembersMoved, done.MembersMoved)
	assert.Equal(t, dry.QuotaCarried, done.QuotaCarried)
	assert.Equal(t, before, totalCredit(t), "the real run must not create or destroy credit either")

	var pool Tenant
	require.NoError(t, DB.First(&pool, teamTenant(t, program.Id, "Nusa Labs")).Error)
	assert.Equal(t, 15000000, pool.Quota, "all three members' credit is now one balance")
}

// Running it twice must not double anything.
func TestTheBackfillIsSafeToRunTwice(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	var customer PartnershipCustomer
	require.NoError(t, DB.Where("name = ?", "Nusa Labs").First(&customer).Error)
	user := makeUser(t, "twice", 5000000)
	require.NoError(t, DB.Create(&PartnershipEnrollment{
		ProgramId: program.Id, CustomerId: customer.Id,
		CustomerGroup: customer.Group, UserId: user.Id,
	}).Error)

	_, err := BackfillCustomerPools(false)
	require.NoError(t, err)
	after := totalCredit(t)

	second, err := BackfillCustomerPools(false)
	require.NoError(t, err)
	assert.Equal(t, after, totalCredit(t), "a second run must not duplicate credit")
	assert.Equal(t, 1, second.AlreadyPooled)
	assert.Zero(t, second.MembersMoved)
}

// A customer's balance appears on every member's row, so without this the
// table reads as the team's credit multiplied by its headcount.
func TestASharedBalanceSaysWhoseItIs(t *testing.T) {
	program := setupPoolTest(t)
	require.NoError(t, ProvisionBuilderCustomer(program.Name, "Nusa Labs"))
	tenantId := teamTenant(t, program.Id, "Nusa Labs")
	for _, name := range []string{"sharea", "shareb", "sharec"} {
		user := makeUser(t, name, 0)
		require.NoError(t, JoinCustomerPoolTx(DB, user.Id, tenantId))
	}
	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", tenantId).Update("quota", 5000000).Error)

	var listed []*User
	require.NoError(t, DB.Where("tenant_id = ?", tenantId).Find(&listed).Error)
	require.NoError(t, FillEffectiveQuotas(listed))

	require.Len(t, listed, 3)
	for _, user := range listed {
		assert.Equal(t, 5000000, user.Quota, "every member sees the customer's balance")
		assert.Equal(t, "Nusa Labs", user.SharedWallet,
			"and the row must say the balance is the customer's, not this member's")
		assert.Equal(t, 3, user.SharedWalletMembers)
	}
}

// A person with a wallet to themselves is not sharing anything, and saying so
// would be noise on every ordinary row.
func TestASoleOccupantIsNotMarkedAsSharing(t *testing.T) {
	setupPoolTest(t)
	user := makeUser(t, "alone", 0)
	solo := &Tenant{Name: "alone", Slug: "alone-solo", Status: TenantStatusEnabled, Quota: 5000000}
	require.NoError(t, DB.Create(solo).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("tenant_id", solo.Id).Error)

	var listed []*User
	require.NoError(t, DB.Where("id = ?", user.Id).Find(&listed).Error)
	require.NoError(t, FillEffectiveQuotas(listed))

	assert.Equal(t, 5000000, listed[0].Quota)
	assert.Empty(t, listed[0].SharedWallet, "a wallet with one member is not shared")
	assert.Zero(t, listed[0].SharedWalletMembers)
}
