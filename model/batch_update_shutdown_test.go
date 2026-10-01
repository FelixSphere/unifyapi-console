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
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupBatchShutdownTestDB gives each test its own database with
// BATCH_UPDATE_ENABLED=true, an empty batch store and a fresh shutdown latch,
// so the in-memory state one test leaves behind cannot satisfy another.
func setupBatchShutdownTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &Token{}, &Channel{}, &Log{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	previousDB, previousLogDB := DB, LOG_DB
	// RedisEnabled is left as TestMain set it (false): writing it here races
	// the cache goroutines DecreaseUserQuota spawns.
	require.False(t, common.RedisEnabled)
	previousBatch := common.BatchUpdateEnabled
	DB, LOG_DB = db, db
	common.BatchUpdateEnabled = true
	drainBatchUpdateStoresForTest()
	resetBatchUpdateShutdownForTest()
	t.Cleanup(func() {
		drainBatchUpdateStoresForTest()
		resetBatchUpdateShutdownForTest()
		DB, LOG_DB = previousDB, previousLogDB
		common.BatchUpdateEnabled = previousBatch
		// Dropping the last connection discards the shared-cache database, so a
		// rerun (-count=N) starts from empty tables.
		_ = sqlDB.Close()
	})
}

func drainBatchUpdateStoresForTest() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
}

func resetBatchUpdateShutdownForTest() {
	batchUpdateRunMu.Lock()
	defer batchUpdateRunMu.Unlock()
	batchUpdateShutdownOnce = sync.Once{}
	batchUpdateStopped = false
}

func pendingBatchRecords() int {
	n := 0
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		n += len(batchUpdateStores[i])
		batchUpdateLocks[i].Unlock()
	}
	return n
}

// The defect: with BATCH_UPDATE_ENABLED=true every deduction below is held in
// process memory until the next BATCH_UPDATE_INTERVAL tick. main.go's shutdown
// path never flushed it, so a restart dropped up to one interval of token,
// wallet and used-quota deductions while the consume log -- written
// synchronously -- kept them. Logs then exceed what the wallet was charged.
//
// This replays a request's billing writes with the scheduled loop never having
// ticked, runs the shutdown hook main.go calls after srv.Shutdown, and asserts
// the database matches the deductions that were made.
func TestShutdownFlushPersistsBatchedDeductions(t *testing.T) {
	setupBatchShutdownTestDB(t)

	// Untenanted: users.quota is this user's own wallet and is batched.
	solo := createTestUser(t, "batch-solo", 10_000)
	// Tenanted: tenants.quota is written synchronously, but used_quota on the
	// tenant, the member and the channel is batched.
	owner := createTestUser(t, "batch-owner", 10_000)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)

	token := &Token{UserId: solo.Id, Key: "batch-shutdown-token", Name: "t", RemainQuota: 5_000, Status: common.TokenStatusEnabled}
	require.NoError(t, DB.Create(token).Error)
	channel := &Channel{Name: "batch-shutdown-channel", Key: "k", Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(channel).Error)

	const soloCharge, ownerCharge = 700, 300

	// What a finished relay request does for the untenanted user.
	require.NoError(t, DecreaseUserQuota(solo.Id, soloCharge, false))
	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, soloCharge))
	UpdateUserUsedQuotaAndRequestCount(solo.Id, soloCharge)
	UpdateChannelUsedQuota(channel.Id, soloCharge)
	// And for the tenant member.
	require.NoError(t, DecreaseUserQuota(owner.Id, ownerCharge, false))
	UpdateUserUsedQuotaAndRequestCount(owner.Id, ownerCharge)
	UpdateChannelUsedQuota(channel.Id, ownerCharge)

	require.NotZero(t, pendingBatchRecords(), "precondition: deductions must be sitting in the batch store")

	FlushBatchUpdateOnShutdown()

	assert.Zero(t, pendingBatchRecords(), "shutdown must leave nothing unwritten in memory")

	var gotSolo User
	require.NoError(t, DB.First(&gotSolo, solo.Id).Error)
	assert.Equal(t, 10_000-soloCharge, gotSolo.Quota, "untenanted wallet must carry the deduction")
	assert.Equal(t, soloCharge, gotSolo.UsedQuota)
	assert.Equal(t, 1, gotSolo.RequestCount)

	var gotToken Token
	require.NoError(t, DB.First(&gotToken, token.Id).Error)
	assert.Equal(t, 5_000-soloCharge, gotToken.RemainQuota, "token limit must carry the deduction")
	assert.Equal(t, soloCharge, gotToken.UsedQuota)

	var gotChannel Channel
	require.NoError(t, DB.First(&gotChannel, channel.Id).Error)
	assert.Equal(t, int64(soloCharge+ownerCharge), gotChannel.UsedQuota)

	var gotOwner User
	require.NoError(t, DB.First(&gotOwner, owner.Id).Error)
	assert.Equal(t, ownerCharge, gotOwner.UsedQuota)
	assert.Equal(t, 1, gotOwner.RequestCount)

	var gotTenant Tenant
	require.NoError(t, DB.First(&gotTenant, tenant.Id).Error)
	assert.Equal(t, 10_000-ownerCharge, gotTenant.Quota, "tenant wallet is synchronous and must not be double-charged by the flush")
	assert.Equal(t, ownerCharge, gotTenant.UsedQuota)
}

// After the shutdown flush the scheduled loop must not run again: the process
// is about to exit, and a tick that swapped the stores out and was then killed
// mid-write would lose exactly what the flush was meant to save.
func TestShutdownFlushStopsScheduledLoopAndRunsOnce(t *testing.T) {
	setupBatchShutdownTestDB(t)
	solo := createTestUser(t, "batch-once", 1_000)

	require.NoError(t, DecreaseUserQuota(solo.Id, 100, false))
	FlushBatchUpdateOnShutdown()

	require.NoError(t, DecreaseUserQuota(solo.Id, 50, false))
	assert.False(t, runScheduledBatchUpdate(), "the loop must stop once shutdown has flushed")
	FlushBatchUpdateOnShutdown() // second call is a no-op, not a second flush

	var got User
	require.NoError(t, DB.First(&got, solo.Id).Error)
	assert.Equal(t, 900, got.Quota, "only the pre-shutdown deduction is flushed, exactly once")
	assert.Equal(t, 1, pendingBatchRecords())
}

// A scheduled tick that is mid-flush when shutdown arrives has already taken
// the stores; the shutdown flush must wait for it rather than return early and
// let main exit under it.
func TestShutdownFlushWaitsForInFlightScheduledFlush(t *testing.T) {
	setupBatchShutdownTestDB(t)
	solo := createTestUser(t, "batch-inflight", 1_000)
	require.NoError(t, DecreaseUserQuota(solo.Id, 200, false))

	batchUpdateRunMu.Lock() // a scheduled tick holds the run lock
	done := make(chan struct{})
	go func() {
		FlushBatchUpdateOnShutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("shutdown flush returned while a scheduled flush still held the run lock")
	default:
	}
	batchUpdate() // the tick finishes its write
	batchUpdateRunMu.Unlock()
	<-done

	var got User
	require.NoError(t, DB.First(&got, solo.Id).Error)
	assert.Equal(t, 800, got.Quota)
}

// With BATCH_UPDATE_ENABLED unset nothing is ever batched; the hook must be a
// harmless no-op so main.go can call it unconditionally.
func TestShutdownFlushIsNoopWhenBatchingDisabled(t *testing.T) {
	setupBatchShutdownTestDB(t)
	common.BatchUpdateEnabled = false
	solo := createTestUser(t, "batch-off", 1_000)
	require.NoError(t, DecreaseUserQuota(solo.Id, 10, false))

	FlushBatchUpdateOnShutdown()

	var got User
	require.NoError(t, DB.First(&got, solo.Id).Error)
	assert.Equal(t, 990, got.Quota, "the direct write happened once; the flush added nothing")
}
