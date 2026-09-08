/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestMigrationIsIdempotentOnSQLite runs AutoMigrate twice against a FILE
// database, the way a restart does. A table whose DDL the sqlite migrator
// cannot re-parse ("invalid DDL, unbalanced brackets") passes the first run
// and kills the process on the second, which no in-memory test can see.
func TestMigrationIsIdempotentOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.db")
	models := []interface{}{
		&Channel{}, &Token{}, &User{}, &Tenant{}, &CreditPool{}, &CreditPoolLot{}, &TenantCreditGrant{},
		&CreditPoolReservation{}, &CreditPoolReservationLot{}, &PartnershipProgram{}, &PartnershipCustomer{},
		&PartnershipEnrollment{}, &CreditSupplier{}, &CreditLot{}, &CreditLotUsage{}, &CreditLotEvent{},
		&UserSession{}, &AuthFlow{}, &ExternalIdentityClaim{}, &PasskeyCredential{}, &Option{}, &Redemption{},
		&Ability{}, &Log{}, &Midjourney{}, &TopUp{}, &QuotaData{}, &ReconcileSnapshot{}, &Settlement{},
		&PricingConfigHistory{}, &Task{}, &Model{}, &Vendor{}, &PrefillGroup{}, &Setup{}, &TwoFA{}, &TwoFABackupCode{},
		&Checkin{}, &SubscriptionOrder{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}, &CustomOAuthProvider{},
		&UserOAuthBinding{}, &PerfMetric{}, &SystemInstance{}, &SystemTask{}, &SystemTaskLock{},
	}
	for round := 1; round <= 2; round++ {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		for _, m := range models {
			require.NoErrorf(t, db.AutoMigrate(m), "round %d: %T", round, m)
		}
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	}
}
