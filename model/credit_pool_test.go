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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupCreditPoolTest(t *testing.T) (*User, *User, *Tenant, *CreditPool) {
	t.Helper()
	setupBillingEntityTestDB(t)
	require.NoError(t, DB.AutoMigrate(
		&Channel{},
		&CreditPool{}, &CreditPoolLot{}, &TenantCreditGrant{},
		&CreditPoolReservation{}, &CreditPoolReservationLot{},
		&CreditContribution{}, &CreditContributionEvent{}, &CreditContributionPayout{},
	))
	require.NoError(t, DB.Create(&Channel{Id: 7, Name: "pool-channel-7", Key: "test", Status: 1}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 8, Name: "pool-channel-8", Key: "test", Status: 1}).Error)
	owner, member, tenant := createSharedBillingTenant(t, 1_000)
	pool := &CreditPool{Name: "OpenAI launch pool", RoutingGroup: "promo-openai", Models: "gpt-*"}
	require.NoError(t, CreateCreditPool(pool))
	return owner, member, tenant, pool
}

func addTestCreditLot(t *testing.T, poolId, channelId int, quota int64, ratio float64) *CreditPoolLot {
	t.Helper()
	source := CreditPoolSourcePurchased
	if ratio == 0 {
		source = CreditPoolSourceFree
	}
	lot := &CreditPoolLot{
		PoolId: poolId, ChannelId: channelId, SourceType: source,
		Label: fmt.Sprintf("lot-%d", channelId), OriginalQuota: quota, AcquisitionRatio: ratio,
	}
	require.NoError(t, AddCreditPoolLot(lot))
	return lot
}

func addTestCreditGrant(t *testing.T, poolId, tenantId int, quota int64) *TenantCreditGrant {
	t.Helper()
	grant := &TenantCreditGrant{PoolId: poolId, TenantId: tenantId, Name: "Launch credit", OriginalQuota: quota}
	require.NoError(t, CreateTenantCreditGrant(grant))
	return grant
}

func TestCreditPoolRouteRequiresMatchingModelGrantAndInventory(t *testing.T) {
	owner, _, tenant, pool := setupCreditPoolTest(t)
	addTestCreditLot(t, pool.Id, 7, 1_000, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 500)

	route, err := ResolveCreditPoolRoute(owner.Id, "gpt-5")
	require.NoError(t, err)
	require.NotNil(t, route)
	assert.Equal(t, pool.Id, route.PoolId)
	assert.Equal(t, grant.Id, route.GrantId)
	assert.Equal(t, "promo-openai", route.RoutingGroup)

	route, err = ResolveCreditPoolRoute(owner.Id, "claude-opus")
	require.NoError(t, err)
	assert.Nil(t, route)
}

func TestCreditPoolReservationKeepsCashAndUsesTwoMeters(t *testing.T) {
	owner, _, tenant, pool := setupCreditPoolTest(t)
	lot := &CreditPoolLot{
		PoolId: pool.Id, ChannelId: 7, SourceType: CreditPoolSourceContributed,
		ContributorTenantId: tenant.Id, Label: "customer contribution", OriginalQuota: 1_000, AcquisitionRatio: 0.2,
	}
	require.NoError(t, AddCreditPoolLot(lot))
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 500)

	reservation, err := ReserveCreditPool("request-two-meters", owner.Id, grant.Id, pool.Id, 7, 100, 200, 0.5, 1)
	require.NoError(t, err)
	require.NoError(t, SettleCreditPoolReservation(reservation.Id, 7, 80, 160))

	var storedGrant TenantCreditGrant
	require.NoError(t, DB.First(&storedGrant, grant.Id).Error)
	assert.EqualValues(t, 420, storedGrant.RemainingQuota)
	var storedLot CreditPoolLot
	require.NoError(t, DB.First(&storedLot, lot.Id).Error)
	assert.EqualValues(t, 840, storedLot.RemainingQuota)
	cash, err := GetTenantQuota(tenant.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, cash, "promotional usage must not touch deposited cash")

	summaries, err := ListCreditPools()
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.InDelta(t, 32, summaries[0].AccruedPayableQuota, 0.001)
	lots, err := GetCreditPoolLots(pool.Id)
	require.NoError(t, err)
	require.Len(t, lots, 1)
	assert.Equal(t, tenant.Id, lots[0].ContributorTenantId)
	assert.InDelta(t, 32, lots[0].AccruedPayableQuota, 0.001)
}

func TestCreditPoolRefundIsAtomicAndIdempotent(t *testing.T) {
	_, member, tenant, pool := setupCreditPoolTest(t)
	lot := addTestCreditLot(t, pool.Id, 7, 1_000, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 500)
	reservation, err := ReserveCreditPool("request-refund", member.Id, grant.Id, pool.Id, 7, 100, 125, 0.8, 1)
	require.NoError(t, err)

	require.NoError(t, RefundCreditPoolReservation(reservation.Id))
	require.NoError(t, RefundCreditPoolReservation(reservation.Id))
	var storedGrant TenantCreditGrant
	require.NoError(t, DB.First(&storedGrant, grant.Id).Error)
	assert.EqualValues(t, 500, storedGrant.RemainingQuota)
	var storedLot CreditPoolLot
	require.NoError(t, DB.First(&storedLot, lot.Id).Error)
	assert.EqualValues(t, 1_000, storedLot.RemainingQuota)
}

func TestCreditPoolConcurrentMembersCannotOverdrawGrant(t *testing.T) {
	owner, member, tenant, pool := setupCreditPoolTest(t)
	addTestCreditLot(t, pool.Id, 7, 1_000, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 100)

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, userId := range []int{owner.Id, member.Id} {
		wg.Add(1)
		go func(index, id int) {
			defer wg.Done()
			_, err := ReserveCreditPool(fmt.Sprintf("concurrent-%d", index), id, grant.Id, pool.Id, 7, 80, 80, 1, 1)
			results <- err
		}(i, userId)
	}
	wg.Wait()
	close(results)

	var success, insufficient int
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrCreditGrantInsufficient) {
			insufficient++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, insufficient)
}

func TestCreditPoolRetryRebindsInventoryToSuccessfulChannel(t *testing.T) {
	owner, _, tenant, pool := setupCreditPoolTest(t)
	first := addTestCreditLot(t, pool.Id, 7, 100, 0)
	second := addTestCreditLot(t, pool.Id, 8, 100, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 100)
	reservation, err := ReserveCreditPool("request-rebind", owner.Id, grant.Id, pool.Id, 7, 50, 50, 1, 1)
	require.NoError(t, err)
	require.NoError(t, SettleCreditPoolReservationAtCost(reservation.Id, 8, 50, 25, 0.5))

	require.NoError(t, DB.First(first, first.Id).Error)
	require.NoError(t, DB.First(second, second.Id).Error)
	assert.EqualValues(t, 100, first.RemainingQuota)
	assert.EqualValues(t, 75, second.RemainingQuota)
	var stored CreditPoolReservation
	require.NoError(t, DB.First(&stored, reservation.Id).Error)
	assert.InDelta(t, 0.5, stored.ChannelCostRatio, 0.000001)
}

func TestContributedLotRequiresOwnerAndValidBuyRate(t *testing.T) {
	_, _, _, pool := setupCreditPoolTest(t)
	err := AddCreditPoolLot(&CreditPoolLot{PoolId: pool.Id, SourceType: CreditPoolSourceContributed, OriginalQuota: 100})
	require.Error(t, err)
	err = AddCreditPoolLot(&CreditPoolLot{PoolId: pool.Id, SourceType: CreditPoolSourcePurchased, OriginalQuota: 100, AcquisitionRatio: 1.2})
	require.Error(t, err)
}

func TestCreditPoolRequestIdCannotBeReusedForDifferentReservation(t *testing.T) {
	owner, member, tenant, pool := setupCreditPoolTest(t)
	addTestCreditLot(t, pool.Id, 7, 1_000, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 500)

	first, err := ReserveCreditPool("same-request", owner.Id, grant.Id, pool.Id, 7, 100, 100, 1, 1)
	require.NoError(t, err)
	second, err := ReserveCreditPool("same-request", owner.Id, grant.Id, pool.Id, 7, 100, 100, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, first.Id, second.Id)

	_, err = ReserveCreditPool("same-request", member.Id, grant.Id, pool.Id, 7, 100, 100, 1, 1)
	require.ErrorContains(t, err, "already used")
}

func TestCreditPoolSettlementOverrunStaysWithExhaustedLot(t *testing.T) {
	owner, _, tenant, pool := setupCreditPoolTest(t)
	lot := addTestCreditLot(t, pool.Id, 7, 100, 0)
	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 200)
	reservation, err := ReserveCreditPool("estimate-exhausts-lot", owner.Id, grant.Id, pool.Id, 7, 100, 100, 1, 1)
	require.NoError(t, err)
	require.NoError(t, SettleCreditPoolReservation(reservation.Id, 7, 120, 120))

	require.NoError(t, DB.First(lot, lot.Id).Error)
	assert.EqualValues(t, -20, lot.RemainingQuota)
	var allocation CreditPoolReservationLot
	require.NoError(t, DB.Where("reservation_id = ?", reservation.Id).First(&allocation).Error)
	assert.EqualValues(t, 120, allocation.Quota)

	// The relay settlement path is idempotent, but a second, different final
	// value must go through the explicit async adjustment API.
	require.NoError(t, SettleCreditPoolReservation(reservation.Id, 7, 120, 120))
	require.ErrorIs(t, SettleCreditPoolReservation(reservation.Id, 7, 130, 130), ErrCreditReservationClosed)
}
