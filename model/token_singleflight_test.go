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
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTokenSingleflightDB gives the test its own database and a counter of
// the SELECTs that actually reach it, plus a gate that holds the first query
// open so concurrency is arranged rather than hoped for.
func setupTokenSingleflightDB(t *testing.T) (queries *atomic.Int64, openGate func(), started <-chan struct{}) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Token{}))

	var count atomic.Int64
	gate := make(chan struct{})
	first := make(chan struct{})
	var once sync.Once

	require.NoError(t, db.Callback().Query().Before("gorm:query").
		Register("test:count_and_hold", func(tx *gorm.DB) {
			count.Add(1)
			// Hold only the FIRST query open. Later ones, if the collapse is
			// broken and they happen at all, run freely -- so a broken
			// implementation fails on the count rather than deadlocking.
			once.Do(func() {
				close(first)
				<-gate
			})
		}))

	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })

	return &count, func() { close(gate) }, first
}

// The defect: GetTokenByKey's Redis fall-through had no coalescing, so N
// concurrent requests carrying one token became N identical Postgres queries
// -- a cache-stampede amplifier aimed at a database sharing a 2-vCPU box
// with the console.
func TestConcurrentLookupsOfOneTokenHitTheDatabaseOnce(t *testing.T) {
	queries, openGate, firstStarted := setupTokenSingleflightDB(t)
	require.NoError(t, DB.Create(&Token{Key: "sk-herd", Name: "herd"}).Error)
	queries.Store(0)

	const callers = 50
	results := make([]*Token, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); results[0], errs[0] = loadTokenFromDBOnce("sk-herd") }()

	// Only release the gate once the first query is genuinely in flight and
	// the rest have had a moment to park behind it. If the machine is slow
	// enough that they arrive late, this test FAILS on the count -- it does
	// not quietly pass, which is the direction a timing-dependent test has
	// to err in.
	<-firstStarted
	for i := 1; i < callers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i], errs[i] = loadTokenFromDBOnce("sk-herd") }(i)
	}
	time.Sleep(50 * time.Millisecond)
	openGate()
	wg.Wait()

	assert.Equal(t, int64(1), queries.Load(),
		"%d concurrent lookups of one key must reach the database once", callers)
	for i := range callers {
		require.NoError(t, errs[i])
		require.NotNil(t, results[i])
		assert.Equal(t, "herd", results[i].Name)
	}
}

// singleflight hands every waiter the same value. Returning that pointer
// directly would make one *Token shared across goroutines that each believe
// it is theirs, turning a load fix into a data race.
func TestEachCallerGetsItsOwnCopy(t *testing.T) {
	_, openGate, firstStarted := setupTokenSingleflightDB(t)
	require.NoError(t, DB.Create(&Token{Key: "sk-copy", Name: "original"}).Error)

	var a, b *Token
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); a, _ = loadTokenFromDBOnce("sk-copy") }()
	<-firstStarted
	wg.Add(1)
	go func() { defer wg.Done(); b, _ = loadTokenFromDBOnce("sk-copy") }()
	time.Sleep(50 * time.Millisecond)
	openGate()
	wg.Wait()

	require.NotNil(t, a)
	require.NotNil(t, b)
	assert.NotSame(t, a, b, "waiters must not share one *Token")

	a.Name = "mutated by one caller"
	assert.Equal(t, "original", b.Name, "mutating one copy must not affect another")
}

// Different tokens must not queue behind each other.
func TestDifferentKeysAreNotCollapsed(t *testing.T) {
	queries, openGate, firstStarted := setupTokenSingleflightDB(t)
	require.NoError(t, DB.Create(&Token{Key: "sk-a", Name: "a"}).Error)
	require.NoError(t, DB.Create(&Token{Key: "sk-b", Name: "b"}).Error)
	queries.Store(0)

	var wg sync.WaitGroup
	var ta, tb *Token
	wg.Add(1)
	go func() { defer wg.Done(); ta, _ = loadTokenFromDBOnce("sk-a") }()
	<-firstStarted
	wg.Add(1)
	go func() { defer wg.Done(); tb, _ = loadTokenFromDBOnce("sk-b") }()
	time.Sleep(50 * time.Millisecond)
	openGate()
	wg.Wait()

	assert.Equal(t, int64(2), queries.Load(), "two distinct keys must be two queries")
	require.NotNil(t, ta)
	require.NotNil(t, tb)
	assert.Equal(t, "a", ta.Name)
	assert.Equal(t, "b", tb.Name)
}

// A miss must stay a miss for every waiter, and must not be cached as a
// success by one of them.
func TestAMissIsReportedToEveryWaiter(t *testing.T) {
	_, openGate, firstStarted := setupTokenSingleflightDB(t)

	const callers = 10
	errs := make([]error, callers)
	toks := make([]*Token, callers)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); toks[0], errs[0] = loadTokenFromDBOnce("sk-absent") }()
	<-firstStarted
	for i := 1; i < callers; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); toks[i], errs[i] = loadTokenFromDBOnce("sk-absent") }(i)
	}
	time.Sleep(50 * time.Millisecond)
	openGate()
	wg.Wait()

	for i := range callers {
		assert.Error(t, errs[i], "caller %d must see the miss", i)
		assert.Nil(t, toks[i])
	}
}
