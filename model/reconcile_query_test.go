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

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNormalizeChannelBaseURL(t *testing.T) {
	require.Equal(t, "https://console.flatkey.ai/api",
		NormalizeChannelBaseURL(" HTTPS://Console.Flatkey.AI/api/?token=secret#fragment "))
	require.Equal(t, "https://openrouter.ai", NormalizeChannelBaseURL("https://openrouter.ai/"))
	require.Equal(t, "glm-coding-plan", NormalizeChannelBaseURL("glm-coding-plan/"))
}

func TestFetchReconcileUsagePrefersSnapshottedChannelBaseURL(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}, &Channel{}))

	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	DB, LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetMainDatabaseType(previousMainType)
		common.SetLogDatabaseType(previousLogType)
	})

	currentBase := "https://api.flatkey.ai/v1"
	require.NoError(t, db.Create(&Channel{Id: 7, Name: "reseller", BaseURL: &currentBase}).Error)
	for _, log := range []*Log{
		{UserId: 1, Username: "acme", CreatedAt: 1_754_006_400, Type: LogTypeConsume,
			ModelName: "gpt-4o", ChannelId: 7, ChannelBaseURL: "https://openrouter.ai/api/v1", Quota: 10},
		{UserId: 1, Username: "acme", CreatedAt: 1_754_006_500, Type: LogTypeConsume,
			ModelName: "gpt-4o", ChannelId: 7, ChannelBaseURL: "", Quota: 20},
	} {
		require.NoError(t, db.Create(log).Error)
	}

	rows, truncated, err := FetchReconcileUsage(ReconcileQuery{
		StartTimestamp: 1_754_000_000,
		EndTimestamp:   1_755_000_000,
	})
	require.NoError(t, err)
	require.False(t, truncated)
	require.Len(t, rows, 2, "different endpoint snapshots remain separate ledger facts")

	byURL := map[string]UsageRow{}
	for _, row := range rows {
		byURL[row.ChannelBaseURL] = row
	}
	require.EqualValues(t, 10, byURL["https://openrouter.ai/api/v1"].Quota,
		"a later channel edit must not move historical usage")
	require.EqualValues(t, 20, byURL[currentBase].Quota,
		"pre-migration logs use the current channel only as a documented fallback")
}
