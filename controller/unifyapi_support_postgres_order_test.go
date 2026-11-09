/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package controller

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Opt-in only. Every run creates and drops its own schema in a loopback test
// PostgreSQL; it never uses SQL_DSN or an application's existing support data.
func setupSupportPostgresOrderTest(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("UNIFYAPI_SUPPORT_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set UNIFYAPI_SUPPORT_POSTGRES_TEST_DSN to a disposable loopback PostgreSQL")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, u.Scheme)
	require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, u.Hostname(), "external/production databases are refused")
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := fmt.Sprintf("support_order_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec(`CREATE SCHEMA "`+schema+`"`).Error)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, admin.Exec(`DROP SCHEMA "`+schema+`" CASCADE`).Error)
		assert.NoError(t, adminSQL.Close())
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(6)
	previousDB, previousType := model.DB, common.MainDatabaseType()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	model.DB = db.WithContext(ctx)
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		cancel()
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		assert.NoError(t, sqlDB.Close())
	})
	require.NoError(t, model.MigrateSupport())
	return db
}

func TestSupportPostgresQueuedCustomerTurnRemainsNewestAfterAI(t *testing.T) {
	db := setupSupportPostgresOrderTest(t)
	ticket, err := model.CreateSupportTicket(17, "Synthetic lock-order regression", "Original question")
	require.NoError(t, err)
	initial, err := model.GetSupportMessages(ticket.Id)
	require.NoError(t, err)
	require.Len(t, initial, 1)

	// Pause the real AI transaction immediately after it holds the ticket row.
	// The following writer must actually block in PostgreSQL before AI resumes;
	// no sleep or assumed scheduler ordering creates the race.
	aiLocked := make(chan int, 1)
	resumeAI := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resumeAI) }) }
	defer release()
	var intercepted atomic.Bool
	callback := "test:support-ai-ticket-lock"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "support_tickets" || !intercepted.CompareAndSwap(false, true) {
			return
		}
		var pid int
		if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			tx.AddError(err)
			return
		}
		aiLocked <- pid
		select {
		case <-resumeAI:
		case <-tx.Statement.Context.Done():
			tx.AddError(tx.Statement.Context.Err())
		}
	}))
	t.Cleanup(func() { assert.NoError(t, db.Callback().Query().Remove(callback)) })
	type result struct {
		message *model.SupportMessage
		err     error
	}
	aiResult, customerResult := make(chan result, 1), make(chan result, 1)
	go func() {
		message, err := model.AddSupportAIMessage(ticket.Id, "Answer to the original question", false, initial[0].Id)
		aiResult <- result{message, err}
	}()
	var aiPID int
	select {
	case aiPID = <-aiLocked:
	case <-time.After(5 * time.Second):
		t.Fatal("AI transaction did not acquire its ticket lock")
	}
	go func() {
		message, err := model.AddSupportMessage(ticket.Id, 17, false, "New customer context while AI owns the row")
		customerResult <- result{message, err}
	}()
	require.Eventually(t, func() bool {
		var blocked int64
		err := db.Raw(`SELECT count(*) FROM pg_stat_activity
            WHERE wait_event_type = 'Lock' AND ? = ANY(pg_blocking_pids(pid))`, aiPID).Scan(&blocked).Error
		return err == nil && blocked > 0
	}, 5*time.Second, 5*time.Millisecond, "customer must be observed waiting on the actual AI row lock")
	release()
	var ai, customer result
	select {
	case ai = <-aiResult:
	case <-time.After(5 * time.Second):
		t.Fatal("AI transaction did not finish")
	}
	select {
	case customer = <-customerResult:
	case <-time.After(5 * time.Second):
		t.Fatal("queued customer transaction did not finish")
	}
	require.NoError(t, ai.err)
	require.NoError(t, customer.err)
	assert.Greater(t, customer.message.Id, ai.message.Id, "a queued customer turn must sort after the answer that completed first")
	messages, err := model.GetSupportMessages(ticket.Id)
	require.NoError(t, err)
	require.Len(t, messages, 3)
	assert.Equal(t, customer.message.Id, messages[len(messages)-1].Id)
	assert.False(t, messages[len(messages)-1].FromAI)
	after, err := model.GetSupportTicket(ticket.Id)
	require.NoError(t, err)
	assert.Equal(t, model.SupportStatusOpen, after.Status)
	unhandled, err := model.UnhandledSupportMessages(time.Now().Unix()+1, 10)
	require.NoError(t, err)
	require.Len(t, unhandled, 1, "the later customer turn must remain visible to the unanswered-message sweep")
	assert.Equal(t, customer.message.Id, unhandled[0].MessageId)
}

func TestSupportPostgresMissingTicketCannotReceiveAnOrphanMessage(t *testing.T) {
	db := setupSupportPostgresOrderTest(t)
	_, err := model.AddSupportMessage(999999, 17, false, "Must not become an orphan")
	require.ErrorIs(t, err, model.ErrSupportTicketNotFound)
	var count int64
	require.NoError(t, db.Model(&model.SupportMessage{}).Count(&count).Error)
	assert.Zero(t, count)
}
