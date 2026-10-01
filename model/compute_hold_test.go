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

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const computeTestNow = int64(1_757_520_000)

func setupComputeHoldTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &Token{}, &TopUp{}, &Log{}, &ComputeHold{}, &ComputeSettlement{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
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

func createComputeTestToken(t *testing.T, userId int, remain int, unlimited bool) *Token {
	t.Helper()
	token := &Token{
		UserId:         userId,
		Key:            fmt.Sprintf("computetestkey%d%d", userId, remain),
		Name:           "compute",
		Status:         common.TokenStatusEnabled,
		ExpiredTime:    -1,
		RemainQuota:    remain,
		UnlimitedQuota: unlimited,
	}
	require.NoError(t, DB.Create(token).Error)
	return token
}

func computeTestTokenRemain(t *testing.T, tokenId int) int {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, tokenId).Error)
	return token.RemainQuota
}

func computeTestTenantQuota(t *testing.T, tenantId int) int {
	t.Helper()
	quota, err := GetTenantQuota(tenantId)
	require.NoError(t, err)
	return quota
}

func computeTestUserColumn(t *testing.T, userId int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, userId).Error)
	return user.Quota
}

func newComputeTestHold(id string, user *User, token *Token, quota int) ComputeHold {
	return ComputeHold{
		HoldId:    id,
		UserId:    user.Id,
		TokenId:   token.Id,
		JobId:     "job-" + id,
		Sku:       "a10g-24gb-x1",
		Quota:     quota,
		ExpiresAt: computeTestNow + 3600,
	}
}

func TestComputeHoldDrawsOnTheTenantWalletNotTheMemberColumn(t *testing.T) {
	setupComputeHoldTestDB(t)
	_, member, tenant := createSharedBillingTenant(t, 10_000)
	token := createComputeTestToken(t, member.Id, 5_000, false)

	hold, replayed, err := CreateComputeHold(newComputeTestHold("h1", member, token, 1_200), token, computeTestNow)
	require.NoError(t, err)
	assert.False(t, replayed)
	assert.Equal(t, tenant.Id, hold.TenantId)
	assert.Equal(t, ComputeHoldStatusActive, hold.Status)
	assert.Equal(t, 8_800, computeTestTenantQuota(t, tenant.Id))
	assert.Equal(t, 0, computeTestUserColumn(t, member.Id))
	assert.Equal(t, 3_800, computeTestTokenRemain(t, token.Id))

	again, replayed, err := CreateComputeHold(newComputeTestHold("h1", member, token, 1_200), token, computeTestNow)
	require.NoError(t, err)
	assert.True(t, replayed)
	assert.Equal(t, 1_200, again.Quota)
	assert.Equal(t, 8_800, computeTestTenantQuota(t, tenant.Id), "a replayed hold must not reserve twice")
	assert.Equal(t, 3_800, computeTestTokenRemain(t, token.Id))
}

func TestComputeHoldOnAnUntenantedUserUsesTheirOwnBalance(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "solo", 2_000)
	require.Zero(t, user.TenantId)
	token := createComputeTestToken(t, user.Id, 0, true)

	hold, _, err := CreateComputeHold(newComputeTestHold("solo-hold", user, token, 500), token, computeTestNow)
	require.NoError(t, err)
	assert.Zero(t, hold.TenantId)
	assert.Equal(t, 1_500, computeTestUserColumn(t, user.Id))
	// An unlimited token is not checked but is still decremented, as the relay
	// does, so its used_quota stays truthful.
	assert.Equal(t, -500, computeTestTokenRemain(t, token.Id))
}

func TestComputeHoldRefusesToOverdrawAndLeavesNoRow(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "poor", 1_000)
	limited := createComputeTestToken(t, user.Id, 300, false)
	unlimited := createComputeTestToken(t, user.Id, 0, true)

	_, _, err := CreateComputeHold(newComputeTestHold("wallet-short", user, unlimited, 1_001), unlimited, computeTestNow)
	assert.ErrorIs(t, err, ErrInsufficientBillingQuota)
	_, _, err = CreateComputeHold(newComputeTestHold("token-short", user, limited, 301), limited, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeTokenQuotaShort)
	_, _, err = CreateComputeHold(newComputeTestHold("zero", user, unlimited, 0), unlimited, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeQuotaOutOfRange)

	var holds int64
	require.NoError(t, DB.Model(&ComputeHold{}).Count(&holds).Error)
	assert.Zero(t, holds)
	assert.Equal(t, 1_000, computeTestUserColumn(t, user.Id))
	assert.Equal(t, 300, computeTestTokenRemain(t, limited.Id))
}

func TestComputeHoldIdCannotBeReusedForAnotherReservation(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "reuse", 5_000)
	token := createComputeTestToken(t, user.Id, 0, true)
	_, _, err := CreateComputeHold(newComputeTestHold("same", user, token, 100), token, computeTestNow)
	require.NoError(t, err)

	other := newComputeTestHold("same", user, token, 100)
	other.JobId = "a-different-job"
	_, _, err = CreateComputeHold(other, token, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeHoldConflict)
	assert.Equal(t, 4_900, computeTestUserColumn(t, user.Id))
}

func TestComputeSettleChargesOnlyTheIncreaseOnceAndMovesNoBalance(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "settler", 10_000)
	token := createComputeTestToken(t, user.Id, 0, true)
	_, _, err := CreateComputeHold(newComputeTestHold("s", user, token, 1_000), token, computeTestNow)
	require.NoError(t, err)

	first, err := SettleComputeHold("s", ComputeSettlement{EventId: "e1", CumulativeQuota: 300, GpuSeconds: 60}, false, computeTestNow)
	require.NoError(t, err)
	assert.Equal(t, 300, first.Delta)
	assert.False(t, first.Replayed)

	replay, err := SettleComputeHold("s", ComputeSettlement{EventId: "e1", CumulativeQuota: 300, GpuSeconds: 60}, false, computeTestNow)
	require.NoError(t, err)
	assert.True(t, replay.Replayed)
	assert.Zero(t, replay.Delta)

	second, err := SettleComputeHold("s", ComputeSettlement{EventId: "e2", CumulativeQuota: 500, GpuSeconds: 100}, false, computeTestNow)
	require.NoError(t, err)
	assert.Equal(t, 200, second.Delta)
	assert.Equal(t, 500, second.Hold.SettledQuota)

	_, err = SettleComputeHold("s", ComputeSettlement{EventId: "e3", CumulativeQuota: 1_001}, false, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeHoldExceeded)
	_, err = SettleComputeHold("s", ComputeSettlement{EventId: "e4", CumulativeQuota: 499}, false, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeSettlementRegressed)

	// The hold already took the money; settling only records how much is spent.
	assert.Equal(t, 9_000, computeTestUserColumn(t, user.Id))
	var settlements int64
	require.NoError(t, DB.Model(&ComputeSettlement{}).Count(&settlements).Error)
	assert.Equal(t, int64(2), settlements, "refused events must not be recorded, so a corrected retry with the same id can apply")
}

func TestComputeSettlementEventIdBelongsToOneHold(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "events", 10_000)
	token := createComputeTestToken(t, user.Id, 0, true)
	for _, id := range []string{"a", "b"} {
		_, _, err := CreateComputeHold(newComputeTestHold(id, user, token, 1_000), token, computeTestNow)
		require.NoError(t, err)
	}
	_, err := SettleComputeHold("a", ComputeSettlement{EventId: "shared", CumulativeQuota: 10}, false, computeTestNow)
	require.NoError(t, err)
	_, err = SettleComputeHold("b", ComputeSettlement{EventId: "shared", CumulativeQuota: 10}, false, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeEventConflict)
}

func TestComputeFinalSettleReturnsTheRemainderAndClosesTheHold(t *testing.T) {
	setupComputeHoldTestDB(t)
	_, member, tenant := createSharedBillingTenant(t, 10_000)
	token := createComputeTestToken(t, member.Id, 5_000, false)
	_, _, err := CreateComputeHold(newComputeTestHold("f", member, token, 1_000), token, computeTestNow)
	require.NoError(t, err)

	result, err := SettleComputeHold("f", ComputeSettlement{EventId: "final", CumulativeQuota: 600}, true, computeTestNow)
	require.NoError(t, err)
	assert.Equal(t, 600, result.Delta)
	assert.Equal(t, 400, result.ReleasedQuota)
	assert.Equal(t, ComputeHoldStatusSettled, result.Hold.Status)
	assert.Equal(t, 9_400, computeTestTenantQuota(t, tenant.Id))
	assert.Equal(t, 4_400, computeTestTokenRemain(t, token.Id))

	released, err := ReleaseComputeHold("f", computeTestNow)
	require.NoError(t, err)
	assert.Equal(t, 400, released.ReleasedQuota, "release after a final settle reports what that settle returned")
	assert.Equal(t, 9_400, computeTestTenantQuota(t, tenant.Id))

	_, err = SettleComputeHold("f", ComputeSettlement{EventId: "late", CumulativeQuota: 700}, false, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeHoldClosed)
}

func TestComputeReleaseReturnsTheUnsettledRemainderOnce(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "releaser", 10_000)
	token := createComputeTestToken(t, user.Id, 5_000, false)
	_, _, err := CreateComputeHold(newComputeTestHold("r", user, token, 1_000), token, computeTestNow)
	require.NoError(t, err)
	_, err = SettleComputeHold("r", ComputeSettlement{EventId: "r1", CumulativeQuota: 250}, false, computeTestNow)
	require.NoError(t, err)

	for range 2 {
		released, err := ReleaseComputeHold("r", computeTestNow)
		require.NoError(t, err)
		assert.Equal(t, 750, released.ReleasedQuota)
		assert.Equal(t, ComputeHoldStatusReleased, released.Status)
	}
	assert.Equal(t, 9_750, computeTestUserColumn(t, user.Id))
	assert.Equal(t, 4_750, computeTestTokenRemain(t, token.Id))

	_, err = ReleaseComputeHold("missing", computeTestNow)
	assert.ErrorIs(t, err, ErrComputeHoldNotFound)
}

func TestComputeExtendReservesMoreOnTheSameTerms(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "extender", 2_000)
	token := createComputeTestToken(t, user.Id, 1_500, false)
	_, _, err := CreateComputeHold(newComputeTestHold("x", user, token, 1_000), token, computeTestNow)
	require.NoError(t, err)

	token.RemainQuota = computeTestTokenRemain(t, token.Id)
	extended, err := ExtendComputeHold("x", 300, token, computeTestNow)
	require.NoError(t, err)
	assert.Equal(t, 1_300, extended.Quota)
	assert.Equal(t, 700, computeTestUserColumn(t, user.Id))
	assert.Equal(t, 200, computeTestTokenRemain(t, token.Id))

	token.RemainQuota = computeTestTokenRemain(t, token.Id)
	_, err = ExtendComputeHold("x", 201, token, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeTokenQuotaShort)
	unlimited := createComputeTestToken(t, user.Id, 0, true)
	_, err = ExtendComputeHold("x", 100, unlimited, computeTestNow)
	assert.ErrorIs(t, err, ErrComputeHoldConflict, "an extend draws on the hold's own token")

	_, err = SettleComputeHold("x", ComputeSettlement{EventId: "x1", CumulativeQuota: 1_300}, false, computeTestNow)
	require.NoError(t, err, "the extended amount is settleable")
}

func TestComputeExtendCannotOverdrawTheWallet(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "overdraw", 1_000)
	token := createComputeTestToken(t, user.Id, 0, true)
	_, _, err := CreateComputeHold(newComputeTestHold("o", user, token, 900), token, computeTestNow)
	require.NoError(t, err)

	_, err = ExtendComputeHold("o", 101, token, computeTestNow)
	assert.ErrorIs(t, err, ErrInsufficientBillingQuota)
	hold, err := GetComputeHold("o")
	require.NoError(t, err)
	assert.Equal(t, 900, hold.Quota, "a refused extend must not grow the hold")
	assert.Equal(t, 100, computeTestUserColumn(t, user.Id))
}

func TestComputeExpiredHoldIsReturnedOnceAndReportedAsExpired(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "expiry", 10_000)
	token := createComputeTestToken(t, user.Id, 5_000, false)
	swept := newComputeTestHold("swept", user, token, 1_000)
	swept.ExpiresAt = computeTestNow + 10
	_, _, err := CreateComputeHold(swept, token, computeTestNow)
	require.NoError(t, err)
	_, err = SettleComputeHold("swept", ComputeSettlement{EventId: "sw1", CumulativeQuota: 100}, false, computeTestNow)
	require.NoError(t, err)
	token.RemainQuota = computeTestTokenRemain(t, token.Id)
	_, _, err = CreateComputeHold(newComputeTestHold("live", user, token, 500), token, computeTestNow)
	require.NoError(t, err)

	later := computeTestNow + 11
	closed, err := SweepExpiredComputeHolds(later, 100)
	require.NoError(t, err)
	assert.Equal(t, 1, closed)
	closed, err = SweepExpiredComputeHolds(later, 100)
	require.NoError(t, err)
	assert.Zero(t, closed, "a second sweep must not refund again")

	assert.Equal(t, 10_000-100-500, computeTestUserColumn(t, user.Id))
	assert.Equal(t, 5_000-100-500, computeTestTokenRemain(t, token.Id))

	_, err = SettleComputeHold("swept", ComputeSettlement{EventId: "sw2", CumulativeQuota: 200}, false, later)
	assert.ErrorIs(t, err, ErrComputeHoldExpired)
	_, err = ReleaseComputeHold("swept", later)
	assert.ErrorIs(t, err, ErrComputeHoldExpired)
}

func TestComputeLapsedHoldIsExpiredByTheCallThatFindsIt(t *testing.T) {
	setupComputeHoldTestDB(t)
	user := createTestUser(t, "lapsed", 10_000)
	token := createComputeTestToken(t, user.Id, 0, true)
	hold := newComputeTestHold("lapsed", user, token, 1_000)
	hold.ExpiresAt = computeTestNow + 10
	_, _, err := CreateComputeHold(hold, token, computeTestNow)
	require.NoError(t, err)

	_, err = SettleComputeHold("lapsed", ComputeSettlement{EventId: "l1", CumulativeQuota: 100}, false, computeTestNow+10)
	assert.ErrorIs(t, err, ErrComputeHoldExpired)
	assert.Equal(t, 10_000, computeTestUserColumn(t, user.Id), "the whole unsettled hold comes back")
	stored, err := GetComputeHold("lapsed")
	require.NoError(t, err)
	assert.Equal(t, ComputeHoldStatusExpired, stored.Status)
	assert.Equal(t, 1_000, stored.ReleasedQuota)
}
