/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// Upstream defaults SQL_MAX_OPEN_CONNS to 1000. Stock Postgres allows 100
// connections, so under load the pool asks for connections the server will
// refuse (SQLSTATE 53300) instead of queueing for one of its own. On staging,
// 2026-10-02, that is what failed at 200 RPS and what lost the charges
// settlement_outbox.go now protects.
//
// When SQL_MAX_OPEN_CONNS is NOT set, the pool is capped at max_connections
// minus the superuser reserve minus postgresPoolHeadroom, which is left for
// everything that is not this pool: psql/admin sessions, a monitoring sampler,
// a migration run from another container. A wait for a pooled connection is
// a delay; a refused connection is an error. When SQL_MAX_OPEN_CONNS IS set it
// is respected, with a warning if it exceeds what the server allows.
//
// A separate LOG_SQL_DSN pool on the SAME server is capped independently, so
// the two together can still exceed the server; set both explicitly then.
const postgresPoolHeadroom = 20

func postgresPoolCeiling(maxConnections, reserved int) int {
	return max(maxConnections-reserved-postgresPoolHeadroom, 10)
}

func applyPostgresPoolCeiling(db *gorm.DB, sqlDB *sql.DB, dsnEnv string) {
	var maxConnStr, reservedStr string
	if err := db.Raw("SHOW max_connections").Scan(&maxConnStr).Error; err != nil {
		common.SysError(fmt.Sprintf("%s: cannot read max_connections, pool left as configured: %v", dsnEnv, err))
		return
	}
	maxConnections, err := strconv.Atoi(maxConnStr)
	if err != nil {
		return
	}
	reserved := 3
	if err := db.Raw("SHOW superuser_reserved_connections").Scan(&reservedStr).Error; err == nil {
		if n, err := strconv.Atoi(reservedStr); err == nil {
			reserved = n
		}
	}
	ceiling := postgresPoolCeiling(maxConnections, reserved)
	if raw := os.Getenv("SQL_MAX_OPEN_CONNS"); raw != "" {
		if configured, err := strconv.Atoi(raw); err == nil && configured > maxConnections-reserved {
			common.SysError(fmt.Sprintf("%s: SQL_MAX_OPEN_CONNS=%d exceeds Postgres max_connections=%d (reserved %d); under load the server will refuse connections",
				dsnEnv, configured, maxConnections, reserved))
		}
		return
	}
	sqlDB.SetMaxOpenConns(ceiling)
	common.SysLog(fmt.Sprintf("%s: SQL_MAX_OPEN_CONNS unset, pool capped at %d (Postgres max_connections=%d, reserved %d, headroom %d)",
		dsnEnv, ceiling, maxConnections, reserved, postgresPoolHeadroom))
}
