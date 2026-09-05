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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func submitTestContribution(t *testing.T, owner *User) *CreditContribution {
	t.Helper()
	contribution, err := CreateCreditContribution(owner.Id, CreateCreditContributionInput{
		Provider: "openai", AccountLabel: "Launch account", Models: "gpt-*",
		RequestedQuota: 1_000, RequestedAcquisitionRatio: 0.2, SupplierNotes: "Monthly grant",
		Attested: true,
	})
	require.NoError(t, err)
	return contribution
}

func TestCreditContributionSubmissionIsOwnerScopedAndRejectsSecrets(t *testing.T) {
	owner, member, tenant, _ := setupCreditPoolTest(t)

	contribution := submitTestContribution(t, owner)
	assert.Equal(t, tenant.Id, contribution.TenantId)
	assert.Equal(t, CreditContributionSubmitted, contribution.Status)
	assert.Equal(t, CreditContributionAttestationVersion, contribution.AttestationVersion)

	_, err := CreateCreditContribution(member.Id, CreateCreditContributionInput{
		Provider: "anthropic", RequestedQuota: 100, Attested: true,
	})
	assert.ErrorIs(t, err, ErrContributionOwnerRequired)

	_, err = CreateCreditContribution(owner.Id, CreateCreditContributionInput{
		Provider: "openai", RequestedQuota: 100, SupplierNotes: "key=sk-proj-do-not-store", Attested: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "credentials")
}

func TestCreditContributionResetPreservesHistoryAndPayoutCannotDoubleSpend(t *testing.T) {
	owner, _, tenant, pool := setupCreditPoolTest(t)
	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 7).Update("group", pool.RoutingGroup).Error)
	contribution := submitTestContribution(t, owner)

	active, err := ActivateCreditContribution(contribution.Id, 99, ActivateCreditContributionInput{
		PoolId: pool.Id, ChannelId: 7, ApprovedQuota: 1_000, AcquisitionRatio: 0.2,
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, active.Cycle)
	firstLotId := active.CurrentLotId

	grant := addTestCreditGrant(t, pool.Id, tenant.Id, 500)
	reservation, err := ReserveCreditPool("contribution-earned", owner.Id, grant.Id, pool.Id, 7, 200, 200, 1, 1)
	require.NoError(t, err)
	require.NoError(t, SettleCreditPoolReservation(reservation.Id, 7, 200, 200))

	reset, err := ResetCreditContribution(contribution.Id, 99, ResetCreditContributionInput{
		VerifiedQuota: 500, ExpiresAt: time.Now().Add(60 * 24 * time.Hour).Unix(), Reason: "Provider monthly credits renewed",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, reset.Cycle)
	assert.NotEqual(t, firstLotId, reset.CurrentLotId)

	var firstLot CreditPoolLot
	require.NoError(t, DB.First(&firstLot, firstLotId).Error)
	assert.Equal(t, CreditPoolStatusDisabled, firstLot.Status)
	assert.EqualValues(t, 800, firstLot.RemainingQuota, "reset closes rather than rewriting the old cycle")

	summaries, err := ListUserCreditContributions(owner.Id)
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.EqualValues(t, 500, summaries[0].InventoryRemaining)
	assert.EqualValues(t, 200, summaries[0].ConsumedQuota)
	assert.EqualValues(t, 40, summaries[0].LifetimePayableQuota)
	assert.EqualValues(t, 40, summaries[0].AvailablePayoutQuota)

	payout, err := CreateContributionPayout(contribution.Id, 99, 40, "September payout")
	require.NoError(t, err)
	assert.Equal(t, CreditPayoutDraft, payout.Status)
	_, err = CreateContributionPayout(contribution.Id, 99, 1, "must not exceed the remaining payable")
	assert.ErrorIs(t, err, ErrPayoutExceedsAvailable)
	require.NoError(t, UpdateContributionPayout(payout.Id, 99, CreditPayoutApproved, ""))
	require.NoError(t, UpdateContributionPayout(payout.Id, 99, CreditPayoutPaid, "wire-2026-09-001"))
	assert.ErrorIs(t, UpdateContributionPayout(payout.Id, 99, CreditPayoutVoid, ""), ErrContributionTransition)

	summaries, err = ListUserCreditContributions(owner.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 0, summaries[0].AvailablePayoutQuota)
	assert.EqualValues(t, 40, summaries[0].CommittedPayoutQuota)
}

func TestCreditContributionActivationRequiresPoolChannelAndRevokeStopsInventory(t *testing.T) {
	owner, _, _, pool := setupCreditPoolTest(t)
	contribution := submitTestContribution(t, owner)

	_, err := ActivateCreditContribution(contribution.Id, 99, ActivateCreditContributionInput{
		PoolId: pool.Id, ChannelId: 7, ApprovedQuota: 100, AcquisitionRatio: 0.3,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "routing group")

	require.NoError(t, DB.Model(&Channel{}).Where("id = ?", 7).Update("group", pool.RoutingGroup).Error)
	active, err := ActivateCreditContribution(contribution.Id, 99, ActivateCreditContributionInput{
		PoolId: pool.Id, ChannelId: 7, ApprovedQuota: 100, AcquisitionRatio: 0.3,
	})
	require.NoError(t, err)
	require.NoError(t, RevokeCreditContribution(active.Id, 99, "Supplier revoked provider access"))

	var lot CreditPoolLot
	require.NoError(t, DB.First(&lot, active.CurrentLotId).Error)
	assert.Equal(t, CreditPoolStatusDisabled, lot.Status)
	assert.ErrorIs(t, RevokeCreditContribution(active.Id, 99, "again"), ErrContributionTransition)
}

func TestCreditContributionListsOnlyTheCallersTenant(t *testing.T) {
	owner, _, tenant, _ := setupCreditPoolTest(t)
	submitTestContribution(t, owner)

	other := createTestUser(t, "other-credit-owner", 0)
	otherTenant, err := EnsureTenantForUser(other.Id)
	require.NoError(t, err)
	require.NotNil(t, otherTenant)
	submitTestContribution(t, other)

	ours, err := ListUserCreditContributions(owner.Id)
	require.NoError(t, err)
	require.Len(t, ours, 1)
	assert.Equal(t, tenant.Id, ours[0].TenantId)
	assert.NotEqual(t, otherTenant.Id, ours[0].TenantId)
}
