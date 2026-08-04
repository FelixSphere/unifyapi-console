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

func TestSuspendTenantDisablesMembers(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "susp-owner", 1000)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)
	member := createTestUser(t, "susp-member", 0)
	require.NoError(t, AddUserToTenant(member.Id, tenant.Id))

	require.NoError(t, SuspendTenant(tenant.Id, "non-payment"))

	reloaded, err := GetTenantById(tenant.Id)
	require.NoError(t, err)
	assert.True(t, reloaded.IsSuspended())
	assert.Equal(t, "non-payment", reloaded.SuspendReason)
	assert.NotZero(t, reloaded.SuspendedAt, "must record when access was cut")

	// The relay authorises from users.status, so that is what has to change --
	// flipping only the tenant row would suspend nothing.
	for _, id := range []int{owner.Id, member.Id} {
		var u User
		require.NoError(t, DB.First(&u, id).Error)
		assert.Equal(t, common.UserStatusDisabled, u.Status,
			"member %d must be disabled for suspension to take effect", id)
	}
}

func TestResumeTenantRestoresAccess(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "res-owner", 500)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)

	require.NoError(t, SuspendTenant(tenant.Id, "billing hold"))
	require.NoError(t, ResumeTenant(tenant.Id))

	reloaded, err := GetTenantById(tenant.Id)
	require.NoError(t, err)
	assert.False(t, reloaded.IsSuspended())
	assert.Zero(t, reloaded.SuspendedAt, "suspension metadata must be cleared")
	assert.Empty(t, reloaded.SuspendReason)

	var u User
	require.NoError(t, DB.First(&u, owner.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, u.Status)
}

func TestSuspendTenantLeavesOtherTenantsAlone(t *testing.T) {
	setupTenantTestDB(t)

	a := createTestUser(t, "iso-a", 100)
	b := createTestUser(t, "iso-b", 100)
	ta, err := EnsureTenantForUser(a.Id)
	require.NoError(t, err)
	_, err = EnsureTenantForUser(b.Id)
	require.NoError(t, err)

	require.NoError(t, SuspendTenant(ta.Id, "abuse"))

	var other User
	require.NoError(t, DB.First(&other, b.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, other.Status,
		"suspending one tenant must not touch another")
}

func TestSuspendTenantNeverDisablesAnOperator(t *testing.T) {
	setupTenantTestDB(t)

	owner := createTestUser(t, "mixed-owner", 100)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)

	// A member who was later promoted to admin. Suspending the tenant must not
	// lock an operator out of the console.
	promoted := createTestUser(t, "promoted", 0)
	require.NoError(t, AddUserToTenant(promoted.Id, tenant.Id))
	require.NoError(t, DB.Model(&User{}).Where("id = ?", promoted.Id).
		Update("role", common.RoleAdminUser).Error)

	require.NoError(t, SuspendTenant(tenant.Id, "test"))

	var op User
	require.NoError(t, DB.First(&op, promoted.Id).Error)
	assert.Equal(t, common.UserStatusEnabled, op.Status,
		"an operator must not be disabled by a tenant suspension")

	var normal User
	require.NoError(t, DB.First(&normal, owner.Id).Error)
	assert.Equal(t, common.UserStatusDisabled, normal.Status)
}

func TestExtendTenantTermFromOpenEnded(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "ext-1", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)
	assert.Zero(t, tenant.ExpiresAt, "new tenants are open-ended")

	before := common.GetTimestamp()
	expiry, err := ExtendTenantTerm(tenant.Id, 30)
	require.NoError(t, err)

	// Extending an unset term starts from now, so a renewal is a full term.
	assert.GreaterOrEqual(t, expiry, before+30*86400)
	assert.Less(t, expiry, before+31*86400)
}

func TestExtendTenantTermStacksOnFutureExpiry(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "ext-2", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	future := common.GetTimestamp() + 10*86400
	require.NoError(t, SetTenantExpiry(tenant.Id, future))

	expiry, err := ExtendTenantTerm(tenant.Id, 30)
	require.NoError(t, err)
	assert.Equal(t, future+30*86400, expiry,
		"extending a live term adds to it rather than restarting")
}

func TestExtendTenantTermFromLapsedTermStartsFromNow(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "ext-3", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	// A term that lapsed a year ago. Extending by 30 days must give 30 days from
	// today, not a date still in the past.
	lapsed := common.GetTimestamp() - 365*86400
	require.NoError(t, SetTenantExpiry(tenant.Id, lapsed))

	now := common.GetTimestamp()
	expiry, err := ExtendTenantTerm(tenant.Id, 30)
	require.NoError(t, err)
	assert.Greater(t, expiry, now, "a renewed term must be in the future")
	assert.GreaterOrEqual(t, expiry, now+30*86400)
}

func TestExtendTenantTermRejectsZero(t *testing.T) {
	setupTenantTestDB(t)
	user := createTestUser(t, "ext-4", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	_, err = ExtendTenantTerm(tenant.Id, 0)
	assert.Error(t, err)
}

func TestTenantIsExpired(t *testing.T) {
	now := int64(1_000_000)
	assert.False(t, (&Tenant{ExpiresAt: 0}).IsExpired(now), "0 means open-ended")
	assert.False(t, (&Tenant{ExpiresAt: now + 1}).IsExpired(now))
	assert.True(t, (&Tenant{ExpiresAt: now - 1}).IsExpired(now))
}

func TestGetTenantPaymentsScopedToTenant(t *testing.T) {
	setupTenantTestDB(t)
	require.NoError(t, DB.AutoMigrate(&TopUp{}))

	owner := createTestUser(t, "pay-owner", 0)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)
	member := createTestUser(t, "pay-member", 0)
	require.NoError(t, AddUserToTenant(member.Id, tenant.Id))

	outsider := createTestUser(t, "pay-outsider", 0)
	_, err = EnsureTenantForUser(outsider.Id)
	require.NoError(t, err)

	for _, tu := range []TopUp{
		{UserId: owner.Id, Amount: 100, Money: 10, TradeNo: "t1", Status: "success"},
		{UserId: member.Id, Amount: 50, Money: 5, TradeNo: "t2", Status: "success"},
		{UserId: outsider.Id, Amount: 999, Money: 99, TradeNo: "t3", Status: "success"},
	} {
		row := tu
		require.NoError(t, DB.Create(&row).Error)
	}

	payments, err := GetTenantPayments(tenant.Id, 0)
	require.NoError(t, err)
	require.Len(t, payments, 2, "both members' payments, and only theirs")

	// Newest first.
	assert.Equal(t, "t2", payments[0].TradeNo)
	assert.Equal(t, "t1", payments[1].TradeNo)
	assert.Equal(t, "pay-member", payments[0].Username)

	_, err = GetTenantPayments(0, 0)
	assert.Error(t, err)
}

func TestGetTenantAuditLogExcludesConsumption(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "audit-user", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	outsider := createTestUser(t, "audit-outsider", 0)
	_, err = EnsureTenantForUser(outsider.Id)
	require.NoError(t, err)

	for _, l := range []Log{
		{UserId: user.Id, Type: LogTypeLogin, Content: "logged in", CreatedAt: 100},
		{UserId: user.Id, Type: LogTypeManage, Content: "quota adjusted", CreatedAt: 200},
		{UserId: user.Id, Type: LogTypeTopup, Content: "topped up", CreatedAt: 300},
		// Consumption is billing detail, not an audit event -- thousands of these
		// would bury the trail.
		{UserId: user.Id, Type: LogTypeConsume, Content: "chat call", CreatedAt: 400},
		{UserId: outsider.Id, Type: LogTypeManage, Content: "other tenant", CreatedAt: 500},
	} {
		row := l
		require.NoError(t, DB.Create(&row).Error)
	}

	entries, err := GetTenantAuditLog(tenant.Id, 0)
	require.NoError(t, err)
	require.Len(t, entries, 3, "login/manage/topup only, scoped to this tenant")

	assert.Equal(t, "topped up", entries[0].Content, "newest first")
	for _, e := range entries {
		assert.NotEqual(t, LogTypeConsume, e.Type)
		assert.NotEqual(t, "other tenant", e.Content)
	}

	_, err = GetTenantAuditLog(0, 0)
	assert.Error(t, err)
}

func TestGetTenantAuditLogRespectsLimit(t *testing.T) {
	setupTenantTestDB(t)

	user := createTestUser(t, "audit-limit", 0)
	tenant, err := EnsureTenantForUser(user.Id)
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		row := Log{UserId: user.Id, Type: LogTypeManage, CreatedAt: int64(i)}
		require.NoError(t, DB.Create(&row).Error)
	}

	entries, err := GetTenantAuditLog(tenant.Id, 3)
	require.NoError(t, err)
	assert.Len(t, entries, 3)
}
