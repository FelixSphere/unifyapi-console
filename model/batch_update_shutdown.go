/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// With BATCH_UPDATE_ENABLED=true, untenanted wallets (users.quota), token
// limits (tokens.remain_quota) and the used_quota/request_count counters on
// users, tenants and channels are held in memory until the next
// BATCH_UPDATE_INTERVAL tick. The consume log is written synchronously, so a
// process exit that skips the last tick leaves logs ahead of the wallet.
//
// batchUpdateRunMu serialises the scheduled tick against the shutdown flush:
// batchUpdate swaps the stores out before writing them, so a tick that is
// mid-write when shutdown arrives already holds deductions the shutdown flush
// cannot see. Waiting on the lock lets that tick finish before main exits.
var (
	batchUpdateRunMu        sync.Mutex
	batchUpdateShutdownOnce sync.Once
	batchUpdateStopped      bool // guarded by batchUpdateRunMu
)

// runScheduledBatchUpdate is one tick of the InitBatchUpdater loop. It reports
// false once shutdown has flushed, which ends the loop.
func runScheduledBatchUpdate() bool {
	batchUpdateRunMu.Lock()
	defer batchUpdateRunMu.Unlock()
	if batchUpdateStopped {
		return false
	}
	batchUpdate()
	return true
}

// FlushBatchUpdateOnShutdown writes every pending batched deduction and stops
// the scheduled loop. Call it after srv.Shutdown has drained in-flight
// requests, so deductions from requests that finished during the drain are
// included. Safe to call more than once and when batching is disabled (the
// stores are then empty and batchUpdate returns without touching the DB).
func FlushBatchUpdateOnShutdown() {
	batchUpdateShutdownOnce.Do(func() {
		batchUpdateRunMu.Lock()
		defer batchUpdateRunMu.Unlock()
		batchUpdateStopped = true
		if common.BatchUpdateEnabled {
			common.SysLog("flushing batched quota updates before exit")
		}
		batchUpdate()
	})
}
