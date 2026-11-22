/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// A refused save of the customer price table used to be written anyway.
//
// Found on staging, 2026-11-22. The stored table still named a delisted model,
// an operator changed one multiplier from 1 to 0.9 through PUT /api/option/,
// and the answer was success=false, "拒绝保存客户模型价格". The row had already
// been overwritten: the refusal came from the in-memory apply, which runs after
// the write. The next sync dropped the stale entry and loaded the rest, so the
// price the operator was told had been refused took effect a minute later.
//
// A stale model was the mild case. A refused multiplier (0, or above the
// bound) and unreadable JSON were written the same way, and the loader does
// not repair those: it refuses the whole stored table on every sync, the
// process keeps billing from the last table it loaded, and the next restart
// starts with no customer prices at all.
//
// What these tests hold is the contract, not the mechanism: an error from a
// save means the stored row, the price history and the live table are exactly
// what they were.

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The table staging held: one live contract, and one entry for a model that
// has since left the catalogue.
const storedCustomerPricesWithAStaleEntry = `{"GenAI":{"a-model-we-delisted":0.9,"claude-opus-5":1}}`

// setupStoredCustomerPrices stores a customer price table, loads it the way a
// running server does, and returns a counter of writes to the options table.
// The counter is what proves a refused save left the row where it was: on
// PostgreSQL an UPDATE moves the row even when it writes the same bytes, and
// the options are applied in storage order.
func setupStoredCustomerPrices(t *testing.T, stored string) *int {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&Option{}, &PricingConfigHistory{}))

	previousDB := DB
	previousType := common.MainDatabaseType()
	previousOptions := common.OptionMap
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.OptionMap = map[string]string{}
	ratio_setting.InitRatioSettings()
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.OptionMap = previousOptions
		require.NoError(t, ratio_setting.UpdateGroupModelDiscountByJSONString(`{}`))
	})

	require.NoError(t, DB.Create(&Option{Key: "GroupModelDiscount", Value: stored}).Error)
	loadOptionsFromDatabase()
	return countOptionWrites(t)
}

// countOptionWrites counts every statement that writes the options table from
// here on, on the test's own database handle.
func countOptionWrites(t *testing.T) *int {
	t.Helper()
	writes := 0
	countOptionWrite := func(tx *gorm.DB) {
		if tx.Statement.Table == "options" {
			writes++
		}
	}
	require.NoError(t, DB.Callback().Create().Before("gorm:create").Register("test:count_option_create", countOptionWrite))
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register("test:count_option_update", countOptionWrite))
	require.NoError(t, DB.Callback().Delete().Before("gorm:delete").Register("test:count_option_delete", countOptionWrite))
	return &writes
}

func storedOptionValue(t *testing.T, key string) string {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(map[string]any{"key": key}).First(&option).Error)
	return option.Value
}

func liveCustomerPrice(t *testing.T, group, modelName string) float64 {
	t.Helper()
	ratio, ok := ratio_setting.GetGroupModelDiscount(group, modelName)
	require.True(t, ok, "%s must still hold a negotiated price for %s", group, modelName)
	return ratio
}

func TestARefusedCustomerPriceSaveWritesNothing(t *testing.T) {
	cases := []struct {
		name      string
		submitted string
		names     string
	}{
		{
			// The staging case: the edit itself is fine, the table carries a
			// stale entry that was already stored.
			name:      "an entry for a model with no catalogue price",
			submitted: `{"GenAI":{"a-model-we-delisted":0.9,"claude-opus-5":0.9}}`,
			names:     "a-model-we-delisted",
		},
		{
			name:      "a multiplier of zero",
			submitted: `{"GenAI":{"claude-opus-5":0}}`,
			names:     "claude-opus-5",
		},
		{
			name:      "a multiplier above the sanity bound",
			submitted: `{"GenAI":{"claude-opus-5":10.5}}`,
			names:     "claude-opus-5",
		},
		{
			name:      "a table that is not JSON",
			submitted: `{"GenAI":{"claude-opus-5":0.9}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writes := setupStoredCustomerPrices(t, storedCustomerPricesWithAStaleEntry)
			published := common.OptionMap["GroupModelDiscount"]
			require.InDelta(t, 1.0, liveCustomerPrice(t, "GenAI", "claude-opus-5"), 1e-12)

			err := UpdateOptionAs("GroupModelDiscount", tc.submitted, "operator")

			require.Error(t, err, "the save must be refused")
			if tc.names != "" {
				assert.Contains(t, err.Error(), tc.names, "the refusal must name the entry")
			}
			assert.Equal(t, storedCustomerPricesWithAStaleEntry, storedOptionValue(t, "GroupModelDiscount"),
				"a refused save must leave the stored table as it was")
			assert.Zero(t, *writes, "a refused save must not touch the options table, or the row moves")

			var history int64
			require.NoError(t, DB.Model(&PricingConfigHistory{}).Count(&history).Error)
			assert.Zero(t, history, "a refused save replaced nothing, so there is nothing to record")
			assert.Equal(t, published, common.OptionMap["GroupModelDiscount"],
				"the settings page must keep showing the table that is in force")

			// What the operator saw on staging: the refused price arriving
			// with the next sync.
			loadOptionsFromDatabase()
			assert.InDelta(t, 1.0, liveCustomerPrice(t, "GenAI", "claude-opus-5"), 1e-12,
				"a refused price must not take effect on the next sync")
		})
	}
}

// The refusal must not cost the operator the edit. The same change, without
// the entry that was named, is stored and billed.
func TestTheSameCustomerPriceSaveGoesThroughOnceTheNamedEntryIsRemoved(t *testing.T) {
	setupStoredCustomerPrices(t, storedCustomerPricesWithAStaleEntry)
	submitted := `{"GenAI":{"claude-opus-5":0.9}}`

	require.NoError(t, UpdateOptionAs("GroupModelDiscount", submitted, "operator"))

	assert.Equal(t, submitted, storedOptionValue(t, "GroupModelDiscount"))
	assert.InDelta(t, 0.9, liveCustomerPrice(t, "GenAI", "claude-opus-5"), 1e-12)
	loadOptionsFromDatabase()
	assert.InDelta(t, 0.9, liveCustomerPrice(t, "GenAI", "claude-opus-5"), 1e-12)
}

// A bulk save commits every key in one transaction and applies them
// afterwards, so a refused customer price table took its neighbours with it:
// they were stored, the caller was told the save failed, and whichever keys
// came after the refusal in map order were not applied until the next sync.
func TestARefusedCustomerPriceTableStopsABulkSaveBeforeAnythingIsWritten(t *testing.T) {
	writes := setupStoredCustomerPrices(t, storedCustomerPricesWithAStaleEntry)
	require.NoError(t, DB.Create(&Option{Key: "TopUpLink", Value: "https://example.com/before"}).Error)
	*writes = 0

	err := UpdateOptionsBulkAs(map[string]string{
		"GroupModelDiscount": `{"GenAI":{"a-model-we-delisted":0.9,"claude-opus-5":0.9}}`,
		"TopUpLink":          "https://example.com/after",
	}, "operator")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "a-model-we-delisted")
	assert.Equal(t, storedCustomerPricesWithAStaleEntry, storedOptionValue(t, "GroupModelDiscount"))
	assert.Equal(t, "https://example.com/before", storedOptionValue(t, "TopUpLink"),
		"a bulk save is all or nothing, including when the refusal is a price")
	assert.Zero(t, *writes)
	loadOptionsFromDatabase()
	assert.InDelta(t, 1.0, liveCustomerPrice(t, "GenAI", "claude-opus-5"), 1e-12)
}
