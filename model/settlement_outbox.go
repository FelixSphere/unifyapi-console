/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// Settlement and the consume log run after the response has been written, so
// nothing about them can fail the request. Upstream logs their errors and
// moves on. Under connection exhaustion that loses money: staging, 2026-10-02,
// 200 RPS against Postgres max_connections=100 -- 372 of 35,525 served
// requests were never charged and 42 never logged, while the supplier billed
// all of them.
//
// Two layers, both limited to errors where the database provably did NOT run
// the statement (the connection was refused before anything was sent), so a
// retry can never apply a write twice:
//
//  1. retryConnectionClass: a short bounded retry in the request goroutine.
//     Exhaustion is usually momentary; a slot frees up within milliseconds.
//  2. The settlement outbox: if the retries run out, the pending charge or log
//     row is parked (a Redis list when Redis is on, process memory otherwise)
//     and a background worker replays it until the database accepts it.
//
// Exactly once: a replayed charge inserts a settlement_ledgers row keyed on
// the request id in the SAME transaction as the wallet and token update, so a
// replay that committed but was not dequeued (a crash, a Redis error) finds
// its key and does nothing. A replayed log row is checked for by request id,
// type, user, timestamp and quota before it is inserted.
//
// Ambiguous errors -- a connection that died after the statement was sent --
// are deliberately NOT retried or parked: the write may have happened, and
// replaying it could charge twice. They are logged as before.

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IsConnectionClassError reports whether err means the database refused or
// never received the statement, so retrying it cannot apply a write twice.
func IsConnectionClassError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "53300", // too_many_connections
			"57P03", // cannot_connect_now (starting up / shutting down)
			"08001", // sqlclient_unable_to_establish_sqlconnection
			"08004": // sqlserver_rejected_establishment_of_sqlconnection
			return true
		}
		return false
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return true
	}
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		// ER_CON_COUNT_ERROR, ER_TOO_MANY_USER_CONNECTIONS: refused at connect.
		return mysqlErr.Number == 1040 || mysqlErr.Number == 1203
	}
	// database/sql's contract: a driver returns ErrBadConn only when the
	// server cannot have performed the operation.
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	return false
}

// settlementRetryDelays is the wait before each retry. ~0.5s in total: long
// enough to outlast a momentary exhaustion, short enough not to pin request
// goroutines -- anything longer is the outbox's job.
var settlementRetryDelays = []time.Duration{25 * time.Millisecond, 100 * time.Millisecond, 400 * time.Millisecond}

func retryConnectionClass(fn func() error) error {
	err := fn()
	for _, delay := range settlementRetryDelays {
		if !IsConnectionClassError(err) {
			return err
		}
		time.Sleep(delay)
		err = fn()
	}
	return err
}

// SettlementLedger records each outbox charge that has been applied, so that
// a replay of the same request is a no-op. Only replayed charges are written
// here; the normal request path never touches it.
type SettlementLedger struct {
	Key         string `json:"key" gorm:"primaryKey;type:varchar(96)"`
	UserId      int    `json:"user_id" gorm:"index"`
	WalletDelta int    `json:"wallet_delta"`
	TokenId     int    `json:"token_id"`
	TokenDelta  int    `json:"token_delta"`
	EnqueuedAt  int64  `json:"enqueued_at"`
	AppliedAt   int64  `json:"applied_at"`
}

// PendingSettlement is one parked write: a charge (wallet and/or token delta)
// or a consume log row. It carries no secret: the token is named by id, and
// its cache by the HMAC the cache is already keyed on.
type PendingSettlement struct {
	Key         string `json:"key"`
	RequestId   string `json:"request_id"`
	UserId      int    `json:"user_id"`
	WalletDelta int    `json:"wallet_delta,omitempty"` // positive = charge the wallet
	TokenId     int    `json:"token_id,omitempty"`
	TokenDelta  int    `json:"token_delta,omitempty"` // positive = draw down the token
	TokenCache  string `json:"token_cache,omitempty"` // HMAC of the token key
	Log         *Log   `json:"log,omitempty"`
	// ResolveTenant: the wallet could not be looked up when the row was
	// built, so its tenant is stamped at replay instead of being written as 0
	// and dropping off the tenant's invoice.
	ResolveTenant bool   `json:"resolve_tenant,omitempty"`
	EnqueuedAt    int64  `json:"enqueued_at"`
	Cause         string `json:"cause,omitempty"`
}

const (
	settlementOutboxRedisKey     = "settlement:outbox"
	settlementOutboxDeadRedisKey = "settlement:outbox:dead"
	// Beyond this, memory parking stops and the item is written to the
	// process log in full so it can still be recovered by hand.
	settlementOutboxMemoryLimit = 50_000
	// Non-connection failures before an item is set aside for a human. A
	// connection-class failure never counts: the database is just still down.
	settlementOutboxMaxAttempts = 20
	settlementOutboxBatch       = 200
)

var settlementOutbox = struct {
	mu       sync.Mutex
	memory   []*PendingSettlement
	dead     []*PendingSettlement
	attempts map[string]int
	started  bool
	stopped  bool
	drainMu  sync.Mutex
}{attempts: map[string]int{}}

// ParkChargeSettlement parks a wallet and/or token adjustment the database
// refused. tokenKey is the raw token key; only its HMAC is stored.
func ParkChargeSettlement(requestId string, userId int, walletDelta int, tokenId int, tokenKey string, tokenDelta int, cause error) {
	if walletDelta == 0 && tokenDelta == 0 {
		return
	}
	if requestId == "" {
		requestId = common.NewRequestId()
	}
	kind := "charge"
	if walletDelta == 0 {
		kind = "token"
	}
	item := &PendingSettlement{
		Key:         kind + ":" + requestId,
		RequestId:   requestId,
		UserId:      userId,
		WalletDelta: walletDelta,
		TokenId:     tokenId,
		TokenDelta:  tokenDelta,
		EnqueuedAt:  common.GetTimestamp(),
		Cause:       errString(cause),
	}
	if tokenDelta != 0 && tokenKey != "" {
		item.TokenCache = common.GenerateHMAC(tokenKey)
	}
	parkSettlement(item)
}

func parkConsumeLog(log *Log, resolveTenant bool, cause error) {
	ensureLogRequestId(log)
	parkSettlement(&PendingSettlement{
		Key:           "log:" + log.RequestId,
		RequestId:     log.RequestId,
		UserId:        log.UserId,
		Log:           log,
		ResolveTenant: resolveTenant,
		EnqueuedAt:    common.GetTimestamp(),
		Cause:         errString(cause),
	})
}

// createConsumeLogDurably writes a consume log row, retrying a refused
// connection and parking the row if the database is still refusing. walletErr
// is the error from resolving the row's wallet: if the database refused that,
// the tenant is stamped at replay rather than written as 0.
func createConsumeLogDurably(log *Log, walletErr error) error {
	if walletErr != nil && IsConnectionClassError(walletErr) {
		parkConsumeLog(log, true, walletErr)
		return nil
	}
	ensureLogRequestId(log)
	err := retryConnectionClass(func() error { return createLog(log) })
	if err != nil && IsConnectionClassError(err) {
		parkConsumeLog(log, false, err)
		return nil
	}
	return err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func parkSettlement(item *PendingSettlement) {
	common.SysError(fmt.Sprintf("settlement outbox: parked %s (user %d, wallet %d, token %d): %s",
		item.Key, item.UserId, item.WalletDelta, item.TokenDelta, item.Cause))
	if common.RedisEnabled && common.RDB != nil {
		raw, err := common.Marshal(item)
		if err == nil {
			if err = common.RDB.RPush(context.Background(), settlementOutboxRedisKey, raw).Err(); err == nil {
				return
			}
		}
		common.SysError("settlement outbox: redis park failed, holding in memory: " + err.Error())
	}
	settlementOutbox.mu.Lock()
	defer settlementOutbox.mu.Unlock()
	if len(settlementOutbox.memory) >= settlementOutboxMemoryLimit {
		raw, _ := common.Marshal(item)
		common.SysError("settlement outbox: memory full, NOT PARKED, recover by hand: " + string(raw))
		return
	}
	settlementOutbox.memory = append(settlementOutbox.memory, item)
}

// errSettlementAlreadyApplied rolls the replay transaction back when its
// ledger key already exists: the charge was applied by an earlier replay.
var errSettlementAlreadyApplied = errors.New("settlement already applied")

// applyPendingSettlement replays one item. It is safe to call any number of
// times for the same item.
func applyPendingSettlement(item *PendingSettlement) error {
	if item.WalletDelta != 0 || item.TokenDelta != 0 {
		if err := applyPendingCharge(item); err != nil {
			return err
		}
	}
	if item.Log != nil {
		if err := applyPendingLog(item); err != nil {
			return err
		}
	}
	return nil
}

func applyPendingCharge(item *PendingSettlement) error {
	var entity BillingEntity
	walletTouched := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SettlementLedger{
			Key:         item.Key,
			UserId:      item.UserId,
			WalletDelta: item.WalletDelta,
			TokenId:     item.TokenId,
			TokenDelta:  item.TokenDelta,
			EnqueuedAt:  item.EnqueuedAt,
			AppliedAt:   common.GetTimestamp(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errSettlementAlreadyApplied
		}
		if item.WalletDelta != 0 {
			var err error
			if entity, err = adjustBillingQuotaWithTx(tx, item.UserId, -item.WalletDelta); err != nil {
				return err
			}
			walletTouched = true
		}
		if item.TokenDelta != 0 && item.TokenId > 0 {
			if err := tx.Model(&Token{}).Where("id = ?", item.TokenId).Updates(map[string]interface{}{
				"remain_quota":  gorm.Expr("remain_quota - ?", item.TokenDelta),
				"used_quota":    gorm.Expr("used_quota + ?", item.TokenDelta),
				"accessed_time": common.GetTimestamp(),
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, errSettlementAlreadyApplied) {
		return nil
	}
	if err != nil {
		return err
	}
	if walletTouched {
		_ = invalidateBillingQuotaCache(entity)
	}
	if item.TokenCache != "" && common.RedisEnabled {
		// The cached remaining quota never saw this charge; drop it so the
		// next read comes from the row just written.
		_ = common.RedisDelKey("token:" + item.TokenCache)
	}
	return nil
}

func applyPendingLog(item *PendingSettlement) error {
	log := *item.Log
	log.Id = 0
	if item.ResolveTenant {
		entity, err := resolveBillingEntity(DB, log.UserId)
		switch {
		case err == nil:
			log.TenantId = entity.TenantId
		case errors.Is(err, gorm.ErrRecordNotFound):
			log.TenantId = 0 // the login is gone; same as the live path
		default:
			return err
		}
	}
	var existing int64
	if err := LOG_DB.Model(&Log{}).
		Where("request_id = ? AND type = ? AND user_id = ? AND created_at = ? AND quota = ?",
			log.RequestId, log.Type, log.UserId, log.CreatedAt, log.Quota).
		Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	return createLog(&log)
}

// DrainSettlementOutbox replays every parked item once and reports how many
// were applied and how many are still waiting.
func DrainSettlementOutbox() (applied int, remaining int) {
	settlementOutbox.drainMu.Lock()
	defer settlementOutbox.drainMu.Unlock()

	a, r := drainMemoryOutbox()
	applied, remaining = applied+a, remaining+r
	// Every node parks into the same Redis list; only the master replays it,
	// so two nodes never race to insert the same log row.
	if common.RedisEnabled && common.RDB != nil && common.IsMasterNode {
		a, r = drainRedisOutbox()
		applied, remaining = applied+a, remaining+r
	}
	return applied, remaining
}

// recordReplayFailure reports whether the item should be set aside.
func recordReplayFailure(item *PendingSettlement, err error) bool {
	if IsConnectionClassError(err) {
		return false
	}
	settlementOutbox.mu.Lock()
	settlementOutbox.attempts[item.Key]++
	n := settlementOutbox.attempts[item.Key]
	settlementOutbox.mu.Unlock()
	if n < settlementOutboxMaxAttempts {
		return false
	}
	raw, _ := common.Marshal(item)
	common.SysError(fmt.Sprintf("settlement outbox: giving up on %s after %d attempts (%s), recover by hand: %s", item.Key, n, err.Error(), string(raw)))
	return true
}

func forgetAttempts(key string) {
	settlementOutbox.mu.Lock()
	delete(settlementOutbox.attempts, key)
	settlementOutbox.mu.Unlock()
}

func drainMemoryOutbox() (applied int, remaining int) {
	settlementOutbox.mu.Lock()
	batch := append([]*PendingSettlement(nil), settlementOutbox.memory...)
	settlementOutbox.mu.Unlock()
	if len(batch) == 0 {
		return 0, 0
	}
	done := make(map[*PendingSettlement]bool, len(batch))
	for _, item := range batch {
		err := applyPendingSettlement(item)
		if err == nil {
			done[item] = true
			applied++
			forgetAttempts(item.Key)
			continue
		}
		if recordReplayFailure(item, err) {
			done[item] = true
			settlementOutbox.mu.Lock()
			settlementOutbox.dead = append(settlementOutbox.dead, item)
			settlementOutbox.mu.Unlock()
		}
	}
	settlementOutbox.mu.Lock()
	kept := settlementOutbox.memory[:0]
	for _, item := range settlementOutbox.memory {
		if !done[item] {
			kept = append(kept, item)
		}
	}
	settlementOutbox.memory = kept
	remaining = len(kept)
	settlementOutbox.mu.Unlock()
	return applied, remaining
}

func drainRedisOutbox() (applied int, remaining int) {
	ctx := context.Background()
	raws, err := common.RDB.LRange(ctx, settlementOutboxRedisKey, 0, settlementOutboxBatch-1).Result()
	if err != nil {
		common.SysError("settlement outbox: redis read failed: " + err.Error())
		return 0, 0
	}
	for _, raw := range raws {
		var item PendingSettlement
		if err := common.UnmarshalJsonStr(raw, &item); err != nil {
			common.SysError("settlement outbox: unreadable item set aside: " + raw)
			_ = common.RDB.RPush(ctx, settlementOutboxDeadRedisKey, raw).Err()
			_ = common.RDB.LRem(ctx, settlementOutboxRedisKey, 1, raw).Err()
			continue
		}
		applyErr := applyPendingSettlement(&item)
		if applyErr != nil && !recordReplayFailure(&item, applyErr) {
			continue
		}
		if applyErr != nil {
			_ = common.RDB.RPush(ctx, settlementOutboxDeadRedisKey, raw).Err()
		} else {
			applied++
			forgetAttempts(item.Key)
		}
		// If this LREM fails the item is replayed later, which the ledger and
		// the log existence check make harmless.
		if err := common.RDB.LRem(ctx, settlementOutboxRedisKey, 1, raw).Err(); err != nil {
			common.SysError("settlement outbox: redis dequeue failed: " + err.Error())
		}
	}
	n, err := common.RDB.LLen(ctx, settlementOutboxRedisKey).Result()
	if err == nil {
		remaining = int(n)
	}
	return applied, remaining
}

// PendingSettlementCount is how many parked items are waiting in this
// process's memory (Redis-parked items are not counted).
func PendingSettlementCount() int {
	settlementOutbox.mu.Lock()
	defer settlementOutbox.mu.Unlock()
	return len(settlementOutbox.memory)
}

const settlementOutboxInterval = 5 * time.Second

// StartSettlementOutboxWorker replays parked settlements every few seconds.
// It also picks up whatever an earlier process left in Redis.
func StartSettlementOutboxWorker() {
	settlementOutbox.mu.Lock()
	if settlementOutbox.started {
		settlementOutbox.mu.Unlock()
		return
	}
	settlementOutbox.started = true
	settlementOutbox.mu.Unlock()
	gopool.Go(func() {
		for {
			time.Sleep(settlementOutboxInterval)
			settlementOutbox.mu.Lock()
			stopped := settlementOutbox.stopped
			settlementOutbox.mu.Unlock()
			if stopped {
				return
			}
			applied, remaining := DrainSettlementOutbox()
			if applied > 0 || remaining > 0 {
				common.SysLog(fmt.Sprintf("settlement outbox: applied %d, %d still waiting", applied, remaining))
			}
		}
	})
}

// FlushSettlementOutboxOnShutdown makes a last replay pass and then moves
// anything still waiting in memory somewhere that outlives the process: Redis
// if it is on, otherwise the process log, in full.
func FlushSettlementOutboxOnShutdown() {
	settlementOutbox.mu.Lock()
	settlementOutbox.stopped = true
	settlementOutbox.mu.Unlock()

	DrainSettlementOutbox()

	settlementOutbox.mu.Lock()
	left := settlementOutbox.memory
	settlementOutbox.memory = nil
	settlementOutbox.mu.Unlock()
	for _, item := range left {
		raw, err := common.Marshal(item)
		if err != nil {
			continue
		}
		if common.RedisEnabled && common.RDB != nil {
			if err := common.RDB.RPush(context.Background(), settlementOutboxRedisKey, raw).Err(); err == nil {
				continue
			}
		}
		common.SysError("settlement outbox: unapplied at exit, recover by hand: " + string(raw))
	}
}
