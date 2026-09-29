/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

// UNIFYAPI-FORK: the schedule behind service.RunPricingChangeNotice.
//
// A list price changes when a release carrying a new catalog starts serving.
// Nothing else in the process knows that moment, so this wakes on a timer,
// compares the catalog with the last announced baseline, and acts only on a
// difference. On an unchanged catalog a run is one options read, one query for
// enabled models and no writes.

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

type pricingChangeNotifyHandler struct{}

func (pricingChangeNotifyHandler) Type() string { return model.SystemTaskTypePricingChangeNotify }

// Enabled defaults to on; the option PricingChangeNotifyEnabled=false turns it
// off. The first run after enabling records the baseline and sends nothing.
func (pricingChangeNotifyHandler) Enabled() bool { return model.PricingChangeNotifyEnabled() }

// Interval is a check cadence, not a send cadence. Nothing is sent unless the
// catalog differs from what customers were last told, so waking hourly costs a
// few reads and means a release that changed a price is announced the same
// hour it went live rather than up to a day later. The DB lease in the task
// runner keeps multiple masters from sending twice.
func (pricingChangeNotifyHandler) Interval() time.Duration { return time.Hour }

func (pricingChangeNotifyHandler) NewPayload() any { return nil }

func (h pricingChangeNotifyHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := service.RunPricingChangeNotice(time.Now())
	if err != nil {
		common.SysError("pricing change notice: " + err.Error())
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, result, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, result, nil)
}
