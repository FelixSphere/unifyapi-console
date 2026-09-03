/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBillingEntityTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &TopUp{}, &Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// Multiple callers still race at the application boundary; one connection
	// keeps SQLite's in-memory locking behavior deterministic for the assertion.
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := DB, LOG_DB
	previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
	DB, LOG_DB = db, db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch
	})
}

func createSharedBillingTenant(t *testing.T, quota int) (*User, *User, *Tenant) {
	t.Helper()
	owner := createTestUser(t, "billing-owner", quota)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)
	member := createTestUser(t, "billing-member", 0)
	require.NoError(t, AddUserToTenant(member.Id, tenant.Id))
	return owner, member, tenant
}

func TestTenantMembersShareOneSpendableQuota(t *testing.T) {
	setupBillingEntityTestDB(t)
	owner, member, tenant := createSharedBillingTenant(t, 1_000)

	ownerQuota, err := GetUserQuota(owner.Id, false)
	require.NoError(t, err)
	memberQuota, err := GetUserQuota(member.Id, false)
	require.NoError(t, err)
	assert.Equal(t, 1_000, ownerQuota)
	assert.Equal(t, ownerQuota, memberQuota)

	require.NoError(t, DecreaseUserQuota(member.Id, 250, false))
	ownerQuota, err = GetUserQuota(owner.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 750, ownerQuota)

	require.NoError(t, IncreaseUserQuota(owner.Id, 125, true))
	memberQuota, err = GetUserQuota(member.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 875, memberQuota)

	var storedTenant Tenant
	require.NoError(t, DB.First(&storedTenant, tenant.Id).Error)
	assert.Equal(t, 875, storedTenant.Quota)
	var users []User
	require.NoError(t, DB.Where("id IN ?", []int{owner.Id, member.Id}).Find(&users).Error)
	for _, user := range users {
		assert.Zero(t, user.Quota, "tenant members must not keep a second wallet")
	}
}

func TestTenantMembersShareQuotaThroughRedisUserCache(t *testing.T) {
	setupBillingEntityTestDB(t)
	useUserCacheMiniRedis(t)
	owner, member, tenant := createSharedBillingTenant(t, 1_000)

	ownerCache, err := GetUserCache(owner.Id)
	require.NoError(t, err)
	memberCache, err := GetUserCache(member.Id)
	require.NoError(t, err)
	assert.Equal(t, tenant.Id, ownerCache.TenantId)
	assert.Equal(t, tenant.Id, memberCache.TenantId)
	assert.Equal(t, 1_000, ownerCache.Quota)
	assert.Equal(t, ownerCache.Quota, memberCache.Quota)

	require.NoError(t, TryDecreaseUserQuota(member.Id, 300))
	ownerCache, err = GetUserCache(owner.Id)
	require.NoError(t, err)
	memberCache, err = GetUserCache(member.Id)
	require.NoError(t, err)
	assert.Equal(t, 700, ownerCache.Quota)
	assert.Equal(t, ownerCache.Quota, memberCache.Quota)
}

func TestSharedQuotaReservationCannotOverdrawConcurrently(t *testing.T) {
	setupBillingEntityTestDB(t)
	owner, member, tenant := createSharedBillingTenant(t, 100)

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, userId := range []int{owner.Id, member.Id} {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			results <- TryDecreaseUserQuota(id, 80)
		}(userId)
	}
	wg.Wait()
	close(results)

	var success, insufficient int
	for err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrInsufficientBillingQuota):
			insufficient++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, insufficient)
	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 20, quota)
}

func TestFailedRequestRefundRestoresSharedQuota(t *testing.T) {
	setupBillingEntityTestDB(t)
	_, member, tenant := createSharedBillingTenant(t, 1_000)

	require.NoError(t, TryDecreaseUserQuota(member.Id, 400))
	require.NoError(t, IncreaseUserQuota(member.Id, 400, false))
	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, quota)
}

func TestUsageRemainsAttributedToMemberAndRollsUpToTenant(t *testing.T) {
	setupBillingEntityTestDB(t)
	owner, member, tenant := createSharedBillingTenant(t, 1_000)

	UpdateUserUsedQuotaAndRequestCount(member.Id, 75)

	var storedMember, storedOwner User
	require.NoError(t, DB.First(&storedMember, member.Id).Error)
	require.NoError(t, DB.First(&storedOwner, owner.Id).Error)
	assert.Equal(t, 75, storedMember.UsedQuota)
	assert.Equal(t, 1, storedMember.RequestCount)
	assert.Zero(t, storedOwner.UsedQuota)

	storedTenant, err := GetTenantById(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 75, storedTenant.UsedQuota)
}

func TestTenantlessUserKeepsUpstreamWalletBehavior(t *testing.T) {
	setupBillingEntityTestDB(t)
	user := createTestUser(t, "legacy-tenantless", 500)

	require.NoError(t, TryDecreaseUserQuota(user.Id, 200))
	require.NoError(t, IncreaseUserQuota(user.Id, 50, true))
	quota, err := GetUserQuota(user.Id, true)
	require.NoError(t, err)
	assert.Equal(t, 350, quota)

	var tenants int64
	require.NoError(t, DB.Model(&Tenant{}).Count(&tenants).Error)
	assert.Zero(t, tenants)
}

func TestStripeTopUpCreditsTenantAndSnapshotsOwnership(t *testing.T) {
	setupBillingEntityTestDB(t)
	_, member, tenant := createSharedBillingTenant(t, 1_000)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	topUp := &TopUp{
		UserId:          member.Id,
		Money:           2,
		TradeNo:         "stripe-shared-tenant",
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, topUp.Insert())
	assert.Equal(t, tenant.Id, topUp.TenantId)

	require.NoError(t, Recharge(topUp.TradeNo, "cus_shared", "127.0.0.1"))
	quota, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_001_000, quota)

	var storedMember User
	require.NoError(t, DB.First(&storedMember, member.Id).Error)
	assert.Zero(t, storedMember.Quota)
	assert.Equal(t, "cus_shared", storedMember.StripeCustomer)
}
