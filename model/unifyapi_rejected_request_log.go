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

	"github.com/QuantumNous/new-api/common"
)

// RecordRejectedRequestLog writes the usage-log row for a request refused
// before the relay (middleware/unifyapi_rejected_request_log.go). UNIFYAPI-FORK.
//
// It runs off the request path of a caller who is already being refused, as
// RecordAuthRejection does, and for the same reason it must not be able to
// take the process down: a panic in a goroutine is fatal, and a refusal is
// exactly what happens while the database is unwell.
func RecordRejectedRequestLog(row *Log) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("rejected request log: record panicked: %v", r))
		}
	}()
	if row == nil || LOG_DB == nil {
		return
	}
	// The IP allow list refuses before auth has loaded the user.
	if row.Username == "" && DB != nil {
		if user, err := GetUserCache(row.UserId); err == nil {
			row.Username = user.Username
		}
	}
	if err := createLog(row); err != nil {
		common.SysError("rejected request log: record failed: " + err.Error())
	}
}
