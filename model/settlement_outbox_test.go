/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupOutboxTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Tenant{}, &User{}, &Token{}, &Log{}, &SettlementLedger{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousLogDB := DB, LOG_DB
	DB, LOG_DB = db, db
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		_ = sqlDB.Close()
	})
}

// Which errors may be retried is the whole safety argument: a retry is only
// harmless if the database never ran the statement. Refusals at connect time
// qualify; a connection that died after the statement was sent does not, and
// retrying it could charge a customer twice.
func TestOnlyRefusalsBeforeTheStatementRanCountAsConnectionClass(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"postgres too many clients", fmt.Errorf("failed to connect: %w", &pgconn.PgError{Code: "53300"}), true},
		{"postgres starting up", &pgconn.PgError{Code: "57P03"}, true},
		{"postgres connect error", &pgconn.ConnectError{}, true},
		{"mysql too many connections", &mysql.MySQLError{Number: 1040}, true},
		{"driver bad conn", driver.ErrBadConn, true},
		{"connection refused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"dial timeout", &net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, true},

		{"nil", nil, false},
		{"postgres unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"postgres connection failure mid-statement", &pgconn.PgError{Code: "08006"}, false},
		{"connection reset after send", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, false},
		{"record not found", gorm.ErrRecordNotFound, false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, IsConnectionClassError(tc.err), tc.name)
	}
}

// A replay that committed but was never dequeued -- the process died between
// the two, or the Redis LREM failed -- is replayed again. The ledger must turn
// the second application into a no-op for the wallet and the token, and the
// existence check must do the same for the log row.
func TestReplayingTheSameSettlementTwiceAppliesItOnce(t *testing.T) {
	setupOutboxTestDB(t)
	owner := createTestUser(t, "outbox-owner", 0)
	tenant, err := EnsureTenantForUser(owner.Id)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Tenant{}).Where("id = ?", tenant.Id).Update("quota", 10_000).Error)
	token := &Token{UserId: owner.Id, Key: "outbox-key", Name: "t", RemainQuota: 5_000, Status: common.TokenStatusEnabled}
	require.NoError(t, DB.Create(token).Error)

	charge := &PendingSettlement{Key: "charge:req-1", RequestId: "req-1", UserId: owner.Id, WalletDelta: 700, TokenId: token.Id, TokenDelta: 700}
	logRow := &PendingSettlement{Key: "log:req-1", RequestId: "req-1", UserId: owner.Id, ResolveTenant: true,
		Log: &Log{UserId: owner.Id, Type: LogTypeConsume, Quota: 700, CreatedAt: 1_790_000_000, RequestId: "req-1", ModelName: "m"}}

	for range 3 {
		require.NoError(t, applyPendingSettlement(charge))
		require.NoError(t, applyPendingSettlement(logRow))
	}

	var gotTenant Tenant
	require.NoError(t, DB.First(&gotTenant, tenant.Id).Error)
	assert.Equal(t, 10_000-700, gotTenant.Quota, "the wallet is charged once, not once per replay")
	var gotToken Token
	require.NoError(t, DB.First(&gotToken, token.Id).Error)
	assert.Equal(t, 5_000-700, gotToken.RemainQuota)
	assert.Equal(t, 700, gotToken.UsedQuota)

	var logs []Log
	require.NoError(t, LOG_DB.Where("request_id = ?", "req-1").Find(&logs).Error)
	require.Len(t, logs, 1, "one invoice line, not one per replay")
	assert.Equal(t, tenant.Id, logs[0].TenantId, "the tenant is stamped at replay")
}

// Settlement moves money both ways: when the pre-consumed estimate was too
// high the difference is refunded. A refused refund parked and replayed must
// give the money back, not take more.
func TestAParkedRefundReturnsTheMoneyToAnUntenantedWallet(t *testing.T) {
	setupOutboxTestDB(t)
	solo := createTestUser(t, "outbox-solo", 1_000)

	require.NoError(t, applyPendingSettlement(&PendingSettlement{Key: "charge:req-2", RequestId: "req-2", UserId: solo.Id, WalletDelta: -300}))

	var got User
	require.NoError(t, DB.First(&got, solo.Id).Error)
	assert.Equal(t, 1_300, got.Quota)
}

// Two different requests are two charges: the ledger key is the request, not
// the user, so one customer's second request is never mistaken for a replay.
func TestDistinctRequestsAreDistinctCharges(t *testing.T) {
	setupOutboxTestDB(t)
	solo := createTestUser(t, "outbox-two", 1_000)

	require.NoError(t, applyPendingSettlement(&PendingSettlement{Key: "charge:req-a", RequestId: "req-a", UserId: solo.Id, WalletDelta: 100}))
	require.NoError(t, applyPendingSettlement(&PendingSettlement{Key: "charge:req-b", RequestId: "req-b", UserId: solo.Id, WalletDelta: 100}))

	var got User
	require.NoError(t, DB.First(&got, solo.Id).Error)
	assert.Equal(t, 800, got.Quota)
}

// Stock Postgres (100, 3 reserved) leaves this pool 77 and everything else 20.
// The floor keeps a tiny server usable rather than capping the pool at zero.
func TestPostgresPoolCeilingLeavesHeadroomBelowMaxConnections(t *testing.T) {
	assert.Equal(t, 77, postgresPoolCeiling(100, 3))
	assert.Equal(t, 477, postgresPoolCeiling(500, 3))
	assert.Equal(t, 10, postgresPoolCeiling(25, 3))
}
