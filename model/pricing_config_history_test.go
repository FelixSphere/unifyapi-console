/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: tests for the billing config history.
//
// The first test reproduces the incident this exists because of. On 2026-08-30
// a seed was written into ModelDiscount over SSM; the map is replace-not-merge,
// so the operator's configured discounts were destroyed rather than merged with,
// and the value existed nowhere afterwards. Everything else here protects the
// property that made that unrecoverable: the OLD value must survive the write.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPricingHistoryDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&PricingConfigHistory{}, &Option{}))

	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMapRWMutex.Unlock()
}

// TestTheDiscountsThatWereLostWouldNowSurvive is the regression test for the
// actual incident: a seed overwrites a configured discount table.
func TestTheDiscountsThatWereLostWouldNowSurvive(t *testing.T) {
	setupPricingHistoryDB(t)

	operatorTable := `{"claude-opus-4-8":0.8,"gpt-4o":0.75}`
	seedTable := `{"claude-opus-4-8":0.085}`

	RecordPricingConfigChange("ModelDiscount", operatorTable, seedTable, "agent-over-ssm", "seed")

	history, err := ListPricingConfigHistory("ModelDiscount", 0)
	require.NoError(t, err)
	require.Len(t, history, 1)

	// The whole point: the operator's table is still here, intact, and can be
	// pasted back.
	assert.Equal(t, operatorTable, history[0].OldValue)
	assert.Equal(t, seedTable, history[0].NewValue)
	assert.Equal(t, "agent-over-ssm", history[0].ChangedBy)
	assert.NotZero(t, history[0].CreatedAt)
}

// TestOnlyBillingKeysAreKept. The history is a recovery tool for money, not a
// second copy of every setting in the system -- some of which hold secrets.
func TestOnlyBillingKeysAreKept(t *testing.T) {
	setupPricingHistoryDB(t)

	RecordPricingConfigChange("StripeApiSecret", "sk_live_old", "sk_live_new", "root", "test")
	RecordPricingConfigChange("ModelDiscount", `{"a":0.5}`, `{"a":0.6}`, "root", "test")

	var total int64
	require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&total).Error)
	assert.EqualValues(t, 1, total, "a payment secret must never be copied into the history table")

	_, err := ListPricingConfigHistory("StripeApiSecret", 0)
	assert.Error(t, err, "asking for a non-billing key must be refused, not silently empty")
}

// TestEveryGuardedKeyIsABillingKey pins the set. Adding one is cheap; forgetting
// one is how the next table gets lost.
func TestEveryGuardedKeyIsABillingKey(t *testing.T) {
	for _, key := range []string{
		"ModelDiscount", "ExtraModelPricing", "ChannelCostRatio", "GroupRatio", "GroupGroupRatio",
	} {
		assert.True(t, IsBillingConfigKey(key), "%s decides what a customer pays and must be guarded", key)
	}
	assert.False(t, IsBillingConfigKey("ModelRatio"),
		"ModelRatio is rebuilt from the catalog, not hand-edited; guarding it would record noise")
}

// TestANoOpSaveIsNotHistory -- opening the pricing page and pressing save must
// not bury the version anyone actually wants.
func TestANoOpSaveIsNotHistory(t *testing.T) {
	setupPricingHistoryDB(t)
	same := `{"gpt-4o":0.75}`
	RecordPricingConfigChange("ModelDiscount", same, same, "root", "test")

	history, err := ListPricingConfigHistory("ModelDiscount", 0)
	require.NoError(t, err)
	assert.Empty(t, history)
}

// TestHistoryIsBoundedButKeepsTheNewest. Unbounded growth would eventually get
// the table truncated by someone, taking the recovery path with it.
func TestHistoryIsBoundedButKeepsTheNewest(t *testing.T) {
	setupPricingHistoryDB(t)
	for i := 0; i < pricingHistoryRetention+15; i++ {
		RecordPricingConfigChange("ModelDiscount",
			`{"gpt-4o":`+itoa(i)+`}`, `{"gpt-4o":`+itoa(i+1)+`}`, "root", "test")
	}

	var total int64
	require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&total).Error)
	assert.LessOrEqual(t, total, int64(pricingHistoryRetention))

	history, err := ListPricingConfigHistory("ModelDiscount", 0)
	require.NoError(t, err)
	require.NotEmpty(t, history)
	assert.Equal(t, `{"gpt-4o":`+itoa(pricingHistoryRetention+15)+`}`, history[0].NewValue,
		"pruning must drop the oldest, never the most recent")
}

// TestScheduledSnapshotCatchesAWriteThatBypassedTheApp. Raw SQL over SSM runs no
// Go, so RecordPricingConfigChange never fires. The scheduled snapshot is the
// only reason such a change is recoverable at all.
func TestScheduledSnapshotCatchesAWriteThatBypassedTheApp(t *testing.T) {
	setupPricingHistoryDB(t)

	common.OptionMapRWMutex.Lock()
	common.OptionMap["ModelDiscount"] = `{"kimi-k3":0.9}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "ModelDiscount")
		common.OptionMapRWMutex.Unlock()
	})

	recorded, err := SnapshotBillingConfig("daily")
	require.NoError(t, err)
	assert.Equal(t, 1, recorded)

	history, err := ListPricingConfigHistory("ModelDiscount", 0)
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, `{"kimi-k3":0.9}`, history[0].OldValue)
	assert.Equal(t, "scheduled-snapshot", history[0].ChangedBy)

	// Running again on an unchanged config must not add a row, or a daily job
	// fills the retention window with duplicates and evicts the real history.
	recorded, err = SnapshotBillingConfig("daily")
	require.NoError(t, err)
	assert.Zero(t, recorded)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}
